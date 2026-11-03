package api

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol/browser"
)

func (s *Server) startReplayArtifactCleanup() {
	if s == nil || s.replayArtifacts == nil || s.replayArtifactCleanupCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
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
			envDir := strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_HAR_DIR"))
			_, _ = browser.ReclaimRuntimeHARWorking(ctx, envDir, 128)
			if s.runner != nil {
				if settingsDir := strings.TrimSpace(s.runner.GetSystemSettings().ArchiveRetention.HARArtifactDir); settingsDir != "" && settingsDir != envDir {
					_, _ = browser.ReclaimRuntimeHARWorking(ctx, settingsDir, 128)
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
		return
	}
	s.replayArtifactCleanupCancel = cancel
	s.replayArtifactCleanupDone = done
}
