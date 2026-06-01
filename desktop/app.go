package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx    context.Context
	server *api.Server
	store  storage.Store
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialize the database storage
	store, err := storage.NewStoreFromEnv()
	if err != nil {
		slog.Error("failed to initialize storage", "err", err)
		return
	}
	a.store = store

	// Initialize the API Server
	a.server = api.NewServer(api.ServerConfig{
		Addr:          "127.0.0.1:8080",
		MaxConcurrent: 10,
		Store:         store,
	})

	// Start the API server in the background
	go func() {
		if err := a.server.Start(); err != nil {
			slog.Error("server error", "err", err)
		}
	}()

	// Wait briefly for the server to be ready
	time.Sleep(200 * time.Millisecond)
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	if a.server != nil {
		a.server.Shutdown(context.Background())
	}
	if a.store != nil {
		a.store.Close()
	}
}

// SelectFile opens a native OS file dialog and returns the selected file path
func (a *App) SelectFile(title string) (string, error) {
	return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
	})
}
