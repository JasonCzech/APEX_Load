package javasidecar

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
)

const (
	PooledControlContract       = "apexload-java-vuser-pooled-control.v1"
	FeaturePooledReservationsV1 = "java_vuser_pooled_reservations_v1"
	FeaturePooledPreparationV1  = "java_vuser_pooled_preparation_v1"
	FeaturePooledCommitV1       = "java_vuser_pooled_commit_v1"
	FeaturePooledInvocationV1   = "java_vuser_pooled_invocation_v1"
	FeaturePooledDiagnosticsV1  = "java_vuser_pooled_diagnostics_v1"
	maxPooledControlReplies     = 64
	// maxPooledControlVUsers bounds one run's admissions on one runner. The
	// complete slot list travels in one prepare request; 4,096 worst-case slot
	// contexts fit MaxRequestBytes (pinned by a test). Larger generators split
	// groups across runs; the shared admission budget remains authoritative.
	maxPooledControlVUsers = 4096
	// maxPooledControlHosts bounds one run's JVMs, independently of the
	// operator process ceiling that also caps every claim.
	maxPooledControlHosts = 1024
	// maxConcurrentPooledControlRuns bounds live network runs (one per
	// execution/group) so a misbehaving owner cannot exhaust run bookkeeping.
	maxConcurrentPooledControlRuns = 64
)

type PooledControlProfile struct {
	// Optional for older programmatic profiles; required by operator manifests.
	ScriptSHA256     string
	Selection        javarunner.PooledSelection
	Budget           javarunner.HostBudget
	AllowPreparation bool
	AllowCommit      bool
	AllowInvocation  bool
}

// PooledControlProfiles is immutable, operator-owned pilot configuration. No
// profile paths, JVM arguments, or memory budgets are accepted over the wire.
type PooledControlProfiles struct {
	profiles map[string]PooledControlProfile
}

func NewPooledControlProfiles(profiles map[string]PooledControlProfile) (*PooledControlProfiles, error) {
	if len(profiles) == 0 || len(profiles) > 32 {
		return nil, javarunner.ErrPooledQualification
	}
	result := &PooledControlProfiles{profiles: make(map[string]PooledControlProfile, len(profiles))}
	for id, p := range profiles {
		d := p.Selection.Decision()
		if (p.ScriptSHA256 != "" && !validPooledScriptSHA256(p.ScriptSHA256)) || (p.AllowInvocation && !p.AllowCommit) || (p.AllowCommit && !p.AllowPreparation) || !executionIDPattern.MatchString(id) || d.Mode != "pooled_pilot" || p.Budget.MaxHosts < 1 || p.Budget.MaxHosts > maxPooledControlHosts || p.Budget.MaxVUsers < d.SlotsPerHost || p.Budget.MaxVUsers > maxPooledControlVUsers || p.Budget.MaxMemoryBytes < d.HostMemoryBytes {
			return nil, javarunner.ErrPooledQualification
		}
		if _, err := p.Selection.LaunchProvider(); err != nil {
			return nil, err
		}
		result.profiles[id] = p
	}
	return result, nil
}

type PooledControlRequest struct {
	Method           string              `json:"method,omitempty"`
	Sequence         uint64              `json:"sequence,omitempty"`
	TimeoutMS        int64               `json:"timeout_ms,omitempty"`
	Params           map[string]string   `json:"params,omitempty"`
	Contract         string              `json:"contract"`
	PooledContract   string              `json:"pooled_contract"`
	Operation        string              `json:"operation"`
	RequestID        string              `json:"request_id"`
	OwnerID          string              `json:"owner_id"`
	OwnerEpoch       string              `json:"owner_epoch"`
	RunID            string              `json:"run_id,omitempty"`
	RunToken         string              `json:"run_token,omitempty"`
	ProfileID        string              `json:"profile_id,omitempty"`
	ExecutionID      string              `json:"execution_id,omitempty"`
	GroupID          string              `json:"group_id,omitempty"`
	VUsers           int                 `json:"vusers,omitempty"`
	PrepareTimeoutMS int64               `json:"prepare_timeout_ms,omitempty"`
	ReservationID    string              `json:"reservation_id,omitempty"`
	Slots            []PooledSlotContext `json:"slots,omitempty"`
	HostID           string              `json:"host_id,omitempty"`
	SlotID           string              `json:"slot_id,omitempty"`
	// Shared is the script-derived input of a claim_shared request only.
	Shared *SharedJVMClaim `json:"shared,omitempty"`
}

