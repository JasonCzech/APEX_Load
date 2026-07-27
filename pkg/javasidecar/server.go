package javasidecar

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
	"github.com/jasonczech/apexload/pkg/javarunner"
)

const (
	harnessIPCContract   = "apexload.java_vuser.ipc.v2"
	harnessEventContract = "apexload.java_vuser.lrapi_events.v2"
	harnessSessionPrefix = "APEXLOAD_JAVA_VUSER_V2 "
	harnessValidationV1  = "java_vuser_validation_only_v1"
	harnessClassloaderV1 = "java_vuser_dedicated_classloader_v1"
	defaultHarnessMain   = "Harness"
	defaultProbeTimeout  = 5 * time.Second
	readinessSuccessTTL  = 30 * time.Second
	readinessFailureTTL  = 2 * time.Second
	processWaitDelay     = 2 * time.Second
	maxPathBytes         = 4096
	maxTargetBytes       = 4096
)

var executionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type ServerOptions struct {
	Operator        config.JavaVuserOperatorConfig
	ProbeTimeout    time.Duration
	CredentialCheck func() error

	// These unexported hooks let package tests exercise persistent lifecycle
	// semantics on hosts such as Darwin that deliberately fail production
	// containment closed.
	supportsStrongProcessContainment func() bool
	startStrongProcessTree           func(*exec.Cmd) error
	resolveJDK                       func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error)
}

// Server owns the operator configuration and the process-wide concurrency
// semaphore. It is safe for concurrent use by net/http.
type Server struct {
	operator        config.JavaVuserOperatorConfig
	authToken       string
	probeTimeout    time.Duration
	credentialCheck func() error
	slots           chan struct{}
	probeSlots      chan struct{}

	supportsStrongProcessContainment func() bool
	startStrongProcessTree           func(*exec.Cmd) error
	resolveJDK                       func(context.Context, javarunner.ResolveOptions) (javarunner.ResolvedJDK, error)

	runningMu sync.Mutex
	running   map[string]runningExecution
	runningWG sync.WaitGroup
	draining  atomic.Bool
	// containmentFailed is terminal for this sidecar process. A capacity token
	// is deliberately left occupied when whole-tree termination cannot be
	// proven, and no later request may recycle another token into a new JVM.
	containmentFailed     atomic.Bool
	containmentFailedOnce sync.Once
	containmentFailedDone chan struct{}

	operationMu      sync.Mutex
	operationWG      sync.WaitGroup
	operationSeq     uint64
	operationCancels map[uint64]context.CancelFunc

	failedStartReaps sync.WaitGroup

	claimMu sync.Mutex
	ownerMu sync.Mutex
	owner   *ownerGeneration

	sessionsMu      sync.Mutex
	sessions        map[string]*persistentSession
	pendingSessions map[string]*pendingPersistentSession
	// A poisoned JVM is removed immediately so it cannot retain process/session
	// resources, but its ID remains as a bounded tombstone until the controller
	// explicitly closes it. This prevents a retry from silently constructing a
	// fresh Actions object with lost state.
	unusableSessions map[string]unusableSession
	allSessions      map[*persistentSession]*ownerGeneration

	harnessHealthMu sync.Mutex
	harnessHealthy  map[string]time.Time

	readinessMu sync.Mutex
	readiness   *readinessCacheEntry

	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
}

func NewServer(options ServerOptions) *Server {
	limit := options.Operator.MaxProcesses
	if limit <= 0 {
		limit = config.DefaultJavaVuserMaxProcesses
	}
	probeTimeout := options.ProbeTimeout
	if probeTimeout <= 0 {
		probeTimeout = defaultProbeTimeout
	}
	credentialCheck := options.CredentialCheck
	if credentialCheck == nil {
		credentialCheck = ValidateCredentialIsolation
	}
	supportsStrongProcessContainment := options.supportsStrongProcessContainment
	if supportsStrongProcessContainment == nil {
		supportsStrongProcessContainment = javarunner.SupportsStrongProcessContainment
	}
	startStrongProcessTree := options.startStrongProcessTree
	if startStrongProcessTree == nil {
		startStrongProcessTree = javarunner.StartStrongProcessTree
	}
	resolveJDK := options.resolveJDK
	if resolveJDK == nil {
		resolveJDK = javarunner.ResolveJDK
	}
	return &Server{
		operator:                         options.Operator,
		authToken:                        strings.TrimSpace(options.Operator.AuthToken),
		probeTimeout:                     probeTimeout,
		credentialCheck:                  credentialCheck,
		slots:                            make(chan struct{}, limit),
		probeSlots:                       make(chan struct{}, 1),
		supportsStrongProcessContainment: supportsStrongProcessContainment,
		startStrongProcessTree:           startStrongProcessTree,
		resolveJDK:                       resolveJDK,
		running:                          make(map[string]runningExecution),
		operationCancels:                 make(map[uint64]context.CancelFunc),
		sessions:                         make(map[string]*persistentSession),
		pendingSessions:                  make(map[string]*pendingPersistentSession),
		unusableSessions:                 make(map[string]unusableSession),
		allSessions:                      make(map[*persistentSession]*ownerGeneration),
		harnessHealthy:                   make(map[string]time.Time),
		shutdownDone:                     make(chan struct{}),
		containmentFailedDone:            make(chan struct{}),
	}
}

type readinessCacheEntry struct {
	ready     chan struct{}
	jdk       javarunner.ResolvedJDK
	failure   *safeFailure
	expiresAt time.Time
}

type ownerGeneration struct {
	ownerID string
	epoch   string
	ready   bool

	// Add is performed only while Server.ownerMu proves this generation is
	// current. A takeover swaps the generation under that same lock before
	// waiting, so Wait can never race a later Add.
	operations sync.WaitGroup
}

type runningExecution struct {
	cancel     context.CancelFunc
	generation *ownerGeneration
}

type unusableSession struct {
	generation *ownerGeneration
	poisonedAt time.Time
}

// Shutdown cancels active executions and closes every persistent JVM. Session
// slots remain owned until each cmd.Wait completes.
// shutdownContainmentReapTimeout bounds how long finishShutdown waits for
// tracked containment reaps. Legitimate reaps complete in well under a second;
// the bound only matters in the terminal ErrProcessContainmentFailed state,
// where a direct root never returns from Wait.
const shutdownContainmentReapTimeout = 30 * time.Second

func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.startShutdown()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-s.shutdownDone:
		// A deadline may race shared cleanup completion. Honor the caller's
		// budget even though the server-owned cleanup itself is already done.
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) startShutdown() {
	s.shutdownOnce.Do(func() {
		// Flip the process-operation admission gate before starting the shared
		// worker. Every Shutdown caller thereafter waits on the same completion
		// channel and never creates its own drain or cleanup goroutines.
		s.operationMu.Lock()
		s.draining.Store(true)
		operationCancels := make([]context.CancelFunc, 0, len(s.operationCancels))
		for _, cancel := range s.operationCancels {
			operationCancels = append(operationCancels, cancel)
		}
		s.operationMu.Unlock()
		for _, cancel := range operationCancels {
			cancel()
		}
		go s.finishShutdown()
	})
}

func (s *Server) finishShutdown() {
	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	s.ownerMu.Lock()
	generation := s.owner
	s.ownerMu.Unlock()
	s.runningMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.running))
	for _, execution := range s.running {
		cancels = append(cancels, execution.cancel)
	}
	s.runningMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	s.operationWG.Wait()
	s.runningWG.Wait()
	if generation != nil {
		generation.operations.Wait()
	}
	s.shutdownErr = s.closeTrackedSessions(context.Background())
	// A strong-start containment failure can return before the direct root is
	// reaped so the action remains bounded. No process operation can register a
	// new failed-start reaper after operationWG above reached zero.
	s.failedStartReaps.Wait()
	// This also covers process-tree failures raised by shared JDK/containment
	// probes before a persistent session object existed. Bound the wait: a
	// terminal ErrProcessContainmentFailed root never returns from Wait, and an
	// unbounded wait here would leak this goroutine and never close
	// shutdownDone for an embedded, long-lived Server host.
	reapCtx, cancel := context.WithTimeout(context.Background(), shutdownContainmentReapTimeout)
	_ = javarunner.WaitForProcessContainmentReaps(reapCtx)
	cancel()
	close(s.shutdownDone)
}

