package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
	yaml "gopkg.in/yaml.v3"
)

const storedRepairCredential = "stored-repair-synthetic-token"

// storedRepairScript is an enterprise-archive import as saved: two legacy TLS
// gates in vuser_init, a kept credential on the business request, and the
// preserved review of its conversion.
func storedRepairScript(id, protocolName string) *dsl.Script {
	script := &dsl.Script{
		ID: id, Name: "Stored repair", Version: "1", Protocol: protocolName, ProjectID: storage.DefaultProjectID,
		Config: dsl.ScriptConfig{BaseURL: "https://example.test", WebRuntime: dsl.WebRuntimeConfig{AdditionalAttributes: map[string]string{"web_set_sockets_option.SSL_VERSION": "2&3"}}},
		Init: []dsl.Action{
			legacyTLSGateForTest("first setting", legacyTLSWarningForTest("2&3")),
			{Name: "home", Type: "GET", Method: "GET", Target: "/"},
			legacyTLSGateForTest("second setting", legacyTLSWarningForTest("AUTO")),
		},
		Actions: []dsl.Action{{Name: "orders", Type: "GET", Method: "GET", Target: "/orders", Headers: map[string]string{"Authorization": "Bearer " + storedRepairCredential}}},
	}
	// The report as the converter writes it (enterprisearchive.ImportFidelityReport),
	// so the promotion and launch gates read it: the two TLS gates block it.
	ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)["fidelity_report"] = map[string]any{
		"source_files": []any{
			map[string]any{"path": "vuser_init.c", "phase": "init", "language": "c"},
			map[string]any{"path": "Action.c", "phase": "action", "language": "c"},
			map[string]any{"path": "vuser_end.c", "phase": "end", "language": "c"},
		},
		"promotion_status": "blocked",
		"blockers":         []any{"legacy_tls_replacement at init[0]", "legacy_tls_replacement at init[2]"},
	}
	attachImportReplayPreflight(script, enterpriseArchiveSaveProfile, buildReplayPreflight(script, nil, validateScript(script), []string{legacyTLSWarningForTest("2&3"), legacyTLSWarningForTest("AUTO")}))
	return script
}

// storeRepairImport saves script the way the import wizard does, so it keeps
// its imported credential as a credential-preserving import.
func storeRepairImport(t *testing.T, server *Server, script *dsl.Script) *StoredScript {
	t.Helper()
	stored, created, err := server.runner.CreateImportedScriptDurableReportingReplay(script, enterpriseArchiveSaveProfile)
	if err != nil || !created || stored == nil {
		t.Fatalf("store import: created=%v err=%v", created, err)
	}
	if !isCredentialPreservingImport(stored.Script) {
		t.Fatal("fixture is not a credential-preserving import")
	}
	// The create result carries no content revision; the durable snapshot does.
	return durableRepairSnapshot(t, server, stored.ID)
}

func selectTLSRepair(path string) ScriptRepairRequest {
	return ScriptRepairRequest{Finding: "legacy_tls_replacement", ActionPath: path, Operation: ImportRepairOperation{Type: "select_tls", TLSMin: "1.2", TLSMax: "1.3"}}
}

func scriptRepairCall(t *testing.T, server *Server, id, action string, body any, tag string) *httptest.ResponseRecorder {
	t.Helper()
	return authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+id+"/repairs/"+action, body, tag)
}

func decodeScriptRepair(t *testing.T, response *httptest.ResponseRecorder) ScriptRepairResponse {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("repair %d: %s", response.Code, response.Body.String())
	}
	var decoded ScriptRepairResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func durableRepairSnapshot(t *testing.T, server *Server, id string) *StoredScript {
	t.Helper()
	stored, found, err := server.runner.GetScriptAuthoringDurable(id)
	if err != nil || !found || stored == nil {
		t.Fatalf("durable read: found=%v err=%v", found, err)
	}
	return stored
}

func executionGatePaths(script *dsl.Script, code string) []string {
	var paths []string
	_ = dslwalk.Walk(script, func(ref dslwalk.SurfaceRef) error {
		if hasExecutionCode(ref.Metadata(), code) {
			paths = append(paths, ref.Path)
		}
		return nil
	})
	return paths
}

// scriptJSONDiff lists the JSON paths whose values differ between two scripts.
func scriptJSONDiff(t *testing.T, before, after *dsl.Script) []string {
	t.Helper()
	decode := func(script *dsl.Script) any {
		raw, err := json.Marshal(script)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	var paths []string
	var walk func(path string, x, y any)
	walk = func(path string, x, y any) {
		xm, xMap := x.(map[string]any)
		ym, yMap := y.(map[string]any)
		if xMap && yMap {
			keys := map[string]bool{}
			for key := range xm {
				keys[key] = true
			}
			for key := range ym {
				keys[key] = true
			}
			for key := range keys {
				child := key
				if path != "" {
					child = path + "." + key
				}
				walk(child, xm[key], ym[key])
			}
			return
		}
		xs, xSlice := x.([]any)
		ys, ySlice := y.([]any)
		if xSlice && ySlice && len(xs) == len(ys) {
			for index := range xs {
				walk(fmt.Sprintf("%s[%d]", path, index), xs[index], ys[index])
			}
			return
		}
		if !reflect.DeepEqual(x, y) {
			paths = append(paths, path)
		}
	}
	walk("", decode(before), decode(after))
	sort.Strings(paths)
	return paths
}

// A preview evaluates the repair on the saved script and persists nothing.
func TestStoredScriptRepairPreviewDoesNotPersist(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-preview", "http"))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	preview := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "preview", selectTLSRepair("init[0]"), tag))

	if preview.ScriptID != stored.ID || preview.ContentRevision != stored.ContentRevision || preview.Stored != nil || preview.Validation != nil {
		t.Fatalf("preview identity = %q/%q stored=%v validation=%v, want the input revision and nothing persisted", preview.ScriptID, preview.ContentRevision, preview.Stored != nil, preview.Validation != nil)
	}
	if !preview.Readiness.ReplayReady {
		t.Fatalf("readiness = %+v, want replay ready once one apply clears both TLS gates", preview.Readiness)
	}
	for _, finding := range preview.Findings {
		if finding.Code == "legacy_tls_replacement" {
			t.Fatalf("preview kept a TLS finding: %+v", finding)
		}
	}
	if tls := preview.Script.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("preview TLS = %#v, want 1.2-1.3", tls)
	}
	if got := executionGatePaths(preview.Before, "legacy_tls_replacement"); strings.Join(got, ",") != "init[0],init[2]" {
		t.Fatalf("before gates = %v, want both stored TLS gates", got)
	}
	allowed := []string{
		"config.http.tls", "config.web_runtime.additional_attributes",
		"init[0].metadata", "init[2].metadata",
		"metadata.import.preserved.fidelity_report", "metadata.import.preserved.replay_preflight",
	}
	for _, path := range scriptJSONDiff(t, preview.Before, preview.Script) {
		ok := false
		for _, prefix := range allowed {
			ok = ok || path == prefix || strings.HasPrefix(path, prefix+".") || strings.HasPrefix(path, prefix+"[")
		}
		if !ok {
			t.Errorf("preview changed %s, outside what a TLS repair owns", path)
		}
	}
	restamped := preservedReplayPreflight(preview.Script)
	if restamped == nil || !restamped.Valid || replayPreflightCheckStatus(restamped, "review_gates") != "pass" {
		t.Fatalf("preview preflight = %+v, want it rebuilt from the repaired script", restamped)
	}

	current := durableRepairSnapshot(t, server, stored.ID)
	if current.ContentRevision != stored.ContentRevision {
		t.Fatal("preview changed the durable revision")
	}
	if got := executionGatePaths(current.Script, "legacy_tls_replacement"); len(got) != 2 {
		t.Fatalf("durable gates after preview = %v, want both", got)
	}
	if current.Script.Config.HTTP.TLS.MinVersion != "" {
		t.Fatalf("durable TLS after preview = %#v, want unchanged", current.Script.Config.HTTP.TLS)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "script.repair.apply", Limit: 10})
	if err != nil || len(events) != 0 {
		t.Fatalf("preview recorded %d repair audit events (err %v)", len(events), err)
	}
}

