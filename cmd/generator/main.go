package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/debugserver"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
	"github.com/jasonczech/apexload/pkg/runtimetuning"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	activeVUserDrainTimeout = 100 * time.Second
	serviceShutdownTimeout  = 10 * time.Second
	grpcShutdownTimeout     = 5 * time.Second
)

type grpcStopper interface {
	GracefulStop()
	Stop()
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	logRuntimeTuning(runtimetuning.ApplyFromCgroup(os.Getenv))

	// Optional pprof debug server (disabled unless APEXLOAD_PPROF_ADDR is set).
	if pprofSrv, err := debugserver.StartPprofFromEnv(os.Getenv); err != nil {
		slog.Error("failed to start pprof debug server", "err", err)
	} else if pprofSrv != nil {
		defer pprofSrv.Close()
	}

	cfg := config.LoadGeneratorConfigFromEnv(os.Getenv)

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
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	// Keep health on a dedicated service-only listener. Kubernetes native gRPC
	// probes cannot present the production mTLS certificate or bearer token, so
	// probing the secured control listener makes every healthy pod unready.
	healthAddr := strings.TrimSpace(os.Getenv("APEXLOAD_GENERATOR_HEALTH_ADDR"))
	if healthAddr == "" {
		healthAddr = ":9092"
	}
	healthListener, err := net.Listen("tcp", healthAddr)
	if err != nil {
		slog.Error("failed to listen for generator health", "addr", healthAddr, "err", err)
		os.Exit(1)
	}
	defer healthListener.Close()
	healthGRPCServer := grpc.NewServer()
	healthpb.RegisterHealthServer(healthGRPCServer, healthServer)
	controlService := generatorrpc.NewService(generatorID, cfg.MaxVUsers, generatorrpc.WithExecution())
	pb.RegisterGeneratorControlServer(grpcServer, controlService)

	errCh := make(chan error, 2)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()
	go func() {
		errCh <- healthGRPCServer.Serve(healthListener)
	}()

	slog.Info("apexload generator starting", "addr", cfg.ListenAddr, "health_addr", healthAddr, "generator_id", generatorID, "max_vusers", cfg.MaxVUsers, "grpc_transport", security.String())

	select {
	case <-ctx.Done():
		slog.Info("generator shutting down")
		controlService.SetDraining(true)
		healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		deadline := time.NewTimer(activeVUserDrainTimeout)
		defer deadline.Stop()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for controlService.ActiveVUsers() > 0 {
			select {
			case <-deadline.C:
				slog.Warn("generator drain deadline reached", "active_vusers", controlService.ActiveVUsers())
				goto stop
			case <-ticker.C:
			}
		}
	stop:
		drainCtx, drainCancel := context.WithTimeout(context.Background(), serviceShutdownTimeout)
		if err := controlService.Shutdown(drainCtx); err != nil {
			slog.Warn("generator service shutdown incomplete", "err", err)
		}
		drainCancel()
		if !gracefulStopWithTimeout(grpcServer, grpcShutdownTimeout) {
			slog.Warn("generator control gRPC graceful shutdown timed out; forced stop")
		}
		if !gracefulStopWithTimeout(healthGRPCServer, grpcShutdownTimeout) {
			slog.Warn("generator health gRPC graceful shutdown timed out; forced stop")
		}
	case err := <-errCh:
		slog.Error("generator server error", "err", err)
		os.Exit(1)
	}
}

func gracefulStopWithTimeout(server grpcStopper, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		server.Stop()
		<-done
		return false
	}
}

func logRuntimeTuning(settings runtimetuning.Settings) {
	if settings.GOMAXPROCS == 0 && settings.GOMEMLIMIT == 0 {
		return
	}
	args := []any{}
	if settings.GOMAXPROCS > 0 {
		args = append(args, "gomaxprocs", settings.GOMAXPROCS, "gomaxprocs_source", settings.GOMAXPROCSSource)
	}
	if settings.GOMEMLIMIT > 0 {
		args = append(args, "gomemlimit", settings.GOMEMLIMIT, "gomemlimit_source", settings.GOMEMLIMITSource)
	}
	slog.Info("go runtime tuning applied", args...)
}
