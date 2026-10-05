package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
)

func ensureAnalysisBackend(store any) error {
	return ensureAnalysisBackendFor(store, currentArchiveBackend())
}

func ensureAnalysisBackendFor(store any, backend archiveBackendRuntime) error {
	binding, ok := store.(interface{ EnsureAnalysisStorageIdentity(string) error })
	if !ok {
		return errors.New("analytical storage identity persistence unavailable")
	}
	if err := backend.validate(); err != nil {
		return err
	}
	// Object cache paths can differ across replicas; only the durable namespace
	// and credential destination identity participate in the binding.
	if backend.Backend == archiveBackendFilesystem {
		root, err := filepath.Abs(backend.LocalRoot)
		if err != nil {
			return err
		}
		backend = archiveBackendRuntime{Backend: archiveBackendFilesystem, LocalRoot: root}
	} else {
		backend.LocalRoot = ""
	}
	body, err := json.Marshal(backend)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	return binding.EnsureAnalysisStorageIdentity(hex.EncodeToString(digest[:]))
}

func (s *Server) requireAnalysisBackend(w http.ResponseWriter) bool {
	if err := ensureAnalysisBackend(s.store); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return false
	}
	return true
}