// One apply clears both TLS gates of the saved copy, which debug checks
// before anything else, so the next debug start is accepted.
func TestStoredScriptRepairApplyClearsGatesAndDebugStarts(t *testing.T) {
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: http.StatusOK}, nil
	})
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-apply", protocolName))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	debug := func(sessionID string) *httptest.ResponseRecorder {
		t.Helper()
		return authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+stored.ID+"/debug/sessions", ScriptDebugStartRequest{ProjectID: stored.ProjectID, ReplaySessionID: sessionID}, "")
	}
	refused := debug("")
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "script_replay_blocked") {
		t.Fatalf("debug before the repair = %d %s, want the stored-gate refusal", refused.Code, refused.Body.String())
	}

	response := scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[2]"), tag)
	applied := decodeScriptRepair(t, response)
	if mode := response.Header().Get("X-APEX-Script-Save-Mode"); mode != "conditioned" {
		t.Fatalf("save mode = %q, want conditioned", mode)
	}
	_, nextTag := requireAuthoringAPIProjection(t, server, stored.ID)
	current := durableRepairSnapshot(t, server, stored.ID)
	if current.ContentRevision == stored.ContentRevision || applied.Stored == nil || applied.Stored.ContentRevision != current.ContentRevision || applied.Stored.AuthoringProjection == nil {
		t.Fatalf("apply revision: stored=%+v durable=%q prior=%q", applied.Stored, current.ContentRevision, stored.ContentRevision)
	}
	if wantTag, _ := scriptAuthoringETag(applied.Stored.ContentRevision); wantTag != nextTag {
		t.Fatalf("apply returned revision %q, but the authoring tag is now %s", applied.Stored.ContentRevision, nextTag)
	}
	if applied.ContentRevision != stored.ContentRevision {
		t.Fatalf("response content_revision = %q, want the input revision %q", applied.ContentRevision, stored.ContentRevision)
	}
	if applied.Validation == nil || applied.Validation.ValidatedContentRevision != current.ContentRevision || applied.Validation.Preflight == nil || replayPreflightCheckStatus(applied.Validation.Preflight, "review_gates") != "pass" {
		t.Fatalf("validation = %+v, want the repaired revision's passing review gates", applied.Validation)
	}
	if tls := current.Script.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("durable TLS = %#v, want 1.2-1.3", tls)
	}
	if _, kept := current.Script.Config.WebRuntime.AdditionalAttributes["web_set_sockets_option.SSL_VERSION"]; kept {
		t.Fatal("legacy SSL_VERSION attribute survived the repair")
	}
	if got := executionGatePaths(current.Script, "legacy_tls_replacement"); len(got) != 0 {
		t.Fatalf("durable TLS gates after one apply = %v, want none", got)
	}
	for _, index := range []int{0, 2} {
		if change := dslwalk.MetadataString(current.Script.Init[index].Metadata, "migration_change"); !strings.Contains(change, "TLS 1.2–1.3") {
			t.Errorf("init[%d] migration_change = %q, want the operator's selection", index, change)
		}
	}
	if !isCredentialPreservingImport(current.Script) || current.Script.Actions[0].Headers["Authorization"] != "Bearer "+storedRepairCredential {
		t.Fatal("the repair dropped the kept imported credential")
	}
	if err := storedScriptReplayGateError(current.Script); err != nil {
		t.Fatalf("stored gate after the repair: %v", err)
	}

	const sessionID = "66666666-6666-4666-8666-666666666666"
	started := debug(sessionID)
	if started.Code != http.StatusOK {
		t.Fatalf("debug after the repair = %d %s, want it accepted", started.Code, started.Body.String())
	}
	_, _ = server.scriptDebug.Stop(sessionID)
}

// dashboardScriptResponseCap is the response cap the dashboard reads a script
// GET, save and repair with (maxResponseBytes in useScriptApi). A larger
// apply receipt is aborted after the repair was saved, so the editor can only
// report an unknown outcome.
const dashboardScriptResponseCap = 12 << 20

// A large saved import (about 3 MB of steps, as a real archive import's
// authoring projection is) repairs within the dashboard's response cap. The
// preview carries the before/after comparison; the apply carries the script
// only as the persisted copy, as a save does, and its validation embeds none.
func TestStoredScriptRepairLargeScriptApplyCarriesOnlyTheStoredCopy(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	const steps, marker = 1500, "synthetic-large-repair-step"
	padding := strings.Repeat("lorem ipsum dolor sit amet ", 80)
	script := storedRepairScript("stored-repair-large", "http")
	for index := range steps {
		script.Actions = append(script.Actions, dsl.Action{
			Name: fmt.Sprintf("item_%04d", index), Type: "POST", Method: "POST", Target: fmt.Sprintf("/items/%d", index),
			Body: fmt.Sprintf(`{"note":"%s %04d %s"}`, marker, index, padding),
		})
	}
	stored := storeRepairImport(t, server, script)
	projection, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	copyBytes, err := json.Marshal(projection.Script)
	if err != nil {
		t.Fatal(err)
	}
	if len(copyBytes) < 3<<20 || bytes.Count(copyBytes, []byte(marker)) != steps {
		t.Fatalf("one script copy is %d bytes with %d steps, want about 3 MB naming each step once", len(copyBytes), bytes.Count(copyBytes, []byte(marker)))
	}
	copies := func(raw []byte) int { return bytes.Count(raw, []byte(marker)) / steps }
	fields := func(response *httptest.ResponseRecorder) map[string]json.RawMessage {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("repair %d: %.300s", response.Code, response.Body.String())
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	preview := scriptRepairCall(t, server, stored.ID, "preview", selectTLSRepair("init[0]"), tag)
	previewed := fields(preview)
	if preview.Body.Len() >= dashboardScriptResponseCap || copies(preview.Body.Bytes()) != 2 || copies(previewed["before"]) != 1 || copies(previewed["script"]) != 1 {
		t.Fatalf("preview is %d bytes with %d script copies, want the before/after pair under %d", preview.Body.Len(), copies(preview.Body.Bytes()), dashboardScriptResponseCap)
	}

	response := scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag)
	applied := fields(response)
	if response.Body.Len() >= dashboardScriptResponseCap {
		t.Errorf("apply is %d bytes, over the dashboard's %d-byte cap", response.Body.Len(), dashboardScriptResponseCap)
	}
	for _, key := range []string{"script", "before"} {
		if _, present := applied[key]; present {
			t.Errorf("apply response carries the preview's %s", key)
		}
	}
	var persisted struct {
		Script              json.RawMessage `json:"script"`
		AuthoringProjection struct {
			Script json.RawMessage `json:"script"`
		} `json:"authoring_projection"`
	}
	if applied["stored"] == nil || applied["validation"] == nil || json.Unmarshal(applied["stored"], &persisted) != nil {
		t.Fatal("apply response lacks the stored script or its validation")
	}
	// The persisted copy is what a save returns: the script and its authoring
	// projection. Nothing else in the receipt repeats a step.
	if copies(persisted.Script) != 1 || copies(persisted.AuthoringProjection.Script) != 1 || copies(response.Body.Bytes()) != 2 {
		t.Fatalf("apply carries %d script copies (stored %d + %d), want only the stored copy", copies(response.Body.Bytes()), copies(persisted.Script), copies(persisted.AuthoringProjection.Script))
	}
	if copies(applied["validation"]) != 0 || len(applied["validation"]) > len(copyBytes)/10 {
		t.Fatalf("validation is %d bytes with %d script copies, want none", len(applied["validation"]), copies(applied["validation"]))
	}
	if outside := response.Body.Len() - len(applied["stored"]); outside > len(copyBytes)/10 {
		t.Fatalf("apply is %d bytes, %d of them outside stored; want the stored copy plus validation", response.Body.Len(), outside)
	}
}

// A credential-preserving import keeps its credentials through the repair,
// which may change only the transport fields its operation owns. The same TLS
// change through PUT is refused, which is why the editor repairs on the server.
func TestStoredScriptRepairCredentialPreservingImportAllowsTLSOnly(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-credentials", "http")
	script.Parameters = []dsl.Parameter{{Name: "order_id", Type: "list", Values: []string{"1001"}, Update: "once"}}
	script.Actions[0].Target = "/orders/{{var.order}}"
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	stored := storeRepairImport(t, server, script)
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	// Control: the client-only repair, a PUT of the same TLS selection.
	edited := maskScriptSecrets(stored.Script)
	edited.Config.HTTP.TLS.MinVersion, edited.Config.HTTP.TLS.MaxVersion = "1.2", "1.3"
	put := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+stored.ID, map[string]any{"script": edited}, tag)
	if put.Code != http.StatusBadRequest || !strings.Contains(put.Body.String(), "execution context for retained imported credentials") {
		t.Fatalf("PUT of the TLS change = %d %s, want the retained-credential refusal", put.Code, put.Body.String())
	}

	// Rewriting the consumer that carries the kept credential moves it.
	bind := ScriptRepairRequest{Finding: "missing_producer:order", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "bind_variable", Producer: "order_id"}}
	for _, action := range []string{"preview", "apply"} {
		refused := scriptRepairCall(t, server, stored.ID, action, bind, tag)
		if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "execution context for retained imported credentials") {
			t.Fatalf("%s bind_variable on the credential consumer = %d %s, want the retained-credential refusal", action, refused.Code, refused.Body.String())
		}
	}
	if durableRepairSnapshot(t, server, stored.ID).ContentRevision != stored.ContentRevision {
		t.Fatal("a refused repair changed the script")
	}

	decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	before, err := store.GetScript(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Actions[0].Headers["Authorization"]; got != "Bearer "+storedRepairCredential {
		t.Fatalf("kept credential after the repair = %q, want it byte-identical", got)
	}
	if before.Config.HTTP.TLS.MinVersion != "1.2" || before.Config.HTTP.TLS.MaxVersion != "1.3" {
		t.Fatalf("durable TLS = %#v, want the repaired range", before.Config.HTTP.TLS)
	}
	if !isCredentialPreservingImport(before) {
		t.Fatal("the repair dropped the credential-preserving policy")
	}
}

