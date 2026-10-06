package dsl

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
)

const (
	// ImportPreservedRuntimeArtifactKey records which protected source artifact
	// a compatibility runtime executes. API projections use it to avoid
	// disclosing a raw artifact identifier while retaining the durable
	// reference internally.
	ImportPreservedRuntimeArtifactKey = "runtime_source_artifact"
	// ImportPreservedCredentialPolicyKey marks a server-produced import draft
	// whose durable execution copy must retain authored credential values.
	ImportPreservedCredentialPolicyKey = "import_value_policy"
	// ImportPreservedCredentialPolicyPreserve is the only recognized policy
	// value. Keeping the value explicit makes older imported metadata inert.
	ImportPreservedCredentialPolicyPreserve = "preserve"
	// ImportHeaderFidelityMetadataKey identifies the inert, execution-gated
	// record of source header occurrences that the HTTP runtime cannot send.
	ImportHeaderFidelityMetadataKey = "import_header_fidelity"
	// ImportHeaderFidelityPreservedNameBase64Key and
	// ImportHeaderFidelityPreservedValueBase64Key carry the exact authored
	// name/value bytes for a runtime-incompatible occurrence. They are base64 so
	// arbitrary source bytes survive JSON persistence; API projections replace
	// both fields with a fixed masked payload.
	ImportHeaderFidelityPreservedNameBase64Key  = "preserved_name_base64"
	ImportHeaderFidelityPreservedValueBase64Key = "preserved_value_base64"
	// ImportMultipartFidelityMetadataKey identifies the inert,
	// execution-gated record of structured multipart fields that the HTTP
	// runtime cannot serialize. It reuses the preserved name/value byte keys
	// above so the disclosure and masked-write boundaries treat both fidelity
	// envelopes consistently.
	ImportMultipartFidelityMetadataKey = "import_multipart_fidelity"
	// ImportPreservedCredentialParameterNamesKey records source-variable names
	// whose values feed an executable credential surface despite having a
	// neutral name. Response projection uses this server-produced provenance to
	// mask the corresponding parameter values and data-source cells without
	// removing them from the durable replay copy.
	ImportPreservedCredentialParameterNamesKey = "credential_parameter_names"
	// ImportPreservedCredentialTargetsMetadataKey records disclosure-safe
	// locators for literal credential values intentionally retained in an
	// imported executable action. The runtime resolves these locators from the
	// action immediately before execution so errors, snapshots, and diagnostics
	// can redact the retained values without replacing them in the request.
	ImportPreservedCredentialTargetsMetadataKey = "import_credential_targets"
	// ImportPreservedJMeterJDBCSourcesKey retains the exact JDBC Connection
	// Configuration fields needed to repair an adapter-required database import.
	// It is inert runtime metadata and every ordinary disclosure projection masks
	// the complete envelope.
	ImportPreservedJMeterJDBCSourcesKey = "jmeter_jdbc_sources"
	// ImportPreservedEnterpriseUnsupportedProxySourceKey retains an exact,
	// base64-encoded source proxy record when its endpoint cannot be represented
	// in executable HTTP config. The proxy stays disabled and review-gated; the
	// protected record avoids forcing the operator to re-enter its credentials.
	ImportPreservedEnterpriseUnsupportedProxySourceKey = "enterprise_unsupported_proxy_source"
	// ImportPreservedEnterpriseJavaVMSourceKey retains exact source JVM rows
	// that could not be parsed or executed as-is. Values are base64 encoded and
	// inert; trusted persistence keeps them for repair while all disclosure
	// projections mask the envelope.
	ImportPreservedEnterpriseJavaVMSourceKey = "enterprise_java_vm_source"
	// ImportPreservedEnterpriseJavaJDBCSourceKey retains exact JDBC connection
	// material for adapter-unsupported or conflicting Java connections. The
	// envelope is inert and base64 encoded so repair never requires re-entry.
	ImportPreservedEnterpriseJavaJDBCSourceKey = "enterprise_java_jdbc_source"
	// ImportURLFormTemplateValueEncodingMetadataKey marks an imported
	// application/x-www-form-urlencoded body whose literal spans are already
	// escaped but whose runtime template values still need component escaping.
	ImportURLFormTemplateValueEncodingMetadataKey = "body_template_value_encoding"
	// ImportURLFormTemplateValueEncodingQueryComponent escapes each resolved
	// template as a form component while preserving imported literal spans.
	ImportURLFormTemplateValueEncodingQueryComponent = aurapayload.TemplateValueEncodingQueryComponent
	// ImportAuraJSONTemplateValueEncoding opts into bounded structural Aura
	// substitution through JSON strings and the outer form component. Invalid
	// or unsupported payloads fail before traffic; legacy modes are unchanged.
	ImportAuraJSONTemplateValueEncoding = aurapayload.TemplateValueEncodingJSONStrings
	// ImportAuraTemplateContractMetadataKey binds retained form-wire template
	// occurrences to value-free typed consumer and token fingerprints.
	ImportAuraTemplateContractMetadataKey = "body_aura_template_contract"
)

type Script struct {
	ArchivedAt    *time.Time      `json:"archived_at,omitempty"`
	ID            string          `json:"id"`
	RefKey        string          `json:"ref_key,omitempty"`
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
	TargetAverageTPS         *float64             `json:"target_average_tps,omitempty"`
	P95LatencyMs             *float64             `json:"p95_latency_ms,omitempty"`
	MaxErrorPct              *float64             `json:"max_error_pct,omitempty"`
	MaxAvgServerProcessingMs *float64             `json:"max_avg_server_processing_ms,omitempty"`
	MaxAvgTransferMs         *float64             `json:"max_avg_transfer_ms,omitempty"`
	MaxErrorCount            *int                 `json:"max_error_count,omitempty"`
	MinVUsers                *int                 `json:"min_vusers,omitempty"`
	MaxVUsers                *int                 `json:"max_vusers,omitempty"`
	StopOnSLABreach          bool                 `json:"stop_on_sla_breach,omitempty"`
	StopOnMaxErrors          bool                 `json:"stop_on_max_errors,omitempty"`
	SLABreachWindow          string               `json:"sla_breach_window,omitempty"`
	TransactionRules         []TransactionSLARule `json:"transaction_rules,omitempty"`
}

// TransactionSLARule is a terminal CI guardrail evaluated against the complete
// archived business-transaction stream. Transaction matching is deliberately
// exact; patterns would make migrations and automated gates ambiguous.
type TransactionSLARule struct {
	Population  string  `json:"population,omitempty"`
	Protocol    string  `json:"protocol,omitempty"`
	Group       string  `json:"group,omitempty"`
	Transaction string  `json:"transaction"`
	Metric      string  `json:"metric"`
	Comparator  string  `json:"comparator"`
	Threshold   float64 `json:"threshold"`
	Severity    string  `json:"severity,omitempty"`
}

type ScriptMetadata struct {
	Import        *ImportMetadata      `json:"import,omitempty"`
	RuntimePolicy *ScriptRuntimePolicy `json:"runtime_policy,omitempty"`
}

// ScriptRuntimePolicy is an execution-time, fail-closed boundary injected by a
// trusted controller. It is serialized with distributed script snapshots so
// generators enforce the same outbound-host policy after controller handoff.
// Author-supplied values can only narrow execution; the CI API always replaces
// them with the authoritative project policy before a run is materialized.
type ScriptRuntimePolicy struct {
	AllowedTargetHosts []string `json:"allowed_target_hosts,omitempty"`
}

type ImportMetadata struct {
	Source           string         `json:"source,omitempty"`
	SourceScriptName string         `json:"source_script_name,omitempty"`
	Preserved        map[string]any `json:"preserved,omitempty"`
}

