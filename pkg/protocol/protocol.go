// Package protocol defines the interface for load testing protocols and
// provides implementations for HTTP and JMS (Solace).
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
	"strings"
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

// CookieController is an optional capability for protocols that maintain a
// per-VUser cookie store. The engine invokes ClearCookies when it dispatches a
// cookie_clear action (imported from web_cleanup_cookies), discarding every
// cookie the VUser has accumulated so far. Protocols that do not implement this
// interface treat cookie_clear as a no-op, which is the correct behavior for
// protocols that keep no cookies.
type CookieController interface {
	// ClearCookies discards all cookies currently held by the VUser session.
	ClearCookies(ctx context.Context) error
}

// Action represents a single operation (HTTP request, message, etc.) to execute.
type HeaderEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type HeaderList []HeaderEntry

func (headers HeaderList) ToMap() map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for _, header := range headers {
		if strings.TrimSpace(header.Name) == "" {
			continue
		}
		out[header.Name] = header.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type Action struct {
	Name               string                 `json:"name"`
	Type               string                 `json:"type"`
	Method             string                 `json:"method,omitempty"`
	Target             string                 `json:"target"`
	RedactedTarget     string                 `json:"-"`
	Selector           string                 `json:"selector,omitempty"`
	Value              string                 `json:"value,omitempty"`
	Headers            map[string]string      `json:"headers,omitempty"`
	HeaderList         HeaderList             `json:"header_list,omitempty"`
	RedactedHeaders    map[string]string      `json:"-"`
	RedactedHeaderList HeaderList             `json:"-"`
	Body               []byte                 `json:"body,omitempty"`
	BodyEncoding       string                 `json:"body_encoding,omitempty"`
	RedactedBody       []byte                 `json:"-"`
	Params             map[string]any         `json:"params,omitempty"`
	RedactedParams     map[string]any         `json:"-"`
	WaitFor            *WaitCondition         `json:"wait_for,omitempty"`
	Sync               *BrowserSyncPolicy     `json:"sync,omitempty"`
	Capture            *BrowserCaptureOptions `json:"capture,omitempty"`
	Metadata           map[string]any         `json:"metadata,omitempty"`
	Extractors         []Extractor            `json:"extractors,omitempty"`
	Assertions         []Assertion            `json:"assertions,omitempty"`
	ThinkTime          ThinkTimeConfig        `json:"think_time,omitempty"`
	Auth               *AuthConfig            `json:"auth,omitempty"`
	RedactedAuth       *AuthConfig            `json:"-"`
	TimeoutMs          int                    `json:"timeout_ms,omitempty"`
	MaxRedirects       *int                   `json:"max_redirects,omitempty"`
	SOAP               *SOAPActionConfig      `json:"soap,omitempty"`
	RedactedSOAP       *SOAPActionConfig      `json:"-"`
}

type AuthConfig struct {
	Type     string `json:"type,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Domain   string `json:"domain,omitempty"`
}

type SOAPActionConfig struct {
	Action    string `json:"action,omitempty"`
	Operation string `json:"operation,omitempty"`
	BodyMode  string `json:"body_mode,omitempty"`
}

type WaitCondition struct {
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

type BrowserSyncPolicy struct {
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
	StableMs  int    `json:"stable_ms,omitempty"`
}

type BrowserCaptureOptions struct {
	Screenshot bool `json:"screenshot,omitempty"`
	FullPage   bool `json:"full_page,omitempty"`
	HTML       bool `json:"html,omitempty"`
}

type ConsoleEntry struct {
	Level     string    `json:"level,omitempty"`
	Text      string    `json:"text,omitempty"`
	Source    string    `json:"source,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type NetworkEntry struct {
	URL                  string            `json:"url,omitempty"`
	Method               string            `json:"method,omitempty"`
	StatusCode           int               `json:"status_code,omitempty"`
	ResourceType         string            `json:"resource_type,omitempty"`
	DurationMs           float64           `json:"duration_ms,omitempty"`
	BytesReceived        int64             `json:"bytes_received,omitempty"`
	RequestHeaders       map[string]string `json:"request_headers,omitempty"`
	RequestBody          string            `json:"request_body,omitempty"`
	RequestBodyEncoding  string            `json:"request_body_encoding,omitempty"`
	RequestBodyTruncated bool              `json:"request_body_truncated,omitempty"`
	ResponseHeaders      map[string]string `json:"response_headers,omitempty"`
}

func CloneNetworkEntries(entries []NetworkEntry) []NetworkEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]NetworkEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = entry
		cloned[i].RequestHeaders = cloneStringMap(entry.RequestHeaders)
		cloned[i].ResponseHeaders = cloneStringMap(entry.ResponseHeaders)
	}
	return cloned
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

