package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	maxReplaySessionReservations           = 1024
	maxReplaySessionReservationsPerProject = 100
	defaultReplaySessionLeaseTTL           = 30 * time.Second
	defaultReplaySessionLeaseTimeout       = 5 * time.Second
	classicReplayRequestTimeout            = 5 * time.Minute
	classicReplayErrorWriteGrace           = 5 * time.Second
)

var (
	errReplaySessionIDConflict       = errors.New("replay_session_id is already in use")
	errReplaySessionCapacity         = errors.New("too many replay sessions are in flight")
	errReplaySessionProjectCapacity  = errors.New("project replay session capacity is exhausted")
	errReplaySessionLeaseUnavailable = errors.New("distributed replay session reservation is unavailable")
	errReplaySessionLeaseLost        = errors.New("distributed replay session reservation was lost")
)

type replaySessionReservation struct {
	ownerToken string
	projectID  string
	state      string
	expiresAt  time.Time
}

const (
	replaySessionReservationPending  = "pending"
	replaySessionReservationActive   = "active"
	replaySessionReservationRetained = "retained"
)

type ReplayScriptRequest struct {
	LiveTableSeeds  []engine.LiveTableSandboxSeed `json:"live_table_seeds,omitempty"`
	GroupName       string                        `json:"group_name,omitempty"`
	ProjectID       string                        `json:"project_id,omitempty"`
	ReplaySessionID string                        `json:"replay_session_id,omitempty"`
	StartPaused     bool                          `json:"start_paused,omitempty"`
	Variables       map[string]string             `json:"variables,omitempty"`
	RedactionRules  []engine.RedactionRule        `json:"redaction_rules,omitempty"`
	IgnoreThinkTime *bool                         `json:"ignore_think_time,omitempty"`
	Script          *dsl.Script                   `json:"script,omitempty"`
	// trustedPreservedImport is server-owned provenance. It is set only when
	// replaying the unchanged durable script; JSON cannot opt into it.
	trustedPreservedImport bool
}

