package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	generatorGatewayIDHeader        = "X-Apexload-Generator-ID"
	generatorGatewayTargetHeader    = "X-Apexload-Execution-Target-ID"
	generatorGatewayLaunchHeader    = "X-Apexload-Launch-Nonce"
	generatorGatewayLifecycleHeader = "X-Apexload-Lifecycle"
	generatorGatewaySessionHeader   = "X-Apexload-Session-Nonce"
	generatorGatewayTimestampHeader = "X-Apexload-Session-Timestamp"
	reverseGatewayTrustForwardedEnv = "APEXLOAD_REVERSE_GATEWAY_TRUST_FORWARDED_HTTPS"
	reverseGeneratorDisconnectGrace = 30 * time.Second
	reverseEnrollmentClockSkew      = 2 * time.Minute
	reverseEnrollmentNonceTTL       = 5 * time.Minute
	// A whole burst fleet legitimately dials from one NAT address at once, so
	// this budget is sized for scale-out rather than for a login form. The
	// worst realistic burst is every generator in a pool reconnecting together
	// after a controller restart, so the default sits well above any supported
	// pool size: throttling a recovering fleet would turn a restart into an
	// outage. Enrollment tokens carry 32 bytes of entropy, so the budget exists
	// to bound connection churn and database load, not to stop guessing.
	//
	// Keyed per peer AND per execution target so one noisy target cannot exhaust
	// the budget another target's generators depend on.
	defaultReverseGatewayRatePerMinute = 600
	reverseGatewayRateLimitEnvVar      = "APEXLOAD_REVERSE_GATEWAY_RATE_LIMIT_PER_MINUTE"
)

type GeneratorGatewayConnectResponse struct {
	GeneratorID       string `json:"generator_id"`
	ExecutionTargetID string `json:"execution_target_id"`
	Transport         string `json:"transport"`
}

var generatorGatewayUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	CheckOrigin: func(r *http.Request) bool {
		// Generators are non-browser clients. Refusing browser Origin headers
		// removes ambient-cookie/CSRF ambiguity from this public, token-auth route.
		return strings.TrimSpace(r.Header.Get("Origin")) == ""
	},
}

// configuredReverseGatewayRatePerMinute lets an operator tune the budget to
// their largest fleet. Zero disables the limit.
func configuredReverseGatewayRatePerMinute() int {
	raw := strings.TrimSpace(os.Getenv(reverseGatewayRateLimitEnvVar))
	if raw == "" {
		return defaultReverseGatewayRatePerMinute
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return defaultReverseGatewayRatePerMinute
	}
	return limit
}

// allowReverseGatewayAttempt bounds enrollment attempts per source address and
// execution target. The target ID is hashed into the key rather than used raw:
// it is attacker-supplied, and an unbounded value would let a caller mint
// arbitrarily many distinct rate-limit keys.
func (s *Server) allowReverseGatewayAttempt(w http.ResponseWriter, r *http.Request, targetID string) bool {
	if s == nil || s.cache == nil {
		return true
	}
	peer := "unknown"
	if ip := requestSourceIP(r); ip != nil {
		peer = ip.String()
	}
	keySum := sha256.Sum256([]byte(targetID))
	key := "generator-gateway:" + peer + ":" + hex.EncodeToString(keySum[:8])
	limit := configuredReverseGatewayRatePerMinute()
	if limit <= 0 {
		return true
	}
	allowed, remaining, err := s.cache.AllowRateLimit(r.Context(), key, limit, time.Minute)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "generator gateway rate limit is unavailable")
		return false
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	if !allowed {
		writeError(w, http.StatusTooManyRequests, "generator gateway rate limit exceeded")
		return false
	}
	return true
}

