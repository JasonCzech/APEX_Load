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
		Contract: ContractVersion,
		OwnerID:  ownerID,
	}, &response)
	if err != nil {
		return ClaimOwnerResponse{}, err
	}
	if response.OwnerID != ownerID || !executionIDPattern.MatchString(response.Epoch) || !response.Claimed {
		return ClaimOwnerResponse{}, requestFailure("Java runner sidecar returned an invalid owner claim", true)
	}
	c.ownerMu.Lock()
	c.ownerID = ownerID
	c.ownerEpoch = response.Epoch
	c.ownerMu.Unlock()
	return response, nil
}

func (c *Client) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResponse, error) {
	request.Contract = ContractVersion
	c.ownerMu.RLock()
	request.OwnerID = c.ownerID
	request.OwnerEpoch = c.ownerEpoch
	c.ownerMu.RUnlock()
	var response ExecutionResponse
	err := c.do(ctx, http.MethodPost, "/v1/executions", request, &response)
	return response, err
}

func (c *Client) Cancel(ctx context.Context, executionID string) (CancelResponse, error) {
	if !executionIDPattern.MatchString(executionID) {
		return CancelResponse{}, fmt.Errorf("Java runner sidecar execution ID is invalid")
	}
	var response CancelResponse
	c.ownerMu.RLock()
	ownerID, ownerEpoch := c.ownerID, c.ownerEpoch
	c.ownerMu.RUnlock()
	err := c.do(ctx, http.MethodPost, "/v1/executions/"+url.PathEscape(executionID)+"/cancel", CancelRequest{
		Contract: ContractVersion, OwnerID: ownerID, OwnerEpoch: ownerEpoch,
	}, &response)
	return response, err
}

func (c *Client) CloseSession(ctx context.Context, sessionID string) (CloseSessionResponse, error) {
	if !executionIDPattern.MatchString(sessionID) {
		return CloseSessionResponse{}, fmt.Errorf("Java runner sidecar session ID is invalid")
	}
	c.ownerMu.RLock()
	ownerID, ownerEpoch := c.ownerID, c.ownerEpoch
	c.ownerMu.RUnlock()
	var response CloseSessionResponse
	err := c.do(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(sessionID)+"/close", CloseSessionRequest{
		Contract:   ContractVersion,
		OwnerID:    ownerID,
		OwnerEpoch: ownerEpoch,
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
	case *ClaimOwnerResponse:
		return value.Contract, nil
	case *ExecutionResponse:
		return value.Contract, nil
	case *CancelResponse:
		return value.Contract, nil
	case *CloseSessionResponse:
		return value.Contract, nil
	default:
		return "", fmt.Errorf("unsupported response type")
	}
}