// storedKafkaRepairCredential is a kept credential a Kafka send delivers in a
// record header.
const storedKafkaRepairCredential = "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJzeW50aGV0aWMta2Fma2EifQ.c3ludGhldGljLWthZmthLXNpZw"

// storedKafkaRepairScript is a credential-preserving Kafka import as saved: a
// send with the record headers given, one of which reads {{var.corr}}, which
// nothing produces, and the orderId parameter a rebind can name instead.
func storedKafkaRepairScript(t *testing.T, id string, headers map[string]any) *dsl.Script {
	t.Helper()
	script := &dsl.Script{
		ID: id, Name: "Stored Kafka repair", Version: "1", Protocol: "kafka", ProjectID: storage.DefaultProjectID,
		Config:     dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true}},
		Parameters: []dsl.Parameter{{Name: "orderId", Type: "list", Values: []string{"synthetic-order-1001"}, Update: "once"}},
		Actions:    []dsl.Action{{Name: "send order", Type: "kafka_produce", Target: "orders", Body: "{}", Metadata: headers}},
	}
	ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)
	findings, err := importreview.RefreshDependencies(script)
	if err != nil || len(findings) != 1 || findings[0].Code != "missing_producer:corr" || findings[0].ActionPath != "actions[0]" {
		t.Fatalf("fixture findings = %+v, %v; want the header's missing_producer:corr", findings, err)
	}
	return script
}

// bind_variable rewrites the headers a Kafka send delivers. On a
// credential-preserving import such a header is compared like every
// executable field: a rebind that edits the text around a kept credential is
// refused as PUT refuses the same edit, and a rebind of a header without one
// still applies beside a kept credential it leaves alone.
func TestStoredScriptRepairBindVariableComparesReplayReadMetadata(t *testing.T) {
	rebind := ScriptRepairRequest{Finding: "missing_producer:corr", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "bind_variable", Producer: "orderId"}}

	t.Run("header holding a kept credential", func(t *testing.T) {
		server, store := newAuditTestServer(t)
		t.Cleanup(func() { store.Close() })
		header := storedKafkaRepairCredential + ";corr={{var.corr}}"
		stored := storeRepairImport(t, server, storedKafkaRepairScript(t, "stored-kafka-credential-header", map[string]any{"x-auth": header}))
		// The disclosure masks the whole value after the scheme, reference
		// included, so an editor cannot even write this rebind through PUT.
		if masked := maskScriptSecrets(stored.Script); masked == nil || strings.Contains(fmt.Sprint(masked.Actions[0].Metadata["x-auth"]), storedKafkaRepairCredential) {
			t.Fatal("fixture header is not a kept credential the disclosure masks")
		}
		_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

		// Control: the check PUT applies to a reconciled edit, given the same
		// rebind of the header.
		edited := cloneScriptDefinition(stored.Script)
		edited.Actions[0].Metadata["x-auth"] = strings.Replace(header, "{{var.corr}}", "{{var.orderId}}", 1)
		want := validatePreservedImportCredentialEdits(edited, stored.Script)
		if want == nil || !strings.Contains(want.Error(), "introduces new executable credentials") {
			t.Fatalf("PUT's credential check on the rebind = %v, want the new-credential refusal", want)
		}
		for _, action := range []string{"preview", "apply"} {
			refused := scriptRepairCall(t, server, stored.ID, action, rebind, tag)
			var refusal struct {
				Error string `json:"error"`
			}
			if refused.Code != http.StatusBadRequest || json.Unmarshal(refused.Body.Bytes(), &refusal) != nil || refusal.Error != want.Error() {
				t.Fatalf("%s bind_variable on the credential header = %d %s, want PUT's refusal %q", action, refused.Code, refused.Body.String(), want)
			}
		}
		current := durableRepairSnapshot(t, server, stored.ID)
		if current.ContentRevision != stored.ContentRevision || current.Script.Actions[0].Metadata["x-auth"] != header {
			t.Fatalf("a refused repair changed the script: header %v", current.Script.Actions[0].Metadata["x-auth"])
		}
	})

	// A nested request has no action of its own; the repair reads and
	// rebinds its metadata as an action decoded from it, and so does the
	// comparison.
	t.Run("nested request entry holding a kept credential", func(t *testing.T) {
		server, store := newAuditTestServer(t)
		t.Cleanup(func() { store.Close() })
		note := storedKafkaRepairCredential + " {{param.corr}}"
		script := &dsl.Script{
			ID: "stored-nested-credential-entry", Name: "Stored nested repair", Version: "1", Protocol: "http", ProjectID: storage.DefaultProjectID,
			Config:     dsl.ScriptConfig{BaseURL: "https://example.test"},
			Parameters: []dsl.Parameter{{Name: "orderId", Type: "list", Values: []string{"synthetic-order-1001"}, Update: "once"}},
			Actions: []dsl.Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{
				map[string]any{"name": "orders", "method": "GET", "target": "https://example.test/orders", "metadata": map[string]any{"x-note": note}},
			}}}},
		}
		ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)
		findings, err := importreview.RefreshDependencies(script)
		if err != nil || len(findings) != 1 || findings[0].Code != "missing_producer:corr" || findings[0].ActionPath != "actions[0].params.requests[0]" {
			t.Fatalf("fixture findings = %+v, %v; want the nested entry's missing_producer:corr", findings, err)
		}
		stored := storeRepairImport(t, server, script)
		_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
		nested := ScriptRepairRequest{Finding: "missing_producer:corr", ActionPath: "actions[0].params.requests[0]", Operation: rebind.Operation}
		refused := scriptRepairCall(t, server, stored.ID, "preview", nested, tag)
		if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "introduces new executable credentials") {
			t.Fatalf("bind_variable on the nested credential entry = %d %s, want the new-credential refusal", refused.Code, refused.Body.String())
		}
	})

	t.Run("header without a credential", func(t *testing.T) {
		server, store := newAuditTestServer(t)
		t.Cleanup(func() { store.Close() })
		stored := storeRepairImport(t, server, storedKafkaRepairScript(t, "stored-kafka-plain-header", map[string]any{"x-auth": storedKafkaRepairCredential, "x-corr": "{{var.corr}}"}))
		_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

		applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", rebind, tag))
		for _, finding := range applied.Findings {
			if finding.Code == rebind.Finding {
				t.Fatalf("the rebound dependency is still reported: %+v", finding)
			}
		}
		current, err := store.GetScript(stored.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got := current.Actions[0].Metadata; got["x-corr"] != "{{var.orderId}}" || got["x-auth"] != storedKafkaRepairCredential || !isCredentialPreservingImport(current) {
			t.Fatalf("headers after the rebind = %v, want x-corr rebound and the kept credential byte-identical", got)
		}
	})
}

// A dependency repair restamps the review codes of the step it repairs. On a
// credential-preserving import, a long-name code that disclosure masks then
// moves up into review_gate and review_reasons[0] of an HTTP step. Replay
// never sends or resolves those entries, so the moved code is server output,
// not a kept credential: the repair applies, as the same fix through PUT does.
func TestStoredScriptRepairMovesAMaskedReviewCodeOnACredentialPreservingImport(t *testing.T) {
	longCode := "missing_producer:" + savedEvaluationLongName
	addStore := ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: "c_storeid", Type: "list", Values: []string{"synthetic-store-1001"}, Update: "once"}}
	storeCart := func(t *testing.T, server *Server, id string) *StoredScript {
		t.Helper()
		script := storedRepairScript(id, "http")
		script.Actions = append(script.Actions, dsl.Action{Name: "cart", Type: "GET", Method: "GET", Target: "/cart/{{var.c_storeid}}/{{var." + savedEvaluationLongName + "}}"})
		if _, err := importreview.RefreshDependencies(script); err != nil {
			t.Fatal(err)
		}
		stored := storeRepairImport(t, server, script)
		cart := stored.Script.Actions[1].Metadata
		masked := maskScriptSecrets(stored.Script).Actions[1].Metadata
		reasons, _ := masked["review_reasons"].([]any)
		if cart["review_gate"] != "missing_producer:c_storeid" || len(reasons) != 2 || strings.Contains(fmt.Sprint(reasons[1]), savedEvaluationLongName) {
			t.Fatalf("fixture cart metadata = %v (disclosed reasons %v), want c_storeid gating and the long code masked behind it", cart, reasons)
		}
		return stored
	}

	t.Run("stored repair", func(t *testing.T) {
		server, store := newAuditTestServer(t)
		t.Cleanup(func() { store.Close() })
		stored := storeCart(t, server, "stored-repair-masked-review-code")
		_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
		repair := ScriptRepairRequest{Finding: "missing_producer:c_storeid", ActionPath: "actions[1]", Operation: addStore}
		decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "preview", repair, tag))
		decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", repair, tag))

		current, err := store.GetScript(stored.ID)
		if err != nil {
			t.Fatal(err)
		}
		if gate := current.Actions[1].Metadata["review_gate"]; gate != longCode {
			t.Fatalf("cart review_gate after the repair = %v, want the long code moved up", gate)
		}
		if len(current.Parameters) != 1 || current.Parameters[0].Name != "c_storeid" {
			t.Fatalf("parameters = %+v, want the c_storeid parameter the repair added", current.Parameters)
		}
		if current.Actions[0].Headers["Authorization"] != "Bearer "+storedRepairCredential || !isCredentialPreservingImport(current) {
			t.Fatal("the repair changed the kept credential")
		}
	})

	t.Run("the same fix through PUT", func(t *testing.T) {
		server, store := newAuditTestServer(t)
		t.Cleanup(func() { store.Close() })
		stored := storeCart(t, server, "stored-put-masked-review-code")
		addParameter := func(t *testing.T, document map[string]any) {
			parameters, _ := document["parameters"].([]any)
			document["parameters"] = append(parameters, map[string]any{"name": "c_storeid", "type": "list", "values": []any{"synthetic-store-1001"}, "update": "once"})
		}
		if response := putAuthoringEdit(t, server, stored.ID, addParameter); response.Code != http.StatusOK {
			t.Fatalf("PUT adding the parameter = %d %s, want 200", response.Code, response.Body.String())
		}
		if gate := durableRepairSnapshot(t, server, stored.ID).Script.Actions[1].Metadata["review_gate"]; gate != longCode {
			t.Fatalf("cart review_gate after the save = %v, want the long code moved up", gate)
		}
	})
}

