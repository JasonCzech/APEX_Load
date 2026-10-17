package recordbridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/scrub"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds the configured size limit")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

func queryIncludes(query url.Values, token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	for _, raw := range query["include"] {
		for _, part := range strings.Split(raw, ",") {
			if strings.ToLower(strings.TrimSpace(part)) == token {
				return true
			}
		}
	}
	return false
}
func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeOptionalJSONBodyWithStrictness(w, r, dst, true)
}

func decodeOptionalJSONBodyWithStrictness(w http.ResponseWriter, r *http.Request, dst any, disallowUnknownFields bool) bool {
	if r == nil || r.Body == nil {
		return true
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if disallowUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds the configured size limit")
			return false
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			writeError(w, http.StatusBadRequest, "invalid request body: multiple JSON values are not allowed")
		} else {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		}
		return false
	}
	return true
}
func exportBaseDir() string {
	if dir := strings.TrimSpace(os.Getenv("APEXLOAD_EXPORT_DIR")); dir != "" {
		return dir
	}
	return "exports"
}

func resolveConfinedExportPath(base, userDir, filename string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "exports"
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve export base directory: %w", err)
	}

	safeName := sanitizeFilename(filepath.Base(strings.TrimSpace(filename)))
	if safeName == "" || safeName == "." || safeName == ".." {
		safeName = "script.json"
	}

	// Force-root the user directory then strip the leading separator so that
	// any leading or embedded ".." can only collapse back to the root, never
	// above it. Absolute paths are likewise neutralized.
	rooted := filepath.Clean("/" + filepath.ToSlash(strings.TrimSpace(userDir)))
	sub := strings.TrimPrefix(rooted, "/")

	target := filepath.Clean(filepath.Join(absBase, filepath.FromSlash(sub), safeName))
	if target != absBase && !strings.HasPrefix(target, absBase+string(filepath.Separator)) {
		return "", fmt.Errorf("resolved export path escapes the export directory")
	}
	return target, nil
}
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	result := replacer.Replace(name)
	if result == "" {
		result = "script"
	}
	return result
}

func WriteJSON(w http.ResponseWriter, status int, value any) { writeJSON(w, status, value) }
func DecodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONBody(w, r, dst)
}
func DecodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeOptionalJSONBody(w, r, dst)
}
func writeError(w http.ResponseWriter, status int, message string) { WriteError(w, status, message) }
func WriteError(w http.ResponseWriter, status int, message string) {
	if status >= http.StatusInternalServerError {
		WriteJSON(w, status, map[string]string{"error": "internal server error", "ref": LogServerError(w, status, errors.New(message))})
		return
	}
	WriteJSON(w, status, map[string]string{"error": scrub.Value(message, scrub.Policy{})})
}
func LogServerError(w http.ResponseWriter, status int, cause error, attrs ...any) string {
	ref := w.Header().Get("X-Request-ID")
	if ref == "" {
		var data [8]byte
		if _, err := rand.Read(data[:]); err == nil {
			ref = hex.EncodeToString(data[:])
		} else {
			ref = "unknown"
		}
	}
	fields := append([]any{"ref", ref, "status", status}, attrs...)
	if cause != nil {
		fields = append(fields, "detail", scrub.Value(cause.Error(), scrub.Policy{}))
	}
	slog.Error("internal server error", fields...)
	return ref
}
func ArtifactWriteTimeout() time.Duration {
	timeout := 30 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_MATERIALIZATION_TIMEOUT")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			timeout = parsed
		}
	}
	return timeout + 5*time.Second
}
