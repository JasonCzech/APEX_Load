package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasonczech/stormrunner/pkg/config"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/http"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.DefaultGeneratorConfig()
	_ = cfg

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("stormrunner generator starting", "addr", cfg.ListenAddr, "max_vusers", cfg.MaxVUsers)

	// Phase 1: generator runs as part of stormctl direct execution.
	// Phase 2 will add gRPC server for controller-managed distributed mode.

	<-ctx.Done()
	slog.Info("generator shutting down")
}
