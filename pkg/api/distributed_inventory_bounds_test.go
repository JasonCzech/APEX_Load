package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSystemInventoryBoundsConcurrentGeneratorProbes(t *testing.T) {
	targets := make([]string, maxGeneratorEpochKeeperWorkers*3)
	for index := range targets {
		targets[index] = fmt.Sprintf("generator-%03d", index)
	}
	entered := make(chan struct{}, len(targets)*2)
	release := make(chan struct{})
	backend := newDistributedExecutionBackend(targets, 5*time.Second, func(ctx context.Context, _ string) (generatorClient, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return nil, errors.New("probe released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	done := make(chan []GeneratorInventoryEntry, 2)
	go func() { done <- backend.SystemInventory(context.Background()) }()
	go func() { done <- backend.SystemInventory(context.Background()) }()
	for range maxGeneratorEpochKeeperWorkers {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("inventory worker pool did not reach its configured concurrency")
		}
	}
	select {
	case <-entered:
		t.Fatalf("concurrent inventory calls started more than %d fleet probes", maxGeneratorEpochKeeperWorkers)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case entries := <-done:
			if len(entries) != len(targets) {
				t.Fatalf("inventory entries = %d, want %d", len(entries), len(targets))
			}
		case <-time.After(2 * time.Second):
			t.Fatal("bounded inventory scans did not finish")
		}
	}
}

func TestSystemInventoryWaiterHonorsCancellation(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, func(ctx context.Context, _ string) (generatorClient, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return nil, errors.New("probe released")
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	firstDone := make(chan struct{})
	go func() {
		backend.SystemInventory(context.Background())
		close(firstDone)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first inventory probe did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	entries := backend.SystemInventory(ctx)
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("canceled inventory waiter took %s", elapsed)
	}
	if len(entries) != 1 || entries[0].Status != "unreachable" {
		t.Fatalf("canceled inventory snapshot = %#v", entries)
	}
	close(release)
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("first inventory probe did not finish")
	}
}

func TestValidateDistributedRunTargetCountRejectsOversizeFanout(t *testing.T) {
	targets := make([]string, maxDistributedTargetsPerRun+1)
	if err := validateDistributedRunTargetCount(targets); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("validateDistributedRunTargetCount error = %v, want limit rejection", err)
	}
	if err := validateDistributedRunTargetCount(targets[:maxDistributedTargetsPerRun]); err != nil {
		t.Fatalf("boundary target count rejected: %v", err)
	}
}
