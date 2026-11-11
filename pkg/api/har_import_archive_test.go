package api

import (
	"archive/zip"
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func zipFiles(t *testing.T, files map[string][]byte, order ...string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	writer := zip.NewWriter(buf)
	for _, name := range order {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func harMultipartRequest(t *testing.T, filename string, data []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("project_id", "synthetic-project"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id=synthetic-project", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	t.Cleanup(func() {
		if req.MultipartForm != nil {
			req.MultipartForm.RemoveAll()
		}
	})
	return req
}

func TestHARUploadUnwrapsZippedCapture(t *testing.T) {
	capture := []byte(`{"log":{"version":"1.2","entries":[]}}`)
	archive := zipFiles(t, map[string][]byte{
		"capture/session.har":            capture,
		"__MACOSX/capture/._session.har": []byte("resource fork"),
		"capture/notes.txt":              []byte("not a capture"),
	}, "capture/session.har", "__MACOSX/capture/._session.har", "capture/notes.txt")
	for _, transport := range []string{"raw", "multipart"} {
		t.Run(transport, func(t *testing.T) {
			req := harMultipartRequest(t, "Session export.zip", archive)
			if transport == "raw" {
				req = httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id=synthetic-project&filename=Session%20export.zip", bytes.NewReader(archive))
				req.Header.Set("Content-Type", "application/zip")
			}
			resp := httptest.NewRecorder()
			got, ok := readHARImportRequest(resp, req)
			if !ok {
				t.Fatalf("zipped HAR rejected: %d %s", resp.Code, resp.Body.String())
			}
			if !bytes.Equal(got.Data, capture) {
				t.Fatal("zipped HAR bytes changed")
			}
			if got.Opts.ScriptName != "Session export" {
				t.Fatalf("script name = %q, want the upload name without .zip", got.Opts.ScriptName)
			}
		})
	}
}

func TestHARUploadUnwrapsPairedTwoHARBundle(t *testing.T) {
	first := []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-01-01T00:00:00Z"}]}}`)
	second := []byte(`{"log":{"version":"1.2","entries":[{"startedDateTime":"2026-01-02T00:00:00Z"}]}}`)
	// Write b.har before a.HAR in the ZIP directory to verify deterministic sorting by name.
	archive := zipFiles(t, map[string][]byte{
		"b.har": second,
		"a.HAR": first,
	}, "b.har", "a.HAR")

	resp := httptest.NewRecorder()
	got, ok := readHARImportRequest(resp, harMultipartRequest(t, "paired.zip", archive))
	if !ok {
		t.Fatalf("paired 2-HAR zip rejected: %d %s", resp.Code, resp.Body.String())
	}
	if !bytes.Equal(got.Data, first) {
		t.Fatalf("primary Data = %s, want a.HAR bytes", string(got.Data))
	}
	if !bytes.Equal(got.SecondaryData, second) {
		t.Fatalf("secondary Data = %s, want b.har bytes", string(got.SecondaryData))
	}
	if got.Opts.ArtifactMode != "design_har_pair" {
		t.Fatalf("Opts.ArtifactMode = %q, want %q", got.Opts.ArtifactMode, "design_har_pair")
	}

	// Single-capture routes (Salesforce) still require a single .har entry.
	sfResp := httptest.NewRecorder()
	if _, _, ok := readSalesforceImportRequest(sfResp, harMultipartRequest(t, "paired.zip", archive)); ok {
		t.Fatal("2-HAR zip accepted on single-capture Salesforce route")
	}
	if sfResp.Code != http.StatusBadRequest || !strings.Contains(sfResp.Body.String(), "contains 2 .har files (a.HAR, b.har)") {
		t.Fatalf("got %d %s, want 400 naming both files", sfResp.Code, sfResp.Body.String())
	}
}

func TestHARUploadZipMustHoldOneOrTwoCaptures(t *testing.T) {
	for name, tc := range map[string]struct {
		archive []byte
		want    string
	}{
		"none": {zipFiles(t, map[string][]byte{"readme.txt": []byte("x")}, "readme.txt"), "contains no .har file"},
		"three": {zipFiles(t, map[string][]byte{"c.har": []byte("{}"), "b.har": []byte("{}"), "a.HAR": []byte("{}")}, "c.har", "b.har", "a.HAR"),
			"contains 3 .har files (a.HAR, b.har, c.har)"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := httptest.NewRecorder()
			if _, ok := readHARImportRequest(resp, harMultipartRequest(t, "capture.zip", tc.archive)); ok {
				t.Fatal("archive accepted")
			}
			if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), tc.want) {
				t.Fatalf("got %d %s, want 400 naming %q", resp.Code, resp.Body.String(), tc.want)
			}
		})
	}
}

func TestHARUploadZipBombIsRefusedAsTooLarge(t *testing.T) {
	// Zeros compress far beyond the archive reader's compression-ratio bound.
	archive := zipFiles(t, map[string][]byte{"bomb.har": make([]byte, 8<<20)}, "bomb.har")
	resp := httptest.NewRecorder()
	if _, ok := readHARImportRequest(resp, harMultipartRequest(t, "bomb.zip", archive)); ok {
		t.Fatal("compression bomb accepted")
	}
	if resp.Code != http.StatusRequestEntityTooLarge || !strings.Contains(resp.Body.String(), "MiB decompressed") {
		t.Fatalf("got %d %s", resp.Code, resp.Body.String())
	}
}

func TestHARUploadLeavesPlainCaptureUnwrapped(t *testing.T) {
	capture := []byte(`{"log":{"entries":[]}}`)
	if got, err := unwrapHARArchive(capture); err != nil || !bytes.Equal(got, capture) {
		t.Fatalf("plain HAR changed: %v", err)
	}
}
