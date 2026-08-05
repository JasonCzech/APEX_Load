package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/debugserver"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/importartifact"
	_ "github.com/jasonczech/apexload/pkg/protocol/builtin"
	"github.com/jasonczech/apexload/pkg/runtimetuning"
	"github.com/jasonczech/apexload/pkg/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	generatorTerminationBudget = 120 * time.Second
	shutdownSafetyMargin       = 10 * time.Second
	serviceShutdownTimeout     = 10 * time.Second
	grpcShutdownTimeout        = 5 * time.Second
	activeVUserDrainTimeout    = generatorTerminationBudget - shutdownSafetyMargin - serviceShutdownTimeout - 2*grpcShutdownTimeout
)

type grpcStopper interface {
	GracefulStop()
	Stop()
}

type generatorReadinessSource interface {
	ReadinessError() error
	ReadinessChanges() <-chan struct{}
}

type generatorShutdownService interface {
	SetDraining(bool)
	ActiveVUsers() int32
	Shutdown(context.Context) error
}

type healthStatusSetter interface {
	SetServingStatus(string, healthpb.HealthCheckResponse_ServingStatus)
}

func main() {
	loadLocalDotEnv()
	logLevel, logLevelErr := config.ResolveLogLevel(os.Getenv)
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
	if logLevelErr != nil {
		slog.Warn("invalid log-level configuration; using info", "err", logLevelErr)
	}
	logRuntimeTuning(runtimetuning.ApplyFromCgroup(os.Getenv))
	build := buildinfo.Get()
	tracingConfig, err := telemetry.LoadConfig(os.Getenv, "apexload-generator", build.Version)
	if err != nil {
		slog.Error("invalid OpenTelemetry tracing configuration", "err", err)
		os.Exit(1)
	}
	tracingRuntime, err := telemetry.Configure(context.Background(), tracingConfig)
	if err != nil {
		slog.Error("failed to initialize OpenTelemetry tracing", "err", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tracingRuntime.Shutdown(shutdownCtx); err != nil {
			slog.Error("failed to flush OpenTelemetry tracing", "err", err)
		}
	}()
	if status := tracingRuntime.Status(); status.Ready {
		slog.Info("OpenTelemetry tracing enabled", "service", status.ServiceName, "protocol", status.Protocol, "sampler", status.Sampler)
	}

	// Optional pprof debug server (disabled unless APEXLOAD_PPROF_ADDR is set).
	if pprofSrv, err := debugserver.StartPprofFromEnv(os.Getenv); err != nil {
		slog.Error("failed to start pprof debug server", "err", err)
	} else if pprofSrv != nil {
		defer pprofSrv.Close()
	}

	cfg := config.LoadGeneratorConfigFromEnv(os.Getenv)
	restoreBrunoResolver, err := configureBrunoResolver()
	if err != nil {
		slog.Error("invalid Bruno distributed artifact configuration", "err", err)
		os.Exit(1)
	}
	defer restoreBrunoResolver()

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
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
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
	if err := controlService.ReadinessError(); err != nil {
		slog.Error("refusing to start without durable generator execution fencing", "generator_id", generatorID, "err", err)
		os.Exit(1)
	}
	pb.RegisterGeneratorControlServer(grpcServer, controlService)
	publishGeneratorHealth(controlService, healthServer)
	go monitorGeneratorHealth(ctx, controlService, healthServer)

	errCh := make(chan error, 2)
	go func() {
		errCh <- grpcServer.Serve(listener)
	}()
	go func() {
		errCh <- healthGRPCServer.Serve(healthListener)
	}()

	slog.Info("apexload generator starting", "addr", cfg.ListenAddr, "health_addr", healthAddr, "generator_id", generatorID, "max_vusers", cfg.MaxVUsers, "grpc_transport", security.String())

	waitForNaturalDrain := false
	serverFailed := false
	select {
	case <-ctx.Done():
		slog.Info("generator shutting down")
		waitForNaturalDrain = true
	case err := <-errCh:
		slog.Error("generator server error", "err", err)
		serverFailed = true
	}

	// A fatal Serve error must take the same archive-safe shutdown path as a
	// signal. Skipping directly to os.Exit can leave open run manifests and the
	// durable terminal evidence needed by the replacement controller. Do not
	// wait for a natural drain when one listener is already unavailable.
	shutdownGeneratorRuntime(controlService, healthServer, grpcServer, healthGRPCServer, waitForNaturalDrain)
	if serverFailed {
		os.Exit(1)
	}
}

func configureBrunoResolver() (func(), error) {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED")))
	if err != nil && strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED")) != "" {
		return nil, fmt.Errorf("APEXLOAD_BRUNO_DISTRIBUTED_EXECUTION_ENABLED: %w", err)
	}
	if !enabled {
		return func() {}, nil
	}
	root := strings.TrimSpace(os.Getenv("APEXLOAD_BRUNO_ARTIFACT_ROOT"))
	if root == "" {
		return nil, errors.New("APEXLOAD_BRUNO_ARTIFACT_ROOT is required when distributed Bruno execution is enabled")
	}
	wrapper, err := importartifact.NewConfiguredKeyWrapper(false)
	if err != nil {
		return nil, err
	}
	objects, err := importartifact.NewEncryptedStore(importartifact.StoreOptions{
		RootDir:  root,
		Wrapper:  wrapper,
		ReadOnly: true,
	})
	if err != nil {
		return nil, err
	}
	return importartifact.SetResolver(&importartifact.ContentAddressedResolver{
		Objects: objects,
		Kind:    "sanitized",
	}), nil
}