type PooledControlDecision struct {
	ScriptSHA256        string `json:"script_sha256,omitempty"`
	TargetClass         string `json:"target_class"`
	InvocationEnabled   bool   `json:"invocation_enabled,omitempty"`
	PreparationEnabled  bool   `json:"preparation_enabled,omitempty"`
	CommitEnabled       bool   `json:"commit_enabled,omitempty"`
	Mode                string `json:"mode"`
	ProfileID           string `json:"profile_id"`
	ResourceProfile     string `json:"resource_profile"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	ContainmentBackend  string `json:"containment_backend"`
	SlotsPerHost        int    `json:"slots_per_host"`
	HostMemoryBytes     int64  `json:"host_memory_bytes"`
	MaxHosts            int    `json:"max_hosts"`
	MaxVUsers           int    `json:"max_vusers"`
	MaxMemoryBytes      int64  `json:"max_memory_bytes"`
}

type PooledPlacement struct {
	HostID      string   `json:"host_id"`
	SlotIDs     []string `json:"slot_ids"`
	MemoryBytes int64    `json:"memory_bytes"`
}

type PooledControlResponse struct {
	NativeData            bool                        `json:"native_data,omitempty"`
	Diagnostics           *javarunner.HostDiagnostics `json:"diagnostics,omitempty"`
	Invocation            *PooledInvocationResult     `json:"invocation,omitempty"`
	Contract              string                      `json:"contract"`
	PooledContract        string                      `json:"pooled_contract"`
	Operation             string                      `json:"operation"`
	RequestID             string                      `json:"request_id"`
	RunID                 string                      `json:"run_id"`
	RunToken              string                      `json:"run_token,omitempty"`
	LeaseTTLMS            int64                       `json:"lease_ttl_ms"`
	Decision              PooledControlDecision       `json:"decision"`
	ReservationID         string                      `json:"reservation_id,omitempty"`
	Placements            []PooledPlacement           `json:"placements,omitempty"`
	PrepareDeadlineUnixMS int64                       `json:"prepare_deadline_unix_ms,omitempty"`
	Reaped                bool                        `json:"reaped,omitempty"`
	Ready                 bool                        `json:"ready,omitempty"`
	Committed             bool                        `json:"committed,omitempty"`
	SlotClosed            bool                        `json:"slot_closed,omitempty"`
	HostID                string                      `json:"host_id,omitempty"`
	SlotID                string                      `json:"slot_id,omitempty"`
	// WarningCodes are value-free claim warnings (claim_shared only).
	WarningCodes []string `json:"warning_codes,omitempty"`
}

type pooledControlReply struct {
	digest      [32]byte
	response    PooledControlResponse
	err         error
	preparation *pooledPreparation
	phase       *pooledControlPhase
}

func (s *Server) pooledControlEnabled() bool {
	return s.hostAdmission != nil && (s.pooledControlProfiles != nil && len(s.pooledControlProfiles.profiles) > 0 || s.sharedJVM != nil)
}

func (s *Server) pooledProfile(id string) (PooledControlProfile, bool) {
	if s.pooledControlProfiles == nil {
		return PooledControlProfile{}, false
	}
	p, ok := s.pooledControlProfiles.profiles[id]
	return p, ok
}

func validPooledControlRequest(q PooledControlRequest) bool {
	if q.Operation != "invoke" && strings.HasPrefix(q.RequestID, "invoke-") {
		return false
	}
	if !executionIDPattern.MatchString(q.RequestID) || !executionIDPattern.MatchString(q.OwnerID) || !executionIDPattern.MatchString(q.OwnerEpoch) {
		return false
	}
	if q.Operation != "invoke" && (q.Method != "" || q.Sequence != 0 || q.TimeoutMS != 0 || len(q.Params) != 0) {
		return false
	}
	if q.Operation != "close_slot" && q.Operation != "invoke" && q.Operation != "diagnostics" && (q.HostID != "" || q.SlotID != "") {
		return false
	}
	if q.Operation != "prepare" && len(q.Slots) != 0 {
		return false
	}
	if (q.Operation == "claim_shared") != (q.Shared != nil) {
		return false
	}
	if q.Operation == "claim" || q.Operation == "claim_shared" {
		profileOK := executionIDPattern.MatchString(q.ProfileID)
		if q.Operation == "claim_shared" {
			profileOK = q.ProfileID == "" && validSharedJVMClaim(q.Shared)
		}
		return profileOK && executionIDPattern.MatchString(q.ExecutionID) && executionIDPattern.MatchString(q.GroupID) && q.RunID == "" && q.RunToken == "" && q.VUsers == 0 && q.PrepareTimeoutMS == 0 && q.ReservationID == ""
	}
	if !executionIDPattern.MatchString(q.RunID) || len(q.RunToken) != 64 || q.ProfileID != "" || q.ExecutionID != "" || q.GroupID != "" {
		return false
	}
	switch q.Operation {
	case "diagnostics":
		return q.VUsers == 0 && q.PrepareTimeoutMS == 0 && q.ReservationID == "" && executionIDPattern.MatchString(q.HostID) && q.SlotID == ""
	case "reserve":
		return q.VUsers > 0 && q.VUsers <= maxPooledControlVUsers && q.PrepareTimeoutMS > 0 && q.PrepareTimeoutMS <= time.Hour.Milliseconds() && q.ReservationID == ""
	case "prepare":
		return q.VUsers == 0 && q.PrepareTimeoutMS == 0 && executionIDPattern.MatchString(q.ReservationID) && validPooledSlots(q.Slots)
	case "invoke":
		return validPooledInvocation(q)
	case "close_slot":
		return q.VUsers == 0 && q.PrepareTimeoutMS == 0 && executionIDPattern.MatchString(q.ReservationID) && executionIDPattern.MatchString(q.HostID) && executionIDPattern.MatchString(q.SlotID)
	case "commit", "abort":
		return q.VUsers == 0 && q.PrepareTimeoutMS == 0 && executionIDPattern.MatchString(q.ReservationID)
	case "renew", "close":
		return q.VUsers == 0 && q.PrepareTimeoutMS == 0 && q.ReservationID == ""
	default:
		return false
	}
}

func (s *Server) handlePooledControl(w http.ResponseWriter, r *http.Request) {
	if javarunner.ServeNativeHTTP(w, r, MaxResponseBytes, s.handlePooledControl) {
		return
	}

	if !s.pooledControlEnabled() {
		writeError(w, http.StatusPreconditionFailed, "pooled_control_disabled")
		return
	}
	var q PooledControlRequest
	if !decodeRequest(w, r, &q) {
		return
	}
	if q.Contract != ContractVersion || q.PooledContract != PooledControlContract {
		writeError(w, http.StatusPreconditionFailed, "pooled_contract_mismatch")
		return
	}
	if !validPooledControlRequest(q) {
		writeError(w, http.StatusBadRequest, "pooled_request_invalid")
		return
	}
	if q.Operation == "claim" {
		s.claimPooledControl(w, r, q)
		return
	}
	if q.Operation == "claim_shared" {
		s.claimSharedControl(w, r, q)
		return
	}
	owner, code := s.beginOwnerOperation(q.OwnerID, q.OwnerEpoch)
	if code != "" {
		writePooledControlError(w, javarunner.ErrHostFence)
		return
	}
	defer owner.operations.Done()
	s.pooledMu.Lock()
	var run *PooledRun
	for _, candidate := range s.pooledRuns {
		if candidate.owner == owner && candidate.controlID == q.RunID && subtle.ConstantTimeCompare([]byte(candidate.controlToken), []byte(q.RunToken)) == 1 {
			run = candidate
			break
		}
	}
	s.pooledMu.Unlock()
	if run == nil {
		writeError(w, http.StatusConflict, "pooled_run_fenced")
		return
	}
	response := PooledControlResponse{Contract: ContractVersion, PooledContract: PooledControlContract, Operation: q.Operation, RequestID: q.RequestID, RunID: q.RunID, LeaseTTLMS: s.runtimeLeaseTTL.Milliseconds(), Decision: run.controlDecision}
	var err error
	switch q.Operation {
	case "diagnostics":
		response.HostID = q.HostID
		var diagnostics javarunner.HostDiagnostics
		diagnostics, err = run.Diagnostics(r.Context(), q.HostID)
		if err == nil {
			response.Diagnostics = &diagnostics
		}
	case "invoke":
		response, err = run.invokeControl(r.Context(), q, response)
	case "commit", "close_slot":
		response, err = run.lifecycleControl(r.Context(), q, response)
	case "prepare":
		response, err = run.prepareControl(r.Context(), q, response)
	case "renew":
		err = run.Renew(r.Context())
	case "close":
		err = run.Close(r.Context())
		response.Reaped = err == nil
	default:
		response, err = run.reservationControl(r, q, response)
	}
	if err != nil {
		writePooledControlError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) claimPooledControl(w http.ResponseWriter, r *http.Request, q PooledControlRequest) {
	// Claims are serialized so the concurrent-run bound below is exact; this
	// lock never blocks renewal or cleanup. Each run owns one execution/group,
	// and the shared host admission budget caps their combined JVMs and memory.
	s.pooledControlMu.Lock()
	defer s.pooledControlMu.Unlock()
	profile, ok := s.pooledProfile(q.ProfileID)
	if !ok {
		writeError(w, http.StatusPreconditionFailed, "pooled_profile_unavailable")
		return
	}
	owner, code := s.beginOwnerOperation(q.OwnerID, q.OwnerEpoch)
	if code != "" {
		writePooledControlError(w, javarunner.ErrHostFence)
		return
	}
	defer owner.operations.Done()
	if !s.pooledRunAdmissionOpen(owner) {
		writeError(w, http.StatusConflict, "pooled_group_limit")
		return
	}
	budget := profile.Budget
	shared := s.hostAdmission.Budget()
	budget.MaxHosts = min(budget.MaxHosts, shared.MaxHosts, cap(s.slots))
	budget.MaxVUsers = min(budget.MaxVUsers, shared.MaxVUsers)
	budget.MaxMemoryBytes = min(budget.MaxMemoryBytes, shared.MaxMemoryBytes)
	d := profile.Selection.Decision()
	if budget.MaxVUsers < d.SlotsPerHost || budget.MaxMemoryBytes < d.HostMemoryBytes {
		writePooledControlError(w, javarunner.ErrHostCapacity)
		return
	}
	run, err := s.OpenPooledRun(r.Context(), q.OwnerID, q.OwnerEpoch, q.ExecutionID, q.GroupID, budget, profile.Selection)
	if err != nil {
		writePooledControlError(w, err)
		return
	}
	decision := PooledControlDecision{ScriptSHA256: profile.ScriptSHA256, TargetClass: d.TargetClass, PreparationEnabled: profile.AllowPreparation, CommitEnabled: profile.AllowCommit, InvocationEnabled: profile.AllowInvocation, Mode: d.Mode, ProfileID: q.ProfileID, ResourceProfile: d.ResourceProfile, ConfigurationSHA256: d.ConfigurationSHA256, ContainmentBackend: d.ContainmentBackend, SlotsPerHost: d.SlotsPerHost, HostMemoryBytes: d.HostMemoryBytes, MaxHosts: budget.MaxHosts, MaxVUsers: budget.MaxVUsers, MaxMemoryBytes: budget.MaxMemoryBytes}
	s.publishPooledControlRun(w, q, run, q.ProfileID, decision, nil)
}

// pooledRunAdmissionOpen bounds live network runs per owner. Fenced runs from
// an earlier epoch stay bounded by maxPooledRuns and must not block the
// replacement generator. Callers hold pooledControlMu so the count is exact.
func (s *Server) pooledRunAdmissionOpen(owner *ownerGeneration) bool {
	active := 0
	for _, run := range s.selectedPooledRuns(owner) {
		select {
		case <-run.closed:
		default:
			active++
		}
	}
	return active < maxConcurrentPooledControlRuns
}

// publishPooledControlRun assigns the opaque wire identity of a newly opened
// run and answers the claim. A token failure closes the run it cannot publish.
func (s *Server) publishPooledControlRun(w http.ResponseWriter, q PooledControlRequest, run *PooledRun, profileID string, decision PooledControlDecision, warnings []string) {
	id, idErr := newOpaqueToken(s.randomBytes)
	token, tokenErr := newOpaqueToken(s.randomBytes)
	if idErr != nil || tokenErr != nil {
		go func() { _ = run.Close(context.Background()) }()
		writeError(w, http.StatusServiceUnavailable, "pooled_control_failed")
		return
	}
	s.pooledMu.Lock()
	run.controlID, run.controlToken, run.controlProfile = "pool-"+id, token, profileID
	run.controlDecision = decision
	s.pooledMu.Unlock()
	go func() { <-run.closed; run.controlMu.Lock(); run.controlReplies = nil; run.controlMu.Unlock() }()
	writeJSON(w, http.StatusOK, PooledControlResponse{Contract: ContractVersion, PooledContract: PooledControlContract, Operation: q.Operation, RequestID: q.RequestID, RunID: run.controlID, RunToken: token, LeaseTTLMS: s.runtimeLeaseTTL.Milliseconds(), Decision: decision, WarningCodes: warnings})
}

var errPooledRequestConflict = errors.New("pooled_request_conflict")
var errPooledControlLimit = errors.New("pooled_control_limit")

func (run *PooledRun) reservationControl(r *http.Request, q PooledControlRequest, response PooledControlResponse) (PooledControlResponse, error) {
	run.controlMu.Lock()
	defer run.controlMu.Unlock()
	finish, err := run.begin(r.Context())
	if err != nil {
		return response, err
	}
	defer finish()
	encoded, _ := json.Marshal(q)
	digest := sha256.Sum256(encoded)
	if previous, ok := run.controlReplies[q.RequestID]; ok {
		if previous.digest != digest {
			return response, errPooledRequestConflict
		}
		if previous.err == nil && q.Operation == "reserve" {
			hosts, _ := run.ledger.Snapshot()
			active := 0
			for _, h := range hosts {
				if h.ReservationID == previous.response.ReservationID && h.State == javarunner.HostReserved {
					active++
				}
			}
			if active != len(previous.response.Placements) {
				return response, javarunner.ErrHostState
			}
		}
		return previous.response, previous.err
	}
	if len(run.controlReplies) >= maxPooledControlReplies {
		// Abort is idempotent in the ledger and must remain available even if
		// request bookkeeping is full. Do not grow the cache for this case.
		if q.Operation == "abort" {
			response.ReservationID = q.ReservationID
			return response, run.Abort(r.Context(), q.ReservationID)
		}
		return response, errPooledControlLimit
	}
	switch q.Operation {
	case "reserve":
		var allocations []javarunner.HostAllocation
		allocations, err = run.Reserve(r.Context(), q.VUsers, time.Duration(q.PrepareTimeoutMS)*time.Millisecond)
		for _, a := range allocations {
			response.ReservationID = a.ReservationID
			response.Placements = append(response.Placements, PooledPlacement{HostID: a.ID, SlotIDs: a.SlotIDs, MemoryBytes: a.MemoryBytes})
		}
		if err == nil {
			var deadline time.Time
			deadline, err = run.ledger.PreparationDeadline(run.owner.epoch, response.ReservationID)
			response.PrepareDeadlineUnixMS = deadline.UnixMilli()
		}
	case "abort":
		response.ReservationID = q.ReservationID
		err = run.Abort(r.Context(), q.ReservationID)
	}
	if run.controlReplies == nil {
		run.controlReplies = make(map[string]pooledControlReply)
	}
	run.controlReplies[q.RequestID] = pooledControlReply{digest: digest, response: response, err: err}
	return response, err
}

func writePooledControlError(w http.ResponseWriter, err error) {
	code := "pooled_control_failed"
	for _, known := range []error{javarunner.ErrHostFence, javarunner.ErrHostCapacity, javarunner.ErrHostIdentity, javarunner.ErrHostState, javarunner.ErrHostCapacityClosed, errPooledRequestConflict, errPooledControlLimit, errPooledPreparationDisabled, errPooledCommitDisabled, javarunner.ErrPooledHostReaped, errPooledInvocationDisabled, errPooledEventCapture, errPooledSlotBusy} {
		if errors.Is(err, known) {
			code = known.Error()
			break
		}
	}
	if errors.Is(err, javarunner.ErrProcessContainmentFailed) {
		code = "pooled_cleanup_failed"
	}
	writeError(w, http.StatusConflict, code)
}

func validPooledScriptSHA256(s string) bool {
	digest, err := hex.DecodeString(s)
	return err == nil && len(digest) == 32 && strings.ToLower(s) == s
}
