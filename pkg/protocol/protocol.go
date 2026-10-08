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
//	func init() {
//	    protocol.Register("myproto", func() protocol.Protocol { return &MyProtocol{} })
//	}
package protocol

import (
	"context"
	"errors"
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

// StreamingProtocol is an optional extension for compound actions that produce
// independently useful child samples before the aggregate action completes.
// The aggregate result controls action success; emitted results pass through
// the engine's normal metrics, archive, redaction, and snapshot path.
type StreamingProtocol interface {
	ExecuteStream(
		ctx context.Context,
		action *Action,
		emit func(*Result) error,
	) (*Result, error)
}

// ParallelSafetyProvider explicitly opts a protocol into isolated concurrent
// branch execution. Absence is fail-closed: the engine never assumes that an
// adapter's connection/session state can be duplicated safely.
type ParallelSafetyProvider interface {
	ParallelSafe() bool
}

// SharedSessionConcurrencyProvider admits concurrent immutable requests on one
// initialized protocol instance. Unlike ParallelSafetyProvider, this retains
// the parent session; lifecycle changes must happen outside the joined group.
type SharedSessionConcurrencyProvider interface {
	SharedSessionConcurrentSafe() bool
	ValidateSharedSessionAction(*Action) error
}

// RuntimeCapabilityProvider lets optional sidecar-backed protocols advertise
// availability, scheduler features, and a protocol-specific VUser ceiling
// without teaching the distributed service protocol names.
type RuntimeCapabilityProvider interface {
	RuntimeCapabilities() RuntimeCapabilities
}

type RuntimeHealthChecker interface{ RuntimeHealthy(context.Context) bool }

// RuntimePreflightProvider performs script-specific runtime validation before
// an engine allocates any VUsers. Unlike RuntimeHealthChecker, this receives
// the secret-resolved protocol configuration and the unique executable targets
// required by the script. Implementations must return only redaction-safe
// errors and stable warning codes.
type RuntimePreflightProvider interface {
	PreflightRuntime(context.Context, RuntimePreflightRequest) (RuntimePreflightResult, error)
}

type RuntimePreflightRequest struct {
	Config  map[string]any           `json:"config,omitempty"`
	Targets []RuntimePreflightTarget `json:"targets,omitempty"`
}

type RuntimePreflightTarget struct {
	ClassName string `json:"class_name"`
	Method    string `json:"method"`
}

type RuntimePreflightResult struct {
	Warnings []string `json:"warnings,omitempty"`
}

type RuntimeCapabilities struct {
	Available bool
	Features  []string
	MaxVUsers int64
}

// ReadRuntimeCapabilities invokes the optional runtime-capability extension
// behind a process-protection boundary. Runtime adapters are allowed to inspect
// external runner state, so a panic must fail the capability check rather than
// crash the controller or generator.
func ReadRuntimeCapabilities(instance Protocol) (capabilities RuntimeCapabilities, supported bool, err error) {
	provider, supported := instance.(RuntimeCapabilityProvider)
	if !supported {
		return RuntimeCapabilities{}, false, nil
	}
	defer func() {
		if recover() != nil {
			capabilities = RuntimeCapabilities{}
			err = errors.New("runtime capability provider panicked")
		}
	}()
	return provider.RuntimeCapabilities(), true, nil
}

// CheckRuntimeHealth invokes the optional runtime-health extension without
// exposing a recovered panic value, which may contain resolved configuration.
func CheckRuntimeHealth(ctx context.Context, instance Protocol) (healthy bool, supported bool, err error) {
	checker, supported := instance.(RuntimeHealthChecker)
	if !supported {
		return false, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, true, err
	}
	defer func() {
		if recover() != nil {
			healthy = false
			err = errors.New("runtime health checker panicked")
		}
	}()
	return checker.RuntimeHealthy(ctx), true, nil
}

// RunRuntimePreflight invokes the optional script-aware preflight extension
// behind the same process-protection boundary as other protocol lifecycle
// calls. Panic values are deliberately omitted from the returned error.
func RunRuntimePreflight(ctx context.Context, instance Protocol, request RuntimePreflightRequest) (result RuntimePreflightResult, supported bool, err error) {
	provider, supported := instance.(RuntimePreflightProvider)
	if !supported {
		return RuntimePreflightResult{}, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return RuntimePreflightResult{}, true, err
	}
	defer func() {
		if recover() != nil {
			result = RuntimePreflightResult{}
			err = errors.New("runtime preflight provider panicked")
		}
	}()
	result, err = provider.PreflightRuntime(ctx, request)
	return result, true, err
}

// FeatureJavaVuserPersistentLifecycleV1 identifies a Java Vuser runtime that
// keeps one JVM and one Actions instance alive for the full VUser lifecycle.
const FeatureJavaVuserPersistentLifecycleV1 = "java_vuser_persistent_lifecycle_v1"

// FeatureJavaVuserSessionFrameTokenV1 identifies a persistent Java Vuser
// runtime whose per-invocation result frames are bound to an unpredictable
// engine-generated token. Persistent lifecycle execution is admitted only
// when this feature is advertised alongside PersistentLifecycleV1.
const FeatureJavaVuserSessionFrameTokenV1 = "java_vuser_session_frame_token_v1"

// FeatureJavaVuserPhaseLifecycleV1 identifies the legacy capability to invoke
// separate init/action/end methods. It does not guarantee that those calls
// share an Actions instance and therefore is insufficient for combined imports.
const FeatureJavaVuserPhaseLifecycleV1 = "java_vuser_phase_lifecycle_v1"

// RuntimeFeaturesSatisfy reports whether advertised runtime features satisfy
// the requested capability after whitespace and case normalization. Governed
// persistent lifecycle support requires authenticated result framing, and is a
// strict superset of legacy phase invocation only when both features exist.
func RuntimeFeaturesSatisfy(features []string, required string) bool {
	if strings.Contains(required, "+") {
		for _, part := range strings.Split(required, "+") {
			if !RuntimeFeaturesSatisfy(features, part) {
				return false
			}
		}
		return true
	}
	required = strings.TrimSpace(required)
	if required == "" {
		return true
	}
	hasRequired := false
	hasPersistentLifecycle := false
	hasSessionFrameToken := false
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if strings.EqualFold(feature, required) {
			hasRequired = true
		}
		if strings.EqualFold(feature, FeatureJavaVuserPersistentLifecycleV1) {
			hasPersistentLifecycle = true
		}
		if strings.EqualFold(feature, FeatureJavaVuserSessionFrameTokenV1) {
			hasSessionFrameToken = true
		}
	}
	if strings.EqualFold(required, FeatureJavaVuserPersistentLifecycleV1) {
		return hasPersistentLifecycle && hasSessionFrameToken
	}
	if strings.EqualFold(required, FeatureJavaVuserPhaseLifecycleV1) {
		return hasRequired || (hasPersistentLifecycle && hasSessionFrameToken)
	}
	return hasRequired
}

