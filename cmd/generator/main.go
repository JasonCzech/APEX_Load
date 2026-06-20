package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/debugserver"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/browser"
	_ "github.com/jasonczech/apexload/pkg/protocol/database"
	_ "github.com/jasonczech/apexload/pkg/protocol/grpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_http"
	_ "github.com/jasonczech/apexload/pkg/protocol/java_vuser"
	_ "github.com/jasonczech/apexload/pkg/protocol/jms"
	_ "github.com/jasonczech/apexload/pkg/protocol/kafka"
	_ "github.com/jasonczech/apexload/pkg/protocol/network"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
	"google.golang.org/grpc"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Optional pprof debug server (disabled unless APEXLOAD_PPROF_ADDR is set).
	if pprofSrv, err := debugserver.StartPprofFromEnv(os.Getenv); err != nil {
		slog.Error("failed to start pprof debug server", "err", err)
	} else if pprofSrv != nil {
		defer pprofSrv.Close()
	}

	cfg := config.DefaultGeneratorConfig()
	_ = cfg

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	generatorID, err := os.Hostname()
	if err != nil || generatorID == "" {
		generatorID = "apexload-generator"
	}

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		slog.Error("failed to listen", "addr", cfg.ListenAddr, "err", err)
		os.Exit(1)
	}
	defer listener.Close()

	serverOptions, err := generatorrpc.ServerOptionsFromEnv()
	if err != nil {
		slog.Error("invalid gRPC transport configuration", "err", err)
		os.Exit(1)
	}
	security := generatorrpc.TransportSecurityFromEnv()
	appEnv := os.Getenv("APEXLOAD_ENV")
	if security.MustRefuseInsecure(appEnv) {
		// Fail closed: a plaintext, tokenless gRPC channel is never acceptable
		// in production. Refuse to start so a missing/misnamed token or TLS
		// secret cannot silently degrade the controller<->generator channel.
		slog.Error("refusing to start: generator gRPC is plaintext and unauthenticated with APEXLOAD_ENV=production; set APEXLOAD_GRPC_TLS_* and/or APEXLOAD_GRPC_AUTH_TOKEN")
		os.Exit(1)
	}
	if security.Insecure() {
		slog.Warn("generator gRPC is plaintext and unauthenticated; set APEXLOAD_GRPC_TLS_* and/or APEXLOAD_GRPC_AUTH_TOKEN")
	}

	grpcServer := grpc.NewServer(serverOptions...)
	pb.RegisterGeneratorControlServer(grpcServer, generatorrpc.NewService(generatorID, cfg.MaxVUsers, generatorrpc.WithExecution()))

	errCh := make(chan error, 1)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()

	slog.Info("apexload generator starting", "addr", cfg.ListenAddr, "generator_id", generatorID, "max_vusers", cfg.MaxVUsers, "grpc_transport", security.String())

	select {
	case <-ctx.Done():
		slog.Info("generator shutting down")
		grpcServer.GracefulStop()
	case err := <-errCh:
		slog.Error("generator server error", "err", err)
		os.Exit(1)
	}
}
