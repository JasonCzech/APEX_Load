package api

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/archivecontract"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/hostnet"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/runcontract"
)

const archiveSchemaVersion = "apexload.archive.v4"
const archiveSchemaVersionV1 = "apexload.archive.v1"
const archiveSchemaVersionV2 = "apexload.archive.v2"
const archiveSchemaVersionV3 = "apexload.archive.v3"
const runArchiveReportSchemaVersion = "apexload.archive_report.v1"
const browserDiagnosticsSchemaVersion = "apexload.browser_diagnostics.v1"
const runArchiveManifestFileName = "manifest.json"
const testRunManifestFileName = "test_manifest.json"

const (
	archivePercentileQualitySourceGenerator = "source_generator"
	archivePercentileQualityMergedSketch    = "merged_sketch"
	archivePercentileQualityMergedDigest    = "merged_tdigest"
	archivePercentileQualityRecomputed      = "recomputed_from_transactions"
	archivePercentileQualityUnavailable     = "unavailable"
)

// RunArchiveSourceCoverage records the controller's source snapshot used for
// collection. It does not prove complete shard history or zero measurement loss.
type RunArchiveSourceCoverage = archivecontract.RunArchiveSourceCoverage

type RunArchiveSource = archivecontract.RunArchiveSource

type RunArchiveManifest struct {
	SourceCoverage      *RunArchiveSourceCoverage   `json:"source_coverage,omitempty"`
	CSVExports          []RunArchiveCSVExportStatus `json:"csv_exports,omitempty"`
	CSVExportIncomplete string                      `json:"csv_export_incomplete,omitempty"`
	TerminationReason   string                      `json:"termination_reason,omitempty"`
	SchemaVersion       string                      `json:"schema_version"`
	TestID              string                      `json:"test_id"`
	TestName            string                      `json:"test_name"`
	CreatedAt           time.Time                   `json:"created_at"`
	CompletedAt         time.Time                   `json:"completed_at,omitempty"`
	Status              string                      `json:"status,omitempty"`
	Error               string                      `json:"error,omitempty"`
	Provenance          RunArchiveProvenance        `json:"provenance,omitempty"`
	// ExecutorCompletedAt keeps the executing controller's own completion time
	// when failover recovery restamps CompletedAt with the durable run end.
	ExecutorCompletedAt *time.Time `json:"executor_completed_at,omitempty"`
	// InitialProvenance is captured once when archive recording begins and is
	// never refreshed by runtime controls. Provenance remains the final enriched
	// execution snapshot for generator inventory, actors, and governance.
	InitialProvenance *RunArchiveProvenance `json:"initial_provenance,omitempty"`
	// This process-local copy excludes the legacy remote-provenance fallback.
	// It is authoritative only while constructing a controller-owned archive.
	controllerInitialProvenance *RunArchiveProvenance
	// businessKeys applies the run's business-key policy while a
	// controller-owned archive is built.
	businessKeys *businessKeyArchiver
	// BusinessKeyDigestsUnavailable records that business-key values of
	// digest-only groups were dropped without a digest (no key wrapper), so
	// lookups cannot match them.
	BusinessKeyDigestsUnavailable bool `json:"business_key_digests_unavailable,omitempty"`
	// Continuity records the HA controller failovers the run survived
	// (docs/HA_RUN_CONTINUITY_PLAN.md); absent when the run never failed over.
	Continuity                        *RunContinuityStatus `json:"continuity,omitempty"`
	BrowserDocumentCoverageIncomplete bool                 `json:"browser_document_coverage_incomplete,omitempty"`
	BrowserDocumentCaptureRequested   bool                 `json:"browser_document_capture_requested,omitempty"`
	BrowserDocumentsFile              string               `json:"browser_documents_file,omitempty"`
	BrowserDocumentsParquetFile       string               `json:"browser_documents_parquet_file,omitempty"`
	// BrowserHARArtifacts are the run's native HARs; BrowserHAROmitted
	// counts, by reason, the ones the archive could not retain.
	BrowserHARArtifacts  []RunArchiveEvidenceArtifact `json:"browser_har_artifacts,omitempty"`
	BrowserHARIncomplete bool                         `json:"browser_har_incomplete,omitempty"`
	BrowserHAROmitted    map[string]int64             `json:"browser_har_omitted,omitempty"`
	// BrowserInteractionsFile is the run's interaction evidence sidecar;
	// BrowserInteractionsArtifact describes a generator's sidecar for transfer.
	BrowserInteractionsFile           string                             `json:"browser_interactions_file,omitempty"`
	BrowserInteractionRecordCount     int64                              `json:"browser_interaction_record_count,omitempty"`
	BrowserInteractionsIncomplete     bool                               `json:"browser_interactions_incomplete,omitempty"`
	BrowserInteractionsDropped        int64                              `json:"browser_interactions_dropped,omitempty"`
	BrowserInteractionsArtifact       *RunArchiveEvidenceArtifact        `json:"browser_interactions_artifact,omitempty"`
	BrowserDocumentRecordCount        int64                              `json:"browser_document_record_count,omitempty"`
	BrowserDocumentsSHA256            string                             `json:"browser_documents_sha256,omitempty"`
	BrowserDocumentsParquetSHA256     string                             `json:"browser_documents_parquet_sha256,omitempty"`
	TransactionsFile                  string                             `json:"transactions_file"`
	TransactionsParquetFile           string                             `json:"transactions_parquet_file,omitempty"`
	ResourceSamplesFile               string                             `json:"resource_samples_file"`
	ResourceSamplesParquetFile        string                             `json:"resource_samples_parquet_file,omitempty"`
	IntervalSummariesFile             string                             `json:"interval_summaries_file"`
	IntervalSummariesParquetFile      string                             `json:"interval_summaries_parquet_file,omitempty"`
	TestManifestFile                  string                             `json:"test_manifest_file,omitempty"`
	TestNotesFile                     string                             `json:"test_notes_file,omitempty"`
	ELKEvidenceFile                   string                             `json:"elk_evidence_file,omitempty"`
	DynatraceEvidenceFile             string                             `json:"dynatrace_evidence_file,omitempty"`
	MonitorEvidenceFile               string                             `json:"monitor_evidence_file,omitempty"`
	MonitorEvidenceCount              int64                              `json:"monitor_evidence_count,omitempty"`
	MonitorManagementFile             string                             `json:"monitor_management_file,omitempty"`
	MonitorManagementVersion          uint32                             `json:"monitor_management_version,omitempty"`
	MonitorManagementSnapshotCount    int64                              `json:"monitor_management_snapshot_count,omitempty"`
	MonitorManagementRowCount         int64                              `json:"monitor_management_row_count,omitempty"`
	MonitorDiagnosticsFile            string                             `json:"monitor_diagnostics_file,omitempty"`
	MonitorDiagnosticsCount           int64                              `json:"monitor_diagnostics_count,omitempty"`
	FrozenMonitorRules                []analysis.MonitorRule             `json:"frozen_monitor_rules,omitempty"`
	MonitorRulesFrozen                bool                               `json:"monitor_rules_frozen,omitempty"`
	MonitoringStatus                  string                             `json:"monitoring_status,omitempty"`
	MonitorRuleResults                []analysis.MonitorRuleResult       `json:"monitor_rule_results,omitempty"`
	MonitorCoverage                   []RunMonitorCoverage               `json:"monitor_coverage,omitempty"`
	Artifacts                         []RunArchiveArtifact               `json:"artifacts,omitempty"`
	TransactionCount                  int64                              `json:"transaction_count"`
	ResourceSampleCount               int64                              `json:"resource_sample_count"`
	IntervalSummaryCount              int64                              `json:"interval_summary_count"`
	ELKEvidenceCount                  int64                              `json:"elk_evidence_count,omitempty"`
	DynatraceEvidenceCount            int64                              `json:"dynatrace_evidence_count,omitempty"`
	SLAEvaluated                      bool                               `json:"sla_evaluated,omitempty"`
	SLABreached                       bool                               `json:"sla_breached"`
	SLADetails                        []string                           `json:"sla_details,omitempty"`
	TransactionRuleResults            []TransactionSLARuleResult         `json:"transaction_rule_results,omitempty"`
	TransactionResponseTimeSLAResults []TransactionResponseTimeSLAResult `json:"transaction_response_time_sla_results,omitempty"`
	MessageDetailCapture              *RunArchiveMessageDetailCapture    `json:"message_detail_capture,omitempty"`
	HostNetwork                       []hostnet.RunEvidence              `json:"host_network,omitempty"` // per process: limits and UDP drops at start and end
	// IntervalDistributionsFile holds per-interval duration distributions
	// (apexload.interval_distribution.v1), written once after finalize.
	IntervalDistributionsFile      string `json:"interval_distributions_file,omitempty"`
	IntervalDistributionCount      int64  `json:"interval_distribution_count,omitempty"`
	IntervalDistributionsSource    string `json:"interval_distributions_source,omitempty"`
	IntervalDistributionsTruncated bool   `json:"interval_distributions_truncated,omitempty"`
	// ProviderLookupsFile records each trace ID asked of a provider
	// (apexload.provider_lookup.v1). ExemplarsFile holds the post-run
	// exemplars (apexload.exemplar.v1), written once after finalize.
	ProviderLookupsFile      string `json:"provider_lookups_file,omitempty"`
	ProviderLookupCount      int64  `json:"provider_lookup_count,omitempty"`
	ProviderLookupsTruncated bool   `json:"provider_lookups_truncated,omitempty"`
	ExemplarsFile            string `json:"exemplars_file,omitempty"`
	ExemplarCount            int64  `json:"exemplar_count,omitempty"`
	ExemplarsTruncated       bool   `json:"exemplars_truncated,omitempty"`
	// ELKObservation labels legacy ELK capture values as cumulative.
	ELKObservation string `json:"elk_observation,omitempty"`
	// ClockObservationsFile holds measured generator and collector clock
	// offsets (apexload.clock_observation.v1), written once after finalize.
	ClockObservationsFile      string `json:"clock_observations_file,omitempty"`
	ClockObservationCount      int64  `json:"clock_observation_count,omitempty"`
	ClockObservationsTruncated bool   `json:"clock_observations_truncated,omitempty"`
	// TelemetrySourcesFile holds per-generator source epochs: received,
	// duplicate, out-of-order and lost publication sequences.
	TelemetrySourcesFile      string `json:"telemetry_sources_file,omitempty"`
	TelemetrySourceEpochCount int64  `json:"telemetry_source_epoch_count,omitempty"`
	TelemetrySourcesTruncated bool   `json:"telemetry_sources_truncated,omitempty"`
	// JointIntervalsFile holds the run's joint evidence v4 rows (one per
	// measured tuple of each retained source publication), also as Parquet.
	JointIntervalsFile        string `json:"joint_intervals_file,omitempty"`
	JointIntervalsParquetFile string `json:"joint_intervals_parquet_file,omitempty"`
	JointIntervalCount        int64  `json:"joint_interval_count,omitempty"`
	JointIntervalsTruncated   bool   `json:"joint_intervals_truncated,omitempty"`
	// TelemetryCoverage is the run's wall-time telemetry account: observed
	// and unknown time per source, and healthy, degraded, breached and
	// unknown time with explicit denominators.
	TelemetryCoverage *TelemetryCoverageRecord `json:"telemetry_coverage,omitempty"`
	// TelemetryStageDistributions are the run's per-stage latency
	// distributions as this controller sampled them, and
	// TelemetryStageDigests the mergeable digests behind them.
	TelemetryStageDistributions []TelemetryStageDistribution      `json:"telemetry_stage_distributions,omitempty"`
	TelemetryStageDigests       map[string]engine.TDigestSnapshot `json:"telemetry_stage_digests,omitempty"`
	// TelemetryRenderPopulation is the run's render accounting for its
	// reference dashboard client, as this controller recorded it (TEL-A).
	TelemetryRenderPopulation *TelemetryRenderPopulation `json:"telemetry_render_population,omitempty"`
	// TelemetryLoss is the run's total of each loss counter at finalize:
	// exact, an observed lower bound or unavailable, with the reason.
	TelemetryLoss *TelemetryLossAccounting `json:"telemetry_loss,omitempty"`
	// PhaseEventsFile holds generated load-phase transitions and their sink
	// outcomes (apexload.phase_event.v1), apart from analyst annotations.
	PhaseEventsFile      string `json:"phase_events_file,omitempty"`
	PhaseEventCount      int64  `json:"phase_event_count,omitempty"`
	PhaseEventsTruncated bool   `json:"phase_events_truncated,omitempty"`
}

