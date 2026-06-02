package api

import (
	"net/http"
	"runtime"
	"strings"

	"github.com/jasonczech/apexload/pkg/storage"
)

type DesktopStatusResponse struct {
	DesktopMode     bool             `json:"desktop_mode"`
	OS              string           `json:"os"`
	APIAddress      string           `json:"api_address"`
	BrowserURL      string           `json:"browser_url"`
	AppDataPath     string           `json:"app_data_path,omitempty"`
	LogPath         string           `json:"log_path,omitempty"`
	Storage         map[string]any   `json:"storage"`
	Cache           CacheBackendInfo `json:"cache"`
	RecorderProxy   map[string]any   `json:"recorder_proxy"`
	RemoteLibraries map[string]any   `json:"remote_libraries"`
}

func (s *Server) handleDesktopStatus(w http.ResponseWriter, _ *http.Request) {
	statusCounts := map[string]int{}
	remoteCount := 0
	s.libraryMu.Lock()
	for _, remote := range s.libraryRemotes {
		if remote == nil {
			continue
		}
		remoteCount++
		status := strings.TrimSpace(remote.Status)
		if status == "" {
			status = "unknown"
		}
		statusCounts[status]++
	}
	s.libraryMu.Unlock()

	storageInfo := map[string]any{"type": "memory"}
	if provider, ok := s.store.(storage.BackendInfoProvider); ok {
		info := provider.BackendInfo()
		storageInfo = map[string]any{
			"type":              info.Type,
			"driver":            info.Driver,
			"migration_version": info.MigrationVersion,
			"lease_capability":  info.LeaseCapability,
		}
	}

	recorderStatus := map[string]any{
		"proxy_addr": s.recorder.ProxyAddr(),
	}
	if active := s.recorder.ActiveSession(); active != nil {
		recorderStatus["active_session_id"] = active.ID
		recorderStatus["active_session_name"] = active.Name
		recorderStatus["active_session_status"] = active.Status
	}

	writeJSON(w, http.StatusOK, DesktopStatusResponse{
		DesktopMode:   s.desktopMode,
		OS:            runtime.GOOS,
		APIAddress:    s.BoundAddr(),
		BrowserURL:    s.BrowserURL(),
		AppDataPath:   s.desktopAppData,
		LogPath:       s.desktopLogPath,
		Storage:       storageInfo,
		Cache:         s.cache.Info(),
		RecorderProxy: recorderStatus,
		RemoteLibraries: map[string]any{
			"count":    remoteCount,
			"statuses": statusCounts,
		},
	})
}
