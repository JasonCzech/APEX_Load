package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/jasonczech/stormrunner/api/proto"
	"github.com/jasonczech/stormrunner/pkg/distributed/generatorrpc"
	"github.com/jasonczech/stormrunner/pkg/config"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/browser"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/http"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/java_http"
	_ "github.com/jasonczech/stormrunner/pkg/protocol/soap"
	"google.golang.org/grpc"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.DefaultGeneratorConfig()
	_ = cfg

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	generatorID, err := os.Hostname()
	if err != nil || generatorID == "" {
		generatorID = "stormrunner-generator"
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		slog.Error("failed to listen", "addr", cfg.ListenAddr, "err", err)
		os.Exit(1)
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	pb.RegisterGeneratorControlServer(grpcServer, generatorrpc.NewService(generatorID, cfg.MaxVUsers, generatorrpc.WithExecution()))

	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()

	slog.Info("stormrunner generator starting", "addr", cfg.ListenAddr, "generator_id", generatorID, "max_vusers", cfg.MaxVUsers)

	select {
	case <-ctx.Done():
		slog.Info("generator shutting down")
		grpcServer.GracefulStop()
	case err := <-errCh:
		slog.Error("generator server error", "err", err)
		os.Exit(1)
	}
}