// OpenAPI-Responses: 429
func (s *Server) handleGeneratorGatewayConnect(w http.ResponseWriter, r *http.Request) {
	if s.executionTargetStore == nil || s.reverseGenerators == nil {
		writeError(w, http.StatusServiceUnavailable, "reverse generator gateway is not configured")
		return
	}
	// Only the execution leader drives generators. A follower that accepted the
	// tunnel would strand the generator where the leader cannot reach it. The
	// 503 is deliberately not a 4xx: the reverse connector treats 4xx as a
	// permanent rejection, and this one clears once routing reaches the leader.
	// Adoption needs this transport before public mutation admission opens.
	if !s.runner.holdsExecutionLeadership() {
		writeError(w, http.StatusServiceUnavailable, errNotExecutionLeader.Error())
		return
	}
	if productionMode() && !reverseGatewayRequestSecure(r, s.startupAuthSettings) {
		writeError(w, http.StatusUpgradeRequired, "reverse generator connections require HTTPS in production")
		return
	}
	generatorID := strings.TrimSpace(r.Header.Get(generatorGatewayIDHeader))
	targetID := strings.TrimSpace(r.Header.Get(generatorGatewayTargetHeader))
	if generatorID == "" || targetID == "" {
		writeError(w, http.StatusBadRequest, "generator and execution-target identities are required")
		return
	}
	if err := storage.ValidateReverseGeneratorIdentity(generatorID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Rate-limit before the store lookup so unauthenticated churn cannot drive
	// database load or WebSocket upgrades, matching every other pre-auth
	// credential-accepting route (local login, OIDC login, CI exchange).
	if !s.allowReverseGatewayAttempt(w, r, targetID) {
		return
	}
	target, err := s.executionTargetStore.GetExecutionTarget(targetID)
	if err != nil || target == nil || target.Status != "active" || target.ConnectivityMode != storage.ConnectivityReverse {
		writeError(w, http.StatusUnauthorized, "generator enrollment was rejected")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || !storage.VerifyExecutionTargetEnrollmentToken(target, token) {
		writeError(w, http.StatusUnauthorized, "generator enrollment was rejected")
		return
	}
	if !s.consumeReverseEnrollmentNonce(targetID, r.Header.Get(generatorGatewaySessionHeader), r.Header.Get(generatorGatewayTimestampHeader), time.Now().UTC()) {
		writeError(w, http.StatusUnauthorized, "generator enrollment was rejected")
		return
	}
	intentBound, validIntent, err := s.validateGeneratorCapacityEnrollment(targetID, generatorID, r.Header.Get(generatorGatewayLaunchHeader))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "generator enrollment authority is unavailable")
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get(generatorGatewayLifecycleHeader)), "burst") || intentBound {
		if !validIntent {
			writeError(w, http.StatusUnauthorized, "burst generator launch identity was rejected")
			return
		}
	}
	poolID, err := s.dynamicPoolForExecutionTarget(targetID)
	if err != nil {
		writeError(w, http.StatusConflict, "generator enrollment target has ambiguous fleet-pool ownership")
		return
	}
	owners, ok := s.store.(storage.ReverseGeneratorIdentityStore)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "generator enrollment authority is unavailable")
		return
	}
	owned, err := owners.ClaimReverseGeneratorIdentity(targetID, generatorID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "generator enrollment authority is unavailable")
		return
	}
	if !owned {
		writeError(w, http.StatusUnauthorized, "generator enrollment was rejected")
		return
	}
	ws, err := generatorGatewayUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := generatorrpc.NewWebSocketConn(ws)
	targetAddress := generatorrpc.ReverseTargetPrefix + generatorID
	if poolID != "" {
		if err := s.associateDynamicGenerator(generatorID, targetAddress, poolID, "", "ready"); err != nil {
			_ = conn.Close()
			return
		}
	}
	// Finish durable admission before replacing a previous live transport.
	generation, err := s.registerReverseGenerator(targetID, generatorID, conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	<-conn.Done()
	s.reverseGenerators.Disconnect(generatorID, generation)
	if !s.launchBackground(func() {
		timer := time.NewTimer(reverseGeneratorDisconnectGrace)
		defer timer.Stop()
		<-timer.C
		if s.reverseGenerators.Connected(generatorID) {
			return
		}
		s.removeDisconnectedReverseGenerator(targetID, generatorID)
	}) {
		return
	}
}

// registerReverseGenerator publishes a transport and its fleet membership as
// one lifecycle transition. Admission and durable writes happen before this
// lock; waiting for the connection to close happens after it is released.
func (s *Server) registerReverseGenerator(targetID, generatorID string, conn net.Conn) (uint64, error) {
	s.reverseLifecycleMu.Lock()
	defer s.reverseLifecycleMu.Unlock()
	generation, err := s.reverseGenerators.Register(generatorID, conn)
	if err != nil {
		return 0, err
	}
	s.addReverseTargetSession(targetID, generatorID)
	targetAddress := generatorrpc.ReverseTargetPrefix + generatorID
	if s.runner != nil {
		if backend, ok := s.runner.getExecutionBackend().(*distributedExecutionBackend); ok {
			backend.AddTarget(targetAddress)
		}
	}
	s.broadcastFleetEvent("generator_reverse_connected", map[string]any{"generator_id": generatorID, "execution_target_id": targetID, "target": targetAddress})
	return generation, nil
}

func (s *Server) removeDisconnectedReverseGenerator(targetID, generatorID string) bool {
	s.reverseLifecycleMu.Lock()
	defer s.reverseLifecycleMu.Unlock()
	// The grace timer's earlier observation can already be stale. Recheck
	// while excluding publication of a newly enrolled replacement session.
	if s.reverseGenerators.Connected(generatorID) {
		return false
	}
	s.removeReverseTargetSession(targetID, generatorID)
	targetAddress := generatorrpc.ReverseTargetPrefix + generatorID
	if s.runner != nil {
		if backend, ok := s.runner.getExecutionBackend().(*distributedExecutionBackend); ok {
			backend.RemoveTarget(targetAddress)
		}
	}
	s.broadcastFleetEvent("generator_reverse_disconnected", map[string]any{"generator_id": generatorID, "execution_target_id": targetID, "target": targetAddress})
	return true
}

