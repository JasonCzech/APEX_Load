package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func enterpriseCollectionTestZIP(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for name, data := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func enterpriseCollectionTestProject(t *testing.T, name string, timeout int) []byte {
	t.Helper()
	return enterpriseCollectionTestZIP(t, map[string][]byte{
		name + ".usr": []byte("[General]\nType=Web\nRuntimeSettings=default.cfg\n\n[Actions]\nAction1=Action.c\n"),
		"default.cfg": []byte(fmt.Sprintf("[HTTP]\nConnectTimeout=%d\n", timeout)),
		"Action.c":    []byte("Action() { web_url(\"Request\", \"URL=https://example.test/\", \"Resource=0\", LAST); return 0; }"),
	})
}

func enterpriseCollectionTestRequest(t *testing.T, route string, data []byte, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	projectID := fields["project_id"]
	if projectID == "" {
		projectID = storage.DefaultProjectID
	}
	if err := writer.WriteField("project_id", projectID); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("file", "script-package.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	for name, value := range fields {
		if name == "project_id" {
			continue
		}
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, route, &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func enterpriseCollectionTestScripts(t *testing.T, archives []enterpriseArchiveCollectionMember) []dsl.Script {
	t.Helper()
	var scripts []dsl.Script
	for _, child := range archives {
		if child.Response == nil {
			continue
		}
		data, err := json.Marshal(child.Response["scripts"])
		if err != nil {
			t.Fatal(err)
		}
		var childScripts []dsl.Script
		if err := json.Unmarshal(data, &childScripts); err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, childScripts...)
	}
	return scripts
}

func TestEnterpriseArchiveCollectionPreservesIndependentReviewsAndSavedMetadata(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "collection.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{
		"package/Second.USZ": enterpriseCollectionTestProject(t, "Second", 63),
		"package/First.USZ":  enterpriseCollectionTestProject(t, "First", 21),
		"package/extra.cfg":  []byte("[LooseSetting]\nValue=1\n"),
	})
	for _, suffix := range []string{"", "/validate"} {
		t.Run("route"+suffix, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive"+suffix, outer, map[string]string{
				"name": "Outer package override", "include_assertions": "true", "preserve_transactions": "true",
			}))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("collection draft must disable caching")
			}
			var payload struct {
				Collection bool                                `json:"archive_collection"`
				Archives   []enterpriseArchiveCollectionMember `json:"archives"`
				Scripts    json.RawMessage                     `json:"scripts"`
				Successes  int                                 `json:"successful_count"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			scripts := enterpriseCollectionTestScripts(t, payload.Archives)
			if !payload.Collection || payload.Successes != 2 || len(payload.Archives) != 2 || len(scripts) != 2 || len(payload.Scripts) != 0 {
				t.Fatalf("collection=%v successes=%d archives=%d scripts=%d duplicate scripts=%d", payload.Collection, payload.Successes, len(payload.Archives), len(scripts), len(payload.Scripts))
			}
			for index, name := range []string{"First", "Second"} {
				child := payload.Archives[index]
				if child.SourceName != "package/"+name+".USZ" || child.SourceSize <= 0 || child.Error != "" {
					t.Fatalf("child source = %#v", child)
				}
				warnings, _ := child.Response["warnings"].([]any)
				looseFileReported := false
				for _, warning := range warnings {
					looseFileReported = looseFileReported || strings.Contains(fmt.Sprint(warning), "extra.cfg")
				}
				if !looseFileReported {
					t.Fatal("loose collection settings were silently omitted")
				}
				summary, ok := child.Response["summary"].(map[string]any)
				if !ok || summary["project_file"] != name+".usr" || summary["runtime_settings_parity"] == nil || child.Response["source_review"] == nil {
					t.Fatalf("child review metadata missing or assigned to the wrong project: %#v", summary)
				}
				if scripts[index].Name != name || scripts[index].ProjectID != storage.DefaultProjectID {
					t.Fatalf("child identity = %q/%q", scripts[index].Name, scripts[index].ProjectID)
				}
				if want := []int{21000, 63000}[index]; scripts[index].Config.HTTP.ConnectTimeoutMs != want {
					t.Fatalf("%s connect timeout=%d want %d", name, scripts[index].Config.HTTP.ConnectTimeoutMs, want)
				}
				if got := scripts[index].Metadata.Import.Preserved["source_archive_member"]; got != child.SourceName {
					t.Fatalf("source archive member=%v want %s", got, child.SourceName)
				}
			}
			if suffix != "" {
				return
			}
			saved := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "collection-roundtrip", "scripts": scripts})
			if saved.Code != http.StatusCreated {
				t.Fatalf("save status=%d body=%s", saved.Code, saved.Body.String())
			}
			stored, err := store.ListScripts(storage.DefaultProjectID, nil)
			if err != nil || len(stored) != 2 {
				t.Fatalf("stored scripts=%d error=%v", len(stored), err)
			}
			for _, script := range stored {
				if script.Metadata == nil || script.Metadata.Import == nil || script.Metadata.Import.Preserved["source_review"] == nil || script.Metadata.Import.Preserved["runtime_settings_parity"] == nil {
					t.Fatalf("saved %q lost its completed import review", script.Name)
				}
				if got := script.Metadata.Import.Preserved["source_archive_member"]; got != "package/"+script.Name+".USZ" {
					t.Fatalf("saved %q lost its collection provenance: %v", script.Name, got)
				}
			}
		})
	}
}

func TestEnterpriseArchiveCollectionMemberSelection(t *testing.T) {
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{
		"First.USZ":  enterpriseCollectionTestProject(t, "First", 21),
		"Other.zip":  enterpriseCollectionTestProject(t, "Other", 63),
		"Nested.zip": enterpriseCollectionTestZIP(t, map[string][]byte{"inside.USZ": enterpriseCollectionTestProject(t, "Inside", 21)}),
	})
	for _, test := range []struct{ member, wantName string }{{"First.USZ", "First"}, {"Other.zip", "Other"}, {"../First.USZ", ""}, {"missing.USZ", ""}, {"Nested.zip", ""}} {
		t.Run(test.member, func(t *testing.T) {
			server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, map[string]string{
				"archive_member": test.member, "name": "", "include_assertions": "true",
			}))
			if test.wantName == "" {
				if response.Code != http.StatusBadRequest {
					t.Fatalf("status=%d want 400", response.Code)
				}
				return
			}
			var payload struct {
				Script dsl.Script `json:"script"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || payload.Script.Name != test.wantName || bytes.Contains(response.Body.Bytes(), []byte("archive_collection")) {
				t.Fatalf("selected member did not retain the ordinary review contract: status=%d name=%q", response.Code, payload.Script.Name)
			}
		})
	}
}

