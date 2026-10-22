package recordbridge

import (
	"context"
	"errors"
	"net/http"
)

func (s *Service) ReplaySessionFor(ctx context.Context, id, project string) (*BrowserReplaySession, error) {
	if s.Replay != nil {
		if session, ok := s.Replay.GetForProject(id, project); ok {
			if session.ArtifactDurability() != "durable" {
				return session, nil
			}
			if s.RetainedReplay == nil {
				return nil, ErrRetainedReplayUnavailable
			}
			return s.RetainedReplay.Session(ctx, id, project)
		}
	}
	if s.RetainedReplay == nil {
		return nil, ErrRetainedReplayNotFound
	}
	return s.RetainedReplay.Session(ctx, id, project)
}
func (s *Service) WriteRetainedReplayError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRetainedReplayNotFound):
		s.writeError(w, http.StatusNotFound, "browser replay session not found")
	case errors.Is(err, ErrRetainedReplayBusy):
		w.Header().Set("Retry-After", "5")
		s.writeError(w, http.StatusServiceUnavailable, "too many replay evidence reads are in progress; retry shortly")
	default:
		s.writeError(w, http.StatusServiceUnavailable, "retained native replay evidence is unavailable; no synthetic replacement was used")
	}
}
func writeReplayDurabilityHeader(w http.ResponseWriter, session *BrowserReplaySession) {
	if durability := session.ArtifactDurability(); durability != "" {
		w.Header().Set("X-APEXLoad-Replay-Durability", durability)
	}
}
