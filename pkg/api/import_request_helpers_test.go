package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeUploadForTest(t *testing.T, r *http.Request) *importUpload {
	t.Helper()
	upload, ok := decodeImportUpload(httptest.NewRecorder(), r, 1<<20)
	if !ok {
		t.Fatal("decodeImportUpload rejected the request")
	}
	return upload
}

// Every transport resolves the project scope the same way. The raw-body branch
// used to read the query value untrimmed, and normalizeProjectID does not trim
// either, so a padded ?project_id= reached storage as a scope no lookup matched.
func TestDecodeImportUploadTrimsProjectScopeOnEveryTransport(t *testing.T) {
	t.Run("raw body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id=%20proj-1%20",
			bytes.NewReader([]byte("openapi: 3.0.0")))
		if got := decodeUploadForTest(t, req).ProjectID; got != "proj-1" {
			t.Fatalf("ProjectID = %q, want proj-1", got)
		}
	})

	t.Run("json body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id=%20proj-1%20",
			bytes.NewReader([]byte(`{"content":"openapi: 3.0.0"}`)))
		req.Header.Set("Content-Type", "application/json")
		if got := decodeUploadForTest(t, req).ProjectID; got != "proj-1" {
			t.Fatalf("ProjectID = %q, want proj-1", got)
		}
	})

	t.Run("multipart", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, err := writer.CreateFormFile("file", "spec.yaml")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("openapi: 3.0.0")); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("project_id", " proj-1 "); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		if got := decodeUploadForTest(t, req).ProjectID; got != "proj-1" {
			t.Fatalf("ProjectID = %q, want proj-1", got)
		}
	})
}

// A URL-pinned scope stays authoritative over one a form body also carries.
func TestDecodeImportUploadPrefersTheQueryProjectScope(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("openapi: 3.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("project_id", "form-project"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id=query-project", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if got := decodeUploadForTest(t, req).ProjectID; got != "query-project" {
		t.Fatalf("ProjectID = %q, want query-project", got)
	}
}

// A whitespace-only body scope falls through to the query rather than winning
// and then collapsing to the empty default.
func TestImportUploadJSONProjectIDIgnoresABlankBodyScope(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id=proj-query",
		bytes.NewReader([]byte(`{"content":"openapi: 3.0.0","project_id":"   "}`)))
	req.Header.Set("Content-Type", "application/json")
	upload := decodeUploadForTest(t, req)
	if got := upload.jsonProjectID("   "); got != "proj-query" {
		t.Fatalf("jsonProjectID = %q, want proj-query", got)
	}
	if got := upload.jsonProjectID(" proj-body "); got != "proj-body" {
		t.Fatalf("jsonProjectID = %q, want proj-body", got)
	}
}

// An upload that named no file has no filename. path.Base turns "" into ".",
// which used to travel onward as the collection filename.
func TestDecodeImportUploadLeavesAnUnnamedUploadWithoutAFilename(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi",
		bytes.NewReader([]byte("openapi: 3.0.0")))
	upload := decodeUploadForTest(t, req)
	if upload.Filename != "" || upload.Name != "" {
		t.Fatalf("Filename = %q, Name = %q, want both empty", upload.Filename, upload.Name)
	}
}

// Options resolve against the multipart form and the query string alike, so a
// format's option table is written once regardless of how the artifact arrived.
func TestDecodeImportUploadReadsOptionsFromFormAndQuery(t *testing.T) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "collection.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`{"item":[]}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("base_url", "https://form.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman?script_name=FromQuery", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	upload := decodeUploadForTest(t, req)
	if got := upload.Value("base_url"); got != "https://form.example.test" {
		t.Fatalf("base_url = %q, want the form value", got)
	}
	if got := upload.Value("script_name"); got != "FromQuery" {
		t.Fatalf("script_name = %q, want the query value", got)
	}
	if got := upload.Name; got != "collection" {
		t.Fatalf("Name = %q, want collection", got)
	}
}
