package javasidecar

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/jasonczech/apexload/pkg/javarunner"
	"net/http"
)

// PooledControl sends an explicitly negotiated pilot control request. It never
// retries, selects pooling, falls back, or changes the dedicated Vuser lease.
// Keep RequestID stable only when retrying reserve/prepare/commit/close_slot/abort with identical input.
// Invocations are never replayable; an ambiguous invocation requires close/abort.
// An ambiguous claim must be abandoned and allowed to expire, not replayed as
// a new run. A transport failure is never evidence that capacity was released.
func (c *Client) PooledControl(ctx context.Context, request PooledControlRequest) (PooledControlResponse, error) {
	request.Contract, request.PooledContract = ContractVersion, PooledControlContract
	request.OwnerID, request.OwnerEpoch, _, _ = c.ownerAndRuntimeLease()
	if !validPooledControlRequest(request) {
		return PooledControlResponse{}, requestFailure("invalid Java pooled control request", false)
	}
	var response PooledControlResponse
	if err := c.do(ctx, http.MethodPost, "/v1/pooled-control", request, &response); err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) && !knownPooledControlError(remote.Code) {
			return PooledControlResponse{}, requestFailure("invalid Java pooled control error response", true)
		}
		return PooledControlResponse{}, err
	}
	if !validPooledControlResponse(request, response) {
		return PooledControlResponse{}, requestFailure("invalid Java pooled control response", true)
	}
	return response, nil
}

func PooledControlOutcomeAmbiguous(err error) bool {
	var remote *RemoteError
	if errors.As(err, &remote) && (remote.Code == "pooled_control_failed" || remote.Code == "pooled_cleanup_failed" || remote.Code == "pooled_host_reaped" || remote.Code == "pooled_event_capture_invalid") {
		return true
	}
	return ExecutionOutcomeAmbiguous(err)
}

func knownPooledControlError(code string) bool {
	switch code {
	case "pooled_invocation_disabled", "pooled_event_capture_invalid", "pooled_slot_busy", "pooled_commit_disabled", "pooled_host_reaped", "pooled_preparation_disabled", "pooled_control_disabled", "pooled_contract_mismatch", "pooled_request_invalid", "pooled_profile_unavailable", "pooled_owner_fenced", "pooled_run_fenced", "pooled_group_limit", "pooled_control_failed", "pooled_cleanup_failed", "pooled_capacity_unavailable", "pooled_host_identity_invalid", "pooled_host_state_invalid", "pooled_admission_closed", "pooled_request_conflict", "pooled_control_limit",
		"runner_disabled", "runner_auth_unavailable", "runner_unauthorized", "server_draining", "request_too_large", "invalid_request", "method_not_allowed":
		return true
	default:
		return false
	}
}

func validPooledControlResponse(q PooledControlRequest, r PooledControlResponse) bool {
	if q.Operation != "diagnostics" && r.Diagnostics != nil {
		return false
	}
	d := r.Decision
	if !javarunner.ValidPooledTargetClass(d.TargetClass) || (d.ScriptSHA256 != "" && !validPooledScriptSHA256(d.ScriptSHA256)) {
		return false
	}
	if (d.InvocationEnabled && !d.CommitEnabled) || (q.Operation != "invoke" && r.Invocation != nil) {
		return false
	}
	if (r.Decision.CommitEnabled && !r.Decision.PreparationEnabled) || (q.Operation != "commit" && r.Committed) || (q.Operation != "close_slot" && r.SlotClosed) || (q.Operation != "close_slot" && q.Operation != "invoke" && q.Operation != "diagnostics" && (r.HostID != "" || r.SlotID != "")) {
		return false
	}
	if q.Operation != "prepare" && (r.Ready || r.NativeData) {
		return false
	}
	if r.Contract != ContractVersion || r.PooledContract != PooledControlContract || r.Operation != q.Operation || r.RequestID != q.RequestID || !executionIDPattern.MatchString(r.RunID) || r.LeaseTTLMS != RuntimeLeaseTTL.Milliseconds() || d.Mode != "pooled_pilot" || !executionIDPattern.MatchString(d.ProfileID) || !executionIDPattern.MatchString(d.ResourceProfile) || d.ContainmentBackend != "delegated_linux" || d.SlotsPerHost < 2 || d.SlotsPerHost > 256 || d.MaxHosts < 1 || d.MaxHosts > 256 || d.MaxVUsers < d.SlotsPerHost || d.MaxVUsers > 256 || d.HostMemoryBytes <= 0 || d.MaxMemoryBytes < d.HostMemoryBytes {
		return false
	}
	if decoded, err := hex.DecodeString(d.ConfigurationSHA256); err != nil || len(decoded) != 32 {
		return false
	}
	if q.Operation == "claim" {
		decoded, err := hex.DecodeString(r.RunToken)
		return err == nil && len(decoded) == 32 && d.ProfileID == q.ProfileID && r.ReservationID == "" && len(r.Placements) == 0 && !r.Reaped && r.PrepareDeadlineUnixMS == 0
	}
	if r.RunID != q.RunID || r.RunToken != "" {
		return false
	}
	switch q.Operation {
	case "diagnostics":
		return d.PreparationEnabled && !r.Reaped && r.ReservationID == "" && r.HostID == q.HostID && r.SlotID == "" && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0 && r.Diagnostics != nil && javarunner.ValidHostDiagnostics(*r.Diagnostics)
	case "invoke":
		return d.InvocationEnabled && !r.Reaped && r.ReservationID == q.ReservationID && r.HostID == q.HostID && r.SlotID == q.SlotID && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0 && validPooledInvocationResult(q, r.Invocation)
	case "commit":
		return d.CommitEnabled && r.Committed && !r.Reaped && r.ReservationID == q.ReservationID && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0
	case "close_slot":
		return d.CommitEnabled && r.SlotClosed && !r.Reaped && r.ReservationID == q.ReservationID && r.HostID == q.HostID && r.SlotID == q.SlotID && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0
	case "prepare":
		return d.PreparationEnabled && r.Ready && !r.Reaped && r.ReservationID == q.ReservationID && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS > 0
	case "close":
		return r.Reaped && r.ReservationID == "" && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0
	case "renew":
		return !r.Reaped && r.ReservationID == "" && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0
	case "abort":
		return !r.Reaped && r.ReservationID == q.ReservationID && len(r.Placements) == 0 && r.PrepareDeadlineUnixMS == 0
	case "reserve":
		if r.Reaped || !executionIDPattern.MatchString(r.ReservationID) || r.PrepareDeadlineUnixMS <= 0 || len(r.Placements) != (q.VUsers-1)/d.SlotsPerHost+1 || len(r.Placements) > d.MaxHosts || q.VUsers > d.MaxVUsers || int64(len(r.Placements)) > d.MaxMemoryBytes/d.HostMemoryBytes {
			return false
		}
		ids := map[string]bool{r.ReservationID: true, r.RunID: true}
		count := 0
		for _, p := range r.Placements {
			if !executionIDPattern.MatchString(p.HostID) || ids[p.HostID] || len(p.SlotIDs) == 0 || len(p.SlotIDs) > d.SlotsPerHost || p.MemoryBytes != d.HostMemoryBytes {
				return false
			}
			ids[p.HostID] = true
			for _, slot := range p.SlotIDs {
				if !executionIDPattern.MatchString(slot) || ids[slot] {
					return false
				}
				ids[slot] = true
				count++
			}
		}
		return count == q.VUsers
	default:
		return false
	}
}
