package jms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/subprocessguard"
)

// sidecarAdapter isolates the external Solace bridge from the provider-neutral
// protocol dispatcher. The command is operator-owned; scripts can select the
// adapter but cannot supply a process command.
type sidecarAdapter struct {
	command string
	cfg     config
	limiter *sidecarProcessLimiter
}

type sidecarRequest struct {
	Operation        string         `json:"operation"`
	Provider         string         `json:"provider,omitempty"`
	Config           config         `json:"config,omitempty"`
	Destination      string         `json:"destination,omitempty"`
	ReplyDestination string         `json:"reply_destination,omitempty"`
	TimeoutMs        int64          `json:"timeout_ms,omitempty"`
	Message          message        `json:"message,omitempty"`
	Messages         []message      `json:"messages,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

type sidecarResponse struct {
	Message  message `json:"message,omitempty"`
	Request  message `json:"request,omitempty"`
	Response message `json:"response,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func (a sidecarAdapter) Publish(ctx context.Context, destination string, msg message) (message, error) {
	resp, err := a.run(ctx, sidecarRequest{
		Operation:   "publish",
		Provider:    "solace",
		Config:      a.cfg,
		Destination: destination,
		TimeoutMs:   durationMilliseconds(a.cfg.Timeout),
		Message:     msg,
	}, a.cfg.Timeout)
	if err != nil {
		return message{}, err
	}
	if resp.Message.ID == "" && len(resp.Message.Body) == 0 {
		return msg, nil
	}
	return normalizeSidecarMessage(resp.Message, msg), nil
}

func (a sidecarAdapter) Consume(ctx context.Context, destination string, timeout time.Duration) (message, error) {
	resp, err := a.run(ctx, sidecarRequest{
		Operation:   "consume",
		Provider:    "solace",
		Config:      a.cfg,
		Destination: destination,
		TimeoutMs:   durationMilliseconds(timeout),
	}, timeout)
	if err != nil {
		return message{}, err
	}
	if resp.Message.ID == "" && len(resp.Message.Body) == 0 {
		return message{}, fmt.Errorf("jms sidecar consume returned no message")
	}
	return normalizeSidecarMessage(resp.Message, message{Destination: destination, CreatedAt: time.Now()}), nil
}

func (a sidecarAdapter) RequestReply(ctx context.Context, requestDestination string, replyDestination string, request message, timeout time.Duration) (message, message, error) {
	resp, err := a.run(ctx, sidecarRequest{
		Operation:        "request_reply",
		Provider:         "solace",
		Config:           a.cfg,
		Destination:      requestDestination,
		ReplyDestination: replyDestination,
		TimeoutMs:        durationMilliseconds(timeout),
		Message:          request,
	}, timeout)
	if err != nil {
		return message{}, message{}, err
	}
	sent := resp.Request
	if sent.ID == "" && len(sent.Body) == 0 {
		sent = request
	}
	response := resp.Response
	if response.ID == "" && len(response.Body) == 0 {
		response = resp.Message
	}
	if response.ID == "" && len(response.Body) == 0 {
		return message{}, message{}, fmt.Errorf("jms sidecar request_reply returned no response message")
	}
	return normalizeSidecarMessage(sent, request), normalizeSidecarMessage(response, message{
		CorrelationID: request.CorrelationID,
		Destination:   replyDestination,
		CreatedAt:     time.Now(),
	}), nil
}

func (a sidecarAdapter) HealthCheck(ctx context.Context) error {
	_, err := a.run(ctx, sidecarRequest{
		Operation: "health",
		Provider:  "solace",
		Config:    a.cfg,
		TimeoutMs: durationMilliseconds(a.cfg.Timeout),
	}, a.cfg.Timeout)
	return err
}

func (a sidecarAdapter) Teardown(ctx context.Context) error {
	_, err := a.run(ctx, sidecarRequest{
		Operation: "teardown",
		Provider:  "solace",
		Config:    a.cfg,
		TimeoutMs: durationMilliseconds(a.cfg.Timeout),
	}, a.cfg.Timeout)
	return err
}

