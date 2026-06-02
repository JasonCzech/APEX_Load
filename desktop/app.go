package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/storage"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx         context.Context
	server      *api.Server
	store       storage.Store
	appDataPath string
	logPath     string
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	appDataPath, err := desktopAppDataPath()
	if err != nil {
		slog.Error("failed to resolve app data path", "err", err)
		return
	}
	a.appDataPath = appDataPath
	if err := os.MkdirAll(appDataPath, 0o755); err != nil {
		slog.Error("failed to create app data path", "path", appDataPath, "err", err)
		return
	}
	logPath := filepath.Join(appDataPath, "logs")
	if err := os.MkdirAll(logPath, 0o755); err != nil {
		slog.Error("failed to create log path", "path", logPath, "err", err)
		return
	}
	a.logPath = logPath

	// Initialize the database storage
	storeConfig := storage.StoreConfigFromEnv()
	if stringsTrimmedEnv("APEXLOAD_DB") == "" && stringsTrimmedEnv("APEXLOAD_DATABASE_URL") == "" {
		storeConfig.Backend = storage.StoreBackendSQLite
		storeConfig.SQLitePath = filepath.Join(appDataPath, "apexload.db")
	}
	store, err := storage.NewConfiguredStore(storeConfig)
	if err != nil {
		slog.Error("failed to initialize storage", "err", err)
		return
	}
	a.store = store

	// Initialize the API Server
	a.server = api.NewServer(api.ServerConfig{
		Addr:               "127.0.0.1:0",
		MaxConcurrent:      10,
		Store:              store,
		LibraryCredentials: api.NewKeyringLibraryCredentialStore(),
		DesktopMode:        true,
		DesktopAppDataPath: appDataPath,
		DesktopLogPath:     logPath,
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

func (a *App) GetAPIOrigin() string {
	if a.server == nil {
		return ""
	}
	return a.server.BrowserURL()
}

func (a *App) OpenInBrowser() error {
	if a.server == nil {
		return fmt.Errorf("API server is not running")
	}
	runtime.BrowserOpenURL(a.ctx, a.server.BrowserURL())
	return nil
}

func (a *App) OpenLogsFolder() error {
	return a.openFolder(a.logPath)
}

func (a *App) OpenAppDataFolder() error {
	return a.openFolder(a.appDataPath)
}

func (a *App) openFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("folder path is not available")
	}
	runtime.BrowserOpenURL(a.ctx, (&url.URL{Scheme: "file", Path: path}).String())
	return nil
}

func desktopAppDataPath() (string, error) {
	if dir := stringsTrimmedEnv("APEXLOAD_DESKTOP_APP_DATA"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "APEX Load"), nil
}

func stringsTrimmedEnv(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
