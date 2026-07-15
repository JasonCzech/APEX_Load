package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

func TestHandleUploadImportAssetStoresProjectScopedCapability(t *testing.T) {
	assetRoot := t.TempDir()
	server := &Server{importAssetDir: assetRoot}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("project_id", "project-a"); err != nil {
		t.Fatalf("WriteField project_id: %v", err)
	}
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
	projectDir, err := fileasset.ProjectAssetDir(assetRoot, "project-a")
	if err != nil {
		t.Fatalf("ProjectAssetDir: %v", err)
	}
	relative, err := filepath.Rel(projectDir, parsed.Path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("stored path %q is outside project namespace %q", parsed.Path, projectDir)
	}
	capability := filepath.Base(filepath.Dir(parsed.Path))
	if len(capability) != 64 || capability == decoded.SHA256 {
		t.Fatalf("capability segment = %q, want independent 256-bit token", capability)
	}
	if _, err := fileasset.ValidateStorageRefForProject(decoded.StorageRef, "project-a"); err != nil {
		t.Fatalf("owner project validation failed: %v", err)
	}
	if _, err := fileasset.ValidateStorageRefForProject(decoded.StorageRef, "project-b"); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("foreign project validation error = %v, want ownership rejection", err)
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

func TestStoreImportAssetCapabilityDoesNotExposeContentAddress(t *testing.T) {
	root := t.TempDir()
	digestBytes := sha256.Sum256([]byte("same bytes"))
	digest := hex.EncodeToString(digestBytes[:])
	first, err := storeImportAssetCapability(root, "project-a", "import-1", digest, []byte("same bytes"))
	if err != nil {
		t.Fatalf("first store: %v", err)
	}
	second, err := storeImportAssetCapability(root, "project-a", "import-1", digest, []byte("same bytes"))
	if err != nil {
		t.Fatalf("second store: %v", err)
	}
	if first == second {
		t.Fatalf("repeated upload reused predictable path %q", first)
	}
	if filepath.Base(first) != digest || filepath.Base(second) != digest {
		t.Fatalf("stored basenames = %q, %q; want digest", filepath.Base(first), filepath.Base(second))
	}
}
