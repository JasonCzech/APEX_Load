package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scriptdisclosure"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestValidateImportBatchCredentialSafetyAllowsTrustedMarkedImportedRuntimeValue(t *testing.T) {
	script := preservedImportBatchCredentialScript()
	script.Actions = []dsl.Action{{
		Name: "request",
		Type: "request",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer imported-runtime-token",
		}},
	}}

	if err := validateImportBatchCredentialSafety(script, true); err != nil {
		t.Fatalf("marked imported runtime credential: %v", err)
	}
}

func TestValidateImportBatchCredentialSafetyRejectsUntrustedMarkedImportedRuntimeValue(t *testing.T) {
	script := preservedImportBatchCredentialScript()
	script.Actions = []dsl.Action{{
		Name: "request",
		Type: "request",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer imported-runtime-token",
		}},
	}}

	err := validateImportBatchCredentialSafety(script, false)
	if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("untrusted marked credential error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}
}

func TestValidateImportBatchCredentialSafetyRejectsTrustedUnmarkedRuntimeValue(t *testing.T) {
	script := preservedImportBatchCredentialScript()
	script.Metadata = nil
	script.Actions = []dsl.Action{{
		Name: "request",
		Type: "request",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer trusted-context-alone-is-insufficient",
		}},
	}}

	err := validateImportBatchCredentialSafety(script, true)
	if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("trusted unmarked credential error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}
}

