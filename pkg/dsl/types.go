package dsl

import "time"

type Script struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Protocol      string          `json:"protocol"`
	GoalConfig    *GoalConfig     `json:"goal_config,omitempty"`
	ProjectID     string          `json:"project_id,omitempty"`
	FolderID      *string         `json:"folder_id,omitempty"`
	GeneratedBy   string          `json:"generated_by,omitempty"`
	GeneratedFrom string          `json:"generated_from,omitempty"`
	Metadata      *ScriptMetadata `json:"metadata,omitempty"`
	Config        ScriptConfig    `json:"config"`
	DataSources   []DataSource    `json:"data_sources,omitempty"`
	Parameters    []Parameter     `json:"parameters,omitempty"`
	Init          []Action        `json:"init,omitempty"`
	Actions       []Action        `json:"actions"`
	Teardown      []Action        `json:"teardown,omitempty"`
	CreatedAt     time.Time       `json:"created_at,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at,omitempty"`
}

type GoalConfig struct {
	TargetAverageTPS         *float64 `json:"target_average_tps,omitempty"`
	P95LatencyMs             *float64 `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64 `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64 `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64 `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int     `json:"max_error_count,omitempty"`
	MinVUsers                *int     `json:"min_vusers,omitempty"`
	MaxVUsers                *int     `json:"max_vusers,omitempty"`
	StopOnSLABreach          bool     `json:"stop_on_sla_breach,omitempty"`
	StopOnMaxErrors          bool     `json:"stop_on_max_errors,omitempty"`
	SLABreachWindow          string   `json:"sla_breach_window,omitempty"`
}

type ScriptMetadata struct {
	Import *ImportMetadata `json:"import,omitempty"`
}

type ImportMetadata struct {
	Source           string         `json:"source,omitempty"`
	SourceScriptName string         `json:"source_script_name,omitempty"`
	Preserved        map[string]any `json:"preserved,omitempty"`
}

type ScriptConfig struct {
	BaseURL       string              `json:"base_url"`
	ThinkTime     ThinkTimeConfig     `json:"think_time,omitempty"`
	Pacing        PacingConfig        `json:"pacing,omitempty"`
	HTTP          HTTPConfig          `json:"http,omitempty"`
	WebRuntime    WebRuntimeConfig    `json:"web_runtime,omitempty"`
	JavaHTTP      JavaHTTPConfig      `json:"java_http,omitempty"`
	SOAP          SOAPConfig          `json:"soap,omitempty"`
	Browser       BrowserConfig       `json:"browser,omitempty"`
	JMS           JMSConfig           `json:"jms,omitempty"`
	ErrorHandling ErrorHandlingConfig `json:"error_handling,omitempty"`
	RunLogic      RunLogicConfig      `json:"run_logic,omitempty"`
	Observability ObservabilityConfig `json:"observability,omitempty"`
	Log           LogConfig           `json:"log,omitempty"`
}

type ObservabilityConfig struct {
	ExternalMetrics []ExternalMetricConfig `json:"external_metrics,omitempty"`
}