func (a sidecarAdapter) run(ctx context.Context, req sidecarRequest, timeout time.Duration) (sidecarResponse, error) {
	fields := strings.Fields(a.command)
	if len(fields) == 0 {
		return sidecarResponse{}, fmt.Errorf("jms sidecar command is not configured")
	}
	if err := subprocessguard.ValidateAmbientCredentials(); err != nil {
		return sidecarResponse{}, fmt.Errorf("JMS sidecar isolation check failed: %w", err)
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
	limiter := a.limiter
	if limiter == nil {
		limiter = globalSidecarProcessLimiter
	}
	release, err := limiter.acquire(runCtx)
	if err != nil {
		return sidecarResponse{}, fmt.Errorf("wait for JMS sidecar process slot: %w", err)
	}
	defer release()

	payload, err := json.Marshal(req)
	if err != nil {
		return sidecarResponse{}, fmt.Errorf("marshal jms sidecar request: %w", err)
	}
	cmd := exec.CommandContext(runCtx, fields[0], fields[1:]...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Env = subprocessguard.Environment(nil)
	cmd.Stdin = bytes.NewReader(payload)
	stdout := &sidecarCappedBuffer{max: maxSidecarCaptureBytes, onOverflow: cancel}
	stderr := &sidecarCappedBuffer{max: maxSidecarCaptureBytes, onOverflow: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	if stdout.truncated {
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s stdout exceeded %d bytes", req.Operation, maxSidecarCaptureBytes)
	}
	if stderr.truncated {
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s stderr exceeded %d bytes", req.Operation, maxSidecarCaptureBytes)
	}
	if err != nil {
		if runCtx.Err() != nil {
			return sidecarResponse{}, fmt.Errorf("jms sidecar %s timed out: %w", req.Operation, runCtx.Err())
		}
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s failed: %w", req.Operation, err)
	}
	out := stdout.buf.Bytes()
	if len(bytes.TrimSpace(out)) == 0 {
		return sidecarResponse{}, nil
	}
	var resp sidecarResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return sidecarResponse{}, fmt.Errorf("decode jms sidecar %s response: %w", req.Operation, err)
	}
	if strings.TrimSpace(resp.Error) != "" {
		return sidecarResponse{}, fmt.Errorf("jms sidecar %s rejected request: %s", req.Operation, resp.Error)
	}
	return resp, nil
}

type sidecarCappedBuffer struct {
	buf          bytes.Buffer
	max          int
	truncated    bool
	onOverflow   func()
	overflowOnce sync.Once
}

func (b *sidecarCappedBuffer) Write(content []byte) (int, error) {
	if remaining := b.max - b.buf.Len(); remaining > 0 {
		if len(content) > remaining {
			_, _ = b.buf.Write(content[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(content)
		}
	} else if len(content) > 0 {
		b.truncated = true
	}
	if b.truncated && b.onOverflow != nil {
		b.overflowOnce.Do(b.onOverflow)
	}
	return len(content), nil
}

func normalizeSidecarMessage(msg message, fallback message) message {
	if msg.ID == "" {
		msg.ID = fallback.ID
	}
	if msg.CorrelationID == "" {
		msg.CorrelationID = fallback.CorrelationID
	}
	if msg.Destination == "" {
		msg.Destination = fallback.Destination
	}
	if msg.Headers == nil {
		msg.Headers = cloneStringMap(fallback.Headers)
	}
	if msg.Properties == nil {
		msg.Properties = cloneStringMap(fallback.Properties)
	}
	if len(msg.Body) == 0 {
		msg.Body = fallback.Body
	}
	if msg.CreatedAt.IsZero() {
		if fallback.CreatedAt.IsZero() {
			msg.CreatedAt = time.Now()
		} else {
			msg.CreatedAt = fallback.CreatedAt
		}
	}
	return msg
}

func durationMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}