func TestValidateImportBatchCredentialSafetyStillRejectsMarkedImportReviewDisclosure(t *testing.T) {
	script := preservedImportBatchCredentialScript()
	script.Metadata.Import.Preserved["source_review"] = map[string]any{
		"authorization": "Bearer review-only-token",
	}

	err := validateImportBatchCredentialSafety(script, true)
	if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("review disclosure error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}
}

func TestCommitImportBatchRejectsForgedPreservationMarkerWithoutTrustedContext(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"untrusted-preserved-credential-session",
		"untrusted-preserved-credential-sha",
	)
	commit.Scripts[0].Metadata = preservedImportBatchCredentialScript().Metadata
	commit.Scripts[0].Actions[0].HeaderList = dsl.HeaderList{{
		Name:  "Authorization",
		Value: "Bearer forged-preservation-canary",
	}}

	_, err := store.CommitImportBatch(commit)
	if !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("CommitImportBatch error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}
	assertGenericImportBatchTablesEmpty(t, store)
}

func TestTrustedGenericImportBatchKeepsLiveCredentialsButSanitizesReceipt(t *testing.T) {
	const canary = "trusted-generic-import-password-canary-7f4c"
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"postman_native",
		"postman-native-commit-v1",
		"trusted-preserved-credential-session",
		"trusted-preserved-credential-sha",
	)
	commit.TrustPreservedExecutableCredentials = true
	script := commit.Scripts[0]
	script.Metadata = preservedImportBatchCredentialScript().Metadata
	script.Config.BaseURL = "https://base.example.test/api"
	script.Config.HTTP.RequestTimeoutMs = 43_210
	script.Config.HTTP.Auth = dsl.AuthConfig{Type: "basic", Username: "operator", Password: canary}
	script.Parameters = []dsl.Parameter{
		{Name: "password", Type: "list", Values: []string{canary}},
		{Name: "region", Type: "list", Values: []string{"us-east-safe"}},
	}
	action := &script.Actions[0]
	action.Target = "https://operator:" + canary + "@example.test/login?access_token=" + canary
	action.HeaderList = dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + canary}}
	action.HeaderList = append(action.HeaderList,
		dsl.HeaderEntry{Name: "X-Flow-ID", Value: "checkout-safe"},
		dsl.HeaderEntry{Name: "x-flow-id", Value: "checkout-safe-duplicate"},
	)
	action.Headers = map[string]string{
		"Authorization": "Bearer " + canary,
		"X-Flow-ID":     "checkout-safe-duplicate",
	}
	action.Auth = &dsl.AuthConfig{Type: "basic", Username: "operator", Password: canary}
	action.Body = `{"password":"` + canary + `","message":"keep-me"}`
	action.Selector = canary
	action.ObjectRef = canary
	action.SOAP = &dsl.SOAPActionConfig{Action: canary, Operation: canary, BodyMode: "literal"}
	action.Assertions = []dsl.Assertion{{Type: "status", Expected: float64(201), Value: "created"}}
	action.Extractors = []dsl.Extractor{{
		Name: "order_id", Source: "body", Type: "jsonpath", Expression: "$.id",
	}}
	script.Actions = append(script.Actions, dsl.Action{
		Name: "Loop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			ForEach: &dsl.ForEachLoop{Source: canary, Var: canary},
			Steps: []dsl.Action{{
				Name:   "Nested",
				Type:   "request",
				Target: canary,
			}},
		},
	})

	created, err := store.CommitImportBatch(commit)
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	if !created.Created || created.Scripts[0].Actions[0].Auth.Password != canary {
		t.Fatalf("created result did not retain executable credential: %+v", created)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	liveJSON, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal live script: %v", err)
	}
	if !strings.Contains(string(liveJSON), canary) {
		t.Fatal("live imported script lost its executable credential")
	}

	var receiptJSON string
	if err := store.db.QueryRow(
		`SELECT scripts_json FROM bruno_native_import_batches WHERE id = ?`,
		created.Receipt.ID,
	).Scan(&receiptJSON); err != nil {
		t.Fatalf("read receipt scripts_json: %v", err)
	}
	if strings.Contains(receiptJSON, canary) || !strings.Contains(receiptJSON, scrub.Redacted) {
		t.Fatalf("receipt snapshot was not disclosure-safe: %s", receiptJSON)
	}

	retry, err := store.GetImportBatchByIdempotencyKey(
		commit.Receipt.ProjectID,
		commit.Receipt.Profile,
		commit.Receipt.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf("GetImportBatchByIdempotencyKey: %v", err)
	}
	retryJSON, err := json.Marshal(retry.Scripts)
	if err != nil {
		t.Fatalf("marshal retry scripts: %v", err)
	}
	if strings.Contains(string(retryJSON), canary) ||
		len(retry.Scripts) != len(commit.Scripts) ||
		retry.Scripts[0].ID != commit.Scripts[0].ID ||
		retry.Scripts[1].ID != commit.Scripts[1].ID {
		t.Fatalf("receipt retry did not preserve sanitized identity/order: %s", retryJSON)
	}
	retryScript := retry.Scripts[0]
	if retryScript.Config.BaseURL != script.Config.BaseURL ||
		retryScript.Config.HTTP.RequestTimeoutMs != script.Config.HTTP.RequestTimeoutMs ||
		len(retryScript.Parameters) != 2 ||
		retryScript.Parameters[0].Values[0] != scrub.Redacted ||
		retryScript.Parameters[1].Values[0] != "us-east-safe" {
		t.Fatalf("receipt retry changed nonsecret config/parameters: %+v", retryScript)
	}
	retryAction := retryScript.Actions[0]
	if retryAction.Headers["X-Flow-ID"] != "checkout-safe-duplicate" ||
		len(retryAction.HeaderList) != 3 ||
		retryAction.HeaderList[1].Name != "X-Flow-ID" ||
		retryAction.HeaderList[2].Name != "x-flow-id" ||
		len(retryAction.Assertions) != 1 ||
		len(retryAction.Extractors) != 1 ||
		retryAction.Extractors[0].Expression != "$.id" ||
		!strings.Contains(retryAction.Body, `"message":"keep-me"`) {
		t.Fatalf("receipt retry changed nonsecret request semantics: %+v", retryAction)
	}
	wantRetry, err := scriptdisclosure.Project(created.Scripts[0])
	if err != nil {
		t.Fatalf("project created response: %v", err)
	}
	wantRetryJSON, err := json.Marshal(wantRetry)
	if err != nil {
		t.Fatalf("marshal projected created response: %v", err)
	}
	gotRetryJSON, err := json.Marshal(retry.Scripts[0])
	if err != nil {
		t.Fatalf("marshal retry response: %v", err)
	}
	if !bytes.Equal(wantRetryJSON, gotRetryJSON) {
		t.Fatalf("created/retry disclosure projections differ\ncreated=%s\nretry=%s", wantRetryJSON, retryJSON)
	}
}

