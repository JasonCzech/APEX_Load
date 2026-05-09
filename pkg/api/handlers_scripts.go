package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonczech/stormrunner/pkg/convert/loadrunner"
	"github.com/jasonczech/stormrunner/pkg/dsl"
)

// handleImportLoadRunner handles POST /api/v1/scripts/import/loadrunner
// Accepts a multipart form upload of a ZIP file containing a LoadRunner script folder.
func (s *Server) handleImportLoadRunner(w http.ResponseWriter, r *http.Request) {
	// Limit upload to 50MB
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)

	// Check content type — support both multipart form and raw zip body
	contentType := r.Header.Get("Content-Type")

	var zipData []byte

	if contentType == "application/zip" || contentType == "application/x-zip-compressed" || contentType == "application/octet-stream" {
		// Raw ZIP body
		data, err := io.ReadAll(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return
		}
		zipData = data
	} else {
		// Multipart form upload
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form (max 50MB): "+err.Error())
			return
		}

		file, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing 'file' field in multipart form: "+err.Error())
			return
		}
		defer file.Close()

		data, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return
		}
		zipData = data
	}

	if len(zipData) == 0 {
		writeError(w, http.StatusBadRequest, "empty file uploaded")
		return
	}

	// Parse conversion options from query params or form values
	opts := loadrunner.ConvertOptions{
		ScriptName:         r.FormValue("name"),
		BaseURL:            r.FormValue("base_url"),
		IncludeAssertions:  r.FormValue("include_assertions") == "true",
		PreserveTransactions: r.FormValue("preserve_transactions") == "true",
	}

	// Run the conversion
	result, err := loadrunner.ConvertZip(zipData, opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"result":   result,
		"script":   result.Script,
		"warnings": result.Warnings,
		"stats":    result.Stats,
	})
}

// handleExportScript handles POST /api/v1/scripts/{id}/export
// Exports a script as a JSON DSL file to disk.
func (s *Server) handleExportScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	// Optional: specify output directory via request body
	var req struct {
		OutputDir  string `json:"output_dir,omitempty"`
		Filename   string `json:"filename,omitempty"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&req)
	}

	outputDir := req.OutputDir
	if outputDir == "" {
		outputDir = "." // Current working directory
	}

	filename := req.Filename
	if filename == "" {
		filename = sanitizeFilename(stored.Name) + ".json"
	}

	// Wrap in the standard script envelope
	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	fullPath := filepath.Join(outputDir, filename)

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create output directory: "+err.Error())
		return
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write script file: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"exported": true,
		"path":     fullPath,
		"filename": filename,
		"size":     len(data),
	})
}

// handleDownloadScript handles GET /api/v1/scripts/{id}/download
// Returns the script JSON as a downloadable file.
func (s *Server) handleDownloadScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	envelope := map[string]any{
		"script": stored.Script,
	}

	data, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to marshal script: "+err.Error())
		return
	}

	filename := sanitizeFilename(stored.Name) + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleUpdateScript handles PUT /api/v1/scripts/{id}
// Updates an existing script in-place.
func (s *Server) handleUpdateScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var wrapper struct {
		Script dsl.Script `json:"script"`
	}
	if err := json.NewDecoder(r.Body).Decode(&wrapper); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	script := &wrapper.Script

	if script.Name == "" {
		writeError(w, http.StatusBadRequest, "script name is required")
		return
	}
	if script.Protocol == "" {
		writeError(w, http.StatusBadRequest, "script protocol is required")
		return
	}
	if len(script.Actions) == 0 {
		writeError(w, http.StatusBadRequest, "script must have at least one action")
		return
	}

	updated, ok := s.runner.UpdateScript(id, script)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteScript handles DELETE /api/v1/scripts/{id}
func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.runner.DeleteScript(id) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// sanitizeFilename makes a string safe for use as a filename.
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
