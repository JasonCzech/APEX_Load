package javasidecar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

const maxResponseHeaderBytes = 64 << 10

type RemoteError struct {
	StatusCode int
	Code       string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("Java runner sidecar request failed (status=%d code=%s)", e.StatusCode, e.Code)
}

// requestOutcomeError records whether a request failure happened after the
// sidecar might have accepted an execution. Callers must permanently poison a
// stateful VUser session after an ambiguous outcome: retrying could otherwise
// create a fresh Actions object after the previous invocation already ran.
type requestOutcomeError struct {
	message   string
	ambiguous bool
}

func (e *requestOutcomeError) Error() string { return e.message }

func (e *requestOutcomeError) executionOutcomeAmbiguous() bool { return e.ambiguous }

func requestFailure(message string, ambiguous bool) error {
	return &requestOutcomeError{message: message, ambiguous: ambiguous}
}

// ExecutionOutcomeAmbiguous reports whether an Execute error could have
// occurred after the sidecar accepted the invocation. Valid RemoteError
// responses and locally rejected requests are authoritative. Unknown errors
// (including test doubles and alternate clients) fail closed.
func ExecutionOutcomeAmbiguous(err error) bool {
	if err == nil {
		return false
	}
	var remote *RemoteError
	if errors.As(err, &remote) {
		return false
	}
	var classified interface {
		executionOutcomeAmbiguous() bool
	}
	if errors.As(err, &classified) {
		return classified.executionOutcomeAmbiguous()
	}
	return true
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	authToken  string
	ownerMu    sync.RWMutex
	ownerID    string
	ownerEpoch string
	leaseMu    sync.RWMutex
	leaseID    string
	leaseToken string
}

// NewClient creates a loopback-only, redirect-refusing sidecar client. The
// supplied client's cookie jar is intentionally not reused, preventing ambient
// loopback cookies from being sent to the runner.
func NewClient(rawURL string, base *http.Client) (*Client, error) {
	return NewClientWithToken(rawURL, "", base)
}

func NewClientWithToken(rawURL, authToken string, base *http.Client) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse Java runner sidecar URL: %w", err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("Java runner sidecar URL must be a loopback HTTP origin")
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return nil, fmt.Errorf("Java runner sidecar URL must use a loopback host")
	}
	parsed.Path = ""
	if base == nil {
		base = &http.Client{}
	}
	clientCopy := *base
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if transport, ok := base.Transport.(*http.Transport); ok {
		transportCopy := transport.Clone()
		transportCopy.MaxResponseHeaderBytes = maxResponseHeaderBytes
		clientCopy.Transport = transportCopy
	} else if base.Transport == nil {
		transportCopy := http.DefaultTransport.(*http.Transport).Clone()
		transportCopy.MaxResponseHeaderBytes = maxResponseHeaderBytes
		clientCopy.Transport = transportCopy
	}
	return &Client{
		baseURL:    parsed,
		httpClient: &clientCopy,
		authToken:  strings.TrimSpace(authToken),
	}, nil
}

func (c *Client) Health(ctx context.Context) (HealthResponse, error) {
	var response HealthResponse
	err := c.do(ctx, http.MethodGet, "/v1/health", nil, &response)
	return response, err
}

func (c *Client) Capabilities(ctx context.Context) (CapabilitiesResponse, error) {
	var response CapabilitiesResponse
	err := c.do(ctx, http.MethodGet, "/v1/capabilities", nil, &response)
	return response, err
}

func (c *Client) Validate(ctx context.Context, request ValidateRequest) (ValidateResponse, error) {
	request.Contract = ContractVersion
	var response ValidateResponse
	err := c.do(ctx, http.MethodPost, "/v1/validate", request, &response)
	return response, err
}

func (c *Client) ClaimOwner(ctx context.Context, ownerID string) (ClaimOwnerResponse, error) {
	ownerID = strings.TrimSpace(ownerID)
	if !executionIDPattern.MatchString(ownerID) {
		return ClaimOwnerResponse{}, fmt.Errorf("Java runner sidecar owner ID is invalid")
	}
	var response ClaimOwnerResponse
	err := c.do(ctx, http.MethodPost, "/v1/owner/claim", ClaimOwnerRequest{
		Contract:   ContractVersion,
		OwnerID:    ownerID,
		LeaseTTLMS: RunnerOwnerLeaseTTL.Milliseconds(),
	}, &response)
	if err != nil {
		return ClaimOwnerResponse{}, err
	}
	if response.OwnerID != ownerID || !executionIDPattern.MatchString(response.Epoch) || !response.Claimed || response.LeaseTTLMS != RunnerOwnerLeaseTTL.Milliseconds() {
		return ClaimOwnerResponse{}, requestFailure("Java runner sidecar returned an invalid owner claim", true)
	}
	c.ownerMu.Lock()
	c.ownerID = ownerID
	c.ownerEpoch = response.Epoch
	c.ownerMu.Unlock()
	return response, nil
}