type ScriptConfig struct {
	JMeter           *JMeterCompatibility `json:"jmeter,omitempty"`
	SamplerSchedules []SamplerSchedule    `json:"sampler_schedules,omitempty"`
	BaseURL          string               `json:"base_url"`
	ThinkTime        ThinkTimeConfig      `json:"think_time,omitempty"`
	Pacing           PacingConfig         `json:"pacing,omitempty"`
	Bruno            BrunoConfig          `json:"bruno,omitempty"`
	HTTP             HTTPConfig           `json:"http,omitempty"`
	WebRuntime       WebRuntimeConfig     `json:"web_runtime,omitempty"`
	WebSocket        WebSocketConfig      `json:"websocket,omitempty"`
	GRPC             GRPCConfig           `json:"grpc,omitempty"`
	JavaHTTP         JavaHTTPConfig       `json:"java_http,omitempty"`
	JavaVuser        JavaVuserConfig      `json:"java_vuser,omitempty"`
	SOAP             SOAPConfig           `json:"soap,omitempty"`
	Database         DatabaseConfig       `json:"database,omitempty"`
	Network          NetworkConfig        `json:"network,omitempty"`
	Browser          BrowserConfig        `json:"browser,omitempty"`
	// BrowserEmulation drives HTTP-transport replay identity (User-Agent /
	// Accept-Language) and browser-cache simulation. Nil means legacy behavior:
	// http.user_agent / http.accept_language / http.cache_enabled stay in
	// charge. The Playwright browser protocol ignores this section and keeps
	// using Browser.
	BrowserEmulation *BrowserEmulationConfig `json:"browser_emulation,omitempty"`
	SAPGUI           SAPGUIConfig            `json:"sapgui,omitempty"`
	JMS              JMSConfig               `json:"jms,omitempty"`
	Kafka            KafkaConfig             `json:"kafka,omitempty"`
	EventHubs        EventHubsConfig         `json:"eventhubs,omitempty"`
	ErrorHandling    ErrorHandlingConfig     `json:"error_handling,omitempty"`
	Results          ResultsConfig           `json:"results,omitempty"`
	RunLogic         RunLogicConfig          `json:"run_logic,omitempty"`
	Observability    ObservabilityConfig     `json:"observability,omitempty"`
	Log              LogConfig               `json:"log,omitempty"`
}

// ResultsConfig controls optional retained run evidence. It is deliberately
// independent from ErrorHandlingConfig: snapshot_on_error remains a debugger
// snapshot policy and does not become the archive-detail switch.
// CSVExport defines a native, per-run projection of complete action evidence.
type CSVExport struct {
	ID         string            `json:"id"`
	FileName   string            `json:"file_name"`
	ActionName string            `json:"action_name"`
	Columns    []CSVExportColumn `json:"columns"`
}

type CSVExportColumn struct {
	Name         string  `json:"name"`
	Extraction   string  `json:"extraction,omitempty"`
	Metric       string  `json:"metric,omitempty"`
	DefaultValue *string `json:"default_value,omitempty"`
}

// PropertyAnalytics explicitly promotes a finite set of non-sensitive extraction
// categories. Unlisted observations aggregate into other; absent values are missing.
type PropertyAnalytics struct {
	Name       string   `json:"name"`
	Extraction string   `json:"extraction"`
	Values     []string `json:"values"`
}

const FeaturePropertyAnalyticsV1 = "property_analytics_v1"

type ResultsConfig struct {
	PropertyAnalytics []PropertyAnalytics `json:"property_analytics,omitempty"`

	CSVExports    []CSVExport          `json:"csv_exports,omitempty"`
	MessageDetail *MessageDetailConfig `json:"message_detail,omitempty"`
}

// MessageDetailConfig is an author-requested capture envelope. Controller and
// generator operator policy may reduce these limits but never raise them.
// Absence preserves the legacy evidence path.
type MessageDetailConfig struct {
	IncludeResources *bool   `json:"include_resources,omitempty"` // retain already-fetched resource evidence within the parent capture budget
	Mode             string  `json:"mode" jsonschema:"enum=off,enum=errors,enum=sampled,enum=adaptive"`
	SampleRate       float64 `json:"sample_rate,omitempty" jsonschema:"minimum=0,maximum=1"`
	MaxItems         int     `json:"max_items,omitempty" jsonschema:"minimum=0,maximum=4096"`
	MaxItemBytes     int64   `json:"max_item_bytes,omitempty" jsonschema:"minimum=0,maximum=16777216"`
	MaxTotalBytes    int64   `json:"max_total_bytes,omitempty" jsonschema:"minimum=0,maximum=1073741824"`
}

// BrunoConfig binds a canonical APEX script to an immutable Bruno source
// revision. The source bundle remains the authority; the bruno_run action
// selects which part of that bundle to execute.
type BrunoConfig struct {
	BundleRef                      string             `json:"bundle_ref,omitempty"`
	BundleSHA256                   string             `json:"bundle_sha256,omitempty"`
	ManifestSHA256                 string             `json:"manifest_sha256,omitempty"`
	SourceFormat                   string             `json:"source_format,omitempty"`
	SourceRevision                 string             `json:"source_revision,omitempty"`
	CompatibilityVersion           string             `json:"compatibility_version,omitempty"`
	DependencySnapshotRef          string             `json:"dependency_snapshot_ref,omitempty"`
	DefaultEnvironment             string             `json:"default_environment,omitempty"`
	DefaultGlobalEnvironment       string             `json:"default_global_environment,omitempty"`
	Sandbox                        string             `json:"sandbox,omitempty"`
	RunnerProfile                  string             `json:"runner_profile,omitempty"`
	PersistSessionAcrossIterations bool               `json:"persist_session_across_iterations,omitempty"`
	Limits                         BrunoRuntimeLimits `json:"limits,omitempty"`
	SecretBindings                 map[string]string  `json:"secret_bindings,omitempty"`
}

// BrunoRuntimeLimits are author-controlled ceilings. Operator-owned runner
// policy may reduce these values but never raise them.
type BrunoRuntimeLimits struct {
	MaxJumps               int `json:"max_jumps,omitempty" jsonschema:"minimum=0,maximum=1000000"`
	MaxRunRequestDepth     int `json:"max_run_request_depth,omitempty" jsonschema:"minimum=0,maximum=1024"`
	MaxParallelRequests    int `json:"max_parallel_requests,omitempty" jsonschema:"minimum=0,maximum=100000"`
	MaxOutputBytes         int `json:"max_output_bytes,omitempty" jsonschema:"minimum=0,maximum=1073741824"`
	DefaultStreamTimeoutMs int `json:"default_stream_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=86400000"`
}

