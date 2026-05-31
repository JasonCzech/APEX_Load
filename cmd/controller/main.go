package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/api"
	_ "github.com/jasonczech/apexload/pkg/protocol/browser"
	_ "github.com/jasonczech/apexload/pkg/protocol/database"
	_ "github.com/jasonczech/apexload/pkg/protocol/grpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_http"
	_ "github.com/jasonczech/apexload/pkg/protocol/jms"
	_ "github.com/jasonczech/apexload/pkg/protocol/network"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
	"github.com/jasonczech/apexload/pkg/storage"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Controller HTTP bind address (for API + embedded dashboard).
	addr := os.Getenv("APEXLOAD_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	store, err := storage.NewStoreFromEnv()
	if err != nil {
		slog.Error("failed to initialize storage", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	generatorTargets := parseGeneratorTargets(os.Getenv("APEXLOAD_GENERATORS"))
	generatorTimeout := parseGeneratorTimeout(os.Getenv("APEXLOAD_GENERATOR_TIMEOUT"))
	recorderProxyAddr := recorderProxyAddrFromEnv()
	if len(generatorTargets) > 0 {
		slog.Info("distributed execution enabled", "generators", generatorTargets, "timeout", generatorTimeout)
	}

	server := api.NewServer(api.ServerConfig{
		Addr:             addr,
		ProxyAddr:        recorderProxyAddr,
		MaxConcurrent:    10,
		Store:            store,
		GeneratorTargets: generatorTargets,
		GeneratorTimeout: generatorTimeout,
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := server.RunDueSchedules(); err != nil {
					slog.Error("scheduled test evaluator failed", "err", err)
				}
			}
		}
	}()

	// Run server in background so the main goroutine can handle shutdown signals.
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

func parseGeneratorTargets(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	targets := make([]string, 0, len(parts))
	for _, part := range parts {
		target := strings.TrimSpace(part)
		if target == "" {
			continue
		}
		targets = append(targets, target)
	}
	return targets
}

func parseGeneratorTimeout(raw string) time.Duration {
	if raw == "" {
		return 10 * time.Second
	}
	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		slog.Warn("invalid APEXLOAD_GENERATOR_TIMEOUT, falling back to default", "value", raw)
		return 10 * time.Second
	}
	return duration
}

func recorderProxyAddrFromEnv() string {
	return strings.TrimSpace(os.Getenv("APEXLOAD_RECORDER_PROXY_ADDR"))
}
