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
	httpServer *http.Server
	hub        *Hub
	runner     *TestRunner
	recorder   *recorder.Recorder
	mux        *http.ServeMux
}

type ServerConfig struct {
	Addr           string
	ProxyAddr      string
	MaxConcurrent  int
	AllowedOrigins []string
	Store          storage.Store
}

func NewServer(cfg ServerConfig) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 10
	}

	hub := NewHub()
	runner := NewTestRunner(cfg.MaxConcurrent, hub, cfg.Store)
	rec := recorder.New(cfg.ProxyAddr)

	s := &Server{
		hub:      hub,
		runner:   runner,
		recorder: rec,
		mux:      http.NewServeMux(),
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

	// API routes
	s.mux.Handle("POST /api/v1/tests", cors(log(http.HandlerFunc(s.handleCreateTest))))
	s.mux.Handle("GET /api/v1/tests", cors(log(http.HandlerFunc(s.handleListTests))))
	s.mux.Handle("GET /api/v1/tests/{id}", cors(log(http.HandlerFunc(s.handleGetTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/start", cors(log(http.HandlerFunc(s.handleStartTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/stop", cors(log(http.HandlerFunc(s.handleStopTest))))
	s.mux.Handle("POST /api/v1/tests/{id}/abort", cors(log(http.HandlerFunc(s.handleAbortTest))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/vusers", cors(log(http.HandlerFunc(s.handleScaleVUsers))))
	s.mux.Handle("PATCH /api/v1/tests/{id}/duration", cors(log(http.HandlerFunc(s.handleAdjustDuration))))
	s.mux.Handle("DELETE /api/v1/tests/{id}", cors(log(http.HandlerFunc(s.handleDeleteTest))))
	s.mux.Handle("GET /api/v1/tests/{id}/metrics", cors(log(http.HandlerFunc(s.handleGetMetrics))))

	s.mux.Handle("POST /api/v1/scripts", cors(log(http.HandlerFunc(s.handleCreateScript))))
	s.mux.Handle("GET /api/v1/scripts", cors(log(http.HandlerFunc(s.handleListScripts))))
	s.mux.Handle("GET /api/v1/scripts/{id}", cors(log(http.HandlerFunc(s.handleGetScript))))
	s.mux.Handle("PUT /api/v1/scripts/{id}", cors(log(http.HandlerFunc(s.handleUpdateScript))))
	s.mux.Handle("DELETE /api/v1/scripts/{id}", cors(log(http.HandlerFunc(s.handleDeleteScript))))
	s.mux.Handle("POST /api/v1/scripts/{id}/validate", cors(log(http.HandlerFunc(s.handleValidateScript))))
	s.mux.Handle("POST /api/v1/scripts/{id}/export", cors(log(http.HandlerFunc(s.handleExportScript))))
	s.mux.Handle("GET /api/v1/scripts/{id}/download", cors(log(http.HandlerFunc(s.handleDownloadScript))))
	s.mux.Handle("POST /api/v1/scripts/import/loadrunner", cors(log(http.HandlerFunc(s.handleImportLoadRunner))))

	// Recording proxy
	s.mux.Handle("POST /api/v1/recorder/start", cors(log(http.HandlerFunc(s.handleStartRecording))))
	s.mux.Handle("POST /api/v1/recorder/{id}/stop", cors(log(http.HandlerFunc(s.handleStopRecording))))
	s.mux.Handle("GET /api/v1/recorder/{id}", cors(log(http.HandlerFunc(s.handleGetRecording))))
	s.mux.Handle("GET /api/v1/recorder", cors(log(http.HandlerFunc(s.handleListRecordings))))
	s.mux.Handle("DELETE /api/v1/recorder/{id}", cors(log(http.HandlerFunc(s.handleDeleteRecording))))
	s.mux.Handle("POST /api/v1/recorder/{id}/convert", cors(log(http.HandlerFunc(s.handleConvertRecording))))
	s.mux.Handle("GET /api/v1/recorder/status", cors(log(http.HandlerFunc(s.handleRecorderStatus))))

	s.mux.Handle("GET /api/v1/system/health", cors(log(http.HandlerFunc(s.handleHealthCheck))))

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
			// Try serving the file; if it doesn't exist, serve index.html (SPA)
			path := strings.TrimPrefix(r.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(staticSub, path); err != nil {
				r.URL.Path = "/"
			}
			fileServer.ServeHTTP(w, r)
		}))
	}
}

func (s *Server) Start() error {
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
	s.hub.Stop()
	s.recorder.StopProxy()
	return s.httpServer.Shutdown(ctx)
}
