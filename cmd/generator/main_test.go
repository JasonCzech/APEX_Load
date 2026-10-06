package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestGeneratorSafetyWatchdogRemainsIndependentOfShutdownSelect(t *testing.T) {
	requests := make(chan generatorrpc.SelfTerminationRequest, 1)
	exits := make(chan int, 1)
	triggered := startGeneratorSafetyWatchdog(requests, 5*time.Millisecond, func(code int) { exits <- code })
	requests <- generatorrpc.SelfTerminationRequest{TestID: "run", Reason: "controller_supervision_lost", Outstanding: 1}
	select {
	case <-triggered:
	case <-time.After(time.Second):
		t.Fatal("watchdog did not observe self-termination request")
	}
	select {
	case code := <-exits:
		if code != 1 {
			t.Fatalf("watchdog exit code = %d, want 1", code)
		}
	case <-time.After(time.Second):
		t.Fatal("watchdog did not force exit")
	}
}

type blockingGRPCStopper struct {
	stopOnce sync.Once
	stopCh   chan struct{}
	stopped  chan struct{}
}

func (s *blockingGRPCStopper) GracefulStop() {
	<-s.stopCh
	close(s.stopped)
}

func (s *blockingGRPCStopper) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func TestGracefulStopWithTimeoutForcesStop(t *testing.T) {
	server := &blockingGRPCStopper{stopCh: make(chan struct{}), stopped: make(chan struct{})}
	started := time.Now()
	if graceful := gracefulStopWithTimeout(server, 10*time.Millisecond); graceful {
		t.Fatal("expected the blocked graceful stop to time out")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("forced stop took too long: %v", elapsed)
	}
	select {
	case <-server.stopped:
	default:
		t.Fatal("GracefulStop goroutine was not released by Stop")
	}
}

type immediateGRPCStopper struct {
	forced bool
}

func (*immediateGRPCStopper) GracefulStop() {}
func (s *immediateGRPCStopper) Stop()       { s.forced = true }

func TestGracefulStopWithTimeoutCompletesGracefully(t *testing.T) {
	server := &immediateGRPCStopper{}
	if graceful := gracefulStopWithTimeout(server, time.Second); !graceful {
		t.Fatal("expected graceful stop to complete")
	}
	if server.forced {
		t.Fatal("Stop was called after GracefulStop completed")
	}
}

func TestShutdownPhasesLeavePodTerminationSafetyMargin(t *testing.T) {
	used := activeVUserDrainTimeout + serviceShutdownTimeout + 2*grpcShutdownTimeout
	if used+shutdownSafetyMargin != generatorTerminationBudget {
		t.Fatalf("shutdown budget = used:%v margin:%v total:%v, want %v", used, shutdownSafetyMargin, used+shutdownSafetyMargin, generatorTerminationBudget)
	}
	if shutdownSafetyMargin <= 0 {
		t.Fatal("generator shutdown has no scheduling margin before Kubernetes SIGKILL")
	}
}

type recordingGeneratorShutdownService struct {
	draining bool
	stopped  bool
}

func (s *recordingGeneratorShutdownService) SetDraining(draining bool) { s.draining = draining }
func (*recordingGeneratorShutdownService) ActiveVUsers() int32         { return 7 }
func (s *recordingGeneratorShutdownService) Shutdown(context.Context) error {
	s.stopped = true
	return nil
}

func TestFatalServerPathUsesArchiveSafeShutdownWithoutNaturalDrain(t *testing.T) {
	service := &recordingGeneratorShutdownService{}
	health := &recordingHealthStatus{statuses: make(chan healthpb.HealthCheckResponse_ServingStatus, 1)}
	controlServer := &immediateGRPCStopper{}
	healthServer := &immediateGRPCStopper{}

	// A Serve failure passes false: active work is canceled/finalized immediately
	// because the generator can no longer provide a complete control surface.
	shutdownGeneratorRuntime(service, health, controlServer, healthServer, false)

	if !service.draining || !service.stopped {
		t.Fatalf("fatal shutdown state = draining:%v stopped:%v, want true/true", service.draining, service.stopped)
	}
	select {
	case got := <-health.statuses:
		if got != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Fatalf("fatal shutdown health = %s, want NOT_SERVING", got)
		}
	default:
		t.Fatal("fatal shutdown did not revoke generator health")
	}
	if controlServer.forced || healthServer.forced {
		t.Fatal("immediate gRPC servers were force-stopped during fatal cleanup")
	}
}

type mutableReadiness struct {
	mu      sync.Mutex
	err     error
	changes chan struct{}
}

func (r *mutableReadiness) ReadinessError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *mutableReadiness) ReadinessChanges() <-chan struct{} { return r.changes }

func (r *mutableReadiness) set(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
	r.changes <- struct{}{}
}

type recordingHealthStatus struct {
	statuses chan healthpb.HealthCheckResponse_ServingStatus
}

func (h *recordingHealthStatus) SetServingStatus(_ string, status healthpb.HealthCheckResponse_ServingStatus) {
	h.statuses <- status
}

func TestMonitorGeneratorHealthRevokesServingAfterRuntimeReadinessFailure(t *testing.T) {
	readiness := &mutableReadiness{changes: make(chan struct{}, 1)}
	health := &recordingHealthStatus{statuses: make(chan healthpb.HealthCheckResponse_ServingStatus, 4)}
	publishGeneratorHealth(readiness, health)
	if status := <-health.statuses; status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("initial health = %s, want SERVING", status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		monitorGeneratorHealth(ctx, readiness, health)
		close(done)
	}()
	readiness.set(errors.New("durable epoch persistence failed"))
	select {
	case status := <-health.statuses:
		if status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Fatalf("health after runtime persistence failure = %s, want NOT_SERVING", status)
		}
	case <-time.After(time.Second):
		t.Fatal("health watcher did not publish runtime readiness failure")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health watcher did not stop with its context")
	}
}