func (s *Server) closeTrackedSessions(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.sessionsMu.Lock()
	sessions := make([]*persistentSession, 0, len(s.allSessions))
	for session := range s.allSessions {
		sessions = append(sessions, session)
	}
	s.sessionsMu.Unlock()
	var failures []error
	for _, session := range sessions {
		if failure := session.close(ctx); failure != nil {
			failures = append(failures, fmt.Errorf("close Java Vuser session (%s)", failure.code))
		}
		// close may return after its graceful-stop budget once it has initiated
		// process-tree termination. The server-owned shutdown worker must still
		// retain this object until cmd.Wait, output drain, capacity disposition,
		// and capture cleanup are all complete.
		<-session.waitDone
		session.cleanup()
		s.evictSessionObject(session)
	}
	return errors.Join(failures...)
}

type pendingPersistentSession struct {
	configKey  string
	generation *ownerGeneration
	ready      chan struct{}
	session    *persistentSession
	failure    *safeFailure
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/livez" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, http.StatusOK, HealthResponse{Contract: ContractVersion, Status: "alive"})
		return
	}
	if r.URL.Path == "/readyz" || r.URL.Path == "/v1/health" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		switch {
		case !s.operator.Enabled:
			writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
				Contract: ContractVersion, Status: "unhealthy", ErrorCode: "runner_disabled",
			})
		case s.authToken == "":
			writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
				Contract: ContractVersion, Status: "unhealthy", ErrorCode: "runner_auth_unavailable",
			})
		default:
			s.handleHealth(w, r)
		}
		return
	}
	if !s.operator.Enabled {
		writeError(w, http.StatusServiceUnavailable, "runner_disabled")
		return
	}
	if s.authToken == "" {
		writeError(w, http.StatusServiceUnavailable, "runner_auth_unavailable")
		return
	}
	providedToken := strings.TrimSpace(r.Header.Get("X-Apexload-Java-Runner-Token"))
	if subtle.ConstantTimeCompare([]byte(providedToken), []byte(s.authToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "runner_unauthorized")
		return
	}
	switch {
	case r.URL.Path == "/v1/capabilities":
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		s.handleCapabilities(w, r)
	case r.URL.Path == "/v1/validate":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleValidate(w, r)
	case r.URL.Path == "/v1/owner/claim":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleClaimOwner(w, r)
	case r.URL.Path == "/v1/executions":
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleExecution(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/executions/") && strings.HasSuffix(r.URL.Path, "/cancel"):
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleCancel(w, r)
	case strings.HasPrefix(r.URL.Path, "/v1/sessions/") && strings.HasSuffix(r.URL.Path, "/close"):
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}
		s.handleCloseSession(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s.draining.Load() {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
			Contract: ContractVersion, Status: "unhealthy", ErrorCode: "server_draining",
		})
		return
	}
	operationCtx, _, finishOperation, operationError := s.beginProcessOperation(r.Context())
	if operationError != "" {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
			Contract: ContractVersion, Status: "unhealthy", ErrorCode: operationError,
		})
		return
	}
	defer finishOperation()
	_, failure := s.health(operationCtx)
	if failure != nil {
		writeJSON(w, http.StatusServiceUnavailable, HealthResponse{
			Contract: ContractVersion, Status: "unhealthy", ErrorCode: failure.code,
		})
		return
	}
	writeJSON(w, http.StatusOK, HealthResponse{Contract: ContractVersion, Status: "healthy"})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	operationCtx, _, finishOperation, operationError := s.beginProcessOperation(r.Context())
	if operationError != "" {
		writeError(w, http.StatusServiceUnavailable, operationError)
		return
	}
	defer finishOperation()
	jdk, failure := s.health(operationCtx)
	strongContainment := s.supportsStrongProcessContainmentAvailable(operationCtx)
	if s.containmentUnavailable() {
		failure = &safeFailure{code: "process_containment_unavailable"}
	}
	response := CapabilitiesResponse{
		Contract: ContractVersion,
		Features: capabilityFeatures(strongContainment),
		// Advertise the floor this sidecar actually enforces. Reporting the
		// unconditional constant while accepting a legacy JVM would make the
		// wire contract disagree with behavior on both sides of it.
		MinimumJavaMajor:      javarunner.EffectiveMinimumJavaMajor(s.operator.AllowLegacyJava),
		MaxProcesses:          cap(s.slots),
		MaxExecutionTimeoutMS: MaximumExecutionTimeout.Milliseconds(),
		MaxCaptureBytes:       MaxCaptureBytes,
	}
	status := http.StatusOK
	if failure != nil {
		response.ErrorCode = failure.code
		status = http.StatusServiceUnavailable
	} else {
		response.Healthy = true
		response.JavaMajor = jdk.Major
	}
	writeJSON(w, status, response)
}

