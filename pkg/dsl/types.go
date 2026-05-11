package dsl

import "time"

type Script struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Protocol      string          `json:"protocol"`
	ProjectID     string          `json:"project_id,omitempty"`
	FolderID      *string         `json:"folder_id,omitempty"`
	GeneratedBy   string          `json:"generated_by,omitempty"`
	GeneratedFrom string          `json:"generated_from,omitempty"`
	Metadata      *ScriptMetadata `json:"metadata,omitempty"`
	Config        ScriptConfig    `json:"config"`
	DataSources   []DataSource    `json:"data_sources,omitempty"`
	Init          []Action        `json:"init,omitempty"`
	Actions       []Action        `json:"actions"`
	Teardown      []Action        `json:"teardown,omitempty"`
	CreatedAt     time.Time       `json:"created_at,omitempty"`
	UpdatedAt     time.Time       `json:"updated_at,omitempty"`
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
	ErrorHandling ErrorHandlingConfig `json:"error_handling,omitempty"`
	RunLogic      RunLogicConfig      `json:"run_logic,omitempty"`
	Log           LogConfig           `json:"log,omitempty"`
}

// RunLogicConfig controls iteration behavior and automatic transaction wrapping.
type RunLogicConfig struct {
	Iterations       int    `json:"iterations,omitempty"`        // 0 = run until duration (default)
	AutoTransactions string `json:"auto_transactions,omitempty"` // "none" (default), "per_action", "per_iteration"
}

// LogConfig controls per-VUser logging verbosity.
type LogConfig struct {
	Level       string `json:"level,omitempty"`         // "disabled", "standard" (default), "extended"
	OnErrorOnly bool   `json:"on_error_only,omitempty"` // if true, use extended logging only when errors occur
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
	Proxy ProxyConfig `json:"proxy,omitempty"`

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
	Strategy       string   `json:"strategy"`            // "sequential", "random", "unique"
	Delimiter      string   `json:"delimiter,omitempty"` // default ","
	FirstRowHeader bool     `json:"first_row_header,omitempty"`
	Columns        []string `json:"columns,omitempty"` // explicit column names if no header
}

type Action struct {
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Method         string            `json:"method,omitempty"`
	Target         string            `json:"target"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	Params         map[string]any    `json:"params,omitempty"`
	Extractors     []Extractor       `json:"extractors,omitempty"`
	Assertions     []Assertion       `json:"assertions,omitempty"`
	ThinkTime      ThinkTimeConfig   `json:"think_time,omitempty"`
	ThinkTimeAfter bool              `json:"think_time_after,omitempty"`
	// Per-action overrides
	Auth         *AuthConfig `json:"auth,omitempty"`          // override global auth
	TimeoutMs    int         `json:"timeout_ms,omitempty"`    // override global request timeout
	MaxRedirects *int        `json:"max_redirects,omitempty"` // override global redirect policy
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
