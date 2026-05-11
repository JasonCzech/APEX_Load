// Package protocol defines the interface for load testing protocols and
// provides implementations for HTTP, JMS (Solace), and JMS (IBM MQ).
//
// All protocol implementations must satisfy the Protocol interface.
// New protocols can be added by:
//  1. Creating a pkg/protocol/<name> package
//  2. Implementing the Protocol interface
//  3. Registering in registry.go
//
// Example custom protocol registration:
//
//	// pkg/protocol/myproto/myproto.go
//	type MyProtocol struct {}
//	func (p *MyProtocol) Name() string { return "myproto" }
//	// ... implement Execute, Init, etc.
//
//	// pkg/protocol/registry.go
//	RegisterProtocol(new(myproto.MyProtocol))
package protocol

import (
	"context"
	"time"
)

// Protocol defines the interface that all load testing protocols must implement.
//
// A protocol is responsible for:
//   - Initialization with protocol-specific config (TLS, timeouts, auth, etc.)
//   - Executing actions (requests/operations) against the target system
//   - Extracting values from responses for templating and assertions
//   - Evaluating assertions to determine pass/fail
//   - Cleanup and resource release
type Protocol interface {
	// Name returns the protocol identifier (e.g., "http", "jms-solace").
	Name() string

	// Init initializes the protocol with the provided config.
	// Config keys vary by protocol (e.g., "base_url" for HTTP, "broker_url" for JMS).
	// Called once per VUser during engine initialization.
	Init(ctx context.Context, config map[string]any) error

	// Execute runs a single action (request) and returns the result.
	// Called repeatedly during test execution.
	Execute(ctx context.Context, action *Action) (*Result, error)

	// Teardown releases protocol resources (connections, sessions, etc.).
	// Called once per VUser during test shutdown.
	Teardown(ctx context.Context) error

	// HealthCheck verifies the protocol can reach the target system.
	// Used for pre-test validation and monitoring.
	HealthCheck(ctx context.Context) error
}

// Action represents a single operation (HTTP request, message, etc.) to execute.
type Action struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Method     string            `json:"method,omitempty"`
	Target     string            `json:"target"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       []byte            `json:"body,omitempty"`
	Params     map[string]any    `json:"params,omitempty"`
	Extractors []Extractor       `json:"extractors,omitempty"`
	Assertions []Assertion       `json:"assertions,omitempty"`
	ThinkTime  ThinkTimeConfig   `json:"think_time,omitempty"`
}

// Extractor extracts a value from the response for use in later templates or variables.
//
// Example: Extract session token from JSON response for use in subsequent requests.
type Extractor struct {
	Name       string `json:"name"`       // Variable name
	Source     string `json:"source"`     // "body", "header", etc.
	Type       string `json:"type"`       // "json", "xpath", "regex", etc.
	Expression string `json:"expression"` // JSONPath, XPath, regex pattern, etc.
}

// Assertion validates a response matches expected conditions.
//
// Example: Assert response status is 200 and body contains "success".
type Assertion struct {
	Type     string `json:"type"`               // "status", "header", "body", "json", etc.
	Expected any    `json:"expected,omitempty"` // Expected value
	Value    string `json:"value,omitempty"`    // Field to check (JSONPath, header name, etc.)
	Max      int    `json:"max,omitempty"`      // Max threshold for numeric assertions
}

// ThinkTimeConfig defines think time (delay) after an action.
//
// Think time simulates user reading time and is applied after each action
// to create more realistic traffic patterns. Supports fixed, random, and gaussian distributions.
type ThinkTimeConfig struct {
	Type       string  `json:"type"`                 // "fixed", "random", "gaussian"
	MeanMs     float64 `json:"mean_ms"`              // Mean value in milliseconds
	StdMs      float64 `json:"std_ms,omitempty"`     // Standard deviation for gaussian
	MinMs      float64 `json:"min_ms,omitempty"`     // Min for random/gaussian
	MaxMs      float64 `json:"max_ms,omitempty"`     // Max for random/gaussian
	Multiplier float64 `json:"multiplier,omitempty"` // Scalar multiplier
	LimitMs    float64 `json:"limit_ms,omitempty"`   // Upper limit cap
	Ignore     bool    `json:"ignore,omitempty"`     // Skip think time for this action
}

// Result contains the outcome of a single action execution.
type Result struct {
	ActionName    string        // Name of the executed action
	Group         string        // Group/transaction name for metrics
	StartTime     time.Time     // When the action started
	Duration      time.Duration // Time to execute action
	DNSTime       time.Duration // DNS lookup time (if applicable)
	ConnectTime   time.Duration // Connection establishment time (if applicable)
	TLSTime       time.Duration
	TTFB          time.Duration
	BytesSent     int64
	BytesReceived int64
	StatusCode    int
	Success       bool
	ErrorMessage  string
	Extractions   map[string]string
	Snapshot      *ErrorSnapshot // populated when snapshot_on_error is enabled
}

// ErrorSnapshot captures full request/response data for post-test debugging.
type ErrorSnapshot struct {
	RequestMethod   string            `json:"request_method"`
	RequestURL      string            `json:"request_url"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	ResponseStatus  int               `json:"response_status"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
}
