package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const (
	activeEndpointOperationTimeout = 5 * time.Second
	activeEndpointReconcileEvery   = 5 * time.Second
)

// ExecutionEndpointPublisher owns the selectorless Kubernetes EndpointSlice
// that routes CI and other execution-mutating calls only to the admitted
// execution leader.
type ExecutionEndpointPublisher interface {
	Publish(context.Context, string, uint64) error
	Clear(context.Context, string, uint64) error
}

type ExecutionReadinessResponse struct {
	Status                    string    `json:"status"`
	GeneratedAt               time.Time `json:"generated_at"`
	HAEnabled                 bool      `json:"ha_enabled"`
	ExecutionLeader           bool      `json:"execution_leader"`
	EndpointRoutingConfigured bool      `json:"endpoint_routing_configured"`
	EndpointPublished         bool      `json:"endpoint_published"`
	// Recovering distinguishes a lease holder that has not finished
	// acquire-time recovery (and so refuses execution) from a follower. Error
	// detail stays in the controller log: this endpoint is unauthenticated.
	Recovering             bool       `json:"recovering"`
	RecoveryStartedAt      *time.Time `json:"recovery_started_at,omitempty"`
	RecoveryFailedAttempts int        `json:"recovery_failed_attempts,omitempty"`
	// CapacityReconciliationPending counts fleet pools whose provider sweep
	// after a takeover has not yet succeeded; they refuse new launches.
	CapacityReconciliationPending int    `json:"capacity_reconciliation_pending,omitempty"`
	Message                       string `json:"message"`
}

// handleExecutionReadiness is deliberately separate from the ordinary pod
// readiness endpoint. Followers remain ready for dashboard and read-only API
// traffic, while the active EndpointSlice and operators can verify that only an
// admitted leader is eligible for CI/execution mutations.
func (s *Server) handleExecutionReadiness(w http.ResponseWriter, _ *http.Request) {
	leader := s.isExecutionLeader()
	configured, published := s.activeExecutionEndpointState()
	ready := leader && (!configured || published)
	response := ExecutionReadinessResponse{
		Status:                    readinessStatusNotReady,
		GeneratedAt:               time.Now().UTC(),
		HAEnabled:                 s != nil && s.haExecution,
		ExecutionLeader:           leader,
		EndpointRoutingConfigured: configured,
		EndpointPublished:         published,
		Message:                   "controller is not the admitted execution leader",
	}
	status := http.StatusServiceUnavailable
	if ready {
		response.Status = readinessStatusReady
		response.Message = "controller is ready for execution mutations"
		status = http.StatusOK
	} else if leader && configured {
		response.Message = "execution leader has not published its active routing endpoint"
	}
	if s != nil && s.leadership != nil && s.leadership.IsLeader() {
		if recovering, startedAt, attempts := s.executionRecovery.snapshot(); recovering && !leader {
			response.Recovering = true
			response.RecoveryStartedAt = &startedAt
			response.RecoveryFailedAttempts = attempts
			response.Message = "controller holds the execution lease but has not finished recovery"
		}
	}
	if s != nil {
		response.CapacityReconciliationPending = s.capacitySweep.pendingCount()
	}
	writeJSON(w, status, response)
}

func (s *Server) activeExecutionEndpointState() (configured bool, published bool) {
	if s == nil || s.activeEndpointPublisher == nil {
		return false, false
	}
	s.activeEndpointMu.RLock()
	defer s.activeEndpointMu.RUnlock()
	return true, s.activeEndpointPublished
}

func (s *Server) setActiveExecutionEndpointState(ownerID string, generation uint64, published bool) {
	if s == nil || ownerID == "" || generation == 0 {
		return
	}
	s.activeEndpointMu.Lock()
	if generation < s.activeEndpointGeneration ||
		(generation == s.activeEndpointGeneration && s.activeEndpointOwnerID != "" && s.activeEndpointOwnerID != ownerID) {
		s.activeEndpointMu.Unlock()
		return
	}
	s.activeEndpointPublished = published
	s.activeEndpointOwnerID = ownerID
	s.activeEndpointGeneration = generation
	s.activeEndpointLastAttempt = time.Now().UTC()
	s.activeEndpointMu.Unlock()
}

func (s *Server) activeExecutionEndpointTerm() (string, uint64) {
	if s == nil {
		return "", 0
	}
	s.activeEndpointMu.RLock()
	defer s.activeEndpointMu.RUnlock()
	return s.activeEndpointOwnerID, s.activeEndpointGeneration
}

func (s *Server) publishActiveExecutionEndpoint() {
	s.publishExecutionEndpoint(false)
}

// Recovery needs reverse generators to reach the lease holder before adoption
// can finish. Execution mutations still require admission at their handlers.
func (s *Server) publishExecutionEndpoint(recovering bool) {
	if s == nil || s.activeEndpointPublisher == nil || s.leadership == nil ||
		!s.BoundAddrReady() || !s.leadership.IsLeader() || (!recovering && !s.isExecutionLeader()) {
		return
	}
	ownerID, generation := s.leadership.executionLeaseTerm()
	if ownerID == "" || generation == 0 {
		return
	}
	// Record the term before starting the request. If the Kubernetes API commits
	// the patch but the response is lost, leadership-loss cleanup still knows
	// which exact durable term it is allowed to clear.
	s.setActiveExecutionEndpointState(ownerID, generation, false)
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), activeEndpointOperationTimeout)
	defer timeoutCancel()
	ctx, cancel := s.leadership.operationContext(timeoutCtx)
	defer cancel()
	if err := s.activeEndpointPublisher.Publish(ctx, ownerID, generation); err != nil {
		s.setActiveExecutionEndpointState(ownerID, generation, false)
		slog.Error("failed to publish active controller execution endpoint", "error", err)
		return
	}
	currentOwner, currentGeneration := s.leadership.executionLeaseTerm()
	if ctx.Err() != nil || !s.leadership.IsLeader() || (!recovering && !s.isExecutionLeader()) || currentOwner != ownerID || currentGeneration != generation {
		// The publisher's generation fence prevents this old success from
		// overwriting a successor. Keep local readiness closed as well.
		s.setActiveExecutionEndpointState(ownerID, generation, false)
		return
	}
	s.setActiveExecutionEndpointState(ownerID, generation, true)
}

func (s *Server) clearActiveExecutionEndpoint() {
	if s == nil || s.activeEndpointPublisher == nil {
		return
	}
	ownerID, generation := s.activeExecutionEndpointTerm()
	if ownerID == "" || generation == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), activeEndpointOperationTimeout)
	defer cancel()
	if err := s.activeEndpointPublisher.Clear(ctx, ownerID, generation); err != nil {
		slog.Error("failed to clear stale active controller execution endpoint", "error", err)
	}
	s.setActiveExecutionEndpointState(ownerID, generation, false)
}

func (s *Server) runActiveExecutionEndpointPublisher(ctx context.Context) {
	if s == nil || s.activeEndpointPublisher == nil {
		return
	}
	reconcile := func() {
		if s.isExecutionLeader() {
			s.publishActiveExecutionEndpoint()
			return
		}
		if s.leadership != nil && s.leadership.IsLeader() {
			return // preserve a recovery endpoint until admission or lease loss
		}
		s.clearActiveExecutionEndpoint()
	}
	reconcile()
	ticker := time.NewTicker(activeEndpointReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}