type TransactionSLARuleResult = runcontract.TransactionSLARuleResult

// TransactionResponseTimeSLAResult is the authoritative compliance result for
// one named business transaction within one scenario group. Failed protocol
// samples remain visible but are deliberately excluded from latency compliance.
type TransactionResponseTimeSLAResult = runcontract.TransactionResponseTimeSLAResult

type RunArchiveMessageDetailCapture struct {
	RetainedItems  int64            `json:"retained_items"`
	RetainedBytes  int64            `json:"retained_bytes"`
	TruncatedItems int64            `json:"truncated_items"`
	Dropped        map[string]int64 `json:"dropped,omitempty"`
}

type RunArchiveArtifact struct {
	Name       string `json:"name"`
	Kind       string `json:"kind,omitempty"`
	Format     string `json:"format,omitempty"`
	RecordType string `json:"record_type,omitempty"`
	Count      int64  `json:"count,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}

type RunArchiveExecutionSettingsSnapshot struct {
	General          GeneralSettings          `json:"general"`
	Thresholds       ThresholdSettings        `json:"thresholds"`
	CapacityPlanning CapacityPlanningSettings `json:"capacity_planning"`
}

type RunArchiveProvenance struct {
	// RunIncarnation is minted by controller admission and frozen in the durable
	// initial provenance. It survives leadership changes, unlike completion time.
	RunIncarnation    string                                 `json:"run_incarnation,omitempty"`
	ProjectLiveTables []RunArchiveProjectLiveTableAttachment `json:"project_live_tables,omitempty"`
	LiveTables        []RunArchiveLiveTableSummary           `json:"live_tables,omitempty"`
	TelemetryPolicy   *TelemetryBudgetPolicy                 `json:"telemetry_policy,omitempty"`
	// PolicyPins are the project policy revisions in force at Start: null on
	// runs admitted before policies existed, an empty list when no default
	// policy was configured. The field is always present so the two differ.
	PolicyPins []RunPolicyPin `json:"policy_pins"`
	// TelemetryAcceptancePolicy is the pinned telemetry acceptance policy's
	// definition, frozen at Start so the run's verdict never follows a later
	// edit (C7).
	TelemetryAcceptancePolicy *TelemetryAcceptancePolicySnapshot  `json:"telemetry_acceptance_policy,omitempty"`
	ComparisonFingerprint     string                              `json:"comparison_fingerprint,omitempty"`
	ComparisonMetrics         map[string]BaselineMetricDefinition `json:"comparison_metrics,omitempty"`
	TerminationMode           string                              `json:"termination_mode,omitempty"`
	CSVExports                []RunArchiveCSVExport               `json:"csv_exports,omitempty"`
	ProtocolMix               *dsl.ProtocolMixExpectation         `json:"protocol_mix,omitempty"` // strictest Protocol Mix expectation across groups
	HTTP3Preflight            *RunArchiveHTTP3Preflight           `json:"http3_preflight,omitempty"`
	ApplicationID             string                              `json:"application_id,omitempty"`
	ReleaseID                 string                              `json:"release_id,omitempty"`
	ScenarioID                string                              `json:"scenario_id,omitempty"`
	ProjectID                 string                              `json:"project_id,omitempty"`
	RequestedAt               time.Time                           `json:"requested_at,omitempty"`
	StartedAt                 *time.Time                          `json:"started_at,omitempty"`
	ScheduledStartAt          *time.Time                          `json:"scheduled_start_at,omitempty"`
	ScenarioMode              string                              `json:"scenario_mode,omitempty"`
	ScriptShakeout            bool                                `json:"script_shakeout,omitempty"`
	ResourceBaseline          bool                                `json:"resource_baseline,omitempty"`
	StartDelay                string                              `json:"start_delay,omitempty"`
	TotalVUsers               int                                 `json:"total_vusers,omitempty"`
	Duration                  string                              `json:"duration,omitempty"`
	Assignment                *GeneratorAssignmentSettings        `json:"assignment,omitempty"`
	GeneratorShards           []GeneratorShardSummary             `json:"generator_shards,omitempty"`
	GoalConfig                *GoalConfig                         `json:"goal_config,omitempty"`
	TrafficProfile            *TrafficProfile                     `json:"traffic_profile,omitempty"`
	// JointEvidence is the run's joint evidence v4 projection, frozen at Start.
	JointEvidence         *engine.JointProjection             `json:"joint_evidence,omitempty"`
	RuntimeSettings       *RuntimeSettingsOverride            `json:"runtime_settings,omitempty"`
	ELKCapture            *ELKCaptureConfig                   `json:"elk_capture,omitempty"`
	DynatraceCapture      *DynatraceCaptureConfig             `json:"dynatrace_capture,omitempty"`
	RuntimeSettingsReview *RuntimeSettingsReview              `json:"runtime_settings_review,omitempty"`
	Groups                []TestGroup                         `json:"groups,omitempty"`
	BaselinePinned        bool                                `json:"baseline_pinned,omitempty"`
	BaselinePinnedAt      *time.Time                          `json:"baseline_pinned_at,omitempty"`
	ExecutionSettings     RunArchiveExecutionSettingsSnapshot `json:"execution_settings"`
	ControllerEnv         map[string]string                   `json:"controller_env,omitempty"`
	GeneratorInventory    []GeneratorInventoryEntry           `json:"generator_inventory,omitempty"`
	CreatedBySubject      string                              `json:"created_by_subject,omitempty"`
	CreatedByDisplayName  string                              `json:"created_by_display_name,omitempty"`
	StartedBySubject      string                              `json:"started_by_subject,omitempty"`
	StartedByDisplayName  string                              `json:"started_by_display_name,omitempty"`
	StoppedBySubject      string                              `json:"stopped_by_subject,omitempty"`
	StoppedByDisplayName  string                              `json:"stopped_by_display_name,omitempty"`
	Governance            *RunArchiveGovernanceSnapshot       `json:"governance,omitempty"`
	// Workload is the offered workload frozen from the compiled run, used to
	// qualify baselines.
	Workload *rca.WorkloadKey `json:"workload,omitempty"`

	// BusinessKeys is the run's business-key archive policy (G16).
	BusinessKeys *RunArchiveBusinessKeys `json:"business_keys,omitempty"`

	// AuraHTTPRequests are the HTTP groups' Aura requests, frozen at Start
	// for the Aura parity report when the run also has browser observers.
	AuraHTTPRequests []RunArchiveAuraHTTPRequest `json:"aura_http_requests,omitempty"`

	// ExecutionGeneration is the HA execution lease generation of the term that
	// ran the run when this provenance was captured (0 without a durable
	// execution fence). Orphan recovery matches it to the durable run row.
	ExecutionGeneration uint64 `json:"execution_generation,omitempty"`
}

type RunArchiveGovernanceSnapshot struct {
	LaunchPreflight        *RunArchiveLaunchPreflightSnapshot       `json:"launch_preflight,omitempty"`
	ExecutionPlanApproval  *RunArchiveExecutionPlanApprovalSnapshot `json:"execution_plan_approval,omitempty"`
	RuntimeSettingsReview  *RuntimeSettingsReview                   `json:"runtime_settings_review,omitempty"`
	CapacityReservationID  string                                   `json:"capacity_reservation_id,omitempty"`
	CapacityReservationRef string                                   `json:"capacity_reservation_ref,omitempty"`
}

type RunArchiveLaunchPreflightSnapshot struct {
	Decision  string   `json:"decision,omitempty"`
	Status    string   `json:"status,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type RunArchiveExecutionPlanApprovalSnapshot struct {
	ID              string     `json:"id,omitempty"`
	PlanID          string     `json:"plan_id,omitempty"`
	Policy          string     `json:"policy,omitempty"`
	Status          string     `json:"status,omitempty"`
	RequestedBy     string     `json:"requested_by,omitempty"`
	RequestedAt     time.Time  `json:"requested_at,omitempty"`
	ResolvedBy      string     `json:"resolved_by,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	DecisionReason  string     `json:"decision_reason,omitempty"`
	RequiredRole    string     `json:"required_role,omitempty"`
	PlanContentHash string     `json:"plan_content_hash,omitempty"`
	// ApprovedPeakVUsers is the immutable per-test ceiling carried from the
	// approved execution-plan snapshot. Quota-burst approval may exceed the
	// normal project aggregate quota only up to this value; it is never an
	// unlimited runtime scaling exemption.
	ApprovedPeakVUsers int `json:"approved_peak_vusers,omitempty"`
}

type TransactionEvent struct {
	WorkerDurationMs      float64                   `json:"worker_duration_ms,omitempty"`
	CSVExportIDs          []string                  `json:"csv_export_ids"`
	ExtractionsIncomplete bool                      `json:"extractions_incomplete,omitempty"`
	Population            string                    `json:"population,omitempty"`
	EventID               string                    `json:"event_id"`
	Timestamp             time.Time                 `json:"timestamp"`
	TestID                string                    `json:"test_id"`
	TestName              string                    `json:"test_name"`
	Group                 string                    `json:"group,omitempty"`
	Region                string                    `json:"region,omitempty"`
	NetworkProfile        string                    `json:"network_profile,omitempty"`
	PropertyDimensions    map[string]string         `json:"property_dimensions,omitempty"`
	SourceTransaction     string                    `json:"source_transaction,omitempty"`
	Hierarchy             string                    `json:"hierarchy,omitempty"`
	PageName              string                    `json:"page_name,omitempty"`
	Phase                 string                    `json:"phase,omitempty"`
	HasDetail             bool                      `json:"has_detail"`
	MessageDetailPriority int                       `json:"message_detail_priority,omitempty"`
	ActionName            string                    `json:"action_name,omitempty"`
	VUserID               int                       `json:"vuser_id,omitempty"`
	Iteration             int64                     `json:"iteration,omitempty"`
	Interrupted           bool                      `json:"interrupted,omitempty"`
	VUserLifetime         uint64                    `json:"vuser_lifetime,omitempty"`
	EndsIteration         bool                      `json:"ends_iteration,omitempty"`
	ElapsedMs             float64                   `json:"elapsed_ms,omitempty"`
	BusinessTransaction   bool                      `json:"business_transaction,omitempty"`
	CorrelationID         string                    `json:"correlation_id,omitempty"`
	TraceID               string                    `json:"trace_id,omitempty"`
	SpanID                string                    `json:"span_id,omitempty"`
	TraceSent             *bool                     `json:"trace_sent,omitempty"`
	TraceSource           string                    `json:"trace_source,omitempty"`
	SentTraceID           string                    `json:"sent_trace_id,omitempty"`
	SentSpanID            string                    `json:"sent_span_id,omitempty"`
	TraceLinks            []protocol.TraceLink      `json:"trace_links,omitempty"`
	TraceLinksTruncated   bool                      `json:"trace_links_truncated,omitempty"`
	DependencyType        string                    `json:"dependency_type,omitempty"`
	DependencyTarget      string                    `json:"dependency_target,omitempty"`
	DurationMs            float64                   `json:"duration_ms"`
	DNSMs                 float64                   `json:"dns_ms,omitempty"`
	ConnectMs             float64                   `json:"connect_ms,omitempty"`
	TLSMs                 float64                   `json:"tls_ms,omitempty"`
	TTFBMs                float64                   `json:"ttfb_ms,omitempty"`
	BytesSent             int64                     `json:"bytes_sent,omitempty"`
	BytesReceived         int64                     `json:"bytes_received,omitempty"`
	ByteEvidence          string                    `json:"byte_evidence,omitempty"` // what the byte counts measure (protocol.ByteEvidence); without it they are unavailable, not zero
	StatusCode            int                       `json:"status_code,omitempty"`
	Success               bool                      `json:"success"`
	ErrorMessage          string                    `json:"error_message,omitempty"`
	Extractions           map[string]string         `json:"extractions,omitempty"`
	PageURL               string                    `json:"page_url,omitempty"`
	PageTitle             string                    `json:"page_title,omitempty"`
	ConsoleEntries        []protocol.ConsoleEntry   `json:"console_entries,omitempty"`
	NetworkEntries        []protocol.NetworkEntry   `json:"network_entries,omitempty"`
	DOMSummary            *protocol.DOMSummary      `json:"dom_summary,omitempty"`
	ClientMetrics         *protocol.ClientMetrics   `json:"client_metrics,omitempty"`
	ServerTelemetry       *protocol.ServerTelemetry `json:"server_telemetry,omitempty"`
	ProtocolEvidence      map[string]any            `json:"protocol_evidence,omitempty"`
	Snapshot              *protocol.ErrorSnapshot   `json:"snapshot,omitempty"`
	SourceStepID          string                    `json:"source_step_id,omitempty"`
	SourceStepIndex       int                       `json:"source_step_index,omitempty"`
	SourceAction          string                    `json:"source_action,omitempty"`
	SourceActionName      string                    `json:"source_action_name,omitempty"`
	SourceProfile         string                    `json:"source_profile,omitempty"`
	// HTTP version facts of a native HTTP action; the full receipt stays in
	// protocol_evidence. HTTPVersion is the negotiated wire version (empty
	// for a cache hit or a request that got no response).
	HTTPVersion         string `json:"http_version,omitempty"`
	HTTPVersionFallback string `json:"http_version_fallback,omitempty"`
	AttemptCount        int    `json:"attempt_count,omitempty"`

	// ExtractionDigests are the business-key digests of mapped extractions,
	// computed from the raw values before scrubbing.
	ExtractionDigests map[string]string `json:"extraction_digests,omitempty"`
}

type ResourceSample struct {
	Timestamp                         time.Time          `json:"timestamp"`
	TestID                            string             `json:"test_id"`
	TestName                          string             `json:"test_name"`
	ActiveVUsers                      int64              `json:"active_vusers"`
	CPUProcessPct                     float64            `json:"cpu_process_pct"`
	CPUFreePct                        float64            `json:"cpu_free_pct"`
	MemProcessMB                      float64            `json:"mem_process_mb"`
	MemFreeMB                         float64            `json:"mem_free_mb"`
	NetSentKbps                       float64            `json:"net_sent_kbps"`
	NetRecvKbps                       float64            `json:"net_recv_kbps"`
	DiskReadKbps                      float64            `json:"disk_read_kbps"`
	DiskWriteKbps                     float64            `json:"disk_write_kbps"`
	CPUUserPct                        float64            `json:"cpu_user_pct,omitempty"`
	CPUSystemPct                      float64            `json:"cpu_system_pct,omitempty"`
	CPUIOWaitPct                      float64            `json:"cpu_iowait_pct,omitempty"`
	CPUStealPct                       float64            `json:"cpu_steal_pct,omitempty"`
	Load1                             float64            `json:"load1,omitempty"`
	Load5                             float64            `json:"load5,omitempty"`
	Load15                            float64            `json:"load15,omitempty"`
	ThreadCount                       int32              `json:"thread_count,omitempty"`
	OpenFDCount                       int32              `json:"open_fd_count,omitempty"`
	GoroutineCount                    int32              `json:"goroutine_count,omitempty"`
	HeapAllocMB                       float64            `json:"heap_alloc_mb,omitempty"`
	HeapInUseMB                       float64            `json:"heap_in_use_mb,omitempty"`
	GCCountDelta                      uint32             `json:"gc_count_delta,omitempty"`
	GCPauseMs                         float64            `json:"gc_pause_ms,omitempty"`
	ContainerCPULimitCores            float64            `json:"container_cpu_limit_cores,omitempty"`
	ContainerCPUUsagePct              float64            `json:"container_cpu_usage_pct,omitempty"`
	ContainerCPUThrottledPeriodsDelta uint64             `json:"container_cpu_throttled_periods_delta,omitempty"`
	ContainerCPUThrottledSecondsRate  float64            `json:"container_cpu_throttled_seconds_rate,omitempty"`
	ContainerMemoryWorkingSetMB       float64            `json:"container_memory_working_set_mb,omitempty"`
	ContainerMemoryLimitMB            float64            `json:"container_memory_limit_mb,omitempty"`
	ContainerMemoryUsagePct           float64            `json:"container_memory_usage_pct,omitempty"`
	ContainerMemoryFailCountDelta     uint64             `json:"container_memory_fail_count_delta,omitempty"`
	ContainerOOMKillCountDelta        uint64             `json:"container_oom_kill_count_delta,omitempty"`
	NetDropInPerSec                   float64            `json:"net_drop_in_per_sec,omitempty"`
	NetDropOutPerSec                  float64            `json:"net_drop_out_per_sec,omitempty"`
	NetErrorInPerSec                  float64            `json:"net_error_in_per_sec,omitempty"`
	NetErrorOutPerSec                 float64            `json:"net_error_out_per_sec,omitempty"`
	TCPRetransmitsPerSec              float64            `json:"tcp_retransmits_per_sec,omitempty"`
	TCPEstablished                    int                `json:"tcp_established,omitempty"`
	TCPTimeWait                       int                `json:"tcp_time_wait,omitempty"`
	EphemeralPortUtilizationPct       float64            `json:"ephemeral_port_utilization_pct,omitempty"`
	DiskIOTimePct                     float64            `json:"disk_io_time_pct,omitempty"`
	DiskWeightedIOTimePct             float64            `json:"disk_weighted_io_time_pct,omitempty"`
	DiskInProgress                    uint64             `json:"disk_in_progress,omitempty"`
	External                          map[string]float64 `json:"external_metrics,omitempty"`

	// Generator is the target of the load generator that took a distributed
	// run's sample, stamped when generator archives merge; empty for a local
	// run and for archives merged before it was recorded.
	Generator string `json:"generator,omitempty"`
	// ContainerCPUPeriodsDelta is the CFS periods elapsed since the previous
	// sample, against which throttled periods are a share.
	ContainerCPUPeriodsDelta uint64 `json:"container_cpu_periods_delta,omitempty"`
}

type IntervalSummaryRecord struct {
	JavaHTTP                 *protocol.JavaHTTPActions             `json:"java_http,omitempty"`
	BrowserDocuments         *protocol.BrowserDocumentSummary      `json:"browser_documents,omitempty"`
	MessagingProgress        *protocol.MessagingProgressSnapshot   `json:"messaging_progress,omitempty"`
	Messaging                map[string]*protocol.MessagingSummary `json:"messaging,omitempty"`
	ObservationState         string                                `json:"observation_state,omitempty"`
	CompletedIterations      int64                                 `json:"completed_iterations,omitempty"`
	Timestamp                time.Time                             `json:"timestamp"`
	TestID                   string                                `json:"test_id"`
	TestName                 string                                `json:"test_name"`
	ActiveVUsers             int64                                 `json:"active_vusers"`
	TPS                      float64                               `json:"tps"`
	TargetTPS                float64                               `json:"target_tps,omitempty"`
	ThroughputGapPct         float64                               `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed   bool                                  `json:"throughput_target_missed,omitempty"`
	AvgResponseMs            float64                               `json:"avg_response_ms"`
	ErrorPct                 float64                               `json:"error_pct"`
	TotalTx                  int64                                 `json:"total_transactions"`
	TotalErrors              int64                                 `json:"total_errors"`
	P50Ms                    float64                               `json:"p50_ms"`
	P90Ms                    float64                               `json:"p90_ms"`
	P95Ms                    float64                               `json:"p95_ms"`
	P99Ms                    float64                               `json:"p99_ms"`
	LoadLevels               []ActiveLoadLevel                     `json:"load_levels,omitempty"`
	ActiveRuntimeSeconds     *float64                              `json:"active_runtime_seconds,omitempty"`
	SampleWindowSeconds      float64                               `json:"sample_window_seconds,omitempty"`
	LoadLevelDurationDigests map[string]*engine.TDigestSnapshot    `json:"load_level_duration_digests,omitempty"`
	// DurationDigest is an optional generator-archive staging field. Distributed
	// finalization consumes it into fleet scalar percentiles; controller archives
	// keep the existing scalar reporting contract.
	DurationDigest *engine.TDigestSnapshot `json:"duration_digest,omitempty"`
	// DurationSketch stages the generator's version 2 distribution beside
	// DurationDigest; finalization prefers it.
	DurationSketch *engine.DurationSketchSnapshot `json:"duration_sketch,omitempty"`
	// LoadLevelDurationSketches holds the version 2 distributions beside
	// LoadLevelDurationDigests; the report prefers them.
	LoadLevelDurationSketches map[string]*engine.DurationSketchSnapshot `json:"load_level_duration_sketches,omitempty"`
	// PercentileQuality identifies how the interval percentile distribution was
	// obtained. It is empty for legacy/local rows, "source_generator" for an
	// unmerged generator row, "merged_sketch" when generator sketches were
	// combined, "merged_tdigest" when generator digests were (archives from
	// generators that predate the sketch), "recomputed_from_transactions" when
	// the controller rebuilt the fleet distribution, and "unavailable" when
	// multiple generator distributions could not be merged without inventing a
	// percentile.
	PercentileQuality string                                 `json:"percentile_quality,omitempty"`
	Groups            map[string]*GroupLiveMetrics           `json:"groups,omitempty"`
	Transactions      map[string]*IntervalTransactionMetrics `json:"transactions,omitempty"`
	ByRegion          map[string]*IntervalTransactionMetrics `json:"by_region,omitempty"`
	Browser           *BrowserLiveMetrics                    `json:"browser,omitempty"`
	BrowserByDevice   map[string]*BrowserDeviceLiveMetrics   `json:"browser_by_device,omitempty"`
	Telemetry         *TelemetryHealth                       `json:"telemetry,omitempty"`
	TelemetryStatus   *TelemetryRunHealth                    `json:"telemetry_status,omitempty"`
	ELK               *ELKCaptureStatus                      `json:"elk,omitempty"`
	Dynatrace         *DynatraceCaptureStatus                `json:"dynatrace,omitempty"`
}