type ExternalMetricConfig struct {
	Name       string            `json:"name,omitempty"`
	URL        string            `json:"url,omitempty"`
	Format     string            `json:"format,omitempty"`
	MetricName string            `json:"metric_name,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type JavaHTTPConfig struct {
	Codec                string            `json:"codec,omitempty"`
	HelperMode           string            `json:"helper_mode,omitempty"`
	ContentType          string            `json:"content_type,omitempty"`
	Accept               string            `json:"accept,omitempty"`
	BodyEncoding         string            `json:"body_encoding,omitempty"`
	ObjectSchema         string            `json:"object_schema,omitempty"`
	RequestMapping       map[string]string `json:"request_mapping,omitempty"`
	ResponseMapping      map[string]string `json:"response_mapping,omitempty"`
	ExtractorType        string            `json:"extractor_type,omitempty"`
	PreserveBinaryResult bool              `json:"preserve_binary_result,omitempty"`
}

type SOAPConfig struct {
	Version       string                  `json:"version,omitempty"`
	Namespaces    map[string]string       `json:"namespaces,omitempty"`
	WSSecurity    *SOAPWSSecurityConfig   `json:"ws_security,omitempty"`
	FaultHandling SOAPFaultHandlingConfig `json:"fault_handling,omitempty"`
}

type SOAPWSSecurityConfig struct {
	UsernameToken *SOAPUsernameTokenConfig `json:"username_token,omitempty"`
	Timestamp     *SOAPTimestampConfig     `json:"timestamp,omitempty"`
}

type SOAPUsernameTokenConfig struct {
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	PasswordType string `json:"password_type,omitempty"`
	Nonce        bool   `json:"nonce,omitempty"`
	Created      bool   `json:"created,omitempty"`
}

type SOAPTimestampConfig struct {
	TTLSeconds int `json:"ttl_seconds,omitempty"`
}

type SOAPFaultHandlingConfig struct {
	FailOnFault bool `json:"fail_on_fault,omitempty"`
}

// JMSConfig holds broker, destination, and reliability settings for JMS scripts.
type JMSConfig struct {
	Provider        string            `json:"provider,omitempty"`         // "solace"
	BrokerURL       string            `json:"broker_url,omitempty"`       // e.g. tcp://broker:55555
	MessageVPN      string            `json:"message_vpn,omitempty"`      // Solace message VPN
	Username        string            `json:"username,omitempty"`         // broker username
	Password        string            `json:"password,omitempty"`         // broker password
	ClientID        string            `json:"client_id,omitempty"`        // durable client identifier
	Destination     string            `json:"destination,omitempty"`      // default queue/topic
	DestinationType string            `json:"destination_type,omitempty"` // "queue" or "topic"
	ReplyTo         string            `json:"reply_to,omitempty"`         // default reply destination
	Acknowledgement string            `json:"acknowledgement,omitempty"`  // "auto", "client", "transacted"
	DeliveryMode    string            `json:"delivery_mode,omitempty"`    // "persistent", "non_persistent"
	TimeoutMs       int               `json:"timeout_ms,omitempty"`       // default consume/request timeout
	Properties      map[string]string `json:"properties,omitempty"`       // provider-specific connection properties
	Mock            bool              `json:"mock,omitempty"`             // use in-memory broker for local shakeout
}

type BrowserConfig struct {
	Engine             string          `json:"engine,omitempty"`
	Headless           *bool           `json:"headless,omitempty"`
	SlowMoMs           int             `json:"slow_mo_ms,omitempty"`
	UserAgent          string          `json:"user_agent,omitempty"`
	DeviceProfile      string          `json:"device_profile,omitempty"`
	TouchMode          *bool           `json:"touch_mode,omitempty"`
	Orientation        string          `json:"orientation,omitempty"`
	ClientMetrics      *bool           `json:"client_metrics,omitempty"`
	CaptureScreenshots string          `json:"capture_screenshots,omitempty"`
	CacheEnabled       *bool           `json:"cache_enabled,omitempty"`
	CacheContentURLs   *bool           `json:"cache_content_urls,omitempty"`
	CheckNewerVersions *bool           `json:"check_newer_versions,omitempty"`
	NewUserPerIter     *bool           `json:"new_user_per_iteration,omitempty"`
	DownloadResources  string          `json:"download_resources,omitempty"`
	Viewport           BrowserViewport `json:"viewport,omitempty"`
}

type WebRuntimeConfig struct {
	AdditionalAttributes map[string]string       `json:"additional_attributes,omitempty"`
	DownloadFilters      DownloadFiltersConfig   `json:"download_filters,omitempty"`
	ContentChecks        []ContentCheckConfig    `json:"content_checks,omitempty"`
	NetworkSimulation    NetworkSimulationConfig `json:"network_simulation,omitempty"`
	Streaming            StreamingConfig         `json:"streaming,omitempty"`
	DataFormatting       DataFormattingConfig    `json:"data_formatting,omitempty"`
}

type DownloadFiltersConfig struct {
	Mode  string   `json:"mode,omitempty"`
	Hosts []string `json:"hosts,omitempty"`
}

type ContentCheckConfig struct {
	Text        string `json:"text,omitempty"`
	Scope       string `json:"scope,omitempty"`
	FailIfFound *bool  `json:"fail_if_found,omitempty"`
}

type NetworkSimulationConfig struct {
	Mode          string  `json:"mode,omitempty"`
	Preset        string  `json:"preset,omitempty"`
	BandwidthKbps int     `json:"bandwidth_kbps,omitempty"`
	LatencyMs     int     `json:"latency_ms,omitempty"`
	JitterMs      int     `json:"jitter_ms,omitempty"`
	PacketLossPct float64 `json:"packet_loss_pct,omitempty"`
}

type StreamingConfig struct {
	Mode                string `json:"mode,omitempty"`
	BufferSizeSeconds   int    `json:"buffer_size_seconds,omitempty"`
	FixedBitrateKbps    int    `json:"fixed_bitrate_kbps,omitempty"`
	BandwidthEstimation string `json:"bandwidth_estimation,omitempty"`
}

type DataFormattingConfig struct {
	AutoDetect bool           `json:"auto_detect,omitempty"`
	Chain      []DFETransform `json:"chain,omitempty"`
}

type DFETransform struct {
	Type    string            `json:"type,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

type BrowserViewport struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

type WaitCondition struct {
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty"`
}

type BrowserCaptureOptions struct {
	Screenshot bool `json:"screenshot,omitempty"`
	FullPage   bool `json:"full_page,omitempty"`
	HTML       bool `json:"html,omitempty"`
}

// RunLogicConfig controls iteration behavior and automatic transaction wrapping.
type RunLogicConfig struct {
	Iterations       int      `json:"iterations,omitempty"`        // 0 = run until duration (default)
	WorkloadModel    string   `json:"workload_model,omitempty"`    // "closed" (default), "open"
	AutoTransactions string   `json:"auto_transactions,omitempty"` // "none" (default), "per_action", "per_iteration"
	BlockScheduling  string   `json:"block_scheduling,omitempty"`  // "sequential", "random", "percentage"
	ActionSequence   []string `json:"action_sequence,omitempty"`   // e.g. ["init", "action", "teardown"]
}

// LogConfig controls per-VUser logging verbosity.
type LogConfig struct {
	Level           string   `json:"level,omitempty"`            // "disabled", "standard" (default), "extended"
	OnErrorOnly     bool     `json:"on_error_only,omitempty"`    // if true, use extended logging only when errors occur
	ExtendedOptions []string `json:"extended_options,omitempty"` // "parameter_substitution", "data_returned", "advanced_trace"
}

// HTTPConfig holds all HTTP protocol runtime settings, modeled after
// the configuration surface exposed by LoadRunner VuGen.
type HTTPConfig struct {
	// Connection
	Version         string `json:"version,omitempty"`            // "1.0", "1.1", "2" (default: auto-negotiate)
	MaxConnsPerHost int    `json:"max_conns_per_host,omitempty"` // default: 100
	MaxIdleConns    int    `json:"max_idle_conns,omitempty"`
	KeepAlive       *bool  `json:"keep_alive,omitempty"` // default: true

	// Timeouts (milliseconds)
	ConnectTimeoutMs int `json:"connect_timeout_ms,omitempty"` // default: 30000
	RequestTimeoutMs int `json:"request_timeout_ms,omitempty"` // default: 60000
	TLSTimeoutMs     int `json:"tls_timeout_ms,omitempty"`     // default: 10000
	IdleTimeoutMs    int `json:"idle_timeout_ms,omitempty"`    // default: 90000

	// TLS / SSL
	TLS TLSConfig `json:"tls,omitempty"`

	// Proxy
	ProxyMode string      `json:"proxy_mode,omitempty"` // "none", "system", "custom"
	Proxy     ProxyConfig `json:"proxy,omitempty"`

	// Authentication
	Auth AuthConfig `json:"auth,omitempty"`

	// Default headers applied to every request
	DefaultHeaders map[string]string `json:"default_headers,omitempty"`
	UserAgent      string            `json:"user_agent,omitempty"`
	AcceptEncoding string            `json:"accept_encoding,omitempty"` // "gzip, deflate, br"
	AcceptLanguage string            `json:"accept_language,omitempty"` // "en-US,en;q=0.9"

	// Redirects
	MaxRedirects *int `json:"max_redirects,omitempty"` // default: 10, -1 = disable

	// Cookies
	CookiesEnabled      *bool `json:"cookies_enabled,omitempty"` // default: true
	ClearCookiesPerIter *bool `json:"clear_cookies_per_iteration,omitempty"`
}

// TLSConfig controls TLS/SSL behavior for the HTTP client.
type TLSConfig struct {
	MinVersion         string `json:"min_version,omitempty"` // "1.0", "1.1", "1.2", "1.3"
	MaxVersion         string `json:"max_version,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"` // skip server cert validation
	ClientCertFile     string `json:"client_cert_file,omitempty"`     // path to PEM client cert
	ClientKeyFile      string `json:"client_key_file,omitempty"`      // path to PEM client key
	CACertFile         string `json:"ca_cert_file,omitempty"`         // path to custom CA bundle
}

// ProxyConfig defines HTTP/HTTPS proxy settings.
type ProxyConfig struct {
	URL      string   `json:"url,omitempty"`      // e.g. "http://proxy.corp:8080"
	Username string   `json:"username,omitempty"` // proxy basic auth
	Password string   `json:"password,omitempty"`
	NoProxy  []string `json:"no_proxy,omitempty"` // bypass list (hosts/CIDRs)
}

// AuthConfig defines default authentication applied to all requests.
type AuthConfig struct {
	Type     string `json:"type,omitempty"`     // "basic", "bearer", "ntlm", "digest"
	Username string `json:"username,omitempty"` // for basic/ntlm/digest
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`  // for bearer
	Domain   string `json:"domain,omitempty"` // for ntlm
}

// ErrorHandlingConfig controls how execution errors are handled.
type ErrorHandlingConfig struct {
	ContinueOnError bool   `json:"continue_on_error,omitempty"`
	MaxRetries      int    `json:"max_retries,omitempty"`       // default: 0
	RetryDelayMs    int    `json:"retry_delay_ms,omitempty"`    // default: 1000
	RetryBackoff    string `json:"retry_backoff,omitempty"`     // "fixed", "exponential"
	SnapshotOnError bool   `json:"snapshot_on_error,omitempty"` // capture req/resp on failure
}

type ThinkTimeConfig struct {
	Type       string  `json:"type"`
	MeanMs     float64 `json:"mean_ms"`
	StdMs      float64 `json:"std_ms,omitempty"`
	MinMs      float64 `json:"min_ms,omitempty"`
	MaxMs      float64 `json:"max_ms,omitempty"`
	Multiplier float64 `json:"multiplier,omitempty"` // 1.0 = as recorded, 0.5 = half, 2.0 = double
	LimitMs    float64 `json:"limit_ms,omitempty"`   // max think time cap (0 = no limit)
	Ignore     bool    `json:"ignore,omitempty"`     // skip all think times
}

type PacingConfig struct {
	Type          string  `json:"type"`                      // "none", "fixed_delay", "random_delay", "fixed_interval", "random_interval", "tps"
	DelayMs       int     `json:"delay_ms,omitempty"`        // for fixed_delay
	MinDelayMs    int     `json:"min_delay_ms,omitempty"`    // for random_delay
	MaxDelayMs    int     `json:"max_delay_ms,omitempty"`    // for random_delay
	IntervalMs    int     `json:"interval_ms,omitempty"`     // for fixed_interval
	MinIntervalMs int     `json:"min_interval_ms,omitempty"` // for random_interval
	MaxIntervalMs int     `json:"max_interval_ms,omitempty"` // for random_interval
	TPS           float64 `json:"tps,omitempty"`             // for tps mode
}

type DataSource struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"` // "csv", "dat"
	Path           string   `json:"path,omitempty"`
	InlineData     string   `json:"inline_data,omitempty"` // optional inline CSV/DAT content
	Strategy       string   `json:"strategy"`              // "sequential", "random", "unique", "pop"
	Delimiter      string   `json:"delimiter,omitempty"`   // default ","
	FirstRowHeader bool     `json:"first_row_header,omitempty"`
	Columns        []string `json:"columns,omitempty"` // explicit column names if no header
}

type Parameter struct {
	Name          string   `json:"name"`
	Type          string   `json:"type"` // "file", "list", "random_number", "date_time", "timestamp", "counter", "uuid", "random_string"
	Update        string   `json:"update,omitempty"`
	Selection     string   `json:"selection,omitempty"`
	OnExhaustion  string   `json:"on_exhaustion,omitempty"`
	DataSource    string   `json:"data_source,omitempty"`
	Column        string   `json:"column,omitempty"`
	Format        string   `json:"format,omitempty"`
	Timezone      string   `json:"timezone,omitempty"`
	OffsetSeconds int      `json:"offset_seconds,omitempty"`
	Min           int      `json:"min,omitempty"`
	Max           int      `json:"max,omitempty"`
	Start         int      `json:"start,omitempty"`
	End           int      `json:"end,omitempty"`
	Step          int      `json:"step,omitempty"`
	Length        int      `json:"length,omitempty"`
	Charset       string   `json:"charset,omitempty"`
	PadWidth      int      `json:"pad_width,omitempty"`
	PadChar       string   `json:"pad_char,omitempty"`
	Values        []string `json:"values,omitempty"`
	Seed          *int64   `json:"seed,omitempty"`
}

type Action struct {
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	Method         string                 `json:"method,omitempty"`
	Target         string                 `json:"target"`
	Selector       string                 `json:"selector,omitempty"`
	Value          string                 `json:"value,omitempty"`
	Headers        map[string]string      `json:"headers,omitempty"`
	Body           string                 `json:"body,omitempty"`
	Params         map[string]any         `json:"params,omitempty"`
	WaitFor        *WaitCondition         `json:"wait_for,omitempty"`
	Capture        *BrowserCaptureOptions `json:"capture,omitempty"`
	Metadata       map[string]any         `json:"metadata,omitempty"`
	Extractors     []Extractor            `json:"extractors,omitempty"`
	Assertions     []Assertion            `json:"assertions,omitempty"`
	ThinkTime      ThinkTimeConfig        `json:"think_time,omitempty"`
	ThinkTimeAfter bool                   `json:"think_time_after,omitempty"`
	// Per-action overrides
	Auth         *AuthConfig       `json:"auth,omitempty"`          // override global auth
	TimeoutMs    int               `json:"timeout_ms,omitempty"`    // override global request timeout
	MaxRedirects *int              `json:"max_redirects,omitempty"` // override global redirect policy
	SOAP         *SOAPActionConfig `json:"soap,omitempty"`
}

type SOAPActionConfig struct {
	Action    string `json:"action,omitempty"`
	Operation string `json:"operation,omitempty"`
	BodyMode  string `json:"body_mode,omitempty"`
}

type Extractor struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Type       string `json:"type"`
	Expression string `json:"expression"`
}

type Assertion struct {
	Type     string `json:"type"`
	Expected any    `json:"expected,omitempty"`
	Value    string `json:"value,omitempty"`
	Max      int    `json:"max,omitempty"`
}
