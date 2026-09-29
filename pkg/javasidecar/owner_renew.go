package javasidecar

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// RenewOwnerRequest never claims or replaces a generation. A stale keeper must
// not use ClaimOwner, which deliberately permits operator-authorized takeover.
type RenewOwnerRequest struct {
	Contract   string `json:"contract"`
	OwnerID    string `json:"owner_id"`
	OwnerEpoch string `json:"owner_epoch"`
}
type RenewOwnerResponse struct {
	Contract   string `json:"contract"`
	OwnerID    string `json:"owner_id"`
	OwnerEpoch string `json:"owner_epoch"`
	Renewed    bool   `json:"renewed"`
	LeaseTTLMS int64  `json:"lease_ttl_ms"`
}

func (s *Server) handleRenewOwner(w http.ResponseWriter, r *http.Request) {
	var q RenewOwnerRequest
	if !decodeRequest(w, r, &q) {
		return
	}
	if q.Contract != ContractVersion {
		writeError(w, http.StatusPreconditionFailed, "contract_version_mismatch")
		return
	}
	if !executionIDPattern.MatchString(q.OwnerID) || !executionIDPattern.MatchString(q.OwnerEpoch) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.ownerMu.Lock()
	if s.draining.Load() {
		s.ownerMu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "server_draining")
		return
	}
	owner, code := s.currentOwnerLocked(q.OwnerID, q.OwnerEpoch)
	if code != "" {
		s.ownerMu.Unlock()
		writeError(w, ownerErrorStatus(code), code)
		return
	}
	s.resetOwnerLeaseLocked(owner, time.Now())
	s.ownerMu.Unlock()
	writeJSON(w, http.StatusOK, RenewOwnerResponse{Contract: ContractVersion, OwnerID: q.OwnerID, OwnerEpoch: q.OwnerEpoch, Renewed: true, LeaseTTLMS: RunnerOwnerLeaseTTL.Milliseconds()})
}

func (c *Client) RenewOwner(ctx context.Context) error {
	owner, epoch, _, _ := c.ownerAndRuntimeLease()
	if !executionIDPattern.MatchString(owner) || !executionIDPattern.MatchString(epoch) {
		return requestFailure("Java runner owner is not claimed", false)
	}
	var response RenewOwnerResponse
	if err := c.do(ctx, http.MethodPost, "/v1/owner/renew", RenewOwnerRequest{Contract: ContractVersion, OwnerID: owner, OwnerEpoch: epoch}, &response); err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) {
			switch remote.Code {
			case "owner_fenced", "owner_not_claimed", "owner_claim_in_progress", "server_draining", "runner_disabled", "runner_unauthorized", "runner_auth_unavailable", "contract_version_mismatch", "invalid_request", "method_not_allowed":
			default:
				return requestFailure("invalid Java runner owner renewal response", true)
			}
		}
		return err
	}
	if response.OwnerID != owner || response.OwnerEpoch != epoch || !response.Renewed || response.LeaseTTLMS != RunnerOwnerLeaseTTL.Milliseconds() {
		return requestFailure("invalid Java runner owner renewal receipt", true)
	}
	return nil
}

// RenewOwner uses the run's frozen epoch, even if the original client has since
// claimed another owner. It remains independent of run cancellation so the
// generator can keep its epoch alive while waiting for containment cleanup.
func (r *PooledClientRun) RenewOwner(ctx context.Context) error { return r.client.RenewOwner(ctx) }