// IntervalTransactionMetrics is the bounded, chart-focused transaction
// breakdown persisted with an interval summary. Count is the number of
// attempts, Errors is a subset of Count, and TPS is the successful transaction
// rate for the interval, matching the live TxMetrics contract.
type IntervalTransactionMetrics struct {
	Name   string  `json:"name"`
	TPS    float64 `json:"tps"`
	Count  int64   `json:"count"`
	Errors int64   `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
}

type archivePage struct {
	Limit  int
	Offset int
}

type transactionEventFilter struct {
	GroupDimension    string
	GroupValue        string
	ActionExact       string
	TimeToExclusive   bool
	PropertyName      string
	PropertyValue     string
	Population        string
	Action            string
	SOAPOperation     string
	Group             string
	Region            string
	NetworkProfile    string
	SourceTransaction string
	Hierarchy         string
	Phase             string
	HasDetail         *bool
	CorrelationID     string
	TraceID           string
	DependencyType    string
	DependencyTarget  string
	TimeFrom          *time.Time
	TimeTo            *time.Time
	Status            string
	MinDurationMs     *float64
	MaxDurationMs     *float64
	MinBytes          *int64
	MaxBytes          *int64
	ErrorContains     string
	Success           *bool
	SortBy            string
	SortDir           string
}

// RunArchiveIdentityNames holds the display names for the ids in the manifest
// provenance. It is resolved at render time (never persisted in the archive) so
// reports show what the UI shows instead of opaque ids.
type RunArchiveIdentityNames struct {
	Project     string `json:"project,omitempty"`
	Application string `json:"application,omitempty"`
	Release     string `json:"release,omitempty"`
	Scenario    string `json:"scenario,omitempty"`
}

type RunArchiveReport struct {
	TelemetryPolicy                  *TelemetryPolicyEvidence              `json:"telemetry_policy,omitempty"`
	BrowserDocumentEvidence          []protocol.BrowserDocumentObservation `json:"browser_document_evidence,omitempty"`
	BrowserDocumentEvidenceTruncated bool                                  `json:"browser_document_evidence_truncated,omitempty"`
	BrowserDocuments                 *protocol.BrowserDocumentSummary      `json:"browser_documents,omitempty"`
	// BrowserSoftNavigation states, beside a document summary, that soft
	// (same-document) navigations are not measured: "unsupported".
	BrowserSoftNavigation string                              `json:"browser_soft_navigation,omitempty"`
	MessagingProgress     *protocol.MessagingProgressSnapshot `json:"messaging_progress,omitempty"`
	MonitorDiagnostics    []RunMonitorDiagnostic              `json:"monitor_diagnostics,omitempty"`
	MonitorRuleResults    []analysis.MonitorRuleResult        `json:"monitor_rule_results,omitempty"`
	StatisticalFindings   []StatisticalFinding                `json:"statistical_findings,omitempty"`
	BaselineComparison    *BaselineComparisonSnapshot         `json:"baseline_comparison,omitempty"`
	IdentityNames         RunArchiveIdentityNames             `json:"identity_names"`
	Outcome               *IntegrationOutcome                 `json:"outcome,omitempty"`
	ByTransaction         map[string]RunArchiveActionStats    `json:"by_transaction,omitempty"`
	ByUnknown             map[string]RunArchiveActionStats    `json:"by_unknown,omitempty"`
	ByRequest             map[string]RunArchiveActionStats    `json:"by_request,omitempty"`
	MetricPopulations     map[string]ArchiveMetricPopulation  `json:"metric_populations,omitempty"`
	// GroupBreakdown is, per metric population, each group's totals and the
	// transactions (or requests) inside it. GroupBreakdownTruncated names the
	// populations that had more group and name pairs than one report keeps.
	GroupBreakdown                    map[string]map[string]RunArchiveGroupBreakdown `json:"group_breakdown,omitempty"`
	GroupBreakdownTruncated           []string                                       `json:"group_breakdown_truncated,omitempty"`
	SchemaVersion                     string                                         `json:"schema_version"`
	Compatibility                     RunArchiveReportCompatibility                  `json:"compatibility"`
	Completeness                      RunArchiveCompletenessScore                    `json:"completeness"`
	ChangeAttribution                 RunArchiveChangeAttribution                    `json:"change_attribution"`
	ReportProfiles                    []RunArchiveReportProfile                      `json:"report_profiles,omitempty"`
	NetworkPhases                     RunArchiveNetworkPhaseSummary                  `json:"network_phases,omitempty"`
	HostNetwork                       []RunArchiveHostNetworkEvidence                `json:"host_network,omitempty"`
	HTTP3Preflight                    *RunArchiveHTTP3Preflight                      `json:"http3_preflight,omitempty"`
	ArtifactInventory                 []RunArchiveArtifact                           `json:"artifact_inventory,omitempty"`
	Integrity                         *RunArchiveIntegrityReport                     `json:"integrity,omitempty"`
	Manifest                          *RunArchiveManifest                            `json:"manifest"`
	Overview                          RunArchiveOverview                             `json:"overview"` // the mixed legacy population (requests and their transactions together, as are ByAction and ByGroup); MetricPopulations, ByRequest and ByTransaction keep them apart
	Verdict                           *ReportVerdict                                 `json:"verdict,omitempty"`
	GeneratedFindings                 []ReportFinding                                `json:"generated_findings,omitempty"` // the derived F-01… list the exports show without analyst findings
	SLARows                           map[string]SLAComplianceRow                    `json:"sla_rows,omitempty"`
	TransactionResponseTimeSLAResults []TransactionResponseTimeSLAResult             `json:"transaction_response_time_sla_results,omitempty"`
	SLARollup                         *RunArchiveSLARollup                           `json:"sla_rollup,omitempty"`
	EvidenceTiers                     []RunArchiveEvidenceTier                       `json:"evidence_tiers,omitempty"`
	ELKSamples                        []ELKLogSample                                 `json:"elk_samples,omitempty"`
	DynatraceEvidence                 []DynatraceEvidenceRecord                      `json:"dynatrace_evidence,omitempty"`
	ErrorPatterns                     []RunArchiveErrorPattern                       `json:"error_patterns,omitempty"`
	SummaryTimeline                   []RunArchiveSummaryPoint                       `json:"summary_timeline,omitempty"`
	LoadLevelSummaries                []RunArchiveLoadLevelSummary                   `json:"load_level_summaries,omitempty"`
	ByAction                          map[string]RunArchiveActionStats               `json:"by_action"`
	ByGroup                           map[string]RunArchiveActionStats               `json:"by_group,omitempty"`
	BySOAPOperation                   map[string]RunArchiveActionStats               `json:"by_soap_operation,omitempty"`
	ByProperty                        map[string]RunArchiveActionStats               `json:"by_property,omitempty"`
	ByNetworkProfile                  map[string]RunArchiveActionStats               `json:"by_network_profile,omitempty"`
	ByRegion                          map[string]RunArchiveActionStats               `json:"by_region,omitempty"`
	BySOAPFaultCode                   map[string]int64                               `json:"by_soap_fault_code,omitempty"`
	BySOAPFaultCategory               map[string]int64                               `json:"by_soap_fault_category,omitempty"`
	SOAPFaultTimeline                 []RunArchiveSOAPFaultTimelineBucket            `json:"soap_fault_timeline,omitempty"`
	SOAPPayloadTimeline               []RunArchiveSOAPPayloadTimelineBucket          `json:"soap_payload_timeline,omitempty"`
	BrowserByEngine                   map[string]*BrowserEngineLiveMetrics           `json:"browser_by_engine,omitempty"`
	BrowserByPlatform                 map[string]*BrowserPlatformLiveMetrics         `json:"browser_by_platform,omitempty"`
	BrowserByDevice                   map[string]*BrowserDeviceLiveMetrics           `json:"browser_by_device,omitempty"`
	BrowserResources                  []BrowserResourceTiming                        `json:"browser_resources,omitempty"`
	AuraActions                       []AuraActionRollup                             `json:"aura_actions,omitempty"`
	BrowserVersionSkews               []BrowserVersionSkew                           `json:"browser_version_skews,omitempty"`
	BrowserIterations                 []BrowserIterationTransaction                  `json:"browser_iterations,omitempty"`
	SalesforceEPT                     []SalesforceEPTCalibration                     `json:"salesforce_ept,omitempty"`
	AuraParity                        []AuraParityTransaction                        `json:"aura_parity,omitempty"`
	Web2Migration                     *RunArchiveWeb2MigrationEvidence               `json:"web2_migration,omitempty"`
	OracleDB                          *RunArchiveOracleDBEvidence                    `json:"oracle_db,omitempty"`
	ResourcePeaks                     RunArchiveResourcePeaks                        `json:"resource_peaks"`
	// ReleaseTrend is the compact release-over-release trend for this run (this run
	// vs the immediately prior run in the same release). It is nil by default:
	// buildRunArchiveReport is a pure function of a single archive record, so the
	// multi-run trend is attached at the storage-aware handler layer via
	// (*Server).attachReleaseTrend. It stays nil for runs with no release association.
	ReleaseTrend *RunArchiveReleaseTrendSummary `json:"release_trend,omitempty"`
	// DashboardPanels is the compact summary of the imported Grafana dashboard panels
	// captured for this run. Like ReleaseTrend it is nil by default and attached at the
	// storage-aware handler layer (attachDashboardPanels) from the run's frozen
	// snapshot, so buildRunArchiveReport stays a pure function of one archive record.
	DashboardPanels *RunArchiveDashboardPanels `json:"dashboard_panels,omitempty"`
	// RootCauseView is the generated root cause every report format renders
	// (v2 where the project presents it, else v1). It is attached at the
	// handler layer (attachRootCause); a report without it renders the v1
	// summary computed from the report itself.
	RootCauseView *ReportRootCause `json:"root_cause_view,omitempty"`
	// RootCauseSummary is the run's root cause in brief: whether the
	// project presents it, the newest analysis's state and the newest
	// completed verdict. Attached by every archive report handler, single and
	// batch (attachRootCauseSummary).
	RootCauseSummary *ReportRootCauseSummary `json:"root_cause_summary,omitempty"`

	// Validity says whether the run's load generators and telemetry could
	// carry the load it reports (apexload rules validity.v1).
	Validity *rca.Validity `json:"validity,omitempty"`

	// TelemetryAcceptance is the run's telemetry verdict under its pinned
	// acceptance policy, apart from the application verdict (C7).
	TelemetryAcceptance *TelemetryAcceptanceVerdict `json:"telemetry_acceptance,omitempty"`
	// BrowserIntelligence holds the browser findings, interaction patterns
	// and journey accounting a report renders (the browser baseline is in
	// the root-cause section). It is attached only for an export that
	// renders it ((*Server).attachBrowserIntelligenceForProfile), never to the
	// JSON report, so it is not part of the JSON contract.
	BrowserIntelligence *ReportBrowserIntelligence `json:"-"`
}

// RunArchiveLoadLevelSummary preserves the evidence collected while one
// stepped-ramp level was active. Partial is true when an early stop or another
// lifecycle transition ended the observed interval before its configured hold.
type RunArchiveLoadLevelSummary struct {
	Group                   string  `json:"group"`
	StageIndex              int     `json:"stage_index"`
	Name                    string  `json:"name"`
	TargetVUsers            int     `json:"target_vusers"`
	ConfiguredDuration      string  `json:"configured_duration"`
	ObservedDurationSeconds float64 `json:"observed_duration_seconds"`
	Requests                int64   `json:"requests"`
	Errors                  int64   `json:"errors"`
	ErrorRatePct            float64 `json:"error_rate_pct"`
	Throughput              float64 `json:"throughput"`
	P50Ms                   float64 `json:"p50_ms"`
	P90Ms                   float64 `json:"p90_ms"`
	P95Ms                   float64 `json:"p95_ms"`
	P99Ms                   float64 `json:"p99_ms"`
	PercentileQuality       string  `json:"percentile_quality,omitempty"`
	Partial                 bool    `json:"partial,omitempty"`
}

// RunArchiveDashboardPanels is a report-friendly digest of a run's imported dashboard
// snapshot: per-panel summary statistics rather than the full resolved time series.
type RunArchiveDashboardPanels struct {
	CapturedAt time.Time             `json:"captured_at,omitempty"`
	Dashboards []RunArchiveDashboard `json:"dashboards"`
}

type RunArchiveDashboard struct {
	Title  string                     `json:"title"`
	Panels []RunArchiveDashboardPanel `json:"panels"`
}

type RunArchiveDashboardPanel struct {
	Title               string   `json:"title"`
	Kind                string   `json:"kind"`
	Unit                string   `json:"unit,omitempty"`
	SeriesCount         int      `json:"series_count,omitempty"`
	Min                 *float64 `json:"min,omitempty"`
	Avg                 *float64 `json:"avg,omitempty"`
	Max                 *float64 `json:"max,omitempty"`
	Last                *float64 `json:"last,omitempty"`
	Value               *float64 `json:"value,omitempty"`
	Error               string   `json:"error,omitempty"`
	ThresholdConfigured bool     `json:"threshold_configured,omitempty"`
	ThresholdEvaluated  bool     `json:"threshold_evaluated,omitempty"`
	ThresholdBreached   bool     `json:"threshold_breached,omitempty"`
	ThresholdDetail     string   `json:"threshold_detail,omitempty"`
}

type RunArchiveReportProfile struct {
	ID               string   `json:"id"`
	Label            string   `json:"label"`
	Description      string   `json:"description"`
	Audience         string   `json:"audience,omitempty"`
	BestFor          string   `json:"best_for,omitempty"`
	ExportFormats    []string `json:"export_formats"`
	IncludedSections []string `json:"included_sections"`
	SectionOrder     []string `json:"section_order,omitempty"`
	// RootCauseHypothesisLimit caps the generated hypotheses the rca section
	// shows; zero shows them all.
	RootCauseHypothesisLimit int `json:"root_cause_hypothesis_limit,omitempty"`
}

type RunArchiveNetworkPhaseSummary struct {
	Samples      int64                              `json:"samples,omitempty"`
	Overall      RunArchiveNetworkPhases            `json:"overall,omitempty"`
	ByAction     map[string]RunArchiveNetworkPhases `json:"by_action,omitempty"`
	ByDependency map[string]RunArchiveNetworkPhases `json:"by_dependency,omitempty"`
	// ByHTTPVersion holds phase averages and maxima per negotiated wire
	// version (1.1, 2, 3); cache hits and requests without a response are
	// not counted.
	ByHTTPVersion map[string]RunArchiveNetworkPhases `json:"by_http_version,omitempty"`
	// VersionMix counts every HTTP action, measured phases or not;
	// VersionMixByAction splits the same counts by action name.
	VersionMix         *RunArchiveHTTPVersionMix           `json:"version_mix,omitempty"`
	VersionMixByAction map[string]*protocol.HTTPVersionMix `json:"version_mix_by_action,omitempty"`
}

type RunArchiveNetworkPhases struct {
	Count        int64   `json:"count,omitempty"`
	AvgDNSMs     float64 `json:"avg_dns_ms,omitempty"`
	AvgConnectMs float64 `json:"avg_connect_ms,omitempty"`
	AvgTLSMs     float64 `json:"avg_tls_ms,omitempty"`
	AvgTTFBMs    float64 `json:"avg_ttfb_ms,omitempty"`
	MaxDNSMs     float64 `json:"max_dns_ms,omitempty"`
	MaxConnectMs float64 `json:"max_connect_ms,omitempty"`
	MaxTLSMs     float64 `json:"max_tls_ms,omitempty"`
	MaxTTFBMs    float64 `json:"max_ttfb_ms,omitempty"`
}

type RunArchiveWeb2MigrationEvidence struct {
	Status                string         `json:"status"`
	ImportedSteps         int            `json:"imported_steps"`
	ReplayedSteps         int            `json:"replayed_steps"`
	FailedSteps           int            `json:"failed_steps"`
	TransactionGroups     []string       `json:"transaction_groups,omitempty"`
	SourceProfiles        []string       `json:"source_profiles,omitempty"`
	ReplayEvidence        map[string]int `json:"replay_evidence"`
	ConversionToHTTPReady bool           `json:"conversion_to_http_ready"`
	NextActions           []string       `json:"next_actions,omitempty"`
}

type RunArchiveChangeAttribution struct {
	ScriptRevisions       []string `json:"script_revisions,omitempty"`
	ScenarioRevision      string   `json:"scenario_revision,omitempty"`
	Environment           string   `json:"environment,omitempty"`
	GeneratorVersions     []string `json:"generator_versions,omitempty"`
	DataRevision          string   `json:"data_revision,omitempty"`
	RuntimeOverrides      bool     `json:"runtime_overrides"`
	RuntimeOverrideFields []string `json:"runtime_override_fields,omitempty"`
}

type RunArchiveCompletenessScore struct {
	ScorePct float64                         `json:"score_pct"`
	Status   string                          `json:"status"`
	Findings []RunArchiveCompletenessFinding `json:"findings,omitempty"`
}

type RunArchiveCompletenessFinding struct {
	Dimension string  `json:"dimension"`
	Severity  string  `json:"severity"`
	Penalty   float64 `json:"penalty"`
	Message   string  `json:"message"`
}

type RunArchiveReportCompatibility struct {
	Status                   string                          `json:"status"`
	ReportSchemaVersion      string                          `json:"report_schema_version"`
	ManifestSchemaVersion    string                          `json:"manifest_schema_version,omitempty"`
	SupportedManifestSchemas []string                        `json:"supported_manifest_schemas"`
	Outputs                  []RunArchiveOutputCompatibility `json:"outputs"`
	Issues                   []RunArchiveCompatibilityIssue  `json:"issues,omitempty"`
}

type RunArchiveOutputCompatibility struct {
	Format        string `json:"format"`
	Artifact      string `json:"artifact,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	Status        string `json:"status"`
	Reason        string `json:"reason,omitempty"`
}

type RunArchiveCompatibilityIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type RunArchiveEvidenceTier struct {
	Tier        string `json:"tier"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	Count       int64  `json:"count,omitempty"`
	Description string `json:"description,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type BrowserResourceTiming struct {
	BrowserEvidence *protocol.BrowserResourceEvidence `json:"browser_evidence,omitempty"`
	FailureKind     string                            `json:"failure_kind,omitempty"`
	Timestamp       time.Time                         `json:"timestamp"`
	ActionName      string                            `json:"action_name,omitempty"`
	PageURL         string                            `json:"page_url,omitempty"`
	URL             string                            `json:"url,omitempty"`
	Method          string                            `json:"method,omitempty"`
	StatusCode      int                               `json:"status_code,omitempty"`
	ResourceType    string                            `json:"resource_type,omitempty"`
	DurationMs      float64                           `json:"duration_ms,omitempty"`
	BytesReceived   int64                             `json:"bytes_received,omitempty"`
	DeviceProfile   string                            `json:"device_profile,omitempty"`
	DevicePlatform  string                            `json:"device_platform,omitempty"`
	BrowserEngine   string                            `json:"browser_engine,omitempty"`
}

type RunArchiveSummaryPoint struct {
	BrowserDocuments       *protocol.BrowserDocumentSummary `json:"browser_documents,omitempty"`
	Timestamp              time.Time                        `json:"timestamp"`
	ActiveVUsers           int64                            `json:"active_vusers"`
	TPS                    float64                          `json:"tps"`
	TargetTPS              float64                          `json:"target_tps,omitempty"`
	ThroughputGapPct       float64                          `json:"throughput_gap_pct,omitempty"`
	ThroughputTargetMissed bool                             `json:"throughput_target_missed,omitempty"`
	ErrorPct               float64                          `json:"error_pct"`
	P95Ms                  float64                          `json:"p95_ms"`
	PercentileQuality      string                           `json:"percentile_quality,omitempty"`
	TotalTx                int64                            `json:"total_transactions"`
	TotalErrors            int64                            `json:"total_errors"`
}

