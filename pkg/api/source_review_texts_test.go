package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func sourceReviewTextTestServer(t *testing.T, projectIDs ...string) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "source-review-texts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	for _, projectID := range projectIDs {
		if err := store.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	return NewServer(ServerConfig{Store: store, ImportAssetDir: t.TempDir()})
}

// longActionSource is a mapped file well past the review's preview limit,
// with a distinct line where its mapping points.
func longActionSource() (string, int) {
	var builder strings.Builder
	const lines = 9000
	for line := 1; line <= lines; line++ {
		fmt.Fprintf(&builder, "\tweb_url(\"step_%d\", \"URL=https://shop.example.test/%d\", LAST);\r\n", line, line)
	}
	return builder.String(), lines + 1
}

func getSourceReviewText(t *testing.T, server *Server, projectID, digest string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+projectID+"/source-review/texts/"+digest, nil)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func TestImportResponseStoresMappedSourceTextThatSurvivesSave(t *testing.T) {
	const projectID = "project-source-text"
	server := sourceReviewTextTestServer(t, projectID, "project-other")
	action, actionLines := longActionSource()
	sum := sha256.Sum256([]byte(action))
	digest := hex.EncodeToString(sum[:])
	review := sourceview.Build(map[string]string{"Action.c": action}, []sourceview.Mapping{
		{SourceFile: "Action.c", StartLine: 8000, Function: "web_url", TargetPath: "actions[0]", Status: "converted"},
	})
	script := &dsl.Script{
		Name: "Long LoadRunner", Version: "1.0.0", Protocol: "http", ProjectID: projectID,
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: enterpriseArchiveSaveProfile, Preserved: map[string]any{"source_review": review}}},
		Config:   dsl.ScriptConfig{BaseURL: "https://shop.example.test"},
		Actions:  []dsl.Action{{Name: "Request", Type: "request", Method: http.MethodGet, Target: "/8000"}},
	}

	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive?project_id="+projectID, nil),
		scriptImportResponseOptions{Script: script, Scripts: []*dsl.Script{script}, SourceReview: review},
	)
	file := review.SourceFiles[0]
	if !file.Truncated || file.FullSHA256 != digest || file.FullBytes != len(action) || file.FullLines != actionLines || file.FullText != "" {
		t.Fatalf("review file = truncated %v sha %q bytes %d lines %d text kept %v, want the stored text's hash %q, %d bytes, %d lines and no in-memory copy",
			file.Truncated, file.FullSHA256, file.FullBytes, file.FullLines, file.FullText != "", digest, len(action), actionLines)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(encoded), `"full_sha256":"`+digest+`"`) < 2 {
		t.Fatalf("response names the full text %d times, want the review and the draft", strings.Count(string(encoded), digest))
	}

	// The hash is part of the attested draft: saving it unchanged succeeds and
	// keeps it.
	masked, ok := preview["script"].(map[string]any)
	if !ok {
		t.Fatalf("preview script = %T", preview["script"])
	}
	saved := postEnterpriseArchiveBatch(t, server, map[string]any{"operation_id": "source-text-save", "scripts": []any{masked}})
	if saved.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%s", saved.Code, saved.Body.String())
	}
	var body struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(saved.Body.Bytes(), &body); err != nil || len(body.Scripts) != 1 {
		t.Fatalf("save response = %s (%v)", saved.Body.String(), err)
	}
	stored, ok := server.runner.GetScript(body.Scripts[0].ID)
	if !ok || stored.Script.Metadata == nil || stored.Script.Metadata.Import == nil {
		t.Fatal("saved script not found")
	}
	storedReview := sourceReviewValue(stored.Script.Metadata.Import.Preserved["source_review"])
	if storedReview == nil || len(storedReview.SourceFiles) != 1 || storedReview.SourceFiles[0].FullSHA256 != digest {
		t.Fatalf("saved review = %#v, want Action.c to name its full text", storedReview)
	}
	if len(storedReview.SourceFiles[0].Content) > sourceview.MaxFileChars+64 {
		t.Fatalf("saved preview is %d bytes, want the bounded preview", len(storedReview.SourceFiles[0].Content))
	}

	response := getSourceReviewText(t, server, projectID, digest, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("text status = %d body=%s", response.Code, response.Body.String())
	}
	var text sourceReviewTextResponse
	if err := json.Unmarshal(response.Body.Bytes(), &text); err != nil {
		t.Fatal(err)
	}
	if text.Content != action || text.SHA256 != digest || text.SizeBytes != len(action) || text.Lines != actionLines {
		t.Fatalf("text = sha %q bytes %d lines %d exact %v, want the authored file", text.SHA256, text.SizeBytes, text.Lines, text.Content == action)
	}
	if !strings.Contains(text.Content, `web_url("step_8000"`) {
		t.Fatal("text does not reach the mapped line")
	}
	if got := response.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}

	revalidated := getSourceReviewText(t, server, projectID, digest, http.Header{"If-None-Match": {response.Header().Get("ETag")}})
	if revalidated.Code != http.StatusNotModified || revalidated.Body.Len() != 0 {
		t.Fatalf("revalidation status = %d body=%q, want 304", revalidated.Code, revalidated.Body.String())
	}
	if other := getSourceReviewText(t, server, "project-other", digest, nil); other.Code != http.StatusNotFound {
		t.Fatalf("other project status = %d, want 404 for text another project stored", other.Code)
	}
	if malformed := getSourceReviewText(t, server, projectID, "not-a-digest", nil); malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed digest status = %d, want 400", malformed.Code)
	}
}

