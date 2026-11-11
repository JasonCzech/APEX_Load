package storage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func TestValidateImportBatchCredentialSafetyAllowsNilCheckAndUnredactedValues(t *testing.T) {
	if err := validateImportBatchCredentialSafety(nil, false); !errors.Is(err, ErrImportBatchCredentialsUnmanaged) {
		t.Fatalf("nil script error = %v, want ErrImportBatchCredentialsUnmanaged", err)
	}

	script := preservedImportBatchCredentialScript()
	script.Actions = []dsl.Action{{
		Name: "request",
		Type: "request",
		HeaderList: dsl.HeaderList{{
			Name:  "Authorization",
			Value: "Bearer imported-runtime-token",
		}},
	}}
	script.Metadata.Import.Preserved["source_review"] = map[string]any{
		"authorization": "Bearer review-only-token",
	}

	if err := validateImportBatchCredentialSafety(script, true); err != nil {
		t.Fatalf("trusted marked credential error = %v, want nil", err)
	}
	if err := validateImportBatchCredentialSafety(script, false); err != nil {
		t.Fatalf("untrusted marked credential error = %v, want nil", err)
	}
	script.Metadata = nil
	if err := validateImportBatchCredentialSafety(script, false); err != nil {
		t.Fatalf("unmarked credential error = %v, want nil", err)
	}
}

func TestTrustedGenericImportBatchKeepsLiveAndReceiptCredentialsUnredacted(t *testing.T) {
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
	if !strings.Contains(string(liveJSON), canary) || strings.Contains(string(liveJSON), scrub.Redacted) {
		t.Fatalf("live imported script did not preserve unredacted credential: %s", liveJSON)
	}

	var receiptJSON string
	if err := store.db.QueryRow(
		`SELECT scripts_json FROM bruno_native_import_batches WHERE id = ?`,
		created.Receipt.ID,
	).Scan(&receiptJSON); err != nil {
		t.Fatalf("read receipt scripts_json: %v", err)
	}
	if !strings.Contains(receiptJSON, canary) || strings.Contains(receiptJSON, scrub.Redacted) {
		t.Fatalf("receipt snapshot scrubbed imported credential: %s", receiptJSON)
	}

	retry, err := store.GetImportBatchByIdempotencyKey(
		commit.Receipt.ProjectID,
		commit.Receipt.Profile,
		commit.Receipt.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf("GetImportBatchByIdempotencyKey: %v", err)
	}
	createdJSON, err := json.Marshal(created.Scripts[0])
	if err != nil {
		t.Fatalf("marshal created response: %v", err)
	}
	gotRetryJSON, err := json.Marshal(retry.Scripts[0])
	if err != nil {
		t.Fatalf("marshal retry response: %v", err)
	}
	if !bytes.Equal(createdJSON, gotRetryJSON) {
		t.Fatalf("created/retry snapshots differ\ncreated=%s\nretry=%s", createdJSON, gotRetryJSON)
	}
}

func TestTrustedGenericJavaImportBatchPreservesVMCredentialInReceipt(t *testing.T) {
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

	retry, err := store.GetImportBatchByIdempotencyKey(
		commit.Receipt.ProjectID,
		commit.Receipt.Profile,
		commit.Receipt.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf("GetImportBatchByIdempotencyKey: %v", err)
	}
	if got := retry.Scripts[0].Config.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[0] != "-Xmx512m" || got[1] != "-Ddb.password="+canary {
		t.Fatalf("receipt Java VM parameters = %#v", got)
	}
}

func TestTrustedGenericImportBatchPersistsProtectedJavaSourceEnvelopes(t *testing.T) {
	const canary = "protected-java-source-password-canary"
	store := newBrunoImportTestStore(t)
	commit := genericImportBatchFixture(
		"enterprise_native",
		"enterprise-native-commit-v1",
		"trusted-enterprise-source-session",
		"trusted-enterprise-source-sha",
	)
	commit.TrustPreservedExecutableCredentials = true
	script := commit.Scripts[0]
	script.Metadata = preservedImportBatchCredentialScript().Metadata
	vmRow := "-Dservice.password=" + canary
	jdbcDSN := `{"jdbc_url":"jdbc:acme://db.example.test/orders","username":"operator","password":"` +
		canary + `"}`
	encodedVMRow := base64.StdEncoding.EncodeToString([]byte(vmRow))
	encodedJDBC := base64.StdEncoding.EncodeToString([]byte(jdbcDSN))
	script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaVMSourceKey] =
		map[string]any{
			"version":     1,
			"rows_base64": []string{encodedVMRow},
		}
	script.Metadata.Import.Preserved[dsl.ImportPreservedEnterpriseJavaJDBCSourceKey] =
		map[string]any{
			"version": 1,
			"connections": []map[string]any{{
				"driver_base64": "",
				"dsn_base64":    encodedJDBC,
				"reason":        "jdbc_connection_driver_unsupported",
				"source_ref":    "Actions.java line 4",
			}},
		}

	created, err := store.CommitImportBatch(commit)
	if err != nil {
		t.Fatalf("CommitImportBatch: %v", err)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatalf("GetScript: %v", err)
	}
	storedPayload, err := json.Marshal(stored.Metadata.Import.Preserved)
	if err != nil {
		t.Fatalf("marshal stored protected metadata: %v", err)
	}
	if !bytes.Contains(storedPayload, []byte(encodedVMRow)) ||
		!bytes.Contains(storedPayload, []byte(encodedJDBC)) {
		t.Fatalf("durable protected source envelopes lost exact bytes: %s", storedPayload)
	}

	var receiptJSON string
	if err := store.db.QueryRow(
		`SELECT scripts_json FROM bruno_native_import_batches WHERE id = ?`,
		created.Receipt.ID,
	).Scan(&receiptJSON); err != nil {
		t.Fatalf("read receipt scripts_json: %v", err)
	}
	if !strings.Contains(receiptJSON, encodedVMRow) ||
		!strings.Contains(receiptJSON, encodedJDBC) {
		t.Fatalf("receipt snapshot lost Java source envelope: %s", receiptJSON)
	}
}

func TestBrunoAdapterTrustKeepsLiveAndReceiptCredentialsUnredacted(t *testing.T) {
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
	if len(retry.Scripts) != len(result.Scripts) ||
		retry.Scripts[0].ID != result.Scripts[0].ID ||
		retry.Scripts[0].Actions[0].Auth == nil ||
		retry.Scripts[0].Actions[0].Auth.Password != canary {
		t.Fatalf("Bruno receipt retry did not retain exact unredacted credentials: %+v", retry.Scripts)
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