func (s *Server) handleReplayDraftScript(w http.ResponseWriter, r *http.Request) {
	var req ReplayScriptRequest
	if r.Body == nil || r.ContentLength == 0 {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}
	if req.Script == nil {
		writeError(w, http.StatusBadRequest, "script is required")
		return
	}
	if err := bindDraftScriptProject(req.Script, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.handleReplayRequestContext(r.Context(), w, "", req.Script, req)
}

func (s *Server) handleReplayScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !requireAuthorizedProjectMatch(w, r, stored.ProjectID) {
		return
	}
	if err := s.runner.checkArchivedExecution("", []string{stored.ID}); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := storedScriptReplayGateError(stored.Script); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req ReplayScriptRequest
	if !decodeOptionalJSONBodyAllowingUnknownFields(w, r, &req) {
		return
	}

	script := stored.Script
	if req.Script != nil {
		script = req.Script
		if script.ID == "" {
			script.ID = stored.ID
		}
		if script.Name == "" {
			script.Name = stored.Name
		}
		var resolveErr error
		script, req.trustedPreservedImport, resolveErr = resolveStoredReplayImportScript(script, stored.Script)
		if resolveErr != nil {
			writeError(w, http.StatusBadRequest, resolveErr.Error())
			return
		}
	} else {
		req.trustedPreservedImport = isCredentialPreservingImport(stored.Script)
	}
	if err := bindStoredScriptProject(script, stored.Script.ProjectID, req.ProjectID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.handleReplayRequestContext(r.Context(), w, id, script, req)
}

func (s *Server) handleReplayRequest(w http.ResponseWriter, id string, script *dsl.Script, req ReplayScriptRequest) {
	s.handleReplayRequestContext(context.Background(), w, id, script, req)
}

func (s *Server) handleReplayRequestContext(requestCtx context.Context, w http.ResponseWriter, id string, script *dsl.Script, req ReplayScriptRequest) {
	// The server-wide WriteTimeout is shorter than a governed classic replay.
	// Extend this route's connection deadline to the replay budget plus enough
	// grace to deliver its timeout/error response.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(classicReplayRequestTimeout + classicReplayErrorWriteGrace))
	if requestCtx == nil {
		requestCtx = context.Background()
	}
	requestCtx, cancelRequest := context.WithTimeout(requestCtx, classicReplayRequestTimeout)
	defer cancelRequest()
	if err := requestCtx.Err(); err != nil {
		writeError(w, http.StatusRequestTimeout, "classic replay request was canceled")
		return
	}
	if !s.requireExecutionLeader(w) {
		return
	}
	if err := validateExternalReplaySessionID(req.ReplaySessionID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var issues, warnings []string
	var prepared bool
	script, issues, warnings, prepared = s.prepareClassicReplayScript(
		w,
		script,
		req.trustedPreservedImport,
	)
	if !prepared {
		return
	}
	if !s.prepareReplayLiveTables(requestCtx, w, script, req.LiveTableSeeds) {
		return
	}
	secretResolver := s.runner.secretResolverForScript(script)
	preflight := buildReplayPreflight(script, req.Variables, issues, warnings, secretResolver)
	if !preflight.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"script_id": id,
			"valid":     preflight.Valid,
			"issues":    issues,
			"warnings":  warnings,
			"preflight": preflight,
		})
		return
	}

	ignoreThinkTime := true
	if req.IgnoreThinkTime != nil {
		ignoreThinkTime = *req.IgnoreThinkTime
	}
	executionCtx, releaseExecutionCtx := s.runner.executionOperationContext(requestCtx)
	defer releaseExecutionCtx()
	replaySessionID, releaseReplaySessionID, leaseSignal, err := s.reserveReplaySessionIDContext(executionCtx, req.ReplaySessionID, script.ProjectID)
	if err != nil {
		if errors.Is(err, errReplaySessionIDConflict) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusServiceUnavailable, err.Error())
		}
		return
	}
	defer releaseReplaySessionID()
	replayCtx, cancelReplay := context.WithCancel(executionCtx)
	defer cancelReplay()
	leaseFailure := watchReplaySessionLease(replayCtx, cancelReplay, leaseSignal)

	if id != "" {
		if err := s.runner.checkArchivedExecution("", []string{id}); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	}
	report, err := s.runner.replayScriptDefinitionContext(replayCtx, script, engine.ReplayOptions{
		ReplaySessionID: replaySessionID,
		LiveTableSeeds:  req.LiveTableSeeds,
		GroupName:       req.GroupName,
		RuntimeProfile:  &engine.ReplayRuntimeProfile{Name: "classic replay", CaptureMode: "diagnostic", LogLevel: "info", IgnoreThinkTime: ignoreThinkTime},
		Variables:       req.Variables,
		RedactionRules:  req.RedactionRules,
		SecretResolver:  secretResolver,
		IgnoreThinkTime: ignoreThinkTime,
		RunID:           debugRunIdentity("", replaySessionID),
		// Classic replays offer no HAR: one the script's own capture policy
		// recorded is retired rather than left in the HAR directory.
		DiscardHAR: retireReplayHARArtifacts,
		OnProgress: func(event engine.ReplayProgressEvent) {
			if replayCtx.Err() != nil {
				return
			}
			if leaseFailure.failureOrExpired(time.Now()) != nil {
				cancelReplay()
				return
			}
			s.broadcastReplayProgress(replaySessionID, id, normalizeProjectID(script.ProjectID), event)
		},
	}, req.trustedPreservedImport)
	if report != nil {
		retireReplayHARArtifacts(report.TakeHARArtifacts())
	}
	if requestErr := requestCtx.Err(); requestErr != nil {
		status := http.StatusRequestTimeout
		message := "classic replay request was canceled"
		if errors.Is(requestErr, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
			message = "classic replay exceeded its request deadline"
		}
		writeError(w, status, message)
		return
	}
	if fenceErr := replayExecutionFenceError(executionCtx, leaseFailure); fenceErr != nil {
		writeError(w, http.StatusServiceUnavailable, fenceErr.Error())
		return
	}
	if errors.Is(err, errRedactionPolicyUnavailable) {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if report != nil {
		s.persistSelfHealToRepository(executionCtx, script.ProjectID, script, report)
		// Repository persistence is an execution-epoch operation too. A lease
		// loss during the write must suppress the successful response, even when
		// the context-aware store correctly rejected the stale mutation.
		if fenceErr := replayExecutionFenceError(executionCtx, leaseFailure); fenceErr != nil {
			writeError(w, http.StatusServiceUnavailable, fenceErr.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"script_id":         id,
		"replay_session_id": replaySessionID,
		"preflight":         preflight,
		"report":            report,
		"warnings":          warnings,
	})
}

// prepareClassicReplayScript applies the same project-scoped object-repository
// resolution boundary as the session replay paths. Keeping this as an explicit
// preparation stage makes it impossible for the synchronous endpoint to hand
// an object_ref-only browser action to a protocol adapter without a concrete
// selector, while unresolved refs still participate in the normal preflight
// response instead of failing later at runtime.
func (s *Server) prepareClassicReplayScript(
	w http.ResponseWriter,
	script *dsl.Script,
	trustedPreservedImport ...bool,
) (*dsl.Script, []string, []string, bool) {
	trusted := len(trustedPreservedImport) > 0 && trustedPreservedImport[0]
	preparedScript, prepared := prepareTransientExecutableScript(
		w,
		script,
		trusted,
	)
	if !prepared {
		return nil, nil, nil, false
	}
	resolvedScript, refWarnings, refIssues := resolveObjectRefs(s.store, preparedScript)
	if resolvedScript != nil {
		preparedScript = resolvedScript
	}
	preparedScript, prepared = prepareTransientExecutableScript(
		w,
		preparedScript,
		trusted,
	)
	if !prepared {
		return nil, nil, nil, false
	}
	issues := append(
		s.transientStaticIssues(script, preparedScript, trusted),
		refIssues...,
	)
	warnings := append(validationWarnings(preparedScript), refWarnings...)
	return preparedScript, issues, warnings, true
}

// replayExecutionFenceError preserves the more specific replay-session lease
// failure when its watcher also canceled the derived worker context. Execution
// leadership is checked second so callers are not told to retry a session ID
// whose integrity reservation was actually lost.
func replayExecutionFenceError(executionCtx context.Context, leaseSignal *replaySessionLeaseSignal) error {
	if leaseErr := leaseSignal.failureOrExpired(time.Now()); leaseErr != nil {
		return leaseErr
	}
	if executionCtx != nil && executionCtx.Err() != nil {
		return errNotExecutionLeader
	}
	return nil
}

func (s *Server) configureReplaySessionReservations() {
	if s == nil {
		return
	}
	s.replaySessionMu.Lock()
	if s.replaySessionReservations == nil {
		s.replaySessionReservations = make(map[string]replaySessionReservation)
	}
	s.replaySessionMu.Unlock()
	if s.browserReplay != nil {
		s.browserReplay.setSessionIDReserver(s.reserveReplaySessionIDContext)
	}
	if s.scriptDebug != nil {
		s.scriptDebug.setSessionIDReserver(s.reserveReplaySessionIDContext)
	}
}

// reserveReplaySessionID claims a session ID in two phases so the process-wide
// replaySessionMu is never held across the HA lease store's network round-trips:
// the ID is first reserved locally as a pending entry under the mutex (which
// keeps two concurrent local callers from claiming the same ID and counts the
// caller against capacity), then the durable lease is acquired with the mutex
// released, and finally the pending entry is activated — or fully removed when
// the durable acquire fails.
func (s *Server) reserveReplaySessionID(requested, projectID string) (string, func(), *replaySessionLeaseSignal, error) {
	return s.reserveReplaySessionIDContext(context.Background(), requested, projectID)
}

func (s *Server) reserveReplaySessionIDContext(executionCtx context.Context, requested, projectID string) (string, func(), *replaySessionLeaseSignal, error) {
	if executionCtx == nil {
		executionCtx = context.Background()
	}
	projectID = normalizeProjectID(projectID)
	generated := requested == ""
	for {
		sessionID, ownerToken, err := s.pendReplaySessionIDLocally(requested, projectID)
		if err != nil {
			return "", nil, nil, err
		}
		claimedID, release, leaseSignal, err := s.activatePendingReplaySessionID(executionCtx, sessionID, ownerToken, projectID)
		if err != nil {
			if generated && errors.Is(err, errReplaySessionIDConflict) {
				// Another controller owns this generated UUID durably; roll a new one.
				continue
			}
			return "", nil, nil, err
		}
		return claimedID, release, leaseSignal, nil
	}
}

// pendReplaySessionIDLocally inserts a pending reservation for the requested
// (or a freshly generated) session ID while holding only the in-memory mutex.
// Pending entries participate in conflict detection and capacity counting
// exactly like active ones, and are removed only by their owner.
func (s *Server) pendReplaySessionIDLocally(requested, projectID string) (string, string, error) {
	ownerToken := uuid.New().String()
	s.replaySessionMu.Lock()
	defer s.replaySessionMu.Unlock()
	if s.replaySessionReservations == nil {
		s.replaySessionReservations = make(map[string]replaySessionReservation)
	}
	s.pruneExpiredReplaySessionReservationsLocked(time.Now())
	if requested != "" {
		if _, exists := s.replaySessionReservations[requested]; exists {
			return "", "", errReplaySessionIDConflict
		}
	}
	if s.replaySessionProjectCountLocked(projectID) >= maxReplaySessionReservationsPerProject {
		return "", "", errReplaySessionProjectCapacity
	}
	if s.replaySessionActiveCountLocked() >= maxReplaySessionReservations {
		return "", "", errReplaySessionCapacity
	}
	sessionID := requested
	if sessionID == "" {
		for {
			sessionID = uuid.New().String()
			if _, exists := s.replaySessionReservations[sessionID]; !exists {
				break
			}
		}
	}
	s.replaySessionReservations[sessionID] = replaySessionReservation{
		ownerToken: ownerToken,
		projectID:  projectID,
		state:      replaySessionReservationPending,
	}
	return sessionID, ownerToken, nil
}

func (s *Server) removePendingReplaySessionID(sessionID, ownerToken string) {
	s.replaySessionMu.Lock()
	reservation, ok := s.replaySessionReservations[sessionID]
	if ok && reservation.ownerToken == ownerToken && reservation.state == replaySessionReservationPending {
		delete(s.replaySessionReservations, sessionID)
	}
	s.replaySessionMu.Unlock()
}

// replaySessionReservationCountsAgainstCapacity reports whether a reservation
// consumes an in-flight slot. Pending entries count so a burst of concurrent
// callers cannot exceed capacity while their durable acquires are in flight.
func replaySessionReservationCountsAgainstCapacity(reservation replaySessionReservation) bool {
	return reservation.state == replaySessionReservationActive || reservation.state == replaySessionReservationPending
}

func (s *Server) replaySessionProjectCountLocked(projectID string) int {
	projectID = normalizeProjectID(projectID)
	count := 0
	for _, reservation := range s.replaySessionReservations {
		if reservation.projectID == projectID && replaySessionReservationCountsAgainstCapacity(reservation) {
			count++
		}
	}
	return count
}

func (s *Server) replaySessionActiveCountLocked() int {
	count := 0
	for _, reservation := range s.replaySessionReservations {
		if replaySessionReservationCountsAgainstCapacity(reservation) {
			count++
		}
	}
	return count
}

func (s *Server) pruneExpiredReplaySessionReservationsLocked(now time.Time) {
	for id, reservation := range s.replaySessionReservations {
		if reservation.state == replaySessionReservationRetained && !reservation.expiresAt.IsZero() && !now.Before(reservation.expiresAt) {
			delete(s.replaySessionReservations, id)
		}
	}
}

// activatePendingReplaySessionID performs the durable HA lease acquisition for
// a locally pended session ID without holding replaySessionMu across the store
// round-trips, then flips the pending entry to active. Every failure path
// removes the pending entry so a failed acquire leaves no local residue.
func (s *Server) activatePendingReplaySessionID(executionCtx context.Context, sessionID, ownerToken, projectID string) (string, func(), *replaySessionLeaseSignal, error) {
	ttl := s.replaySessionLeaseTTL
	if ttl <= 0 {
		ttl = defaultReplaySessionLeaseTTL
	}
	var (
		leaseStore               replaySessionLeaseStore
		leaseSignal              *replaySessionLeaseSignal
		renewStop                context.CancelFunc
		renewDone                chan struct{}
		leadershipCtx            context.Context
		releaseLeadershipContext context.CancelFunc
	)
	if s.haExecution {
		leadershipCtx, releaseLeadershipContext = context.WithCancel(executionCtx)
	}
	fail := func(err error) (string, func(), *replaySessionLeaseSignal, error) {
		if releaseLeadershipContext != nil {
			releaseLeadershipContext()
			releaseLeadershipContext = nil
		}
		s.removePendingReplaySessionID(sessionID, ownerToken)
		return "", nil, nil, err
	}
	leaseStartedAt := time.Now()
	if s.haExecution {
		var ok bool
		leaseStore, ok = s.store.(replaySessionLeaseStore)
		if !ok || leaseStore == nil {
			return fail(errReplaySessionLeaseUnavailable)
		}
		ctx, cancel := context.WithTimeout(leadershipCtx, replaySessionLeaseOperationTimeout(ttl))
		result, err := leaseStore.TryAcquireReplaySessionLease(ctx, sessionID, ownerToken, projectID, ttl, maxReplaySessionReservations, maxReplaySessionReservationsPerProject)
		cancel()
		if err != nil {
			slog.Error("distributed replay session reservation failed", "session_id", sessionID, "project_id", projectID, "error", err)
			return fail(errReplaySessionLeaseUnavailable)
		}
		switch result {
		case storage.ReplaySessionLeaseAcquired:
		case storage.ReplaySessionLeaseConflict:
			return fail(errReplaySessionIDConflict)
		case storage.ReplaySessionLeaseGlobalCapacity:
			return fail(errReplaySessionCapacity)
		case storage.ReplaySessionLeaseProjectCapacity:
			return fail(errReplaySessionProjectCapacity)
		default:
			return fail(errReplaySessionLeaseUnavailable)
		}
		if leadershipCtx.Err() != nil {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
			_, _ = leaseStore.ReleaseReplaySessionLease(releaseCtx, sessionID, ownerToken, projectID)
			releaseCancel()
			return fail(errNotExecutionLeader)
		}
		if !time.Now().Before(leaseStartedAt.Add(ttl)) {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
			_, _ = leaseStore.ReleaseReplaySessionLease(releaseCtx, sessionID, ownerToken, projectID)
			releaseCancel()
			return fail(errReplaySessionLeaseUnavailable)
		}
		leaseSignal = newReplaySessionLeaseSignal()
		leaseSignal.setValidUntil(leaseStartedAt.Add(ttl))
		renewCtx, cancelRenewal := context.WithCancel(leadershipCtx)
		renewStop = cancelRenewal
		renewDone = make(chan struct{})
		go s.renewReplaySessionLease(renewCtx, leadershipCtx, renewDone, leaseSignal, leaseStore, sessionID, ownerToken, projectID, ttl, leaseStartedAt)
	}

	if leaseSignal == nil {
		leaseSignal = newReplaySessionLeaseSignal()
	}
	s.replaySessionMu.Lock()
	reservation, ok := s.replaySessionReservations[sessionID]
	if ok && reservation.ownerToken == ownerToken && reservation.state == replaySessionReservationPending {
		reservation.state = replaySessionReservationActive
		s.replaySessionReservations[sessionID] = reservation
	} else {
		ok = false
	}
	s.replaySessionMu.Unlock()
	if !ok {
		// Defensive: nothing else removes a pending entry, but if it vanished the
		// durable lease must not leak.
		if renewStop != nil {
			renewStop()
			<-renewDone
		}
		if leaseStore != nil {
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
			_, _ = leaseStore.ReleaseReplaySessionLease(releaseCtx, sessionID, ownerToken, projectID)
			releaseCancel()
		}
		if releaseLeadershipContext != nil {
			releaseLeadershipContext()
		}
		return "", nil, nil, errReplaySessionLeaseUnavailable
	}
	var lifecycleMu sync.Mutex
	lifecycleState := replaySessionReservationActive
	stopRenewal := func() {
		if renewStop != nil {
			renewStop()
			<-renewDone
			renewStop = nil
		}
	}
	detachLeadership := func() {
		if releaseLeadershipContext != nil {
			releaseLeadershipContext()
			releaseLeadershipContext = nil
		}
	}
	deleteLocal := func(state string) {
		s.replaySessionMu.Lock()
		reservation, ok := s.replaySessionReservations[sessionID]
		if ok && reservation.ownerToken == ownerToken && reservation.state == state {
			delete(s.replaySessionReservations, sessionID)
		}
		s.replaySessionMu.Unlock()
	}
	release := func() {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if lifecycleState != replaySessionReservationActive {
			return
		}
		stopRenewal()
		if leaseStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
			_, err := leaseStore.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
			cancel()
			if err != nil {
				slog.Error("distributed replay session release failed", "session_id", sessionID, "project_id", projectID, "error", err)
			}
		}
		detachLeadership()
		deleteLocal(replaySessionReservationActive)
		lifecycleState = "released"
	}
	leaseSignal.retainForTerminalFn = func(retentionTTL time.Duration) error {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if lifecycleState == replaySessionReservationRetained {
			return nil
		}
		if lifecycleState != replaySessionReservationActive {
			leaseSignal.fail(errReplaySessionLeaseLost)
			return errReplaySessionLeaseLost
		}
		stopRenewal()
		if leadershipCtx != nil && leadershipCtx.Err() != nil {
			leaseSignal.fail(errReplaySessionLeaseLost)
			if leaseStore != nil {
				ctx, cancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
				_, _ = leaseStore.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
				cancel()
			}
			detachLeadership()
			deleteLocal(replaySessionReservationActive)
			lifecycleState = "released"
			return errReplaySessionLeaseLost
		}
		if err := leaseSignal.failureOrExpired(time.Now()); err != nil {
			if leaseStore != nil {
				ctx, cancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
				_, _ = leaseStore.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
				cancel()
			}
			deleteLocal(replaySessionReservationActive)
			detachLeadership()
			lifecycleState = "released"
			return err
		}
		retentionTTL = replaySessionTerminalTTL(retentionTTL)
		// Anchor the local publication fence before the durable operation. The
		// database writes its expiry later than this instant, so this deadline is
		// conservative even when the controller and database wall clocks differ.
		retentionStartedAt := time.Now()
		if leaseStore != nil {
			ctx, cancel := context.WithTimeout(leadershipCtx, replaySessionLeaseOperationTimeout(ttl))
			retained, err := leaseStore.RetainReplaySessionLease(ctx, sessionID, ownerToken, projectID, retentionTTL)
			cancel()
			if err != nil || !retained || leadershipCtx.Err() != nil {
				leaseSignal.fail(errReplaySessionLeaseLost)
				releaseCtx, releaseCancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
				_, _ = leaseStore.ReleaseReplaySessionLease(releaseCtx, sessionID, ownerToken, projectID)
				releaseCancel()
				deleteLocal(replaySessionReservationActive)
				detachLeadership()
				lifecycleState = "released"
				if err != nil {
					slog.Error("distributed replay session retention failed", "session_id", sessionID, "project_id", projectID, "error", err)
				}
				return errReplaySessionLeaseLost
			}
		}
		expiresAt := retentionStartedAt.Add(retentionTTL)
		s.replaySessionMu.Lock()
		reservation, ok := s.replaySessionReservations[sessionID]
		if ok && reservation.ownerToken == ownerToken && reservation.state == replaySessionReservationActive {
			reservation.state = replaySessionReservationRetained
			reservation.expiresAt = expiresAt
			s.replaySessionReservations[sessionID] = reservation
		}
		s.replaySessionMu.Unlock()
		leaseSignal.setValidUntil(expiresAt)
		detachLeadership()
		lifecycleState = replaySessionReservationRetained
		return nil
	}
	leaseSignal.releaseRetainedIDFn = func() {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if lifecycleState != replaySessionReservationRetained {
			return
		}
		if leaseStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
			_, err := leaseStore.ReleaseRetainedReplaySessionLease(ctx, sessionID, ownerToken, projectID)
			cancel()
			if err != nil {
				slog.Error("distributed replay session tombstone release failed", "session_id", sessionID, "project_id", projectID, "error", err)
			}
		}
		deleteLocal(replaySessionReservationRetained)
		lifecycleState = "released"
	}
	leaseSignal.releaseAllFn = func() {
		lifecycleMu.Lock()
		defer lifecycleMu.Unlock()
		if lifecycleState == "released" {
			return
		}
		stopRenewal()
		state := lifecycleState
		if leaseStore != nil {
			ctx, cancel := context.WithTimeout(context.Background(), replaySessionLeaseOperationTimeout(ttl))
			var err error
			switch state {
			case replaySessionReservationActive:
				_, err = leaseStore.ReleaseReplaySessionLease(ctx, sessionID, ownerToken, projectID)
			case replaySessionReservationRetained:
				_, err = leaseStore.ReleaseRetainedReplaySessionLease(ctx, sessionID, ownerToken, projectID)
			}
			cancel()
			if err != nil {
				slog.Error("distributed replay session shutdown release failed", "session_id", sessionID, "project_id", projectID, "state", state, "error", err)
			}
		}
		deleteLocal(state)
		leaseSignal.clearValidUntil()
		detachLeadership()
		lifecycleState = "released"
	}
	return sessionID, release, leaseSignal, nil
}