func capabilityFeatures(strongProcessContainment bool) []string {
	features := []string{
		FeatureExactVMParameters,
		FeatureJDKSelectionV1,
		FeatureDedicatedClassloader,
		FeatureValidationOnly,
		FeatureOwnerFencingV1,
	}
	if strongProcessContainment {
		features = append(features, FeaturePhaseLifecycleV1, FeaturePersistentLifecycleV1, FeatureSessionFrameTokenV1)
	}
	return features
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var request ValidateRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	response := ValidateResponse{Contract: ContractVersion}
	timeout, failure := boundedTimeout(request.TimeoutMS, DefaultExecutionTimeout)
	if failure != nil {
		response.Issues = []ValidationIssue{{Code: failure.code}}
		writeJSON(w, http.StatusOK, response)
		return
	}
	operationCtx, _, finishOperation, operationError := s.beginProcessOperation(r.Context())
	if operationError != "" {
		writeError(w, http.StatusServiceUnavailable, operationError)
		return
	}
	defer finishOperation()
	ctx, cancel := context.WithTimeout(operationCtx, timeout)
	defer cancel()
	prepared, failure := s.prepareRuntime(ctx, request.Runtime)
	if failure != nil {
		response.Issues = []ValidationIssue{failure.issue()}
		writeJSON(w, http.StatusOK, response)
		return
	}
	response.JavaMajor = prepared.jdk.Major
	response.WarningCodes = append(response.WarningCodes, prepared.warningCodes...)
	if failure := s.verifyHarness(ctx, prepared); failure != nil {
		response.Issues = []ValidationIssue{{Code: failure.code}}
		writeJSON(w, http.StatusOK, response)
		return
	}
	for index, target := range request.Targets {
		if failure := validateTarget(target); failure != nil {
			failure.index = intPointer(index)
			response.Issues = append(response.Issues, failure.issue())
			continue
		}
		command := ActionCommand{ClassName: target.ClassName, Method: target.Method}
		outcome := s.runHarness(ctx, prepared, command, "validate-"+strconv.Itoa(index), true)
		if !outcome.success {
			response.Issues = append(response.Issues, ValidationIssue{
				Code: "target_validation_failed", Index: intPointer(index),
			})
		}
	}
	response.Valid = len(response.Issues) == 0
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleExecution(w http.ResponseWriter, r *http.Request) {
	var request ExecutionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	if !executionIDPattern.MatchString(request.ExecutionID) {
		writeError(w, http.StatusBadRequest, "execution_id_invalid")
		return
	}
	if request.SessionID != "" && !executionIDPattern.MatchString(request.SessionID) {
		writeError(w, http.StatusBadRequest, "session_id_invalid")
		return
	}
	if failure := validateTarget(Target{ClassName: request.Command.ClassName, Method: request.Command.Method}); failure != nil {
		writeError(w, http.StatusUnprocessableEntity, failure.code)
		return
	}
	if request.Command.ActionType != "" && request.Command.ActionType != "java_vuser_run" {
		writeError(w, http.StatusUnprocessableEntity, "action_type_invalid")
		return
	}
	timeout, failure := boundedTimeout(request.TimeoutMS, DefaultExecutionTimeout)
	if failure != nil {
		writeError(w, http.StatusUnprocessableEntity, failure.code)
		return
	}
	operationCtx, operationCancel, finishOperation, operationError := s.beginProcessOperation(r.Context())
	if operationError != "" {
		writeError(w, http.StatusServiceUnavailable, operationError)
		return
	}
	defer finishOperation()
	runCtx, cancel := context.WithTimeout(operationCtx, timeout)
	defer cancel()
	generation, registrationError := s.registerExecution(
		request.ExecutionID,
		operationCancel,
		request.OwnerID,
		request.OwnerEpoch,
	)
	if registrationError != "" {
		writeError(w, ownerErrorStatus(registrationError), registrationError)
		return
	}
	defer func() {
		s.unregisterExecution(request.ExecutionID, generation)
	}()
	if request.SessionID != "" {
		s.handlePersistentExecution(w, runCtx, request, generation)
		return
	}
	prepared, failure := s.prepareRuntime(runCtx, request.Runtime)
	if failure != nil {
		writeError(w, http.StatusUnprocessableEntity, failure.code)
		return
	}
	if failure := s.verifyHarness(runCtx, prepared); failure != nil {
		if runCtx.Err() != nil {
			writeJSON(w, http.StatusOK, ExecutionResponse{
				Contract: ContractVersion, ExecutionID: request.ExecutionID,
				ErrorCode: contextErrorCode(runCtx),
			})
			return
		}
		writeError(w, http.StatusUnprocessableEntity, failure.code)
		return
	}
	started := time.Now()
	outcome := s.runHarness(runCtx, prepared, request.Command, request.ExecutionID, false)
	response := ExecutionResponse{
		Contract:        ContractVersion,
		ExecutionID:     request.ExecutionID,
		Success:         outcome.success,
		ErrorCode:       outcome.errorCode,
		Error:           outcome.errorMessage,
		Stdout:          outcome.stdout,
		Stderr:          outcome.stderr,
		Events:          outcome.events,
		StdoutTruncated: outcome.stdoutTruncated,
		StderrTruncated: outcome.stderrTruncated,
		EventsTruncated: outcome.eventsTruncated,
		DurationMS:      time.Since(started).Milliseconds(),
		WarningCodes:    append([]string(nil), prepared.warningCodes...),
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleClaimOwner(w http.ResponseWriter, r *http.Request) {
	var request ClaimOwnerRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	request.OwnerID = strings.TrimSpace(request.OwnerID)
	if !executionIDPattern.MatchString(request.OwnerID) {
		writeError(w, http.StatusBadRequest, "owner_id_invalid")
		return
	}
	if s.draining.Load() {
		writeError(w, http.StatusServiceUnavailable, "server_draining")
		return
	}
	if s.containmentUnavailable() {
		writeError(w, http.StatusServiceUnavailable, "process_containment_unavailable")
		return
	}

	s.claimMu.Lock()
	defer s.claimMu.Unlock()
	if s.draining.Load() {
		writeError(w, http.StatusServiceUnavailable, "server_draining")
		return
	}
	if s.containmentUnavailable() {
		writeError(w, http.StatusServiceUnavailable, "process_containment_unavailable")
		return
	}

	s.ownerMu.Lock()
	if current := s.owner; current != nil && current.ready && current.ownerID == request.OwnerID {
		epoch := current.epoch
		s.ownerMu.Unlock()
		writeJSON(w, http.StatusOK, ClaimOwnerResponse{
			Contract: ContractVersion, OwnerID: request.OwnerID, Epoch: epoch, Claimed: true,
		})
		return
	}
	previous := s.owner
	next := &ownerGeneration{
		ownerID: request.OwnerID,
		epoch:   newOwnerEpoch(),
	}
	s.owner = next
	s.ownerMu.Unlock()

	// Cleanup deliberately ignores the request context. A disconnected or
	// timed-out claimant must not strand the old generator's JVMs and slots.
	if err := s.reapGeneration(previous); err != nil {
		writeError(w, http.StatusServiceUnavailable, "owner_claim_failed")
		return
	}
	s.ownerMu.Lock()
	if s.owner != next {
		s.ownerMu.Unlock()
		writeError(w, http.StatusConflict, "owner_fenced")
		return
	}
	next.ready = true
	s.ownerMu.Unlock()
	writeJSON(w, http.StatusOK, ClaimOwnerResponse{
		Contract: ContractVersion, OwnerID: request.OwnerID, Epoch: next.epoch, Claimed: true,
	})
}

func (s *Server) reapGeneration(generation *ownerGeneration) error {
	if generation == nil {
		return nil
	}
	if s.containmentUnavailable() {
		return fmt.Errorf("Java Vuser process containment is unavailable")
	}
	s.runningMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.running))
	for _, execution := range s.running {
		if execution.generation == generation {
			cancels = append(cancels, execution.cancel)
		}
	}
	s.runningMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	generation.operations.Wait()

	s.sessionsMu.Lock()
	sessions := make([]*persistentSession, 0, len(s.allSessions))
	for session, owner := range s.allSessions {
		if owner == generation {
			sessions = append(sessions, session)
		}
	}
	s.sessionsMu.Unlock()
	for _, session := range sessions {
		failure := session.close(context.Background())
		// close() waits for cmd.Wait in the no-deadline cleanup context. The
		// capacity slot has therefore been released before a new owner is ready.
		// A failed strong kill is the exception: it closes the terminal
		// containment gate while cmd.Wait may never complete. Reject takeover
		// instead of stranding this owner-claim handler forever.
		if failure != nil && s.containmentUnavailable() {
			return fmt.Errorf("Java Vuser process containment is unavailable")
		}
		select {
		case <-session.waitDone:
		case <-s.containmentFailedDone:
			return fmt.Errorf("Java Vuser process containment is unavailable")
		}
		s.evictSessionObject(session)
	}
	s.sessionsMu.Lock()
	for id, session := range s.sessions {
		if session.generation == generation {
			delete(s.sessions, id)
		}
	}
	for id, pending := range s.pendingSessions {
		if pending.generation == generation {
			delete(s.pendingSessions, id)
		}
	}
	for id, unusable := range s.unusableSessions {
		if unusable.generation == generation {
			delete(s.unusableSessions, id)
		}
	}
	s.sessionsMu.Unlock()
	return nil
}

func newOwnerEpoch() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("epoch-%d", time.Now().UnixNano())
}

func (s *Server) handlePersistentExecution(
	w http.ResponseWriter,
	ctx context.Context,
	request ExecutionRequest,
	generation *ownerGeneration,
) {
	started := time.Now()
	if s.sessionUnusable(request.SessionID, generation) {
		writeJSON(w, http.StatusOK, ExecutionResponse{
			Contract: ContractVersion, ExecutionID: request.ExecutionID,
			ErrorCode: "session_unusable", DurationMS: time.Since(started).Milliseconds(),
		})
		return
	}
	session, existing := s.session(request.SessionID, generation)
	if existing {
		if session.configKey != persistentSessionConfigKey(request.Runtime, request.Command) {
			writeError(w, http.StatusConflict, "session_configuration_mismatch")
			return
		}
	} else {
		if request.RequireExistingSession {
			writeJSON(w, http.StatusOK, ExecutionResponse{
				Contract: ContractVersion, ExecutionID: request.ExecutionID,
				ErrorCode: "session_unusable", DurationMS: time.Since(started).Milliseconds(),
			})
			return
		}
		prepared, failure := s.prepareRuntime(ctx, request.Runtime)
		if failure != nil {
			writeError(w, http.StatusUnprocessableEntity, failure.code)
			return
		}
		if failure := s.verifyHarness(ctx, prepared); failure != nil {
			if ctx.Err() != nil {
				writeJSON(w, http.StatusOK, ExecutionResponse{Contract: ContractVersion, ExecutionID: request.ExecutionID, ErrorCode: contextErrorCode(ctx)})
				return
			}
			writeError(w, http.StatusUnprocessableEntity, failure.code)
			return
		}
		session, failure = s.createPersistentSession(ctx, request.SessionID, prepared, request, generation)
		if failure != nil {
			writeJSON(w, http.StatusOK, ExecutionResponse{
				Contract: ContractVersion, ExecutionID: request.ExecutionID, ErrorCode: failure.code,
				WarningCodes: append([]string(nil), prepared.warningCodes...), DurationMS: time.Since(started).Milliseconds(),
			})
			return
		}
	}
	outcome := session.invoke(ctx, request)
	if persistentOutcomePoisonsSession(outcome.errorCode) {
		s.poisonSession(request.SessionID, session)
	}
	writeJSON(w, http.StatusOK, ExecutionResponse{
		Contract: ContractVersion, ExecutionID: request.ExecutionID,
		Success: outcome.success, ErrorCode: outcome.errorCode, Error: outcome.errorMessage,
		Stdout: outcome.stdout, Stderr: outcome.stderr, Events: outcome.events,
		StdoutTruncated: outcome.stdoutTruncated, StderrTruncated: outcome.stderrTruncated,
		EventsTruncated: outcome.eventsTruncated, DurationMS: time.Since(started).Milliseconds(),
		WarningCodes: append([]string(nil), session.warningCodes...),
	})
}

func persistentOutcomePoisonsSession(code string) bool {
	switch strings.TrimSpace(code) {
	case "session_unusable", "contract_version_mismatch", "execution_timeout", "execution_canceled":
		return true
	default:
		return false
	}
}

