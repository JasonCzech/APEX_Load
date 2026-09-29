package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

const (
	runnerAddressEnvironment       = "APEXLOAD_JAVA_VUSER_RUNNER_ADDR"
	runnerPilotManifestEnvironment = "APEXLOAD_JAVA_VUSER_PILOT_MANIFEST"
	defaultRunnerAddress           = "127.0.0.1:8098"
)

func main() {
	address := strings.TrimSpace(os.Getenv(runnerAddressEnvironment))
	if address == "" {
		address = defaultRunnerAddress
	}
	operator := config.LoadJavaVuserOperatorConfig()
	startupCtx, startupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	options, err := runnerServerOptions(startupCtx, operator, strings.TrimSpace(os.Getenv(runnerPilotManifestEnvironment)))
	startupCancel()
	if err != nil {
		log.Fatalf("Java Vuser runner sidecar refused pilot configuration: %v", err)
	}
	listener, err := javasidecar.ListenLoopback(address)
	if err != nil {
		log.Fatalf("Java Vuser runner sidecar refused its listener: %v", err)
	}
	if operator.ClasspathUploadEnabled && strings.TrimSpace(operator.ClasspathAssetDir) != "" {
		// Allow the sidecar to read uploaded classpath JAR bytes from the
		// controller's import asset store (same-host coupling). If this fails,
		// uploaded dependencies simply fail to resolve (fail closed) rather than
		// taking down the runner.
		if err := fileasset.RegisterProjectRoot(operator.ClasspathAssetDir); err != nil {
			log.Printf("Java Vuser runner sidecar could not register classpath asset dir %q: %v", operator.ClasspathAssetDir, err)
		}
	}
	runner := javasidecar.NewServer(options)
	server := &http.Server{
		Handler:           runner,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	signalCtx, signalStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer signalStop()
	ctx, stop := context.WithCancel(signalCtx)
	defer stop()
	go func() {
		select {
		case <-ctx.Done():
		case <-runner.ContainmentFailed():
			// The signal itself is emitted only after an immediate containment
			// failure or after the run-scoped cleanup watchdog expires. Do not add
			// another delay: the advertised outer bound is lease TTL + 2 seconds.
			os.Exit(1)
		case <-runner.OwnerLeaseExpired():
			go func() {
				time.Sleep(2 * time.Second)
				os.Exit(1)
			}()
			stop()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = runner.Shutdown(shutdownCtx)
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("Java Vuser runner sidecar listening on %s", listener.Addr())
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Java Vuser runner sidecar stopped: %v", err)
	}
}

// Pilot configuration is supplied solely by the operator environment, never DSL.
func runnerServerOptions(ctx context.Context, operator config.JavaVuserOperatorConfig, manifest string) (javasidecar.ServerOptions, error) {
	options := javasidecar.ServerOptions{Operator: operator}
	if manifest == "" {
		return options, nil
	}
	if runtime.GOOS != "linux" || !operator.Enabled || operator.AuthToken == "" {
		return options, javasidecar.ErrPooledPilotManifest
	}
	pilot, err := javasidecar.LoadPooledPilotManifest(ctx, manifest, operator.MaxProcesses)
	if err != nil {
		return options, err
	}
	options.PooledControlProfiles, options.HostAdmission = pilot.Profiles, pilot.Admission
	return options, nil
}