type DOMSummary struct {
	Title       string   `json:"title,omitempty"`
	ReadyState  string   `json:"ready_state,omitempty"`
	VisibleText []string `json:"visible_text,omitempty"`
}

type ClientMetrics struct {
	DeviceProfile         string  `json:"device_profile,omitempty"`
	DevicePlatform        string  `json:"device_platform,omitempty"`
	DeviceTier            string  `json:"device_tier,omitempty"`
	BrowserEngine         string  `json:"browser_engine,omitempty"`
	ActionDurationMs      float64 `json:"action_duration_ms,omitempty"`
	FCPMs                 float64 `json:"fcp_ms,omitempty"`
	LCPMs                 float64 `json:"lcp_ms,omitempty"`
	CLSScore              float64 `json:"cls_score,omitempty"`
	INPMs                 float64 `json:"inp_ms,omitempty"`
	TTFBMs                float64 `json:"ttfb_ms,omitempty"`
	DOMContentLoadedMs    float64 `json:"dom_content_loaded_ms,omitempty"`
	DOMCompleteMs         float64 `json:"dom_complete_ms,omitempty"`
	LoadEventMs           float64 `json:"load_event_ms,omitempty"`
	BlockingResourceCount int     `json:"blocking_resource_count,omitempty"`
	TotalResourceCount    int     `json:"total_resource_count,omitempty"`
	ResourceTransferKB    float64 `json:"resource_transfer_kb,omitempty"`
	JSHeapUsedMB          float64 `json:"js_heap_used_mb,omitempty"`
	JSHeapTotalMB         float64 `json:"js_heap_total_mb,omitempty"`
	LayoutCount           int     `json:"layout_count,omitempty"`
	RecalcStyleCount      int     `json:"recalc_style_count,omitempty"`
	ScriptDurationMs      float64 `json:"script_duration_ms,omitempty"`
	LayoutDurationMs      float64 `json:"layout_duration_ms,omitempty"`
	TaskDurationMs        float64 `json:"task_duration_ms,omitempty"`
	LongTaskCount         int     `json:"long_task_count,omitempty"`
	BrowserProcessCPUPct  float64 `json:"browser_process_cpu_pct,omitempty"`
	BrowserProcessMemMB   float64 `json:"browser_process_mem_mb,omitempty"`
}