func TestRetainSourceReviewTextsSharesStoredTextAndRejectsCorruption(t *testing.T) {
	const projectID = "project-source-text-dedupe"
	server := sourceReviewTextTestServer(t, projectID)
	action, _ := longActionSource()
	mappings := []sourceview.Mapping{{SourceFile: "Action.c", StartLine: 8000, Status: "converted"}}
	first := sourceview.Build(map[string]string{"Action.c": action}, mappings)
	second := sourceview.Build(map[string]string{"Action.c": action}, mappings)

	server.retainSourceReviewTexts(t.Context(), projectID, []*sourceview.Review{first, second})
	digest := first.SourceFiles[0].FullSHA256
	if digest == "" || second.SourceFiles[0].FullSHA256 != digest {
		t.Fatalf("hashes = %q and %q, want both copies to name one stored text", digest, second.SourceFiles[0].FullSHA256)
	}
	path, err := server.sourceReviewTextPath(projectID, digest)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("stored entries = %v (%v), want one text and no temporary files", entries, err)
	}

	// Importing the same source again reuses the text rather than writing it.
	before, _ := os.Stat(path)
	again := sourceview.Build(map[string]string{"Action.c": action}, mappings)
	server.retainSourceReviewTexts(t.Context(), projectID, []*sourceview.Review{again})
	after, _ := os.Stat(path)
	if again.SourceFiles[0].FullSHA256 != digest || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("re-import rewrote the stored text")
	}

	if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if response := getSourceReviewText(t, server, projectID, digest, nil); response.Code != http.StatusInternalServerError {
		t.Fatalf("tampered text status = %d body=%s, want 500", response.Code, response.Body.String())
	}
}

func TestImportResponseWithoutProjectKeepsOnlyThePreview(t *testing.T) {
	server := sourceReviewTextTestServer(t, "project-scoped")
	action, _ := longActionSource()
	review := sourceview.Build(map[string]string{"Action.c": action}, []sourceview.Mapping{{SourceFile: "Action.c", StartLine: 8000, Status: "converted"}})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	// A request scoped to another project than its drafts stores nothing.
	request = request.WithContext(context.WithValue(request.Context(), authorizedProjectContextKey{}, "project-scoped"))
	server.retainImportResponseSourceTexts(request, &scriptImportResponseOptions{
		SourceReview: review,
		Script:       &dsl.Script{Name: "Other", ProjectID: "project-elsewhere"},
	})
	if file := review.SourceFiles[0]; file.FullSHA256 != "" || file.FullText != "" {
		t.Fatalf("file = sha %q text kept %v, want the preview only", file.FullSHA256, file.FullText != "")
	}

	// A response for scripts the server already saved cannot reach them.
	saved := sourceview.Build(map[string]string{"Action.c": action}, []sourceview.Mapping{{SourceFile: "Action.c", StartLine: 8000, Status: "converted"}})
	server.retainImportResponseSourceTexts(
		httptest.NewRequest(http.MethodPost, "/api/v1/recorder/session/convert?project_id=project-scoped", nil),
		&scriptImportResponseOptions{SourceReview: saved, Script: &dsl.Script{Name: "Recorded", ProjectID: "project-scoped"}, SkipDraftAttestation: true},
	)
	if file := saved.SourceFiles[0]; file.FullSHA256 != "" || file.FullText != "" {
		t.Fatalf("already-saved file = sha %q text kept %v, want the preview only", file.FullSHA256, file.FullText != "")
	}
}
