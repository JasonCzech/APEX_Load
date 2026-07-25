package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/fileasset"
	"github.com/jasonczech/apexload/pkg/javasidecar"
)

const (
	runnerAddressEnvironment = "APEXLOAD_JAVA_VUSER_RUNNER_ADDR"
	defaultRunnerAddress     = "127.0.0.1:8098"
)

func main() {
	address := strings.TrimSpace(os.Getenv(runnerAddressEnvironment))
	if address == "" {
		address = defaultRunnerAddress
	}
	listener, err := javasidecar.ListenLoopback(address)
	if err != nil {
		log.Fatalf("Java Vuser runner sidecar refused its listener: %v", err)
	}
	operator := config.LoadJavaVuserOperatorConfig()
	if operator.ClasspathUploadEnabled && strings.TrimSpace(operator.ClasspathAssetDir) != "" {
		// Allow the sidecar to read uploaded classpath JAR bytes from the
		// controller's import asset store (same-host coupling). If this fails,
		// uploaded dependencies simply fail to resolve (fail closed) rather than
		// taking down the runner.
		if err := fileasset.RegisterProjectRoot(operator.ClasspathAssetDir); err != nil {
			log.Printf("Java Vuser runner sidecar could not register classpath asset dir %q: %v", operator.ClasspathAssetDir, err)
		}
	}
	runner := javasidecar.NewServer(javasidecar.ServerOptions{Operator: operator})
	server := &http.Server{
		Handler:           runner,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
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
