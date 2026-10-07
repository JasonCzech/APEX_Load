// Package elastic implements bounded read-only Elasticsearch and OpenSearch
// observability collection. Runtime credentials never serialize into configuration.
package elastic

import (
	"encoding/json"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

const (
	ProductElasticsearch = "elasticsearch"
	ProductOpenSearch    = "opensearch"
	AuthAPIKey           = "api_key"
	AuthBasic            = "basic"
	AuthBearer           = "bearer"
	AuthMTLS             = "mtls"
)

type Config struct {
	Product               string   `json:"product"`
	Endpoint              string   `json:"endpoint"`
	DashboardsURL         string   `json:"dashboards_url,omitempty"`
	AuthType              string   `json:"auth_type"`
	ApprovedCIDRs         []string `json:"approved_cidrs,omitempty"`
	CAPEM                 string   `json:"ca_pem,omitempty"`
	TimeoutSeconds        int      `json:"timeout_seconds,omitempty"`
	RequestsPerMinute     int      `json:"requests_per_minute,omitempty"`
	MaxResponseBytes      int      `json:"max_response_bytes,omitempty"`
	MaxRequestsPerPoll    int      `json:"max_requests_per_poll,omitempty"`
	MaxPollSeconds        int      `json:"max_poll_seconds,omitempty"`
	MaxPoints             int      `json:"max_points,omitempty"`
	MaxEvidence           int      `json:"max_evidence,omitempty"`
	MaxSeries             int      `json:"max_series,omitempty"`
	MaxRetries            int      `json:"max_retries,omitempty"`
	KibanaEndpoint        string   `json:"kibana_endpoint,omitempty"`
	KibanaCAPEM           string   `json:"kibana_ca_pem,omitempty"`
	KibanaApprovedCIDRs   []string `json:"kibana_approved_cidrs,omitempty"`
	LogstashEndpoint      string   `json:"logstash_endpoint,omitempty"`
	LogstashCAPEM         string   `json:"logstash_ca_pem,omitempty"`
	LogstashApprovedCIDRs []string `json:"logstash_approved_cidrs,omitempty"`
}

type ComponentCredentials struct {
	AuthType      string `json:"-"`
	APIKey        string `json:"-"`
	Username      string `json:"-"`
	Password      string `json:"-"`
	BearerToken   string `json:"-"`
	ClientCertPEM string `json:"-"`
	ClientKeyPEM  string `json:"-"`
}

type Credentials struct {
	APIKey        string               `json:"-"`
	Username      string               `json:"-"`
	Password      string               `json:"-"`
	BearerToken   string               `json:"-"`
	ClientCertPEM string               `json:"-"`
	ClientKeyPEM  string               `json:"-"`
	Kibana        ComponentCredentials `json:"-"`
	Logstash      ComponentCredentials `json:"-"`
}

type MetricSelection struct {
	Name          string   `json:"name"`
	Field         string   `json:"field,omitempty"`
	Statistic     string   `json:"statistic"`
	Kind          string   `json:"kind,omitempty"`
	Unit          string   `json:"unit,omitempty"`
	BucketSeconds int      `json:"bucket_seconds,omitempty"`
	GroupBy       []string `json:"group_by,omitempty"`
	SumField      string   `json:"sum_field,omitempty"`
	CountField    string   `json:"count_field,omitempty"`
	ExactWindow   bool     `json:"exact_window,omitempty"`
}

// Query permits either a validated query-clause DSL document or a restricted
// ES|QL pipeline. The client owns indices, enforced filters, sorting and limits.
type Query struct {
	Name             string          `json:"name"`
	Language         string          `json:"language"`
	Kind             string          `json:"kind"`
	DSL              json.RawMessage `json:"dsl,omitempty"`
	ESQL             string          `json:"esql,omitempty"`
	Params           []any           `json:"params,omitempty"`
	TimestampColumn  string          `json:"timestamp_column,omitempty"`
	ValueColumn      string          `json:"value_column,omitempty"`
	CountColumn      string          `json:"count_column,omitempty"`
	SumColumn        string          `json:"sum_column,omitempty"`
	GroupBy          []string        `json:"group_by,omitempty"`
	DimensionColumns []string        `json:"dimension_columns,omitempty"`
	Async            bool            `json:"async,omitempty"`
	// ExactWindow omits native buckets and describes the exact enforced interval.
	ExactWindow   bool   `json:"exact_window,omitempty"`
	Approximate   bool   `json:"approximate,omitempty"`
	Statistic     string `json:"statistic,omitempty"`
	Unit          string `json:"unit,omitempty"`
	BucketSeconds int    `json:"bucket_seconds,omitempty"`
}

type Selection struct {
	ProjectID               string              `json:"project_id"`
	Indices                 []string            `json:"indices"`
	Preset                  string              `json:"preset,omitempty"`
	TimestampField          string              `json:"timestamp_field,omitempty"`
	IngestedField           string              `json:"ingested_field,omitempty"`
	ServiceField            string              `json:"service_field,omitempty"`
	ServiceValues           []string            `json:"service_values,omitempty"`
	RunIDField              string              `json:"run_id_field,omitempty"`
	TraceIDField            string              `json:"trace_id_field,omitempty"`
	SpanIDField             string              `json:"span_id_field,omitempty"`
	TransactionIDField      string              `json:"transaction_id_field,omitempty"`
	ParentSpanIDField       string              `json:"parent_span_id_field,omitempty"`
	MessageField            string              `json:"message_field,omitempty"`
	SeverityField           string              `json:"severity_field,omitempty"`
	DurationField           string              `json:"duration_field,omitempty"`
	DurationUnit            string              `json:"duration_unit,omitempty"`
	StatusField             string              `json:"status_field,omitempty"`
	UniqueSortField         string              `json:"unique_sort_field,omitempty"`
	AttributeFields         []string            `json:"attribute_fields,omitempty"`
	ProjectedFields         []string            `json:"projected_fields,omitempty"`
	Transactions            bool                `json:"transactions,omitempty"`
	Logs                    bool                `json:"logs,omitempty"`
	Traces                  bool                `json:"traces,omitempty"`
	Errors                  bool                `json:"errors,omitempty"`
	ClusterHealth           bool                `json:"cluster_health,omitempty"`
	KibanaHealth            bool                `json:"kibana_health,omitempty"`
	LogstashHealth          bool                `json:"logstash_health,omitempty"`
	Metrics                 []MetricSelection   `json:"metrics,omitempty"`
	Queries                 []Query             `json:"queries,omitempty"`
	SettleSeconds           int                 `json:"settle_seconds,omitempty"`
	OverlapSeconds          int                 `json:"overlap_seconds,omitempty"`
	MaxWindowSeconds        int                 `json:"max_window_seconds,omitempty"`
	PageSize                int                 `json:"page_size,omitempty"`
	RunHeader               bool                `json:"run_header,omitempty"`
	HeaderOrigins           []string            `json:"header_origins,omitempty"`
	QueryOffset             int                 `json:"query_offset,omitempty"`
	Continuation            *Continuation       `json:"-"`
	ResolvedIndices         []string            `json:"-"`
	TerminalWindows         []TerminalWindow    `json:"-"`
	LastHealthAt            time.Time           `json:"-"`
	HealthCoverage          map[string]Coverage `json:"-"`
	FinalHealthAfter        time.Time           `json:"-"`
	IngestionFrom           time.Time           `json:"-"`
	ObservedTraceIDs        []string            `json:"-"`
	CorrelationLimitReached bool                `json:"-"`
	DisabledQueries         map[string]bool     `json:"-"`
	MaxRetainedRecords      int                 `json:"max_retained_records,omitempty"`
	MaxRetainedBytes        int                 `json:"max_retained_bytes,omitempty"`
}

// TerminalWindow is supplied by the controller from frozen run rules. It never
// forms part of a reusable source or the public configuration.
type TerminalWindow struct {
	Metric   string    `json:"metric"`
	FromUTC  time.Time `json:"from_utc"`
	UntilUTC time.Time `json:"until_utc"`
}

// Continuation is an executor-private cursor; it must never enter public source
// configuration or a dashboard payload. PIT handles expire after KeepAliveUntil.
type Continuation = analysis.ProviderContinuation

type JobConfig struct {
	Config                  Config              `json:"config"`
	Selection               Selection           `json:"selection"`
	Continuation            *Continuation       `json:"continuation,omitempty"`
	ResolvedIndices         []string            `json:"resolved_indices,omitempty"`
	TerminalWindows         []TerminalWindow    `json:"terminal_windows,omitempty"`
	LastHealthAt            time.Time           `json:"last_health_at,omitempty"`
	HealthCoverage          map[string]Coverage `json:"health_coverage,omitempty"`
	FinalHealthAfter        time.Time           `json:"final_health_after,omitempty"`
	IngestionFrom           time.Time           `json:"ingestion_from,omitempty"`
	ObservedTraceIDs        []string            `json:"observed_trace_ids,omitempty"`
	CorrelationLimitReached bool                `json:"correlation_limit_reached,omitempty"`
	DisabledQueries         map[string]bool     `json:"disabled_queries,omitempty"`
	Capabilities            *Capabilities       `json:"capabilities,omitempty"`
}

type Capabilities struct {
	Product        string            `json:"product"`
	Version        string            `json:"version,omitempty"`
	Serverless     bool              `json:"serverless"`
	DSL            bool              `json:"dsl"`
	ESQL           bool              `json:"esql"`
	AsyncESQL      bool              `json:"async_esql"`
	PIT            bool              `json:"pit"`
	ShardDoc       bool              `json:"shard_doc"`
	Metrics        bool              `json:"metrics"`
	Logs           bool              `json:"logs"`
	Traces         bool              `json:"traces"`
	Health         bool              `json:"health"`
	KibanaHealth   bool              `json:"kibana_health"`
	LogstashHealth bool              `json:"logstash_health"`
	Status         map[string]string `json:"status,omitempty"`
}

type Index struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
type Field struct {
	Name            string   `json:"name"`
	Types           []string `json:"types"`
	Searchable      bool     `json:"searchable"`
	Aggregatable    bool     `json:"aggregatable"`
	Conflict        bool     `json:"conflict"`
	PartiallyMapped bool     `json:"partially_mapped,omitempty"`
}

type Evidence = analysis.ProviderEvidence
type Coverage = analysis.ProviderCoverage
type Progress = analysis.ProviderProgress

type Result struct {
	CleanupContinuations []*Continuation        `json:"-"`
	Points               []analysis.MetricPoint `json:"points"`
	Evidence             []Evidence             `json:"evidence"`
	Coverage             map[string]Coverage    `json:"coverage"`
	Capabilities         Capabilities           `json:"capabilities"`
	Progress             Progress               `json:"progress"`
	Continuation         *Continuation          `json:"continuation,omitempty"`
	ResolvedIndices      []string               `json:"-"`
	series               map[string]bool
}
