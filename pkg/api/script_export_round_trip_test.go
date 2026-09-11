package api

// Round-trip regression for the durable/export credential contract:
//
//   - the DURABLE row is the executable script (never the masked projection), and
//   - every byte written to disk or handed to source control is re-importable
//     into a script that still runs.
//
// Imported source-review evidence is part of the authoring artifact and stays
// exact through persistence, export, and re-import.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

const (
	roundTripAuthorization = "Bearer round-trip-bearer-9f2a4c1b8e7d"
	roundTripProxyUser     = "round-trip-proxy-user"
	roundTripProxyPassword = "round-trip-proxy-password"
	roundTripTablePassword = "round-trip-table-password"
	roundTripReviewSecret  = "round-trip-review-only-secret"
)

func importedRoundTripScript(id string) *dsl.Script {
	script := &dsl.Script{
		ID:        id,
		Name:      "Round trip import",
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: "project-a",
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.test",
			HTTP: dsl.HTTPConfig{
				ProxyMode: "none",
				Proxy: dsl.ProxyConfig{
					Username: roundTripProxyUser,
					Password: roundTripProxyPassword,
				},
			},
		},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "test",
			Preserved: map[string]any{
				"source_review": map[string]any{
					"authorization": "Bearer " + roundTripReviewSecret,
				},
			},
		}},
		DataSources: []dsl.DataSource{{
			Name:           "credentials",
			Type:           "csv",
			InlineData:     "username,password\nalice," + roundTripTablePassword + "\n",
			Strategy:       "sequential",
			FirstRowHeader: true,
		}},
		Actions: []dsl.Action{{
			Name:    "Request",
			Type:    "request",
			Method:  http.MethodGet,
			Target:  "/",
			Headers: map[string]string{"Authorization": roundTripAuthorization},
		}},
	}
	return script
}

// assertRoundTripScriptIsRunnable checks the three properties that make a
// stored/exported copy usable: exact credential bytes, no masking sentinel in
// an executable position, and a clean stored-execution validation with no
// review gate.
func assertRoundTripScriptIsRunnable(t *testing.T, label string, script *dsl.Script) {
	t.Helper()
	if script == nil {
		t.Fatalf("%s: script is nil", label)
	}
	if got := script.Actions[0].Headers["Authorization"]; got != roundTripAuthorization {
		t.Fatalf("%s: Authorization = %q, want %q", label, got, roundTripAuthorization)
	}
	if got := script.Config.HTTP.Proxy.Password; got != roundTripProxyPassword {
		t.Fatalf("%s: proxy password = %q, want %q", label, got, roundTripProxyPassword)
	}
	if got := script.Config.HTTP.Proxy.Username; got != roundTripProxyUser {
		t.Fatalf("%s: proxy username = %q, want %q", label, got, roundTripProxyUser)
	}
	wantTable := "username,password\nalice," + roundTripTablePassword + "\n"
	if got := script.DataSources[0].InlineData; got != wantTable {
		t.Fatalf("%s: credential table = %q, want %q", label, got, wantTable)
	}
	if issues := validateStoredExecutionScript(script); len(issues) > 0 {
		t.Fatalf("%s: stored execution validation = %v", label, issues)
	}
	if err := replayReviewGateError(script); err != nil {
		t.Fatalf("%s: script is review-gated and cannot run: %v", label, err)
	}
}