// storedMessagingSend is a Kafka or JMS send step and the script protocol and
// configuration that replay it against an in-memory broker.
type storedMessagingSend struct {
	action   string
	protocol string
	config   dsl.ScriptConfig
}

// storedMessagingSends deliver every string metadata entry, review codes
// included, as a record header (Kafka) or a message property (JMS).
var storedMessagingSends = []storedMessagingSend{
	{action: "kafka_produce", protocol: "kafka", config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{Mock: true}}},
	{action: "jms_publish", protocol: "jms-solace", config: dsl.ScriptConfig{JMS: dsl.JMSConfig{Provider: "solace", Mock: true}}},
}

// storedMessagingReviewCodeScript is a credential-preserving Kafka or JMS
// import as saved: an orders send whose x-auth header keeps a credential, a
// cart send that reads c_storeid and savedEvaluationLongName, which nothing
// produces, and a summary send that reads nothing. A messaging send has no
// placeholder fallback, so the cart's codes are execution blockers, the
// c_storeid code first.
func storedMessagingReviewCodeScript(t *testing.T, id string, send storedMessagingSend) *dsl.Script {
	t.Helper()
	script := &dsl.Script{
		ID: id, Name: "Stored messaging review code", Version: "1", Protocol: send.protocol, ProjectID: storage.DefaultProjectID, Config: send.config,
		Actions: []dsl.Action{
			{Name: "orders", Type: send.action, Target: "orders", Body: "{}", Metadata: map[string]any{"x-auth": storedKafkaRepairCredential}},
			{Name: "cart", Type: send.action, Target: "carts", Body: `{"store":"{{var.c_storeid}}","session":"{{var.` + savedEvaluationLongName + `}}"}`},
			{Name: "summary", Type: send.action, Target: "summaries", Body: "{}"},
		},
	}
	ensureScriptImportPreserved(script, enterpriseArchiveSaveProfile)
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	return script
}

// A Kafka or JMS send delivers its execution_blocker as a header or property,
// so a repair compares that entry like one. A dependency code in it is still
// server output that the repair stamps again from the repaired script: a
// repair that moves a masked long-name code up into execution_blocker applies,
// as on an HTTP step, and the kept credential header stays byte-identical.
func TestStoredScriptRepairMovesAMaskedReviewCodeOnACredentialPreservingMessagingImport(t *testing.T) {
	longCode := "missing_producer:" + savedEvaluationLongName
	addStore := ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: "c_storeid", Type: "list", Values: []string{"synthetic-store-1001"}, Update: "once"}}
	for _, send := range storedMessagingSends {
		t.Run(send.action, func(t *testing.T) {
			server, store := newAuditTestServer(t)
			t.Cleanup(func() { store.Close() })
			stored := storeRepairImport(t, server, storedMessagingReviewCodeScript(t, "stored-"+send.action+"-masked-review-code", send))
			masked := maskScriptSecrets(stored.Script)
			blockers, _ := masked.Actions[1].Metadata["execution_blockers"].([]any)
			if gate := stored.Script.Actions[1].Metadata["execution_blocker"]; gate != "missing_producer:c_storeid" || len(blockers) != 2 || strings.Contains(fmt.Sprint(blockers[1]), savedEvaluationLongName) {
				t.Fatalf("fixture cart execution_blocker = %v (disclosed blockers %v), want c_storeid gating and the long code masked behind it", gate, blockers)
			}
			if strings.Contains(fmt.Sprint(masked.Actions[0].Metadata["x-auth"]), storedKafkaRepairCredential) {
				t.Fatal("fixture header is not a kept credential the disclosure masks")
			}
			_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
			repair := ScriptRepairRequest{Finding: "missing_producer:c_storeid", ActionPath: "actions[1]", Operation: addStore}
			decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "preview", repair, tag))
			decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", repair, tag))

			current, err := store.GetScript(stored.ID)
			if err != nil {
				t.Fatal(err)
			}
			if gate := current.Actions[1].Metadata["execution_blocker"]; gate != longCode {
				t.Fatalf("cart execution_blocker after the repair = %v, want the long code moved up", gate)
			}
			if disclosed := fmt.Sprint(maskScriptSecrets(current).Actions[1].Metadata["execution_blocker"]); strings.Contains(disclosed, savedEvaluationLongName) {
				t.Fatalf("disclosed cart execution_blocker = %q, want the moved code masked as the fixture requires", disclosed)
			}
			if len(current.Parameters) != 1 || current.Parameters[0].Name != "c_storeid" {
				t.Fatalf("parameters = %+v, want the c_storeid parameter the repair added", current.Parameters)
			}
			if current.Actions[0].Metadata["x-auth"] != storedKafkaRepairCredential || !isCredentialPreservingImport(current) {
				t.Fatal("the repair changed the kept credential")
			}
		})
	}
}

// putAuthoringEdit reads the script's authoring view and tag the way the
// editor does, applies edit to that document and saves it with PUT.
func putAuthoringEdit(t *testing.T, server *Server, id string, edit func(t *testing.T, document map[string]any)) *httptest.ResponseRecorder {
	t.Helper()
	projection, tag := requireAuthoringAPIProjection(t, server, id)
	var document map[string]any
	if err := json.Unmarshal([]byte(mustMarshalJSON(t, projection.Script)), &document); err != nil {
		t.Fatal(err)
	}
	edit(t, document)
	return authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+id, map[string]any{"script": document}, tag)
}

// addAuthoringParameter adds a parameter no request reads.
func addAuthoringParameter(t *testing.T, document map[string]any) {
	parameters, _ := document["parameters"].([]any)
	document["parameters"] = append(parameters, map[string]any{"name": "region", "type": "list", "values": []any{"west"}, "update": "once"})
}

// retargetAuthoringAction changes the target of the request named name.
func retargetAuthoringAction(name, target string) func(t *testing.T, document map[string]any) {
	return func(t *testing.T, document map[string]any) {
		t.Helper()
		for _, phase := range []string{"init", "actions", "teardown"} {
			actions, _ := document[phase].([]any)
			for _, raw := range actions {
				if action, _ := raw.(map[string]any); action != nil && action["name"] == name {
					action["target"] = target
					return
				}
			}
		}
		t.Fatalf("authoring document has no request named %q", name)
	}
}

// assertImportEvaluationDisclosedAsStored fails when disclosure masks any leaf
// of the preserved evaluation output: the fidelity report and the review's
// replay preflight. They hold counts, codes and secret names, never a
// credential, and a masked leaf reads as a kept credential with script-wide
// context to every later save of a credential-preserving import.
func assertImportEvaluationDisclosedAsStored(t *testing.T, script *dsl.Script) {
	t.Helper()
	masked := maskScriptSecrets(script)
	if script == nil || script.Metadata == nil || script.Metadata.Import == nil ||
		masked == nil || masked.Metadata == nil || masked.Metadata.Import == nil {
		t.Fatal("script has no import metadata to disclose")
	}
	if _, present := script.Metadata.Import.Preserved["fidelity_report"]; !present {
		t.Fatal("script has no preserved fidelity report")
	}
	for _, key := range []string{"fidelity_report", "replay_preflight"} {
		stored := mustMarshalJSON(t, script.Metadata.Import.Preserved[key])
		if disclosed := mustMarshalJSON(t, masked.Metadata.Import.Preserved[key]); disclosed != stored {
			t.Fatalf("disclosure changed preserved %s:\nstored    %s\ndisclosed %s", key, stored, disclosed)
		}
	}
}

