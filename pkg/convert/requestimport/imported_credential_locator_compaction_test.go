package requestimport

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// ReconcileProtectedScriptCredentialSources is per-candidate: an import calls it
// once per credential candidate against the same script, and every call compacts
// script.Parameters and script.DataSources before publishing locators for its own
// candidate. Locators published by an EARLIER candidate are merged forward
// verbatim, so a positional locator has to stay attached to the element it was
// published for across every later compaction. If it does not, the runtime either
// refuses the run (index now past the end) or registers whatever unrelated element
// slid into that slot — which both loses the credential's redaction coverage and
// turns an arbitrary value into an exact-match redaction pattern.
//
// The single-candidate guards in imported_credential_locator_runtime_contract_test.go
// cannot see this: nothing has compacted anything by the time they resolve.
const (
	compactionCredentialCanary = "r11c-live-canary"
	compactionUnrelatedValue   = "25"
	compactionDataSourceCanary = "inline-compaction-canary"
)

func compactionParameterScript() *dsl.Script {
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Parameters = []dsl.Parameter{
		{Name: "tmp_token", Type: "list", Values: []string{"scratch-token"}},
		{Name: "login_password", Type: "list", Values: []string{compactionCredentialCanary}},
	}
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		Body:   `{"password":"{{param.login_password}}"}`,
	}}
	return script
}

func compactionDataSourceScript() *dsl.Script {
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Parameters = []dsl.Parameter{
		{Name: "db_password", Type: "file", DataSource: "creds", Column: "password"},
		{Name: "tmp_lookup", Type: "file", DataSource: "scratch", Column: "token"},
	}
	script.DataSources = []dsl.DataSource{
		{
			Name:           "warmup",
			Type:           "csv",
			Strategy:       "sequential",
			FirstRowHeader: true,
			InlineData:     "id\n1\n",
		},
		{
			Name:           "scratch",
			Type:           "csv",
			Strategy:       "sequential",
			FirstRowHeader: true,
			InlineData:     "token\nabc\n",
		},
		{
			Name:           "creds",
			Type:           "csv",
			Strategy:       "sequential",
			FirstRowHeader: true,
			InlineData:     "user,password\nalice," + compactionDataSourceCanary + "\n",
		},
	}
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		Body:   `{"password":"{{param.db_password}}"}`,
	}}
	return script
}

func compactionRequirements(id, value, candidateKey string) []credentialpolicy.Requirement {
	return []credentialpolicy.Requirement{{
		ID:      id,
		Key:     "password",
		Label:   "password",
		Context: "actions[0].body",
		Value:   value,
		Targets: []credentialpolicy.TargetRef{{
			RequestKey: candidateKey,
			Field:      "actions[0].body",
		}},
	}}
}

// assertLocatorAddresses proves the structural half directly: the locator in the
// given family must still index the element it was published for. The runtime
// half alone cannot prove this — an action body whose JSON key is credential
// shaped registers its {{param.*}} value by name as well, so a drifted locator
// can look harmless in the resolved value set while covering nothing.
func assertLocatorAddresses(t *testing.T, script *dsl.Script, prefix, wantName string) {
	t.Helper()
	found := false
	for _, target := range preservedCredentialTargets(t, script) {
		locatorPrefix, suffix, positional := scriptCredentialLocatorFamily(target.Field)
		if !positional || locatorPrefix != prefix {
			continue
		}
		index, ok := scriptPositionalLocatorIndex(target.Field, locatorPrefix, suffix)
		if !ok {
			t.Fatalf("locator %q is not parseable", target.Field)
		}
		found = true
		var name string
		switch prefix {
		case scriptParameterValuesLocatorPrefix:
			if index >= len(script.Parameters) {
				t.Fatalf("locator %q addresses parameter %d of %d",
					target.Field, index, len(script.Parameters))
			}
			name = script.Parameters[index].Name
		default:
			if index >= len(script.DataSources) {
				t.Fatalf("locator %q addresses data source %d of %d",
					target.Field, index, len(script.DataSources))
			}
			name = script.DataSources[index].Name
		}
		if name != wantName {
			t.Fatalf("locator %q addresses %q, want the credential-supplying %q it was "+
				"published for", target.Field, name, wantName)
		}
	}
	if !found {
		t.Fatalf("preserved locators %#v carry no %q locator",
			preservedCredentialTargets(t, script), prefix)
	}
}

func resolveCompactionCredentialValues(t *testing.T, script *dsl.Script) []string {
	t.Helper()
	values, err := engine.ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("engine.ImportedCredentialValues(locators %#v) = %v, want the retained "+
			"credential locators to still resolve after compaction",
			preservedCredentialTargets(t, script), err)
	}
	return values
}