// assertRoundTripBytesAreRunnable asserts the on-disk/source-control envelope
// carries the exact imported executable and review content.
func assertRoundTripBytesAreRunnable(t *testing.T, label string, data []byte) *dsl.Script {
	t.Helper()
	if !strings.Contains(string(data), roundTripReviewSecret) {
		t.Fatalf("%s: envelope omitted imported review evidence: %s", label, data)
	}
	var envelope struct {
		Script *dsl.Script `json:"script"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("%s: envelope is not a script envelope: %v", label, err)
	}
	assertRoundTripScriptIsRunnable(t, label, envelope.Script)
	return envelope.Script
}

// portableRoundTripForm drops the fields a new durable row legitimately
// reassigns so two copies can be compared byte for byte.
func portableRoundTripForm(t *testing.T, script *dsl.Script) string {
	t.Helper()
	normalized := cloneScriptDefinition(script)
	if normalized == nil {
		t.Fatal("failed to clone script for comparison")
	}
	normalized.ID = ""
	normalized.RefKey = ""
	normalized.CreatedAt = time.Time{}
	normalized.UpdatedAt = time.Time{}
	payload, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestImportedScriptExportDownloadAndReimportStayRunnable(t *testing.T) {
	exportDir := t.TempDir()
	t.Setenv("APEXLOAD_EXPORT_DIR", exportDir)
	server := NewServer(ServerConfig{})

	// Import: the authenticated importer commit's durable entry point.
	imported, created, err := server.runner.CreateImportedScriptDurableReportingReplay(
		importedRoundTripScript("round-trip-import"),
		"test",
	)
	if err != nil {
		t.Fatalf("import commit: %v", err)
	}
	if !created {
		t.Fatal("import unexpectedly resolved as an idempotent replay")
	}

	// Persist + read back: the durable row is the runnable script.
	durable, ok := server.runner.GetScript(imported.ID)
	if !ok || durable == nil {
		t.Fatal("imported script was not persisted")
	}
	assertRoundTripScriptIsRunnable(t, "durable row", durable.Script)
	storedPayload, err := json.Marshal(durable.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(storedPayload), roundTripReviewSecret) {
		t.Fatalf("durable row omitted imported review evidence: %s", storedPayload)
	}

	// Export to disk.
	exportReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/"+durable.ID+"/export",
		strings.NewReader(`{"filename":"round-trip.json"}`),
	)
	exportReq.Header.Set("Content-Type", "application/json")
	exportReq.SetPathValue("id", durable.ID)
	exportResponse := httptest.NewRecorder()
	server.handleExportScript(exportResponse, exportReq)
	if exportResponse.Code != http.StatusOK {
		t.Fatalf("export status = %d body=%s", exportResponse.Code, exportResponse.Body.String())
	}
	exported, err := os.ReadFile(filepath.Join(exportDir, "round-trip.json"))
	if err != nil {
		t.Fatal(err)
	}
	reimportCandidate := assertRoundTripBytesAreRunnable(t, "export file", exported)

	// Download over HTTP carries the same contract.
	downloadReq := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/scripts/"+durable.ID+"/download",
		nil,
	)
	downloadReq.SetPathValue("id", durable.ID)
	downloadResponse := httptest.NewRecorder()
	server.handleDownloadScript(downloadResponse, downloadReq)
	if downloadResponse.Code != http.StatusOK {
		t.Fatalf("download status = %d body=%s", downloadResponse.Code, downloadResponse.Body.String())
	}
	assertRoundTripBytesAreRunnable(t, "download body", downloadResponse.Body.Bytes())

	// Source control gets the same bytes, so a pushed file is importable too.
	envelope, _, err := githubScriptEnvelope(durable.Script)
	if err != nil {
		t.Fatalf("github envelope: %v", err)
	}
	assertRoundTripBytesAreRunnable(t, "github envelope", envelope)

	// Re-import the exported file into a clean server.
	reimportServer := NewServer(ServerConfig{})
	reimportCandidate.ID = "round-trip-reimport"
	reimported, _, err := reimportServer.runner.CreateImportedScriptDurableReportingReplay(
		reimportCandidate,
		"test",
	)
	if err != nil {
		t.Fatalf("re-import exported script: %v", err)
	}
	assertRoundTripScriptIsRunnable(t, "re-imported script", reimported.Script)
	if got, want := portableRoundTripForm(t, reimported.Script), portableRoundTripForm(t, durable.Script); got != want {
		t.Fatalf("re-imported script is not byte-identical to the original durable row\n got: %s\nwant: %s", got, want)
	}

	view := maskStoredScript(reimported)
	if view == nil || view.Script == nil {
		t.Fatal("stored-script response projection returned nothing")
	}
	if got := view.Script.Actions[0].Headers["Authorization"]; got != roundTripAuthorization {
		t.Fatalf("read projection changed Authorization: %q", got)
	}
	if got := view.Script.Config.HTTP.Proxy.Password; got != roundTripProxyPassword {
		t.Fatalf("read projection changed the proxy password: %q", got)
	}
}

// TestOrdinaryDurableWriteKeepsSentinelFreeProjectedValues covers the durable
// half of the same contract on the ordinary (non-import) write path. The
// logical-secret binding gate only sees [REDACTED] sentinels, so a projection
// that rewrites an executable value WITHOUT one — a redacted XML <messageId>
// becomes {{fn.uuid()}} — used to pass the gate and then overwrite the stored
// value with the projection.
func TestOrdinaryDurableWriteKeepsSentinelFreeProjectedValues(t *testing.T) {
	const correlationID = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9." +
		"eyJzdWIiOiIxMjM0NTY3ODkwIn0." +
		"dBjftJeZ4CVPmB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	envelope := "<Envelope><messageId>" + correlationID + "</messageId></Envelope>"

	script := &dsl.Script{
		ID: "sentinel-free-projection", Name: "Sentinel free projection",
		Version: "1.0.0", Protocol: "http", ProjectID: "project-a",
		Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/",
			Params: map[string]any{"soap_envelope": envelope},
		}},
	}

	// Precondition: the response projection really does rewrite this value
	// without leaving a sentinel, so the binding gate cannot catch it.
	projected := maskScriptSecrets(cloneScriptDefinition(script))
	if projected == nil {
		t.Fatal("maskScriptSecrets returned nil")
	}
	projectedEnvelope, _ := projected.Actions[0].Params["soap_envelope"].(string)
	if projectedEnvelope == envelope {
		t.Skipf("projection no longer rewrites <messageId>; vector is stale: %q", projectedEnvelope)
	}
	if strings.Contains(projectedEnvelope, maskedScriptSecret) {
		t.Skipf("projection now leaves a sentinel the binding gate catches: %q", projectedEnvelope)
	}

	runner := NewTestRunner(1, NewHub(), nil)
	stored, err := runner.StoreScriptDurable(cloneScriptDefinition(script))
	if err != nil {
		t.Fatalf("ordinary durable create: %v", err)
	}
	if got, _ := stored.Script.Actions[0].Params["soap_envelope"].(string); got != envelope {
		t.Fatalf("durable envelope = %q, want the submitted executable value %q", got, envelope)
	}
	durable, ok := runner.GetScript(stored.ID)
	if !ok {
		t.Fatal("stored script disappeared")
	}
	if got, _ := durable.Script.Actions[0].Params["soap_envelope"].(string); got != envelope {
		t.Fatalf("read-back envelope = %q, want %q", got, envelope)
	}
}

// A library bundle crosses an instance boundary: the receiving APEX Load
// imports it and must end up with a script that runs. Masking the bundled
// script produced the same round-trip break as a masked export — the transfer
// silently degraded a runnable script into one that could not execute — so the
// executable definition travels verbatim and ContentHash is taken over it.
//
// The non-executable source-review boundary still holds: review-only evidence
// must not ride along, which is asserted here so this test cannot be used to
// relax it.
func TestLibraryScriptBundleShipsTheRunnableScript(t *testing.T) {
	server := NewServer(ServerConfig{})

	imported, created, err := server.runner.CreateImportedScriptDurableReportingReplay(
		importedRoundTripScript("library-bundle-round-trip"),
		"test",
	)
	if err != nil {
		t.Fatalf("import commit: %v", err)
	}
	if !created {
		t.Fatal("import unexpectedly resolved as an idempotent replay")
	}

	item, err := server.libraryScriptBundleItem(imported.ID)
	if err != nil {
		t.Fatalf("libraryScriptBundleItem: %v", err)
	}

	assertRoundTripScriptIsRunnable(t, "library bundle item", item.Script)

	payload, err := json.Marshal(item.Script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), roundTripReviewSecret) {
		t.Fatalf("library bundle omitted imported review evidence: %s", payload)
	}

	// ContentHash stays the PORTABLE hash: hashLibraryScript masks internally, so
	// it is stable when only an inline credential differs and both ends compute
	// it identically. Shipping the executable body must not disturb that — link
	// identity and drift detection are unaffected by this change.
	if want := hashLibraryScript(item.Script); item.ContentHash != want {
		t.Fatalf("ContentHash = %q, want the portable masked hash %q", item.ContentHash, want)
	}

	// The bundled item's label stays a disclosure-safe projection.
	if strings.Contains(item.Name, roundTripAuthorization) {
		t.Fatalf("library bundle item name leaked a credential: %q", item.Name)
	}
}
