package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHARUploadAboveFormerLimitPreservesBytes(t *testing.T) {
	// A valid capture with padding exercises the upload envelope without
	// allocating thousands of exchange objects or depending on customer data.
	// It sits exactly at the file limit, so the multipart run also proves the
	// body limit leaves room for the framing around a capture at the limit.
	envelope := []byte(`{"log":{"entries":[]}}`)
	capture := append(envelope, bytes.Repeat([]byte(" "), int(harImportMaxFileBytes)-len(envelope))...)
	for _, transport := range []string{"raw", "multipart"} {
		t.Run(transport, func(t *testing.T) {
			var body io.Reader = bytes.NewReader(capture)
			contentType := "application/octet-stream"
			if transport == "multipart" {
				prefix := &bytes.Buffer{}
				writer := multipart.NewWriter(prefix)
				if err := writer.WriteField("project_id", "synthetic-project"); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.CreateFormFile("file", "synthetic.har"); err != nil {
					t.Fatal(err)
				}
				body = io.MultiReader(bytes.NewReader(prefix.Bytes()), bytes.NewReader(capture), strings.NewReader("\r\n--"+writer.Boundary()+"--\r\n"))
				contentType = writer.FormDataContentType()
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id=synthetic-project", body)
			req.Header.Set("Content-Type", contentType)
			defer func() {
				if req.MultipartForm != nil {
					req.MultipartForm.RemoveAll()
				}
			}()
			resp := httptest.NewRecorder()
			var got *harImportRequest
			var ok bool
			bodyLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got, ok = readHARImportRequest(w, r) })).ServeHTTP(resp, req)
			if !ok {
				t.Fatalf("upload rejected: status %d", resp.Code)
			}
			if !bytes.Equal(got.Data, capture) || got.ProjectID != "synthetic-project" {
				t.Fatal("upload bytes or project scope changed")
			}
		})
	}
}

func TestHARUploadLimitStillRejectsOversizedCapture(t *testing.T) {
	// The envelope fits a capture pair; one capture is still held to the
	// per-file limit, and the refusal is 413.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", io.LimitReader(zeroReader{}, harImportMaxFileBytes+1))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := httptest.NewRecorder()
	if _, ok := readHARImportRequest(resp, req); ok || resp.Code != http.StatusRequestEntityTooLarge || !strings.Contains(resp.Body.String(), "request body too large") {
		t.Fatalf("oversized upload accepted or wrong status: %d", resp.Code)
	}
}
