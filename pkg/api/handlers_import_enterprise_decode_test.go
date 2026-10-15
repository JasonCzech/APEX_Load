package api

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
)

func enterpriseFolderRequest(t *testing.T, files map[string][]byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, data := range files {
		part, err := writer.CreateFormFile("file:"+url.PathEscape(name), filepath.Base(name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func TestEnterpriseVuGenFolderImportUsesArchiveConverter(t *testing.T) {
	fixture := filepath.Join("..", "convert", "enterprisearchive", "testdata", "golden_web_http_html_vugen24")
	files := map[string][]byte{}
	for _, name := range []string{"VuGen24Real.usr", "Action.c", "vuser_init.c", "vuser_end.c", "default.cfg", "default.usp", "VuGen24Real.prm"} {
		data, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		files["VuGen24Real/"+name] = data
	}
	files["VuGen24Real/data/repetitive.dat"] = bytes.Repeat([]byte("a"), 1<<20)
	response := httptest.NewRecorder()
	decoded, ok := readEnterpriseArchiveImportRequest(response, enterpriseFolderRequest(t, files))
	if !ok {
		t.Fatalf("decode failed: status=%d body=%s", response.Code, response.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(decoded.Data), int64(len(decoded.Data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if file.Method != zip.Store {
			t.Fatalf("folder entry %q was recompressed", file.Name)
		}
	}
	result, err := enterprisearchive.ConvertZip(decoded.Data, decoded.Opts)
	if err != nil {
		t.Fatalf("convert folder: %v", err)
	}
	if result.Script == nil || result.Script.Name != "VuGen24Real" {
		t.Fatalf("converted script = %#v", result.Script)
	}
	merged, err := mergeEnterpriseArchiveZipWithSupplementalFiles(decoded.Data, []string{"extra.txt"}, [][]byte{[]byte("extra")})
	if err != nil {
		t.Fatalf("merge supplemental file: %v", err)
	}
	mergedArchive, err := zip.NewReader(bytes.NewReader(merged), int64(len(merged)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range mergedArchive.File {
		if file.Name == "VuGen24Real/data/repetitive.dat" && file.Method != zip.Store {
			t.Fatal("supplemental merge recompressed the folder's repetitive data")
		}
	}
	if _, err := enterprisearchive.ConvertZip(merged, decoded.Opts); err != nil {
		t.Fatalf("convert folder after supplemental merge: %v", err)
	}
}

// A folder picked on a Mac or from a working copy carries .DS_Store and .git
// files, sometimes beside the script folder itself. They are left out: they
// neither join the archive nor make the selection look like two roots.
func TestEnterpriseVuGenFolderLeavesOutOperatingSystemAndVersionControlFiles(t *testing.T) {
	fixture := filepath.Join("..", "convert", "enterprisearchive", "testdata", "golden_web_http_html_vugen24")
	files := map[string][]byte{
		".DS_Store":              []byte("Bud1"),
		"VuGen24Real/.DS_Store":  []byte("Bud1"),
		"VuGen24Real/.git/HEAD":  []byte("ref: refs/heads/main"),
		"VuGen24Real/._Action.c": []byte("appledouble"),
	}
	for _, name := range []string{"VuGen24Real.usr", "Action.c", "vuser_init.c", "vuser_end.c", "default.cfg", "default.usp", "VuGen24Real.prm"} {
		data, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		files["VuGen24Real/"+name] = data
	}
	response := httptest.NewRecorder()
	decoded, ok := readEnterpriseArchiveImportRequest(response, enterpriseFolderRequest(t, files))
	if !ok {
		t.Fatalf("decode failed: status=%d body=%s", response.Code, response.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(decoded.Data), int64(len(decoded.Data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if strings.Contains(file.Name, ".DS_Store") || strings.Contains(file.Name, ".git/") || strings.Contains(file.Name, "._") {
			t.Errorf("the folder archive holds %s", file.Name)
		}
	}
	if len(archive.File) != 7 {
		t.Errorf("archive has %d files, want the 7 script files", len(archive.File))
	}
}

func TestEnterpriseVuGenFolderRejectsUnsafeAndIncompletePaths(t *testing.T) {
	for _, test := range []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{name: "missing project", files: map[string][]byte{"Demo/Action.c": []byte("x")}, want: "expected one .usr"},
		{name: "traversal", files: map[string][]byte{"Demo/Demo.usr": []byte("x"), "Demo/../Action.c": []byte("x")}, want: "unsafe"},
		{name: "case collision", files: map[string][]byte{"Demo/Demo.usr": []byte("x"), "Demo/Action.c": []byte("x"), "Demo/action.c": []byte("x")}, want: "duplicate"},
		{name: "mixed roots", files: map[string][]byte{"Demo/Demo.usr": []byte("x"), "Other/Action.c": []byte("x")}, want: "one root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			_, ok := readEnterpriseArchiveImportRequest(response, enterpriseFolderRequest(t, test.files))
			if ok || response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), test.want) {
				t.Fatalf("ok=%v status=%d body=%s; want %q", ok, response.Code, response.Body.String(), test.want)
			}
		})
	}
}

func TestEnterpriseArchiveDraftExpirationRequiresExplicitOptIn(t *testing.T) {
	for _, test := range []struct {
		name string
		url  string
		want bool
	}{
		{name: "default disabled", url: "/api/v1/scripts/import/enterprise-script-archive"},
		{name: "explicit opt in", url: "/api/v1/scripts/import/enterprise-script-archive?expire_draft=true", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.url, bytes.NewReader([]byte{'P', 'K', 0x05, 0x06}))
			request.Header.Set("Content-Type", "application/zip")
			response := httptest.NewRecorder()
			decoded, ok := readEnterpriseArchiveImportRequest(response, request)
			if !ok {
				t.Fatalf("decode failed: status=%d body=%s", response.Code, response.Body.String())
			}
			if decoded.ExpireDraft != test.want {
				t.Fatalf("ExpireDraft = %v, want %v", decoded.ExpireDraft, test.want)
			}
		})
	}
}

func TestSourceReviewValuePreservesCompletenessEvidence(t *testing.T) {
	review := sourceReviewValue(map[string]any{
		"total_source_files":   float64(4),
		"omitted_source_files": []any{"Support.java", "Helper.java"},
	})
	if review == nil {
		t.Fatal("sourceReviewValue returned nil for completeness-only review evidence")
	}
	if review.TotalSourceFiles != 4 {
		t.Fatalf("TotalSourceFiles = %d, want 4", review.TotalSourceFiles)
	}
	wantOmitted := []string{"Helper.java", "Support.java"}
	if !reflect.DeepEqual(review.OmittedSourceFiles, wantOmitted) {
		t.Fatalf("OmittedSourceFiles = %#v, want %#v", review.OmittedSourceFiles, wantOmitted)
	}
}
