package api

import (
	"embed"
	"net/http"
)

//go:embed apidocs/swagger.yaml apidocs/api-endpoint-catalog.json
var apiDocsFS embed.FS

func (s *Server) handleGetAPIDocsSwagger(w http.ResponseWriter, r *http.Request) {
	writeEmbeddedAPIDoc(w, "apidocs/swagger.yaml", "application/yaml; charset=utf-8")
}

func (s *Server) handleGetAPIDocsEndpointCatalog(w http.ResponseWriter, r *http.Request) {
	writeEmbeddedAPIDoc(w, "apidocs/api-endpoint-catalog.json", "application/json; charset=utf-8")
}

func writeEmbeddedAPIDoc(w http.ResponseWriter, path string, contentType string) {
	data, err := apiDocsFS.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "api documentation asset unavailable")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
