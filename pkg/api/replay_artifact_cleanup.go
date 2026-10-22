package api

import (
	"context"
	"log/slog"
	"time"
)

func (s *Server) startReplayArtifactCleanup() {
	if s == nil || s.replayArtifacts == nil || s.replayArtifactCleanupCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.replayArtifactCleanupCancel = cancel
	done := make(chan struct{})
	s.replayArtifactCleanupDone = done
	if !s.launchBackground(func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		sweep := func() {
			if s.isExecutionLeader() {
				if err := s.replayArtifacts.Sweep(ctx); err != nil && ctx.Err() == nil {
					slog.Warn("replay artifact cleanup remains pending")
				}
			}
		}
		sweep()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}) {
		cancel()
		close(done)
		s.replayArtifactCleanupCancel = nil
	}
}