func reverseGatewayRequestSecure(r *http.Request, settings AuthSettings) bool {
	if localAuthCookieSecure(r, settings) {
		return true
	}
	// Managed PaaS ingress terminates TLS before forwarding plain HTTP to the
	// container. Trust its scheme header only under an explicit deployment
	// assertion; direct installs retain the trusted-proxy gate above.
	return envBool(reverseGatewayTrustForwardedEnv) && githubForwardedProto(r.Header.Get("X-Forwarded-Proto")) == "https"
}

func (s *Server) consumeReverseEnrollmentNonce(targetID, nonce, timestamp string, now time.Time) bool {
	if s == nil {
		return false
	}
	targetID, nonce, timestamp = strings.TrimSpace(targetID), strings.TrimSpace(nonce), strings.TrimSpace(timestamp)
	if targetID == "" || len(nonce) < 32 || len(nonce) > 128 {
		return false
	}
	unixSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	sentAt := time.Unix(unixSeconds, 0).UTC()
	if sentAt.Before(now.Add(-reverseEnrollmentClockSkew)) || sentAt.After(now.Add(reverseEnrollmentClockSkew)) {
		return false
	}
	key := targetID + "\x00" + nonce
	s.reverseEnrollmentMu.Lock()
	defer s.reverseEnrollmentMu.Unlock()
	if s.reverseEnrollmentNonces == nil {
		s.reverseEnrollmentNonces = make(map[string]time.Time)
	}
	for candidate, expiresAt := range s.reverseEnrollmentNonces {
		if !now.Before(expiresAt) {
			delete(s.reverseEnrollmentNonces, candidate)
		}
	}
	if _, replayed := s.reverseEnrollmentNonces[key]; replayed {
		return false
	}
	s.reverseEnrollmentNonces[key] = now.Add(reverseEnrollmentNonceTTL)
	return true
}

func (s *Server) dynamicPoolForExecutionTarget(targetID string) (string, error) {
	if s == nil || s.executionTargetStore == nil {
		return "", nil
	}
	pools, err := s.executionTargetStore.ListFleetPoolsForExecutionTarget(strings.TrimSpace(targetID))
	if err != nil {
		return "", err
	}
	if len(pools) == 0 {
		return "", nil
	}
	if len(pools) > 1 {
		return "", fmt.Errorf("execution target %q is bound to %d fleet pools", targetID, len(pools))
	}
	return strings.TrimSpace(pools[0].ID), nil
}

func (s *Server) addReverseTargetSession(targetID, generatorID string) {
	s.reverseTargetMu.Lock()
	defer s.reverseTargetMu.Unlock()
	if s.reverseTargetSessions == nil {
		s.reverseTargetSessions = make(map[string]map[string]struct{})
	}
	sessions := s.reverseTargetSessions[targetID]
	if sessions == nil {
		sessions = make(map[string]struct{})
		s.reverseTargetSessions[targetID] = sessions
	}
	sessions[generatorID] = struct{}{}
}

func (s *Server) removeReverseTargetSession(targetID, generatorID string) {
	s.reverseTargetMu.Lock()
	defer s.reverseTargetMu.Unlock()
	sessions := s.reverseTargetSessions[targetID]
	delete(sessions, generatorID)
	if len(sessions) == 0 {
		delete(s.reverseTargetSessions, targetID)
	}
}

func (s *Server) reverseTargetSessionIDs(targetID string) []string {
	s.reverseTargetMu.RLock()
	defer s.reverseTargetMu.RUnlock()
	sessions := s.reverseTargetSessions[targetID]
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *Server) validateBurstGeneratorEnrollment(targetID, generatorID, launchNonce string) bool {
	_, valid, err := s.validateGeneratorCapacityEnrollment(targetID, generatorID, launchNonce)
	return err == nil && valid
}

// validateGeneratorCapacityEnrollment reports whether the generator identity
// is bound to any durable capacity intent and, separately, whether that intent
// is currently eligible to enroll. Known failed/expired identities remain
// fenced even if a client omits the burst lifecycle header.
func (s *Server) validateGeneratorCapacityEnrollment(targetID, generatorID, launchNonce string) (known, valid bool, err error) {
	store, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok {
		return false, false, fmt.Errorf("capacity intent storage is unavailable")
	}
	intents, err := store.ListCapacityInstanceIntents(targetID, "")
	if err != nil {
		return false, false, err
	}
	for _, intent := range intents {
		if intent == nil || subtle.ConstantTimeCompare([]byte(intent.GeneratorID), []byte(generatorID)) != 1 {
			continue
		}
		known = true
		if intent.State != storage.CapacityIntentPlanned && intent.State != storage.CapacityIntentLaunching && intent.State != storage.CapacityIntentReady {
			continue
		}
		if intent.CleanupDeadline != nil && !time.Now().UTC().Before(*intent.CleanupDeadline) {
			continue
		}
		if strings.TrimSpace(launchNonce) != "" && subtle.ConstantTimeCompare([]byte(intent.LaunchNonce), []byte(strings.TrimSpace(launchNonce))) == 1 {
			return true, true, nil
		}
	}
	return known, false, nil
}