// MissingJavaVuserPersistentRuntimeFeature returns the first missing feature
// from the governed persistent Java Vuser contract. It is intended for stable
// diagnostics at admission and placement boundaries.
func MissingJavaVuserPersistentRuntimeFeature(features []string) string {
	hasPersistentLifecycle := false
	hasSessionFrameToken := false
	for _, feature := range features {
		switch {
		case strings.EqualFold(strings.TrimSpace(feature), FeatureJavaVuserPersistentLifecycleV1):
			hasPersistentLifecycle = true
		case strings.EqualFold(strings.TrimSpace(feature), FeatureJavaVuserSessionFrameTokenV1):
			hasSessionFrameToken = true
		}
	}
	if !hasPersistentLifecycle {
		return FeatureJavaVuserPersistentLifecycleV1
	}
	if !hasSessionFrameToken {
		return FeatureJavaVuserSessionFrameTokenV1
	}
	return ""
}

// TextRedactor is a runtime-only projection used by protocol implementations
// that retain diagnostics outside the ordinary Result pipeline (for example,
// browser HAR artifacts). It must never be serialized or persisted.
type TextRedactor func(string) string

// RuntimeTextRedactorSetter is an optional protocol capability. The engine
// binds it to the owning VUser so the projection follows credentials extracted
// during execution as well as the project secrets resolved at startup.
type RuntimeTextRedactorSetter interface {
	SetRuntimeTextRedactor(TextRedactor)
}