func (point RunArchiveSummaryPoint) HasP95() bool {
	return archivePercentileAvailable(point.PercentileQuality)
}

func archivePercentileAvailable(quality string) bool {
	return !strings.EqualFold(strings.TrimSpace(quality), archivePercentileQualityUnavailable)
}

type RunArchiveErrorPattern struct {
	Signature string `json:"signature"`
	Sample    string `json:"sample"`
	// Count is the failures of one metric population (requests and their
	// enclosing named transactions are both archived); CountsByPopulation
	// keeps the per-population figures.
	Count              int64                   `json:"count"`
	CountsByPopulation map[string]int64        `json:"counts_by_population,omitempty"`
	FirstSeen          time.Time               `json:"first_seen"`
	LatestSeen         time.Time               `json:"latest_seen"`
	Actions            []string                `json:"actions,omitempty"`
	Snapshot           *protocol.ErrorSnapshot `json:"snapshot,omitempty"`
}

type RunArchiveSOAPFaultTimelineBucket struct {
	Timestamp  time.Time        `json:"timestamp"`
	Total      int64            `json:"total"`
	ByCode     map[string]int64 `json:"by_code,omitempty"`
	ByCategory map[string]int64 `json:"by_category,omitempty"`
}

type RunArchiveSOAPPayloadTimelineBucket struct {
	Timestamp             time.Time `json:"timestamp"`
	Samples               int64     `json:"samples"`
	AvgRequestPayloadKB   float64   `json:"avg_request_payload_kb,omitempty"`
	AvgResponsePayloadKB  float64   `json:"avg_response_payload_kb,omitempty"`
	AvgEnvelopeOverheadKB float64   `json:"avg_envelope_overhead_kb,omitempty"`
	MaxEnvelopeOverheadKB float64   `json:"max_envelope_overhead_kb,omitempty"`
}