type ServerTelemetry struct {
	ServerProcessingMs     float64 `json:"server_processing_ms,omitempty"`
	TransferMs             float64 `json:"transfer_ms,omitempty"`
	SerializationMs        float64 `json:"serialization_ms,omitempty"`
	DeserializationMs      float64 `json:"deserialization_ms,omitempty"`
	RequestPayloadKB       float64 `json:"request_payload_kb,omitempty"`
	RequestSizeClass       string  `json:"request_size_class,omitempty"`
	ResponsePayloadKB      float64 `json:"response_payload_kb,omitempty"`
	ResponseSizeClass      string  `json:"response_size_class,omitempty"`
	EnvelopeOverheadKB     float64 `json:"envelope_overhead_kb,omitempty"`
	SOAPFaultCode          string  `json:"soap_fault_code,omitempty"`
	SOAPFaultCategory      string  `json:"soap_fault_category,omitempty"`
	SOAPNamespaceConflict  string  `json:"soap_namespace_conflict,omitempty"`
	RedirectCount          int     `json:"redirect_count,omitempty"`
	RedirectMs             float64 `json:"redirect_ms,omitempty"`
	FirstEventMs           float64 `json:"first_event_ms,omitempty"`
	EventCount             int     `json:"event_count,omitempty"`
	MessageLatencyMs       float64 `json:"message_latency_ms,omitempty"`
	MessagesSent           int     `json:"messages_sent,omitempty"`
	MessagesReceived       int     `json:"messages_received,omitempty"`
	MessagesSentPerSec     float64 `json:"messages_sent_per_sec,omitempty"`
	MessagesReceivedPerSec float64 `json:"messages_received_per_sec,omitempty"`
	ConsumerLagMs          float64 `json:"consumer_lag_ms,omitempty"`
	ConsumerOffsetLag      int64   `json:"consumer_offset_lag,omitempty"` // offset/sequence backlog behind the partition tail (Kafka/Event Hubs); a count, not a duration
	// Messaging reliability counters (per-transaction; the engine/controller sum
	// them into run totals). Populated by the messaging protocols (JMS family,
	// Kafka, Event Hubs) for honest queue-test reliability accounting.
	SendFailureCount  int `json:"send_failure_count,omitempty"`  // failed produce/publish attempts
	RedeliveryCount   int `json:"redelivery_count,omitempty"`    // messages delivered more than once (JMSXDeliveryCount/DeliveryCount > 1)
	DeadLetteredCount int `json:"dead_lettered_count,omitempty"` // messages routed to / received from a dead-letter destination
	DuplicateCount    int `json:"duplicate_count,omitempty"`     // duplicate messages detected (by message id / dedup window)
	OutOfOrderCount   int `json:"out_of_order_count,omitempty"`  // messages observed out of expected order
	CommitCount       int `json:"commit_count,omitempty"`        // transaction / ack commits
	AbortCount        int `json:"abort_count,omitempty"`         // transaction rollbacks / aborts
	// Event Hubs / streaming ingestion signals (EH.4). Counters are summed into
	// run totals; EndToEndLatencyMs is averaged like ConsumerLagMs; PartitionID
	// lets the aggregator bucket produce volume per partition to derive skew.
	ThrottledRequests  int     `json:"throttled_requests,omitempty"`    // throttle / 429 responses (broker backpressure)
	ServerBusyCount    int     `json:"server_busy_count,omitempty"`     // ServerBusy (amqp:resource-limit-exceeded) errors
	QuotaExceededCount int     `json:"quota_exceeded_count,omitempty"`  // quota-exceeded errors
	EventTooLargeCount int     `json:"event_too_large_count,omitempty"` // events rejected for exceeding the size cap
	EndToEndLatencyMs  float64 `json:"end_to_end_latency_ms,omitempty"` // producer-enqueue -> observed-consume latency
	PartitionID        string  `json:"partition_id,omitempty"`          // partition this event was produced to (skew bucketing)
	ConnectionReused   bool    `json:"connection_reused"`
}

// HasMeasuredSample reports whether the telemetry carries an actual measured
// transfer/latency/payload sample, as opposed to a control-only record that
// carries only reliability counters (e.g. a jms_commit/jms_rollback/kafka_commit
// op). Aggregators sum the reliability counters unconditionally but must only
// count measured samples toward the averaging divisor, so control ops do not
// dilute average payload/latency/throughput.
func (t *ServerTelemetry) HasMeasuredSample() bool {
	if t == nil {
		return false
	}
	return t.ServerProcessingMs != 0 || t.TransferMs != 0 || t.SerializationMs != 0 ||
		t.DeserializationMs != 0 || t.RequestPayloadKB != 0 || t.ResponsePayloadKB != 0 ||
		t.EnvelopeOverheadKB != 0 || t.RedirectMs != 0 || t.RedirectCount != 0 ||
		t.FirstEventMs != 0 || t.EventCount != 0 || t.MessageLatencyMs != 0 ||
		t.MessagesSent != 0 || t.MessagesReceived != 0 || t.MessagesSentPerSec != 0 ||
		t.MessagesReceivedPerSec != 0 || t.ConsumerLagMs != 0 || t.ConsumerOffsetLag != 0 ||
		t.ThrottledRequests != 0 || t.ServerBusyCount != 0 || t.QuotaExceededCount != 0 ||
		t.EventTooLargeCount != 0 || t.EndToEndLatencyMs != 0 ||
		t.RequestSizeClass != "" || t.ResponseSizeClass != "" || t.ConnectionReused
}

