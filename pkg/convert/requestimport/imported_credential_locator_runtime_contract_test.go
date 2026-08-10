package requestimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

// This file is the cross-package guard for the retained-credential locator
// vocabulary. requestimport publishes script-level locators into
// script.Metadata.Import.Preserved[import_credential_targets]; pkg/engine is the
// only consumer, and it is FAIL-CLOSED: engine.ImportedCredentialValues returns
// an error for any locator field it does not understand, and every runtime entry
// point (engine.New, ReplayScript, NewMultiGroup, RunScriptShakeoutWithSecretResolver,
// and the generator RPC staging boundary) turns that error into a refused run —
// whether or not config.log.redact_run_evidence is on.
//
// So a locator field this package can emit but the engine cannot resolve is not
// a cosmetic mismatch: it blocks the run. The tests below round-trip the locators
// this package actually attaches through the real runtime resolver.

const (
	locatorContractListCanary = "list-parameter-password-canary"
	locatorContractCSVCanary  = "inline-data-password-canary"
)

func credentialLocatorContractScript() *dsl.Script {
	script := validBaseScript()
	script.GeneratedFrom = "test_import"
	script.Parameters = []dsl.Parameter{
		{
			Name:   "login_password",
			Type:   "list",
			Values: []string{locatorContractListCanary},
		},
		{
			// No inline Values: the runtime resolves this one out of the data
			// source column, so that column is what run evidence can leak.
			Name:       "db_password",
			Type:       "file",
			DataSource: "creds",
			Column:     "password",
		},
	}
	script.DataSources = []dsl.DataSource{{
		Name:           "creds",
		Type:           "csv",
		Strategy:       "sequential",
		FirstRowHeader: true,
		InlineData:     "user,password\nalice," + locatorContractCSVCanary + "\n",
	}}
	script.Actions = []dsl.Action{{
		Name:   "login",
		Type:   "POST",
		Method: "POST",
		Target: "/login",
		Body: `{"password":"{{param.login_password}}",` +
			`"db":"{{param.db_password}}"}`,
	}}
	return script
}

func credentialLocatorContractRequirements() []credentialpolicy.Requirement {
	return []credentialpolicy.Requirement{
		{
			ID:      "requirement-list",
			Key:     "password",
			Label:   "password",
			Context: "actions[0].body",
			Value:   "{{param.login_password}}",
			Targets: []credentialpolicy.TargetRef{{
				RequestKey: "candidate:actions[0]",
				Field:      "actions[0].body",
			}},
		},
		{
			ID:      "requirement-data-source",
			Key:     "db_password",
			Label:   "db_password",
			Context: "actions[0].body",
			Value:   "{{param.db_password}}",
			Targets: []credentialpolicy.TargetRef{{
				RequestKey: "candidate:actions[0]",
				Field:      "actions[0].body",
			}},
		},
	}
}

func preservedCredentialTargets(t *testing.T, script *dsl.Script) []credentialpolicy.TargetRef {
	t.Helper()
	if script.Metadata == nil || script.Metadata.Import == nil ||
		script.Metadata.Import.Preserved == nil {
		return nil
	}
	raw, ok := script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialTargetsMetadataKey]
	if !ok || raw == nil {
		return nil
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal preserved credential targets %#v: %v", raw, err)
	}
	var targets []credentialpolicy.TargetRef
	if err := json.Unmarshal(payload, &targets); err != nil {
		t.Fatalf("decode preserved credential targets %s: %v", payload, err)
	}
	return targets
}

// TestPreservedCredentialLocatorsResolveInTheRuntime drives the real exported
// entry point and then asks the runtime to resolve what it published.
func TestPreservedCredentialLocatorsResolveInTheRuntime(t *testing.T) {
	t.Parallel()
	script := credentialLocatorContractScript()

	ReconcileProtectedScriptCredentialSources(
		script,
		credentialLocatorContractRequirements(),
		"test_import",
		"candidate",
	)
	markCredentialPreservingImport(script)

	targets := preservedCredentialTargets(t, script)
	if len(targets) == 0 {
		t.Fatalf("preserved credential targets = %#v, want the source locators published", targets)
	}

	values, err := engine.ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("engine.ImportedCredentialValues(%#v) = %v, want the published locators to resolve",
			targets, err)
	}
	for _, want := range []string{locatorContractListCanary, locatorContractCSVCanary} {
		if !containsFold(values, want) {
			t.Fatalf("runtime credential values = %#v, want %q registered for run-evidence redaction",
				values, want)
		}
	}
}

// TestPublishedCredentialLocatorFieldsAreRuntimeResolvable feeds each published
// locator to the runtime on its own, so a drift in either vocabulary names the
// exact field that broke instead of failing as one opaque aggregate.
func TestPublishedCredentialLocatorFieldsAreRuntimeResolvable(t *testing.T) {
	t.Parallel()
	published := credentialLocatorContractScript()
	ReconcileProtectedScriptCredentialSources(
		published,
		credentialLocatorContractRequirements(),
		"test_import",
		"candidate",
	)
	if _, err := ProjectScriptCredentials(published, ScriptCredentialProjectionOptions{
		SourceProfile: "test_import",
		CandidateKey:  "candidate",
		NamePrefix:    "TEST_IMPORT",
	}); err != nil {
		t.Fatalf("ProjectScriptCredentials: %v", err)
	}
	markCredentialPreservingImport(published)

	targets := preservedCredentialTargets(t, published)
	wantFields := []string{"parameters[0].values", "data_sources[0].inline_data"}
	fields := make([]string, 0, len(targets))
	for _, target := range targets {
		fields = append(fields, target.Field)
	}
	for _, want := range wantFields {
		if !containsFold(fields, want) {
			t.Fatalf("published locator fields = %#v, want a %q locator", fields, want)
		}
	}

	for _, target := range targets {
		t.Run(target.Field, func(t *testing.T) {
			isolated := credentialLocatorContractScript()
			ReconcileProtectedScriptCredentialSources(
				isolated,
				credentialLocatorContractRequirements(),
				"test_import",
				"candidate",
			)
			markCredentialPreservingImport(isolated)
			isolated.Metadata.Import.Preserved[dsl.ImportPreservedCredentialTargetsMetadataKey] =
				[]credentialpolicy.TargetRef{target}

			if _, err := engine.ImportedCredentialValues(isolated); err != nil {
				t.Fatalf("engine.ImportedCredentialValues(locator %#v) = %v, "+
					"want a locator vocabulary the runtime resolves", target, err)
			}
		})
	}
}

// A locator the runtime cannot resolve must stay loud. This pins the fail-closed
// half of the contract so a later "make the error go away" change cannot turn an
// unresolvable locator into a silent no-op.
func TestUnknownCredentialLocatorFieldStillFailsClosed(t *testing.T) {
	t.Parallel()
	script := credentialLocatorContractScript()
	markCredentialPreservingImport(script)
	script.Metadata.Import.Preserved[dsl.ImportPreservedCredentialTargetsMetadataKey] =
		[]credentialpolicy.TargetRef{{Field: "config.not_a_runtime_field"}}

	_, err := engine.ImportedCredentialValues(script)
	if err == nil || !strings.Contains(err.Error(), "unsupported executable config field") {
		t.Fatalf("engine.ImportedCredentialValues = %v, want the unsupported-field failure", err)
	}
}