type ObservabilityConfig struct {
	ExternalMetrics []ExternalMetricConfig `json:"external_metrics,omitempty"`
	// TraceCarrier opts messaging protocols into W3C trace context: "w3c"
	// writes it on produce and archives consumed messages' producer links.
	// Empty or "none" leaves messages unchanged.
	TraceCarrier string `json:"trace_carrier,omitempty"`
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

type JavaVuserConfig struct {
	Enabled                bool     `json:"enabled,omitempty"`
	RunnerCommand          string   `json:"runner_command,omitempty"`
	HarnessJar             string   `json:"harness_jar,omitempty"`
	Classpath              []string `json:"classpath,omitempty" jsonschema:"maxItems=256"`
	WorkDir                string   `json:"work_dir,omitempty"`
	MaxProcesses           int      `json:"max_processes,omitempty"`
	StartupTimeoutMs       int      `json:"startup_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	ActionTimeoutMs        int      `json:"action_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	JDKSelection           string   `json:"jdk_selection,omitempty" jsonschema:"enum=internal,enum=specified"`
	JDKHome                string   `json:"jdk_home,omitempty" jsonschema:"maxLength=4096"`
	AdditionalVMParameters []string `json:"additional_vm_parameters,omitempty" jsonschema:"maxItems=128"`
	UseXBootClasspath      *bool    `json:"use_xbootclasspath,omitempty"`
	DedicatedClassLoader   *bool    `json:"dedicated_class_loader,omitempty"`
	// SkipParameterEvaluation disables the engine-side automatic parameter
	// snapshot (data-source rows + named .prm parameters) that is otherwise
	// injected as lr_parameters before dispatch to the JVM. Explicitly
	// authored lr_parameters entries are still delivered.
	SkipParameterEvaluation *bool `json:"skip_parameter_evaluation,omitempty"`
	// ClasspathAssets binds user-uploaded JAR files to individual Classpath
	// entries so an imported Java Vuser whose dependencies are not
	// pre-provisioned on the runner can still execute. The []string Classpath
	// above and its wildcard/separator validation are unaffected: this is a
	// parallel, entry-keyed sidecar record resolved by content hash. An entry
	// is only user-suppliable when the user explicitly attaches an asset to it;
	// unattached entries remain assumed operator-provisioned and fail closed.
	ClasspathAssets []JavaClasspathAsset `json:"classpath_assets,omitempty" jsonschema:"maxItems=256"`
	// ParamLeftBrace and ParamRightBrace are the parameter delimiters the
	// Java source uses in lr.eval_string (the .usr ParamLeftBrace and
	// ParamRightBrace); both empty means LoadRunner's default "{" and "}".
	ParamLeftBrace  string `json:"param_left_brace,omitempty" jsonschema:"maxLength=8"`
	ParamRightBrace string `json:"param_right_brace,omitempty" jsonschema:"maxLength=8"`
}

// JavaClasspathAsset binds one exact Java Vuser classpath entry to a
// user-uploaded JAR by content hash. SHA256 is the portable identity every
// runner (local or distributed) resolves against; Entry is the exact string in
// JavaVuserConfig.Classpath that this asset materializes.
type JavaClasspathAsset struct {
	Entry       string `json:"entry"`                  // exact string in Classpath this asset materializes
	FileName    string `json:"file_name,omitempty"`    // original upload name (display only)
	SHA256      string `json:"sha256"`                 // lowercase hex; portable key + integrity check
	SizeBytes   int64  `json:"size_bytes,omitempty"`   // uploaded size in bytes
	ContentType string `json:"content_type,omitempty"` // sniffed/declared content type
	StorageRef  string `json:"storage_ref,omitempty"`  // file://… controller/local-reachable source
	StagedAsset string `json:"staged_asset,omitempty"` // set by distributed staging; empty for local
}

type WebSocketConfig struct {
	CallbackIntervalMs int    `json:"callback_interval_ms,omitempty" jsonschema:"minimum=0,maximum=60000"`
	ReadTimeoutMs      int    `json:"read_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	WriteTimeoutMs     int    `json:"write_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	MaxMessageSize     int64  `json:"max_message_size,omitempty" jsonschema:"minimum=0,maximum=16777216"`
	Subprotocol        string `json:"subprotocol,omitempty"`
	Origin             string `json:"origin,omitempty"`
	Compression        bool   `json:"compression,omitempty"`
	Reconnect          bool   `json:"reconnect,omitempty"`
}

type GRPCConfig struct {
	ProtoFiles     []string          `json:"proto_files,omitempty"`
	UseReflection  bool              `json:"use_reflection,omitempty"`
	PlainText      bool              `json:"plain_text,omitempty"`
	MaxRecvMsgSize int               `json:"max_recv_msg_size,omitempty" jsonschema:"minimum=0,maximum=8388608"`
	TimeoutMs      int               `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Authority      string            `json:"authority,omitempty"`
}

const (
	MaxWebSocketMessageBytes       = 16 << 20
	MaxGRPCStreamMessages          = 10_000
	MaxGRPCStreamBytes             = 8 << 20
	MaxConfiguredProtocolTimeout   = time.Hour
	MaxConfiguredProtocolTimeoutMS = 3_600_000
	MaxErrorRetries                = 10
	MaxThinkTimeMultiplier         = 100
)

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

type DatabaseConfig struct {
	Driver             string `json:"driver,omitempty"`
	DSN                string `json:"dsn,omitempty"`
	DSNSecret          string `json:"dsn_secret,omitempty"`
	MaxOpenConns       int    `json:"max_open_conns,omitempty"`
	MaxIdleConns       int    `json:"max_idle_conns,omitempty"`
	ConnMaxLifetimeMs  int    `json:"conn_max_lifetime_ms,omitempty"`
	QueryTimeoutMs     int    `json:"query_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	MaxRows            int    `json:"max_rows,omitempty"`
	ReadOnly           bool   `json:"read_only,omitempty"`
	RollbackOnTeardown bool   `json:"rollback_on_teardown,omitempty"`
}

type NetworkConfig struct {
	Subprotocol string   `json:"subprotocol,omitempty"`
	Host        string   `json:"host,omitempty"`
	Port        int      `json:"port,omitempty"`
	Address     string   `json:"address,omitempty"`
	TimeoutMs   int      `json:"timeout_ms,omitempty"`
	TLS         bool     `json:"tls,omitempty"`
	Username    string   `json:"username,omitempty"`
	Password    string   `json:"password,omitempty"`
	From        string   `json:"from,omitempty"`
	To          []string `json:"to,omitempty"`
	BaseDN      string   `json:"base_dn,omitempty"`
}

type SAPGUIConfig struct {
	ConnectionString string `json:"connection_string,omitempty"`
	Client           string `json:"client,omitempty"`
	Language         string `json:"language,omitempty"`
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	AutoLogon        bool   `json:"auto_logon,omitempty"`
	SessionBehavior  string `json:"session_behavior,omitempty"`
	SyncTimeoutMs    int    `json:"sync_timeout_ms,omitempty"`
}

// JMSConfig holds broker, destination, and reliability settings for JMS scripts.
type JMSConfig struct {
	Provider        string            `json:"provider,omitempty"`                                          // "solace", "servicebus", or "ibmmq"
	BrokerURL       string            `json:"broker_url,omitempty"`                                        // e.g. tcp://broker:55555
	MessageVPN      string            `json:"message_vpn,omitempty"`                                       // Solace message VPN
	Username        string            `json:"username,omitempty"`                                          // broker username
	Password        string            `json:"password,omitempty"`                                          // broker password
	ClientID        string            `json:"client_id,omitempty"`                                         // durable client identifier
	Destination     string            `json:"destination,omitempty"`                                       // default queue/topic
	DestinationType string            `json:"destination_type,omitempty"`                                  // "queue" or "topic"
	Subscription    string            `json:"subscription,omitempty"`                                      // Service Bus topic subscription
	ReplyTo         string            `json:"reply_to,omitempty"`                                          // default reply destination
	Acknowledgement string            `json:"acknowledgement,omitempty"`                                   // "auto", "client", "transacted"
	DeliveryMode    string            `json:"delivery_mode,omitempty"`                                     // "persistent", "non_persistent"
	TimeoutMs       int               `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"` // default consume/request timeout
	Properties      map[string]string `json:"properties,omitempty"`                                        // provider-specific connection properties
	TLS             *JMSTLSConfig     `json:"tls,omitempty"`                                               // TLS material for real broker adapters
	Mock            bool              `json:"mock,omitempty"`                                              // use in-memory broker for local shakeout
}

type JMSTLSConfig struct {
	CACertSecret       string `json:"ca_cert_secret,omitempty"`
	TrustStoreRef      string `json:"trust_store_ref,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
}

// KafkaConfig holds broker, security, and producer/consumer settings for Kafka scripts.
type KafkaOAuthConfig struct {
	Mode                 string `json:"mode"`
	TokenURL             string `json:"token_url"`
	ClientID             string `json:"client_id"`
	ClientSecretRef      string `json:"client_secret_ref"`
	Scope                string `json:"scope,omitempty"`
	AuthenticationMethod string `json:"authentication_method,omitempty"`
}

type KafkaProducerConfig struct {
	Retries            *int `json:"retries,omitempty"`
	LingerMs           *int `json:"linger_ms,omitempty"`
	BatchBytes         *int `json:"batch_bytes,omitempty"`
	BufferBytes        *int `json:"buffer_bytes,omitempty"`
	AdmissionTimeoutMs *int `json:"admission_timeout_ms,omitempty"`
	DeliveryTimeoutMs  *int `json:"delivery_timeout_ms,omitempty"`
	RequestTimeoutMs   *int `json:"request_timeout_ms,omitempty"`
}

type KafkaConfig struct {
	ConsumerTransactions bool                 `json:"consumer_transactions,omitempty"` // Atomically commit consumed offsets and produced records (real adapter).
	Producer             *KafkaProducerConfig `json:"producer,omitempty"`
	OAuth                *KafkaOAuthConfig    `json:"oauth,omitempty"`
	Brokers              []string             `json:"brokers,omitempty"`                                                   // bootstrap brokers, e.g. ["broker:9092"]
	Profile              string               `json:"profile,omitempty"`                                                   // ""|eventhubs — Azure Event Hubs Kafka-endpoint profile (forces sasl_ssl + $ConnectionString PLAIN, 1 MiB cap)
	SecurityProtocol     string               `json:"security_protocol,omitempty"`                                         // plaintext|ssl|sasl_ssl|sasl_plaintext
	SASLMechanism        string               `json:"sasl_mechanism,omitempty"`                                            // plain|scram-sha-256|scram-sha-512|oauthbearer
	SASLUsername         string               `json:"sasl_username,omitempty"`                                             // SASL username
	SASLPassword         string               `json:"sasl_password,omitempty"`                                             // SASL password (inline)
	SASLPasswordSecret   string               `json:"sasl_password_secret,omitempty"`                                      // secret reference resolved at runtime
	OAuthScope           string               `json:"oauth_scope,omitempty"`                                               // Managed-identity OAUTHBEARER is Event Hubs-only and fixed to https://eventhubs.azure.net/.default.
	TLS                  *KafkaTLSConfig      `json:"tls,omitempty"`                                                       // TLS material for ssl/sasl_ssl
	ClientID             string               `json:"client_id,omitempty"`                                                 // Kafka client.id
	ConsumerGroup        string               `json:"consumer_group,omitempty"`                                            // consumer group id for kafka_consume
	Topic                string               `json:"topic,omitempty"`                                                     // default topic
	Acks                 string               `json:"acks,omitempty"`                                                      // none|leader|all
	Idempotence          string               `json:"idempotence,omitempty"`                                               // ""(default)|enabled|disabled — idempotent producer (requires acks=all)
	TransactionalID      string               `json:"transactional_id,omitempty"`                                          // producer transactional.id; enables Kafka transactions / EOS (requires acks=all)
	Compression          string               `json:"compression,omitempty"`                                               // none|gzip|snappy|lz4|zstd
	PartitionStrategy    string               `json:"partition_strategy,omitempty"`                                        // key-hash|round-robin|explicit
	AutoOffsetReset      string               `json:"auto_offset_reset,omitempty"`                                         // earliest|latest
	IsolationLevel       string               `json:"isolation_level,omitempty"`                                           // ""(default)|read_uncommitted|read_committed (consumer)
	Partitions           int                  `json:"partitions,omitempty" jsonschema:"minimum=0,maximum=1000000"`         // partition count hint for the mock broker
	TimeoutMs            int                  `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`         // default consume/request timeout
	MaxMessageBytes      int                  `json:"max_message_bytes,omitempty" jsonschema:"minimum=0,maximum=67108864"` // producer-side max record size; Event Hubs caps at 1 MiB (Std/Prem), 20 MiB (Dedicated)
	Serialization        string               `json:"serialization,omitempty"`                                             // ""(raw)|json_schema|avro — Confluent Schema Registry wire-format serde
	SchemaRegistryURL    string               `json:"schema_registry_url,omitempty"`                                       // Confluent Schema Registry base URL; registers the subject to obtain the schema id
	Subject              string               `json:"subject,omitempty"`                                                   // explicit registry subject; defaults to <topic>-value
	Schema               string               `json:"schema,omitempty"`                                                    // schema document registered for the record value
	SchemaID             int                  `json:"schema_id,omitempty" jsonschema:"minimum=0,maximum=2147483647"`       // static schema id to frame with (alternative to schema_registry_url)
	Properties           map[string]string    `json:"properties,omitempty"`                                                // additional client properties
	Mock                 bool                 `json:"mock,omitempty"`                                                      // use in-memory broker for local shakeout
}

// KafkaTLSConfig holds TLS material for ssl/sasl_ssl security protocols.
type KafkaTLSConfig struct {
	MinVersion         string `json:"min_version,omitempty"`
	MaxVersion         string `json:"max_version,omitempty"`
	InsecureSkipVerify bool   `json:"insecure_skip_verify,omitempty"`
	ClientCertFile     string `json:"client_cert_file,omitempty"`
	ClientKeyFile      string `json:"client_key_file,omitempty"`
	CACertFile         string `json:"ca_cert_file,omitempty"`
}

// EventHubsConfig configures the native Azure Event Hubs protocol (distinct from
// the Kafka-endpoint profile under config.kafka). The real azeventhubs adapter is
// opt-in (build -tags eventhubs_real); the default build uses the in-memory mock.
type EventHubsConfig struct {
	ObservePartitionTail   bool   `json:"observe_partition_tail,omitempty"`                                                 // Opt-in management-plane tail queries in processor mode.
	TailPollIntervalMs     int    `json:"tail_poll_interval_ms,omitempty" jsonschema:"minimum=0,maximum=300000"`            // Zero uses 10 seconds; explicit intervals are 10 seconds to 5 minutes.
	ProcessorMaxPartitions int    `json:"processor_max_partitions,omitempty" jsonschema:"minimum=0,maximum=32"`             // Immutable partition-worker ceiling per processor; omitted uses 32.
	OwnerLevel             *int64 `json:"owner_level,omitempty" jsonschema:"minimum=0,maximum=9007199254740991"`            // Explicit exclusive consumer epoch; absent for observer and processor.
	ConsumerMode           string `json:"consumer_mode,omitempty" jsonschema:"enum=observer,enum=processor,enum=exclusive"` // observer default; processor checkpoints or explicitly scoped exclusive epoch.
	CheckpointContainerURL string `json:"checkpoint_container_url,omitempty" jsonschema:"maxLength=1024"`                   // Operator-approved existing Blob container; processor AAD authentication.
	Namespace              string `json:"namespace,omitempty"`                                                              // <ns>.servicebus.windows.net (AAD auth)
	EventHub               string `json:"event_hub,omitempty"`                                                              // default event hub (the "topic")
	ConnectionString       string `json:"connection_string,omitempty"`                                                      // SAS connection string (inline)
	ConnectionStringSecret string `json:"connection_string_secret,omitempty"`                                               // secret reference resolved at runtime
	ConsumerGroup          string `json:"consumer_group,omitempty"`                                                         // consumer group for the observer probe; defaults to $Default
	PartitionKey           string `json:"partition_key,omitempty"`                                                          // default partition key (hash routing)
	PartitionID            string `json:"partition_id,omitempty"`                                                           // default explicit partition id
	StartPosition          string `json:"start_position,omitempty"`                                                         // observer-probe start: latest (default, tail) | earliest (partition start, for produce→consume round-trips)
	AuthType               string `json:"auth_type,omitempty"`                                                              // connection_string | aad
	TimeoutMs              int    `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`                      // default send/receive timeout
	Mock                   bool   `json:"mock,omitempty"`                                                                   // use in-memory broker for local shakeout
}

// BrowserEmulationConfig selects the browser identity HTTP-transport replay
// presents to the server and the browser-cache behavior it simulates.
// Mode "" leaves everything to the legacy http.* fields; "specific" derives
// User-Agent/Accept-Language from the built-in browser catalog; "custom" uses
// CustomUserAgent verbatim.
type BrowserEmulationConfig struct {
	Mode            string                        `json:"mode,omitempty" jsonschema:"enum=specific,enum=custom"`
	BrowserType     string                        `json:"browser_type,omitempty" jsonschema:"enum=chrome,enum=edge,enum=firefox,enum=safari,enum=ie"`
	BrowserVersion  string                        `json:"browser_version,omitempty" jsonschema:"maxLength=32"`
	Platform        string                        `json:"platform,omitempty" jsonschema:"enum=windows,enum=macos,enum=linux,enum=android,enum=ios"`
	Language        string                        `json:"language,omitempty" jsonschema:"maxLength=64"`
	CustomUserAgent string                        `json:"custom_user_agent,omitempty" jsonschema:"maxLength=1024"`
	Cache           *BrowserCacheSimulationConfig `json:"cache,omitempty"`
}

// BrowserCacheSimulationConfig mirrors LoadRunner's Browser Emulation cache
// checkboxes. All fields are tri-state: nil inherits the engine default
// (http.cache_enabled for SimulateCache, off for the rest).
type BrowserCacheSimulationConfig struct {
	ClearCacheForNewUser *bool `json:"clear_cache_for_new_user,omitempty"` // nil preserves the default: clear

	// SimulateCache toggles the per-VUser HTTP response cache. Explicit false
	// wins over http.cache_enabled=true.
	SimulateCache *bool `json:"simulate_cache,omitempty"`
	// CacheContentURLs limits cache storage to content-bearing (HTML) responses
	// — LR "Cache URLs requiring content (HTMLs)".
	CacheContentURLs *bool `json:"cache_content_urls,omitempty"`
	// CheckNewerVersions revalidates stored pages on every visit even while
	// fresh — LR "Check for newer versions of stored pages every visit".
	CheckNewerVersions *bool `json:"check_newer_versions,omitempty"`
	// DownloadNonHTML fetches embedded page resources (images/CSS/JS/…) after
	// each HTML response — LR "Download non-HTML resources".
	DownloadNonHTML *bool `json:"download_non_html_resources,omitempty"`
	// NewUserEachIteration resets cookies (and cache unless explicitly retained) at each
	// iteration — LR "Simulate a new user on each iteration".
	NewUserEachIteration *bool `json:"new_user_each_iteration,omitempty"`
	// SimulatePrefetch fetches link rel=prefetch/prerender/preload targets
	// after page resources — LR "Simulate browser prefetch and prerender".
	// Independent of SimulateCache: the server-side load is issued either way.
	SimulatePrefetch *bool `json:"simulate_prefetch,omitempty"`
}

type BrowserConfig struct {
	// StorageState is a whole project-secret reference resolving to a Playwright
	// cookies/localStorage JSON snapshot captured before the tested transaction.
	StorageState string `json:"storage_state,omitempty"`

	SystemLanguage        string `json:"system_language,omitempty"`
	AvailableScreenWidth  int    `json:"available_screen_width,omitempty" jsonschema:"minimum=0,maximum=8192"`
	AvailableScreenHeight int    `json:"available_screen_height,omitempty" jsonschema:"minimum=0,maximum=8192"`

	Engine           string `json:"engine,omitempty"`
	Headless         *bool  `json:"headless,omitempty"`
	SlowMoMs         int    `json:"slow_mo_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	UserAgent        string `json:"user_agent,omitempty"`
	Locale           string `json:"locale,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	DeviceProfile    string `json:"device_profile,omitempty"`
	TouchMode        *bool  `json:"touch_mode,omitempty"`
	Orientation      string `json:"orientation,omitempty"`
	ClientMetrics    *bool  `json:"client_metrics,omitempty"`
	ExperienceCanary bool   `json:"experience_canary,omitempty"`
	// Traceparent injects a fresh W3C traceparent header per action into
	// browser-originated requests so waterfall entries correlate with backend
	// traces. Opt-in: the extra header can introduce CORS preflights real user
	// traffic would not have.
	Traceparent *bool `json:"traceparent,omitempty"`
	// HARCapture controls session network capture for diagnostics:
	// "off" (default), "on_error", "on_sla_breach", "manual", or "always".
	// Captured HARs pass through scrub-on-write before persistence.
	HARCapture         string                     `json:"har_capture,omitempty"`
	HARCapturePolicy   string                     `json:"har_capture_policy,omitempty"`
	HARSLAThresholdMs  int                        `json:"har_sla_threshold_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	HARArtifactDir     string                     `json:"har_artifact_dir,omitempty"`
	CaptureScreenshots string                     `json:"capture_screenshots,omitempty"`
	CacheEnabled       *bool                      `json:"cache_enabled,omitempty"`
	CacheContentURLs   *bool                      `json:"cache_content_urls,omitempty"`
	CheckNewerVersions *bool                      `json:"check_newer_versions,omitempty"`
	NewUserPerIter     *bool                      `json:"new_user_per_iteration,omitempty"`
	DownloadResources  string                     `json:"download_resources,omitempty"`
	Viewport           BrowserViewport            `json:"viewport,omitempty"`
	Geolocation        BrowserGeolocation         `json:"geolocation,omitempty"`
	HTTPCredentials    BrowserHTTPCredentials     `json:"http_credentials,omitempty"`
	Proxy              BrowserProxyConfig         `json:"proxy,omitempty"`
	ClientCertificates []BrowserClientCertificate `json:"client_certificates,omitempty"`
	ReviewPolicy       BrowserReviewPolicy        `json:"review_policy,omitempty"`
}

type BrowserGeolocation struct {
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	Accuracy  *float64 `json:"accuracy,omitempty"`
}

type BrowserHTTPCredentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Origin   string `json:"origin,omitempty"`
	Send     string `json:"send,omitempty"`
}

