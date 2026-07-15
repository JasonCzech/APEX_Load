package api

import (
	"io"
	"net/http"
)

const importMultipartMemoryBytes = 1 << 20

// readImportMultipartFile parses a multipart/form-data import upload and returns
// the bytes and original filename of the "file" field. On any failure it writes
// the appropriate 400 response and returns ok=false. Callers remain responsible
// for deriving a script name and reading format-specific form values (which are
// available via r.FormValue after this call parses the form).
func readImportMultipartFile(w http.ResponseWriter, r *http.Request, maxBytes int64) (data []byte, filename string, ok bool) {
	if err := r.ParseMultipartForm(importMultipartMemoryBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return nil, "", false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field is required")
		return nil, "", false
	}
	defer file.Close()
	data, err = io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
		return nil, "", false
	}
	return data, header.Filename, true
}

// readImportBody reads the full request body for a non-multipart import upload,
// writing a 400 response and returning ok=false if the read fails.
func readImportBody(w http.ResponseWriter, r *http.Request) (data []byte, ok bool) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return nil, false
	}
	return data, true
}