func (c *Client) ClaimRuntimeLease(ctx context.Context) (ClaimRuntimeLeaseResponse, error) {
	c.ownerMu.RLock()
	ownerID, ownerEpoch := c.ownerID, c.ownerEpoch
	c.ownerMu.RUnlock()
	if ownerID == "" || ownerEpoch == "" {
		return ClaimRuntimeLeaseResponse{}, fmt.Errorf("Java runner sidecar owner is not claimed")
	}
	var response ClaimRuntimeLeaseResponse
	err := c.do(ctx, http.MethodPost, "/v1/runtime-leases/claim", ClaimRuntimeLeaseRequest{
		Contract: ContractVersion, OwnerID: ownerID, OwnerEpoch: ownerEpoch,
		LeaseTTLMS: RuntimeLeaseTTL.Milliseconds(),
	}, &response)
	if err != nil {
		return ClaimRuntimeLeaseResponse{}, err
	}
	if !response.Claimed || !executionIDPattern.MatchString(response.LeaseID) ||
		len(response.LeaseToken) != 64 || response.LeaseTTLMS != RuntimeLeaseTTL.Milliseconds() {
		return ClaimRuntimeLeaseResponse{}, requestFailure("Java runner sidecar returned an invalid runtime lease", true)
	}
	c.leaseMu.Lock()
	c.leaseID, c.leaseToken = response.LeaseID, response.LeaseToken
	c.leaseMu.Unlock()
	return response, nil
}

func (c *Client) RenewRuntimeLease(ctx context.Context) (RenewRuntimeLeaseResponse, error) {
	ownerID, ownerEpoch, leaseID, leaseToken := c.ownerAndRuntimeLease()
	if leaseID == "" || leaseToken == "" {
		return RenewRuntimeLeaseResponse{}, fmt.Errorf("Java runner sidecar runtime lease is not claimed")
	}
	var response RenewRuntimeLeaseResponse
	err := c.do(ctx, http.MethodPost, "/v1/runtime-leases/"+url.PathEscape(leaseID)+"/renew", RenewRuntimeLeaseRequest{
		Contract: ContractVersion, OwnerID: ownerID, OwnerEpoch: ownerEpoch, LeaseToken: leaseToken,
	}, &response)
	if err != nil {
		return RenewRuntimeLeaseResponse{}, err
	}
	if !response.Renewed || response.LeaseID != leaseID || response.LeaseTTLMS != RuntimeLeaseTTL.Milliseconds() {
		return RenewRuntimeLeaseResponse{}, requestFailure("Java runner sidecar returned an invalid runtime lease renewal", true)
	}
	return response, nil
}

// RevokeRuntimeLease returns only after the sidecar has positively reaped all
// work bound to this lease. The credentials are retained on every failure so a
// caller can keep retrying an ambiguous response without creating a new lease.
func (c *Client) RevokeRuntimeLease(ctx context.Context) (RevokeRuntimeLeaseResponse, error) {
	ownerID, ownerEpoch, leaseID, leaseToken := c.ownerAndRuntimeLease()
	if leaseID == "" || leaseToken == "" {
		return RevokeRuntimeLeaseResponse{}, fmt.Errorf("Java runner sidecar runtime lease is not claimed")
	}
	var response RevokeRuntimeLeaseResponse
	err := c.do(ctx, http.MethodPost, "/v1/runtime-leases/"+url.PathEscape(leaseID)+"/revoke", RevokeRuntimeLeaseRequest{
		Contract: ContractVersion, OwnerID: ownerID, OwnerEpoch: ownerEpoch, LeaseToken: leaseToken,
	}, &response)
	if err != nil {
		return RevokeRuntimeLeaseResponse{}, err
	}
	if !response.Revoked || !response.Reaped || response.LeaseID != leaseID {
		return RevokeRuntimeLeaseResponse{}, requestFailure("Java runner sidecar did not prove runtime cleanup", true)
	}
	c.leaseMu.Lock()
	if c.leaseID == leaseID && c.leaseToken == leaseToken {
		c.leaseID, c.leaseToken = "", ""
	}
	c.leaseMu.Unlock()
	return response, nil
}