// A repair on a saved credential-preserving import leaves the editor's next
// ordinary save judged like any other. The restamped fidelity report holds
// counts, never a credential, so it pins no part of the script: a parameter,
// or a request that does not carry the kept credential, may change. The kept
// credential's own request still may not.
func TestStoredScriptRepairKeepsLaterEditorSavesOpen(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-later-saves", "http"))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	assertImportEvaluationDisclosedAsStored(t, durableRepairSnapshot(t, server, stored.ID).Script)

	if response := putAuthoringEdit(t, server, stored.ID, addAuthoringParameter); response.Code != http.StatusOK {
		t.Fatalf("PUT adding a parameter after the repair = %d %s, want 200", response.Code, response.Body.String())
	}
	if response := putAuthoringEdit(t, server, stored.ID, retargetAuthoringAction("home", "/home")); response.Code != http.StatusOK {
		t.Fatalf("PUT retargeting a request without the kept credential = %d %s, want 200", response.Code, response.Body.String())
	}
	refused := putAuthoringEdit(t, server, stored.ID, retargetAuthoringAction("orders", "/orders/export"))
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "execution context for retained imported credentials at actions[0]") {
		t.Fatalf("PUT retargeting the kept credential's request = %d %s, want the retained-credential refusal at that request", refused.Code, refused.Body.String())
	}

	current := durableRepairSnapshot(t, server, stored.ID)
	if got := current.Script.Actions[0].Headers["Authorization"]; got != "Bearer "+storedRepairCredential || current.Script.Actions[0].Target != "/orders" {
		t.Fatalf("kept credential request = %q %q, want it unchanged", current.Script.Actions[0].Target, got)
	}
	if len(current.Script.Parameters) != 1 || current.Script.Parameters[0].Name != "region" || current.Script.Init[1].Target != "/home" {
		t.Fatalf("durable script lost an accepted edit: parameters=%+v home=%q", current.Script.Parameters, current.Script.Init[1].Target)
	}
	if tls := current.Script.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" || !isCredentialPreservingImport(current.Script) {
		t.Fatalf("durable TLS = %#v (credential-preserving %v), want the repair kept", tls, isCredentialPreservingImport(current.Script))
	}
}

// storedRepairArchiveAction is the guided TLS archive with a cart request that
// reads {CartId}, which nothing in the archive supplies.
var storedRepairArchiveAction = strings.Replace(guidedTLSRepairAction, "URL=https://example.test/cart", "URL=https://example.test/cart/{CartId}", 1)

// A saved archive import keeps its source review, which quotes the kept
// Authorization header. The quote pins no execution context, so on the saved
// script a TLS repair and then a dependency repair for the value the archive
// never supplies are both applied, and the kept credential is unchanged.
func TestStoredScriptRepairOnSavedArchiveImportAppliesDependencyRepair(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := newGuidedRepairArchiveServer(t, store)
	archive := buildEnterpriseArchiveProtocolArchive(t, "Web", storedRepairArchiveAction)
	validated := serveEnterpriseArchiveDraft(t, server, guidedRepairArchiveValidatePath(store), archive)
	saved := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "stored-repair-archive", "scripts": []any{validated}})
	if saved.Code != http.StatusCreated {
		t.Fatalf("save of the validated archive = %d body=%s", saved.Code, saved.Body.String())
	}
	stored := storedEnterpriseBatchScript(t, server, saved)
	if !isCredentialPreservingImport(stored) {
		t.Fatal("the saved archive is not a credential-preserving import")
	}
	assertSourceReviewQuotesMaskedCredential(t, stored, guidedTLSRepairCredential)
	gates := executionGatePaths(stored, "legacy_tls_replacement")
	if len(gates) != 2 {
		t.Fatalf("saved TLS gates = %v, want one per legacy SSL setting", gates)
	}

	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair(gates[0]), tag))
	findings, err := importreview.FindingsFromScript(durableRepairSnapshot(t, server, stored.ID).Script)
	if err != nil {
		t.Fatal(err)
	}
	// A saved gate's code carries the variable name as the stored metadata
	// spells it, which is lower case.
	var missing importreview.Finding
	for _, finding := range findings {
		if finding.Code == "legacy_tls_replacement" {
			t.Fatalf("one select_tls apply left a TLS gate at %s", finding.ActionPath)
		}
		if strings.EqualFold(finding.Code, "missing_producer:CartId") {
			missing = finding
		}
	}
	if missing.ActionPath != namedActionPath(t, stored, "cart") {
		t.Fatalf("missing CartId finding at %q, want it on the cart request; findings %+v", missing.ActionPath, findings)
	}

	_, tag = requireAuthoringAPIProjection(t, server, stored.ID)
	name := strings.TrimPrefix(missing.Code, "missing_producer:")
	repair := ScriptRepairRequest{Finding: missing.Code, ActionPath: missing.ActionPath, Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: name, Type: "list", Values: []string{"synthetic-cart-1001"}, Update: "once"}}}
	decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "preview", repair, tag))
	applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", repair, tag))
	for _, finding := range applied.Findings {
		if strings.EqualFold(finding.Code, missing.Code) {
			t.Fatalf("the repaired dependency is still reported: %+v", finding)
		}
	}

	current, err := store.GetScript(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Parameters) != 1 || current.Parameters[0].Name != name {
		t.Fatalf("stored parameters = %+v, want the %s parameter the repair added", current.Parameters, name)
	}
	orders := namedAction(t, current, "orders")
	if orders.Headers["Authorization"] != guidedTLSRepairCredential || orders.Target != namedAction(t, stored, "orders").Target || !isCredentialPreservingImport(current) {
		t.Fatalf("kept credential request = %q %q, want it unchanged", orders.Target, orders.Headers["Authorization"])
	}
	if tls := current.Config.HTTP.TLS; tls.MinVersion != "1.2" || tls.MaxVersion != "1.3" {
		t.Fatalf("stored TLS = %#v, want the repaired range", tls)
	}
}

// An ordinary import has no kept credentials; a repair passes the same
// disclosure-safe binding gate as a save and is never written as trusted.
func TestStoredScriptRepairOrdinaryImportKeepsTheSaveBindingGate(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-ordinary", "http")
	script.Actions[0].Headers = nil
	script.Actions[0].Body = "password={{var.password}}"
	script.Actions[0].Type, script.Actions[0].Method = "POST", "POST"
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	if _, err := server.runner.CreateScriptAuthoringDurable(script); err != nil {
		t.Fatal(err)
	}
	created := durableRepairSnapshot(t, server, script.ID)
	if isCredentialPreservingImport(created.Script) || scriptImportSource(created.Script) != enterpriseArchiveSaveProfile {
		t.Fatal("fixture is not an ordinary import")
	}
	_, tag := requireAuthoringAPIProjection(t, server, created.ID)

	raw := ScriptRepairRequest{Finding: "missing_producer:password", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: "password", Type: "list", Values: []string{"synthetic-raw-password"}, Update: "once"}}}
	refused := scriptRepairCall(t, server, created.ID, "apply", raw, tag)
	if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), secretBindingsRequiredCode) || strings.Contains(refused.Body.String(), "synthetic-raw-password") {
		t.Fatalf("raw credential parameter = %d %s, want the binding gate", refused.Code, refused.Body.String())
	}

	decodeScriptRepair(t, scriptRepairCall(t, server, created.ID, "apply", selectTLSRepair("init[0]"), tag))
	current := durableRepairSnapshot(t, server, created.ID)
	if isCredentialPreservingImport(current.Script) {
		t.Fatal("a repair on an ordinary import stamped the credential-preserving policy")
	}
	if got := executionGatePaths(current.Script, "legacy_tls_replacement"); len(got) != 0 {
		t.Fatalf("ordinary import gates after the repair = %v, want none", got)
	}
}

// The repair is bound to the editor's revision. A retry of an applied repair
// with the old tag is stale; with the new one the finding is gone.
func TestStoredScriptRepairRequiresCurrentIfMatch(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-if-match", "http"))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	other := `"script-authoring-v1:scr-v1-` + strings.Repeat("a", 64) + `"`
	for _, test := range []struct {
		name, tag string
		status    int
	}{{"missing", "", 428}, {"wildcard", "*", 400}, {"weak", "W/" + tag, 400}, {"stale", other, 412}} {
		for _, action := range []string{"preview", "apply"} {
			if response := scriptRepairCall(t, server, stored.ID, action, selectTLSRepair("init[0]"), test.tag); response.Code != test.status {
				t.Fatalf("%s %s: %d %s, want %d", test.name, action, response.Code, response.Body.String(), test.status)
			}
		}
	}
	if durableRepairSnapshot(t, server, stored.ID).ContentRevision != stored.ContentRevision {
		t.Fatal("a refused precondition changed the script")
	}

	decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	if retry := scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag); retry.Code != http.StatusPreconditionFailed {
		t.Fatalf("retry with the old tag = %d %s, want 412", retry.Code, retry.Body.String())
	}
	_, nextTag := requireAuthoringAPIProjection(t, server, stored.ID)
	again := scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), nextTag)
	if again.Code != http.StatusBadRequest || !strings.Contains(again.Body.String(), "Finding is no longer present") {
		t.Fatalf("retry with the new tag = %d %s, want the finding gone", again.Code, again.Body.String())
	}
}

