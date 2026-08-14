package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
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

func (s *Server) handleGeneratorGatewayConnect(w http.ResponseWriter, r *http.Request) {
	if s.executionTargetStore == nil || s.reverseGenerators == nil {
		writeError(w, http.StatusServiceUnavailable, "reverse generator gateway is not configured")
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
	intentBound, validIntent := s.validateGeneratorCapacityEnrollment(targetID, generatorID, r.Header.Get(generatorGatewayLaunchHeader))
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
	ws, err := generatorGatewayUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	conn := generatorrpc.NewWebSocketConn(ws)
	generation, err := s.reverseGenerators.Register(generatorID, conn)
	if err != nil {
		_ = conn.Close()
		return
	}
	targetAddress := generatorrpc.ReverseTargetPrefix + generatorID
	if poolID != "" {
		if err := s.associateDynamicGenerator(generatorID, targetAddress, poolID, "", "ready"); err != nil {
			s.reverseGenerators.Disconnect(generatorID, generation)
			_ = conn.Close()
			return
		}
	}
	s.addReverseTargetSession(targetID, generatorID)
	if s.runner != nil {
		if backend, ok := s.runner.getExecutionBackend().(*distributedExecutionBackend); ok {
			backend.AddTarget(targetAddress)
		}
	}
	s.broadcastFleetEvent("generator_reverse_connected", map[string]any{"generator_id": generatorID, "execution_target_id": targetID, "target": targetAddress})
	<-conn.Done()
	s.reverseGenerators.Disconnect(generatorID, generation)
	if !s.launchBackground(func() {
		timer := time.NewTimer(reverseGeneratorDisconnectGrace)
		defer timer.Stop()
		<-timer.C
		if s.reverseGenerators.Connected(generatorID) {
			return
		}
		s.removeReverseTargetSession(targetID, generatorID)
		if s.runner != nil {
			if backend, ok := s.runner.getExecutionBackend().(*distributedExecutionBackend); ok {
				backend.RemoveTarget(targetAddress)
			}
		}
		s.broadcastFleetEvent("generator_reverse_disconnected", map[string]any{"generator_id": generatorID, "execution_target_id": targetID, "target": targetAddress})
	}) {
		return
	}
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
	_, valid := s.validateGeneratorCapacityEnrollment(targetID, generatorID, launchNonce)
	return valid
}

// validateGeneratorCapacityEnrollment reports whether the generator identity
// is bound to any durable capacity intent and, separately, whether that intent
// is currently eligible to enroll. Known failed/expired identities remain
// fenced even if a client omits the burst lifecycle header.
func (s *Server) validateGeneratorCapacityEnrollment(targetID, generatorID, launchNonce string) (known, valid bool) {
	store, ok := s.store.(storage.CapacityInstanceIntentStore)
	if !ok {
		return false, false
	}
	intents, err := store.ListCapacityInstanceIntents(targetID, "")
	if err != nil {
		return false, false
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
			return true, true
		}
	}
	return known, false
}