// Extractor extracts a value from the response for use in later templates or variables.
//
// Example: Extract session token from JSON response for use in subsequent requests.
type Extractor struct {
	Name       string `json:"name"`                  // Variable name
	Source     string `json:"source"`                // "body", "header", etc.
	Type       string `json:"type"`                  // "json", "xpath", "regex", etc.
	Expression string `json:"expression"`            // JSONPath, XPath, regex pattern, etc.
	OnMiss     string `json:"on_miss,omitempty"`     // ""/"ignore": leave unset; "fail": fail the request
	Mode       string `json:"mode,omitempty"`        // "": single value; "all": name_1..name_N + name_count
	SaveOffset int    `json:"save_offset,omitempty"` // skip leading bytes of each matched value
	SaveLength int    `json:"save_length,omitempty"` // truncate each value to at most N bytes (0 = entire)
	Convert    string `json:"convert,omitempty"`     // "url_encode" | "html_decode"
}

// Assertion validates a response matches expected conditions.
//
// Example: Assert response status is 200 and body contains "success".
type Assertion struct {
	Type     string `json:"type"`               // "status", "header", "body", "json", etc.
	Expected any    `json:"expected,omitempty"` // Expected value
	Value    string `json:"value,omitempty"`    // Field to check (JSONPath, header name, etc.)
	Min      int    `json:"min,omitempty"`      // Min threshold for numeric assertions
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
	ActionName          string        // Name of the executed action
	BusinessTransaction bool          // True when this sample represents a completed business transaction
	Group               string        // Group/transaction name for metrics
	Region              string        // Generator region that produced this result (per-result override; empty = use writer region)
	StartTime           time.Time     // When the action started
	Duration            time.Duration // Time to execute action
	CorrelationID       string        // Correlation or request id when the protocol exposes one
	NetworkProfile      string        // Effective network emulation profile used for this result
	DependencyType      string        // Classified dependency type (http, jms_queue, etc.)
	DependencyTarget    string        // Classified downstream target (host, queue, topic, etc.)
	DNSTime             time.Duration // DNS lookup time (if applicable)
	ConnectTime         time.Duration // Connection establishment time (if applicable)
	TLSTime             time.Duration
	TTFB                time.Duration
	BytesSent           int64
	BytesReceived       int64
	StatusCode          int
	Success             bool
	ErrorMessage        string
	ResponseBody        string
	Extractions         map[string]string
	PageURL             string
	PageTitle           string
	HTML                string
	ScreenshotBase64    string
	ConsoleEntries      []ConsoleEntry
	NetworkEntries      []NetworkEntry
	DOMSummary          *DOMSummary
	ClientMetrics       *ClientMetrics
	ServerTelemetry     *ServerTelemetry
	ProtocolEvidence    map[string]any
	Snapshot            *ErrorSnapshot // populated when snapshot_on_error is enabled
	SourceStepID        string
	SourceStepIndex     int
	SourceAction        string
	SourceActionName    string
	SourceTransaction   string
	SourceProfile       string
}

// ErrorSnapshot captures full request/response data for post-test debugging.
type ErrorSnapshot struct {
	RequestMethod    string            `json:"request_method"`
	RequestURL       string            `json:"request_url"`
	RequestHeaders   map[string]string `json:"request_headers,omitempty"`
	RequestBody      string            `json:"request_body,omitempty"`
	ResponseStatus   int               `json:"response_status"`
	ResponseHeaders  map[string]string `json:"response_headers,omitempty"`
	ResponseBody     string            `json:"response_body,omitempty"`
	PageURL          string            `json:"page_url,omitempty"`
	PageTitle        string            `json:"page_title,omitempty"`
	HTML             string            `json:"html,omitempty"`
	ScreenshotBase64 string            `json:"screenshot_base64,omitempty"`
	ConsoleEntries   []ConsoleEntry    `json:"console_entries,omitempty"`
	NetworkEntries   []NetworkEntry    `json:"network_entries,omitempty"`
	DOMSummary       *DOMSummary       `json:"dom_summary,omitempty"`
}

func ClassifyPayloadSize(value int64) string {
	switch {
	case value <= 0:
		return ""
	case value < 16*1024:
		return "small"
	case value < 128*1024:
		return "medium"
	default:
		return "large"
	}
}