type BrowserEngineLiveMetrics struct {
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	DocumentLoads        int64   `json:"document_loads"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type BrowserPlatformLiveMetrics struct {
	DevicePlatform       string  `json:"device_platform,omitempty"`
	BrowserEngine        string  `json:"browser_engine,omitempty"`
	Samples              int64   `json:"samples"`
	DocumentLoads        int64   `json:"document_loads"`
	AvgFCPMs             float64 `json:"avg_fcp_ms,omitempty"`
	AvgLCPMs             float64 `json:"avg_lcp_ms,omitempty"`
	AvgCLSScore          float64 `json:"avg_cls_score,omitempty"`
	AvgINPMs             float64 `json:"avg_inp_ms,omitempty"`
	AvgTTFBMs            float64 `json:"avg_ttfb_ms,omitempty"`
	AvgBlockingResources float64 `json:"avg_blocking_resources,omitempty"`
	AvgLongTaskCount     float64 `json:"avg_long_task_count,omitempty"`
	AvgJSHeapUsedMB      float64 `json:"avg_js_heap_used_mb,omitempty"`
	AvgBrowserProcessCPU float64 `json:"avg_browser_process_cpu_pct,omitempty"`
	AvgBrowserProcessMem float64 `json:"avg_browser_process_mem_mb,omitempty"`
}

type RunArchiveOverview struct {
	Transactions              int64                       `json:"transactions"`
	Errors                    int64                       `json:"errors"`
	ErrorPct                  float64                     `json:"error_pct"`
	AvgResponseMs             float64                     `json:"avg_response_ms"`
	MinResponseMs             float64                     `json:"min_response_ms"`
	MaxResponseMs             float64                     `json:"max_response_ms"`
	P50Ms                     float64                     `json:"p50_ms,omitempty"`
	P90Ms                     float64                     `json:"p90_ms,omitempty"`
	P95Ms                     float64                     `json:"p95_ms,omitempty"`
	P99Ms                     float64                     `json:"p99_ms,omitempty"`
	PercentileQuality         string                      `json:"percentile_quality,omitempty"`
	ResourceSamples           int64                       `json:"resource_samples"`
	IntervalSummaries         int64                       `json:"interval_summaries"`
	PeakTPS                   float64                     `json:"peak_tps"`
	PeakTargetTPS             float64                     `json:"peak_target_tps,omitempty"`
	MaxThroughputGapPct       float64                     `json:"max_throughput_gap_pct,omitempty"`
	ThroughputMissedIntervals int64                       `json:"throughput_missed_intervals,omitempty"`
	PeakActiveVUsers          int64                       `json:"peak_active_vusers"`
	Browser                   *BrowserLiveMetrics         `json:"browser,omitempty"`
	Telemetry                 *RunArchiveTelemetrySummary `json:"telemetry,omitempty"`
	LoadModelGoals            *RunArchiveLoadModelGoals   `json:"load_model_goals,omitempty"`
	GoalCompletion            *RunArchiveGoalCompletion   `json:"goal_completion,omitempty"`
	GoalAttainment            *RunArchiveGoalAttainment   `json:"goal_attainment,omitempty"`
}

// RunArchiveGoalAttainment states a goal or traffic-profile run's TPS goal
// against what it delivered, judged over the same 10 s windows the run was
// steered on: each window is within tolerance of its target, over it or
// under it.
type RunArchiveGoalAttainment struct {
	TargetAverageTPS   float64 `json:"target_average_tps"`
	AchievedAverageTPS float64 `json:"achieved_average_tps"`
	WindowSeconds      int64   `json:"window_seconds"`
	TolerancePct       float64 `json:"tolerance_pct"`
	Windows            int64   `json:"windows"`
	WithinWindows      int64   `json:"within_windows"`
	OverWindows        int64   `json:"over_windows"`
	UnderWindows       int64   `json:"under_windows"`
}

type RunArchiveLoadModelGoals struct {
	ConfiguredGlobalIterations int64   `json:"configured_global_iterations,omitempty"`
	ConfiguredMaxTransactions  int64   `json:"configured_max_transactions,omitempty"`
	TargetTPS                  float64 `json:"target_tps,omitempty"`
	StagedGroups               int     `json:"staged_groups,omitempty"`
	StageCount                 int     `json:"stage_count,omitempty"`
	OnceOnlyGroups             int     `json:"once_only_groups,omitempty"`
}

type RunArchiveGoalCompletion struct {
	AchievedTransactions       int64   `json:"achieved_transactions"`
	ConfiguredMaxTransactions  int64   `json:"configured_max_transactions,omitempty"`
	TransactionCompletionPct   float64 `json:"transaction_completion_pct,omitempty"`
	ConfiguredGlobalIterations int64   `json:"configured_global_iterations,omitempty"`
	IterationEvidence          string  `json:"iteration_evidence,omitempty"`
	ThroughputTargetTPS        float64 `json:"throughput_target_tps,omitempty"`
	ThroughputMissedIntervals  int64   `json:"throughput_missed_intervals,omitempty"`
}

type RunArchiveTelemetrySummary struct {
	Status            string `json:"status"`
	Samples           int64  `json:"samples"`
	HealthyIntervals  int64  `json:"healthy_intervals,omitempty"`
	DegradedIntervals int64  `json:"degraded_intervals,omitempty"`
	BreachedIntervals int64  `json:"breached_intervals,omitempty"`
	// UnscoredIntervals had a budget decision on a cross-host duration that
	// could not be scored and no breach; they are neither healthy nor breached.
	UnscoredIntervals           int64            `json:"unscored_intervals,omitempty"`
	SustainedBreachIntervals    int64            `json:"sustained_breach_intervals,omitempty"`
	DroppedResults              int64            `json:"dropped_results,omitempty"`
	WebsocketDroppedMessages    int64            `json:"websocket_dropped_messages,omitempty"`
	WebsocketDroppedEvents      int64            `json:"websocket_dropped_events,omitempty"`
	MaxSampleAgeMs              float64          `json:"max_sample_age_ms,omitempty"`
	MaxGeneratorProcessingLagMs float64          `json:"max_generator_processing_lag_ms,omitempty"`
	MaxTransportLagMs           float64          `json:"max_transport_lag_ms,omitempty"`
	MaxControllerFanoutLagMs    float64          `json:"max_controller_fanout_lag_ms,omitempty"`
	MaxBrowserRenderLagMs       float64          `json:"max_browser_render_lag_ms,omitempty"`
	FirstBreachAt               *time.Time       `json:"first_breach_at,omitempty"`
	LastBreachAt                *time.Time       `json:"last_breach_at,omitempty"`
	ConditionCounts             map[string]int64 `json:"condition_counts,omitempty"`
}

type RunArchiveActionStats struct {
	Messaging          *protocol.MessagingSummary   `json:"messaging,omitempty"`
	JavaHTTP           *protocol.JavaHTTPSummary    `json:"java_http,omitempty"`
	GRPC               *protocol.GRPCSummary        `json:"grpc,omitempty"`
	WebSocket          *protocol.WebSocketSummary   `json:"websocket,omitempty"`
	DiagnosticFindings []protocol.DiagnosticFinding `json:"diagnostic_findings,omitempty"`
	Transactions       int64                        `json:"transactions"`
	Errors             int64                        `json:"errors"`
	ErrorPct           float64                      `json:"error_pct"`
	AvgResponseMs      float64                      `json:"avg_response_ms"`
	MinResponseMs      float64                      `json:"min_response_ms"`
	MaxResponseMs      float64                      `json:"max_response_ms"`
	PeakTPS            float64                      `json:"peak_tps,omitempty"`
	P50Ms              float64                      `json:"p50_ms,omitempty"`
	P90Ms              float64                      `json:"p90_ms,omitempty"`
	P95Ms              float64                      `json:"p95_ms,omitempty"`
	P99Ms              float64                      `json:"p99_ms,omitempty"`
	PercentileQuality  string                       `json:"percentile_quality,omitempty"`
	durations          []float64
	// javaHTTPActions counts actual java_http actions, which JavaHTTP.Population
	// reports; enclosing transactions and other protocols' actions are excluded.
	javaHTTPActions int64
}

type RunArchiveResourcePeaks struct {
	CPUProcessPct                    float64 `json:"cpu_process_pct"`
	MemProcessMB                     float64 `json:"mem_process_mb"`
	NetSentKbps                      float64 `json:"net_sent_kbps"`
	NetRecvKbps                      float64 `json:"net_recv_kbps"`
	DiskReadKbps                     float64 `json:"disk_read_kbps"`
	DiskWriteKbps                    float64 `json:"disk_write_kbps"`
	CPUIOWaitPct                     float64 `json:"cpu_iowait_pct,omitempty"`
	CPUStealPct                      float64 `json:"cpu_steal_pct,omitempty"`
	GCPauseMs                        float64 `json:"gc_pause_ms,omitempty"`
	ContainerCPUUsagePct             float64 `json:"container_cpu_usage_pct,omitempty"`
	ContainerCPUThrottledSecondsRate float64 `json:"container_cpu_throttled_seconds_rate,omitempty"`
	ContainerMemoryUsagePct          float64 `json:"container_memory_usage_pct,omitempty"`
	ContainerOOMKillCountDelta       uint64  `json:"container_oom_kill_count_delta,omitempty"`
	NetDropInPerSec                  float64 `json:"net_drop_in_per_sec,omitempty"`
	NetDropOutPerSec                 float64 `json:"net_drop_out_per_sec,omitempty"`
	NetErrorInPerSec                 float64 `json:"net_error_in_per_sec,omitempty"`
	NetErrorOutPerSec                float64 `json:"net_error_out_per_sec,omitempty"`
	TCPRetransmitsPerSec             float64 `json:"tcp_retransmits_per_sec,omitempty"`
	DiskIOTimePct                    float64 `json:"disk_io_time_pct,omitempty"`
}

type BrowserDiagnosticsBundle struct {
	SchemaVersion  string                     `json:"schema_version"`
	GeneratedAt    time.Time                  `json:"generated_at"`
	Manifest       *RunArchiveManifest        `json:"manifest"`
	Overview       BrowserDiagnosticsOverview `json:"overview"`
	Events         []BrowserDiagnosticsEvent  `json:"events"`
	TotalEvents    int64                      `json:"total_events"`
	RetainedEvents int64                      `json:"retained_events"`
	DroppedEvents  int64                      `json:"dropped_events"`
	Truncated      bool                       `json:"truncated"`
}

type BrowserDiagnosticsOverview struct {
	BrowserTransactions       int64 `json:"browser_transactions"`
	FailedBrowserTransactions int64 `json:"failed_browser_transactions"`
	ClientMetricSamples       int64 `json:"client_metric_samples"`
	ScreenshotArtifacts       int64 `json:"screenshot_artifacts"`
	HTMLArtifacts             int64 `json:"html_artifacts"`
	ConsoleEntries            int64 `json:"console_entries"`
	NetworkEntries            int64 `json:"network_entries"`
	DOMSnapshots              int64 `json:"dom_snapshots"`
}

type BrowserDiagnosticsEvent struct {
	Index          int                        `json:"index"`
	Timestamp      time.Time                  `json:"timestamp"`
	Group          string                     `json:"group,omitempty"`
	ActionName     string                     `json:"action_name,omitempty"`
	DurationMs     float64                    `json:"duration_ms"`
	StatusCode     int                        `json:"status_code,omitempty"`
	Success        bool                       `json:"success"`
	ErrorMessage   string                     `json:"error_message,omitempty"`
	PageURL        string                     `json:"page_url,omitempty"`
	PageTitle      string                     `json:"page_title,omitempty"`
	ClientMetrics  *protocol.ClientMetrics    `json:"client_metrics,omitempty"`
	ConsoleEntries []protocol.ConsoleEntry    `json:"console_entries,omitempty"`
	NetworkEntries []protocol.NetworkEntry    `json:"network_entries,omitempty"`
	DOMSummary     *protocol.DOMSummary       `json:"dom_summary,omitempty"`
	Snapshot       *BrowserDiagnosticSnapshot `json:"snapshot,omitempty"`
	Artifacts      BrowserDiagnosticArtifacts `json:"artifacts,omitempty"`

	screenshotBase64 string
	html             string
}

type BrowserDiagnosticSnapshot struct {
	Resources        []protocol.ResourceSnapshot `json:"resources,omitempty"`
	OmittedResources int                         `json:"omitted_resources,omitempty"`
	RequestMethod    string                      `json:"request_method,omitempty"`
	RequestURL       string                      `json:"request_url,omitempty"`
	RequestHeaders   map[string]string           `json:"request_headers,omitempty"`
	RequestBody      string                      `json:"request_body,omitempty"`
	ResponseStatus   int                         `json:"response_status,omitempty"`
	ResponseHeaders  map[string]string           `json:"response_headers,omitempty"`
	ResponseBody     string                      `json:"response_body,omitempty"`
}

type BrowserDiagnosticArtifacts struct {
	ScreenshotFile string `json:"screenshot_file,omitempty"`
	HTMLFile       string `json:"html_file,omitempty"`
}

var archiveErrorUUIDPattern = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\b`)
var archiveErrorNumberPattern = regexp.MustCompile(`\b\d+\b`)
var archiveErrorWhitespacePattern = regexp.MustCompile(`\s+`)
var archiveSnapshotLeadingTemplateBasePattern = regexp.MustCompile(`^\{\{\s*(?:var|param)\.[^{}]+\s*\}\}\s*(.*)$`)

