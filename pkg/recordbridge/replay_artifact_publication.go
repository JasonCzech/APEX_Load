package recordbridge

import (
	"context"
	"time"
)

const replayArtifactPublicationTimeout = 2 * time.Minute

// ArtifactDurability reports native replay persistence independently of action
// success. Empty means execution has not reached publication. This is a passive
// capability result; controller HTTP presentation is supplied by its adapter.
func (s *BrowserReplaySession) ArtifactDurability() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.artifactDurability
}

// publishTerminalReplayArtifacts must run after terminal normalization and HAR
// retention. No completion callback or successful execution implies durability.
func (m *BrowserReplayManager) publishTerminalReplayArtifacts(ctx context.Context, session *BrowserReplaySession) bool {
	publisher := session.reservation.artifactPublisher()
	var snapshot *BrowserReplaySession
	current := m.withCurrentSession(session, func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.workerAbandoned {
			return
		}
		session.artifactDurability = "local_only"
		if publisher == nil {
			session.workerRecordedTerminal = true
			session.signalStateChangedLocked()
		}
		if publisher != nil && session.terminalFenceEstablished {
			session.artifactDurability = "pending"
			snapshot = session.snapshotLocked()
		}
	})
	if !current || session.workerWasAbandoned() {
		return false
	}
	// Preserve the existing local-only terminal-fence contract. Only an
	// actual publication introduces another I/O window requiring a new fence.
	if publisher == nil {
		return true
	}
	durability := "local_only"
	if snapshot != nil {
		durability = "unavailable"
		// Do not use ReplayHARFor: its synthetic fallback cannot establish native
		// artifact durability. Opening only the owned copy also keeps paths private.
		if snapshot.harCopy != "" {
			file, err := m.har.open(snapshot.harCopy)
			if err == nil {
				info, statErr := file.Stat()
				if statErr == nil && info.Size() > 0 {
					publishCtx, cancel := context.WithTimeout(ctx, replayArtifactPublicationTimeout)
					err = m.executionFenceError(publishCtx, session.reservation)
					if err == nil {
						err = publisher.Publish(publishCtx, snapshot, file, info.Size())
					}
					if err == nil {
						err = publishCtx.Err()
					}
					cancel()
					if err == nil {
						durability = "durable"
					}
				}
				file.Close()
			}
		}
	}
	// Abandonment/replacement can occur during I/O. Never settle an orphan, and
	// never expose a report after its execution epoch or retained lease was lost.
	fenceErr := m.executionFenceError(ctx, session.reservation)
	settled := false
	current = m.withCurrentSession(session, func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.workerAbandoned {
			return
		}
		if fenceErr != nil {
			session.terminalFenceEstablished = false
			session.FinalReport = nil
			session.Status = BrowserReplayFailed
			session.Error = fenceErr.Error()
			durability = "unavailable"
		}
		session.artifactDurability = durability
		session.workerRecordedTerminal = true
		session.signalStateChangedLocked()
		settled = true
	})
	if current && settled && fenceErr != nil {
		m.broadcastTerminal("browser_replay_failed", session, -1, nil, nil)
		m.retireUnfencedSession(session)
		return false
	}
	return current && settled
}