// RuntimeSecretActivitySetter is an optional protocol capability for binary
// diagnostics that cannot be safely passed through TextRedactor. The provider
// is memory-only and reflects values discovered during the run as well as
// secrets resolved during startup.
type RuntimeSecretActivitySetter interface {
	SetRuntimeSecretActivity(func() bool)
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

// CacheController is an optional capability for protocols that maintain a
// per-VUser response cache. The engine invokes ClearCache at the start of an
// iteration when browser emulation's "simulate a new user on each iteration"
// is enabled. Protocols without a cache simply do not implement this
// interface and the engine treats the clear as a no-op.
type CacheController interface {
	// ClearCache discards all cached responses held by the VUser session.
	ClearCache(ctx context.Context) error
}

// IterationController receives main-iteration boundaries, after session resets.
// Calls are sequential and occur before that iteration's actions start.
type IterationController interface {
	BeginIteration(ctx context.Context) error
}

// ConnectionController is an optional capability for protocols that pool
// reusable network connections. The engine invokes CloseIdleConnections for
// an http_connections_close action. Active requests are never interrupted.
type ConnectionController interface {
	// CloseIdleConnections closes only currently-idle pooled connections.
	CloseIdleConnections(ctx context.Context) error
}

// Action represents a single operation (HTTP request, message, etc.) to execute.
type HeaderEntry struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type HeaderList []HeaderEntry

func (headers HeaderList) ToMap() map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for _, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
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
	Name               string              `json:"name"`
	Type               string              `json:"type"`
	Method             string              `json:"method,omitempty"`
	HTTPVersion        OptionalHTTPVersion `json:"http_version,omitzero"`
	Target             string              `json:"target"`
	RedactedTarget     string              `json:"-"`
	Selector           string              `json:"selector,omitempty"`
	RedactedSelector   string              `json:"-"`
	Value              string              `json:"value,omitempty"`
	RedactedValue      string              `json:"-"`
	Headers            map[string]string   `json:"headers,omitempty"`
	HeaderList         HeaderList          `json:"header_list,omitempty"`
	RedactedHeaders    map[string]string   `json:"-"`
	RedactedHeaderList HeaderList          `json:"-"`
	// ResolvedDefaultHeaders marks per-VUser default headers interpolated by
	// the engine for this action. HTTP falls back to its initialized defaults
	// for direct protocol callers that do not set the marker.
	ResolvedDefaultHeaders bool                   `json:"-"`
	DefaultHeaders         map[string]string      `json:"-"`
	DefaultHeaderList      HeaderList             `json:"-"`
	Body                   []byte                 `json:"body,omitempty"`
	BodyEncoding           string                 `json:"body_encoding,omitempty"`
	RedactedBody           []byte                 `json:"-"`
	Params                 map[string]any         `json:"params,omitempty"`
	RedactedParams         map[string]any         `json:"-"`
	WaitFor                *WaitCondition         `json:"wait_for,omitempty"`
	Sync                   *BrowserSyncPolicy     `json:"sync,omitempty"`
	Capture                *BrowserCaptureOptions `json:"capture,omitempty"`
	Metadata               map[string]any         `json:"metadata,omitempty"`
	HierarchyPath          string                 `json:"hierarchy_path,omitempty"`
	PageName               string                 `json:"page_name,omitempty"`
	Extractors             []Extractor            `json:"extractors,omitempty"`
	Assertions             []Assertion            `json:"assertions,omitempty"`
	ThinkTime              ThinkTimeConfig        `json:"think_time,omitempty"`
	Auth                   *AuthConfig            `json:"auth,omitempty"`
	RedactedAuth           *AuthConfig            `json:"-"`
	TimeoutMs              int                    `json:"timeout_ms,omitempty"`
	MaxRedirects           *int                   `json:"max_redirects,omitempty"`
	SOAP                   *SOAPActionConfig      `json:"soap,omitempty"`
	RedactedSOAP           *SOAPActionConfig      `json:"-"`
	// DiscardResponseBodyOnSuccess lets high-volume engine callers avoid a
	// second full payload copy after extractors/assertions have completed. Error
	// responses still retain their bounded body evidence.
	DiscardResponseBodyOnSuccess bool `json:"-"`
	// CaptureMessageDetail is an engine-only execution hint. Protocols that can
	// produce bounded request/response evidence may retain it for this one
	// successful result; archive sinks still enforce the run-wide budget.
	CaptureResourceDetails     bool   `json:"-"`
	CaptureMessageDetail       bool   `json:"-"`
	MessageDetailMode          string `json:"-"`
	MessageDetailMaxItems      int    `json:"-"`
	MessageDetailMaxItemBytes  int64  `json:"-"`
	MessageDetailMaxTotalBytes int64  `json:"-"`
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
	BrowserEvidence *BrowserResourceEvidence `json:"browser_evidence,omitempty"`
	FailureKind     string                   `json:"failure_kind,omitempty"` // http, transport, timeout, canceled

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
		cloned[i].BrowserEvidence = entry.BrowserEvidence.Clone()
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
	RuntimeAvailability   *BrowserRuntimeMetricAvailability `json:"runtime_availability,omitempty"`
	DocumentEvidence      *BrowserDocumentEvidence          `json:"document_evidence,omitempty"`
	DeviceProfile         string                            `json:"device_profile,omitempty"`
	DevicePlatform        string                            `json:"device_platform,omitempty"`
	DeviceTier            string                            `json:"device_tier,omitempty"`
	BrowserEngine         string                            `json:"browser_engine,omitempty"`
	ActionDurationMs      float64                           `json:"action_duration_ms,omitempty"`
	FCPMs                 float64                           `json:"fcp_ms,omitempty"`
	LCPMs                 float64                           `json:"lcp_ms,omitempty"`
	CLSScore              float64                           `json:"cls_score,omitempty"`
	INPMs                 float64                           `json:"inp_ms,omitempty"`
	TTFBMs                float64                           `json:"ttfb_ms,omitempty"`
	DOMContentLoadedMs    float64                           `json:"dom_content_loaded_ms,omitempty"`
	DOMCompleteMs         float64                           `json:"dom_complete_ms,omitempty"`
	LoadEventMs           float64                           `json:"load_event_ms,omitempty"`
	BlockingResourceCount int                               `json:"blocking_resource_count,omitempty"`
	TotalResourceCount    int                               `json:"total_resource_count,omitempty"`
	ResourceTransferKB    float64                           `json:"resource_transfer_kb,omitempty"`
	JSHeapUsedMB          float64                           `json:"js_heap_used_mb,omitempty"`
	JSHeapTotalMB         float64                           `json:"js_heap_total_mb,omitempty"`
	LayoutCount           int                               `json:"layout_count,omitempty"`
	RecalcStyleCount      int                               `json:"recalc_style_count,omitempty"`
	ScriptDurationMs      float64                           `json:"script_duration_ms,omitempty"`
	LayoutDurationMs      float64                           `json:"layout_duration_ms,omitempty"`
	TaskDurationMs        float64                           `json:"task_duration_ms,omitempty"`
	LongTaskCount         int                               `json:"long_task_count,omitempty"`
	BrowserProcessCPUPct  float64                           `json:"browser_process_cpu_pct,omitempty"`
	BrowserProcessMemMB   float64                           `json:"browser_process_mem_mb,omitempty"`
}