// Repairs need editor access on the script's own project, and another
// project's scope learns nothing about the script or its revision.
func TestStoredScriptRepairAuthz(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-authz", "http"))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	for _, action := range []string{"preview", "apply"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/repairs/"+action, strings.NewReader(`{}`))
		request.SetPathValue("id", stored.ID)
		request.Header.Set("If-Match", tag)
		bindAuthorizedProjectToRequest(request, "foreign-project")
		response := httptest.NewRecorder()
		server.processScriptRepair(response, request, action == "apply")
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "revision") || strings.Contains(response.Body.String(), "If-Match") {
			t.Fatalf("foreign %s = %d %s, want a plain 404", action, response.Code, response.Body.String())
		}
	}

	enableTrustedHeaderAuth(t, server)
	body, _ := json.Marshal(selectTLSRepair("init[0]"))
	call := func(action string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/repairs/"+action, bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("If-Match", tag)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	viewer := map[string]string{"X-Auth-Request-User": "viewer", "X-APEXLoad-Project-Roles": storage.DefaultProjectID + "=" + RoleViewer}
	for _, action := range []string{"preview", "apply"} {
		if response := call(action, viewer); response.Code != http.StatusForbidden {
			t.Fatalf("viewer %s = %d %s, want 403", action, response.Code, response.Body.String())
		}
	}
	if durableRepairSnapshot(t, server, stored.ID).ContentRevision != stored.ContentRevision {
		t.Fatal("a refused viewer changed the script")
	}
	editor := map[string]string{"X-Auth-Request-User": "editor", "X-APEXLoad-Project-Roles": storage.DefaultProjectID + "=" + RoleEditor}
	if response := call("apply", editor); response.Code != http.StatusOK {
		t.Fatalf("project editor apply = %d %s, want 200", response.Code, response.Body.String())
	}
	for _, action := range []string{"preview", "apply"} {
		requirement, ok := accessRequirementFromRouteCatalog(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/repairs/"+action)
		if !ok || requirement.ProjectLookup == nil || requirement.MaxBodyBytes != scriptRepairMaxBodyBytes || strings.Join(requirement.ProjectRoles, ",") != "editor,project_admin" {
			t.Fatalf("%s route access = %+v (catalogued %v), want editor access on the script's project", action, requirement, ok)
		}
	}
}

// Only the repairs a saved script can take are accepted. Java Vuser scripts,
// scripts that are not imports and stores without durable revisions refuse.
func TestStoredScriptRepairRejectsUnsupportedOperations(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-operations", "http"))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	body := "body"
	for _, test := range []struct {
		name    string
		request ScriptRepairRequest
		apply   bool
		want    string
	}{
		{"restore body preview", ScriptRepairRequest{Finding: "restored_body_required", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "restore_body", Body: &body}}, false, "only during import"},
		{"restore body apply", ScriptRepairRequest{Finding: "restored_body_required", ActionPath: "actions[0]", Operation: ImportRepairOperation{Type: "restore_body", Body: &body}}, true, "only during import"},
		{"allocation preview", ScriptRepairRequest{Operation: ImportRepairOperation{Type: "preview_allocation", VUsers: 1}}, false, "Unsupported repair operation"},
		{"unknown", ScriptRepairRequest{Finding: "legacy_tls_replacement", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "acknowledge"}}, true, "Unsupported repair operation"},
		{"inspect apply", ScriptRepairRequest{Operation: ImportRepairOperation{Type: "inspect"}}, true, "only previews"},
		{"other finding", ScriptRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[0]", Operation: ImportRepairOperation{Type: "replace_proxy_secret", SecretName: "PROXY_PASSWORD"}}, true, "Finding is no longer present"},
	} {
		action := map[bool]string{false: "preview", true: "apply"}[test.apply]
		response := scriptRepairCall(t, server, stored.ID, action, test.request, tag)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s = %d %s, want 400 %q", test.name, response.Code, response.Body.String(), test.want)
		}
	}
	unknownField := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+stored.ID+"/repairs/preview", strings.NewReader(`{"operation":{"type":"inspect"},"candidate_id":"protected:x"}`))
	unknownField.Header.Set("If-Match", tag)
	unknownFieldResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(unknownFieldResponse, unknownField)
	if unknownFieldResponse.Code != http.StatusBadRequest {
		t.Fatalf("an import-repair field = %d %s, want 400", unknownFieldResponse.Code, unknownFieldResponse.Body.String())
	}

	// inspect previews the saved script's own findings.
	inspected := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "preview", ScriptRepairRequest{Operation: ImportRepairOperation{Type: "inspect"}}, tag))
	gated := 0
	for _, finding := range inspected.Findings {
		if finding.Code == "legacy_tls_replacement" {
			gated++
		}
	}
	if gated != 2 || inspected.Readiness.ReplayReady {
		t.Fatalf("inspect = %d TLS findings, ready=%v; want both gates and not ready", gated, inspected.Readiness.ReplayReady)
	}
	if durableRepairSnapshot(t, server, stored.ID).ContentRevision != stored.ContentRevision {
		t.Fatal("a refused or inspect-only request changed the script")
	}

	// A script that is not an import has no import findings to repair.
	authored := authoringAPIScript("stored-repair-authored")
	if _, err := server.runner.CreateScriptAuthoringDurable(authored); err != nil {
		t.Fatal(err)
	}
	_, authoredTag := requireAuthoringAPIProjection(t, server, authored.ID)
	if response := scriptRepairCall(t, server, authored.ID, "preview", ScriptRepairRequest{Operation: ImportRepairOperation{Type: "inspect"}}, authoredTag); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "imported scripts") {
		t.Fatalf("authored script = %d %s, want 409", response.Code, response.Body.String())
	}
	if current := durableRepairSnapshot(t, server, authored.ID); current.Script.Metadata != nil && current.Script.Metadata.Import != nil {
		t.Fatal("a refused repair stamped import metadata on an authored script")
	}

	t.Run("java vuser", func(t *testing.T) {
		javaStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { javaStore.Close() })
		java := &dsl.Script{ID: "stored-repair-java", Name: "Java import", Version: "1", Protocol: "java_vuser", ProjectID: storage.DefaultProjectID,
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: enterpriseArchiveSaveProfile}},
			Actions:  []dsl.Action{{Name: "run", Type: "java_vuser_run"}},
		}
		if !scriptRequiresJavaVuserAuthoringApproval(java) {
			t.Fatal("fixture does not require Java approval")
		}
		if err := javaStore.SaveScript(java); err != nil {
			t.Fatal(err)
		}
		javaServer := NewServer(ServerConfig{Store: javaStore})
		_, javaTag := requireAuthoringAPIProjection(t, javaServer, java.ID)
		if response := scriptRepairCall(t, javaServer, java.ID, "preview", ScriptRepairRequest{Operation: ImportRepairOperation{Type: "inspect"}}, javaTag); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "Java Vuser") {
			t.Fatalf("Java Vuser script = %d %s, want 409", response.Code, response.Body.String())
		}
	})

	t.Run("memory-only store", func(t *testing.T) {
		memory := NewServer(ServerConfig{})
		script := storedRepairScript("stored-repair-memory", "http")
		script.Actions[0].Headers = nil
		if _, err := memory.runner.StoreScriptDurable(script); err != nil {
			t.Fatal(err)
		}
		for _, tag := range []string{"", `"script-authoring-v1:scr-v1-` + strings.Repeat("b", 64) + `"`} {
			response := scriptRepairCall(t, memory, "stored-repair-memory", "apply", selectTLSRepair("init[0]"), tag)
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("memory-only apply (tag %q) = %d %s, want 503", tag, response.Code, response.Body.String())
			}
			// Only this 503 names the missing capability; every other one is
			// masked, so the editor would read a transient failure as this.
			var body struct{ Error, Code string }
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Code != scriptRevisionsUnavailableCode || body.Error != "durable script content revisions unavailable" {
				t.Fatalf("memory-only apply (tag %q) body = %s, want the %s code", tag, response.Body.String(), scriptRevisionsUnavailableCode)
			}
		}
	})
}

// The audit trail names the repair and both revisions but none of its values,
// and the repaired scripts in either response are disclosure-safe. The
// persisted script follows the same contract a GET of the script does.
func TestStoredScriptRepairAuditAndDisclosure(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-audit", "http")
	script.Actions = append(script.Actions, dsl.Action{Name: "cart", Type: "GET", Method: "GET", Target: "/cart/{{var.cart}}"})
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	stored := storeRepairImport(t, server, script)
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	const value = "synthetic-cart-value"
	repair := ScriptRepairRequest{Finding: "missing_producer:cart", ActionPath: "actions[1]", Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: "cart", Type: "list", Values: []string{value}, Update: "once"}}}

	preview := scriptRepairCall(t, server, stored.ID, "preview", repair, tag)
	if preview.Code != http.StatusOK || strings.Contains(preview.Body.String(), storedRepairCredential) {
		t.Fatalf("preview %d disclosed the kept credential or failed: %s", preview.Code, preview.Body.String())
	}
	previewed := decodeScriptRepair(t, preview)
	if previewed.Script == nil || previewed.Before == nil {
		t.Fatal("preview omitted the scripts it compares")
	}
	response := scriptRepairCall(t, server, stored.ID, "apply", repair, tag)
	applied := decodeScriptRepair(t, response)
	// Only the persisted script, checked against a GET below, may carry it.
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	delete(receipt, "stored")
	if raw, _ := json.Marshal(receipt); bytes.Contains(raw, []byte(storedRepairCredential)) {
		t.Fatal("apply disclosed the kept credential outside the persisted script")
	}
	for _, finding := range applied.Findings {
		if finding.Code == "missing_producer:cart" {
			t.Fatalf("the repaired dependency is still reported: %+v", finding)
		}
	}
	projection, _ := requireAuthoringAPIProjection(t, server, stored.ID)
	if !reflect.DeepEqual(applied.Stored.AuthoringProjection, projection) {
		t.Fatal("the persisted script in the response differs from what a GET of the script returns")
	}

	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "script.repair.apply", Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("repair audit events = %d (err %v), want 1", len(events), err)
	}
	event := events[0]
	if event.Status != "success" || event.ResourceType != "script" || event.ResourceID != stored.ID || event.ProjectID != stored.ProjectID {
		t.Fatalf("audit event = %+v", event)
	}
	want := map[string]any{"operation": "add_parameter", "finding": "missing_producer:cart", "action_path": "actions[1]", "content_revision_before": stored.ContentRevision, "content_revision_after": applied.Stored.ContentRevision}
	for key, expected := range want {
		if event.Details[key] != expected {
			t.Errorf("audit %s = %v, want %v", key, event.Details[key], expected)
		}
	}
	details, _ := json.Marshal(event.Details)
	if bytes.Contains(details, []byte(value)) || bytes.Contains(details, []byte(storedRepairCredential)) {
		t.Fatalf("audit details carry a value: %s", details)
	}
}

