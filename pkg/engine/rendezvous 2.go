package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

type RendezvousCoordinator struct {
	mu       sync.Mutex
	barriers map[string]*rendezvousBarrier
}

type rendezvousBarrier struct {
	generation int
	waiting    int
	releaseCh  chan struct{}
}

func NewRendezvousCoordinator() *RendezvousCoordinator {
	return &RendezvousCoordinator{barriers: make(map[string]*rendezvousBarrier)}
}

func (rc *RendezvousCoordinator) Wait(ctx context.Context, key string, participants int, timeout time.Duration) error {
	if rc == nil || participants <= 1 {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "default"
	}

	rc.mu.Lock()
	barrier := rc.barriers[key]
	if barrier == nil {
		barrier = &rendezvousBarrier{releaseCh: make(chan struct{})}
		rc.barriers[key] = barrier
	}
	generation := barrier.generation
	releaseCh := barrier.releaseCh
	barrier.waiting++
	if barrier.waiting >= participants {
		close(barrier.releaseCh)
		barrier.generation++
		barrier.waiting = 0
		barrier.releaseCh = make(chan struct{})
		rc.mu.Unlock()
		return nil
	}
	rc.mu.Unlock()

	waitCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	select {
	case <-releaseCh:
		return nil
	case <-waitCtx.Done():
		rc.mu.Lock()
		barrier = rc.barriers[key]
		if barrier == nil || barrier.generation != generation {
			rc.mu.Unlock()
			return nil
		}
		if barrier.waiting > 0 {
			barrier.waiting--
		}
		if barrier.waiting == 0 {
			delete(rc.barriers, key)
		}
		rc.mu.Unlock()
		return fmt.Errorf("rendezvous %q wait failed: %w", key, waitCtx.Err())
	}
}