type ServerTelemetry struct {
	SubscriptionID string `json:"subscription_id,omitempty"`
	// Successful client publish acknowledgement or settlement duration; unavailable for local/mock acknowledgements.
	Messaging                 *MessagingDiagnostics `json:"messaging,omitempty"`
	AcknowledgementMs         *float64              `json:"acknowledgement_ms,omitempty"`
	WebSocket                 *WebSocketDiagnostics `json:"websocket,omitempty"`
	GRPC                      *GRPCDiagnostics      `json:"grpc,omitempty"`
	ServerProcessingMs        float64               `json:"server_processing_ms,omitempty"`
	TransferMs                float64               `json:"transfer_ms,omitempty"`
	SerializationMs           float64               `json:"serialization_ms,omitempty"`
	DeserializationMs         float64               `json:"deserialization_ms,omitempty"`
	RequestPayloadKB          float64               `json:"request_payload_kb,omitempty"`
	RequestSizeClass          string                `json:"request_size_class,omitempty"`
	ResponsePayloadKB         float64               `json:"response_payload_kb,omitempty"`
	ResponseSizeClass         string                `json:"response_size_class,omitempty"`
	EnvelopeOverheadKB        float64               `json:"envelope_overhead_kb,omitempty"`
	SOAPFaultCode             string                `json:"soap_fault_code,omitempty"`
	SOAPFaultCategory         string                `json:"soap_fault_category,omitempty"`
	SOAPNamespaceConflict     string                `json:"soap_namespace_conflict,omitempty"`
	RedirectCount             int                   `json:"redirect_count,omitempty"`
	RedirectMs                float64               `json:"redirect_ms,omitempty"`
	FirstEventMs              float64               `json:"first_event_ms,omitempty"`
	EventCount                int                   `json:"event_count,omitempty"`
	MessageLatencyMs          float64               `json:"message_latency_ms,omitempty"`
	MessagesSent              int                   `json:"messages_sent,omitempty"`
	MessagesReceived          int                   `json:"messages_received,omitempty"`
	MessagesSentPerSec        float64               `json:"messages_sent_per_sec,omitempty"`
	MessagesReceivedPerSec    float64               `json:"messages_received_per_sec,omitempty"`
	ConsumerLagObserved       bool                  `json:"consumer_lag_observed,omitempty"`
	ConsumerOffsetLagObserved bool                  `json:"consumer_offset_lag_observed,omitempty"`
	ConsumerLagMs             float64               `json:"consumer_lag_ms,omitempty"`
	ConsumerOffsetLag         int64                 `json:"consumer_offset_lag,omitempty"` // offset/sequence backlog behind the partition tail (Kafka/Event Hubs); a count, not a duration
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
		t.MessagesReceivedPerSec != 0 || t.ConsumerLagObserved || t.ConsumerOffsetLagObserved || t.ConsumerLagMs != 0 || t.ConsumerOffsetLag != 0 ||
		t.ThrottledRequests != 0 || t.ServerBusyCount != 0 || t.QuotaExceededCount != 0 ||
		t.EventTooLargeCount != 0 || t.EndToEndLatencyMs != 0 ||
		t.RequestSizeClass != "" || t.ResponseSizeClass != "" || t.ConnectionReused
}

