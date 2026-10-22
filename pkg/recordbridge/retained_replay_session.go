package recordbridge

import (
	"bytes"
	"encoding/json"
	"errors"
)

// ErrRetainedReplayUnavailable means a retained native replay cannot currently
// be reconstructed. It must not be replaced with synthetic evidence.
var ErrRetainedReplayUnavailable = errors.New("retained native replay evidence is unavailable")

// RestoreRetainedReplaySession validates a terminal envelope already authorized
// and verified by its host. It accepts no local file or execution capability.
// The result is detached; it does not register a new replay or managed test.
func RestoreRetainedReplaySession(session *BrowserReplaySession, id, project string) (*BrowserReplaySession, error) {
	if session == nil {
		return nil, ErrRetainedReplayUnavailable
	}
	// Clone the portable envelope without rerunning current evidence truncation
	// policies. Historical verified evidence must not be silently rewritten.
	session.mu.RLock()
	raw, err := json.Marshal(session)
	session.mu.RUnlock()
	if err != nil || len(raw) > 16<<20 {
		return nil, ErrRetainedReplayUnavailable
	}
	clone := new(BrowserReplaySession)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(clone); err != nil {
		return nil, ErrRetainedReplayUnavailable
	}
	if id == "" || project == "" || clone.ID != id || clone.ProjectID != project ||
		!ReplaySessionStatusTerminal(clone.Status) || clone.CompletedAt == nil || clone.CompletedAt.IsZero() ||
		clone.FinalReport == nil || clone.FinalReport.ReplaySessionID != id {
		return nil, ErrRetainedReplayUnavailable
	}
	clone.harCopy = ""
	clone.workerSettled = true
	clone.artifactDurability = "durable"
	return clone, nil
}