func (s *Server) renewReplaySessionLease(
	ctx context.Context,
	leadershipCtx context.Context,
	done chan<- struct{},
	leaseSignal *replaySessionLeaseSignal,
	store replaySessionLeaseStore,
	sessionID, ownerToken, projectID string,
	ttl time.Duration,
	leaseStartedAt time.Time,
) {
	defer close(done)
	validUntil := leaseStartedAt.Add(ttl)
	nextRenewal := leaseStartedAt.Add(ttl / 3)
	for {
		delay := time.Until(nextRenewal)
		if delay < 0 {
			delay = 0
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if leadershipCtx != nil && leadershipCtx.Err() != nil {
				leaseSignal.fail(errReplaySessionLeaseLost)
			}
			return
		case <-timer.C:
		}

		renewalStartedAt := time.Now()
		renewCtx, cancel := context.WithDeadline(ctx, validUntil)
		renewed, err := store.RenewReplaySessionLease(renewCtx, sessionID, ownerToken, projectID, ttl)
		cancel()
		if ctx.Err() != nil {
			if leadershipCtx != nil && leadershipCtx.Err() != nil {
				leaseSignal.fail(errReplaySessionLeaseLost)
			}
			return
		}
		if err == nil && renewed && time.Now().Before(validUntil) {
			// Anchoring to the start of the confirmed renewal request is
			// conservative: the database expiry was written later than this local
			// monotonic deadline, never earlier because of controller clock skew.
			validUntil = renewalStartedAt.Add(ttl)
			leaseSignal.setValidUntil(validUntil)
			nextRenewal = renewalStartedAt.Add(ttl / 3)
			continue
		}
		if err != nil && time.Now().Before(validUntil) {
			// A store error is indistinguishable from a network blip; while the
			// previously confirmed lease still has local validity headroom, retry
			// instead of irreversibly fencing the session. Only a confirmed
			// ownership loss (renewed=false) or exhausted validity is terminal.
			slog.Warn("distributed replay session lease renewal failed; retrying within the validity window",
				"session_id", sessionID, "project_id", projectID, "valid_until", validUntil, "error", err)
			nextRenewal = time.Now().Add(replaySessionLeaseRenewRetryInterval(ttl))
			continue
		}
		if err == nil && !renewed {
			err = errors.New("lease ownership or expiry fence no longer matches")
		} else if err == nil {
			err = errors.New("lease renewal completed after the local validity deadline")
		} else {
			err = fmt.Errorf("lease renewal still failing at the local validity deadline: %w", err)
		}
		slog.Error("distributed replay session lease lost", "session_id", sessionID, "project_id", projectID, "error", err)
		leaseSignal.fail(fmt.Errorf("%w", errReplaySessionLeaseLost))
		return
	}
}