// Extractor extracts a value from the response for use in later templates or variables.
//
// Example: Extract session token from JSON response for use in subsequent requests.
type Extractor struct {
	Name         string  `json:"name"`       // Variable name
	Source       string  `json:"source"`     // "body", "header", etc.
	Type         string  `json:"type"`       // "json", "xpath", "regex", etc.
	Expression   string  `json:"expression"` // JSONPath, XPath, regex pattern, etc.
	DefaultValue *string `json:"default_value,omitempty"`
	OnMiss       string  `json:"on_miss,omitempty"`     // ""/"ignore": leave unset; "fail": fail the request
	Mode         string  `json:"mode,omitempty"`        // "": single value; "all": name_1..name_N + name_count
	SaveOffset   int     `json:"save_offset,omitempty"` // skip leading bytes of each matched value
	SaveLength   int     `json:"save_length,omitempty"` // truncate each value to at most N bytes (0 = entire)
	Convert      string  `json:"convert,omitempty"`     // "url_encode" | "url_decode" | "html_decode"
}

// Assertion validates a response matches expected conditions.
//
// Example: Assert response status is 200 and body contains "success".
type Assertion struct {
	LegacyHTMLCheck bool `json:"legacy_html_check,omitempty"` // imported web_find only

	Type     string `json:"type"`               // "status", "header", "body", "json", etc.
	Expected any    `json:"expected,omitempty"` // Expected value
	Value    string `json:"value,omitempty"`    // Field to check (JSONPath, header name, etc.)
	Min      int    `json:"min,omitempty"`      // Min threshold for numeric assertions
	Max      int    `json:"max,omitempty"`      // Max threshold for numeric assertions
	// CaseInsensitive makes body_contains and body_not_contains ignore case.
	CaseInsensitive bool `json:"case_insensitive,omitempty"`
}

