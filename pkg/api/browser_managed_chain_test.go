package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Drive authoring and the actual local engine through authenticated controller
// routes. No archive writer or protocol result is substituted by a fixture.
func TestBrowserManagedAuthorReplayRunArchiveHARChain(t *testing.T) {
	for _, tc := range []struct{ engine, profile string }{{"chromium", ""}, {"chromium", "pixel_8"}, {"webkit", ""}, {"webkit", "iphone_15_pro"}} {
		name := tc.profile
		if name == "" {
			name = "desktop"
		}
		t.Run(tc.engine+"/"+name, func(t *testing.T) { browserManagedAuthorReplayRunArchiveHARChain(t, tc.engine, tc.profile) })
	}
}

func browserManagedAuthorReplayRunArchiveHARChain(t *testing.T, browserEngine, profile string) {
	if os.Getenv("APEXLOAD_BROWSER_TESTS") != "require" {
		t.Skip("requires installed native browsers")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	var mu sync.Mutex
	counts := map[string]int{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="icon" href="data:,"><link rel="stylesheet" href="/style.css"><h1>Managed browser chain</h1><button id="go" onclick="fetch('/clicked').then(r=>r.text()).then(()=>document.querySelector('#done').hidden=false)">Go</button><div id="done" hidden>Done</div>`)
		case "/style.css":
			w.Header().Set("Content-Type", "text/css")
			fmt.Fprint(w, "button{width:140px;height:60px}")
		case "/clicked":
			fmt.Fprint(w, "ok")
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(target.Close)
	server, store := baselineTestServer(t)
	enableTrustedHeaderAuth(t, server)
	settings := server.runner.GetSystemSettings()
	settings.ArchiveRetention.HARArtifactDir = t.TempDir()
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	otherProject, err := store.GetProject(storage.DefaultProjectID)
	if err != nil || otherProject == nil {
		t.Fatalf("default project: %v", err)
	}
	otherProject.ID, otherProject.Name = "another-project", "Other browser fixture"
	if err := store.SaveProject(otherProject); err != nil {
		t.Fatal(err)
	}
	identities := map[string]string{}
	for index, roles := range []string{harEditor, harViewer, harOtherEditor} {
		parts := strings.SplitN(roles, "=", 2)
		id := fmt.Sprintf("managed-browser-user-%d", index)
		now := time.Now().UTC()
		if err := store.SaveUser(&storage.User{ID: id, Subject: id, Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{ID: id + "-binding", UserID: id, ScopeType: "project", ScopeID: parts[0], Role: parts[1], CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		identities[roles] = id
	}
	call := func(method, path string, body any, role string, want int, out any) *httptest.ResponseRecorder {
		t.Helper()
		var data []byte
		var err error
		if body != nil {
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", identities[role])
		req.Header.Set("X-APEXLoad-Project-Roles", role)
		reply := httptest.NewRecorder()
		server.mux.ServeHTTP(reply, req)
		if reply.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%.1600s", method, path, reply.Code, want, reply.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(reply.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
		return reply
	}
	on := true
	one := 1
	script := &dsl.Script{Name: "Managed native browser chain", Protocol: "browser", Version: "1.0.0", ProjectID: storage.DefaultProjectID,
		Config:  dsl.ScriptConfig{BaseURL: target.URL, Browser: dsl.BrowserConfig{Engine: browserEngine, DeviceProfile: profile, Headless: &on, CaptureDocumentMetrics: &on, ClientMetrics: &on, CaptureInteractions: &on, HARCapture: "always", HARArtifactDir: t.TempDir()}},
		Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/"}, {Name: "activate", Type: "browser_click", Selector: "#go", Sync: &dsl.BrowserSyncPolicy{Type: "element_visible", Value: "#done", TimeoutMs: 2000}}},
	}
	var authored StoredScript
	call("POST", "/api/v1/scripts", map[string]any{"script": script}, harEditor, 201, &authored)
	if authored.ID == "" {
		t.Fatal("authoring did not persist a script")
	}
	var replay BrowserReplayStartResponse
	call("POST", "/api/v1/scripts/"+authored.ID+"/browser-replay/sessions", BrowserReplayStartRequest{RecordHAR: &on}, harEditor, 200, &replay)
	terminal := waitBrowserReplayTerminal(t, server.browserReplay, replay.ReplaySessionID, 60*time.Second)
	if terminal.Status != BrowserReplayCompleted {
		t.Fatalf("native replay failed: %s %s", terminal.Status, terminal.Error)
	}
	replayBase := "/api/v1/browser-replay/sessions/" + replay.ReplaySessionID
	replayHAR := call("GET", replayBase+"/har", nil, harEditor, 200, nil)
	if replayHAR.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" {
		t.Fatal("replay silently substituted a synthetic HAR")
	}
	replayZIP := call("GET", replayBase+"/evidence", nil, harEditor, 200, nil)
	if !bytes.Equal(harZipEntries(t, replayZIP.Body.Bytes())["replay.har"], replayHAR.Body.Bytes()) {
		t.Fatal("replay ZIP changed native bytes")
	}
	call("GET", replayBase+"/har", nil, harViewer, 403, nil)
	var run ManagedTest
	call("POST", "/api/v1/tests", CreateTestRequest{Name: "Managed native chain", ProjectID: storage.DefaultProjectID, TerminationMode: "iterations", Groups: []CreateGroupEntry{{Name: "browser", ScriptID: authored.ID, VUsers: 1, Iterations: &one}}}, harEditor, 201, &run)
	call("POST", "/api/v1/tests/"+run.ID+"/start", map[string]any{}, harEditor, 200, nil)
	waitForTestStatusWithin(t, server.runner, run.ID, 90*time.Second, "COMPLETE")
	var manifest *RunArchiveManifest
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		record, err := store.GetRunArchive(run.ID)
		if err != nil || record == nil || record.ManifestSHA256 == "" {
			continue
		}
		manifest, err = loadRunArchiveManifestFromRecord(record)
		if err == nil && manifest.BrowserDocumentsFile != "" && len(manifest.BrowserHARArtifacts) > 0 {
			break
		}
	}
	if manifest == nil || manifest.BrowserDocumentsFile == "" || len(manifest.BrowserHARArtifacts) != 1 {
		t.Fatalf("managed archive lacks native document/HAR evidence: manifest_present=%t", manifest != nil)
	}
	base := "/api/v1/tests/" + run.ID + "/archive"
	resourceReply := call("GET", base+"/browser-resources", nil, harViewer, 200, nil)
	resources, err := DecodeBrowserResourcePage(resourceReply.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	wantResources := 3
	if resources.SourceIntegrity != "verified_manifest_and_artifact" || len(resources.Items) != wantResources {
		urls := make([]string, 0, len(resources.Items))
		for _, item := range resources.Items {
			urls = append(urls, item.URL)
		}
		t.Fatalf("resource integrity=%s retained_urls=%v", resources.SourceIntegrity, urls)
	}
	var list BrowserHARArchiveList
	call("GET", base+"/browser/har", nil, harViewer, 200, &list)
	if list.CanDownload || list.DownloadRole != RoleEditor || list.Incomplete {
		t.Fatalf("viewer HAR listing authorization/coverage: %+v", list)
	}
	native := manifest.BrowserHARArtifacts[0]
	var report RunArchiveReport
	documentsReply := call("GET", base+"/report", nil, harViewer, 200, &report)
	if report.BrowserDocumentEvidenceTruncated {
		t.Fatal("fixture document report is truncated")
	}
	latest := map[string]protocol.BrowserDocumentObservation{}
	for _, observation := range report.BrowserDocumentEvidence {
		if !observation.Valid() || observation.SourceGeneration != native.EvidenceGeneration {
			t.Fatal("invalid document observation or HAR/source mismatch")
		}
		if _, exists := latest[observation.MeasurementID]; exists {
			t.Fatal("duplicate report measurement")
		}
		latest[observation.MeasurementID] = observation
	}
	if len(latest) == 0 {
		t.Fatal("no native document measurements")
	}
	documentIDs := map[string]bool{}
	for _, observation := range latest {
		if observation.Engine != browserEngine || observation.DeviceProfile != profile || observation.Cohort == nil || !observation.Cohort.Valid() || observation.Cohort.Platform != runtime.GOOS || observation.State == "provisional" {
			t.Fatalf("document cohort/finality: %+v", observation)
		}
		documentIDs[observation.DocumentID] = true
	}
	joined := false
	documentJoined := false
	seenURLs := map[string]int{}
	for _, item := range resources.Items {
		seenURLs[item.URL]++
		if item.BrowserEngine != browserEngine || item.DeviceProfile != profile || item.StatusCode == nil || *item.StatusCode != 200 {
			t.Fatalf("resource cohort/status: engine=%q profile=%q status=%v", item.BrowserEngine, item.DeviceProfile, item.StatusCode)
		}
		if item.BrowserEvidence != nil && item.BrowserEvidence.SourceGeneration == native.EvidenceGeneration {
			joined = true
			documentJoined = documentJoined || documentIDs[item.BrowserEvidence.DocumentID]
		}
	}
	wantNavigation := 1
	if seenURLs[target.URL+"/"] != wantNavigation || seenURLs[target.URL+"/style.css"] != 1 || seenURLs[target.URL+"/clicked"] != 1 {
		t.Fatalf("retained subresource population: %v", seenURLs)
	}
	if !joined || !documentJoined {
		t.Fatal("retained resource did not join native HAR generation")
	}
	artifactPath := base + "/browser/har/" + native.Name
	downloaded := call("GET", artifactPath, nil, harEditor, 200, nil)
	sum := sha256.Sum256(downloaded.Body.Bytes())
	if hex.EncodeToString(sum[:]) != native.SHA256 || int64(downloaded.Body.Len()) != native.Bytes || downloaded.Header().Get("X-APEXLoad-HAR-Fidelity") != "native" {
		t.Fatal("native download differs from finalized receipt")
	}
	if !strings.Contains(downloaded.Body.String(), target.URL+"/clicked") {
		t.Fatal("native HAR omits controlled action request")
	}
	call("GET", artifactPath, nil, harViewer, 403, nil)
	call("GET", artifactPath, nil, harOtherEditor, 403, nil)
	for _, item := range resources.Items {
		if item.BrowserEvidence == nil || item.BrowserEvidence.RequestID == "" {
			t.Fatal("resource request identity unavailable")
		}
		entryPath := base + "/browser/har-entry?request_id=" + item.BrowserEvidence.RequestID
		var entry BrowserHAREntryResponse
		call("GET", entryPath, nil, harEditor, 200, &entry)
		if entry.Fidelity != "native" || entry.Artifact != native.Name || entry.SourceGeneration != native.EvidenceGeneration {
			t.Fatalf("native HAR drill-down: %+v", entry)
		}
		call("GET", entryPath, nil, harViewer, 403, nil)
	}
	archiveZIP := call("GET", base+"?include=har", nil, harEditor, 200, nil)
	entries := harZipEntries(t, archiveZIP.Body.Bytes())
	if !bytes.Equal(entries[native.Name], downloaded.Body.Bytes()) {
		t.Fatal("archive ZIP changed native HAR bytes")
	}
	call("GET", base+"?include=har", nil, harViewer, 403, nil)
	call("GET", base+"/browser-resources", nil, harOtherEditor, 403, nil)
	call("POST", "/api/v1/scripts", map[string]any{"script": script}, harViewer, 403, nil)
	// Replay and managed execution must bind the same authored definition even
	// though their controller-owned HAR directories and runtime options differ.
	if terminal.FinalReport == nil || len(terminal.FinalReport.Events) != 2 {
		t.Fatal("replay event proof unavailable")
	}
	replayStep := terminal.FinalReport.Events[1].AuthoredStep
	if !replayStep.Valid() || replayStep.Version != protocol.BrowserOriginalAuthoredStepVersion || replayStep.StepPath != "actions[1]" {
		t.Fatalf("replay source proof=%+v", replayStep)
	}
	sourceDefinition := manifest.Provenance.BrowserAuthoredSources["browser"]
	if !sourceDefinition.Valid() || sourceDefinition.ScriptSHA256 != replayStep.ScriptSHA256 || sourceDefinition.StructureSHA256 != replayStep.SourceStructureSHA256 {
		t.Fatal("archive lost original source provenance")
	}
	archiveRecord, err := store.GetRunArchive(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	matches := 0
	if _, err := forEachBrowserInteractionRecord(filepath.Join(archiveRecord.ArchivePath, manifest.BrowserInteractionsFile), func(e *protocol.BrowserInteractionEvidence) error {
		for _, interaction := range e.Interactions {
			if !reflect.DeepEqual(interaction.AuthoredStep, replayStep) {
				t.Fatalf("managed/replay original step mismatch: managed=%+v replay=%+v", interaction.AuthoredStep, replayStep)
			}
			matches++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if matches != 1 {
		t.Fatalf("original source interaction population=%d", matches)
	}
	var interactions BrowserInteractionAnalysis
	call("GET", base+"/browser-interactions", nil, harViewer, 200, &interactions)
	wantEligible := int64(1)
	if profile != "" {
		wantEligible = 0
	}
	if interactions.Version != browserInteractionAnalysisVersion3 || interactions.Coverage.Interactions != 1 || interactions.Coverage.PatternEligible != wantEligible {
		t.Fatalf("managed native dispatch did not qualify: %+v", interactions)
	}
	if profile != "" && interactions.Coverage.PatternExcluded["dispatch_unavailable"] != 1 {
		t.Fatalf("synthetic mobile dispatch was not excluded: %+v", interactions.Coverage)
	}
	if root := os.Getenv("APEX_BROWSER_MANAGED_EVIDENCE_DIR"); root != "" {
		name := browserEngine + "-" + profile
		if profile == "" {
			name += "desktop"
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		for suffix, body := range map[string][]byte{".zip": archiveZIP.Body.Bytes(), "-replay.zip": replayZIP.Body.Bytes(), "-resources.json": resourceReply.Body.Bytes(), "-report.json": documentsReply.Body.Bytes()} {
			if err := os.WriteFile(filepath.Join(root, name+suffix), body, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["/"] != 2 || counts["/clicked"] != 2 || counts["/style.css"] != 2 || len(counts) != 3 {
		t.Fatalf("unexpected target traffic: %+v", counts)
	}
	t.Logf("engine=%s emulated_profile=%q authenticated author→native replay+ZIP→managed one-iteration run→verified resources→native HAR receipt; requests=%v eligible_dispatch=%d", browserEngine, profile, counts, interactions.Coverage.PatternEligible)
}