func TestEnterpriseArchiveCollectionRejectsLooseSourceBesideNestedProjects(t *testing.T) {
	loose := []byte(`Action() { web_url("Loose", "URL=https://example.test/loose", LAST); return 0; }`)
	first := enterpriseCollectionTestProject(t, "First", 21)
	second := enterpriseCollectionTestProject(t, "Second", 63)
	for _, extensions := range [][2]string{{".USZ", ".USZ"}, {".zip", ".zip"}, {".USZ", ".zip"}} {
		outer := enterpriseCollectionTestZIP(t, map[string][]byte{
			"First" + extensions[0]: first, "Second" + extensions[1]: second, "Action.c": loose,
		})
		for _, suffix := range []string{"", "/validate"} {
			t.Run(strings.Join(extensions[:], "+")+suffix, func(t *testing.T) {
				server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive"+suffix, outer, nil))
				if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "import each nested script archive separately") {
					t.Fatalf("ambiguous packaging status=%d body=%s", response.Code, response.Body.String())
				}
				var payload map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if payload["script"] != nil || payload["scripts"] != nil || server.memoryImportDraftOwner().Len() > 0 || len(server.sourceSecretSessions) > 0 {
					t.Fatal("ambiguous package published a partial executable draft or review capabilities")
				}
			})
		}
	}
}

