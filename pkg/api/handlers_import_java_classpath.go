package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/fileasset"
)

// Upload failure classes, so status selection is by errors.Is rather than
// fragile substring matching. Everything not wrapped in one of these is a
// client-input error (400).
var (
	errClasspathUploadTooLarge = errors.New("uploaded jar exceeds the size limit")
	errClasspathUploadInternal = errors.New("failed to store uploaded jar")
)

// jarLocalFileHeaderSignature is the ZIP local-file-header magic that begins
// every real JAR (a JAR is a ZIP). Uploads are rejected unless they start with
// it, so a mistyped or non-archive file fails fast instead of at runtime.
var jarLocalFileHeaderSignature = []byte{0x50, 0x4B, 0x03, 0x04} // "PK\x03\x04"

type javaClasspathAssetUploadResponse struct {
	ImportID    string    `json:"import_id"`
	Entry       string    `json:"entry,omitempty"`
	FileName    string    `json:"file_name"`
	ContentType string    `json:"content_type,omitempty"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	StorageRef  string    `json:"storage_ref"`
	UploadedAt  time.Time `json:"uploaded_at"`
}

// javaClasspathUploadMaxBytes is the operator-overridable per-JAR cap enforced
// by the handler while streaming. The env override may only LOWER the cap: it is
// clamped to defaultJavaClasspathUploadMaxBytes so it can never exceed the fixed
// transport ceiling (javaClasspathUploadMaxBodyBytes, which adds framing
// headroom) nor the materialization ceiling (fileasset.MaxJarBytes, equal to the
// default). This prevents an operator from configuring an upload cap that would
// accept files the transport layer truncates or that materialization later
// rejects as classpath_asset_unavailable.
func javaClasspathUploadMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("APEXLOAD_JAVA_VUSER_CLASSPATH_MAX_BYTES"))
	if raw == "" {
		return defaultJavaClasspathUploadMaxBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 || value > defaultJavaClasspathUploadMaxBytes {
		return defaultJavaClasspathUploadMaxBytes
	}
	return value
}

// handleUploadJavaClasspathJar accepts a user-supplied JAR for an imported Java
// Vuser whose classpath dependency is not pre-provisioned on the runner. The
// bytes are streamed to the project-fenced, content-addressed import asset
// store (reusing the capability-path layout of handleUploadImportAsset). The
// caller binds the returned storage_ref/sha256 to an exact classpath entry as a
// dsl.JavaClasspathAsset. The endpoint is inert unless the operator has opted
// in via APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED.
func (s *Server) handleUploadJavaClasspathJar(w http.ResponseWriter, r *http.Request) {
	if !config.JavaClasspathUploadEnabled() {
		writeError(w, http.StatusNotFound, "java classpath upload is not enabled")
		return
	}

	// Resolve the project without consuming the streamed body: r.FormValue would
	// call ParseMultipartForm and consume the stream before r.MultipartReader can
	// run. The route resolver has already validated project presence/authorization.
	// Prefer the authorized-context project (project-scoped roles); else the query
	// parameter; else, for a globally-authorized caller that supplied project_id
	// only as a leading multipart field, peek it (peekMultipartProjectID restores
	// the body). A disagreeing form project_id part is still rejected in the loop.
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if authorizedProjectID, scoped := authorizedProjectFromRequest(r); scoped {
		projectID = authorizedProjectID
	} else if projectID == "" {
		if bodyProjectID, err := peekMultipartProjectID(r); err == nil {
			projectID = strings.TrimSpace(bodyProjectID)
		}
	}
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "project_id query parameter or leading multipart field is required")
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
		return
	}

	maxBytes := javaClasspathUploadMaxBytes()
	var (
		entry       string
		fileName    string
		contentType string
		digest      string
		size        int64
		storedPath  string
		fileSeen    bool
	)
	cleanup := func() {
		if storedPath != "" {
			_ = os.Remove(storedPath)
		}
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			writeError(w, http.StatusBadRequest, "invalid multipart upload: "+err.Error())
			return
		}
		switch part.FormName() {
		case "entry":
			value, err := io.ReadAll(io.LimitReader(part, 8<<10))
			_ = part.Close()
			if err != nil {
				cleanup()
				writeError(w, http.StatusBadRequest, "invalid entry field: "+err.Error())
				return
			}
			entry = strings.TrimSpace(string(value))
		case "project_id":
			// Authorization already fixed projectID from the query parameter; a
			// disagreeing form value must not redirect the write to another
			// project.
			value, err := io.ReadAll(io.LimitReader(part, 4<<10))
			_ = part.Close()
			if err != nil {
				cleanup()
				writeError(w, http.StatusBadRequest, "invalid project_id field: "+err.Error())
				return
			}
			formProject := strings.TrimSpace(string(value))
			if formProject != "" && formProject != projectID {
				cleanup()
				writeError(w, http.StatusForbidden, "project_id form field does not match the authorized project_id")
				return
			}
		case "file":
			if fileSeen {
				_ = part.Close()
				cleanup()
				writeError(w, http.StatusBadRequest, "duplicate file part")
				return
			}
			fileSeen = true
			fileName = sanitizeFilename(firstNonEmpty(part.FileName(), "dependency.jar"))
			contentType = firstNonEmpty(strings.TrimSpace(part.Header.Get("Content-Type")), "application/java-archive")
			digest, size, storedPath, err = streamJavaClasspathJar(part, s.importAssetDir, projectID, r.PathValue("id"), maxBytes)
			_ = part.Close()
			if err != nil {
				cleanup()
				storedPath = ""
				switch {
				case errors.Is(err, errClasspathUploadTooLarge):
					writeError(w, http.StatusRequestEntityTooLarge, "uploaded jar exceeds the size limit")
				case errors.Is(err, errClasspathUploadInternal):
					// Internal I/O detail may include a server temp path; do not echo it.
					writeError(w, http.StatusInternalServerError, "failed to store uploaded jar")
				default:
					// Client-input errors carry static, value-free messages.
					writeError(w, http.StatusBadRequest, err.Error())
				}
				return
			}
		default:
			_ = part.Close()
		}
	}

	if !fileSeen {
		cleanup()
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}

	storageRef := (&url.URL{Scheme: "file", Path: storedPath}).String()
	writeJSON(w, http.StatusCreated, javaClasspathAssetUploadResponse{
		ImportID:    safeImportAssetSegment(firstNonEmpty(r.PathValue("id"), "import")),
		Entry:       entry,
		FileName:    fileName,
		ContentType: contentType,
		SizeBytes:   size,
		SHA256:      digest,
		StorageRef:  storageRef,
		UploadedAt:  time.Now().UTC(),
	})
}

// streamJavaClasspathJar streams a JAR part to a temp file (bounded, hashed),
// verifies the ZIP magic, then copies it into the content-addressed capability
// store. It never buffers the whole JAR in memory. Returns the hex digest, the
// byte size, and the stored path.
func streamJavaClasspathJar(part io.Reader, root, projectID, importID string, maxBytes int64) (string, int64, string, error) {
	if err := fileasset.RegisterProjectRoot(root); err != nil {
		return "", 0, "", fmt.Errorf("%w: register store: %v", errClasspathUploadInternal, err)
	}

	tmp, err := os.CreateTemp("", "apexload-java-classpath-*.jar")
	if err != nil {
		return "", 0, "", fmt.Errorf("%w: stage upload: %v", errClasspathUploadInternal, err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	size, err := copyWithMaxBytes(io.MultiWriter(tmp, hasher), part, maxBytes)
	if err != nil {
		// Distinguish "too large" (handler cap via copyWithMaxBytes, or the
		// transport MaxBytesReader typed error) from a genuine I/O failure.
		var maxBytesErr *http.MaxBytesError
		if size > maxBytes || errors.As(err, &maxBytesErr) {
			return "", 0, "", fmt.Errorf("%w: %v", errClasspathUploadTooLarge, err)
		}
		return "", 0, "", fmt.Errorf("%w: copy upload: %v", errClasspathUploadInternal, err)
	}
	if size == 0 {
		return "", 0, "", fmt.Errorf("uploaded jar is empty")
	}
	if err := verifyJarMagic(tmpPath); err != nil {
		return "", 0, "", err
	}
	digest := hex.EncodeToString(hasher.Sum(nil))

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", 0, "", fmt.Errorf("%w: rewind upload: %v", errClasspathUploadInternal, err)
	}
	storedPath, err := storeImportAssetCapabilityFromReader(root, projectID, importID, digest, tmp)
	if err != nil {
		return "", 0, "", fmt.Errorf("%w: %v", errClasspathUploadInternal, err)
	}
	return digest, size, storedPath, nil
}

// verifyJarMagic returns a client-input error (unwrapped -> 400) when the bytes
// are not a ZIP/JAR, and an internal error (500) only when the temp file cannot
// be reopened.
func verifyJarMagic(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: inspect upload: %v", errClasspathUploadInternal, err)
	}
	defer f.Close()
	header := make([]byte, len(jarLocalFileHeaderSignature))
	if _, err := io.ReadFull(f, header); err != nil {
		return fmt.Errorf("uploaded file is not a valid jar archive")
	}
	for i := range jarLocalFileHeaderSignature {
		if header[i] != jarLocalFileHeaderSignature[i] {
			return fmt.Errorf("uploaded file is not a valid jar archive")
		}
	}
	return nil
}

// storeImportAssetCapabilityFromReader mirrors storeImportAssetCapability but
// streams from an io.Reader so large JARs are never held fully in memory.
func storeImportAssetCapabilityFromReader(root, projectID, importID, digest string, src io.Reader) (string, error) {
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
	if _, err := io.Copy(file, src); err != nil {
		return "", fmt.Errorf("write asset: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close asset: %w", err)
	}
	written = true
	return path, nil
}
