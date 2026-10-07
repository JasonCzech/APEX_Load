// Package monitorcontract owns passive monitoring DTOs retained for historical
// evidence compatibility. It contains no provider clients or execution hooks.
package monitorcontract

type AppDynamicsConfig struct {
	Endpoint                   string   `json:"endpoint"`
	ContextPath                string   `json:"context_path,omitempty"`
	AccountName                string   `json:"account_name"`
	AuthType                   string   `json:"auth_type"`
	EventsEndpoint             string   `json:"events_endpoint,omitempty"`
	EventsAccountName          string   `json:"events_account_name,omitempty"`
	ApprovedCIDRs              []string `json:"approved_cidrs,omitempty"`
	CAPEM                      string   `json:"ca_pem,omitempty"`
	TimeoutSeconds             int      `json:"timeout_seconds,omitempty"`
	RequestsPerMinute          int      `json:"requests_per_minute,omitempty"`
	AnalyticsRequestsPerMinute int      `json:"analytics_requests_per_minute,omitempty"`
	MaxResponseBytes           int      `json:"max_response_bytes,omitempty"`
	MaxSeries                  int      `json:"max_series,omitempty"`
	MaxPoints                  int      `json:"max_points,omitempty"`
	MaxRequestsPerPoll         int      `json:"max_requests_per_poll,omitempty"`
	MaxPollSeconds             int      `json:"max_poll_seconds,omitempty"`
}

type AppDynamicsMetricSelection struct {
	ApplicationID string            `json:"application_id"`
	Path          string            `json:"path"`
	Name          string            `json:"name"`
	Unit          string            `json:"unit,omitempty"`
	Statistic     string            `json:"statistic,omitempty"`
	EntityIDs     map[string]string `json:"entity_ids,omitempty"`
}

type AppDynamicsAnalyticsQuery struct {
	Name               string `json:"name"`
	Query              string `json:"query"`
	ApplicationID      string `json:"application_id"`
	Kind               string `json:"kind,omitempty"`
	Unit               string `json:"unit,omitempty"`
	Statistic          string `json:"statistic,omitempty"`
	BucketSeconds      int    `json:"bucket_seconds,omitempty"`
	RunIDField         string `json:"run_id_field,omitempty"`
	ApplicationIDField string `json:"application_id_field,omitempty"`
	ApplicationName    string `json:"application_name,omitempty"`
}

type AppDynamicsSelection struct {
	ProjectID       string                       `json:"project_id"`
	QueryOffset     int                          `json:"query_offset,omitempty"`
	Applications    []string                     `json:"applications"`
	Metrics         []AppDynamicsMetricSelection `json:"metrics,omitempty"`
	ScopeIDs        map[string][]string          `json:"scope_ids,omitempty"`
	Events          bool                         `json:"events,omitempty"`
	Violations      bool                         `json:"violations,omitempty"`
	Snapshots       bool                         `json:"snapshots,omitempty"`
	Analytics       []AppDynamicsAnalyticsQuery  `json:"analytics,omitempty"`
	SettleSeconds   int                          `json:"settle_seconds,omitempty"`
	RunHeader       bool                         `json:"run_header,omitempty"`
	HeaderOrigins   []string                     `json:"header_origins,omitempty"`
	CollectorHeader string                       `json:"collector_header,omitempty"`
	CollectorValue  string                       `json:"collector_value,omitempty"`
}

type AppDynamicsCapabilities struct {
	MetricAPI  string            `json:"metric_api,omitempty"`
	Metrics    bool              `json:"metrics"`
	Events     bool              `json:"events"`
	Violations bool              `json:"violations"`
	Snapshots  bool              `json:"snapshots"`
	Analytics  bool              `json:"analytics"`
	Status     map[string]string `json:"status,omitempty"`
}

type Entity struct {
	ID        string            `json:"id,omitempty"`
	Name      string            `json:"name"`
	Type      string            `json:"type"`
	ParentID  string            `json:"parent_id,omitempty"`
	Path      string            `json:"path,omitempty"`
	EntityIDs map[string]string `json:"entity_ids,omitempty"`
}