// ThinkTimeConfig defines think time (delay) after an action.
//
// Think time simulates user reading time and is applied after each action
// to create more realistic traffic patterns. Supports fixed, random, and gaussian distributions.
type ThinkTimeConfig struct {
	MinPercent float64 `json:"min_percent,omitempty" jsonschema:"minimum=0,maximum=10000"` // random_percentage: percentage of each recorded delay
	MaxPercent float64 `json:"max_percent,omitempty" jsonschema:"minimum=0,maximum=10000"`
	Type       string  `json:"type"`                 // "fixed", "random", "gaussian"
	MeanMs     float64 `json:"mean_ms"`              // Mean value in milliseconds
	StdMs      float64 `json:"std_ms,omitempty"`     // Standard deviation for gaussian
	MinMs      float64 `json:"min_ms,omitempty"`     // Min for random/gaussian
	MaxMs      float64 `json:"max_ms,omitempty"`     // Max for random/gaussian
	Multiplier float64 `json:"multiplier,omitempty"` // Scalar multiplier; zero means omitted/inherit 1
	LimitMs    float64 `json:"limit_ms,omitempty"`   // Upper limit cap
	Ignore     bool    `json:"ignore,omitempty"`     // Skip think time for this action
}

// LifecycleControl is an in-process control receipt from a stateful protocol.
// It continues after an action or ends the main iteration, with separate status.
// It is never decoded from script content or retained in exported results.
type LifecycleControl struct {
	EndIteration bool
	Failed       bool
}

// Result contains the outcome of a single action execution.
//
// Values a protocol adapter HARVESTS FROM THE SYSTEM UNDER TEST — HTTP
// cookie-jar contents in particular — are deliberately not carried to the engine
// as redaction material, and this struct has no field for them. They are not
// platform-managed secrets: the operator is running the test against that
// system, and substring-replacing an ordinary "Set-Cookie: locale=en" out of the
// run destroyed the evidence (and the metric dimensions) they need. Platform
// secrets reach the matcher through {{secret.NAME}} resolution instead.
//
// This says nothing about Extractions below, which IS carried to the engine and
// whose sensitively-named entries are still registered for redaction.
type Result struct {
	LifecycleControl *LifecycleControl `json:"-"`
	// CSVExportIDs binds finalized samples to declared exports before display-name redaction.
	CSVExportIDs             []string
	ExtractionsIncomplete    bool     `json:"extractions_incomplete,omitempty"`
	ContentCheckErrorMatches int      `json:"content_check_error_matches,omitempty"`
	ContentCheckInfoMatches  int      `json:"content_check_info_matches,omitempty"`
	WarningCodes             []string `json:"warning_codes,omitempty"` // bounded protocol policy warnings

	// VUserID and Iteration identify the VUser iteration that produced the
	// result: the run-wide VUser ID and the 1-based iteration (0 during init
	// and teardown). Journey accounting (E5) groups archived rows by them.
	// Interrupted marks a failure recorded once the VUser was stopping (its
	// in-flight action cancelled, or a stop or goal boundary abandoning the
	// iteration): the stop, not the target, may have caused it.
	// VUserLifetime is a random identity of the VUser's lifetime: a VUser ID
	// is reused by later VUsers (open-workload arrivals, scale-up), a
	// lifetime never is. WallElapsed is a transaction's wall-clock span when
	// its Duration excludes time (think time inside it). EndsIteration marks
	// a row the engine recorded because the iteration ended on its own (the
	// iteration transaction, or a transaction the VUser left open when it
	// ended on its own), so the iteration was not cut by a stop.
	VUserID             int           `json:"vuser_id,omitempty"`
	Iteration           int64         `json:"iteration,omitempty"`
	Interrupted         bool          `json:"interrupted,omitempty"`
	VUserLifetime       uint64        `json:"vuser_lifetime,omitempty"`
	WallElapsed         time.Duration `json:"wall_elapsed,omitempty"`
	EndsIteration       bool          `json:"ends_iteration,omitempty"`
	ActionName          string        // Name of the executed action
	BusinessTransaction bool          // True when this sample represents a completed business transaction
	SampleKind          string        // "child" or "aggregate" for StreamingProtocol results
	ParentActionName    string        // Aggregate action that emitted this child sample
	Attempt             int           // 1-based execution attempt, including retries
	Group               string        // Group/transaction name for metrics
	Region              string        // Generator region that produced this result (per-result override; empty = use writer region)
	Phase               string        // init, action, or teardown lifecycle phase assigned by the engine
	Hierarchy           string        // Bounded author/import hierarchy path used for analytics attribution
	PageName            string        // Stable authored/imported page label used for rollups
	StartTime           time.Time     // When the action started
	Duration            time.Duration // Time to execute action
	WorkerDuration      time.Duration `json:"worker_duration,omitempty"` // Includes compatibility cleanup outside sample timing
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
	// CorrelationEvidence carries the raw request/response exchange needed for
	// server-side record-vs-replay diagnosis. It is deliberately excluded from
	// every JSON boundary and must be consumed only by the replay engine's
	// pre-scrub correlation observer. Public replay events, reports, snapshots,
	// exports, and WebSocket messages must never retain it.
	CorrelationEvidence *CorrelationEvidence `json:"-"`
	// TraceContext is the W3C context the request actually carried, kept apart
	// from CorrelationID; nil when the protocol does not record one.
	TraceContext *SentTraceContext `json:"trace_context,omitempty"`
	// TraceLinks relate a messaging result to other contexts: a consumed
	// message's producer, batch members or a redelivery. At most MaxTraceLinks.
	TraceLinks          []TraceLink `json:"trace_links,omitempty"`
	TraceLinksTruncated bool        `json:"trace_links_truncated,omitempty"`
	// Extractions carries the values an action's extractors pulled out of the
	// response, keyed by the variable they are saved to. An entry whose KEY is
	// sensitive (scrub.SensitiveName — "session_token" and the like) IS
	// registered for redaction by the engine, so do not place a value here
	// expecting it to pass through run evidence untouched.
	PropertyDimensions         map[string]string `json:"property_dimensions,omitempty"`
	Extractions                map[string]string
	PageURL                    string
	PageTitle                  string
	HTML                       string
	ScreenshotBase64           string
	ConsoleEntries             []ConsoleEntry
	NetworkEntries             []NetworkEntry
	DOMSummary                 *DOMSummary
	ClientMetrics              *ClientMetrics
	ServerTelemetry            *ServerTelemetry
	HTTPVersionPolicy          string `json:"-"`
	HTTPVersionNegotiated      string `json:"-"`
	HTTPVersionSource          string `json:"-"`
	ProtocolEvidence           map[string]any
	Snapshot                   *ErrorSnapshot // populated when snapshot_on_error is enabled
	SourceStepID               string
	SourceStepIndex            int
	SourceAction               string
	SourceActionName           string
	SourceTransaction          string
	SourceProfile              string
	SourcePath                 string
	SourceUID                  string
	RequestType                string
	OperationKind              string
	MessageDetailRequested     bool   `json:"-"`
	MessageDetailMode          string `json:"-"`
	MessageDetailPriority      int    `json:"-"`
	MessageDetailRetained      bool   `json:"-"`
	MessageDetailMaxItems      int    `json:"-"`
	MessageDetailMaxItemBytes  int64  `json:"-"`
	MessageDetailMaxTotalBytes int64  `json:"-"`
}

