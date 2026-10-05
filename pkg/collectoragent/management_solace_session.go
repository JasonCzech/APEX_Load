package collectoragent

import (
	"context"
	"math"
	"sync"
	"time"
)

// One state per preapproved normalized origin, shared across aliases/revisions.
// Unresolved broker session state is separate from the raw socket close owner.
// The quarantine makes this state survive a fresh observer or process restart.
type solaceManagementOriginState struct {
	mu               sync.Mutex
	quarantine       *solaceManagementQuarantine
	intent           *solaceManagementSessionIntent
	cookie           []byte
	authSent         bool
	sessionUncertain bool
	release          func()
	tokens           float64
	refilled         time.Time
}

func solaceManagementState(poll *managementObservation, origin string) (*solaceManagementOriginState, error) {
	if poll.origins == nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	gate := poll.origins
	gate.mu.Lock()
	defer gate.mu.Unlock()
	approved := gate.origins[managementOriginKey(origin)]
	if approved == nil || approved.solace == nil || approved.solace.quarantine == nil {
		return nil, &managementObservationError{reason: "permission_denied"}
	}
	return approved.solace, nil
}

// Called after durable quarantine load under the existing collector spool lock,
// before any management poll can dispatch. New/Run must never replace a loaded
// store with empty transient state; a missing/corrupt store forbids Basic I/O.
func (g *managementOriginGate) bindSolaceQuarantine(q *solaceManagementQuarantine) error {
	if q == nil {
		return &managementObservationError{reason: "permission_denied"}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, origin := range g.origins {
		if origin.solace != nil && origin.solace.quarantine != q {
			return &managementObservationError{reason: "permission_denied"}
		}
		if origin.solace == nil {
			origin.solace = &solaceManagementOriginState{quarantine: q}
		}
	}
	return nil
}
func (s *solaceManagementOriginState) begin(poll *managementObservation, origin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent != nil || s.quarantine == nil || s.quarantine.blocked(origin) {
		return &managementObservationError{reason: "not_observed"}
	}
	intent := solaceManagementIntent(origin, poll.grant)
	if err := s.quarantine.begin(intent); err != nil {
		return err
	}
	proof, err := poll.retainResource()
	if err != nil {
		// No Basic bytes can have escaped. Durable deletion still must succeed
		// before another actor authenticates; an I/O failure remains quarantined.
		_ = s.quarantine.clearAfterProviderProof(intent)
		return err
	}
	s.intent = &intent
	s.release = proof
	s.authSent = false
	s.sessionUncertain = false
	return nil
}
func (s *solaceManagementOriginState) hasAuthenticationSent() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authSent
}
func (s *solaceManagementOriginState) markSessionUncertain() {
	s.mu.Lock()
	s.sessionUncertain = true
	s.mu.Unlock()
}
func (s *solaceManagementOriginState) canLogout() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.intent != nil && s.authSent && !s.sessionUncertain && s.cookie != nil
}
func (s *solaceManagementOriginState) markAuthenticationSent() {
	s.mu.Lock()
	s.authSent = true
	s.mu.Unlock()
}
func (s *solaceManagementOriginState) observeCookie(cookie []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent == nil || len(cookie) < 9 || len(cookie) > 4096 {
		return &managementObservationError{reason: "invalid"}
	}
	if s.cookie != nil && string(s.cookie) != string(cookie) {
		return &managementObservationError{reason: "identity_mismatch"}
	}
	if s.cookie == nil {
		s.cookie = append([]byte(nil), cookie...)
	}
	return nil
}
func (s *solaceManagementOriginState) cookieCopy() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.cookie...)
}
func (s *solaceManagementOriginState) abandonBeforeAuthentication() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent == nil {
		return nil
	}
	if s.authSent {
		return &managementObservationError{reason: "not_observed"}
	}
	return s.releaseLocked()
}
func (s *solaceManagementOriginState) confirmOwnSessionLogout() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent == nil || !s.authSent || s.sessionUncertain || s.cookie == nil {
		return &managementObservationError{reason: "invalid"}
	}
	return s.releaseLocked()
}
func (s *solaceManagementOriginState) releaseLocked() error {
	if err := s.quarantine.clearAfterProviderProof(*s.intent); err != nil {
		return err
	}
	clear(s.cookie)
	s.cookie = nil
	s.intent = nil
	s.authSent = false
	s.sessionUncertain = false
	if s.release != nil {
		s.release()
		s.release = nil
	}
	return nil
}

// Operator integration must supply audited provider-session release AND drained
// old transport/process ownership, not merely the latter. This method is not
// called by New, expiry, cancellation or ordinary shutdown. No public reset API
// is introduced by this preparatory profile; that workflow remains qualified
// only after its durable identity/audit gates execute.
func (s *solaceManagementOriginState) auditedProviderSessionRelease(intent solaceManagementSessionIntent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.intent != nil && !s.intent.same(intent) {
		return &managementObservationError{reason: "permission_denied"}
	}
	if err := s.quarantine.auditedProviderSessionRelease(intent); err != nil {
		return err
	}
	clear(s.cookie)
	s.cookie = nil
	s.intent = nil
	s.authSent = false
	s.sessionUncertain = false
	if s.release != nil {
		s.release()
		s.release = nil
	}
	return nil
}

// Count only admitted HTTP exchanges, not TCP/TLS setup. Rate waiting occurs
// before request admission, so timeout does not manufacture a sent GET count.
func (s *solaceManagementOriginState) waitHTTP(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		now := time.Now()
		if s.refilled.IsZero() {
			s.refilled = now
			s.tokens = 2
		}
		s.tokens = min(float64(2), s.tokens+max(float64(0), now.Sub(s.refilled).Seconds())*5)
		s.refilled = now
		if s.tokens >= 1 {
			s.tokens--
			s.mu.Unlock()
			return nil
		}
		wait := time.Duration(math.Ceil((1 - s.tokens) / 5 * float64(time.Second)))
		s.mu.Unlock()
		timer := time.NewTimer(max(time.Nanosecond, wait))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