type runArchiveWriter struct {
	// businessKeys digests the run's business-key values (G16).
	businessKeys            *businessKeyArchiver
	captureBudget           *archiveCaptureBudget
	conversionBudget        *archiveOutputBudget
	mu                      sync.Mutex
	manifest                RunArchiveManifest
	region                  string
	rootDir                 string
	manifestPath            string
	txFile                  *os.File
	resourceFile            *os.File
	summaryFile             *os.File
	elkFile                 *os.File
	browserDocumentsFile    *os.File
	browserDocumentsBuf     *bufio.Writer
	browserDocumentsEncoder *json.Encoder
	dynatraceFile           *os.File
	txBuf                   *bufio.Writer
	resourceBuf             *bufio.Writer
	summaryBuf              *bufio.Writer
	elkBuf                  *bufio.Writer
	dynatraceBuf            *bufio.Writer
	txEncoder               *json.Encoder
	resourceEncoder         *json.Encoder
	summaryEncoder          *json.Encoder
	elkEncoder              *json.Encoder
	dynatraceEncoder        *json.Encoder
	flushStop               chan struct{}
	flushStopped            chan struct{}
	flushErr                error
	detailUsage             engine.MessageDetailUsage
	// Provider lookup outcomes stream here when a capture can look up traces.
	lookupFile    *os.File
	lookupBuf     *bufio.Writer
	lookupEncoder *json.Encoder
	// harMu serializes HAR moves with Finalize; it is taken before mu.
	harMu    sync.Mutex
	harBytes int64
	// harSources are the HAR-directory copies of archived HARs, retired once
	// the archive is finalized.
	harSources         []runArchiveHARSource
	interactionsFile   *os.File
	interactionBytes   int64
	interactionsBroken bool
	// interactionCount is the interactions written into the sidecar.
	interactionCount int64
}