func (s *Server) evictSession(id string, session *persistentSession) {
	s.sessionsMu.Lock()
	if current := s.sessions[id]; current == session {
		delete(s.sessions, id)
	}
	s.sessionsMu.Unlock()
}

func (s *Server) evictSessionObject(session *persistentSession) {
	s.sessionsMu.Lock()
	for id, current := range s.sessions {
		if current == session {
			delete(s.sessions, id)
		}
	}
	delete(s.allSessions, session)
	s.sessionsMu.Unlock()
}

func (s *Server) registerPersistentSession(session *persistentSession, generation *ownerGeneration) {
	s.sessionsMu.Lock()
	s.allSessions[session] = generation
	s.sessionsMu.Unlock()
}

const (
	unusableSessionTTL  = 15 * time.Minute
	maxUnusableSessions = 4096
)

func (s *Server) poisonSession(id string, session *persistentSession) {
	now := time.Now()
	s.sessionsMu.Lock()
	if current := s.sessions[id]; current != session {
		s.sessionsMu.Unlock()
		return
	}
	delete(s.sessions, id)
	// Ordinarily failAndReap has synchronously reaped the JVM. If the strong
	// containment primitive itself failed, however, the action returns after a
	// bounded wait while this object must remain visible to generation cleanup
	// and Shutdown until cmd.Wait eventually completes.
	reaped := false
	select {
	case <-session.waitDone:
		reaped = true
		delete(s.allSessions, session)
	default:
	}
	s.pruneUnusableSessionsLocked(now)
	if len(s.unusableSessions) >= maxUnusableSessions {
		oldestID := ""
		var oldest time.Time
		for candidate, unusable := range s.unusableSessions {
			if oldestID == "" || unusable.poisonedAt.Before(oldest) {
				oldestID, oldest = candidate, unusable.poisonedAt
			}
		}
		delete(s.unusableSessions, oldestID)
	}
	s.unusableSessions[id] = unusableSession{
		generation: session.generation,
		poisonedAt: now,
	}
	s.sessionsMu.Unlock()
	if reaped {
		session.cleanup()
		return
	}
	go func() {
		<-session.waitDone
		session.cleanup()
		s.evictSessionObject(session)
	}()
}

func (s *Server) sessionUnusable(id string, generation *ownerGeneration) bool {
	now := time.Now()
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.pruneUnusableSessionsLocked(now)
	unusable, exists := s.unusableSessions[id]
	return exists && unusable.generation == generation
}

func (s *Server) pruneUnusableSessionsLocked(now time.Time) {
	for id, unusable := range s.unusableSessions {
		if now.Sub(unusable.poisonedAt) >= unusableSessionTTL {
			delete(s.unusableSessions, id)
		}
	}
}

func (s *Server) session(id string, generation *ownerGeneration) (*persistentSession, bool) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	session, ok := s.sessions[id]
	return session, ok && session.generation == generation
}

func (s *Server) createPersistentSession(
	ctx context.Context,
	id string,
	prepared preparedRuntime,
	request ExecutionRequest,
	generation *ownerGeneration,
) (*persistentSession, *safeFailure) {
	configKey := persistentSessionConfigKey(request.Runtime, request.Command)
	for {
		s.ownerMu.Lock()
		if s.draining.Load() || s.owner != generation || !generation.ready {
			s.ownerMu.Unlock()
			return nil, &safeFailure{code: "owner_fenced"}
		}
		s.sessionsMu.Lock()
		if unusable, exists := s.unusableSessions[id]; exists && unusable.generation == generation {
			s.sessionsMu.Unlock()
			s.ownerMu.Unlock()
			return nil, &safeFailure{code: "session_unusable"}
		}
		if existing, ok := s.sessions[id]; ok {
			s.sessionsMu.Unlock()
			s.ownerMu.Unlock()
			if existing.generation != generation {
				return nil, &safeFailure{code: "owner_fenced"}
			}
			if existing.configKey != configKey {
				return nil, &safeFailure{code: "session_configuration_mismatch"}
			}
			return existing, nil
		}
		if pending, ok := s.pendingSessions[id]; ok {
			if pending.generation != generation {
				s.sessionsMu.Unlock()
				s.ownerMu.Unlock()
				return nil, &safeFailure{code: "owner_fenced"}
			}
			if pending.configKey != configKey {
				s.sessionsMu.Unlock()
				s.ownerMu.Unlock()
				return nil, &safeFailure{code: "session_configuration_mismatch"}
			}
			ready := pending.ready
			s.sessionsMu.Unlock()
			s.ownerMu.Unlock()
			select {
			case <-ready:
				if pending.failure != nil {
					return nil, pending.failure
				}
				if pending.session != nil {
					return pending.session, nil
				}
				return nil, &safeFailure{code: "session_unusable"}
			case <-ctx.Done():
				return nil, &safeFailure{code: contextErrorCode(ctx)}
			}
		}
		pending := &pendingPersistentSession{
			configKey: configKey, generation: generation, ready: make(chan struct{}),
		}
		s.pendingSessions[id] = pending
		s.sessionsMu.Unlock()
		s.ownerMu.Unlock()

		session, failure := s.startPersistentSession(ctx, prepared, request, generation)
		s.ownerMu.Lock()
		stillCurrent := !s.draining.Load() && s.owner == generation && generation.ready
		if failure == nil && session != nil && !stillCurrent {
			// Do not publish a JVM created by a fenced owner. A background claim
			// is waiting on this operation, so reap synchronously before return.
			_ = session.close(context.Background())
			session = nil
			failure = &safeFailure{code: "owner_fenced"}
		}
		s.sessionsMu.Lock()
		if current := s.pendingSessions[id]; current == pending {
			delete(s.pendingSessions, id)
		}
		pending.session = session
		pending.failure = failure
		if stillCurrent && failure == nil && session != nil {
			s.sessions[id] = session
		}
		close(pending.ready)
		s.sessionsMu.Unlock()
		s.ownerMu.Unlock()
		return session, failure
	}
}

func (s *Server) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	var request CloseSessionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	generation, ownerError := s.beginOwnerOperation(request.OwnerID, request.OwnerEpoch)
	if ownerError != "" {
		writeError(w, ownerErrorStatus(ownerError), ownerError)
		return
	}
	defer generation.operations.Done()
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/close")
	if !executionIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "session_id_invalid")
		return
	}
	var session *persistentSession
	exists := false
	unusable := false
	for {
		s.sessionsMu.Lock()
		session, exists = s.sessions[id]
		if exists && session.generation != generation {
			exists, session = false, nil
		}
		unusableEntry, hasUnusable := s.unusableSessions[id]
		unusable = hasUnusable && unusableEntry.generation == generation
		pending := s.pendingSessions[id]
		if pending != nil && pending.generation != generation {
			pending = nil
		}
		s.sessionsMu.Unlock()
		if pending == nil {
			break
		}
		select {
		case <-pending.ready:
			continue
		case <-r.Context().Done():
			writeError(w, http.StatusRequestTimeout, contextErrorCode(r.Context()))
			return
		}
	}
	if exists {
		failure := session.close(r.Context())
		// close() either observed process reaping or initiated kill-and-reap
		// cleanup. The server must relinquish the exact poisoned object even
		// when that cleanup reports a failure; retaining it makes abandoned
		// client IDs an unbounded sessions-map leak.
		s.evictSession(id, session)
		if failure != nil {
			// A canceled close can return while strong process-tree reaping is
			// still completing. Keep the object in allSessions until that
			// barrier closes so owner takeover and Shutdown can still wait for
			// it; retire it asynchronously only after cleanup is safe.
			go func() {
				<-session.waitDone
				session.cleanup()
				s.evictSessionObject(session)
			}()
			writeError(w, http.StatusInternalServerError, failure.code)
			return
		}
		s.evictSessionObject(session)
	}
	s.sessionsMu.Lock()
	if unusableEntry, ok := s.unusableSessions[id]; ok && unusableEntry.generation == generation {
		delete(s.unusableSessions, id)
	}
	s.sessionsMu.Unlock()
	writeJSON(w, http.StatusOK, CloseSessionResponse{
		Contract:  ContractVersion,
		SessionID: id,
		Closed:    exists || unusable,
	})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	var request CancelRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/executions/"), "/cancel")
	if !executionIDPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "execution_id_invalid")
		return
	}
	canceled, ownerError := s.cancelExecution(id, request.OwnerID, request.OwnerEpoch)
	if ownerError != "" {
		writeError(w, ownerErrorStatus(ownerError), ownerError)
		return
	}
	writeJSON(w, http.StatusOK, CancelResponse{Contract: ContractVersion, ExecutionID: id, Canceled: canceled})
}

