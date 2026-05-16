package api

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/recorder"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

//go:embed static
var staticFS embed.FS

type Server struct {
	httpServer    *http.Server
	hub           *Hub
	runner        *TestRunner
	browserReplay *BrowserReplayManager
	recorder      *recorder.Recorder
	store         storage.Store
	mux           *http.ServeMux
}

// ServerConfig configures API server startup behavior and integrations.
type ServerConfig struct {
	Addr             string
	ProxyAddr        string
	MaxConcurrent    int
	AllowedOrigins   []string
	Store            storage.Store
	GeneratorTargets []string
	GeneratorTimeout time.Duration
}

// NewServer wires the HTTP server, test runner, websocket hub, recorder, and routes.
func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}

	hub := NewHub()
	runner := NewTestRunner(cfg.MaxConcurrent, hub, cfg.Store)
	if len(cfg.GeneratorTargets) > 0 {
		runner.setExecutionBackend(newDistributedExecutionBackend(cfg.GeneratorTargets, cfg.GeneratorTimeout, nil))
	}
	rec := recorder.New(cfg.ProxyAddr)

	s := &Server{
		hub:           hub,
		runner:        runner,
		browserReplay: NewBrowserReplayManager(hub),
		recorder:      rec,
		store:         cfg.Store,
		mux:           http.NewServeMux(),
	}

	s.routes(cfg.AllowedOrigins)

	s.httpServer = &http.Server{
		Addr:         cfg.Addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

func (s *Server) routes(allowedOrigins []string) {
	cors := corsMiddleware(allowedOrigins)
	log := loggingMiddleware
	auth := authMiddleware(func() AuthSettings { return s.runner.GetSystemSettings().Auth }, s.store)

	// Test lifecycle and archive endpoints.
	s.mux.Handle("POST /api/v1/applications", cors(log(http.HandlerFunc(s.handleCreateApplication))))
	s.mux.Handle("GET /api/v1/applications", cors(log(http.HandlerFunc(s.handleListApplications))))
	s.mux.Handle("GET /api/v1/applications/{id}", cors(log(http.HandlerFunc(s.handleGetApplication))))
	s.mux.Handle("PUT /api/v1/applications/{id}", cors(log(http.HandlerFunc(s.handleUpdateApplication))))
	s.mux.Handle("DELETE /api/v1/applications/{id}", cors(log(http.HandlerFunc(s.handleDeleteApplication))))
	s.mux.Handle("POST /api/v1/applications/{id}/releases", cors(log(http.HandlerFunc(s.handleCreateRelease))))
	s.mux.Handle("GET /api/v1/applications/{id}/releases", cors(log(http.HandlerFunc(s.handleListReleases))))
	s.mux.Handle("GET /api/v1/releases/{id}", cors(log(http.HandlerFunc(s.handleGetRelease))))
	s.mux.Handle("PUT /api/v1/releases/{id}", cors(log(http.HandlerFunc(s.handleUpdateRelease))))
	s.mux.Handle("POST /api/v1/releases/{id}/archive", cors(log(http.HandlerFunc(s.handleArchiveRelease))))
	s.mux.Handle("DELETE /api/v1/releases/{id}", cors(log(http.HandlerFunc(s.handleDeleteRelease))))

	s.mux.Handle("POST /api/v1/projects", cors(log(auth(http.HandlerFunc(s.handleCreateProject)))))
	s.mux.Handle("GET /api/v1/projects", cors(log(auth(http.HandlerFunc(s.handleListProjects)))))
	s.mux.Handle("GET /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleGetProject)))))
	s.mux.Handle("PUT /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateProject)))))
	s.mux.Handle("DELETE /api/v1/projects/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteProject)))))
	s.mux.Handle("POST /api/v1/projects/{pid}/folders", cors(log(auth(http.HandlerFunc(s.handleCreateFolder)))))
	s.mux.Handle("GET /api/v1/projects/{pid}/folders", cors(log(auth(http.HandlerFunc(s.handleListFolders)))))
	s.mux.Handle("PUT /api/v1/folders/{id}", cors(log(auth(http.HandlerFunc(s.handleUpdateFolder)))))
	s.mux.Handle("DELETE /api/v1/folders/{id}", cors(log(auth(http.HandlerFunc(s.handleDeleteFolder)))))

	s.mux.Handle("POST /api/v1/tests", cors(log(http.HandlerFunc(s.handleCreateTest))))
	s.mux.Handle("POST /api/v1/tests/estimate", cors(log(http.HandlerFunc(s.handleEstimateTest))))
	s.mux.Handle("GET /api/v1/tests", cors(log(http.HandlerFunc(s.handleListTests))))
	s.mux.Handle("GET /api/v1/tests/{id}", cors(log(http.HandlerFunc(s.handleGetTest))))
	s.mux.Handle("GET /api/v1/tests/{id}/sla-status", cors(log(http.HandlerFunc(s.handleGetTestSLAStatus))))
	s.mux.Handle("GET /api/v1/tests/{id}/report", cors(log(http.HandlerFunc(s.handleGetTestHTMLReport))))
	s.mux.Handle("POST /api/v1/tests/{id}/start", cors(log(http.HandlerFunc(s.handleStartTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/stop", cors(log(http.HandlerFunc(s.handleStopTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/abort", cors(log(http.HandlerFunc(s.handleAbortTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/pause", cors(log(http.HandlerFunc(s.handlePauseTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/resume", cors(log(http.HandlerFunc(s.handleResumeTest))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/vusers", cors(log(http.HandlerFunc(s.handleScaleVUsers))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/duration", cors(log(http.HandlerFunc(s.handleAdjustDuration))))
	s.mux.Handle("DELETE /api/v1/tests/{id}", cors(log(http.HandlerFunc(s.handleDeleteTest))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/move", cors(log(http.HandlerFunc(s.handleMoveTest))))
	s.mux.Handle("GET /api/v1/tests/{id}/metrics", cors(log(http.HandlerFunc(s.handleGetMetrics))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive", cors(log(http.HandlerFunc(s.handleDownloadRunArchive))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/manifest", cors(log(http.HandlerFunc(s.handleGetRunArchiveManifest))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/files/{name}", cors(log(http.HandlerFunc(s.handleDownloadRunArchiveArtifact))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/transactions", cors(log(http.HandlerFunc(s.handleGetRunArchiveTransactions))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/resources", cors(log(http.HandlerFunc(s.handleGetRunArchiveResources))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/summaries", cors(log(http.HandlerFunc(s.handleGetRunArchiveSummaries))))
	s.mux.Handle("GET /api/v1/tests/{id}/archive/report", cors(log(http.HandlerFunc(s.handleGetRunArchiveReport))))

	// Reusable scenario definitions.
	s.mux.Handle("POST /api/v1/scenarios", cors(log(http.HandlerFunc(s.handleCreateScenario))))
	s.mux.Handle("GET /api/v1/scenarios", cors(log(http.HandlerFunc(s.handleListScenarios))))
	s.mux.Handle("GET /api/v1/scenarios/{id}", cors(log(http.HandlerFunc(s.handleGetScenario))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}", cors(log(http.HandlerFunc(s.handleUpdateScenario))))
	s.mux.Handle("DELETE /api/v1/scenarios/{id}", cors(log(http.HandlerFunc(s.handleDeleteScenario))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/results", cors(log(http.HandlerFunc(s.handleScenarioResults))))
	s.mux.Handle("GET /api/v1/scenarios/{id}/data-pools", cors(log(http.HandlerFunc(s.handleListScenarioDataPools))))
	s.mux.Handle("PUT /api/v1/scenarios/{id}/data-pools", cors(log(http.HandlerFunc(s.handleUpdateScenarioDataPools))))

	// Shared data pools used across scripts in a scenario run.
	s.mux.Handle("POST /api/v1/data-pools", cors(log(http.HandlerFunc(s.handleCreateDataPool))))
	s.mux.Handle("GET /api/v1/data-pools", cors(log(http.HandlerFunc(s.handleListDataPools))))
	s.mux.Handle("GET /api/v1/data-pools/{id}", cors(log(http.HandlerFunc(s.handleGetDataPool))))
	s.mux.Handle("PUT /api/v1/data-pools/{id}", cors(log(http.HandlerFunc(s.handleUpdateDataPool))))
	s.mux.Handle("DELETE /api/v1/data-pools/{id}", cors(log(http.HandlerFunc(s.handleDeleteDataPool))))
	s.mux.Handle("GET /api/v1/data-pools/{id}/rows", cors(log(http.HandlerFunc(s.handleListDataPoolRows))))
	s.mux.Handle("POST /api/v1/data-pools/{id}/rows", cors(log(http.HandlerFunc(s.handleCreateDataPoolRow))))
	s.mux.Handle("PUT /api/v1/data-pools/{id}/rows/{row_id}", cors(log(http.HandlerFunc(s.handleUpdateDataPoolRow))))
	s.mux.Handle("DELETE /api/v1/data-pools/{id}/rows/{row_id}", cors(log(http.HandlerFunc(s.handleDeleteDataPoolRow))))

	// Script CRUD, validation, import, and export endpoints.
	s.mux.Handle("POST /api/v1/scripts", cors(log(http.HandlerFunc(s.handleCreateScript))))
	s.mux.Handle("GET /api/v1/scripts", cors(log(http.HandlerFunc(s.handleListScripts))))
	s.mux.Handle("GET /api/v1/scripts/{id}", cors(log(http.HandlerFunc(s.handleGetScript))))
	s.mux.Handle("PUT /api/v1/scripts/{id}", cors(log(http.HandlerFunc(s.handleUpdateScript))))
	s.mux.Handle("DELETE /api/v1/scripts/{id}", cors(log(http.HandlerFunc(s.handleDeleteScript))))
	s.mux.Handle("PATCH /api/v1/scripts/{id}/move", cors(log(http.HandlerFunc(s.handleMoveScript))))
	s.mux.Handle("POST /api/v1/scripts/replay", cors(log(http.HandlerFunc(s.handleReplayDraftScript))))
	s.mux.Handle("POST /api/v1/scripts/browser-replay/sessions", cors(log(http.HandlerFunc(s.handleStartBrowserReplayDraftScript))))
	s.mux.Handle("POST /api/v1/scripts/{id}/validate", cors(log(http.HandlerFunc(s.handleValidateScript))))
	s.mux.Handle("POST /api/v1/scripts/{id}/replay", cors(log(http.HandlerFunc(s.handleReplayScript))))
	s.mux.Handle("POST /api/v1/scripts/{id}/browser-replay/sessions", cors(log(http.HandlerFunc(s.handleStartBrowserReplayScript))))
	s.mux.Handle("POST /api/v1/scripts/{id}/export", cors(log(http.HandlerFunc(s.handleExportScript))))
	s.mux.Handle("GET /api/v1/scripts/{id}/download", cors(log(http.HandlerFunc(s.handleDownloadScript))))
	s.mux.Handle("POST /api/v1/scripts/import/loadrunner", cors(log(http.HandlerFunc(s.handleImportLoadRunner))))
	s.mux.Handle("POST /api/v1/scripts/import/loadrunner/validate", cors(log(http.HandlerFunc(s.handleValidateLoadRunnerImport))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter", cors(log(http.HandlerFunc(s.handleImportJMeter))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/save", cors(log(http.HandlerFunc(s.handleSaveJMeterImport))))
	s.mux.Handle("POST /api/v1/scripts/import/jmeter/validate", cors(log(http.HandlerFunc(s.handleValidateJMeterImport))))
	s.mux.Handle("GET /api/v1/scripts/import/jmeter/support-matrix", cors(log(http.HandlerFunc(s.handleJMeterSupportMatrix))))
	s.mux.Handle("POST /api/v1/scripts/import/sample", cors(log(http.HandlerFunc(s.handleImportSample))))
	s.mux.Handle("GET /api/v1/browser-replay/sessions/{session_id}", cors(log(http.HandlerFunc(s.handleGetBrowserReplaySession))))
	s.mux.Handle("POST /api/v1/browser-replay/sessions/{session_id}/pause", cors(log(http.HandlerFunc(s.handlePauseBrowserReplaySession))))
	s.mux.Handle("POST /api/v1/browser-replay/sessions/{session_id}/resume", cors(log(http.HandlerFunc(s.handleResumeBrowserReplaySession))))
	s.mux.Handle("POST /api/v1/browser-replay/sessions/{session_id}/stop", cors(log(http.HandlerFunc(s.handleStopBrowserReplaySession))))

	// Recording proxy control and artifact retrieval.
	s.mux.Handle("POST /api/v1/recorder/start", cors(log(http.HandlerFunc(s.handleStartRecording))))
	s.mux.Handle("POST /api/v1/recorder/{id}/pause", cors(log(http.HandlerFunc(s.handlePauseRecording))))
	s.mux.Handle("POST /api/v1/recorder/{id}/resume", cors(log(http.HandlerFunc(s.handleResumeRecording))))
	s.mux.Handle("POST /api/v1/recorder/{id}/stop", cors(log(http.HandlerFunc(s.handleStopRecording))))
	s.mux.Handle("GET /api/v1/recorder/{id}", cors(log(http.HandlerFunc(s.handleGetRecording))))
	s.mux.Handle("GET /api/v1/recorder", cors(log(http.HandlerFunc(s.handleListRecordings))))
	s.mux.Handle("DELETE /api/v1/recorder/{id}", cors(log(http.HandlerFunc(s.handleDeleteRecording))))
	s.mux.Handle("PATCH /api/v1/recorder/{id}/requests/{seq}", cors(log(http.HandlerFunc(s.handleUpdateRecordedRequest))))
	s.mux.Handle("POST /api/v1/recorder/{id}/convert/preview", cors(log(http.HandlerFunc(s.handlePreviewRecordingConversion))))
	s.mux.Handle("POST /api/v1/recorder/{id}/convert", cors(log(http.HandlerFunc(s.handleConvertRecording))))
	s.mux.Handle("GET /api/v1/recorder/{id}/export", cors(log(http.HandlerFunc(s.handleExportRecording))))
	s.mux.Handle("GET /api/v1/recorder/status", cors(log(http.HandlerFunc(s.handleRecorderStatus))))
	s.mux.Handle("GET /api/v1/recorder/ca.pem", cors(log(http.HandlerFunc(s.handleRecorderCACert))))

	// System endpoints for health and global settings.
	s.mux.Handle("GET /api/v1/system/health", cors(log(http.HandlerFunc(s.handleHealthCheck))))
	s.mux.Handle("GET /api/v1/system/inventory", cors(log(http.HandlerFunc(s.handleGetSystemInventory))))
	s.mux.Handle("GET /api/v1/system/settings", cors(log(auth(http.HandlerFunc(s.handleGetSystemSettings)))))
	s.mux.Handle("PUT /api/v1/system/settings", cors(log(auth(http.HandlerFunc(s.handleUpdateSystemSettings)))))

	// WebSocket — no CORS wrapper (handled by upgrade check)
	s.mux.Handle("GET /api/v1/ws", log(http.HandlerFunc(s.handleWebSocket)))
	s.mux.Handle("GET /api/v1/tests/{id}/stream", log(http.HandlerFunc(s.handleTestStream)))

	// OPTIONS preflight for all API routes
	s.mux.Handle("OPTIONS /api/", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	// Dashboard static files — serve from embedded FS, fallback to index.html for SPA routing
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		slog.Warn("embedded static files not available, dashboard will 404")
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<!DOCTYPE html><html><body><h1>Stormrunner</h1><p>Dashboard not built. Run: cd dashboard && npm run build</p></body></html>"))
		}))
	} else {
		fileServer := http.FileServer(http.FS(staticSub))
		s.mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Serve real files directly. Only extensionless client routes fall back to the SPA shell.
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticSub, path); err != nil {
				if path != "index.html" && strings.Contains(path, ".") {
					http.NotFound(w, r)
					return
				}
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		}))
	}
}

func (s *Server) Start() error {
	// Start websocket broadcast loop before serving HTTP requests.
	go s.hub.Run()

	if err := s.recorder.StartProxy(); err != nil {
		return fmt.Errorf("starting recording proxy: %w", err)
	}

	slog.Info("stormrunner API server starting", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	// Stop background components before closing active HTTP listeners.
	s.hub.Stop()
	s.recorder.StopProxy()
	return s.httpServer.Shutdown(ctx)
}
