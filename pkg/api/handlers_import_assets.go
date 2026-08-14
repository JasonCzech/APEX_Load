package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

type importAssetUploadResponse struct {
	ImportID    string         `json:"import_id"`
	Name        string         `json:"name"`
	FileName    string         `json:"file_name"`
	ContentType string         `json:"content_type,omitempty"`
	SizeBytes   int64          `json:"size_bytes"`
	SHA256      string         `json:"sha256"`
	StorageRef  string         `json:"storage_ref"`
	DataSource  dsl.DataSource `json:"data_source"`
	UploadedAt  time.Time      `json:"uploaded_at"`
}

func resolveImportAssetDir(cfg ServerConfig) string {
	if dir := strings.TrimSpace(cfg.ImportAssetDir); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(cfg.DesktopAppDataPath); dir != "" {
		return filepath.Join(dir, "import-assets")
	}
	return fileasset.DefaultImportAssetDir()
}

func (s *Server) handleUploadImportAsset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(importMultipartMemoryBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	projectID, status, message := reconcileImportProjectID(r.URL.Query().Get("project_id"), r.FormValue("project_id"), "multipart body")
	if status != 0 {
		writeError(w, status, message)
		return
	}
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		if projectID != authorizedProjectID {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		projectID = authorizedProjectID
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, harImportMaxBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
		return
	}
	if int64(len(data)) > harImportMaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("asset exceeds %d byte limit", harImportMaxBodyBytes))
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "uploaded asset is empty")
		return
	}

	importID := safeImportAssetSegment(firstNonEmpty(r.PathValue("id"), "import"))
	fileName := sanitizeFilename(firstNonEmpty(header.Filename, "asset.bin"))
	name := assetDataSourceName(firstNonEmpty(r.FormValue("name"), r.FormValue("asset_name"), strings.TrimSuffix(fileName, filepath.Ext(fileName))))
	contentType := firstNonEmpty(strings.TrimSpace(r.FormValue("content_type")), header.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	storageRef := ""
	if currentArchiveBackend().Backend != archiveBackendFilesystem && s.importAssets != nil {
		storageRef, err = s.storeImportAssetObject(r.Context(), projectID, importID, digest, contentType, data)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to store uploaded asset: "+err.Error())
			return
		}
	} else {
		if err := fileasset.RegisterProjectRoot(s.importAssetDir); err != nil {
			writeError(w, http.StatusInternalServerError, "invalid import asset store: "+err.Error())
			return
		}
		path, storeErr := storeImportAssetCapability(s.importAssetDir, projectID, importID, digest, data)
		if storeErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to store uploaded asset: "+storeErr.Error())
			return
		}
		storageRef = (&url.URL{Scheme: "file", Path: path}).String()
	}
	source := dsl.DataSource{
		Name:        name,
		Type:        "file",
		FileName:    fileName,
		ContentType: contentType,
		StorageRef:  storageRef,
	}
	response := importAssetUploadResponse{
		ImportID:    importID,
		Name:        name,
		FileName:    fileName,
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		SHA256:      digest,
		StorageRef:  storageRef,
		DataSource:  source,
		UploadedAt:  time.Now().UTC(),
	}
	writeJSON(w, http.StatusCreated, response)
}

func storeImportAssetCapability(root, projectID, importID, digest string, data []byte) (string, error) {
	projectDir, err := fileasset.ProjectAssetDir(root, projectID)
	if err != nil {
		return "", err
	}
	capabilityBytes := make([]byte, 32)
	if _, err := rand.Read(capabilityBytes); err != nil {
		return "", fmt.Errorf("generate asset capability: %w", err)
	}
	capability := hex.EncodeToString(capabilityBytes)
	dir := filepath.Join(projectDir, safeImportAssetSegment(importID), digest[:2], capability)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("prepare asset store: %w", err)
	}
	path := filepath.Join(dir, digest)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create asset: %w", err)
	}
	written := false
	defer func() {
		_ = file.Close()
		if !written {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		return "", fmt.Errorf("write asset: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close asset: %w", err)
	}
	written = true
	return path, nil
}

func safeImportAssetSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "import"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "import"
	}
	return out
}

func assetDataSourceName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "file"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "file"
	}
	return out
}