// beginProcessOperation atomically gates every handler that can resolve a JDK
// or start a JVM. Shutdown flips draining and snapshots cancellations under the
// same mutex, so Wait never races a later WaitGroup.Add.
func (s *Server) beginProcessOperation(
	parent context.Context,
) (context.Context, context.CancelFunc, func(), string) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	s.operationMu.Lock()
	if s.draining.Load() {
		s.operationMu.Unlock()
		cancel()
		return nil, nil, func() {}, "server_draining"
	}
	if s.containmentUnavailable() {
		s.operationMu.Unlock()
		cancel()
		return nil, nil, func() {}, "process_containment_unavailable"
	}
	s.operationSeq++
	id := s.operationSeq
	s.operationWG.Add(1)
	s.operationCancels[id] = cancel
	s.operationMu.Unlock()

	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			s.operationMu.Lock()
			delete(s.operationCancels, id)
			s.operationMu.Unlock()
			s.operationWG.Done()
		})
	}
	return ctx, cancel, finish, ""
}

func (s *Server) registerExecution(
	id string,
	cancel context.CancelFunc,
	ownerID string,
	ownerEpoch string,
) (*ownerGeneration, string) {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	generation, ownerError := s.currentOwnerLocked(ownerID, ownerEpoch)
	if ownerError != "" {
		return nil, ownerError
	}
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.draining.Load() {
		return nil, "server_draining"
	}
	if _, exists := s.running[id]; exists {
		return nil, "execution_id_conflict"
	}
	generation.operations.Add(1)
	s.runningWG.Add(1)
	s.running[id] = runningExecution{cancel: cancel, generation: generation}
	return generation, ""
}

func (s *Server) unregisterExecution(id string, generation *ownerGeneration) {
	s.runningMu.Lock()
	execution, exists := s.running[id]
	if exists && execution.generation == generation {
		delete(s.running, id)
	}
	s.runningMu.Unlock()
	if exists && execution.generation == generation {
		generation.operations.Done()
		s.runningWG.Done()
	}
}

func (s *Server) cancelExecution(id, ownerID, ownerEpoch string) (bool, string) {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	generation, ownerError := s.currentOwnerLocked(ownerID, ownerEpoch)
	if ownerError != "" {
		return false, ownerError
	}
	s.runningMu.Lock()
	execution, exists := s.running[id]
	s.runningMu.Unlock()
	if exists && execution.generation == generation {
		execution.cancel()
		return true, ""
	}
	return false, ""
}

func (s *Server) beginOwnerOperation(ownerID, ownerEpoch string) (*ownerGeneration, string) {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	generation, ownerError := s.currentOwnerLocked(ownerID, ownerEpoch)
	if ownerError != "" {
		return nil, ownerError
	}
	if s.draining.Load() {
		return nil, "server_draining"
	}
	generation.operations.Add(1)
	return generation, ""
}

func (s *Server) currentOwnerLocked(ownerID, ownerEpoch string) (*ownerGeneration, string) {
	generation := s.owner
	if generation == nil {
		return nil, "owner_not_claimed"
	}
	if !generation.ready {
		return nil, "owner_claim_in_progress"
	}
	if strings.TrimSpace(ownerID) != generation.ownerID ||
		strings.TrimSpace(ownerEpoch) != generation.epoch {
		return nil, "owner_fenced"
	}
	return generation, ""
}

func ownerErrorStatus(code string) int {
	switch code {
	case "server_draining", "owner_claim_in_progress":
		return http.StatusServiceUnavailable
	default:
		return http.StatusConflict
	}
}

type preparedRuntime struct {
	jdk              javarunner.ResolvedJDK
	harnessJar       string
	scriptClasspath  []string
	sharedClasspath  []string
	vmParameters     []string
	dedicatedLoader  bool
	startupTimeoutMS int64
	workDir          string
	warningCodes     []string
}

type safeFailure struct {
	code  string
	index *int
}

func (f *safeFailure) issue() ValidationIssue {
	return ValidationIssue{Code: f.code, Index: f.index}
}

func (s *Server) prepareRuntime(ctx context.Context, settings RuntimeSettings) (preparedRuntime, *safeFailure) {
	if err := s.credentialCheck(); err != nil {
		return preparedRuntime{}, &safeFailure{code: "ambient_credentials_detected"}
	}
	if issues := javarunner.ValidateVMParameters(settings.AdditionalVMParameters); len(issues) > 0 {
		issue := issues[0]
		failure := &safeFailure{code: "vm_parameter_" + issue.Category}
		if issue.Index >= 0 {
			failure.index = intPointer(issue.Index)
		}
		return preparedRuntime{}, failure
	}
	if settings.StartupTimeoutMS < 0 || settings.StartupTimeoutMS > MaximumExecutionTimeout.Milliseconds() {
		return preparedRuntime{}, &safeFailure{code: "startup_timeout_out_of_range"}
	}
	selection := strings.TrimSpace(settings.JDKSelection)
	if selection == "" {
		selection = string(javarunner.JDKSelectionInternal)
	}
	if selection != string(javarunner.JDKSelectionInternal) && selection != string(javarunner.JDKSelectionSpecified) {
		return preparedRuntime{}, &safeFailure{code: "jdk_selection_invalid"}
	}
	if selection == string(javarunner.JDKSelectionSpecified) && strings.TrimSpace(settings.JDKHome) == "" {
		return preparedRuntime{}, &safeFailure{code: "jdk_home_required"}
	}
	if len(settings.JDKHome) > maxPathBytes || strings.IndexFunc(settings.JDKHome, unicode.IsControl) >= 0 {
		return preparedRuntime{}, &safeFailure{code: "jdk_home_invalid"}
	}
	if s.operator.RunnerCommandExplicit && !javarunner.IsDirectJavaCommand(s.operator.RunnerCommand) {
		return preparedRuntime{}, &safeFailure{code: "legacy_runner_wrapper_unsupported"}
	}
	release, failure := s.acquireProbeSlot(ctx)
	if failure != nil {
		return preparedRuntime{}, failure
	}
	releasePending := true
	defer func() {
		if releasePending {
			release()
		}
	}()
	explicitCommand := ""
	if s.operator.RunnerCommandExplicit {
		explicitCommand = s.operator.RunnerCommand
	}
	jdk, err := s.resolveJDK(ctx, javarunner.ResolveOptions{
		Selection:           javarunner.JDKSelection(selection),
		SpecifiedHome:       settings.JDKHome,
		ExplicitJavaCommand: explicitCommand,
		DefaultHome:         s.operator.DefaultJDKHome,
		// The sidecar reads its own operator environment, so the legacy opt-in
		// has to be honored here as well. Resolving without it would fail the
		// sidecar's own health probe on a legacy JVM, and the caller would then
		// see zero capacity rather than a version error naming this check.
		AllowLegacyJava: s.operator.AllowLegacyJava,
		ProbeTimeout:    s.probeTimeout,
	})
	if err != nil {
		if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
			if reapDone := javarunner.FailedStartReapDone(err); reapDone != nil {
				s.trackFailedStartReapDone(reapDone, nil)
			}
			// JDK discovery uses the dedicated probe semaphore rather than a
			// Vuser capacity slot. Keep this exact probe admission occupied and
			// close the terminal gate so no request can launch a second
			// potentially uncontained tree.
			s.finishProcessCapacity(release, err)
			releasePending = false
			return preparedRuntime{}, &safeFailure{code: "process_containment_unavailable"}
		}
		return preparedRuntime{}, &safeFailure{code: "jdk_unavailable"}
	}
	workDir, failure := resolveWorkDir(s.operator.WorkDir)
	if failure != nil {
		return preparedRuntime{}, failure
	}
	harnessJar, failure := resolveRequiredFile(s.operator.HarnessJar, workDir, "harness_unavailable")
	if failure != nil {
		return preparedRuntime{}, failure
	}
	materializedClasspath, failure := s.materializeClasspathAssets(settings.Classpath, settings.ClasspathAssets, workDir)
	if failure != nil {
		return preparedRuntime{}, failure
	}
	scriptClasspath, failure := resolveClasspath(materializedClasspath, workDir)
	if failure != nil {
		return preparedRuntime{}, failure
	}
	sharedClasspath, failure := resolveClasspath(s.operator.Classpath, workDir)
	if failure != nil {
		return preparedRuntime{}, &safeFailure{code: "shared_classpath_unavailable"}
	}
	if settings.DedicatedClassLoader && len(scriptClasspath)+len(sharedClasspath) == 0 {
		return preparedRuntime{}, &safeFailure{code: "dedicated_classpath_required"}
	}
	prepared := preparedRuntime{
		jdk:              jdk,
		harnessJar:       harnessJar,
		scriptClasspath:  scriptClasspath,
		sharedClasspath:  sharedClasspath,
		vmParameters:     append([]string(nil), settings.AdditionalVMParameters...),
		dedicatedLoader:  settings.DedicatedClassLoader,
		startupTimeoutMS: settings.StartupTimeoutMS,
		workDir:          workDir,
	}
	if settings.UseXBootClasspath {
		prepared.warningCodes = []string{WarningModernXBootClasspathFallback}
	}
	return prepared, nil
}