const (
	// CorrelationEvidenceCaptureConfigKey is an internal runtime capability flag.
	// Replay alone is not sufficient to enable raw exchange capture: the engine
	// sets this only when a private pre-scrub correlation observer is present.
	CorrelationEvidenceCaptureConfigKey = "apexload_internal_capture_correlation_evidence"
	// MaxCorrelationEvidenceBytes bounds the aggregate private request/response
	// exchange before it is copied into replay-only correlation evidence.
	MaxCorrelationEvidenceBytes = 12 << 20
	// CorrelationEvidenceTooLargeReason is retained only in the private evidence
	// sentinel so downstream consumers can fail closed without retaining a
	// truncated or partially copied exchange.
	CorrelationEvidenceTooLargeReason = "raw correlation evidence exceeds the capture byte limit"
)

// CorrelationEvidence is an ephemeral, raw protocol exchange used only for
// server-side correlation diagnosis. Every field is independently excluded
// from JSON as defense in depth in case the value is ever detached from Result.
// Protocol implementations should populate the actual wire request and decoded
// response where available; callers must treat every value as sensitive.
type CorrelationEvidence struct {
	// UnavailableReason marks an exchange that was deliberately not retained.
	// When set, every raw field must remain empty and consumers must fail closed.
	UnavailableReason      string            `json:"-"`
	RequestURL             string            `json:"-"`
	RequestBody            string            `json:"-"`
	RequestBodyTransformed bool              `json:"-"`
	RequestHeaders         map[string]string `json:"-"`
	// Authored request fields are the private, unresolved DSL surfaces. They are
	// compared once inside the replay session to prove whether a wire offset maps
	// exactly to editable source, then discarded with the rest of the evidence.
	AuthoredRequestURL     string            `json:"-"`
	AuthoredRequestBody    string            `json:"-"`
	AuthoredRequestHeaders map[string]string `json:"-"`
	ResponseBody           string            `json:"-"`
	ResponseHeaders        map[string]string `json:"-"`
	ResponseStatus         int               `json:"-"`
	// ResponseSuccess is the protocol's response/transport outcome before
	// extractors, content checks, or non-status assertions can fail the action.
	// Nil lets non-HTTP protocols fall back to the overall action outcome.
	ResponseSuccess *bool `json:"-"`
}