type BrowserProxyConfig struct {
	Server   string `json:"server,omitempty"`
	Bypass   string `json:"bypass,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type BrowserClientCertificate struct {
	Origin     string `json:"origin,omitempty"`
	CertPath   string `json:"cert_path,omitempty"`
	KeyPath    string `json:"key_path,omitempty"`
	PfxPath    string `json:"pfx_path,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

type BrowserReviewPolicy struct {
	AllowReviewedJavaScript bool `json:"allow_reviewed_javascript,omitempty"`
	AllowReviewRequired     bool `json:"allow_review_required,omitempty"`
}

type WebRuntimeConfig struct {
	AdditionalAttributes map[string]string       `json:"additional_attributes,omitempty"`
	DownloadFilters      DownloadFiltersConfig   `json:"download_filters,omitempty"`
	ContentChecks        []ContentCheckConfig    `json:"content_checks,omitempty"`
	NetworkSimulation    NetworkSimulationConfig `json:"network_simulation,omitempty"`
	Streaming            StreamingConfig         `json:"streaming,omitempty"`
	DataFormatting       DataFormattingConfig    `json:"data_formatting,omitempty"`
	SourceIPs            []string                `json:"source_ips,omitempty"`
	NetworkProfiles      []NetworkProfileWeight  `json:"network_profiles,omitempty"`
}

type DownloadFiltersConfig struct {
	Mode  string   `json:"mode,omitempty"`
	Hosts []string `json:"hosts,omitempty"`
}

type ContentCheckConfig struct {
	LeftBoundary  string `json:"left_boundary,omitempty"`
	RightBoundary string `json:"right_boundary,omitempty"`

	Text            string `json:"text,omitempty"`
	Scope           string `json:"scope,omitempty"`
	FailIfFound     *bool  `json:"fail_if_found,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

type NetworkSimulationConfig struct {
	Mode          string  `json:"mode,omitempty"`
	Preset        string  `json:"preset,omitempty"`
	BandwidthKbps int     `json:"bandwidth_kbps,omitempty" jsonschema:"minimum=0,maximum=100000000"`
	LatencyMs     int     `json:"latency_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	JitterMs      int     `json:"jitter_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	PacketLossPct float64 `json:"packet_loss_pct,omitempty" jsonschema:"minimum=0,maximum=100"`
}

type NetworkProfileWeight struct {
	Name                    string `json:"name"`
	Weight                  int    `json:"weight"`
	NetworkSimulationConfig        // embedded: mode/preset/bandwidth/latency/jitter/loss
}

type StreamingConfig struct {
	Mode                string `json:"mode,omitempty"`
	BufferSizeSeconds   int    `json:"buffer_size_seconds,omitempty" jsonschema:"minimum=0,maximum=3600"`
	FixedBitrateKbps    int    `json:"fixed_bitrate_kbps,omitempty" jsonschema:"minimum=0,maximum=100000000"`
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
	Width  int `json:"width,omitempty" jsonschema:"minimum=0,maximum=8192"`
	Height int `json:"height,omitempty" jsonschema:"minimum=0,maximum=8192"`
}

type WaitCondition struct {
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
}

type BrowserSyncPolicy struct {
	Type      string `json:"type,omitempty"`
	Value     string `json:"value,omitempty"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	StableMs  int    `json:"stable_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
}

type BrowserCaptureOptions struct {
	Screenshot bool `json:"screenshot,omitempty"`
	FullPage   bool `json:"full_page,omitempty"`
	HTML       bool `json:"html,omitempty"`
}

// RunLogicConfig controls iteration behavior and automatic transaction wrapping.
type RunLogicConfig struct {
	TerminationMode  string   `json:"termination_mode,omitempty"`
	Iterations       int      `json:"iterations,omitempty"`        // 0 = run until duration (default)
	GlobalIterations int      `json:"global_iterations,omitempty"` // 0 = no shared iteration cap across VUsers
	MaxTransactions  int64    `json:"max_transactions,omitempty"`  // 0 = no shared transaction cap across VUsers
	WorkloadModel    string   `json:"workload_model,omitempty"`    // "closed" (default), "open"
	MaxInFlight      int      `json:"max_in_flight,omitempty"`     // open model only: max concurrent in-flight arrival workers (0 = derive a safe default from TPS)
	AutoTransactions string   `json:"auto_transactions,omitempty"` // "none" (default), "per_action", "per_iteration"
	BlockScheduling  string   `json:"block_scheduling,omitempty"`  // "sequential", "random", "percentage"
	ActionSequence   []string `json:"action_sequence,omitempty"`   // e.g. ["init", "action", "teardown"]
}

// LogConfig controls per-VUser logging verbosity.
type LogConfig struct {
	ErrorBufferBytes int `json:"error_buffer_bytes,omitempty" jsonschema:"minimum=0,maximum=1048576"` // zero uses 100 KiB in error-only mode
	MaxResponseBytes int `json:"max_response_bytes,omitempty" jsonschema:"minimum=0,maximum=1048576"` // zero keeps the engine evidence bound

	Level           string   `json:"level,omitempty"`            // "disabled", "standard" (default), "extended"
	OnErrorOnly     bool     `json:"on_error_only,omitempty"`    // if true, use extended logging only when errors occur
	ExtendedOptions []string `json:"extended_options,omitempty"` // "parameter_substitution", "data_returned", "advanced_trace"
	// RedactRunEvidence controls whether credential values that came from the
	// script itself are redacted out of RUN OUTPUT — logs, snapshots, reports,
	// archives and debugger evidence. Default false.
	//
	// It governs output only. The script is never altered: an imported script
	// keeps its literal credentials so it stays runnable, which is why turning
	// this off is a legitimate operator choice rather than a footgun — the
	// operator can already read every one of those values in the script.
	//
	// It does NOT govern platform-managed secrets. Values resolved from the
	// project secret store through {{secret.NAME}} are redacted regardless,
	// because the operator does not hold those. Name-based suppression
	// (scrub.SensitiveName) and the generic JWT/high-entropy scrubbers are
	// likewise unaffected.
	RedactRunEvidence *bool `json:"redact_run_evidence,omitempty"` // default: false
}

// HTTPConfig holds all HTTP protocol runtime settings, modeled after
// the configuration surface exposed by LoadRunner VuGen.
type HTTPConfig struct {
	ReceiveBufferBytes     int `json:"receive_buffer_bytes,omitempty" jsonschema:"minimum=0,maximum=1048576"`
	MaxMetaRefresh         int `json:"max_meta_refresh,omitempty" jsonschema:"minimum=0,maximum=50"`
	ContentCheckErrorLimit int `json:"content_check_error_limit,omitempty" jsonschema:"minimum=0,maximum=1000"`

	// Replay outcome policies preserve explicit false values from source profiles.
	HTTPErrorsAsWarnings     *bool `json:"http_errors_as_warnings,omitempty"`
	FailNonCriticalResources *bool `json:"fail_noncritical_resources,omitempty"`
	ResourceTimeoutAsWarning *bool `json:"resource_timeout_as_warning,omitempty"`
	LegacyHTMLChecks         *bool `json:"legacy_html_checks,omitempty"`
	DNSCache                 *bool `json:"dns_cache,omitempty"` // per-VUser, bounded to 256 hosts and 60 seconds
	AuthDelayMs              int   `json:"challenge_delay_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`

	// PageTimeoutMs bounds the complete request plus embedded-resource operation.
	PageTimeoutMs int `json:"page_timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	// IPVersion selects the address family for outgoing TCP connections, including proxy connections.
	IPVersion string `json:"ip_version,omitempty" jsonschema:"enum=,enum=auto,enum=ipv4,enum=ipv6"`
	// CacheMaxUnusedIterations evicts entries after this many unreferenced main iterations; zero disables aging.
	CacheMaxUnusedIterations int      `json:"cache_max_unused_iterations,omitempty" jsonschema:"minimum=0,maximum=1000000"`
	CacheExcludedMIMETypes   []string `json:"cache_excluded_mime_types,omitempty"`
	// HTMLParseMode controls resource discovery; empty preserves the native HTML/XHTML default.
	HTMLParseMode         string   `json:"html_parse_mode,omitempty" jsonschema:"enum=,enum=html,enum=text,enum=any"`
	HTMLExcludedMIMETypes []string `json:"html_excluded_mime_types,omitempty"`
	// AuraRecordedFailures decides how aura_actions_success treats actions that
	// already failed in the recording: "require_success" (the empty default)
	// fails them, labelled as recorded failures; "accept_recorded" accepts a
	// replay that ends in the same unsuccessful state as the recording.
	AuraRecordedFailures string `json:"aura_recorded_failures,omitempty" jsonschema:"enum=,enum=require_success,enum=accept_recorded"`

	// Connection
	Version         string `json:"version,omitempty" jsonschema:"enum=,enum=1.1,enum=2"` // "1.1" exact, "2" required/no fallback; empty auto-negotiates HTTP/1.1 or HTTP/2
	MaxConnsPerHost int    `json:"max_conns_per_host,omitempty"`                         // default: 100
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
	DefaultHeaders    map[string]string `json:"default_headers,omitempty"`
	DefaultHeaderList HeaderList        `json:"default_header_list,omitempty"`
	UserAgent         string            `json:"user_agent,omitempty"`
	AcceptEncoding    string            `json:"accept_encoding,omitempty"` // "gzip, deflate, br"
	AcceptLanguage    string            `json:"accept_language,omitempty"` // "en-US,en;q=0.9"

	// Redirects
	MaxRedirects *int `json:"max_redirects,omitempty"` // default: 10, -1 = disable

	// Cookies
	CookiesEnabled      *bool `json:"cookies_enabled,omitempty"` // default: true
	ClearCookiesPerIter *bool `json:"clear_cookies_per_iteration,omitempty"`
	CacheEnabled        *bool `json:"cache_enabled,omitempty"` // enable per-VUser HTTP response cache emulation
	Traceparent         *bool `json:"traceparent,omitempty"`   // propagate active W3C context, or synthesize a fallback header (default: true)
	TraceparentSampled  *bool `json:"traceparent_sampled,omitempty"`
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
	PACURL   string   `json:"pac_url,omitempty"`
	URL      string   `json:"url,omitempty"`      // e.g. "http://proxy.corp:8080"
	Username string   `json:"username,omitempty"` // proxy basic auth
	Password string   `json:"password,omitempty"`
	NoProxy  []string `json:"no_proxy,omitempty"` // bypass list (hosts/CIDRs)
}

// AuthConfig defines default authentication applied to all requests.
type AuthConfig struct {
	Type     string `json:"type,omitempty"`     // "basic", "bearer", or "digest"; imported "ntlm"/"kerberos"/"negotiate" are review-only and fail closed at HTTP runtime
	Username string `json:"username,omitempty"` // for basic/ntlm/digest
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`  // for bearer
	Domain   string `json:"domain,omitempty"` // for ntlm
}

// ErrorHandlingConfig controls how execution errors are handled.
type ErrorHandlingConfig struct {
	FailTransactionOnErrorMessage *bool `json:"fail_transaction_on_error_message,omitempty"`

	// ContinueOnError is tri-state: nil means "not set at this layer" and lets a
	// higher layer (the scenario's continue-on-error policy) supply the value,
	// while a non-nil false is an explicit opt-out that the scenario default must
	// not silently overturn. Pointer + omitempty keeps an explicit false on the
	// wire, which a plain bool could never round-trip.
	ContinueOnError *bool  `json:"continue_on_error,omitempty"`
	MaxRetries      int    `json:"max_retries,omitempty" jsonschema:"minimum=0,maximum=10"`         // default: 0
	RetryDelayMs    int    `json:"retry_delay_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"` // default: 1000
	RetryBackoff    string `json:"retry_backoff,omitempty"`                                         // "fixed", "exponential"
	SnapshotOnError bool   `json:"snapshot_on_error,omitempty"`                                     // capture req/resp on failure
}

// ContinueOnErrorEnabled reports the effective continue-on-error behavior for a
// script that is executed as-is. An unset value falls back to the engine's
// built-in default (stop the iteration on the first error); the scenario layer
// resolves its own, more permissive default before execution.
func (c ErrorHandlingConfig) ContinueOnErrorEnabled() bool {
	return c.ContinueOnError != nil && *c.ContinueOnError
}

// ContinueOnErrorSet reports whether this layer states a continue-on-error
// intent at all, which is what separates an explicit opt-out from silence.
func (c ErrorHandlingConfig) ContinueOnErrorSet() bool {
	return c.ContinueOnError != nil
}

// ContinueOnErrorValue returns a settable pointer for the given value.
func ContinueOnErrorValue(enabled bool) *bool { return &enabled }

type ThinkTimeConfig struct {
	MinPercent float64 `json:"min_percent,omitempty" jsonschema:"minimum=0,maximum=10000"` // random_percentage: percentage of each recorded delay
	MaxPercent float64 `json:"max_percent,omitempty" jsonschema:"minimum=0,maximum=10000"`
	Type       string  `json:"type"`
	MeanMs     float64 `json:"mean_ms" jsonschema:"minimum=0,maximum=3600000"`
	StdMs      float64 `json:"std_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	MinMs      float64 `json:"min_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	MaxMs      float64 `json:"max_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`
	Multiplier float64 `json:"multiplier,omitempty" jsonschema:"exclusiveMinimum=0,maximum=100"` // omitted/0 in Go inherits 1; authored JSON must be >0
	LimitMs    float64 `json:"limit_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`        // max think time cap (0 = no limit)
	Ignore     bool    `json:"ignore,omitempty"`                                                 // skip all think times
	// Force applies this delay exactly whatever the script's think-time
	// settings (ignore, multiplier, limit, percentage) say, as LoadRunner's
	// lr_force_think_time does. Meaningful on an action's think_time only.
	Force bool `json:"force,omitempty"`
}

type PacingConfig struct {
	Type          string  `json:"type"`                                                             // "none", "fixed_delay", "random_delay", "fixed_interval", "random_interval", "tps"
	DelayMs       int     `json:"delay_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`        // for fixed_delay
	MinDelayMs    int     `json:"min_delay_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`    // for random_delay
	MaxDelayMs    int     `json:"max_delay_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`    // for random_delay
	IntervalMs    int     `json:"interval_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"`     // for fixed_interval
	MinIntervalMs int     `json:"min_interval_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"` // for random_interval
	MaxIntervalMs int     `json:"max_interval_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"` // for random_interval
	TPS           float64 `json:"tps,omitempty"`                                                    // for tps mode
}

type DataSource struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"` // "csv", "dat", "file"
	Path           string   `json:"path,omitempty"`
	InlineData     string   `json:"inline_data,omitempty"` // optional inline CSV/DAT content
	Encoding       string   `json:"encoding,omitempty"`    // for file inline_data: "text" (default) or "base64"
	FileName       string   `json:"file_name,omitempty"`   // original asset filename for file data sources
	ContentType    string   `json:"content_type,omitempty"`
	StorageRef     string   `json:"storage_ref,omitempty"`  // governed asset reference, e.g. file://...
	StagedAsset    string   `json:"staged_asset,omitempty"` // controller-staged distributed asset (internal execution boundary)
	Strategy       string   `json:"strategy"`               // "sequential", "random", "unique", "pop"
	Delimiter      string   `json:"delimiter,omitempty"`    // default ","; accepts "auto", "comma", "tab"
	FirstRowHeader bool     `json:"first_row_header"`
	Columns        []string `json:"columns,omitempty"` // explicit column names if no header
}