func shutdownGeneratorRuntime(service generatorShutdownService, health healthStatusSetter, controlServer grpcStopper, healthServer grpcStopper, waitForNaturalDrain bool) {
	if service == nil {
		return
	}
	service.SetDraining(true)
	if health != nil {
		health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	}
	if waitForNaturalDrain {
		deadline := time.NewTimer(activeVUserDrainTimeout)
		ticker := time.NewTicker(500 * time.Millisecond)
		for service.ActiveVUsers() > 0 {
			select {
			case <-deadline.C:
				slog.Warn("generator drain deadline reached", "active_vusers", service.ActiveVUsers())
				goto drained
			case <-ticker.C:
			}
		}
	drained:
		ticker.Stop()
		deadline.Stop()
	}

	drainCtx, drainCancel := context.WithTimeout(context.Background(), serviceShutdownTimeout)
	if err := service.Shutdown(drainCtx); err != nil {
		slog.Warn("generator service shutdown incomplete", "err", err)
	}
	drainCancel()
	if controlServer != nil && !gracefulStopWithTimeout(controlServer, grpcShutdownTimeout) {
		slog.Warn("generator control gRPC graceful shutdown timed out; forced stop")
	}
	if healthServer != nil && !gracefulStopWithTimeout(healthServer, grpcShutdownTimeout) {
		slog.Warn("generator health gRPC graceful shutdown timed out; forced stop")
	}
}

func publishGeneratorHealth(source generatorReadinessSource, health healthStatusSetter) {
	if health == nil {
		return
	}
	if source == nil || source.ReadinessError() != nil {
		health.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
		return
	}
	health.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
}

func monitorGeneratorHealth(ctx context.Context, source generatorReadinessSource, health healthStatusSetter) {
	if ctx == nil || source == nil || health == nil {
		return
	}
	changes := source.ReadinessChanges()
	for {
		select {
		case <-ctx.Done():
			return
		case <-changes:
			publishGeneratorHealth(source, health)
		}
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

func loadLocalDotEnv() {
	appEnv := strings.ToLower(strings.TrimSpace(os.Getenv("APEXLOAD_ENV")))
	if appEnv == "production" || appEnv == "prod" {
		return
	}
	if err := config.LoadDotEnv(".env"); err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Warn("failed to load local .env", "err", err)
	}
}