// replaySessionLeaseRenewRetryInterval spaces transient-failure retries so
// several attempts fit inside the remaining validity window (renewal first
// fires at ttl/3, leaving 2*ttl/3 of headroom).
func replaySessionLeaseRenewRetryInterval(ttl time.Duration) time.Duration {
	interval := ttl / 12
	if interval > 2*time.Second {
		interval = 2 * time.Second
	}
	if interval < 25*time.Millisecond {
		interval = 25 * time.Millisecond
	}
	return interval
}

func replaySessionLeaseOperationTimeout(ttl time.Duration) time.Duration {
	timeout := defaultReplaySessionLeaseTimeout
	if half := ttl / 2; half > 0 && half < timeout {
		timeout = half
	}
	if timeout <= 0 {
		return time.Millisecond
	}
	return timeout
}

// prepareTransientExecutableScript applies the same disclosure-safe binding
// boundary used by durable script writes to replay, debugger, and browser
// drafts. Caller-supplied overrides are executable even though they are not
// persisted, so plaintext credentials and executable sentinels must be
// converted through the Credentials flow before any session or progress event
// can observe them.
func prepareTransientExecutableScript(
	w http.ResponseWriter,
	script *dsl.Script,
	trustedPreservedImport ...bool,
) (*dsl.Script, bool) {
	trusted := len(trustedPreservedImport) > 0 && trustedPreservedImport[0]
	if trusted {
		// This path is restricted to a server-owned durable import snapshot:
		// either the unchanged snapshot itself, or an operator edit already
		// reconciled against it by resolveStoredReplayImportScript. Preserve
		// its exact executable values, including JVM argv,
		// including credentials whose literal bytes resemble a disclosure
		// sentinel, while stripping non-executable review material before it
		// enters a runtime session.
		candidate := cloneScriptDefinition(script)
		if candidate == nil {
			writeError(w, http.StatusBadRequest, "failed to prepare imported script for replay")
			return nil, false
		}
		stripImportDraftAttestation(candidate)
		if err := sanitizePersistentImportReviewSurfaces(candidate); err != nil {
			writeError(w, http.StatusBadRequest, "failed to sanitize imported script review evidence")
			return nil, false
		}
		if err := reconcileMaskedScriptSecretsWithTrust(candidate, nil, true); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, false
		}
		return candidate, true
	}
	projected := maskScriptSecrets(script)
	if projected == nil {
		writeError(w, http.StatusBadRequest, "failed to project script for secret binding")
		return nil, false
	}
	if unresolved := discoverSecretBindingRequirements(projected, 0); len(unresolved) > 0 {
		writeSecretBindingsRequired(w, unresolved)
		return nil, false
	}
	return projected, true
}

func (s *Server) broadcastReplayProgress(replaySessionID string, scriptID string, projectID string, event engine.ReplayProgressEvent) {
	message := map[string]any{
		"type":              event.Type,
		"event_id":          event.EventID,
		"replay_session_id": replaySessionID,
		"script_id":         scriptID,
		"project_id":        projectID,
		"timestamp":         event.Timestamp,
	}
	if event.EventIndex >= 0 {
		message["event_index"] = event.EventIndex
	}
	if event.Event != nil {
		message["event"] = event.Event
	}
	if event.Action != nil {
		message["action"] = event.Action
	}
	if event.Report != nil {
		message["report"] = event.Report
	}
	s.hub.BroadcastJSON(message)
}
