package recordbridge

import "context"

// BackgroundRunning reports whether this manager owns a retention worker.
// Hosts can inspect it while coordinating startup, rollback, and shutdown.
func (m *BrowserReplayManager) BackgroundRunning() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.janitorCancel != nil
}

// BackgroundRunning reports whether this manager owns a retention worker.
func (m *BrowserRecordingManager) BackgroundRunning() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.janitorCancel != nil
}

// Wait waits for the worker belonging to the current session ID to finish.
// It does not cancel the worker and releases all manager locks before waiting.
// The boolean is false if the session was already retired or was never present;
// a replacement using the same ID is never substituted for the captured worker.
func (m *BrowserReplayManager) Wait(ctx context.Context, id string) (bool, error) {
	if m == nil {
		return false, nil
	}
	m.mu.RLock()
	session := m.sessions[id]
	if session == nil {
		m.mu.RUnlock()
		return false, nil
	}
	done := session.workerDone
	m.mu.RUnlock()
	if done == nil {
		return true, nil
	}
	select {
	case <-done:
		return true, nil
	case <-ctx.Done():
		return true, ctx.Err()
	}
}