// A later candidate that deletes an earlier parameter must not strand the first
// candidate's locator past the end of the compacted slice: that locator is what
// the fail-closed runtime gate resolves, so a stale index refuses the run.
func TestPreservedParameterLocatorSurvivesLaterCandidateCompaction(t *testing.T) {
	t.Parallel()
	script := compactionParameterScript()

	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("credential", "{{param.login_password}}", "candA"),
		"test_import",
		"candA",
	)
	markCredentialPreservingImport(script)
	if values := resolveCompactionCredentialValues(t, script); !containsFold(values, compactionCredentialCanary) {
		t.Fatalf("runtime credential values after the first candidate = %#v, want %q registered",
			values, compactionCredentialCanary)
	}

	// A second candidate of the same import. Its own source parameter is not
	// referenced anywhere else, so reconciliation removes it and compacts
	// script.Parameters underneath the locator published above.
	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("scratch", "{{param.tmp_token}}", "candB"),
		"test_import",
		"candB",
	)

	values := resolveCompactionCredentialValues(t, script)
	if !containsFold(values, compactionCredentialCanary) {
		t.Fatalf("runtime credential values after the second candidate = %#v, want %q "+
			"still registered for run-evidence redaction", values, compactionCredentialCanary)
	}
}

// The in-range half of the same drift: the stale index still addresses something,
// so the runtime silently registers an unrelated parameter's value as a credential
// while the credential it was published for goes unmasked.
func TestPreservedParameterLocatorDoesNotDriftOntoAnUnrelatedParameter(t *testing.T) {
	t.Parallel()
	script := compactionParameterScript()
	script.Parameters = append(script.Parameters, dsl.Parameter{
		Name:   "page_size",
		Type:   "list",
		Values: []string{compactionUnrelatedValue},
	})

	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("credential", "{{param.login_password}}", "candA"),
		"test_import",
		"candA",
	)
	markCredentialPreservingImport(script)
	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("scratch", "{{param.tmp_token}}", "candB"),
		"test_import",
		"candB",
	)

	assertLocatorAddresses(t, script, scriptParameterValuesLocatorPrefix, "login_password")

	values := resolveCompactionCredentialValues(t, script)
	if !containsFold(values, compactionCredentialCanary) {
		t.Fatalf("runtime credential values = %#v, want %q still registered",
			values, compactionCredentialCanary)
	}
	if containsFold(values, compactionUnrelatedValue) {
		t.Fatalf("runtime credential values = %#v, want the unrelated parameter value %q NOT "+
			"registered: an exact-match redaction pattern that short would mangle every log "+
			"line, snapshot and report containing it", values, compactionUnrelatedValue)
	}
}

// Data-source locators drift the same way: removeCredentialDataSourceColumns
// compacts script.DataSources for the second candidate, moving the source the
// first candidate's locator addressed.
func TestPreservedDataSourceLocatorSurvivesLaterCandidateCompaction(t *testing.T) {
	t.Parallel()
	script := compactionDataSourceScript()

	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("credential", "{{param.db_password}}", "candA"),
		"test_import",
		"candA",
	)
	markCredentialPreservingImport(script)
	if values := resolveCompactionCredentialValues(t, script); !containsFold(values, compactionDataSourceCanary) {
		t.Fatalf("runtime credential values after the first candidate = %#v, want %q registered",
			values, compactionDataSourceCanary)
	}

	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("scratch", "{{param.tmp_lookup}}", "candB"),
		"test_import",
		"candB",
	)

	assertLocatorAddresses(t, script, scriptDataSourceLocatorPrefix, "creds")

	values := resolveCompactionCredentialValues(t, script)
	if !containsFold(values, compactionDataSourceCanary) {
		t.Fatalf("runtime credential values after the second candidate = %#v, want %q "+
			"still registered for run-evidence redaction", values, compactionDataSourceCanary)
	}
}

// The other half of the contract: when the pass deletes the element a locator was
// published for, the locator must go with it. Leaving it behind is what strands a
// stale index — and the deletion already took the executable value with it, so
// there is nothing left at that address to mask.
func TestPreservedLocatorForADeletedParameterIsDropped(t *testing.T) {
	t.Parallel()
	script := compactionParameterScript()

	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("credential", "{{param.login_password}}", "candA"),
		"test_import",
		"candA",
	)
	markCredentialPreservingImport(script)
	assertLocatorAddresses(t, script, scriptParameterValuesLocatorPrefix, "login_password")

	// The projection pass that governs the credential-bearing surface replaces the
	// last {{param.*}} reference with the project-secret reference. The next
	// candidate's reconciliation then finds the parameter unreferenced and removes
	// it — values blanked and all — which is what makes dropping its locator safe.
	script.Actions[0].Body = `{"password":"{{secret.TEST_IMPORT_PASSWORD}}"}`

	ReconcileProtectedScriptCredentialSources(
		script,
		compactionRequirements("consumed", "{{param.login_password}}", "candB"),
		"test_import",
		"candB",
	)

	for _, parameter := range script.Parameters {
		if parameter.Name == "login_password" {
			t.Fatalf("script parameters = %#v, want the consumed credential parameter removed "+
				"so the drop branch is what is under test", script.Parameters)
		}
	}
	for _, target := range preservedCredentialTargets(t, script) {
		if _, _, positional := scriptCredentialLocatorFamily(target.Field); positional {
			t.Fatalf("preserved locator %q survived the deletion of the parameter it "+
				"addressed", target.Field)
		}
	}
	if _, err := engine.ImportedCredentialValues(script); err != nil {
		t.Fatalf("engine.ImportedCredentialValues = %v, want no refused run after the "+
			"addressed parameter was removed", err)
	}
}