func TestTrustedGenericJavaImportBatchMasksVMCredentialInReceipt(t *testing.T) {
	const canary = "java-vm-receipt-password-canary-4d8f"
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"java_native",
		"java-native-commit-v1",
		"trusted-java-vm-credential-session",
		"trusted-java-vm-credential-sha",
	)
	commit.TrustPreservedExecutableCredentials = true
	script := commit.Scripts[0]
	script.Protocol = "java_vuser"
	script.Metadata = preservedImportBatchCredentialScript().Metadata
	script.Config.JavaVuser.Enabled = true
	script.Config.JavaVuser.AdditionalVMParameters = []string{
		"-Xmx512m",
		"-Ddb.password=" + canary,
	}

	created, err := store.CommitImportBatch(commit)
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	if got := created.Scripts[0].Config.JavaVuser.AdditionalVMParameters[1]; got != "-Ddb.password="+canary {
		t.Fatalf("live Java VM credential = %q, want exact imported value", got)
	}

	var receiptJSON string
	if err := store.db.QueryRow(
		`SELECT scripts_json FROM bruno_native_import_batches WHERE id = ?`,
		created.Receipt.ID,
	).Scan(&receiptJSON); err != nil {
		t.Fatalf("read receipt scripts_json: %v", err)
	}
	if strings.Contains(receiptJSON, canary) {
		t.Fatalf("receipt snapshot disclosed Java VM credential: %s", receiptJSON)
	}

	retry, err := store.GetImportBatchByIdempotencyKey(
		commit.Receipt.ProjectID,
		commit.Receipt.Profile,
		commit.Receipt.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf("GetImportBatchByIdempotencyKey: %v", err)
	}
	if got := retry.Scripts[0].Config.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[0] != "-Xmx512m" || got[1] != scrub.Redacted {
		t.Fatalf("receipt Java VM parameters = %#v", got)
	}
}

func TestBrunoAdapterTrustKeepsLiveCredentialsButSanitizesReceipt(t *testing.T) {
	const canary = "trusted-bruno-import-password-canary-2c91"
	store := newBrunoImportTestStore(t)
	commit := brunoNativeImportCommitFixture(
		"trusted-bruno-credential-session",
		"trusted-bruno-credential-sha",
	)
	script := commit.Scripts[0]
	script.Metadata = preservedImportBatchCredentialScript().Metadata
	script.Actions[0].Auth = &dsl.AuthConfig{
		Type: "basic", Username: "operator", Password: canary,
	}

	result, err := store.CommitBrunoNativeImport(commit)
	if err != nil {
		t.Fatalf("CommitBrunoNativeImport: %v", err)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	if stored.Actions[0].Auth == nil || stored.Actions[0].Auth.Password != canary {
		t.Fatalf("live Bruno credential = %+v, want retained", stored.Actions[0].Auth)
	}
	retry, err := store.GetBrunoNativeImportByIdempotencyKey(
		commit.Batch.IdempotencyKey,
		commit.Batch.ProjectID,
	)
	if err != nil {
		t.Fatalf("GetBrunoNativeImportByIdempotencyKey: %v", err)
	}
	retryJSON, err := json.Marshal(retry.Scripts)
	if err != nil {
		t.Fatalf("marshal Bruno retry: %v", err)
	}
	if strings.Contains(string(retryJSON), canary) ||
		len(retry.Scripts) != len(result.Scripts) ||
		retry.Scripts[0].ID != result.Scripts[0].ID {
		t.Fatalf("Bruno receipt retry was not disclosure-safe and ordered: %s", retryJSON)
	}
}

func preservedImportBatchCredentialScript() *dsl.Script {
	return &dsl.Script{
		Name:     "Imported",
		Protocol: "http",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "test",
			Preserved: map[string]any{
				dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
			},
		}},
	}
}