func (s *Server) health(ctx context.Context) (javarunner.ResolvedJDK, *safeFailure) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.containmentUnavailable() {
		return javarunner.ResolvedJDK{}, &safeFailure{code: "process_containment_unavailable"}
	}
	now := time.Now()
	s.readinessMu.Lock()
	if cached := s.readiness; cached != nil {
		if cached.ready == nil && now.Before(cached.expiresAt) {
			jdk := cached.jdk
			failure := cached.failure
			s.readinessMu.Unlock()
			return jdk, failure
		}
		if cached.ready != nil {
			ready := cached.ready
			s.readinessMu.Unlock()
			select {
			case <-ready:
				return s.health(ctx)
			case <-ctx.Done():
				return javarunner.ResolvedJDK{}, &safeFailure{code: contextErrorCode(ctx)}
			}
		}
	}
	entry := &readinessCacheEntry{ready: make(chan struct{})}
	s.readiness = entry
	s.readinessMu.Unlock()

	// Probe work is bounded by the server's internal budget and remains attached
	// to the admitted server operation so drain cancellation cannot leave a JDK
	// or harness process behind.
	healthCtx, cancel := context.WithTimeout(ctx, s.probeTimeout*2)
	prepared, failure := s.prepareRuntime(healthCtx, RuntimeSettings{})
	if failure != nil {
		cancel()
		s.finishReadiness(entry, javarunner.ResolvedJDK{}, failure)
		return javarunner.ResolvedJDK{}, failure
	}
	if failure := s.verifyHarness(healthCtx, prepared); failure != nil {
		cancel()
		s.finishReadiness(entry, javarunner.ResolvedJDK{}, failure)
		return javarunner.ResolvedJDK{}, failure
	}
	cancel()
	s.finishReadiness(entry, prepared.jdk, nil)
	return prepared.jdk, nil
}

func (s *Server) finishReadiness(entry *readinessCacheEntry, jdk javarunner.ResolvedJDK, failure *safeFailure) {
	s.readinessMu.Lock()
	defer s.readinessMu.Unlock()
	if s.readiness != entry {
		return
	}
	entry.jdk = jdk
	entry.failure = failure
	if failure == nil {
		entry.expiresAt = time.Now().Add(readinessSuccessTTL)
	} else {
		entry.expiresAt = time.Now().Add(readinessFailureTTL)
	}
	ready := entry.ready
	entry.ready = nil
	close(ready)
}

func (s *Server) verifyHarness(ctx context.Context, prepared preparedRuntime) *safeFailure {
	cacheKey := harnessHealthCacheKey(prepared)
	now := time.Now()
	s.harnessHealthMu.Lock()
	expiresAt, healthy := s.harnessHealthy[cacheKey]
	if healthy && !now.Before(expiresAt) {
		delete(s.harnessHealthy, cacheKey)
		healthy = false
	}
	s.harnessHealthMu.Unlock()
	if healthy {
		return nil
	}
	plan, err := javarunner.BuildCommand(javarunner.CommandSpec{
		HarnessJar:  prepared.harnessJar,
		HarnessMain: defaultHarnessMain,
		HarnessArgs: []string{"--health"},
	})
	if err != nil {
		return &safeFailure{code: "harness_configuration_invalid"}
	}
	outcome := s.runProbeProcess(ctx, prepared, plan.Args)
	if !outcome.success || !validHarnessHealth(outcome.stdout) {
		return &safeFailure{code: "harness_health_failed"}
	}
	s.harnessHealthMu.Lock()
	// This cache is bounded because a server has one operator harness/JDK
	// pairing. Clearing also prevents stale paths from accumulating if an
	// operator rotates the mounted harness in place.
	clear(s.harnessHealthy)
	s.harnessHealthy[cacheKey] = time.Now().Add(readinessSuccessTTL)
	s.harnessHealthMu.Unlock()
	return nil
}

type harnessHealthEnvelope struct {
	Contract      string   `json:"contract"`
	Kind          string   `json:"kind"`
	EventContract string   `json:"event_contract"`
	Features      []string `json:"features"`
}

func validHarnessHealth(stdout string) bool {
	for _, raw := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, harnessSessionPrefix) {
			continue
		}
		var envelope harnessHealthEnvelope
		if json.Unmarshal([]byte(strings.TrimPrefix(line, harnessSessionPrefix)), &envelope) != nil ||
			envelope.Contract != harnessIPCContract || envelope.Kind != "capabilities" ||
			envelope.EventContract != harnessEventContract {
			continue
		}
		validation := false
		classloader := false
		persistent := false
		frameToken := false
		for _, feature := range envelope.Features {
			switch feature {
			case harnessValidationV1:
				validation = true
			case harnessClassloaderV1:
				classloader = true
			case FeaturePersistentLifecycleV1:
				persistent = true
			case FeatureSessionFrameTokenV1:
				frameToken = true
			}
		}
		return validation && classloader && persistent && frameToken
	}
	return false
}