// The same repair on the same script reports the same findings, readiness and
// fidelity report whether it runs on an import candidate or a saved script.
func TestRepairEvaluationParityImportVsStored(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "stored-parity-test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store, BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	draft := storedRepairScript("stored-repair-parity", "http")
	draft.Actions = append(draft.Actions, dsl.Action{Name: "cart", Type: "GET", Method: "GET", Target: "/cart/{{var.cart}}"})
	if _, err := importreview.RefreshDependencies(draft); err != nil {
		t.Fatal(err)
	}
	stored := storeRepairImport(t, server, cloneScriptDefinition(draft))
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)
	id, _, err := server.registerImportCandidateSession(httptest.NewRequest(http.MethodPost, "/", nil), enterpriseArchiveSaveProfile, draft)
	if err != nil {
		t.Fatal(err)
	}
	token := importDraftProtectedTokenPrefix + id

	for _, action := range []string{"preview", "apply"} {
		repair := selectTLSRepair("init[0]")
		if action == "apply" {
			_, tag = requireAuthoringAPIProjection(t, server, stored.ID)
		}
		body, _ := json.Marshal(ImportRepairRequest{CandidateID: token, Revision: token, ProjectID: draft.ProjectID, Finding: repair.Finding, ActionPath: repair.ActionPath, Operation: repair.Operation})
		importResponse := httptest.NewRecorder()
		server.mux.ServeHTTP(importResponse, httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/repairs/"+action, bytes.NewReader(body)))
		if importResponse.Code != http.StatusOK {
			t.Fatalf("import %s %d: %s", action, importResponse.Code, importResponse.Body.String())
		}
		var imported ImportRepairResponse
		if err := json.Unmarshal(importResponse.Body.Bytes(), &imported); err != nil {
			t.Fatal(err)
		}
		if action == "apply" {
			token = imported.CandidateID
		}
		saved := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, action, repair, tag))

		same := func(label string, a, b any) {
			t.Helper()
			left, _ := json.Marshal(a)
			right, _ := json.Marshal(b)
			if !bytes.Equal(left, right) {
				t.Errorf("%s %s differs:\nimport %s\nstored %s", action, label, left, right)
			}
		}
		same("findings", imported.Findings, saved.Findings)
		same("readiness", imported.Readiness, saved.Readiness)
		same("fidelity_report", imported.FidelityReport, saved.FidelityReport)
		same("secret_binding_requirements", imported.SecretBindingRequirements, saved.SecretBindingRequirements)
		// Apply returns the repaired script only as the persisted copy.
		repaired := saved.Script
		if action == "apply" {
			repaired = saved.Stored.Script
		}
		if repaired.Metadata.Import.Preserved["guided_repair_candidate"] != nil {
			t.Errorf("%s marked the saved script a guided-repair candidate", action)
		}
		same("preserved replay_preflight", preservedReplayPreflight(imported.Script), preservedReplayPreflight(repaired))
	}
}

// A TLS repair on a saved import keeps the converter's dropped-body blocker,
// so the persisted report stays blocked and the launch gate still refuses it.
func TestStoredScriptRepairKeepsConverterFidelityBlockers(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-fidelity", "http")
	script.Metadata.Import.Preserved["fidelity_report"] = map[string]any{
		"dropped_bodies": 1, "promotion_status": "blocked",
		"blockers": []any{droppedBodiesFidelityBlocker, "legacy_tls_replacement at init[0]", "legacy_tls_replacement at init[2]"},
	}
	stored := storeRepairImport(t, server, script)
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	if !applied.Readiness.PromotionReady {
		t.Fatalf("readiness = %+v, want both TLS gates cleared", applied.Readiness)
	}
	assertRepairedFidelityReport(t, "response", applied.FidelityReport, "blocked", droppedBodiesFidelityBlocker)
	current := durableRepairSnapshot(t, server, stored.ID)
	assertRepairedFidelityReport(t, "persisted", current.Script.Metadata.Import.Preserved["fidelity_report"], "blocked", droppedBodiesFidelityBlocker)
	if got := scriptImportPromotionBlockers(current.Script); strings.Join(got, "\n") != droppedBodiesFidelityBlocker {
		t.Fatalf("launch promotion blockers = %q, want the dropped body", got)
	}
}

// The fixture's preserved report has the converter's shape, so promotion and
// launch read it: both TLS gates hold the saved import, and the one apply
// that clears them releases it.
func TestStoredScriptRepairFixtureReportHoldsLaunchUntilRepaired(t *testing.T) {
	t.Setenv(allowBlockedImportEnv, "")
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stored := storeRepairImport(t, server, storedRepairScript("stored-repair-launch", "http"))
	assertRepairedFidelityReport(t, "stored", stored.Script.Metadata.Import.Preserved["fidelity_report"], "blocked", "legacy_tls_replacement at init[0]", "legacy_tls_replacement at init[2]")
	if got := scriptImportPromotionBlockers(stored.Script); strings.Join(got, "\n") != "legacy_tls_replacement at init[0]\nlegacy_tls_replacement at init[2]" {
		t.Fatalf("launch promotion blockers before the repair = %q, want both TLS gates", got)
	}
	if conflicts := importPromotionLaunchConflicts([]TestGroup{{script: stored.Script}}); len(conflicts) != 1 || conflicts[0].Code != "import_promotion_blocked" {
		t.Fatalf("launch conflicts before the repair = %+v, want the import held", conflicts)
	}
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	current := durableRepairSnapshot(t, server, stored.ID)
	assertRepairedFidelityReport(t, "persisted", current.Script.Metadata.Import.Preserved["fidelity_report"], "promotable")
	if got := scriptImportPromotionBlockers(current.Script); len(got) != 0 {
		t.Fatalf("launch promotion blockers after the repair = %q, want none", got)
	}
	if conflicts := importPromotionLaunchConflicts([]TestGroup{{script: current.Script}}); len(conflicts) != 0 {
		t.Fatalf("launch conflicts after the repair = %+v, want none", conflicts)
	}
}

// A placeholder review code is not a blocker: the converter never counts one,
// and no replay refuses it. Repairing one of two placeholders on a saved
// import whose report is promotable keeps it promotable and launchable.
func TestStoredScriptRepairKeepsAPromotableReportPromotable(t *testing.T) {
	t.Setenv(allowBlockedImportEnv, "")
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-promotable", "http")
	script.Init = []dsl.Action{{Name: "home", Type: "GET", Method: "GET", Target: "/"}}
	delete(script.Config.WebRuntime.AdditionalAttributes, "web_set_sockets_option.SSL_VERSION")
	script.Actions = append(script.Actions,
		dsl.Action{Name: "cart", Type: "GET", Method: "GET", Target: "/cart/{{var.cart}}"},
		dsl.Action{Name: "store", Type: "GET", Method: "GET", Target: "/stores/{{var.store}}"})
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	script.Metadata.Import.Preserved["fidelity_report"] = map[string]any{
		"converted_actions": 4, "promotion_status": "promotable",
		"warnings": []any{"missing_producer:cart", "missing_producer:store"},
	}
	attachImportReplayPreflight(script, enterpriseArchiveSaveProfile, buildReplayPreflight(script, nil, validateScript(script), nil))
	stored := storeRepairImport(t, server, script)
	if got := scriptImportPromotionBlockers(stored.Script); len(got) != 0 {
		t.Fatalf("launch promotion blockers before the repair = %q, want none", got)
	}
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	repair := ScriptRepairRequest{Finding: "missing_producer:cart", ActionPath: namedActionPath(t, stored.Script, "cart"), Operation: ImportRepairOperation{Type: "add_parameter", Parameter: &dsl.Parameter{Name: "cart", Type: "list", Values: []string{"synthetic-cart-1001"}, Update: "once"}}}
	applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", repair, tag))
	if !slices.Equal(applied.Readiness.ReviewCodes, []string{"missing_producer:store"}) {
		t.Fatalf("review codes = %q, want the other placeholder left", applied.Readiness.ReviewCodes)
	}
	assertRepairedFidelityReport(t, "response", applied.FidelityReport, "promotable")
	current := durableRepairSnapshot(t, server, stored.ID)
	assertRepairedFidelityReport(t, "persisted", current.Script.Metadata.Import.Preserved["fidelity_report"], "promotable")
	if got := scriptImportPromotionBlockers(current.Script); len(got) != 0 {
		t.Fatalf("launch promotion blockers = %q, want none for a placeholder review code", got)
	}
	if conflicts := importPromotionLaunchConflicts([]TestGroup{{script: current.Script}}); len(conflicts) != 0 {
		t.Fatalf("launch conflicts = %+v, want none", conflicts)
	}
}