func TestEnterpriseArchiveCollectionPreservesDirectSourceProjectsWithZIPAssets(t *testing.T) {
	loose := []byte(`Action() { web_url("Loose", "URL=https://example.test/loose", LAST); return 0; }`)
	assets := enterpriseCollectionTestZIP(t, map[string][]byte{"data.txt": []byte("fixture"), "helper.c": []byte("int helper() { return 0; }")})
	for _, anchored := range []bool{false, true} {
		files := map[string][]byte{"Action.c": loose, "library.zip": assets, "more-assets.zip": assets}
		if anchored {
			files["Direct.usr"] = []byte("[General]\nType=Web\n[Actions]\nAction1=Action.c\n")
			// An explicitly declared parent project owns its package boundary.
			files["dependency.USZ"] = enterpriseCollectionTestProject(t, "Dependency", 21)
		}
		outer := enterpriseCollectionTestZIP(t, files)
		for _, suffix := range []string{"", "/validate"} {
			t.Run(fmt.Sprintf("anchored=%v%s", anchored, suffix), func(t *testing.T) {
				if _, collection, err := readEnterpriseArchiveCollection(outer); collection || err != nil {
					t.Fatalf("ordinary project treated as collection: %v %v", collection, err)
				}
				server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive"+suffix, outer, nil))
				var payload struct {
					Script            *dsl.Script `json:"script"`
					ArchiveCollection bool        `json:"archive_collection"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
					t.Fatal(err)
				}
				if response.Code != http.StatusOK || payload.ArchiveCollection || payload.Script == nil || len(payload.Script.Actions) != 1 {
					t.Fatalf("ordinary source project lost its review: status=%d", response.Code)
				}
			})
		}
	}
}

func TestEnterpriseArchiveCollectionSupplementalSettingsRequireOneMember(t *testing.T) {
	first := enterpriseCollectionTestProject(t, "First", 21)
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{
		"First.USZ": first,
		"Other.USZ": enterpriseCollectionTestProject(t, "Other", 63),
	})
	for _, test := range []struct{ member, suffix string }{{"", ""}, {"", "/validate"}, {"First.USZ", ""}, {"First.USZ", "/validate"}} {
		member := test.member
		t.Run("member="+member+test.suffix, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			for _, file := range []struct {
				field, name string
				data        []byte
			}{
				{"file", "package.zip", outer},
				{"supplemental_file", "default.cfg", []byte("[HTTP]\nConnectTimeout=84\n")},
			} {
				part, err := writer.CreateFormFile(file.field, file.name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := part.Write(file.data); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.WriteField("archive_member", member); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive"+test.suffix, &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, request)
			if member == "" {
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "select an archive_member") {
					t.Fatalf("ambiguous settings status=%d body=%s", response.Code, response.Body.String())
				}
				return
			}
			var payload struct {
				Script dsl.Script `json:"script"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || payload.Script.Name != "First" || payload.Script.Config.HTTP.ConnectTimeoutMs != 84000 {
				t.Fatalf("supplemental settings did not apply to the selected source: status=%d name=%q timeout=%d", response.Code, payload.Script.Name, payload.Script.Config.HTTP.ConnectTimeoutMs)
			}
			if payload.Script.Metadata == nil || payload.Script.Metadata.Import == nil {
				t.Fatal("selected member provenance is missing")
			}
			preserved := payload.Script.Metadata.Import.Preserved
			if size, ok := preserved["source_archive_size"].(float64); !ok || size != float64(len(first)) || preserved["source_archive_member"] != member {
				t.Fatalf("supplemental merge changed source archive provenance: size=%v member=%v", preserved["source_archive_size"], preserved["source_archive_member"])
			}
		})
	}
}

func TestEnterpriseArchiveCollectionPartialFailuresRemainReviewable(t *testing.T) {
	server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{
		"valid.USZ":  enterpriseCollectionTestProject(t, "Valid", 21),
		"bad.USZ":    []byte("invalid ZIP"),
		"nested.zip": enterpriseCollectionTestZIP(t, map[string][]byte{"again.USZ": enterpriseCollectionTestProject(t, "Nested", 21)}),
	})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, nil))
	var payload struct {
		Archives  []enterpriseArchiveCollectionMember `json:"archives"`
		Successes int                                 `json:"successful_count"`
		Failures  int                                 `json:"failed_count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload.Successes != 1 || payload.Failures != 2 || len(payload.Archives) != 3 {
		t.Fatalf("partial collection: status=%d successes=%d failures=%d", response.Code, payload.Successes, payload.Failures)
	}
	for _, child := range payload.Archives {
		if child.SourceName == "valid.USZ" && child.Response == nil || child.SourceName != "valid.USZ" && (child.Error == "" || child.Response != nil) {
			t.Fatalf("child disposition=%#v", child)
		}
	}
}

func TestEnterpriseArchiveCollectionAllFailuresRemainReviewable(t *testing.T) {
	server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{"broken.USZ": []byte("invalid ZIP")})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, nil))
	var payload struct {
		Archives  []enterpriseArchiveCollectionMember `json:"archives"`
		Successes int                                 `json:"successful_count"`
		Failures  int                                 `json:"failed_count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload.Successes != 0 || payload.Failures != 1 || len(payload.Archives) != 1 || payload.Archives[0].Error == "" {
		t.Fatalf("all-failed collection: status=%d successes=%d failures=%d", response.Code, payload.Successes, payload.Failures)
	}
}