func harnessHealthCacheKey(prepared preparedRuntime) string {
	modified := ""
	if info, err := os.Stat(prepared.harnessJar); err == nil {
		modified = strconv.FormatInt(info.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(info.Size(), 10)
	}
	parts := []string{prepared.jdk.JavaBin, prepared.harnessJar, modified}
	return strings.Join(parts, "\x00")
}

func (s *Server) runHarness(ctx context.Context, prepared preparedRuntime, command ActionCommand, requestID string, validationOnly bool) processOutcome {
	method, ok := canonicalLifecycleMethod(command.Method)
	if !ok {
		return processOutcome{errorCode: "target_method_invalid"}
	}
	plan, err := javarunner.BuildCommand(javarunner.CommandSpec{
		VMParameters:         prepared.vmParameters,
		HarnessJar:           prepared.harnessJar,
		ScriptClasspath:      prepared.scriptClasspath,
		SharedClasspath:      prepared.sharedClasspath,
		DedicatedClassLoader: prepared.dedicatedLoader,
		HarnessMain:          defaultHarnessMain,
	})
	if err != nil {
		return processOutcome{errorCode: "harness_configuration_invalid"}
	}
	payload := harnessCommand{
		ProtocolVersion:  harnessIPCContract,
		EventContract:    harnessEventContract,
		RequestID:        requestID,
		ActionName:       command.ActionName,
		ActionType:       command.ActionType,
		ClassName:        strings.TrimSpace(command.ClassName),
		Method:           method,
		SourceArchiveRef: command.SourceArchiveRef,
		StartupTimeoutMS: prepared.startupTimeoutMS,
		UserClasspath:    append([]string(nil), plan.UserClasspath...),
		DedicatedLoader:  prepared.dedicatedLoader,
		ValidationOnly:   validationOnly,
		Params:           command.Params,
	}
	stdin, err := json.Marshal(payload)
	if err != nil {
		return processOutcome{errorCode: "harness_payload_invalid"}
	}
	stdin = append(stdin, '\n')
	return s.runProcess(ctx, prepared, plan.Args, stdin, command, !validationOnly, requestID)
}

type harnessCommand struct {
	ProtocolVersion  string         `json:"protocol_version,omitempty"`
	EventContract    string         `json:"event_contract,omitempty"`
	RequestID        string         `json:"request_id,omitempty"`
	SessionSecret    string         `json:"session_secret,omitempty"`
	ActionName       string         `json:"action_name,omitempty"`
	ActionType       string         `json:"action_type,omitempty"`
	ClassName        string         `json:"class_name"`
	Method           string         `json:"method"`
	SourceArchiveRef string         `json:"source_archive_ref,omitempty"`
	StartupTimeoutMS int64          `json:"startup_timeout_ms,omitempty"`
	UserClasspath    []string       `json:"user_classpath,omitempty"`
	DedicatedLoader  bool           `json:"dedicated_class_loader,omitempty"`
	ValidationOnly   bool           `json:"validation_only,omitempty"`
	Params           map[string]any `json:"params,omitempty"`
}

type processOutcome struct {
	success         bool
	errorCode       string
	errorMessage    string
	stdout          string
	stderr          string
	events          string
	stdoutTruncated bool
	stderrTruncated bool
	eventsTruncated bool
	containmentErr  error
}

func (s *Server) runProcess(ctx context.Context, prepared preparedRuntime, args []string, stdin []byte, action ActionCommand, captureEvents bool, requestID string) processOutcome {
	release, failure := s.acquireProcessSlot(ctx)
	if failure != nil {
		return processOutcome{errorCode: failure.code}
	}
	outcome := s.runProcessAcquired(ctx, prepared, args, stdin, action, captureEvents, requestID)
	s.finishProcessCapacity(release, outcome.containmentErr)
	return outcome
}

func (s *Server) runProbeProcess(ctx context.Context, prepared preparedRuntime, args []string) processOutcome {
	release, failure := s.acquireProbeSlot(ctx)
	if failure != nil {
		return processOutcome{errorCode: failure.code}
	}
	outcome := s.runProcessAcquired(ctx, prepared, args, nil, ActionCommand{}, false, "")
	s.finishProcessCapacity(release, outcome.containmentErr)
	return outcome
}

func (s *Server) runProcessAcquired(ctx context.Context, prepared preparedRuntime, args []string, stdin []byte, action ActionCommand, captureEvents bool, requestID string) processOutcome {
	eventPath := ""
	eventCleanupPending := false
	if captureEvents {
		file, err := os.CreateTemp("", "apexload-java-vuser-events-*.jsonl")
		if err != nil {
			return processOutcome{errorCode: "event_capture_unavailable"}
		}
		eventPath = file.Name()
		_ = file.Close()
		eventCleanupPending = true
		defer func() {
			if eventCleanupPending {
				_ = os.Remove(eventPath)
			}
		}()
	}

	cmd := exec.CommandContext(ctx, prepared.jdk.JavaBin, args...)
	javarunner.ConfigureProcessTree(cmd)
	cmd.WaitDelay = processWaitDelay
	if prepared.workDir != "" {
		cmd.Dir = prepared.workDir
	}
	environment := prepared.jdk.EnvironmentOverrides(os.Getenv("PATH"))
	addKnownEnvironment(environment, action)
	if eventPath != "" {
		environment["APEXLOAD_JAVA_VUSER_EVENT_FILE"] = eventPath
	}
	cmd.Env = subprocessguard.Environment(environment)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout captureBuffer = &cappedBuffer{max: MaxCaptureBytes}
	if requestID != "" {
		// The authoritative one-shot result is the final v2 line. Retain the
		// bounded tail so noisy Actions output cannot evict that frame before it
		// is validated, while still reporting that diagnostics were truncated.
		stdout = &tailCappedBuffer{max: MaxCaptureBytes}
	}
	var stderr captureBuffer = &cappedBuffer{max: MaxCaptureBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	startErr := javarunner.StartProcessTree(cmd)
	err := startErr
	if startErr == nil {
		err = cmd.Wait()
	}
	cleanupErr := javarunner.KillProcessTree(cmd)
	failedStartCapture := errors.Is(startErr, javarunner.ErrProcessContainmentFailed)
	if failedStartCapture {
		s.trackFailedStartReap(cmd, func() { _ = os.Remove(eventPath) })
		eventCleanupPending = false
		// The platform starter already attempted cleanup, but explicitly
		// reported that descendant termination could not be proven. Propagate
		// that terminal condition even when the follow-up kill is a no-op.
		cleanupErr = errors.Join(cleanupErr, startErr)
	}
	err = errors.Join(err, cleanupErr)

	outcome := processOutcome{
		stdout:          stdout.String(),
		stderr:          stderr.String(),
		stdoutTruncated: stdout.Truncated() || failedStartCapture,
		stderrTruncated: stderr.Truncated() || failedStartCapture,
		eventsTruncated: failedStartCapture,
		containmentErr:  cleanupErr,
	}
	if eventPath != "" {
		var fileTruncated bool
		outcome.events, fileTruncated = readCappedFile(eventPath, MaxCaptureBytes)
		outcome.eventsTruncated = outcome.eventsTruncated || fileTruncated
	}
	var summary harnessSummary
	hasSummary := false
	if len(stdin) > 0 {
		summary, hasSummary = parseHarnessSummary(outcome.stdout, requestID)
		if hasSummary {
			outcome.errorMessage = boundedHarnessError(summary.Error)
			outcome.eventsTruncated = outcome.eventsTruncated || summary.EventsTruncated ||
				(summary.EventFallbackUsed && outcome.stderrTruncated)
		}
	}
	if ctx.Err() != nil {
		outcome.errorCode = contextErrorCode(ctx)
		return outcome
	}
	if err != nil {
		outcome.errorCode = "process_failed"
		return outcome
	}
	// Health mode has no stdin and emits a capabilities frame rather than a
	// result summary. Its caller performs the contract check.
	if len(stdin) == 0 {
		outcome.success = true
		return outcome
	}
	if !hasSummary {
		outcome.errorCode = "result_summary_missing"
		return outcome
	}
	if summary.Success == nil || !*summary.Success {
		outcome.errorCode = "harness_reported_failure"
		return outcome
	}
	outcome.success = true
	return outcome
}

func (s *Server) trackFailedStartReap(cmd *exec.Cmd, cleanup func()) {
	s.trackFailedStartReapDone(javarunner.ReapFailedStart(cmd), cleanup)
}

func (s *Server) trackFailedStartReapDone(reaped <-chan error, cleanup func()) {
	if reaped == nil {
		if cleanup != nil {
			cleanup()
		}
		return
	}
	s.failedStartReaps.Add(1)
	go func() {
		defer s.failedStartReaps.Done()
		<-reaped
		if cleanup != nil {
			cleanup()
		}
	}()
}

func (s *Server) acquireProcessSlot(ctx context.Context) (func(), *safeFailure) {
	if err := ctx.Err(); err != nil {
		return nil, &safeFailure{code: contextErrorCode(ctx)}
	}
	if s.containmentUnavailable() {
		return nil, &safeFailure{code: "process_containment_unavailable"}
	}
	if s.draining.Load() {
		return nil, &safeFailure{code: "session_unusable"}
	}
	select {
	case s.slots <- struct{}{}:
		if err := ctx.Err(); err != nil || s.draining.Load() || s.containmentUnavailable() {
			<-s.slots
			if err != nil {
				return nil, &safeFailure{code: contextErrorCode(ctx)}
			}
			if s.containmentUnavailable() {
				return nil, &safeFailure{code: "process_containment_unavailable"}
			}
			return nil, &safeFailure{code: "session_unusable"}
		}
		return func() { <-s.slots }, nil
	case <-s.containmentFailedDone:
		return nil, &safeFailure{code: "process_containment_unavailable"}
	case <-ctx.Done():
		return nil, &safeFailure{code: contextErrorCode(ctx)}
	}
}

func (s *Server) acquireProbeSlot(ctx context.Context) (func(), *safeFailure) {
	if err := ctx.Err(); err != nil {
		return nil, &safeFailure{code: contextErrorCode(ctx)}
	}
	if s.containmentUnavailable() {
		return nil, &safeFailure{code: "process_containment_unavailable"}
	}
	if s.draining.Load() {
		return nil, &safeFailure{code: "server_draining"}
	}
	select {
	case s.probeSlots <- struct{}{}:
		if err := ctx.Err(); err != nil || s.draining.Load() || s.containmentUnavailable() {
			<-s.probeSlots
			if err != nil {
				return nil, &safeFailure{code: contextErrorCode(ctx)}
			}
			if s.containmentUnavailable() {
				return nil, &safeFailure{code: "process_containment_unavailable"}
			}
			return nil, &safeFailure{code: "server_draining"}
		}
		return func() { <-s.probeSlots }, nil
	case <-s.containmentFailedDone:
		return nil, &safeFailure{code: "process_containment_unavailable"}
	case <-ctx.Done():
		return nil, &safeFailure{code: contextErrorCode(ctx)}
	}
}

func (s *Server) finishProcessCapacity(release func(), containmentErr error) {
	if containmentErr != nil {
		// The token is the quarantine lease. Releasing it would let a stream of
		// failed cleanup attempts create more descendants than MaxProcesses.
		s.containmentFailed.Store(true)
		s.containmentFailedOnce.Do(func() {
			close(s.containmentFailedDone)
		})
		return
	}
	if release != nil {
		release()
	}
}

func (s *Server) containmentUnavailable() bool {
	if javarunner.ProcessContainmentFailed() {
		s.finishProcessCapacity(nil, javarunner.ErrProcessContainmentFailed)
	}
	return s.containmentFailed.Load()
}

func (s *Server) supportsStrongProcessContainmentAvailable(ctx context.Context) bool {
	release, failure := s.acquireProbeSlot(ctx)
	if failure != nil {
		return false
	}
	available := s.supportsStrongProcessContainment()
	return s.finishStrongContainmentProbe(
		release,
		available,
		javarunner.ProcessContainmentFailed(),
	)
}

func (s *Server) finishStrongContainmentProbe(release func(), available, containmentFailed bool) bool {
	if containmentFailed {
		// Keep the probe token as a quarantine lease. The OS capability check
		// launches a real process on platforms such as Linux; if its cleanup
		// cannot be proven, releasing the probe slot would let later checks
		// create more potentially uncontained descendants.
		s.finishProcessCapacity(nil, javarunner.ErrProcessContainmentFailed)
		return false
	}
	s.finishProcessCapacity(release, nil)
	return available
}

func addKnownEnvironment(environment map[string]string, command ActionCommand) {
	values := map[string]string{
		"APEXLOAD_VUSER_ID":            command.VUserID,
		"APEXLOAD_GROUP_NAME":          command.GroupName,
		"APEXLOAD_SCENARIO_ID":         command.ScenarioID,
		"APEXLOAD_IGNORE_THINK_TIME":   command.IgnoreThinkTime,
		"APEXLOAD_THINK_TIME_LIMIT_MS": command.ThinkTimeLimitMS,
	}
	for name, value := range values {
		if value != "" {
			environment[name] = value
		}
	}
}

type harnessSummary struct {
	Contract          string `json:"contract"`
	Kind              string `json:"kind"`
	RequestID         string `json:"request_id"`
	Success           *bool  `json:"success"`
	Error             string `json:"error,omitempty"`
	EventFallbackUsed bool   `json:"event_fallback_used"`
	EventsTruncated   bool   `json:"events_truncated"`
}

func parseHarnessSummary(stdout, requestID string) (harnessSummary, bool) {
	lines := strings.Split(stdout, "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var summary harnessSummary
		if json.Unmarshal([]byte(line), &summary) == nil &&
			summary.Contract == harnessIPCContract && summary.Kind == "result" &&
			summary.RequestID == requestID && summary.Success != nil {
			return summary, true
		}
	}
	return harnessSummary{}, false
}

func resolveWorkDir(raw string) (string, *safeFailure) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	if len(raw) > maxPathBytes || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", &safeFailure{code: "work_dir_unavailable"}
	}
	path, err := filepath.Abs(filepath.Clean(raw))
	if err != nil {
		return "", &safeFailure{code: "work_dir_unavailable"}
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", &safeFailure{code: "work_dir_unavailable"}
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", &safeFailure{code: "work_dir_unavailable"}
	}
	return path, nil
}

