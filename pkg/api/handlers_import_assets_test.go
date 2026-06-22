package api

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestHandleUploadImportAssetStoresContentAddressedFile(t *testing.T) {
	server := &Server{importAssetDir: t.TempDir()}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "Payload File"); err != nil {
		t.Fatalf("WriteField name: %v", err)
	}
	if err := writer.WriteField("content_type", "application/octet-stream"); err != nil {
		t.Fatalf("WriteField content_type: %v", err)
	}
	part, err := writer.CreateFormFile("file", "payload.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(part, "asset bytes"); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/import-1/assets/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.SetPathValue("id", "import-1")
	resp := httptest.NewRecorder()

	server.handleUploadImportAsset(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}

	var decoded importAssetUploadResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Name != "payload_file" || decoded.DataSource.Name != "payload_file" || decoded.DataSource.Type != "file" {
		t.Fatalf("response data source = %#v", decoded.DataSource)
	}
	if decoded.DataSource.FileName != "payload.bin" || decoded.DataSource.ContentType != "application/octet-stream" {
		t.Fatalf("response metadata = %#v", decoded.DataSource)
	}
	parsed, err := url.Parse(decoded.StorageRef)
	if err != nil {
		t.Fatalf("parse storage_ref: %v", err)
	}
	stored, err := os.ReadFile(parsed.Path)
	if err != nil {
		t.Fatalf("read stored asset: %v", err)
	}
	if string(stored) != "asset bytes" {
		t.Fatalf("stored bytes = %q", stored)
	}
	dp, err := engine.NewDataProvider([]dsl.DataSource{decoded.DataSource})
	if err != nil {
		t.Fatalf("NewDataProvider returned upload data source error: %v", err)
	}
	got, ok := dp.GetFileContent("payload_file")
	if !ok || got != "asset bytes" {
		t.Fatalf("resolved file asset = %q/%v, want asset bytes/true", got, ok)
	}
}