func (c *Client) ownerAndRuntimeLease() (string, string, string, string) {
	c.ownerMu.RLock()
	ownerID, ownerEpoch := c.ownerID, c.ownerEpoch
	c.ownerMu.RUnlock()
	c.leaseMu.RLock()
	leaseID, leaseToken := c.leaseID, c.leaseToken
	c.leaseMu.RUnlock()
	return ownerID, ownerEpoch, leaseID, leaseToken
}

func (c *Client) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResponse, error) {
	request.Contract = ContractVersion
	request.OwnerID, request.OwnerEpoch, request.RuntimeLeaseID, request.RuntimeLeaseToken = c.ownerAndRuntimeLease()
	var response ExecutionResponse
	err := c.do(ctx, http.MethodPost, "/v1/executions", request, &response)
	return response, err
}

func (c *Client) Cancel(ctx context.Context, executionID string) (CancelResponse, error) {
	if !executionIDPattern.MatchString(executionID) {
		return CancelResponse{}, fmt.Errorf("Java runner sidecar execution ID is invalid")
	}
	var response CancelResponse
	ownerID, ownerEpoch, leaseID, leaseToken := c.ownerAndRuntimeLease()
	err := c.do(ctx, http.MethodPost, "/v1/executions/"+url.PathEscape(executionID)+"/cancel", CancelRequest{
		Contract: ContractVersion, OwnerID: ownerID, OwnerEpoch: ownerEpoch,
		RuntimeLeaseID: leaseID, RuntimeLeaseToken: leaseToken,
	}, &response)
	return response, err
}

func (c *Client) CloseSession(ctx context.Context, sessionID string) (CloseSessionResponse, error) {
	if !executionIDPattern.MatchString(sessionID) {
		return CloseSessionResponse{}, fmt.Errorf("Java runner sidecar session ID is invalid")
	}
	ownerID, ownerEpoch, leaseID, leaseToken := c.ownerAndRuntimeLease()
	var response CloseSessionResponse
	err := c.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/close", CloseSessionRequest{
		Contract:          ContractVersion,
		OwnerID:           ownerID,
		OwnerEpoch:        ownerEpoch,
		RuntimeLeaseID:    leaseID,
		RuntimeLeaseToken: leaseToken,
	}, &response)
	return response, err
}

func (c *Client) do(ctx context.Context, method, path string, payload any, destination any) error {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return requestFailure("encode Java runner sidecar request", false)
		}
		if int64(len(encoded)) > MaxRequestBytes {
			return requestFailure("Java runner sidecar request exceeds the size limit", false)
		}
		body = bytes.NewReader(encoded)
	}
	endpoint := *c.baseURL
	endpoint.Path = path
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return requestFailure("create Java runner sidecar request", false)
	}
	request.Header.Set("Accept", "application/json")
	if c.authToken != "" {
		request.Header.Set("X-Apexload-Java-Runner-Token", c.authToken)
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return requestFailure("Java runner sidecar is unavailable", true)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return requestFailure("read Java runner sidecar response", true)
	}
	if int64(len(data)) > MaxResponseBytes {
		return requestFailure("Java runner sidecar response exceeds the size limit", true)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var remote ErrorResponse
		if json.Unmarshal(data, &remote) != nil || remote.Contract != ContractVersion || remote.ErrorCode == "" {
			return requestFailure("Java runner sidecar returned an invalid error response", true)
		}
		return &RemoteError{StatusCode: response.StatusCode, Code: remote.ErrorCode}
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return requestFailure("decode Java runner sidecar response", true)
	}
	contract, err := responseContract(destination)
	if err != nil || contract != ContractVersion {
		return requestFailure("Java runner sidecar contract mismatch", true)
	}
	return nil
}

func responseContract(response any) (string, error) {
	switch value := response.(type) {
	case *HealthResponse:
		return value.Contract, nil
	case *CapabilitiesResponse:
		return value.Contract, nil
	case *ValidateResponse:
		return value.Contract, nil
	case *RenewOwnerResponse:
		return value.Contract, nil
	case *ClaimOwnerResponse:
		return value.Contract, nil
	case *ClaimRuntimeLeaseResponse:
		return value.Contract, nil
	case *RenewRuntimeLeaseResponse:
		return value.Contract, nil
	case *RevokeRuntimeLeaseResponse:
		return value.Contract, nil
	case *ExecutionResponse:
		return value.Contract, nil
	case *CancelResponse:
		return value.Contract, nil
	case *CloseSessionResponse:
		return value.Contract, nil
	case *PooledControlResponse:
		return value.Contract, nil
	default:
		return "", fmt.Errorf("unsupported response type")
	}
}