// A report the converter blocked on a TLS gate stays blocked while a review
// code remains after the gate is repaired, and the launch refusal names the
// code it is held for rather than only saying the script is held.
func TestStoredScriptRepairNamesTheReviewCodeABlockedReportIsHeldFor(t *testing.T) {
	t.Setenv(allowBlockedImportEnv, "")
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-held", "http")
	script.Actions = append(script.Actions, dsl.Action{Name: "cart", Type: "GET", Method: "GET", Target: "/cart/{{var.cart}}"})
	if _, err := importreview.RefreshDependencies(script); err != nil {
		t.Fatal(err)
	}
	script.Metadata.Import.Preserved["fidelity_report"] = map[string]any{
		"converted_actions": 4, "promotion_status": "blocked",
		"blockers": []any{"legacy_tls_replacement at init[0]", "legacy_tls_replacement at init[2]"},
		"warnings": []any{"missing_producer:cart"},
	}
	stored := storeRepairImport(t, server, script)
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	if len(applied.Readiness.BlockingCodes) != 0 || !slices.Equal(applied.Readiness.ReviewCodes, []string{"missing_producer:cart"}) {
		t.Fatalf("readiness = %+v, want the TLS gates cleared and the placeholder left", applied.Readiness)
	}
	current := durableRepairSnapshot(t, server, stored.ID)
	assertRepairedFidelityReport(t, "persisted", current.Script.Metadata.Import.Preserved["fidelity_report"], "blocked")
	const held = "Imported script needs import review before promotion: missing_producer:cart."
	if got := scriptImportPromotionBlockers(current.Script); !slices.Equal(got, []string{held}) {
		t.Fatalf("launch promotion blockers = %q, want %q", got, held)
	}
	conflicts := importPromotionLaunchConflicts([]TestGroup{{script: current.Script}})
	if len(conflicts) != 1 || conflicts[0].Code != "import_promotion_blocked" || !strings.Contains(conflicts[0].Message, held) {
		t.Fatalf("launch conflicts = %+v, want the refusal to name the review code", conflicts)
	}
}

// A project secret the script reads but that is not configured yet shows in
// the repair's readiness. The persisted report is evaluated from the script
// alone, so configuring the secret later needs no re-import to launch.
func TestStoredScriptRepairPersistsNoProjectSecretState(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := storedRepairScript("stored-repair-secret-state", "http")
	script.Init[1].Headers = map[string]string{"X-Api-Key": "{{secret.WI_SYNTHETIC_REPAIR_API_KEY}}"}
	script.Metadata.Import.Preserved["fidelity_report"] = map[string]any{
		"promotion_status": "blocked",
		"blockers":         []any{"legacy_tls_replacement at init[0]", "legacy_tls_replacement at init[2]"},
	}
	stored := storeRepairImport(t, server, script)
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", selectTLSRepair("init[0]"), tag))
	if !slices.Contains(applied.Readiness.BlockingCodes, "credential_bindings_required") || applied.Readiness.UnresolvedBindings != 1 {
		t.Fatalf("readiness = %+v, want the missing project secret reported", applied.Readiness)
	}
	assertRepairedFidelityReport(t, "response", applied.FidelityReport, "promotable")
	current := durableRepairSnapshot(t, server, stored.ID)
	assertRepairedFidelityReport(t, "persisted", current.Script.Metadata.Import.Preserved["fidelity_report"], "promotable")
	if got := scriptImportPromotionBlockers(current.Script); len(got) != 0 {
		t.Fatalf("launch promotion blockers = %q, want none from project-secret state", got)
	}
}

// A repair on a saved import restamps its preflight against the script's own
// secret binding, as an import repair does against the project, so a proxy
// secret binding to a secret the project holds is not persisted as missing.
func TestStoredScriptRepairRestampedPreflightResolvesProjectSecrets(t *testing.T) {
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "stored-proxy-secret-test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	server := NewServer(ServerConfig{Store: store, BrunoArtifactDir: t.TempDir(), BrunoArtifactWrapper: wrapper})
	const secret = "WI_SYNTHETIC_STORED_REPAIR_PROXY_PW"
	if _, err := server.projectSecretStore.Create(context.Background(), storage.DefaultProjectID, secret, "synthetic-proxy-password", "test"); err != nil {
		t.Fatal(err)
	}
	script := storedRepairScript("stored-repair-proxy-secret", "http")
	script.Config.HTTP.ProxyMode, script.Config.HTTP.Proxy.URL = "custom", "http://proxy.example.test:8080"
	script.Init = append(script.Init, encryptedProxyGateForTest())
	stored := storeRepairImport(t, server, script)
	_, tag := requireAuthoringAPIProjection(t, server, stored.ID)

	repair := ScriptRepairRequest{Finding: "encrypted_proxy_password", ActionPath: "init[3]", Operation: ImportRepairOperation{Type: "replace_proxy_secret", SecretName: secret}}
	applied := decodeScriptRepair(t, scriptRepairCall(t, server, stored.ID, "apply", repair, tag))
	if applied.Readiness.UnresolvedBindings != 0 {
		t.Fatalf("readiness = %+v, want the project's secret resolved", applied.Readiness)
	}
	persisted := preservedReplayPreflight(durableRepairSnapshot(t, server, stored.ID).Script)
	if persisted == nil || !slices.Equal(persisted.RequiredSecrets, []string{secret}) || len(persisted.MissingSecrets) != 0 {
		t.Fatalf("persisted preflight = %+v, want %s required and nothing missing", persisted, secret)
	}
	if detail := replayPreflightCheckDetail(persisted, "secrets"); detail != "Required: "+secret {
		t.Fatalf("persisted secrets check detail = %q, want the secret required, not missing", detail)
	}
}

// Both routes document the required authoring tag and every refusal.
func TestScriptRepairOpenAPIContract(t *testing.T) {
	data, err := GenerateOpenAPIYAML("server.go")
	if err != nil {
		t.Fatal(err)
	}
	var doc oasDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"preview", "apply"} {
		operation := doc.Paths["/api/v1/scripts/{id}/repairs/"+action]["post"]
		required := false
		for _, parameter := range operation.Parameters {
			if parameter.Name == "If-Match" {
				required = parameter.In == "header" && parameter.Required && strings.Contains(parameter.Description, "428") && strings.Contains(parameter.Description, "412")
			}
		}
		if !required {
			t.Fatalf("%s does not require the authoring tag", action)
		}
		for _, status := range []string{"200", "400", "404", "409", "412", "422", "428", "503"} {
			if _, exists := operation.Responses[status]; !exists {
				t.Fatalf("%s is missing the %s response", action, status)
			}
		}
		// The one lasting 503 is told apart from a transient one by its code.
		unavailable := operation.Responses["503"]
		body := unavailable.Content["application/json"].Schema
		if code := body.Properties["code"]; !slices.Equal(code.Enum, []string{scriptRevisionsUnavailableCode}) || !strings.Contains(unavailable.Description, scriptRevisionsUnavailableCode) || !slices.Contains(body.Required, "error") {
			t.Fatalf("%s 503 = %+v, want the %s code documented", action, unavailable, scriptRevisionsUnavailableCode)
		}
		request := operation.RequestBody.Content["application/json"].Schema
		for _, field := range []string{"finding", "action_path", "operation"} {
			if _, exists := request.Properties[field]; !exists {
				t.Fatalf("%s request lacks %s", action, field)
			}
		}
		response := operation.Responses["200"].Content["application/json"].Schema
		for _, field := range []string{"script_id", "content_revision", "script", "before", "findings", "readiness", "fidelity_report", "stored", "validation"} {
			if _, exists := response.Properties[field]; !exists {
				t.Fatalf("%s response lacks %s", action, field)
			}
		}
		// The before/after comparison is the preview's; apply omits it.
		for _, field := range []string{"script", "before"} {
			if slices.Contains(response.Required, field) || !strings.HasPrefix(response.Properties[field].Description, "Preview only") {
				t.Fatalf("%s response %s = required %v, %q; want it optional and documented as preview-only", action, field, response.Required, response.Properties[field].Description)
			}
		}
	}
	apply := doc.Paths["/api/v1/scripts/{id}/repairs/apply"]["post"]
	if description := apply.Responses["200"].Description; !strings.Contains(description, "script and before are omitted") {
		t.Fatalf("apply 200 description = %q, want it to say the preview's scripts are omitted", description)
	}
	if mode := apply.Responses["200"].Headers["X-APEX-Script-Save-Mode"]; len(mode.Schema.Enum) != 1 || mode.Schema.Enum[0] != "conditioned" {
		t.Fatalf("apply save mode = %+v, want conditioned", mode)
	}
}