func TestEnterpriseArchiveCollectionErrorsRemainDisclosureSafe(t *testing.T) {
	server := NewServer(ServerConfig{})
	const syntheticJWT = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJjb2xsZWN0aW9uLWVycm9yLWNhbmFyeSJ9.signature"
	for _, scenario := range []struct {
		name       string
		filename   string
		canary     string
		credential bool
	}{
		// This artificial filename is intentionally compared with the existing
		// single-import contract; it is not a password in source content.
		{"arbitrary quoted filename", "password=collection-error-canary.usr", "collection-error-canary", false},
		{"recognized token query", "diagnostic?token=collection-error-canary.usr", "collection-error-canary", true},
		{"recognized bearer JWT", "Authorization: Bearer " + syntheticJWT + ".usr", syntheticJWT, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			broken := enterpriseCollectionTestZIP(t, map[string][]byte{
				scenario.filename: []byte("[General]\nType=Web\n[Actions]\nAction1=Action.c\n[TransactionsOrder]\nOrder=\"unterminated\n"),
				"Action.c":        []byte("Action() { return 0; }"),
			})
			outer := enterpriseCollectionTestZIP(t, map[string][]byte{"Broken.USZ": broken})
			response := httptest.NewRecorder()
			server.handleImportEnterpriseArchive(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, nil))
			var payload struct {
				Archives []enterpriseArchiveCollectionMember `json:"archives"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || len(payload.Archives) != 1 || payload.Archives[0].Error == "" {
				t.Fatalf("failed member lost its reviewable error: status=%d body=%s", response.Code, response.Body.String())
			}
			single := httptest.NewRecorder()
			server.handleImportEnterpriseArchive(single, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", broken, nil))
			var singleError struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(single.Body.Bytes(), &singleError); err != nil {
				t.Fatal(err)
			}
			if single.Code != http.StatusUnprocessableEntity || singleError.Error == "" || payload.Archives[0].Error != singleError.Error {
				t.Fatalf("collection changed single-import disclosure: status=%d collection=%q single=%q", single.Code, payload.Archives[0].Error, singleError.Error)
			}
			if scenario.credential && strings.Contains(response.Body.String(), scenario.canary) {
				t.Fatal("collection member error disclosed a recognized credential")
			}
		})
	}
}

type enterpriseCollectionCancelResponseWriter struct {
	*httptest.ResponseRecorder
	cancel   context.CancelFunc
	server   *Server
	observed bool
}

func (w *enterpriseCollectionCancelResponseWriter) WriteHeader(status int) {
	w.server.sourceSecretMu.Lock()
	w.observed = len(w.server.sourceSecretSessions) > 0
	w.server.sourceSecretMu.Unlock()
	w.cancel()
	w.ResponseRecorder.WriteHeader(status)
}

type enterpriseCollectionFailResponseWriter struct {
	*httptest.ResponseRecorder
	server   *Server
	observed bool
}

func (w *enterpriseCollectionFailResponseWriter) Write([]byte) (int, error) {
	w.server.sourceSecretMu.Lock()
	w.observed = len(w.server.sourceSecretSessions) > 0
	w.server.sourceSecretMu.Unlock()
	return 0, errors.New("transport closed")
}

func TestEnterpriseArchiveCollectionDiscardsCapabilitiesWhenNotPublished(t *testing.T) {
	credentialSource := enterpriseCollectionTestZIP(t, map[string][]byte{
		"First.usr": []byte("[General]\nType=Web\n[Actions]\nAction1=Action.c\n"),
		"Action.c": []byte(`Action() {
 web_add_header("Authorization", "Bearer collection-fixture-canary");
 web_url("Request", "URL=https://example.test/", "Resource=0", LAST);
 return 0;
}`),
	})
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{
		"First.USZ": credentialSource, "Second.USZ": enterpriseCollectionTestProject(t, "Second", 63),
	})
	for _, scenario := range []string{"canceled while publishing", "response transport failed", "published"} {
		t.Run(scenario, func(t *testing.T) {
			server, _, _, projectID := newProjectSecretAPIServer(t)
			server.protectedSourceSessions = nil
			settings := server.runner.GetSystemSettings()
			settings.ScriptImports.AllowSourceSecretMigration = true
			if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(server.clearAllSourceSecretSessions)
			request := enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, map[string]string{"project_id": projectID})
			response := httptest.NewRecorder()
			switch scenario {
			case "canceled while publishing":
				ctx, cancel := context.WithCancel(request.Context())
				defer cancel()
				writer := &enterpriseCollectionCancelResponseWriter{ResponseRecorder: response, cancel: cancel, server: server}
				server.handleImportEnterpriseArchive(writer, request.WithContext(ctx))
				if !writer.observed || !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("cancellation did not follow retained source allocation: observed=%v context=%v", writer.observed, ctx.Err())
				}
			case "response transport failed":
				writer := &enterpriseCollectionFailResponseWriter{ResponseRecorder: response, server: server}
				server.handleImportEnterpriseArchive(writer, request)
				if !writer.observed {
					t.Fatal("transport failure did not follow retained source allocation")
				}
			case "published":
				server.handleImportEnterpriseArchive(response, request)
				if response.Code != http.StatusOK || len(server.sourceSecretSessions) == 0 || server.memoryImportDraftOwner().Len() == 0 {
					t.Fatalf("published review lost its usable capabilities: status=%d source sessions=%d drafts=%d", response.Code, len(server.sourceSecretSessions), server.memoryImportDraftOwner().Len())
				}
				return
			}
			if len(server.sourceSecretSessions) != 0 || server.memoryImportDraftOwner().Len() != 0 {
				t.Fatalf("unpublished collection retained source sessions=%d drafts=%d", len(server.sourceSecretSessions), server.memoryImportDraftOwner().Len())
			}
		})
	}
}

type enterpriseCollectionDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *enterpriseCollectionDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestEnterpriseArchiveCollectionExtendsOnlyItsWriteDeadline(t *testing.T) {
	server := NewServer(ServerConfig{})
	child := enterpriseCollectionTestProject(t, "Deadline", 21)
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{"Deadline.USZ": child})
	for _, scenario := range []struct {
		name       string
		data       []byte
		collection bool
	}{
		{"collection", outer, true},
		{"ordinary project", child, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			recorder := &enterpriseCollectionDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			// The controller's logging middleware must let ResponseController reach
			// the connection through Unwrap.
			writer := &responseWriter{ResponseWriter: recorder}
			started := time.Now()
			server.handleImportEnterpriseArchive(writer, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", scenario.data, nil))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if !scenario.collection {
				if !recorder.deadline.IsZero() {
					t.Fatalf("ordinary archive changed its write deadline: %v", recorder.deadline)
				}
				return
			}
			minimum := started.Add(enterpriseArchiveCollectionTimeout + enterpriseArchiveCollectionErrorGrace)
			maximum := time.Now().Add(enterpriseArchiveCollectionTimeout + enterpriseArchiveCollectionErrorGrace)
			if recorder.deadline.Before(minimum) || recorder.deadline.After(maximum) {
				t.Fatalf("write deadline=%v want within [%v, %v]", recorder.deadline, minimum, maximum)
			}
		})
	}
}

func TestEnterpriseArchiveCollectionRequestBudgetHonorsCallerContext(t *testing.T) {
	for _, scenario := range []string{"bounded default", "earlier deadline", "elapsed caller deadline", "caller cancellation"} {
		t.Run(scenario, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
			var callerDeadline time.Time
			var wantErr error
			switch scenario {
			case "earlier deadline", "elapsed caller deadline":
				callerDeadline = time.Now().Add(time.Minute)
				if scenario == "elapsed caller deadline" {
					callerDeadline = time.Now().Add(-time.Second)
					wantErr = context.DeadlineExceeded
				}
				ctx, cancel := context.WithDeadline(request.Context(), callerDeadline)
				defer cancel()
				request = request.WithContext(ctx)
			case "caller cancellation":
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
				wantErr = context.Canceled
			}
			recorder := &enterpriseCollectionDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			started := time.Now()
			bounded, cancel := enterpriseArchiveCollectionRequest(recorder, request)
			defer cancel()
			deadline, ok := bounded.Context().Deadline()
			if !ok {
				t.Fatal("collection conversion has no finite context deadline")
			}
			if !callerDeadline.IsZero() {
				if !deadline.Equal(callerDeadline) {
					t.Fatalf("caller deadline=%v conversion deadline=%v", callerDeadline, deadline)
				}
			} else if deadline.Before(started.Add(enterpriseArchiveCollectionTimeout)) || deadline.After(time.Now().Add(enterpriseArchiveCollectionTimeout)) {
				t.Fatalf("default conversion deadline=%v", deadline)
			}
			if !recorder.deadline.Equal(deadline.Add(enterpriseArchiveCollectionErrorGrace)) {
				t.Fatalf("error response grace lost: conversion=%v write=%v", deadline, recorder.deadline)
			}
			if !errors.Is(bounded.Context().Err(), wantErr) {
				t.Fatalf("context error=%v want %v", bounded.Context().Err(), wantErr)
			}
		})
	}
}

func TestEnterpriseArchiveCollectionCanceledBeforeConversionDoesNotAllocateDrafts(t *testing.T) {
	server := NewServer(ServerConfig{})
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{"Deadline.USZ": enterpriseCollectionTestProject(t, "Deadline", 21)})
	for _, timeout := range []bool{false, true} {
		request := enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, nil)
		var ctx context.Context
		var cancel context.CancelFunc
		if timeout {
			ctx, cancel = context.WithDeadline(request.Context(), time.Now().Add(-time.Second))
		} else {
			ctx, cancel = context.WithCancel(request.Context())
			cancel()
		}
		response := httptest.NewRecorder()
		server.handleImportEnterpriseArchive(response, request.WithContext(ctx))
		cancel()
		if response.Code != http.StatusRequestTimeout || server.memoryImportDraftOwner().Len() != 0 || len(server.sourceSecretSessions) != 0 {
			t.Fatalf("canceled collection: timeout=%v status=%d drafts=%d sources=%d", timeout, response.Code, server.memoryImportDraftOwner().Len(), len(server.sourceSecretSessions))
		}
	}
}

func TestEnterpriseArchiveCollectionCleanupUsesUncanceledProtectedScope(t *testing.T) {
	server, records, _, projectID := newProjectSecretAPIServer(t)
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x33}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	server.brunoArtifactWrapper = wrapper
	server.importAssetDir = t.TempDir()
	server.protectedSourceSessions = &secretstore.ProtectedSourceSessionStore{Records: records, Wrapper: wrapper}
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil).WithContext(ctx)
	script := attestedImportTestScript("Protected collection cleanup", "protected-collection-canary")
	script.ProjectID = projectID
	if _, err := server.attachImportDraftAttestationWithExpirationPolicy(request, script, true); err != nil {
		t.Fatal(err)
	}
	copyForClaim := cloneScriptDefinition(script)
	if !strings.HasPrefix(importDraftAttestationToken(copyForClaim), importDraftProtectedTokenPrefix) {
		t.Fatal("test did not allocate a protected non-expiring draft")
	}
	sessionID, err := server.registerSourceSecretSession(request, projectID, map[string]string{"requirement": "protected-collection-canary"})
	if err != nil || sessionID == "" {
		t.Fatalf("source session=%q error=%v", sessionID, err)
	}
	cancel()
	server.discardUnpublishedEnterpriseArchiveCollection(request, projectID, []*dsl.Script{script}, []string{sessionID})
	cleanupRequest := request.WithContext(context.WithoutCancel(request.Context()))
	if claim, err := server.claimImportDraftAttestation(cleanupRequest, copyForClaim); err == nil {
		server.releaseImportDraftAttestation(claim)
		t.Fatal("canceled collection left its protected draft claimable")
	}
	if _, err := server.consumeSourceSecretSession(cleanupRequest, projectID, sessionID); !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("source session remained after canceled collection cleanup: %v", err)
	}
}

func TestEnterpriseArchiveCollectionKeepsJavaKafkaSettingsWithItsGovernedDraft(t *testing.T) {
	server := NewServer(ServerConfig{ImportAssetDir: t.TempDir()})
	java := buildJavaEnterpriseArchive(t, "KafkaSource", "Java Vuser", `
import java.util.Properties;
import org.apache.kafka.clients.producer.KafkaProducer;
public class Actions {
  public int init() { return 0; }
  public int action() {
    Properties props = new Properties();
    props.put("bootstrap.servers", "kafka.example.test:9092");
    props.put("acks", "all");
    props.put("linger.ms", "4");
    KafkaProducer<String, String> producer = new KafkaProducer<>(props);
    producer.close();
    return 0;
  }
  public int end() { return 0; }
}`)
	outer := enterpriseCollectionTestZIP(t, map[string][]byte{
		"KafkaSource.USZ": java, "HTTP.USZ": enterpriseCollectionTestProject(t, "HTTP", 21),
	})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, enterpriseCollectionTestRequest(t, "/api/v1/scripts/import/enterprise-script-archive", outer, nil))
	var payload struct {
		Archives []enterpriseArchiveCollectionMember `json:"archives"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	scripts := enterpriseCollectionTestScripts(t, payload.Archives)
	if response.Code != http.StatusOK || len(scripts) != 2 || len(payload.Archives) != 2 {
		t.Fatalf("status=%d scripts=%d archives=%d body=%s", response.Code, len(scripts), len(payload.Archives), response.Body.String())
	}
	var javaDraft *dsl.Script
	for index := range scripts {
		script := &scripts[index]
		preserved := script.Metadata.Import.Preserved
		if script.Protocol == "java_vuser" {
			javaDraft = script
			report, ok := preserved[enterprisearchive.JavaKafkaSettingsMetadataKey].(map[string]any)
			if !ok || report["native_migration_ready"] != false {
				t.Fatalf("governed Kafka settings report missing or inaccurately runnable: %#v", report)
			}
			settings, ok := report["settings"].([]any)
			if !ok || len(settings) != 3 {
				t.Fatalf("Kafka settings inventory=%#v", report["settings"])
			}
			for _, item := range settings {
				setting := item.(map[string]any)
				if setting["source_file"] == "" || setting["start_line"].(float64) <= 0 || setting["go_support"] == "" {
					t.Fatalf("Kafka setting lost its review location/support: %#v", setting)
				}
			}
		} else if preserved[enterprisearchive.JavaKafkaSettingsMetadataKey] != nil {
			t.Fatal("Kafka settings were assigned to the HTTP sibling")
		}
	}
	if javaDraft == nil {
		t.Fatal("Java source lost its governed draft")
	}
	saved := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "collection-java-governed", "scripts": []dsl.Script{*javaDraft}})
	if saved.Code != http.StatusConflict || !strings.Contains(saved.Body.String(), "authoring-session") {
		t.Fatalf("Java approval gate was bypassed: status=%d body=%s", saved.Code, saved.Body.String())
	}
}