type Parameter struct {
	Scope             string   `json:"scope,omitempty"`
	ScopeID           string   `json:"scope_id,omitempty"`
	RandomAlgorithm   string   `json:"random_algorithm,omitempty"`
	FormatDialect     string   `json:"format_dialect,omitempty"`
	Name              string   `json:"name"`
	Type              string   `json:"type"` // "file", "list", "random_number", "date_time", "timestamp", "counter", "current_iteration", "vuser_id", "host", "time", "uuid", "random_string"
	Update            string   `json:"update,omitempty"`
	Selection         string   `json:"selection,omitempty"`
	OnExhaustion      string   `json:"on_exhaustion,omitempty"`
	SameLineAs        string   `json:"same_line_as,omitempty"`
	DataSource        string   `json:"data_source,omitempty"`
	Column            string   `json:"column,omitempty"`
	StartRow          int      `json:"start_row,omitempty"`
	BlockSize         int      `json:"block_size,omitempty"`
	AutoAllocate      bool     `json:"auto_allocate,omitempty"`
	ValueForEachVUser bool     `json:"value_for_each_vuser,omitempty"`
	Format            string   `json:"format,omitempty"` // timestamp: unix|unix_ms|iso8601|rfc3339|rfc3339_nano; uuid: uppercase (any other value is lowercase)
	Timezone          string   `json:"timezone,omitempty"`
	OffsetSeconds     int      `json:"offset_seconds,omitempty"`
	Min               int      `json:"min,omitempty"`
	Max               int      `json:"max,omitempty"`
	Start             int      `json:"start,omitempty"`
	End               int      `json:"end,omitempty"`
	Step              int      `json:"step,omitempty"`
	Length            int      `json:"length,omitempty"`
	Charset           string   `json:"charset,omitempty"`
	PadWidth          int      `json:"pad_width,omitempty"`
	PadChar           string   `json:"pad_char,omitempty"`
	Values            []string `json:"values,omitempty"`
	Seed              *int64   `json:"seed,omitempty"`
}

