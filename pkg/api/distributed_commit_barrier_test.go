package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

type delayedCoordinatedCommitClient struct {
	raceFakeGeneratorClient
	delay   time.Duration
	started atomic.Bool
}

func (c *delayedCoordinatedCommitClient) CommitWorkAt(ctx context.Context, _ string, _ uint64, startAt time.Time) (generatorrpc.WorkCommitResult, error) {
	if c.delay > 0 {
		timer := time.NewTimer(c.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return generatorrpc.WorkCommitResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	go func() {
		delay := time.Until(startAt)
		if delay > 0 {
			time.Sleep(delay)
		}
		c.started.Store(true)
	}()
	return generatorrpc.WorkCommitResult{Accepted: true}, nil
}

func TestCommitBarrierOutlivesEntireCommitAckWindow(t *testing.T) {
	timeout := 400 * time.Millisecond
	lead := distributedCommitStartLead(timeout)
	if lead <= distributedCommitAckTimeout(timeout) {
		t.Fatalf("commit lead %s must exceed ACK window %s", lead, distributedCommitAckTimeout(timeout))
	}
	fast := &delayedCoordinatedCommitClient{}
	slow := &delayedCoordinatedCommitClient{delay: 300 * time.Millisecond}
	backend := newDistributedExecutionBackend([]string{"fast", "slow"}, timeout, nil)
	startAt := time.Now().Add(lead)
	err := backend.commitDistributedWork(context.Background(), "test", 1, startAt, map[string]generatorClient{
		"fast": fast,
		"slow": slow,
	})
	if err != nil {
		t.Fatalf("commitDistributedWork: %v", err)
	}
	if fast.started.Load() || slow.started.Load() {
		t.Fatal("a generator started before the slow commit ACK completed")
	}
	deadline := time.Now().Add(lead + time.Second)
	for (!fast.started.Load() || !slow.started.Load()) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !fast.started.Load() || !slow.started.Load() {
		t.Fatalf("coordinated generators did not start: fast=%v slow=%v", fast.started.Load(), slow.started.Load())
	}
}