func resolveRequiredFile(raw, workDir, code string) (string, *safeFailure) {
	if strings.TrimSpace(raw) == "" || len(raw) > maxPathBytes || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", &safeFailure{code: code}
	}
	path := raw
	if !filepath.IsAbs(path) && workDir != "" {
		path = filepath.Join(workDir, path)
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", &safeFailure{code: code}
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", &safeFailure{code: code}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", &safeFailure{code: code}
	}
	return resolved, nil
}

func resolveClasspath(entries []string, workDir string) ([]string, *safeFailure) {
	resolved, issue := javarunner.ResolveClasspath(entries, workDir)
	if issue == nil {
		return resolved, nil
	}
	failure := &safeFailure{code: "classpath_entry_invalid"}
	if issue.Index >= 0 {
		failure.index = intPointer(issue.Index)
	}
	switch issue.Category {
	case javarunner.ClasspathTooMany:
		failure.code = "classpath_too_many_entries"
	case javarunner.ClasspathUnavailable:
		failure.code = "classpath_entry_unavailable"
	}
	return nil, failure
}

func validateTarget(target Target) *safeFailure {
	className := strings.TrimSpace(target.ClassName)
	method := strings.TrimSpace(target.Method)
	if className == "" || len(className) > maxTargetBytes || strings.IndexFunc(className, unicode.IsControl) >= 0 {
		return &safeFailure{code: "target_class_invalid"}
	}
	if len(method) > maxTargetBytes || strings.IndexFunc(method, unicode.IsControl) >= 0 {
		return &safeFailure{code: "target_method_invalid"}
	}
	if _, ok := canonicalLifecycleMethod(method); !ok {
		return &safeFailure{code: "target_method_invalid"}
	}
	return nil
}

// canonicalLifecycleMethod accepts the LoadRunner lifecycle aliases supported
// by the harness while retaining Java's distinct capital-A Action method.
// Requests carrying a session_id execute these phases on one persistent
// Actions instance; legacy full_lifecycle requests remain one-shot.
func canonicalLifecycleMethod(method string) (string, bool) {
	method = strings.TrimSpace(method)
	if method == "" {
		return "full_lifecycle", true
	}
	if method == "Action" {
		return method, true
	}
	switch strings.ToLower(method) {
	case "init", "vuser_init", "initialize", "action", "run", "end", "vuser_end", "teardown", "full_lifecycle":
		return strings.ToLower(method), true
	default:
		return method, false
	}
}

func boundedTimeout(milliseconds int64, fallback time.Duration) (time.Duration, *safeFailure) {
	if milliseconds == 0 {
		return fallback, nil
	}
	if milliseconds < 0 || milliseconds > MaximumExecutionTimeout.Milliseconds() {
		return 0, &safeFailure{code: "timeout_out_of_range"}
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func contextErrorCode(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "execution_timeout"
	}
	return "execution_canceled"
}

type captureBuffer interface {
	io.Writer
	String() string
	Truncated() bool
}

type cappedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	max       int
	truncated bool
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.max - b.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			_, _ = b.buffer.Write(data[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buffer.Write(data)
		}
	} else if len(data) > 0 {
		b.truncated = true
	}
	return len(data), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *cappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func (b *cappedBuffer) markTruncated() {
	b.mu.Lock()
	b.truncated = true
	b.mu.Unlock()
}

// tailCappedBuffer retains the final max bytes. One-shot v2 result envelopes
// are emitted last, so this keeps the authoritative frame available even after
// bounded, arbitrarily noisy user stdout.
type tailCappedBuffer struct {
	mu        sync.Mutex
	buffer    []byte
	max       int
	truncated bool
}

func (b *tailCappedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	written := len(data)
	if written == 0 {
		return 0, nil
	}
	if b.max <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(data) >= b.max {
		hadBuffered := len(b.buffer) > 0
		b.buffer = append(b.buffer[:0], data[len(data)-b.max:]...)
		b.truncated = b.truncated || hadBuffered || len(data) > b.max
		return written, nil
	}
	overflow := len(b.buffer) + len(data) - b.max
	if overflow > 0 {
		copy(b.buffer, b.buffer[overflow:])
		b.buffer = b.buffer[:len(b.buffer)-overflow]
		b.truncated = true
	}
	b.buffer = append(b.buffer, data...)
	return written, nil
}

func (b *tailCappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buffer)
}

func (b *tailCappedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

func readCappedFile(path string, max int) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(max)+1))
	if err != nil {
		return "", false
	}
	if len(data) > max {
		return string(data[:max]), true
	}
	return string(data), false
}

func decodeRequest(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func methodNotAllowed(w http.ResponseWriter, method string) {
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, ErrorResponse{Contract: ContractVersion, ErrorCode: code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func intPointer(value int) *int { return &value }

// ListenLoopback binds an explicit loopback-only TCP address. Wildcard and
// non-loopback binds fail before an HTTP server is started.
func ListenLoopback(address string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid Java runner sidecar address: %w", err)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("Java runner sidecar address must be loopback-only")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !tcpAddress.IP.IsLoopback() {
		_ = listener.Close()
		return nil, fmt.Errorf("Java runner sidecar listener is not loopback-only")
	}
	return listener, nil
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if zoneIndex := strings.LastIndex(host, "%"); zoneIndex >= 0 {
		host = host[:zoneIndex]
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