// UnavailableCorrelationEvidence returns an explicit, raw-data-free sentinel.
// It is preferable to nil for a requested capture that could not be retained:
// nil means no exchange existed, while this marker forces diagnosis to reject
// incomplete evidence.
func UnavailableCorrelationEvidence(reason string) *CorrelationEvidence {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "raw correlation evidence is unavailable"
	}
	return &CorrelationEvidence{UnavailableReason: reason}
}

// ErrorSnapshot captures full request/response data for post-test debugging.
// ResourceSnapshot is a bounded, redacted exchange already performed by a parent
// HTTP action. It cannot recursively contain other snapshots.
type ResourceSnapshot struct {
	RequestURL        string            `json:"request_url"`
	RequestHeaders    map[string]string `json:"request_headers,omitempty"`
	ResponseStatus    int               `json:"response_status"`
	ResponseHeaders   map[string]string `json:"response_headers,omitempty"`
	ResponseBody      string            `json:"response_body,omitempty"`
	ResponseBodyState string            `json:"response_body_state"`
	CacheHit          bool              `json:"cache_hit,omitempty"`
	FailureKind       string            `json:"failure_kind,omitempty"`
	Truncated         bool              `json:"truncated,omitempty"`
}

func CloneResourceSnapshots(entries []ResourceSnapshot) []ResourceSnapshot {
	if entries == nil {
		return nil
	}
	out := append([]ResourceSnapshot(nil), entries...)
	for i := range out {
		out[i].RequestHeaders = cloneResourceHeaders(out[i].RequestHeaders)
		out[i].ResponseHeaders = cloneResourceHeaders(out[i].ResponseHeaders)
	}
	return out
}
func cloneResourceHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	return out
}

type ErrorSnapshot struct {
	Resources         []ResourceSnapshot `json:"resources,omitempty"`
	OmittedResources  int                `json:"omitted_resources,omitempty"`
	ResponseBodyState string             `json:"response_body_state,omitempty"`
	RequestMethod     string             `json:"request_method"`
	RequestURL        string             `json:"request_url"`
	RequestHeaders    map[string]string  `json:"request_headers,omitempty"`
	RequestBody       string             `json:"request_body,omitempty"`
	ResponseStatus    int                `json:"response_status"`
	ResponseHeaders   map[string]string  `json:"response_headers,omitempty"`
	ResponseBody      string             `json:"response_body,omitempty"`
	PageURL           string             `json:"page_url,omitempty"`
	PageTitle         string             `json:"page_title,omitempty"`
	HTML              string             `json:"html,omitempty"`
	ScreenshotBase64  string             `json:"screenshot_base64,omitempty"`
	ConsoleEntries    []ConsoleEntry     `json:"console_entries,omitempty"`
	NetworkEntries    []NetworkEntry     `json:"network_entries,omitempty"`
	DOMSummary        *DOMSummary        `json:"dom_summary,omitempty"`
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

// MessageCallbackSource exposes queued connection events without running user
// code on a protocol I/O goroutine. Generation invalidates events after close.
type CallbackMessage struct {
	Diagnostics   *WebSocketDiagnostics
	ReceivedAt    time.Time
	Generation    uint64
	Body          string
	MessageType   int
	BytesReceived int64
}
type MessageCallbackSource interface {
	DrainMessageCallbacks(context.Context, bool) ([]CallbackMessage, error)
	CallbackGeneration() uint64
}