func TestEnterpriseArchiveCollectionBoundsAndUnsafePaths(t *testing.T) {
	valid := enterpriseCollectionTestProject(t, "Bounded", 21)
	tooMany := map[string][]byte{}
	for i := 0; i <= maxEnterpriseArchiveCollectionMembers; i++ {
		tooMany[fmt.Sprintf("%03d.USZ", i)] = []byte("invalid ZIP")
	}
	// Declared sizes are bounded across all child archives before conversion,
	// including a malicious child whose compressed content is tiny.
	oversized := append([]byte(nil), valid...)
	largeDeclared := append([]byte(nil), valid...)
	for offset := 0; offset+46 <= len(oversized); offset++ {
		if binary.LittleEndian.Uint32(oversized[offset:]) == 0x02014b50 {
			binary.LittleEndian.PutUint32(oversized[offset+24:], uint32(enterprisearchive.MaxArchiveTotalBytes))
			binary.LittleEndian.PutUint32(largeDeclared[offset+24:], uint32(enterprisearchive.MaxArchiveEntryBytes))
			break
		}
	}
	combinedBytes := map[string][]byte{}
	for index := 0; index < 9; index++ {
		combinedBytes[fmt.Sprintf("large-%d.USZ", index)] = largeDeclared
	}
	entryFiles := map[string][]byte{}
	for index := 0; index < enterprisearchive.MaxArchiveEntries/2+1; index++ {
		entryFiles[fmt.Sprintf("file-%04d.txt", index)] = []byte("x")
	}
	manyEntries := enterpriseCollectionTestZIP(t, entryFiles)
	for _, test := range []struct {
		name    string
		files   map[string][]byte
		message string
	}{
		{"unsafe outer path", map[string][]byte{"../unsafe.USZ": valid}, "unsafe"},
		{"case collision", map[string][]byte{"A.USZ": valid, "a.usz": valid}, "colliding"},
		{"member count", tooMany, "at most"},
		{"decompression budget", map[string][]byte{"large.USZ": oversized}, "decompression limit"},
		{"combined decompression budget", combinedBytes, "decompression limit"},
		{"combined entry budget", map[string][]byte{"first.USZ": manyEntries, "second.USZ": manyEntries}, "combined entry count"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, collection, err := readEnterpriseArchiveCollection(enterpriseCollectionTestZIP(t, test.files))
			if !collection || err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("collection=%v error=%v want %q", collection, err, test.message)
			}
		})
	}
	if _, collection, err := readEnterpriseArchiveCollection(valid); collection || err != nil {
		t.Fatalf("ordinary script archive treated as collection: %v %v", collection, err)
	}
}
