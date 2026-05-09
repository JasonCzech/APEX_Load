package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasonczech/stormrunner/pkg/api"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/http"
	"github.com/jasonczech/stormrunner/pkg/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	addr := os.Getenv("STORMRUNNER_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	dbPath := os.Getenv("STORMRUNNER_DB")
	if dbPath == "" {
		dbPath = "stormrunner.db"
	}

	store, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		slog.Error("failed to initialize storage", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	server := api.NewServer(api.ServerConfig{
		Addr:          addr,
		MaxConcurrent: 10,
		Store:         store,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := server.Start(); err != nil {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	server.Shutdown(context.Background())
}