// HeaderEntry preserves one logical authored header occurrence in the DSL.
// The legacy Headers map remains a single-value compatibility summary.
// HeaderList retains authored order, casing, and duplicates, but an HTTP
// transport may normalize casing/order or combine protocol-compatible values.
// Imports whose special headers would lose semantics must omit those unsafe
// occurrences and retain a durable review gate with value-free fidelity evidence.
type HeaderEntry struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled *bool  `json:"enabled,omitempty"`
}

type HeaderList []HeaderEntry

func HeaderListFromMap(headers map[string]string) HeaderList {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(names[i]))
		right := strings.ToLower(strings.TrimSpace(names[j]))
		if left == right {
			return names[i] < names[j]
		}
		return left < right
	})
	list := make(HeaderList, 0, len(names))
	for _, name := range names {
		list = append(list, HeaderEntry{Name: name, Value: headers[name]})
	}
	return list
}

func (headers HeaderList) ToMap() map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	spelling := make(map[string]string, len(headers))
	for _, header := range headers {
		if header.Enabled != nil && !*header.Enabled {
			continue
		}
		name := strings.TrimSpace(header.Name)
		if name == "" {
			continue
		}
		identity := strings.ToLower(name)
		if previous, exists := spelling[identity]; exists && previous != name {
			delete(out, previous)
		}
		spelling[identity] = name
		out[name] = header.Value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type Action struct {
	PacingIDs   []string            `json:"pacing_ids,omitempty"`
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Method      string              `json:"method,omitempty"`
	HTTPVersion OptionalHTTPVersion `json:"http_version,omitzero"`
	Target      string              `json:"target"`
	Selector    string              `json:"selector,omitempty"`
	// ObjectRef binds this action to a named object in a project-scoped object
	// repository instead of (or as a fallback to) an inline selector. The
	// controller resolves it at script-preparation time into a concrete selector
	// plus ranked fallback candidates in Metadata; generators never see the
	// repository. Bare "logicalName" resolves across the project's repositories;
	// qualified "repoID:logicalName" pins to one repository.
	ObjectRef    string                 `json:"object_ref,omitempty"`
	Value        string                 `json:"value,omitempty"`
	Headers      map[string]string      `json:"headers,omitempty"`
	HeaderList   HeaderList             `json:"header_list,omitempty"`
	Body         string                 `json:"body,omitempty"`
	BodyFile     string                 `json:"body_file,omitempty"`
	BodyEncoding string                 `json:"body_encoding,omitempty"`
	Params       map[string]any         `json:"params,omitempty"`
	WaitFor      *WaitCondition         `json:"wait_for,omitempty"`
	Sync         *BrowserSyncPolicy     `json:"sync,omitempty"`
	Capture      *BrowserCaptureOptions `json:"capture,omitempty"`
	Metadata     map[string]any         `json:"metadata,omitempty"`
	// Attribution is the typed, bounded analytics identity for this action.
	// Importers and authors should prefer it over ad-hoc metadata keys; legacy
	// source_path/hierarchy metadata remains readable for compatibility.
	Attribution    *AnalyticsAttribution `json:"attribution,omitempty"`
	Extractors     []Extractor           `json:"extractors,omitempty"`
	Assertions     []Assertion           `json:"assertions,omitempty"`
	ThinkTime      ThinkTimeConfig       `json:"think_time,omitempty"`
	ThinkTimeAfter bool                  `json:"think_time_after,omitempty"`
	// Control flow (for action types "control_if", "control_switch", and "control_loop").
	Control *ControlFlow `json:"control,omitempty"`
	// Per-action overrides
	Auth         *AuthConfig       `json:"auth,omitempty"`                                              // override global auth
	TimeoutMs    int               `json:"timeout_ms,omitempty" jsonschema:"minimum=0,maximum=3600000"` // override global request timeout
	MaxRedirects *int              `json:"max_redirects,omitempty" jsonschema:"minimum=-1,maximum=50"`  // override global redirect policy
	SOAP         *SOAPActionConfig `json:"soap,omitempty"`
}

// AnalyticsAttribution assigns an action to a stable composition/page
// hierarchy without changing its protocol semantics.
type AnalyticsAttribution struct {
	HierarchyPath string `json:"hierarchy_path,omitempty"`
	PageName      string `json:"page_name,omitempty"`
}

// ControlFlow carries the body and branching/looping parameters for control_if,
// control_switch, and control_loop actions. Cases are evaluated in declaration
// order and only the first matching control_switch case executes.
type ControlFlow struct {
	Condition     *Condition       `json:"condition,omitempty"`      // branch/while predicate (nil = always true)
	Steps         []Action         `json:"steps,omitempty"`          // body run when the condition holds / each loop pass
	Else          []Action         `json:"else,omitempty"`           // control_if: body run when the condition is false
	Cases         []SwitchCase     `json:"cases,omitempty"`          // control_switch: ordered first-match cases
	Default       []Action         `json:"default,omitempty"`        // control_switch: body run when no case matches
	Branches      []ParallelBranch `json:"branches,omitempty"`       // control_parallel: named isolated branches
	Concurrency   int              `json:"concurrency,omitempty"`    // control_parallel: author ceiling, default 4, maximum 16
	FailFast      bool             `json:"fail_fast,omitempty"`      // control_parallel: cancel siblings after first failure
	Count         int              `json:"count,omitempty"`          // control_loop: fixed pass count (0 = condition-driven)
	MaxIterations int              `json:"max_iterations,omitempty"` // control_loop: safety cap (0 = engine default)
	ForEach       *ForEachLoop     `json:"for_each,omitempty"`       // control_loop: data-driven iteration (takes precedence over count/condition)
	// DoWhile makes a condition-driven control_loop run its body once BEFORE the
	// predicate is first evaluated (post-test / run-at-least-once) instead of the
	// default pre-test behavior. Useful when the predicate depends on a variable
	// the body itself produces — e.g. a poll loop whose terminal-state var persists
	// across VUser iterations would otherwise be skipped on re-entry. Ignored for
	// count/for_each modes (their pass counts are already determined).
	DoWhile bool `json:"do_while,omitempty"`
}

// SwitchCase is one ordered branch in a control_switch. Condition is required;
// a catch-all branch belongs in ControlFlow.Default so imported scripts cannot
// accidentally shadow later cases with a nil predicate.
type SwitchCase struct {
	Condition *Condition `json:"condition"`
	Steps     []Action   `json:"steps,omitempty"`
}

// ParallelBranch is one independently initialized protocol branch. Names are
// stable extraction namespaces and therefore must be unique within a block.
type ParallelBranch struct {
	Name  string   `json:"name"`
	Steps []Action `json:"steps"`
}

// ControlActionBranch exposes every nested action list in deterministic DSL
// order. Admission, redaction, import, and readiness code should use this
// helper so adding a new control container cannot create an unvisited branch.
type ControlActionBranch struct {
	Path    string
	Actions []Action
}

func (control *ControlFlow) ActionBranches() []ControlActionBranch {
	if control == nil {
		return nil
	}
	branches := make([]ControlActionBranch, 0, 3+len(control.Cases)+len(control.Branches))
	if len(control.Steps) > 0 {
		branches = append(branches, ControlActionBranch{Path: "steps", Actions: control.Steps})
	}
	if len(control.Else) > 0 {
		branches = append(branches, ControlActionBranch{Path: "else", Actions: control.Else})
	}
	for index, switchCase := range control.Cases {
		if len(switchCase.Steps) > 0 {
			branches = append(branches, ControlActionBranch{Path: fmt.Sprintf("cases[%d].steps", index), Actions: switchCase.Steps})
		}
	}
	if len(control.Default) > 0 {
		branches = append(branches, ControlActionBranch{Path: "default", Actions: control.Default})
	}
	for index, branch := range control.Branches {
		if len(branch.Steps) > 0 {
			branches = append(branches, ControlActionBranch{Path: fmt.Sprintf("branches[%d].steps", index), Actions: branch.Steps})
		}
	}
	return branches
}

// ForEachLoop drives a data-driven control_loop: it iterates every row of a data
// source, binding each row's columns to {{var.<Var>.<col>}} for the loop body.
// This is a distinct namespace from the per-VUser {{data.src.col}} binding, so a
// foreach never clobbers the row the VUser selected for its own iteration.
type ForEachLoop struct {
	Source    string     `json:"source"`              // data source name to iterate (required)
	Var       string     `json:"var"`                 // var-namespace prefix each row binds under (e.g. "row" → {{var.row.email}})
	Condition *Condition `json:"condition,omitempty"` // optional per-row early-exit predicate: iteration stops the first time it is false
}

// Condition is a deterministic boolean expression over template-expanded operands,
// e.g. {Left: "{{var.status}}", Op: "eq", Right: "active"}. Both operands are
// interpolated against VUser variables/parameters before comparison.
type Condition struct {
	Expr  string `json:"expr,omitempty"`  // CEL expression; takes precedence over left/op/right when set.
	Left  string `json:"left,omitempty"`  // left operand (template-expanded)
	Op    string `json:"op,omitempty"`    // eq|ne|contains|not_contains|gt|lt|gte|lte|exists|not_exists
	Right string `json:"right,omitempty"` // right operand (template-expanded)
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
	// OnMiss controls what happens when the expression finds no match:
	// "" or "ignore" leaves the variable unset and the request unaffected;
	// "fail" marks the request failed so correlation misses surface
	// immediately instead of cascading into later requests.
	OnMiss string `json:"on_miss,omitempty"`
	// DefaultValue distinguishes an absent fallback from an explicitly empty one.
	DefaultValue *string `json:"default_value,omitempty"`
	// Mode "" extracts a single value into {{var.name}}. Mode "all" saves every
	// match as {{var.name_1}}..{{var.name_N}} plus {{var.name_count}}; zero
	// matches count as a miss for OnMiss purposes.
	Mode string `json:"mode,omitempty"`
	// SaveOffset skips that many leading bytes of each matched value, and
	// SaveLength then truncates it to at most that many bytes (0 = entire
	// remainder). Both are clamped to the value's bounds.
	SaveOffset int `json:"save_offset,omitempty"`
	SaveLength int `json:"save_length,omitempty"`
	// Convert post-processes each matched value: "url_encode" percent-encodes
	// it for safe reuse in URLs/bodies; "url_decode" decodes form/query
	// escaping; "html_decode" decodes HTML entities.
	Convert string `json:"convert,omitempty"`
}

type Assertion struct {
	LegacyHTMLCheck bool `json:"legacy_html_check,omitempty"` // imported web_find only

	Type     string `json:"type"`
	Expected any    `json:"expected,omitempty"`
	Value    string `json:"value,omitempty"`
	Min      int    `json:"min,omitempty"`
	Max      int    `json:"max,omitempty"`
	// CaseInsensitive makes HTTP body_contains and body_not_contains ignore
	// case, as LoadRunner's Text/IC does.
	CaseInsensitive bool `json:"case_insensitive,omitempty"`
}