func cloneArchiveTestGroups(groups []TestGroup) []TestGroup {
	if len(groups) == 0 {
		return nil
	}
	cloned := make([]TestGroup, len(groups))
	copy(cloned, groups)
	for i := range cloned {
		cloned[i].BrowserDocumentCaptureRequested = groupRequestsBrowserDocumentCapture(groups[i])
		cloned[i].EffectiveEndAt = cloneTimePtr(groups[i].EffectiveEndAt)
		cloned[i].EndedAt = cloneTimePtr(groups[i].EndedAt)
		cloned[i].Stages = cloneLoadStages(groups[i].Stages)
		cloned[i].Iterations = cloneIntPtr(groups[i].Iterations)
		cloned[i].IterationOverride = cloneIntPtr(groups[i].IterationOverride)
		cloned[i].RuntimeSettings = cloneRuntimeSettings(groups[i].RuntimeSettings)
		cloned[i].Assignment = cloneGeneratorAssignmentSettings(groups[i].Assignment)
		cloned[i].RunLogicGoals = cloneRunLogicGoalSnapshot(groups[i].RunLogicGoals)
		cloned[i].TransactionResponseTimeSLAMs = cloneFloat64Ptr(groups[i].TransactionResponseTimeSLAMs)
		cloned[i].runtimeSecretValues = nil
	}
	return cloned
}

func cloneGeneratorInventoryEntries(entries []GeneratorInventoryEntry) []GeneratorInventoryEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]GeneratorInventoryEntry, len(entries))
	copy(cloned, entries)
	for index := range cloned {
		if entries[index].PodPlacement != nil {
			placement := *entries[index].PodPlacement
			cloned[index].PodPlacement = &placement
		}
		cloned[index].PodCapacity = entries[index].PodCapacity.Clone()
		cloned[index].SupportedProtocols = append([]string(nil), entries[index].SupportedProtocols...)
		cloned[index].Labels = cloneStringMap(entries[index].Labels)
		cloned[index].CompatibleFeatures = append([]string(nil), entries[index].CompatibleFeatures...)
		cloned[index].RuntimeCapacities = cloneProtocolRuntimeCapacities(entries[index].RuntimeCapacities)
		cloned[index].JavaHost = cloneJavaHostCapabilitySummary(entries[index].JavaHost)
		cloned[index].CompatibilityIssues = append([]string(nil), entries[index].CompatibilityIssues...)
		cloned[index].LastSafetyExit = cloneGeneratorSafetyExitEvidence(entries[index].LastSafetyExit)
		cloned[index].SafetyExits = append([]GeneratorSafetyExitEvidence(nil), entries[index].SafetyExits...)
	}
	return cloned
}

func cloneGeneratorSafetyExitEvidence(value *GeneratorSafetyExitEvidence) *GeneratorSafetyExitEvidence {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

type archiveActionSummaryBucket struct {
	transactions    int64
	errors          int64
	totalDurationMs float64
	durations       []float64
}

// archiveFilteredThroughputTarget derives the throughput target, gap, and
// missed flag for a filtered interval row. The whole-test target does not apply
// to a filtered subset, so it is recomputed from the selected groups' own
// per-group targets when those were captured. When no per-group target exists
// (older archives) or only an action filter is applied, it returns zeros rather
// than a gap measured against a target the subset never owned.

func cloneProtocolConsoleEntries(entries []protocol.ConsoleEntry) []protocol.ConsoleEntry {
	if len(entries) == 0 {
		return nil
	}
	cloned := make([]protocol.ConsoleEntry, len(entries))
	copy(cloned, entries)
	return cloned
}

func cloneProtocolNetworkEntries(entries []protocol.NetworkEntry) []protocol.NetworkEntry {
	return protocol.CloneNetworkEntries(entries)
}

func cloneProtocolDOMSummary(summary *protocol.DOMSummary) *protocol.DOMSummary {
	if summary == nil {
		return nil
	}
	cloned := *summary
	if len(summary.VisibleText) > 0 {
		cloned.VisibleText = append([]string(nil), summary.VisibleText...)
	}
	return &cloned
}

func normalizeArchiveErrorSignature(message string) string {
	normalized := strings.TrimSpace(strings.ToLower(message))
	if normalized == "" {
		return "request failed"
	}
	normalized = archiveErrorUUIDPattern.ReplaceAllString(normalized, "<id>")
	normalized = archiveErrorNumberPattern.ReplaceAllString(normalized, "#")
	normalized = archiveErrorWhitespacePattern.ReplaceAllString(normalized, " ")
	return strings.TrimSpace(normalized)
}

func cloneGroupLiveMetricsMap(metrics map[string]*GroupLiveMetrics) map[string]*GroupLiveMetrics {
	if len(metrics) == 0 {
		return nil
	}
	cloned := make(map[string]*GroupLiveMetrics, len(metrics))
	for name, groupMetrics := range metrics {
		cloned[name] = cloneGroupLiveMetrics(groupMetrics)
	}
	return cloned
}

func zipDirectory(sourceDir string, dst io.Writer) error {
	return zipDirectoryContext(context.Background(), sourceDir, dst)
}

func zipDirectoryContext(ctx context.Context, sourceDir string, dst io.Writer) error {
	return zipDirectoryContextWithReplacements(ctx, sourceDir, dst, nil)
}

func zipDirectoryContextWithReplacements(ctx context.Context, sourceDir string, dst io.Writer, replacements map[string][]byte) error {
	return zipDirectoryContextWithSanitizers(ctx, sourceDir, dst, replacements, nil, nil)
}

func zipDirectoryContextWithSanitizers(ctx context.Context, sourceDir string, dst io.Writer, replacements map[string][]byte, streamReplacements map[string]func(io.Writer) error, omit map[string]struct{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	zipWriter := zip.NewWriter(dst)
	root, err := openArchiveDirectoryNoSymlink(sourceDir)
	if err != nil {
		_ = zipWriter.Close()
		return err
	}
	err = zipArchiveDirectoryRoot(ctx, root, "", zipWriter, replacements, streamReplacements, omit)
	rootCloseErr := root.Close()
	zipCloseErr := zipWriter.Close()
	if err != nil {
		return err
	}
	if rootCloseErr != nil {
		return rootCloseErr
	}
	return zipCloseErr
}

func zipArchiveDirectoryRoot(ctx context.Context, root *os.Root, prefix string, zipWriter *zip.Writer, replacements map[string][]byte, streamReplacements map[string]func(io.Writer) error, omit map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := entry.Name()
		if name == archiveMaterializationCacheMarkerSuffix {
			continue
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains symbolic link %s", filepath.Join(prefix, name))
		}
		archiveName := filepath.ToSlash(filepath.Join(prefix, name))
		if _, omitted := omit[archiveName]; omitted {
			continue
		}
		if info.IsDir() {
			child, err := openArchiveChildDirectoryAt(root, name, false, 0)
			if err != nil {
				return err
			}
			err = zipArchiveDirectoryRoot(ctx, child, archiveName, zipWriter, replacements, streamReplacements, omit)
			closeErr := child.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive contains non-regular artifact %s", archiveName)
		}
		if replacement, ok := replacements[archiveName]; ok {
			fileWriter, err := zipWriter.Create(archiveName)
			if err != nil {
				return err
			}
			if _, err := fileWriter.Write(replacement); err != nil {
				return err
			}
			continue
		}
		if streamReplacement, ok := streamReplacements[archiveName]; ok {
			fileWriter, err := zipWriter.Create(archiveName)
			if err != nil {
				return err
			}
			if err := streamReplacement(fileWriter); err != nil {
				return err
			}
			continue
		}
		file, _, err := openArchiveArtifactForReadAt(root, name)
		if err != nil {
			return err
		}
		fileWriter, err := zipWriter.Create(archiveName)
		if err != nil {
			_ = file.Close()
			return err
		}
		_, copyErr := io.Copy(fileWriter, archiveContextReader{ctx: ctx, r: file})
		fileCloseErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if fileCloseErr != nil {
			return fileCloseErr
		}
	}
	return nil
}
