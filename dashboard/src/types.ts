export type { TelemetryBudgetPolicy, TelemetryPolicyEvidence } from './types/telemetryPolicy'
// Compatibility barrel for dashboard domain contracts. New API/domain code
// should import from ./types/<domain>; existing consumers can migrate without a
// flag day while these re-exports preserve their public type names.
import type { ScriptActionDefinition, ScriptDefinition, ScriptParameterDefinition } from './types/scripts'
import type { LibraryImportResultItem, LibraryRemote } from './types/library'
import type { DashboardAuditEvent } from './types/auth'
import type { DiagnosticDistribution, WebSocketDiagnostics, WebSocketSummary, ProtocolDiagnosticFinding } from './types/protocolDiagnostics'
import type {
  SystemInventoryGenerator,
} from './types/system'
import type {
  BrowserReplayConsoleEntry,
  BrowserReplayDomSummary,
  BrowserReplayNetworkEntry,
  BrowserReplaySnapshot,
  ProtocolEvidence,
} from './types/replay'
import type { ActiveLoadLevel, ClientMetrics } from './types/metrics'
import type { BrowserDocumentSummary, BrowserDocumentObservation } from './types/browserDocumentEvidence'
import type { ScenarioDataPoolBinding } from './types/dataPools'
import type { TrafficProfile } from './types/trafficProfiles'
import type {
  RedactionRule,
  RuntimeSettingsOverride,
  RuntimeSettingsReview,
  ScenarioRuntimeSettingsDraft,
} from './types/runtimeSettings'
import type { CapacityAdvisory } from './types/capacity'
import type { LoadModelValidationReport, LoadStageDraft } from './types/loadModels'
import type {
  AssignmentRecommendation,
  ContinueOnErrorResolution,
  GeneratorAssignmentSettings,
  GeneratorShardSummary,
  ScenarioFootprint,
  TestGroup,
} from './types/testConfiguration'
import type { GoalConfig, GoalStatus, GoalTargetStatus, ScenarioMode, TransactionResponseTimeSLAResult, TransactionSLARuleResult } from './types/goals'
import type { AuthoringHealth } from './types/authoring'

export type { ReplayPreflight, ReplayPreflightCheck } from './types/scripts'
export type {
  DashboardAccessReviewResponse,
  DashboardAccessReviewServiceAccount,
  DashboardAccessReviewUser,
  DashboardAuthenticatedUser,
  DashboardAuditEvent,
  DashboardAuditEventFilter,
  DashboardAuditEventListResponse,
  DashboardAuthAccessMatrix,
  DashboardAuthAccessOperation,
  DashboardAuthAccessResource,
  DashboardAuthDiagnostics,
  DashboardAuthDiagnosticsCheck,
  DashboardAuthEffectiveAccess,
  DashboardAuthEffectiveAccessOperation,
  DashboardAuthEffectiveAccessResource,
  DashboardAuthPolicy,
  DashboardAuthRoleCatalog,
  DashboardAuthRoleDefinition,
  DashboardAuthSettings,
  DashboardAuthStatus,
  DashboardCreateServiceAccountRequest,
  DashboardCreateServiceAccountResponse,
  DashboardCreateUserBindingRequest,
  DashboardCreateUserRequest,
  DashboardOnboardUserRequest,
  DashboardServiceAccount,
  DashboardServiceAccountListResponse,
  DashboardUpdateServiceAccountRequest,
  DashboardUpdateUserRequest,
  DashboardUser,
  DashboardUserActivityResponse,
  DashboardUserListResponse,
  DashboardUserRoleBinding,
  DashboardUserRoleScopeType,
  DashboardUserUsageBucket,
} from './types/auth'
export type {
  GitHubAppInstallation,
  GitHubBranch,
  GitHubConfigResponse,
  GitHubConnectionResponse,
  GitHubContentEntry,
  GitHubContentsResponse,
  GitHubDiagnosticsResponse,
  GitHubPushResponse,
  GitHubRepo,
  GitHubScriptStatus,
  GitHubSyncJob,
} from './types/github'
export type {
  CapacityAdvisory,
  CapacityDimensionRecommendations,
  CapacityReservation,
  CapacityResourceEstimate,
  CapacityTimeslot,
  CapacityTimeslotAvailabilityResponse,
  CapacityTimeslotListResponse,
  CapacityTimeslotRequest,
  ProtocolDensityEnvelope,
} from './types/capacity'
export type {
  FleetAlert,
  FleetAlertsResponse,
  FleetCapacityResponse,
  FleetGeneratorMetadata,
  FleetGeneratorMetadataListResponse,
  FleetPool,
  FleetPoolListResponse,
  FleetPoolMember,
  FleetProjectSlotAccounting,
  FleetProvisioningEvent,
  FleetProvisioningInstance,
  FleetProvisioningInstanceListResponse,
  FleetProvisioningRequest,
  FleetProvisioningRequestListResponse,
  ProvisionerInfo,
} from './types/fleet'
export type {
  AWSCredentialMode,
  AllocationPreflight,
  ClusterAuthMode,
  ClusterCapacityResponse,
  ClusterConnection,
  ClusterConnectionInput,
  ClusterConnectionListResponse,
  ClusterKind,
  ClusterNodeCapacity,
  ClusterNodePool,
  ClusterProbe,
  ClusterQuota,
  ClusterTLSMode,
  ClusterTestResult,
  ClusterWorkload,
  PoolAllocation,
  PoolAllocationInput,
  PoolAllocationResponse,
  Toleration,
} from './types/cluster'
export type {
  ConflictingFleetPool,
  DeploymentProvider,
  DomainDeploymentTarget,
  DomainDeploymentTargetConflict,
  DomainDeploymentTargetInput,
  DomainDeploymentTargetListResponse,
} from './types/deploymentTarget'
export type {
  BrowserBenchmarkAcceptance,
  BrowserBenchmarkDensityEnvelope,
  BrowserBenchmarkEvidenceResponse,
  BrowserBenchmarkProfile,
  CompetitiveParityArea,
  DeploymentIdentity,
  HealthResponse,
  ParityStatusArea,
  ParityStatusResponse,
  SystemInventoryController,
  SystemInventoryGenerator,
  SystemInventoryResponse,
  SystemReadinessCheck,
  SystemReadinessResponse,
  SystemRestartCapabilities,
  SystemRestartRequest,
  SystemRestartResult,
  SystemServiceEntry,
  SystemServicesResponse,
  UsageAccountingBucket,
  UsageAccountingResponse,
  UsageAccountingSnapshot,
  UsageAccountingSnapshotsResponse,
  VersionResponse,
} from './types/system'
export type {
  DashboardArchiveRetentionPolicy,
  DashboardArchiveRetentionSettings,
  DashboardCapacityAvailabilitySettings,
  DashboardCapacityGeneratorSettings,
  DashboardCapacityPlanningSettings,
  DashboardClientAppSettings,
  DashboardClientDownloadsSettings,
  DashboardGeneralSettings,
  DashboardGitHubSettings,
  DashboardGrafanaSettings,
  DashboardNotificationsSettings,
  DashboardNotificationWebhook,
  DashboardPrometheusSettings,
  DashboardOpenSearchSettings,
  DashboardScheduleDefaultsSettings,
  DashboardScriptImportSettings,
  DashboardSystemSettings,
  DashboardThresholdSettings,
} from './types/settings'
export type {
  RunCalendarCapacity,
  RunCalendarCapacityDay,
  RunCalendarEvent,
  RunCalendarRange,
  RunCalendarResponse,
  RunCalendarTopology,
  RunCalendarUser,
  RunCalendarWorkload,
  ScenarioSchedule,
  ScenarioScheduleApproval,
  ScenarioScheduleGovernance,
  ScenarioScheduleHealth,
  ScenarioScheduleOccurrence,
  ScenarioScheduleWindow,
} from './types/scheduling'
export type {
  BrowserReplayConsoleEntry,
  BrowserReplayDomSummary,
  BrowserReplayEvent,
  BrowserReplayNetworkEntry,
  BrowserReplayReport,
  BrowserReplaySession,
  BrowserReplaySessionResponse,
  BrowserReplaySnapshot,
  BrowserReplayStartRequest,
  BrowserReplayStartResponse,
  ConditionEvidence,
  ProtocolEvidence,
  ReplayArtifactManifest,
  ReplayDiagnosis,
  ReplayDiagnosisSummary,
  ReplayRepairSuggestion,
  ReplayRuntimeProfile,
  ScriptDebugAction,
  ScriptDebugBreakpoint,
  ScriptDebugLogEntry,
  ScriptDebugLogLevel,
  ScriptDebugSession,
  ScriptDebugSessionResponse,
  ScriptDebugStartRequest,
  ScriptDebugStartResponse,
  ScriptDebugStepMode,
} from './types/replay'
export type {
  ExecutionPlan,
  ExecutionPlanApproval,
  ExecutionPlanApprovalListResponse,
  ExecutionPlanEntry,
  ExecutionPlanListResponse,
  ExecutionPlanRun,
  ExecutionPlanRunEntry,
  ExecutionPlanRunListResponse,
  ExecutionPlanRunResponse,
  ExecutionPlanRunResult,
  ExecutionPlanSchedule,
  ExecutionPlanScheduleListResponse,
  ExecutionPlanScheduleOccurrence,
  ExecutionPlanScheduleOccurrenceListResponse,
  ExecutionPlanScheduleRequest,
  ScheduleOccurrenceResolutionRequest,
} from './types/executionPlans'
export type {
  CollectorMetricPack,
  CollectorMetricSnapshot,
  MonitoringCollector,
  MonitoringCollectorListResponse,
  MonitoringCollectorTokenResponse,
  ELKConnection,
  ELKConnectionListResponse,
  ELKMetricAggregation,
  ELKCaptureStatus,
  ELKLogSample,
  DynatraceConnection,
  DynatraceConnectionListResponse,
  DynatraceMetricQuery,
  DynatraceCaptureStatus,
  DynatraceLogSample,
  DynatraceProblemSummary,
  DynatraceEvidenceRecord,
} from './types/monitoring'
export type {
  EntitlementBundleConsumption,
  EntitlementLimitView,
  EntitlementsProtocolBundle,
  EntitlementsResponse,
  EntitlementsUsageResponse,
  ProjectEntitlementView,
  ProjectEntitlementsUsageResponse,
} from './types/entitlements'
export type {
  ObjectRepository,
  ObjectRepositoryListResponse,
  RepositoryObject,
  RepositoryObjectCandidate,
} from './types/objectRepository'
export type {
  CorrelationRule,
  CorrelationRuleset,
  CorrelationRulesetApplyResponse,
  CorrelationRulesetListResponse,
} from './types/correlationRulesets'
export type { ActiveLoadLevel, ClientMetrics } from './types/metrics'
export type {
  ScenarioDataPoolBinding,
  SharedDataPool,
  SharedDataPoolRow,
} from './types/dataPools'
export type {
  RedactionRule,
  RuntimeBrowserSettings,
  RuntimeJavaVuserSettings,
  RuntimeSettingsFinding,
  RuntimeSettingsOverride,
  RuntimeSettingsReview,
  ScenarioRuntimeSettingsDraft,
} from './types/runtimeSettings'
export type { LoadModelValidationItem, LoadModelValidationReport, LoadStageDraft } from './types/loadModels'
export type {
  AssignmentRecommendation,
  ContinueOnErrorResolution,
  GeneratorAssignmentSettings,
  GeneratorShardSummary,
  JavaVMShardOutcome,
  ScenarioFootprint,
  TestGroup,
} from './types/testConfiguration'
export type { GoalConfig, GoalStatus, GoalTargetStatus, ScenarioMode, TransactionResponseTimeSLAResult, TransactionSLARule, TransactionSLARuleResult } from './types/goals'
export type { AuthoringHealth, AuthoringHealthInput } from './types/authoring'
export type { ReleaseRef, ScenarioRef, StoredScenario, StoredScript } from './types/storedContent'
export type {
  TrafficProfile,
  TrafficProfileAnalyzeResponse,
  TrafficProfileDeriveRequest,
  TrafficProfileDeriveResponse,
  TrafficProfileGapPolicy,
  TrafficProfileMapping,
  TrafficProfilePoint,
  TrafficProfileRuntimePoint,
  TrafficProfileSeries,
  TrafficProfileSource,
  TrafficProfileSourceEntry,
  TrafficProfileSourcesResponse,
  TrafficProfileStats,
  TrafficProfileValueSemantics,
} from './types/trafficProfiles'

export interface Project {
  id: string
  domain_id?: string
  category_id?: string
  name: string
  settings?: ProjectSettings
  created_at: string
  updated_at: string
}

export interface Domain {
  id: string
  name: string
  description?: string
  created_at: string
  updated_at: string
}

export interface ApplicationRepository {
  id: string
  domain_id: string
  name: string
  description?: string
  created_at: string
  updated_at: string
}

export interface RepositoryCategory {
  id: string
  domain_id: string
  repository_id: string
  name: string
  description?: string
  sort_order?: number
  created_at: string
  updated_at: string
}

export interface ProjectSettings {
  // analysis holds the project's analysis opt-ins; it changes only through
  // PUT /projects/{id}/analysis-settings.
  analysis?: import('./types/rca').ProjectAnalysisSettings
  redaction_rules?: RedactionRule[]
  max_concurrent_tests?: number
  max_vusers?: number
  max_vusers_per_test?: number
  max_test_duration_minutes?: number
  github?: GitHubProjectPolicy
  // pool_quotas caps this project's footprint on a specific fleet pool (keyed by
  // fleet-pool id). Additive to the project-wide limits above.
  pool_quotas?: Record<string, PoolProjectQuota>
	features?: ProjectFeatureFlags
  telemetry?: ProjectTelemetryPolicy
}

// Opt-in telemetry evidence a project's viewers contribute (C5).
export interface ProjectTelemetryPolicy {
  // Dashboard tabs viewing a live run report receive-to-render times, which
  // feed the run's advisory browser_render stage.
  client_render_evidence?: boolean
}

export interface ProjectFeatureFlags {
	successful_message_detail?: boolean
	control_parallel?: boolean
	native_elastic_capacity?: boolean
	privileged_monitors?: boolean
	media_stream?: boolean
}

export interface PoolProjectQuota {
  max_vusers?: number
  max_concurrent_tests?: number
}

export interface GitHubProjectPolicy {
  enabled?: boolean
  allowed_repos?: string[]
  allowed_branches?: string[]
  path_template?: string
  require_pull_request?: boolean
  allow_direct_commit?: boolean
  allow_import?: boolean
  allow_push?: boolean
  bulk_sync_enabled?: boolean
  auto_sync_enabled?: boolean
  checks_enabled?: boolean
  app_installation_id?: number
  automation_identity?: string
  default_repo?: string
  default_branch?: string
  default_sync_directory?: string
  actions?: GitHubActionsPolicy
}

export interface GitHubActionsPolicy {
  enabled?: boolean
  issuer?: string
  server_url?: string
  audience?: string
  allowed_repository_ids?: string[]
  allowed_repositories?: string[]
  allowed_refs?: string[]
  allowed_workflow_refs?: string[]
  allowed_environments?: string[]
  required_runner_environment?: string
  max_vusers?: number
  max_duration_minutes?: number
  allowed_protocols?: string[]
  allowed_target_hosts?: string[]
  checks_enabled?: boolean
  revision?: number
  updated_at?: string
  updated_by?: string
  /** Fails CI runs whose validity is "invalid", or with "suspect" also suspect runs; empty is advisory. */
  validity_gate?: '' | 'invalid' | 'suspect'
}

export interface Application {
  id: string
  project_id?: string
  name: string
  created_at: string
  updated_at: string
}

export interface Release {
  id: string
  group_id?: string
  application_id: string
  application_release_id?: string
  member_ids?: Record<string, string>
  project_id?: string
  name: string
  created_at: string
  updated_at: string
  archived_at?: string
}

export interface Folder {
  id: string
  project_id: string
  parent_id?: string | null
  name: string
  category: 'scripts' | 'scenarios' | 'results' | 'recordings'
  created_at: string
  updated_at: string
}

export type DurationUnit = 's' | 'm' | 'h'

export interface DurationFieldValue {
  value: string
  unit: DurationUnit
}

export type RampEntryMode = 'simple' | 'stair_step'

export interface StageTimelineValidationResult {
  status: 'ok' | 'warning' | 'error'
  totalSeconds: number
  scenarioDurationSeconds: number
  message?: string
}

export interface RunComparisonBaseline {
  source_test_id: string
  source_test_name: string
  policy?: 'pinned' | 'latest_successful' | 'latest_finished' | string
  completed_at?: string
  transactions?: Record<string, TxMetrics>
  dependency_health?: Record<string, TxMetrics>
}

export interface RunArchiveExecutionSettingsSnapshot {
  general: {
    api_endpoint: string
    max_concurrent_tests: number
    metrics_interval_seconds: number
  }
  thresholds: {
    max_error_rate_pct?: number
    max_p99_latency_ms?: number
    max_avg_server_processing_ms?: number
    max_avg_transfer_ms?: number
    max_telemetry_sample_age_ms?: number
    max_telemetry_generator_lag_ms?: number
    max_telemetry_transport_lag_ms?: number
    max_telemetry_controller_fanout_ms?: number
    max_telemetry_browser_render_lag_ms?: number
    fleet_constrained_available_slots?: number
    fleet_saturated_available_slots?: number
  }
  capacity_planning: {
    headroom_pct: number
    generator: {
      cpu_cores: number
      memory_mb: number
      network_mbps: number
      max_vusers: number
    }
    availability: {
      source: string
      configured_available_generators: number
    }
  }
}

export interface ReleaseTrendDeltas {
  error_pct_delta: number
  avg_response_ms_delta: number
  avg_response_pct_delta: number
  peak_tps_delta: number
  completeness_delta: number
}

export interface ReleaseTrendPoint {
 metric_population?: string
  run_id: string
  run_name: string
  status: string
  completed_at: string
  transactions: number
  error_pct: number
  avg_response_ms: number
  peak_tps: number
  peak_active_vusers: number
  completeness_pct: number
  sla_breached: boolean
  telemetry_status?: string
  deltas?: ReleaseTrendDeltas
  regressions?: string[]
}

export interface ReleaseTrendSummary {
  latest_regressed: boolean
  latest_regressions?: string[]
  avg_response_trend_pct: number
  error_pct_trend: number
}

export interface ReleaseTrend {
  release_id: string
  run_count: number
  points: ReleaseTrendPoint[]
  summary: ReleaseTrendSummary
}

// RunArchiveReleaseTrendSummary is the compact, single-run slice of the multi-run
// release trend attached to an archive report's executive view: this run vs the
// immediately prior run in the same release, plus the release-wide trajectory.
export interface RunArchiveReleaseTrendSummary {
 release_name?: string
  release_id: string
  run_count: number
  has_baseline: boolean
  regressed: boolean
  regressions?: string[]
  current: ReleaseTrendPoint
  baseline?: ReleaseTrendPoint
  deltas?: ReleaseTrendDeltas
  trajectory: ReleaseTrendSummary
  note?: string
}

export type LoadComparisonScope = 'transaction' | 'group'

export interface LoadComparisonOption {
  key: string
  label: string
  scope: LoadComparisonScope
  source_kind: 'source_transaction' | 'action' | 'group' | string
  release_count: number
  run_count: number
  transaction_count: number
  sample_count: number
}

export interface LoadComparisonLoadLevel {
  id: string
  label: string
  min_vusers: number
  max_vusers: number
  exact: boolean
  sample_count: number
}

export interface LoadComparisonLatency {
  samples: number
  average_ms: number
  p50_ms: number
  p95_ms: number
  p99_ms: number
}

export interface LoadComparisonPoint {
  duration_estimated?: boolean
  evidence_status?: 'observed' | 'no_samples' | 'inconsistent_counts'
  observed_seconds?: number
  successful_rate?: number
  latency?: LoadComparisonLatency
  successful_latency?: LoadComparisonLatency
  failed_latency?: LoadComparisonLatency
  run_ids?: string[]
  load_level_id: string
  load_label: string
  tps: number
  avg_response_ms: number
  transactions: number
  errors: number
  error_pct: number
  run_count: number
  sample_count: number
}

export interface LoadComparisonReleaseSeries {
  run_id?: string
  run_name?: string
  release_id: string
  release_name: string
  run_count: number
  transaction_count: number
  points: LoadComparisonPoint[]
}

export interface LoadComparisonResponse {
  analysis?: { population?: string; start_seconds: number; end_seconds?: number }
  run_series?: LoadComparisonReleaseSeries[]
  warnings?: string[]
  skipped_runs?: { test_id: string; reason: string }[]
  application_id: string
  scope: LoadComparisonScope
  selected_key?: string
  options: LoadComparisonOption[]
  load_levels: LoadComparisonLoadLevel[]
  releases: LoadComparisonReleaseSeries[]
}

export interface LoadComparisonRequestOptions {
  population?: 'action' | 'transaction' | 'unknown'
  startSeconds?: number
  endSeconds?: number
  testIds?: string[]
}

export interface RunArchiveProvenance {
  live_tables?: import('./types/dataPools').RunArchiveLiveTableSummary[]
  project_live_tables?: import('./types/projectLiveTables').ProjectLiveTableRunReference[]
  telemetry_policy?: import('./types/telemetryPolicy').TelemetryBudgetPolicy
  elk_capture?: { connection_id: string; connection_name?: string }
  dynatrace_capture?: { connection_id: string; connection_name?: string }
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  requested_at?: string
  started_at?: string
  scheduled_start_at?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  start_delay?: string
  total_vusers?: number
  duration?: string
  assignment?: GeneratorAssignmentSettings
  goal_config?: GoalConfig
  runtime_settings?: RuntimeSettingsOverride
  groups?: TestGroup[]
  generator_shards?: GeneratorShardSummary[]
  baseline_pinned?: boolean
  baseline_pinned_at?: string
  execution_settings?: RunArchiveExecutionSettingsSnapshot
  controller_env?: Record<string, string>
  created_by_subject?: string
  created_by_display_name?: string
  started_by_subject?: string
  started_by_display_name?: string
  stopped_by_subject?: string
  stopped_by_display_name?: string
  governance?: RunArchiveGovernanceSnapshot
  generator_inventory?: Array<{
    target: string
    generator_id?: string
    status: string
    available_capacity: number
    active_vusers?: number
    cpu_percent?: number
    memory_bytes?: number
    goroutine_count?: number
    cpu_limit_cores?: number
    memory_limit_bytes?: number
    max_vusers?: number
  }>
  // Analysis policy revisions pinned when the run started.
  policy_pins?: Array<{ kind?: string; id?: string; name?: string; revision?: number; source?: string }> | null
}

export interface RunArchiveGovernanceSnapshot {
  launch_preflight?: {
    decision?: string
    status?: string
    conflicts?: string[]
    warnings?: string[]
  }
  execution_plan_approval?: {
    id?: string
    plan_id?: string
    policy?: string
    status?: string
    requested_by?: string
    requested_at?: string
    resolved_by?: string
    resolved_at?: string
    decision_reason?: string
    required_role?: string
  }
  runtime_settings_review?: RuntimeSettingsReview
  capacity_reservation_id?: string
  capacity_reservation_ref?: string
}

export interface RunArchiveErrorPattern {
  signature: string
  sample: string
  count: number
  first_seen: string
  latest_seen: string
  actions?: string[]
  snapshot?: {
    request_url?: string
  }
}

/** One controller failover a run survived (a successor adopted it). */
export interface RunFailover {
  prior_generation: number
  generation: number
  adopted_at: string
  /** Newest live metrics persisted before the failover; nothing was observed between it and adoption. */
  last_observed_at?: string
  gap_seconds?: number
  adopted_generators: number
  /** Generators whose share finished while no controller was leading. */
  completed_generators?: number
}

export interface RunContinuityStatus {
  policy: 'fail' | 'continue'
  /** A successor controller holds what it needs to adopt the run. */
  armed: boolean
  execution_generation?: number
  failovers?: RunFailover[]
}

export interface TestSummary {
  /** Safe configured capture identities; credentials are never part of this projection. */
  elk_capture?: { connection_id: string; connection_name?: string }
  dynatrace_capture?: { connection_id: string; connection_name?: string }
  monitor_warnings?: string[]
  monitor_bindings?: { revision?: number; sources: import('./types/runMonitoring').MonitorBinding[] }
  activity?: import("./types/objectActivity").ObjectActivitySummary
  termination_mode?: "duration" | "iterations"
  id: string
  ref_key?: string
  name: string
  status: TestStatus
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  folder_id?: string | null
  data_pools?: ScenarioDataPoolBinding[]
  groups: TestGroup[]
  capacity_advisory?: CapacityAdvisory
  assignment?: GeneratorAssignmentSettings
  script_shakeout?: boolean
  resource_baseline?: boolean
  total_vusers: number
  duration: string
  start_delay?: string
  created_at: string
  scheduled_start_at?: string
  pending_since?: string
  pending_reason?: string
  queue_position?: number
  baseline_pinned?: boolean
  baseline_pinned_at?: string
  started_at?: string
  ended_at?: string
  /** While the run is paused: when this pause began. */
  paused_at?: string
  /** Time spent in earlier pauses, which the run makes up (nanoseconds). */
  cumulative_pause_ns?: number
  created_by_subject?: string
  created_by_display_name?: string
  started_by_subject?: string
  started_by_display_name?: string
  stopped_by_subject?: string
  stopped_by_display_name?: string
  generator_shards?: GeneratorShardSummary[]
  vuser_ramps?: Record<string, VUserRampStatus>
  goal_targets?: Record<string, GoalTargetStatus>
  /** HA run continuity; absent for fail-policy runs that never failed over. */
  continuity?: RunContinuityStatus
  live_metrics?: LiveMetrics
  sparkline?: TestSparklinePoint[]
  telemetry_health?: TelemetryRunHealth
  error?: string
  history?: LiveMetrics[]
  comparison_baseline?: RunComparisonBaseline
  start_time?: string
  runtime_settings?: RuntimeSettingsOverride
  scenario_mode?: ScenarioMode
  goal_config?: GoalConfig
  /** A traffic-profile run's replayed profile: its per-group TPS (or VUser) points by offset. */
  traffic_profile?: import('./types/trafficProfiles').TrafficProfile
  /** The execution-plan run that started this run. */
  execution_plan?: TestExecutionPlanLink
  archive?: RunArchiveRecord
  archive_failure?: RunArchiveFailure
  archive_recovery?: RunArchiveRecovery
  // backwards compat
  script_id?: string
  script_name?: string
}

/** The execution-plan run that started a run, and the run's place in it. */
export interface TestExecutionPlanLink {
  plan_id: string
  plan_name?: string
  plan_run_id: string
  entry_order: number
  /** 1-based position among the plan run's enabled entries. */
  entry_number?: number
  entry_count?: number
}

/** Durable recovery bookkeeping. Verified sources are historical collection receipts. */
export interface RunArchiveRecovery {
  state: string
  recovery_attempts?: number
  expected_sources?: number
  verified_sources?: number
  updated_at?: string
  retry_window_ends_at?: string
}

/** Observed collection failure; this is separate from archive publication retries. */
export interface RunArchiveFailure {
  reason: string
  recorded_at: string
}

export interface RuntimeAdjustmentGroupRequest {
  name: string
  target_duration?: string
  target_vusers?: number
  target_tps?: number
}

export interface RuntimeAdjustmentRequest {
  groups: RuntimeAdjustmentGroupRequest[]
  transition?: 'immediate' | 'gradual'
  ramp_duration?: string
  expected_revision?: string
}

export interface RuntimeAdjustmentGroupPreview {
  name: string
  current_duration: string
  target_duration: string
  current_vusers: number
  target_vusers: number
  current_target_tps?: number
  target_tps?: number
  effective_end_at?: string
  runtime_state: string
}

export interface RuntimeAdjustmentPreview {
  test_id: string
  decision: 'allowed' | 'blocked'
  revision: string
  groups: RuntimeAdjustmentGroupPreview[]
  impact: {
    scenario_duration: string
    scenario_end_at?: string
    peak_vusers: number
    recommended_generators: number
  }
  conflicts?: Array<{ code: string; source: string; message: string; starts_at?: string }>
  safe_proposal?: RuntimeAdjustmentRequest
}

export interface TestSparklinePoint {
  timestamp: string
  tps: number
}

export interface RemoteRunLink {
  local_run_id: string
  remote_id: string
  remote_test_id: string
  remote_url: string
  local_project_id?: string
  local_scenario_id?: string
  remote_project_id?: string
  status: string
  created_at: string
  updated_at: string
}

export interface RemoteRunProxyResponse {
  remote?: LibraryRemote
  link: RemoteRunLink
  test?: TestSummary
  action?: Record<string, unknown>
  pushed?: LibraryImportResultItem[]
}

export interface ScriptShakeoutResult {
  group_name: string
  script_id?: string
  script_name?: string
  status: 'passed' | 'failed' | string
  error?: string
}

export interface ScriptShakeoutReport {
  passed: boolean
  summary: string
  started_at?: string
  ended_at?: string
  results: ScriptShakeoutResult[]
}

export interface VUserRampStatus {
  group: string
  start_vusers: number
  target_vusers: number
  started_at: string
  ramp_duration: string
  ramp_duration_seconds: number
}

export interface RunArchiveRecord {
  test_id: string
  ref_key?: string
  schema_version: string
  archive_path: string
  manifest_path: string
  transaction_count: number
  resource_sample_count: number
  interval_summary_count: number
  created_at: string
  completed_at: string
}

export interface TestNote {
  id: string
  test_id: string
  body: string
  created_at: string
  updated_at: string
  created_by?: string
  updated_by?: string
}

export interface RunArchiveManifest {
  frozen_monitor_rules?: import('./types/runMonitoring').MonitorRule[]
  monitoring_status?: string
  monitor_coverage?: import('./types/runMonitoring').RunMonitorCoverage[]
  monitor_evidence_file?: string
  monitor_evidence_count?: number
  // phase_events_file holds the run's load-phase transitions
  // (apexload.phase_event.v1), read through /archive/files/{name}.
  phase_events_file?: string
  phase_events_truncated?: boolean
  monitor_diagnostics_file?: string
  monitor_diagnostics_count?: number
  monitor_rule_results?: import('./types/runMonitoring').MonitorRuleResult[]
  source_coverage?: { mode: string; sources: Array<{ target: string; collected: boolean; generator_id?: string; region?: string; groups?: string[] }> }
  csv_exports?: Array<{ id: string; group: string; file_name: string; status: string; rows: number; error?: string }>
  termination_reason?: string

  schema_version: string
  test_id: string
  test_name: string
  created_at: string
  completed_at?: string
  status?: string
  error?: string
  provenance?: RunArchiveProvenance
  transactions_file: string
  transactions_parquet_file?: string
  resource_samples_file: string
  resource_samples_parquet_file?: string
  interval_summaries_file: string
  interval_summaries_parquet_file?: string
  test_manifest_file?: string
  test_notes_file?: string
  artifacts?: RunArchiveArtifact[]
  transaction_count: number
  resource_sample_count: number
  interval_summary_count: number
  sla_breached?: boolean
  sla_details?: string[]
  transaction_rule_results?: TransactionSLARuleResult[]
	transaction_response_time_sla_results?: TransactionResponseTimeSLAResult[]
}

export interface RunArchiveArtifact {
  name: string
  kind?: string
  format?: string
  record_type?: string
  count?: number
  size_bytes?: number
  sha256?: string
}

export interface RunArchiveIntegrityArtifact {
  name: string
  status: 'match' | 'mismatch' | 'missing' | 'unverifiable' | string
  expected_sha256?: string
  actual_sha256?: string
  size_bytes?: number
  detail?: string
}

export interface RunArchiveIntegrityReport {
  schema_version: string
  test_id: string
  status: 'verified' | 'verified_partial' | 'tampered' | 'corrupt' | 'unverifiable' | string
  verified: boolean
  verified_at: string
  summary: string
  total: number
  matched: number
  mismatched: number
  missing: number
  unverifiable: number
  artifacts?: RunArchiveIntegrityArtifact[]
}

export interface TestRunManifestIdentity {
  application_id?: string
  release_id?: string
  scenario_id?: string
  project_id?: string
  folder_id?: string | null
  created_at?: string
  scheduled_start_at?: string
  started_at?: string
  ended_at?: string
  created_by_subject?: string
  created_by_display_name?: string
  started_by_subject?: string
  started_by_display_name?: string
  stopped_by_subject?: string
  stopped_by_display_name?: string
}

export interface TestRunManifestConfiguration {
  status?: string
  duration?: string
  total_vusers?: number
  active_vusers?: number
  start_delay?: string
  scenario_mode?: ScenarioMode
  script_shakeout?: boolean
  resource_baseline?: boolean
  assignment?: GeneratorAssignmentSettings
  generator_shards?: GeneratorShardSummary[]
  goal_config?: GoalConfig
  runtime_settings?: RuntimeSettingsOverride
  runtime_settings_review?: RuntimeSettingsReview
  groups?: TestGroup[]
  vuser_ramps?: Record<string, VUserRampStatus>
  baseline_pinned?: boolean
  baseline_pinned_at?: string
  started_at?: string
  ended_at?: string
}

export interface TestRunManifestConfigurationProvenance {
  source: 'archive_provenance' | 'stored_test_record' | 'retained_run_state' | string
  immutable: boolean
}

export interface TestRunManifestScript {
  group_name: string
  script_id?: string
  script_name?: string
  protocol?: string
  vusers?: number
  ramp_up?: string
	ramp_down?: string
	start_offset?: string
  stages?: LoadStageDraft[]
  iterations?: number
  runtime_settings?: RuntimeSettingsOverride
  assignment?: GeneratorAssignmentSettings
  run_logic_goals?: {
    global_iterations?: number
    max_transactions?: number
  }
  initialization?: string
  continue_on_error?: ContinueOnErrorResolution
}

export interface TestRunManifestContinueOnErrorConflict {
  group_name: string
  script_id?: string
  script_name?: string
  effective: boolean
  source: 'group' | 'script' | string
  detail: string
}

export interface TestRunManifestContinueOnError {
  scenario_value: boolean
  scenario_explicit: boolean
  conflicts?: TestRunManifestContinueOnErrorConflict[]
}

export interface TestRunManifestLoadGenerators {
  assignment?: GeneratorAssignmentSettings
  generator_shards?: GeneratorShardSummary[]
  generator_inventory?: SystemInventoryGenerator[]
  execution_settings?: {
    general?: Record<string, unknown>
    thresholds?: Record<string, unknown>
    capacity_planning?: Record<string, unknown>
  }
  controller_env?: Record<string, string>
}

export interface TestRunManifestRuntimeChange {
  id: string
  action: string
  label: string
  status?: string
  actor_type?: string
  actor_subject?: string
  actor_display_name?: string
  details?: Record<string, unknown>
  created_at: string
}

export interface TestRunManifestArchive {
  schema_version?: string
  archive_backend?: string
  archive_path?: string
  manifest_path?: string
  archive_uri?: string
  manifest_uri?: string
  test_manifest_file?: string
  test_notes_file?: string
  transaction_count?: number
  resource_sample_count?: number
  interval_summary_count?: number
  created_at?: string
  completed_at?: string
  sla_breached?: boolean
  sla_details?: string[]
  error?: string
}

export interface TestRunManifest {
  telemetry_policy?: import('./types/telemetryPolicy').TelemetryPolicyEvidence
  schema_version: string
  test_id: string
  ref_key?: string
  test_name: string
  status: string
  source: 'live' | 'archive' | 'archive_fallback' | string
  generated_at: string
  identity: TestRunManifestIdentity
  initial_configuration: TestRunManifestConfiguration
  // Older persisted manifests predate this field. Missing provenance is
  // intentionally treated as non-immutable by preview consumers.
  initial_configuration_provenance?: TestRunManifestConfigurationProvenance
  current_configuration: TestRunManifestConfiguration
  scripts?: TestRunManifestScript[]
  load_generators: TestRunManifestLoadGenerators
  runtime_changes?: TestRunManifestRuntimeChange[]
  archive?: TestRunManifestArchive
  // Absent on manifests whose groups were compiled before the scenario
  // continue-on-error policy existed.
  continue_on_error?: TestRunManifestContinueOnError
  warnings?: string[]
}

export interface TestGroupParameterDataSource {
  name: string
  type?: string
  columns?: string[]
  row_count?: number
}

export interface TestGroupRuntimeSettingsView {
  test_id: string
  ref_key?: string
  group: string
  group_index: number
  script_id?: string
  script_name?: string
  protocol?: string
  status: string
  editable: boolean
  revision: string
  editable_reason?: string
  editable_fields: string[]
  runtime_settings?: RuntimeSettingsOverride
  test_runtime_settings?: RuntimeSettingsOverride
  script_settings?: RuntimeSettingsOverride
  effective_settings?: RuntimeSettingsOverride
  imported_runtime_source?: {
    files?: Record<string, Record<string, Record<string, string>>>
    dispositions?: Record<string, Record<string, Record<string, {
      status?: string
      parity_id?: string
      reason?: string
      fields?: string[]
      note?: string
    }>>>
    truncated?: boolean
  }
}

export interface TestGroupRuntimeSettingsUpdateResult {
  test_id: string
  group: string
  fields: string[]
  no_changes?: boolean
  changes?: Record<string, { from?: unknown; to?: unknown }>
  runtime_settings?: RuntimeSettingsOverride
  review_status?: string
  revision: string
}

export interface TestGroupParametersView {
  test_id: string
  ref_key?: string
  group: string
  group_index: number
  script_id?: string
  script_name?: string
  protocol?: string
  status: string
  editable: boolean
  revision: string
  editable_reason?: string
  parameters: ScriptParameterDefinition[]
  data_sources?: TestGroupParameterDataSource[]
}

export interface TestGroupParametersUpdateResult {
  test_id: string
  group: string
  changed: string[]
  no_changes?: boolean
  changes?: Record<string, unknown>
  parameters: ScriptParameterDefinition[]
  revision: string
}

export interface TransactionEvent {
	event_id?: string
  timestamp: string
  test_id: string
  test_name: string
  group?: string
	region?: string
	network_profile?: string
	source_transaction?: string
	hierarchy?: string
	phase?: string
	has_detail?: boolean
  action_name?: string
  correlation_id?: string
  trace_id?: string
  span_id?: string
  dependency_type?: string
  dependency_target?: string
  duration_ms: number
  dns_ms?: number
  connect_ms?: number
  tls_ms?: number
  ttfb_ms?: number
  /** Negotiated wire version of a native HTTP action (1.1, 2 or 3). */
  http_version?: string
  /** How an attempted QUIC path ended on TCP. */
  http_version_fallback?: string
  /** HTTP attempts in the logical action (recorded by HTTP/3 policies). */
  attempt_count?: number
  bytes_sent?: number
  bytes_received?: number
  status_code?: number
  success: boolean
  /** A script-declared transaction rather than a request; graphs prefer these when a run has any. */
  business_transaction?: boolean
  error_message?: string
  extractions?: Record<string, string>
  page_url?: string
  page_title?: string
  console_entries?: BrowserReplayConsoleEntry[]
  network_entries?: BrowserReplayNetworkEntry[]
  dom_summary?: BrowserReplayDomSummary
  client_metrics?: ClientMetrics
  server_telemetry?: ServerTelemetry
  protocol_evidence?: ProtocolEvidence
  snapshot?: {
    request_method: string
    request_url: string
    request_headers?: Record<string, string>
    request_body?: string
    response_status: number
    response_headers?: Record<string, string>
    resources?: import('./types/scripts').ScriptResourceSnapshot[]
    omitted_resources?: number
    response_body?: string
  }
}

export interface IntervalSummaryRecord {
  browser_documents?: BrowserDocumentSummary | null
  timestamp: string
  test_id: string
  test_name: string
  active_vusers: number
  tps: number
  avg_response_ms: number
  error_pct: number
  total_transactions: number
  total_errors: number
  p50_ms: number
  p90_ms: number
  p95_ms: number
  p99_ms: number
  groups?: Record<string, GroupLiveMetrics>
  transactions?: Record<string, IntervalTransactionMetrics>
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  latency?: NetworkLatency
	load_levels?: ActiveLoadLevel[]
}

export interface IntervalTransactionMetrics {
  name: string
  tps: number
  count: number
  errors: number
  avg_ms: number
}

export interface GRPCDiagnostics {
  encode_ms?: number
  decode_ms?: number
  method: string
  mode: string
  call_ms: number
  first_message_ms?: number
  deadline_budget_ms?: number
  status_code?: number
  messages_sent: number
  messages_received: number
  request_bytes: number
  response_bytes: number
  receive_limit_reached?: boolean
}

export interface GRPCSummary {
  encode_ms?: DiagnosticDistribution
  decode_ms?: DiagnosticDistribution
  method?: string
  mode?: string
  mixed_methods?: boolean
  deadline_observed?: number
  near_deadline?: number
  samples: number
  call_ms: DiagnosticDistribution
  first_message_ms: DiagnosticDistribution
  deadline_budget_ms: DiagnosticDistribution
  status_counts: number[]
  unavailable_status: number
  receive_limit_reached: number
  messages_sent: number
  messages_received: number
  request_bytes: number
  response_bytes: number
}

export interface ServerTelemetry {
  subscription_id?: string
  acknowledgement_ms?: number
  websocket?: WebSocketDiagnostics
  grpc?: GRPCDiagnostics
  server_processing_ms?: number
  transfer_ms?: number
  serialization_ms?: number
  deserialization_ms?: number
  request_payload_kb?: number
  request_size_class?: string
  response_payload_kb?: number
  response_size_class?: string
  envelope_overhead_kb?: number
  soap_fault_code?: string
  soap_fault_category?: string
  soap_namespace_conflict?: string
  redirect_count?: number
  redirect_ms?: number
  message_latency_ms?: number
  messages_sent?: number
  messages_received?: number
  messages_sent_per_sec?: number
  messages_received_per_sec?: number
  consumer_lag_ms?: number
  first_event_ms?: number
  event_count?: number
  connection_reused?: boolean
}

export interface ServerTelemetrySummary {
  java_http?: import('./types/javaHttp').JavaHTTPSummary
  messaging?: MessagingSummary
  consumer_lag_samples?: number
  consumer_offset_lag_samples?: number
  websocket?: WebSocketSummary
  diagnostic_findings?: ProtocolDiagnosticFinding[]
  grpc?: GRPCSummary
  first_event_samples?: number
  samples: number
  avg_server_processing_ms?: number
  avg_transfer_ms?: number
  avg_request_payload_kb?: number
  avg_response_payload_kb?: number
  request_payload_histogram?: Record<string, number>
  response_payload_histogram?: Record<string, number>
  avg_message_latency_ms?: number
  max_message_latency_ms?: number
  messages_sent?: number
  messages_received?: number
  avg_messages_sent_per_sec?: number
  avg_messages_received_per_sec?: number
  avg_consumer_lag_ms?: number
  max_consumer_lag_ms?: number
  avg_consumer_offset_lag?: number
  max_consumer_offset_lag?: number
  avg_first_event_ms?: number
  max_first_event_ms?: number
  event_count?: number
  // Messaging reliability counters (P1.1) + derived rates (P4.1).
  send_failure_count?: number
  redelivery_count?: number
  dead_lettered_count?: number
  duplicate_count?: number
  out_of_order_count?: number
  commit_count?: number
  abort_count?: number
  redelivery_rate_pct?: number
  dead_letter_rate_pct?: number
  duplicate_rate_pct?: number
  // Event Hubs / streaming ingestion (EH.4).
  total_throttled_requests?: number
  total_server_busy_count?: number
  total_quota_exceeded_count?: number
  total_event_too_large_count?: number
  avg_end_to_end_latency_ms?: number
  max_end_to_end_latency_ms?: number
  partition_skew_pct?: number
  partition_events?: Record<string, number>
  request_size_classes?: Record<string, number>
  response_size_classes?: Record<string, number>
  connection_reused_count?: number
  connection_reused_pct?: number
}

export interface ArchivePage<T> {
  count: number
  total: number
  limit: number
  offset: number
  sampled?: boolean
  filters?: Record<string, unknown>
  transactions?: T[]
  resources?: T[]
  summaries?: T[]
}

export interface RunArchiveGroupBreakdown {
  totals: RunArchiveActionStats & { average_rate_per_second?: number }
  items: Record<string, RunArchiveActionStats & { average_rate_per_second?: number }>
}

export interface RunArchiveActionStats {
  java_http?: import('./types/javaHttp').JavaHTTPSummary
  messaging?: MessagingSummary
  transactions: number
  errors: number
  error_pct: number
  avg_response_ms: number
  min_response_ms: number
  max_response_ms: number
  peak_tps?: number
  p50_ms?: number
  p90_ms?: number
  p95_ms?: number
  p99_ms?: number
  /** 'approximate' when the percentiles came from the duration sketch. */
  percentile_quality?: string
}

export interface RunArchiveResourcePeaks {
  cpu_process_pct: number
  mem_process_mb: number
  net_sent_kbps: number
  net_recv_kbps: number
  disk_read_kbps: number
  disk_write_kbps: number
  cpu_iowait_pct?: number
  cpu_steal_pct?: number
  gc_pause_ms?: number
  container_cpu_usage_pct?: number
  container_cpu_throttled_seconds_rate?: number
  container_memory_usage_pct?: number
  container_oom_kill_count_delta?: number
  net_drop_in_per_sec?: number
  net_drop_out_per_sec?: number
  net_error_in_per_sec?: number
  net_error_out_per_sec?: number
  tcp_retransmits_per_sec?: number
  disk_io_time_pct?: number
}

export interface ResourceSample {
  timestamp: string
  test_id?: string
  test_name?: string
  active_vusers?: number
  cpu_process_pct: number
  cpu_free_pct: number
  mem_process_mb: number
  mem_free_mb: number
  net_sent_kbps: number
  net_recv_kbps: number
  disk_read_kbps: number
  disk_write_kbps: number
  cpu_user_pct?: number
  cpu_system_pct?: number
  cpu_iowait_pct?: number
  cpu_steal_pct?: number
  load1?: number
  load5?: number
  load15?: number
  thread_count?: number
  open_fd_count?: number
  goroutine_count?: number
  heap_alloc_mb?: number
  heap_in_use_mb?: number
  gc_count_delta?: number
  gc_pause_ms?: number
  container_cpu_limit_cores?: number
  container_cpu_usage_pct?: number
  container_cpu_throttled_periods_delta?: number
  container_cpu_throttled_seconds_rate?: number
  container_memory_working_set_mb?: number
  container_memory_limit_mb?: number
  container_memory_usage_pct?: number
  container_memory_fail_count_delta?: number
  container_oom_kill_count_delta?: number
  net_drop_in_per_sec?: number
  net_drop_out_per_sec?: number
  net_error_in_per_sec?: number
  net_error_out_per_sec?: number
  tcp_retransmits_per_sec?: number
  tcp_established?: number
  tcp_time_wait?: number
  ephemeral_port_utilization_pct?: number
  disk_io_time_pct?: number
  disk_weighted_io_time_pct?: number
  disk_in_progress?: number
  external_metrics?: Record<string, number>
}

export interface RunArchiveOverview {
  transactions: number
  errors: number
  error_pct: number
  avg_response_ms: number
  min_response_ms: number
  max_response_ms: number
  p50_ms?: number
  p90_ms?: number
  p95_ms?: number
  p99_ms?: number
  /** 'approximate' when the percentiles came from the duration sketch. */
  percentile_quality?: string
  resource_samples: number
  interval_summaries: number
  peak_tps: number
  peak_target_tps?: number
  max_throughput_gap_pct?: number
  throughput_missed_intervals?: number
  peak_active_vusers: number
  browser?: BrowserLiveMetrics
  telemetry?: RunArchiveTelemetrySummary
  load_model_goals?: RunArchiveLoadModelGoals
  goal_completion?: RunArchiveGoalCompletion
  /** A goal or traffic-profile run's TPS goal against what it delivered, over 10 s windows. */
  goal_attainment?: {
    target_average_tps: number
    achieved_average_tps: number
    window_seconds: number
    tolerance_pct: number
    windows: number
    within_windows: number
    over_windows: number
    under_windows: number
  }
}

export interface RunArchiveLoadModelGoals {
  configured_global_iterations?: number
  configured_max_transactions?: number
  target_tps?: number
  staged_groups?: number
  stage_count?: number
  once_only_groups?: number
}

export interface RunArchiveGoalCompletion {
  achieved_transactions: number
  configured_max_transactions?: number
  transaction_completion_pct?: number
  configured_global_iterations?: number
  iteration_evidence?: string
  throughput_target_tps?: number
  throughput_missed_intervals?: number
}

export interface RunArchiveTelemetrySummary {
  status: 'healthy' | 'degraded' | 'breached' | 'unscored'
  samples: number
  healthy_intervals?: number
  degraded_intervals?: number
  breached_intervals?: number
  // Intervals whose cross-host budget decision could not be scored.
  unscored_intervals?: number
  sustained_breach_intervals?: number
  dropped_results?: number
  websocket_dropped_messages?: number
  websocket_dropped_events?: number
  max_sample_age_ms?: number
  max_generator_processing_lag_ms?: number
  max_transport_lag_ms?: number
  max_controller_fanout_lag_ms?: number
  max_browser_render_lag_ms?: number
  first_breach_at?: string
  last_breach_at?: string
  condition_counts?: Record<string, number>
}

export interface RunArchiveSOAPFaultTimelineBucket {
  timestamp: string
  total: number
  by_code?: Record<string, number>
  by_category?: Record<string, number>
}

export interface RunArchiveSOAPPayloadTimelineBucket {
  timestamp: string
  samples: number
  avg_request_payload_kb?: number
  avg_response_payload_kb?: number
  avg_envelope_overhead_kb?: number
  max_envelope_overhead_kb?: number
}

export interface RunArchiveSummaryPoint {
  timestamp: string
  active_vusers: number
  tps: number
  target_tps?: number
  throughput_gap_pct?: number
  throughput_target_missed?: boolean
  error_pct: number
  p95_ms: number
  total_transactions: number
  total_errors: number
}

export interface RunArchiveVerdict {
  status: string
  conditional: boolean
  failed: boolean
  explanation: string
}

export interface RunArchiveSLARow {
  name: string
  volume: number
  p95_ms: number
  min_ms?: number
  p50_ms?: number
  p90_ms?: number
  p99_ms?: number
  max_ms?: number
  sla_ms?: number
  has_sla: boolean
  error_pct: number
  result: string
  result_kind: 'pass' | 'warn' | 'fail' | 'na' | string
  /** 'approximate' when the percentiles came from the duration sketch. */
  percentile_quality?: string
}

export interface RunArchiveSLARollup {
  evaluated: number
  passed: number
  warned: number
  failed: number
  na: number
  worst_kind: 'pass' | 'warn' | 'fail' | 'na' | string
}

export interface IntegrationOutcome {
  scope?: { project_id?: string; application_id?: string; release_id?: string }
  schema_version: 1
  execution_id: string
  observed_at: string
  assessment: { status: 'passed' | 'failed' | 'inconclusive' | 'not_assessed' | 'unavailable'; reason_code: string; explanation: string; criteria_revision: string | null }
  execution: { termination_reason: string; planned_duration_seconds: number | null; observed_duration_seconds: number | null }
  evidence: { status: 'complete' | 'partial' | 'unavailable'; coverage_percent: number | null }
}

export interface RunArchiveReport {
  telemetry_policy?: import('./types/telemetryPolicy').TelemetryPolicyEvidence
  // The finalized telemetry verdict under the run's pinned acceptance policy.
  telemetry_acceptance?: TelemetryAcceptanceVerdict
  browser_documents?: BrowserDocumentSummary | null
  browser_document_evidence?: BrowserDocumentObservation[]
  browser_document_evidence_truncated?: boolean
  /** Beside a document summary: soft (same-document) navigations are not measured. */
  browser_soft_navigation?: 'unsupported'
  messaging_progress?: MessagingProgressSnapshot
  monitor_diagnostics?: import('./types/runMonitoring').RunMonitorDiagnostic[]
  monitor_rule_results?: import('./types/runMonitoring').MonitorRuleResult[]
  outcome?: IntegrationOutcome
  by_unknown?: Record<string, RunArchiveActionStats>
  by_request?: Record<string, RunArchiveActionStats>
  by_transaction?: Record<string, RunArchiveActionStats>
  metric_populations?: Record<string, RunArchiveActionStats & { average_rate_per_second?: number }>
  /** Per metric population, each group's totals and the transactions (or requests) inside it. */
  group_breakdown?: Record<string, Record<string, RunArchiveGroupBreakdown>>
  /** The populations that had more group and name pairs than the report keeps. */
  group_breakdown_truncated?: string[]
  /** Display names for the provenance ids, where the controller could resolve them. */
  identity_names?: { project?: string; application?: string; release?: string; scenario?: string }

  schema_version?: string
  compatibility?: RunArchiveReportCompatibility
  completeness?: RunArchiveCompletenessScore
  change_attribution?: RunArchiveChangeAttribution
  report_profiles?: RunArchiveReportProfile[]
  network_phases?: RunArchiveNetworkPhaseSummary
  /** Each execution process's network limits and UDP drops during the run. */
  host_network?: RunArchiveHostNetworkEvidence[]
  /** QUIC handshake checks of the HTTP/3 origins before the run started. */
  http3_preflight?: RunArchiveHTTP3Preflight
  artifact_inventory?: RunArchiveArtifact[]
  integrity?: RunArchiveIntegrityReport
  manifest: RunArchiveManifest
  overview: RunArchiveOverview
  verdict?: RunArchiveVerdict
  /** F-01… findings derived from the archive: what the exports list when no analyst findings were saved. */
  generated_findings?: ReportFinding[]
  sla_rows?: Record<string, RunArchiveSLARow>
	transaction_response_time_sla_results?: TransactionResponseTimeSLAResult[]
  sla_rollup?: RunArchiveSLARollup
  evidence_tiers?: RunArchiveEvidenceTier[]
  elk_samples?: import('./types/monitoring').ELKLogSample[]
  dynatrace_evidence?: import('./types/monitoring').DynatraceEvidenceRecord[]
  error_patterns?: RunArchiveErrorPattern[]
  summary_timeline?: RunArchiveSummaryPoint[]
	load_level_summaries?: RunArchiveLoadLevelSummary[]
  by_action: Record<string, RunArchiveActionStats>
  by_group?: Record<string, RunArchiveActionStats>
  by_soap_operation?: Record<string, RunArchiveActionStats>
  by_property?: Record<string, RunArchiveActionStats>
  by_network_profile?: Record<string, RunArchiveActionStats>
  by_region?: Record<string, RunArchiveActionStats>
  by_soap_fault_code?: Record<string, number>
  by_soap_fault_category?: Record<string, number>
  soap_fault_timeline?: RunArchiveSOAPFaultTimelineBucket[]
  soap_payload_timeline?: RunArchiveSOAPPayloadTimelineBucket[]
  browser_by_engine?: Record<string, BrowserEngineLiveMetrics>
  browser_by_platform?: Record<string, BrowserPlatformLiveMetrics>
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  browser_resources?: BrowserResourceTiming[]
  // The Salesforce Aura actions each transaction's steps sent, most time first.
  aura_actions?: AuraActionRollup[]
  // Browser sessions whose browser major version differs from the driver's
  // tested build or from the recording's.
  browser_version_skews?: BrowserVersionSkew[]
  // Each transaction's times in cold and warm browser iterations.
  browser_iterations?: BrowserIterationTransaction[]
  // Each step's Salesforce EPT readings beside its own times (calibration).
  salesforce_ept?: SalesforceEPTCalibration[]
  // Per transaction, the browsers' Aura calls beside the HTTP scripts'.
  aura_parity?: AuraParityTransaction[]
  resource_peaks: RunArchiveResourcePeaks
  release_trend?: RunArchiveReleaseTrendSummary
  // root_cause_view is the generated root cause every report format renders:
  // v2 where the project presents it and an analysis completed, else v1.
  root_cause_view?: import('./types/rca').ReportRootCause
  // root_cause_summary is the run's root cause in brief, on every report
  // response including batches.
  root_cause_summary?: import('./types/rca').ReportRootCauseSummary
  // validity says whether the run's load generators and telemetry could
  // carry the load it reports (validity.v1).
  validity?: import('./types/rca').RcaValidity
}

export interface RunArchiveLoadLevelSummary {
	group: string
	stage_index: number
	name: string
	target_vusers: number
	configured_duration: string
	observed_duration_seconds: number
	requests: number
	errors: number
	error_rate_pct: number
	throughput: number
	p50_ms: number
	p90_ms: number
	p95_ms: number
	p99_ms: number
	partial?: boolean
	/** 'approximate' (duration sketch) or 'approximate_tdigest' (an archive older than the sketch). */
	percentile_quality?: string
}

export interface RunArchiveNetworkPhaseSummary {
  samples?: number
  overall?: RunArchiveNetworkPhases
  by_action?: Record<string, RunArchiveNetworkPhases>
  by_dependency?: Record<string, RunArchiveNetworkPhases>
  /** Phase averages per negotiated wire version (1.1, 2, 3). */
  by_http_version?: Record<string, RunArchiveNetworkPhases>
  version_mix?: RunArchiveHTTPVersionMix
  version_mix_by_action?: Record<string, HTTPVersionMix>
}

/** HTTP actions counted by negotiated version and routing. by_version keys are
 * 1.1, 2, 3, unknown (no response) and cache. */
export interface HTTPVersionMix {
  actions: number
  by_version?: Record<string, number>
  by_selection?: Record<string, number>
  by_fallback?: Record<string, number>
  by_quic_error?: Record<string, number>
  http3_policies?: number
  upgrade_eligible?: number
  fell_back?: number
  quic_attempted?: number
  quic_failed?: number
  attempts?: number
}

export interface UDPDropCounters {
  /** pod: the process's own network namespace; network_namespace: possibly shared. */
  scope: string
  scope_id?: string
  receive_buffer_errors: number
  send_buffer_errors: number
  /** Every input error; Linux counts each receive-buffer drop here too. */
  in_errors: number
  sampled_at?: string
}

export interface RunArchiveHostNetworkEvidence {
  source?: string
  open_files?: { available: boolean; soft?: number; hard?: number }
  udp_buffers?: { available: boolean; receive_max_bytes?: number; send_max_bytes?: number }
  udp_drops_at_start?: UDPDropCounters
  udp_drops_at_end?: UDPDropCounters
  udp_drops_delta?: { receive_buffer_errors: number; send_buffer_errors: number; in_errors: number }
  /** Set only when drops grew in the process's own pod scope. */
  warning?: string
}

export interface HTTP3PreflightResult {
  group: string
  origin: string
  /** 3 or auto+h3. */
  policy: string
  ok: boolean
  error_class?: string
  error?: string
  address?: string
  handshake_ms?: number
  /** Why the origin was not checked. */
  skipped?: string
  checked_at?: string
  /** Generator target, or "controller" for a local run. */
  source: string
  region?: string
}

export interface RunArchiveHTTP3Preflight {
  /** report or block (APEXLOAD_HTTP3_PREFLIGHT). */
  policy: string
  results?: HTTP3PreflightResult[]
  omitted?: number
  unchecked?: { source: string; region?: string; reason: string }[]
  blocked?: boolean
}

export interface HTTPVersionRatio {
  numerator: number
  denominator: number
  value: number
}

export interface RunArchiveHTTPVersionMix extends HTTPVersionMix {
  /** Fell back ÷ upgrade-eligible auto+h3 actions. */
  fallback_ratio: HTTPVersionRatio
  /** Failed ÷ attempted QUIC actions. */
  quic_failure_ratio: HTTPVersionRatio
  /** Denominator a ratio needs before the finding judges it (runs that used HTTP/3). */
  min_samples?: number
  finding?: RunArchiveProtocolMixFinding
}

export interface RunArchiveProtocolMixFinding {
  breaches: Array<{ ratio: 'fallback' | 'quic_failure'; observed: HTTPVersionRatio; expected: number; min_samples: number }>
  top_reasons?: Array<{ reason: string; count: number }>
  summary: string
}

export interface RunArchiveNetworkPhases {
  count?: number
  avg_dns_ms?: number
  avg_connect_ms?: number
  avg_tls_ms?: number
  avg_ttfb_ms?: number
  max_dns_ms?: number
  max_connect_ms?: number
  max_tls_ms?: number
  max_ttfb_ms?: number
}

export interface ReportCommentarySection {
  id: string
  label: string
}

export interface ReportFinding {
  id: string
  title: string
  severity: 'major' | 'minor' | 'info'
  observed?: string
  evidence?: string
  root_cause?: string
  impact?: string
}

export interface ReportAnnotations {
  test_id: string
  profile: string
  title: string
  prepared_by: string
  executive_narrative: string
  section_comments: Record<string, string>
  verdict_override?: string
  verdict_explanation?: string
  findings?: ReportFinding[]
  updated_at: string
  updated_by: string
  /** The verdict is stored once per run: which audience last set it, when and by whom. */
  verdict_profile?: string
  verdict_updated_at?: string
  verdict_updated_by?: string
}

export interface ReportAnnotationsResponse {
  annotations: ReportAnnotations
  sections: ReportCommentarySection[]
}

export interface ReportAnnotationsInput {
  expected_updated_at: string
  title: string
  prepared_by: string
  executive_narrative: string
  section_comments: Record<string, string>
  verdict_override: string
  verdict_explanation: string
  /** Verdict revision the editor loaded; a stale verdict change is refused with 409. */
  expected_verdict_updated_at?: string
  findings: ReportFinding[]
}

export interface RunArchiveAnalysisStats {
  run_id: string
  run_name?: string
  transactions: number
  errors: number
  error_pct: number
  avg_response_ms: number
  min_response_ms?: number
  max_response_ms?: number
  p95_ms?: number
  p99_ms?: number
  bytes_received?: number
}

export interface RunArchiveAnalysisNode {
  id: string
  kind: string
  label: string
  runs: RunArchiveAnalysisStats[]
}

export interface RunArchiveAnalysisEdge {
  id: string
  from: string
  to: string
  kind: string
  runs: RunArchiveAnalysisStats[]
}

export interface RunArchiveAnalysisGraph {
  nodes: RunArchiveAnalysisNode[]
  edges: RunArchiveAnalysisEdge[]
}

export interface RunArchiveTransactionDrilldown {
  index: number
  timestamp: string
  run_id: string
  group?: string
  region?: string
  action_name?: string
  dependency_type?: string
  dependency_target?: string
  duration_ms: number
  status_code?: number
  success: boolean
  error_message?: string
  bytes_received?: number
  trace_id?: string
  correlation_id?: string
  source_transaction?: string
  source_action?: string
  path?: string[]
}

export interface RunArchiveAnalysisResponse {
  schema_version: string
  test_id: string
  baseline_id?: string
  graph: RunArchiveAnalysisGraph
  transactions: RunArchiveTransactionDrilldown[]
  count: number
  total: number
  limit: number
  offset: number
  filters?: Record<string, unknown>
}

export interface RunArchiveReportProfile {
  id: string
  label: string
  description: string
  audience?: string
  best_for?: string
  export_formats: string[]
  included_sections: string[]
  section_order?: string[]
  // root_cause_hypothesis_limit caps the generated hypotheses the rca
  // section shows; absent shows them all.
  root_cause_hypothesis_limit?: number
}

export interface ReportProfilesResponse {
  generated_at: string
  default_profile: string
  profiles: RunArchiveReportProfile[]
  download_formats: string[]
}

export interface RootCauseSignal {
  name: string
  status: string
  value?: string
  severity?: string
  score?: number
}

export interface RootCauseFinding {
  category: string
  severity: 'critical' | 'warning' | 'healthy' | string
  title: string
  detail: string
  evidence?: string[]
  score: number
}

export interface RunRootCauseSummary {
  schema_version: string
  test_id: string
  generated_at: string
  status: 'healthy' | 'degraded' | 'breached' | 'unknown' | string
  confidence: 'low' | 'medium' | 'high' | string
  summary: string
  signals?: RootCauseSignal[]
  top_findings?: RootCauseFinding[]
  next_actions?: string[]
  evidence?: string[]
}

export interface RunArchiveChangeAttribution {
  script_revisions?: string[]
  scenario_revision?: string
  environment?: string
  generator_versions?: string[]
  data_revision?: string
  runtime_overrides: boolean
  runtime_override_fields?: string[]
}

export interface RunArchiveCompletenessScore {
  score_pct: number
  status: 'complete' | 'needs_review' | 'incomplete' | string
  findings?: RunArchiveCompletenessFinding[]
}

export interface RunArchiveCompletenessFinding {
  dimension: string
  severity: 'info' | 'warning' | 'critical' | string
  penalty: number
  message: string
}

export interface RunArchiveReportCompatibility {
  status: 'compatible' | 'incompatible' | string
  report_schema_version: string
  manifest_schema_version?: string
  supported_manifest_schemas: string[]
  outputs: RunArchiveOutputCompatibility[]
  issues?: RunArchiveCompatibilityIssue[]
}

export interface RunArchiveOutputCompatibility {
  format: string
  artifact?: string
  schema_version?: string
  status: 'compatible' | 'missing' | 'incompatible' | 'unsupported' | string
  reason?: string
}

export interface RunArchiveCompatibilityIssue {
  severity: 'info' | 'warning' | 'error' | string
  code: string
  message: string
}

export interface RunArchiveEvidenceTier {
  tier: string
  label: string
  status: 'present' | 'missing' | string
  count?: number
  description?: string
  reason?: string
}

// One Aura action in one transaction over the run: its calls, the time of
// the requests that carried it (a batch's time counts for each action in it),
// and its errors.
export interface AuraActionRollup {
  transaction: string
  name: string
  apex?: boolean
  count: number
  total_ms: number
  errors?: number
}

// A transaction's passed times in browser iterations of one kind, and how
// many failed.
export interface BrowserIterationTimes {
  count: number
  errors?: number
  avg_ms: number
  p90_ms: number
}

// One transaction's Aura calls in browsers and in the HTTP scripts, as median
// calls per transaction instance (one VUser's pass through it in one
// iteration), with each side's batching and response bytes per request.
export interface AuraParityTransaction {
  transaction: string
  browser_instances: number
  http_instances: number
  browser_requests: number
  http_requests: number
  browser_actions_per_request: number
  http_actions_per_request: number
  browser_bytes_per_request: number
  http_bytes_per_request: number
  actions: AuraParityAction[]
}

// One Aura action's median calls per instance on each side; flagged when they
// differ by more than one call.
export interface AuraParityAction {
  name: string
  browser: number
  http: number
  flagged?: boolean
}

// One step's Salesforce EPT readings (eptVisible=1, read when the counter
// changed) and the same steps' own times; the difference is step minus EPT.
export interface SalesforceEPTCalibration {
  step: string
  samples: number
  avg_ept_ms: number
  avg_step_ms: number
  avg_difference_ms: number
}

// A transaction's times in cold (a browser context's first) and warm browser
// iterations.
export interface BrowserIterationTransaction {
  transaction: string
  cold?: BrowserIterationTimes
  warm?: BrowserIterationTimes
}

// One browser version mismatch the run's sessions reported: the launched
// browser's version and the one expected by the Playwright driver ('driver')
// or the recording ('recording').
export interface BrowserVersionSkew {
  source: 'driver' | 'recording' | string
  actual: string
  expected: string
  sessions: number
}

export interface BrowserResourceTiming {
  failure_kind?: string
  timestamp: string
  action_name?: string
  page_url?: string
  url?: string
  method?: string
  status_code?: number
  resource_type?: string
  duration_ms?: number
  bytes_received?: number
  device_profile?: string
  device_platform?: string
  browser_engine?: string
}

export interface BrowserEngineLiveMetrics extends BrowserLiveMetrics {
  browser_engine?: string
}

export interface BrowserPlatformLiveMetrics extends BrowserLiveMetrics {
  device_platform?: string
  browser_engine?: string
}

export interface ScenarioDraftGroup {
  name: string
  scriptId: string
  /** Immutable source-run group identity used only while creating a revised copy. */
  revisionSourceGroupIndex?: number
  /** Explicitly distinguishes a newly added/replaced group from a lost source identity. */
  revisionNewGroup?: boolean
  /** "load" (default) or "observer" — browser experience canary, pinned during goal scaling. */
  role?: 'load' | 'observer'
  vusers: number
  rampUp: string
	rampDown: string
	startOffset?: string
  stages?: LoadStageDraft[]
  iterations?: number
  /** Keeps a displayed canonical runtime iteration from being duplicated as a compact override. */
  iterationPayloadSource?: 'runtime' | 'top_level'
  initialization: 'at_runtime' | 'at_once'
  /** Per-group runtime overrides; merged over the scenario-wide runtime defaults. */
  runtimeSettings?: ScenarioRuntimeSettingsDraft
  assignment?: GeneratorAssignmentSettings
	/** Optional group override; omitted values inherit the scenario default. */
	transactionResponseTimeSlaMs?: number
}

export interface ScenarioDraftRevisionContext {
  /** Initial source presence distinguishes an authored empty replacement from inheritance. */
  sourceGoalConfigPresent?: boolean
  sourceRuntimeSettingsPresent?: boolean
  sourceStartDelayPresent?: boolean
  elkConnectionId?: string
  dynatraceConnectionId?: string
  monitorBindings?: import('./types/runMonitoring').MonitorBinding[]
  sourceTestId: string
  applicationId?: string
  releaseId?: string
  scenarioId?: string
  projectId?: string
  folderId?: string | null
  dataPools: ScenarioDataPoolBinding[]
}

export interface ScenarioDraft {
  terminationMode?: 'duration' | 'iterations'
  name: string
  duration: string
  startDelay: string
  scenarioMode: ScenarioMode
  scriptShakeout: boolean
  resourceBaseline: boolean
  assignment: GeneratorAssignmentSettings
  goalConfig: GoalConfig
  groups: ScenarioDraftGroup[]
  runtimeSettings: ScenarioRuntimeSettingsDraft
  /** Authored production traffic profile (TestHub's production-replay shape). */
  trafficProfile?: TrafficProfile
  /** Authored data-pool bindings of a saved scenario (a revision keeps its source's in revisionContext). */
  dataPools?: ScenarioDataPoolBinding[]
  /** Immutable source-run context carried only when a queued run is revised. */
  revisionContext?: ScenarioDraftRevisionContext
}

export type TestStatus =
  | 'QUEUED'
  | 'RESERVED'
  | 'SCHEDULED'
  | 'PENDING'
  | 'SHAKEOUT'
  | 'SHAKEOUT_REVIEW'
  | 'INITIALIZING'
  | 'RUNNING'
  | 'STOPPING'
  | 'COMPLETE'
  | 'FAILED'
  | 'ABORTED'
  | 'PAUSED'

export interface LiveMetrics {
  java_http?: import('./types/javaHttp').JavaHTTPActions
  /** Run-to-date HTTP version mix (cumulative, unlike the window fields). */
  http_versions?: RunArchiveHTTPVersionMix
  telemetry_policy?: import('./types/telemetryPolicy').TelemetryPolicyEvidence
  browser_documents?: BrowserDocumentSummary | null
  messaging_progress?: MessagingProgressSnapshot
  messaging?: Record<string, MessagingSummary>
  observation_state?: 'historical' | 'stale'
  observation_valid_until?: string
  completed_iterations?: number
  timestamp: string
  active_vusers: number
  tps: number
  target_tps?: number
  throughput_gap_pct?: number
  throughput_target_missed?: boolean
  /** Goal run: TPS over the goal window (10 s), the figure its gap is judged on. */
  goal_window_tps?: number
  /** Goal run: the targets in force over that window, averaged — what the gap is measured against. */
  goal_window_target_tps?: number
  /** Goal run: (target − goal_window_tps) / target × 100 — positive under target, negative over. */
  goal_gap_pct?: number
  avg_response_ms: number
  /** Error % of the last sample window (sample_window_seconds, normally 1 s). */
  error_pct: number
  /** Successful transactions in the last sample window. */
  total_transactions: number
  /** Failed transactions in the last sample window. */
  total_errors: number
  /** Successful transactions since the run started (absent on older runs). */
  run_total_transactions?: number
  /** Failed transactions since the run started (absent on older runs). */
  run_total_errors?: number
  /** Failed ÷ all attempts since the run started (absent on older runs). */
  run_error_pct?: number
  /**
   * Requests attempted in the last sample window, failed ones included: every
   * result that is not a business transaction (the report's request
   * population). Omitted when zero, absent from older controllers, and absent
   * (with the run_* request counters) for a run whose request counts are
   * incomplete, which is then read in transactions.
   */
  requests?: number
  /** Failed requests in the last sample window. */
  request_errors?: number
  /** Requests per second in the last sample window. */
  request_rate?: number
  /** Requests attempted since the run started (absent on older runs, or when the run's request counts are incomplete). */
  run_total_requests?: number
  /** Failed requests since the run started (absent with run_total_requests). */
  run_request_errors?: number
  /** Failed ÷ attempted requests since the run started (absent with run_total_requests). */
  run_request_error_pct?: number
  p50_ms: number
  p90_ms: number
  p95_ms: number
  p99_ms: number
  /** Set when a distributed window fell back from the merged duration sketch: 'approximate_tdigest' or 'averaged'. */
  percentile_quality?: string
  avg_dns_ms?: number
  avg_connect_ms?: number
  avg_tls_ms?: number
  avg_ttfb_ms?: number
  avg_wait_ms?: number
  error_breakdown?: Record<string, number>
  transactions?: Record<string, TxMetrics>
	transaction_response_time_sla?: TransactionResponseTimeSLAResult[]
  dependency_health?: Record<string, TxMetrics>
  dependency_map?: LiveDependencyMap
  error_patterns?: LiveErrorPattern[]
  soap_operations?: Record<string, TxMetrics>
  properties?: Record<string, TxMetrics>
  network_profiles?: Record<string, TxMetrics>
  by_region?: Record<string, TxMetrics>
  groups?: Record<string, GroupLiveMetrics>
  browser?: BrowserLiveMetrics
  browser_by_device?: Record<string, BrowserDeviceLiveMetrics>
  capacity_status?: LiveCapacityStatus
  goal_status?: GoalStatus
  telemetry_status?: TelemetryRunHealth
  telemetry?: TelemetryHealth
  resources?: SystemResources
  generator_resources?: Record<string, GeneratorResourceSnapshot>
  latency?: NetworkLatency
	load_levels?: ActiveLoadLevel[]
	elk?: import('./types/monitoring').ELKCaptureStatus
	dynatrace?: import('./types/monitoring').DynatraceCaptureStatus
}

export interface GeneratorResourceSnapshot {
  pod_capacity?: import('./types/capacity').PodCapacitySnapshot
  generator_id: string
  target: string
  region?: string
  active_vusers: number
  tps?: number
  error_pct?: number
  p95_ms?: number
  p99_ms?: number
  saturation_state?: 'clear' | 'warning' | 'danger' | 'unknown' | string
  impact_reasons?: string[]
  resources?: SystemResources
  timestamp: string
}

export interface BrowserLiveMetrics {
  samples: number
  // Samples that loaded a document: FCP, LCP, TTFB and render-blocking
  // resources average over them (a single-page app loads once per journey).
  document_loads?: number
  avg_fcp_ms?: number
  avg_lcp_ms?: number
  avg_cls_score?: number
  avg_inp_ms?: number
  avg_ttfb_ms?: number
  avg_blocking_resources?: number
  avg_long_task_count?: number
  avg_js_heap_used_mb?: number
  avg_browser_process_cpu_pct?: number
  avg_browser_process_mem_mb?: number
}

export interface LiveCapacityStatus {
  allocated_vusers: number
  available_generators: number
  recommended_generators: number
  saturated: boolean
  queued_vusers: number
  rejected_vusers: number
  by_protocol?: Record<string, LiveCapacityBreakdown>
  by_region?: Record<string, LiveCapacityBreakdown>
}

export interface LiveCapacityBreakdown {
  allocated_vusers?: number
  queued_vusers?: number
  rejected_vusers?: number
}

export interface BrowserDeviceLiveMetrics extends BrowserLiveMetrics {
  device_profile?: string
  device_platform?: string
  device_tier?: string
  browser_engine?: string
}

export interface TelemetryHealth {
  dropped_results_available?: boolean
  dropped_results?: number
  shedded_arrivals?: number
  dropped_subscriber_batches?: number
  missing_metric_windows?: number
  history_points?: number
  websocket_clients?: number
  websocket_dropped_messages?: number
  websocket_dropped_events?: number
  generator_published_at?: string
  controller_received_at?: string
  collected_at?: string
  published_at?: string
  client_received_at?: string
  generator_processing_lag_ms?: number
  transport_lag_ms?: number
  controller_fanout_lag_ms?: number
  browser_render_lag_ms?: number
  delivery_lag_ms?: number
  end_to_end_age_ms?: number
  sample_age_ms?: number
  // Run-cumulative stage distributions (C5); the live snapshot only.
  stage_distributions?: TelemetryStageDistribution[]
  // Provisional telemetry verdict under the run's pinned acceptance policy
  // (C7); the finalized verdict is in the run report.
  acceptance?: TelemetryAcceptanceVerdict
}

// One telemetry acceptance criterion with the evidence it read (C7).
export interface TelemetryAcceptanceCriterion {
  criterion: string
  outcome: 'pass' | 'fail' | 'inconclusive' | string
  comparator: string
  threshold: number
  unit: 'ratio' | 'records' | 'ms' | string
  observed?: number
  // exact; lower_bound when more loss, observed time or lag may exist;
  // upper_bound when the whole run's value is at most observed; partial when
  // observed covers only part of the run's evidence and bounds nothing.
  bound?: string
  numerator?: number
  denominator?: number
  reason?: string
  // A criterion on an advisory stage (browser render): shown, never decisive.
  advisory?: boolean
}

export interface TelemetryAcceptancePolicyPin {
  kind: string
  id: string
  name: string
  revision: number
  sha256: string
  source?: string
}

// A run's telemetry verdict, separate from the application verdict. Only a
// fail_ci policy can change a CI outcome; unknown evidence is inconclusive.
export interface TelemetryAcceptanceVerdict {
  outcome: 'pass' | 'fail' | 'inconclusive' | string
  mode: 'advisory' | 'fail_ci' | 'unknown' | string
  provisional?: boolean
  policy?: TelemetryAcceptancePolicyPin
  criteria: TelemetryAcceptanceCriterion[]
  reasons?: string[]
  exclusions?: string[]
}

export type TelemetryStageName = 'generator_processing' | 'transport' | 'controller_fanout' | 'sample_age' | 'browser_render'

// One telemetry stage's sampled distribution so far. Percentiles exist only
// for a measured stage, and stage percentiles are never added together.
export interface TelemetryStageDistribution {
  stage: TelemetryStageName | string
  unit: string
  availability: 'measured' | 'unavailable' | string
  samples: number
  excluded?: Record<string, number>
  p50_ms?: number
  p95_ms?: number
  p99_ms?: number
  max_ms?: number
  // Advisory stages (browser render) never affect run health.
  advisory?: boolean
  // When this controller began sampling: a controller that adopted the run
  // after a failover or restart samples only from then.
  sampled_from?: string
  // Why an unavailable stage has no evidence (recorder_lost: the finalizing
  // controller never sampled the run).
  reason?: string
}

export interface TelemetryRunHealth {
  status: 'healthy' | 'degraded' | 'breached' | 'unscored'
  severity: 'healthy' | 'warning' | 'danger'
  summary?: string
  current_signature?: string
  conditions?: string[]
  // Budget decisions that could not be scored, as "condition:reason".
  unscored?: string[]
  breach_active?: boolean
  sustained_breach?: boolean
  breach_duration_sec?: number
  breach_started_at?: string
  last_observed_at: string
  last_transition_at?: string
}

export interface TelemetryDiagnostic {
  severity: 'healthy' | 'warning' | 'danger'
  summary: string
  conditions?: string[]
  samples?: {
    issue: 'slow' | 'error' | 'broker' | 'dead_letter'
    transaction: string
    dependency_type?: string
    dependency_target?: string
    correlation_ids?: string[]
    count?: number
    errors?: number
    error_pct?: number
    avg_ms?: number
    p99_ms?: number
    avg_message_latency_ms?: number
    avg_consumer_lag_ms?: number
    avg_messages_sent_per_sec?: number
    avg_messages_received_per_sec?: number
  }[]
  telemetry?: TelemetryHealth
}

export interface TelemetryDiagnosticEntry {
  test_id: string
  test_name?: string
  timestamp: string
  diagnostic: TelemetryDiagnostic
}

export interface NetworkLatency {
  targets: LatencyTarget[]
}

export interface LatencyTarget {
  name: string
  endpoint: string
  latency_ms: number
  probe_total_ms?: number
  dns_ms?: number
  connect_ms?: number
  tls_ms?: number
  ttfb_ms?: number
  http_status?: number
  probe_method?: string
  ip?: string
  hops?: number
  last_success: boolean
  jitter_ms?: number
  success_rate_pct?: number
  sample_count?: number
  route_status?: string
  error?: string
  timestamp: string
}

export interface SystemResources {
  cpu_process_pct: number
  cpu_process_pct_max?: number
  cpu_free_pct: number
  mem_process_mb: number
  mem_free_mb: number
  net_sent_kbps: number
  net_recv_kbps: number
  disk_read_kbps: number
  disk_write_kbps: number
  cpu_user_pct?: number
  cpu_system_pct?: number
  cpu_iowait_pct?: number
  cpu_steal_pct?: number
  load1?: number
  load5?: number
  load15?: number
  thread_count?: number
  open_fd_count?: number
  goroutine_count?: number
  heap_alloc_mb?: number
  heap_in_use_mb?: number
  gc_count_delta?: number
  gc_pause_ms?: number
  container_cpu_limit_cores?: number
  container_cpu_usage_pct?: number
  container_cpu_throttled_periods_delta?: number
  container_cpu_throttled_seconds_rate?: number
  container_memory_working_set_mb?: number
  container_memory_limit_mb?: number
  container_memory_usage_pct?: number
  container_memory_fail_count_delta?: number
  container_oom_kill_count_delta?: number
  net_drop_in_per_sec?: number
  net_drop_out_per_sec?: number
  net_error_in_per_sec?: number
  net_error_out_per_sec?: number
  tcp_retransmits_per_sec?: number
  tcp_established?: number
  tcp_time_wait?: number
  ephemeral_port_utilization_pct?: number
  disk_io_time_pct?: number
  disk_weighted_io_time_pct?: number
  disk_in_progress?: number
  external_metrics?: Record<string, number>
}

export interface TxMetrics {
  name: string
  tps?: number
  dependency_type?: string
  dependency_target?: string
  last_error_message?: string
  last_status_code?: number
  last_error_at?: string
  server_telemetry?: ServerTelemetrySummary
  avg_dns_ms?: number
  avg_connect_ms?: number
  avg_tls_ms?: number
  avg_ttfb_ms?: number
  correlation_ids?: string[]
  correlation_count?: number
  count: number
  errors: number
  avg_ms: number
  p50_ms: number
  p90_ms: number
  p95_ms?: number
  p99_ms: number
  /** Set when a distributed window fell back from the merged duration sketch: 'approximate_tdigest' or 'averaged'. */
  percentile_quality?: string
  /** The groups whose VUsers ran it in the window, sorted; absent when the source does not attribute transactions. */
  groups?: string[]
  /** Each group's share, when more than one group ran it. */
  by_group?: Record<string, TransactionGroupShare>
}

/** One group's part of a transaction more than one group ran in the window. */
export interface TransactionGroupShare {
  tps: number
  count: number
  errors: number
  avg_ms: number
  p50_ms: number
  p90_ms: number
  p95_ms?: number
  p99_ms: number
}

export interface LiveErrorPattern {
  signature: string
  sample: string
  count: number
  first_seen: string
  latest_seen: string
  actions?: string[]
  affected_groups?: string[]
  status_code?: number
  /** @deprecated No longer sent; it was one action's one-second error rate. */
  error_pct?: number
  /** This signature's share of all live errors so far. */
  share_of_errors_pct?: number
  snapshot_url?: string
  evidence_url?: string
  evidence_count?: number
  latest_evidence_id?: string
}

export interface LiveErrorEvidence {
  id: string
  signature: string
  action: string
  group?: string
  timestamp: string
  duration_ms: number
  status: string
  status_code?: number
  error_message?: string
  snapshot?: BrowserReplaySnapshot
}

export interface LiveDependencyMap {
  nodes: LiveDependencyNode[]
  edges: LiveDependencyEdge[]
}

export interface LiveDependencyNode {
  id: string
  label: string
  kind: 'source' | 'dependency'
  dependency_type?: string
  status?: 'healthy' | 'warning' | 'danger' | 'unknown'
  transactions?: number
  errors?: number
  error_pct?: number
  p95_ms?: number
}

export interface LiveDependencyEdge {
  source: string
  target: string
  dependency_type?: string
  transactions?: number
  errors?: number
  error_pct?: number
  p95_ms?: number
}

export interface GroupLiveMetrics {
  name: string
  vusers: number
  transactions: number
  errors: number
  run_transactions?: number
  run_errors?: number
  run_error_pct?: number
  /** The group's request counters, as on LiveMetrics. */
  requests?: number
  request_errors?: number
  request_rate?: number
  run_requests?: number
  run_request_errors?: number
  run_request_error_pct?: number
  tps: number
  target_tps?: number
  throughput_gap_pct?: number
  throughput_target_missed?: boolean
  /** Goal run: TPS over the goal window (10 s), the figure its gap is judged on. */
  goal_window_tps?: number
  /** Goal run: the targets in force over that window, averaged — what the gap is measured against. */
  goal_window_target_tps?: number
  /** Goal run: (target − goal_window_tps) / target × 100 — positive under target, negative over. */
  goal_gap_pct?: number
  /** The group's iterations completed so far in the run; absent when a generator does not count them. */
  completed_iterations?: number
  avg_response_ms: number
  p50_ms: number
  p90_ms: number
  p95_ms?: number
  p99_ms: number
}

export interface AuthoringTemplate {
  id: string
  name: string
  protocol: string
  use_case: string
  description: string
  tags?: string[]
  script: ScriptDefinition
  recommended_scenario?: {
    duration?: string
    vusers?: number
    ramp_up?: string
    iterations?: number
  }
}

export interface AuthoringComponent {
  id: string
  kind: string
  name: string
  protocols: string[]
  summary: string
  tags?: string[]
  config?: Record<string, unknown>
  data_sources?: ScriptDefinition['data_sources']
  parameters?: ScriptDefinition['parameters']
  init?: ScriptActionDefinition[]
  actions?: ScriptDefinition['actions']
  teardown?: ScriptActionDefinition[]
  usage_notes?: string[]
  variables?: Record<string, string>
  /** When a user saved the block (RFC 3339); built-in catalog entries have none. */
  saved_at?: string
}

export interface AuthoringSession {
  id: string
  project_id?: string
  source: string
  name: string
  status: string
  draft?: ScriptDefinition
  review_state?: Record<string, unknown>
  next_actions?: string[]
  blocking_health_inputs?: string[]
  approval_ready?: boolean
  created_at: string
  updated_at: string
  last_accessed_at: string
}

export interface AuthoringCorrelationPreviewRequest {
  name: string
  expected_updated_at: string
  source?: string
  type?: string
  expression: string
  sample_value?: string
  found_action_index?: number
  used_action_indexes?: number[]
  replace_sample_value?: boolean
  apply?: boolean
}

export interface AuthoringCorrelationDiff {
  kind: string
  path: string
  before?: string
  after?: string
}

export interface AuthoringCorrelationPreviewResponse {
  session_id: string
  applied: boolean
  extractor: {
    name: string
    source: string
    type: string
    expression: string
  }
  draft?: ScriptDefinition
  changes: AuthoringCorrelationDiff[]
  authoring_health: AuthoringHealth
}

export interface WSMessage {
  type: string
  archive_failure?: RunArchiveFailure
  test_id?: string
  project_id?: string
  script_id?: string
  replay_session_id?: string
  // browser_recording_* events: the live recording session this event is about,
  // plus the recorded step payload on browser_recording_step messages.
  recording_session_id?: string
  step?: unknown
  warning?: string
  url?: string
  test_name?: string
  status?: string
  timestamp?: string
  event_index?: number
  data?: LiveMetrics
  latency?: NetworkLatency
  event?: unknown
  report?: unknown
  session?: unknown
}

export interface MetricsHistoryPoint {
  time: string
  timestampMs?: number
  relativeMs?: number
  vusers: number
  tps: number
  /** Requests per second in the window (zero when it sent none). */
  requestRate?: number
  targetTps?: number
  avgMs: number
  errorPct: number
  p50: number
  p90: number
  p95?: number
  p99: number
  resourceSampleOnly?: number
  // System Resources
  cpuProcess?: number
  cpuFree?: number
  memProcess?: number
  memFree?: number
  netSent?: number
  netRecv?: number
  diskRead?: number
  diskWrite?: number
  cpuIOWait?: number
  cpuSteal?: number
  gcPauseMs?: number
  containerCPUUsagePct?: number
  containerCPUThrottledSecondsRate?: number
  containerMemoryUsagePct?: number
  containerOOMKillCountDelta?: number
  netDropsPerSec?: number
  netErrorsPerSec?: number
  tcpRetransmitsPerSec?: number
  diskIOTimePct?: number
  jmsConsumerLagMs?: number
  jmsPeakConsumerLagMs?: number
  cpuProcessMax?: number
  // Network-phase breakdown (real VU traffic), ms
  txDnsMs?: number
  txConnectMs?: number
  txTlsMs?: number
  txTtfbMs?: number
  txWaitMs?: number
  // Error taxonomy counts per interval
  errTimeout?: number
  errConnection?: number
  errHttp4xx?: number
  errHttp5xx?: number
  errAssertion?: number
  errResourceLimit?: number
  errSessionLost?: number
  errOther?: number
  // Marks an unobserved interval with a value-less spacer row. Runtime graphs
  // may connect its surrounding samples without filling the missing values.
  isGapBoundary?: number
  [key: string]: string | number | number[] | undefined
}

export interface APMIntegrationProfile {
  id: string
  name: string
  status: 'supported' | 'configured' | 'available' | string
  capabilities: string[]
  supported_signals: string[]
  correlation_fields: string[]
  required_settings?: string[]
  evidence?: string[]
  setup_actions?: string[]
}

export interface APMCorrelationContract {
  status: string
  propagation: string
  correlation_fields: string[]
  evidence?: string[]
}

export interface APMIntegrationProfilesResponse {
  generated_at: string
  configured_count: number
  profiles: APMIntegrationProfile[]
  generic_correlation: APMCorrelationContract
}

export interface InfrastructureMonitorPack {
  id: string
  name: string
  status: 'supported' | 'integration_contract' | string
  source: string
  resource_type: string
  signals: string[]
  evidence?: string[]
  setup_actions?: string[]
}

export interface InfrastructureMonitorPacksResponse {
  generated_at: string
  packs: InfrastructureMonitorPack[]
  evidence?: string[]
}

export interface ReleaseSLAWaiver {
  id: string
  release_id: string
  project_id?: string
  scope: string
  reason: string
  approved_by?: string
  status: 'active' | 'expired' | string
  expires_at?: string
  created_at: string
  updated_at: string
}

export interface ReleaseSLAGovernanceRun {
  test_id: string
  test_name?: string
  status: string
  completed_at?: string
  sla_breached: boolean
  sla_details?: string[]
  telemetry_status?: string
  completeness?: string
}

export interface ReleaseSLAGovernanceResponse {
  generated_at: string
  release_id: string
  release_name?: string
  application_id?: string
  project_id?: string
  owner: {
    type: string
    id?: string
    display_name: string
  }
  status: 'passed' | 'failed' | 'waived' | 'no_runs' | string
  summary: {
    total_runs: number
    archived_runs: number
    passed_runs: number
    failed_runs: number
    waived_breaches: number
    unwaived_breaches: number
  }
  breach_history?: ReleaseSLAGovernanceRun[]
  waivers?: ReleaseSLAWaiver[]
  audit_trail?: DashboardAuditEvent[]
  next_actions?: string[]
}

export interface ExternalResultImport {
  activity?: import('./types/objectActivity').ObjectActivitySummary
  id: string
  provider: 'k6' | 'jmeter' | 'gatling' | string
  name: string
  project_id?: string
  application_id?: string
  release_id?: string
  scenario_id?: string
  source?: string
  summary?: Record<string, unknown>
  content_hash?: string
  has_archive?: boolean
  created_at: string
}

export interface ExternalResultImportsResponse {
  imports: ExternalResultImport[]
  count: number
  limit: number
  offset: number
  has_more: boolean
}

export interface ArchivePruneCandidate {
  test_id: string
  project_id?: string
  release_id?: string
  run_status?: string
  archive_path: string
  policy_name?: string
  artifact_type: string
  compliance_class?: string
  reason?: string
  retention_days: number
  completed_at: string
  expired_at: string
}

export interface ArchiveRetentionResult {
  test_id: string
  archive_path: string
  status: string
  compliance_class?: string
  reason?: string
  error?: string
}

export interface ArchiveRetentionPlanResponse {
  generated_at: string
  dry_run: boolean
  candidates: ArchivePruneCandidate[]
  results?: ArchiveRetentionResult[]
  summary: {
    candidate_count: number
    pruned_count: number
    failed_count: number
  }
}

export interface LaunchWindow {
  open_ended?: boolean
  start_at: string
  end_at?: string
  duration_seconds: number
}

export interface LaunchConflict {
  code: string
  severity: 'blocking' | 'warning' | string
  source?: string
  resource_id?: string
  message: string
  starts_at?: string
  ends_at?: string
  required_generators?: number
  available_generators?: number
}

export interface LaunchPreflightResponse {
  pod_placements?: import('./types/capacity').PodCapacityPreview[]
  intent?: string
  decision: 'start_now' | 'schedule' | 'queue' | 'blocked' | string
  start_now_allowed: boolean
  schedule_allowed: boolean
  queue_allowed: boolean
  queue_reason?: string
  candidate_window: LaunchWindow
  capacity_advisory?: CapacityAdvisory
  blocking_conflicts?: LaunchConflict[]
  warnings?: LaunchConflict[]
  recommended_assignment?: AssignmentRecommendation
}

export interface LoadModelTemplateGroup {
  name: string
  vusers: number
  ramp_up?: string
  ramp_down?: string
  stages?: LoadStageDraft[]
  iterations?: number
  start_delay?: string
}

export interface LoadModelTemplate {
  id: string
  name: string
  shape_id: string
  description: string
  scenario: {
    scenario_mode?: ScenarioMode
    duration: string
    script_shakeout?: boolean
    resource_baseline?: boolean
    goal_config?: GoalConfig
    runtime_settings?: RuntimeSettingsOverride
    groups: LoadModelTemplateGroup[]
  }
  notes?: string[]
}

export interface LoadModelSupportEntry {
  id: string
  name: string
  category: string
  status: 'executed' | 'partial' | 'planned' | string
  apexload_shape: string
  fields: string[]
  notes: string
}

export interface LoadModelSupportMatrixResponse {
  support_matrix: LoadModelSupportEntry[]
  status_counts: Record<string, number>
  notes: string[]
}

export interface LoadModelTemplatesResponse {
  templates: LoadModelTemplate[]
  count: number
}

export interface GoalSizingGroupTarget {
  script_id?: string
  group?: string
  name?: string
  target_tps: number
  art_basis?: 'manual' | 'baseline'
  art_ms?: number
}

export interface GoalSizingRequest {
  baseline_test_id?: string
  name?: string
  duration?: string
  start_delay?: string
  groups: GoalSizingGroupTarget[]
  min_vusers?: number
  max_vusers?: number
}

export interface GoalSizingGroupResult {
  group: string
  script_id?: string
  baseline_art_ms: number
  target_tps: number
  computed_vusers: number
  basis: 'manual' | 'baseline' | string
}

export interface GoalSizingScenarioDraftGroup {
  name: string
  script_id: string
  vusers: number
  ramp_up?: string
}

export interface GoalSizingScenarioDraft {
  name: string
  duration: string
  start_delay?: string
  scenario_mode?: ScenarioMode
  goal_config?: GoalConfig
  groups: GoalSizingScenarioDraftGroup[]
}

export interface GoalSizingReport {
  baseline_test_id: string
  groups: GoalSizingGroupResult[]
  total_target_tps: number
  total_vusers: number
  scenario_draft: GoalSizingScenarioDraft
  load_model_validation?: LoadModelValidationReport
}

export interface DraftCapacityEstimate {
  pod_placements?: import('./types/capacity').PodCapacityPreview[]
  pod_capacity_warnings?: string[]
  scenario_mode?: ScenarioMode
  total_vusers: number
  duration: string
  resource_baseline?: boolean
  groups: TestGroup[]
  assignment?: GeneratorAssignmentSettings
  footprint?: ScenarioFootprint
  assignment_recommendation?: AssignmentRecommendation
  capacity_advisory?: CapacityAdvisory
}

export interface PrometheusPoint {
  timestamp: string
  timestamp_ms: number
  value: number
}

export interface PrometheusSeries {
  key: string
  label: string
  labels?: Record<string, string>
  values: PrometheusPoint[]
}

export interface PrometheusGraph {
  id: string
  title: string
  unit?: string
  series: PrometheusSeries[]
}

export interface PrometheusGraphsResponse {
  enabled: boolean
  configured: boolean
  generated_at: string
  start?: string
  end?: string
  step_seconds?: number
  graphs: PrometheusGraph[]
}

// Imported Grafana dashboards.
export type ImportedDashboardPanelKind = 'line' | 'single_value' | 'table' | 'heatmap'

export interface ImportedDashboardTarget {
  ref_id?: string
  expr: string
  legend_format?: string
  label_fields?: string[]
  datasource_uid?: string
}

export interface ImportedDashboardPanelDef {
  unsupported_transformations?: boolean
  panel_id: number
  title: string
  kind: ImportedDashboardPanelKind
  unit?: string
  grid_pos?: { x: number; y: number; w: number; h: number }
  targets?: ImportedDashboardTarget[]
  row_title?: string
  datasource_uid?: string
  thresholds?: ImportedDashboardPanelThreshold[]
}

export interface ImportedDashboardPanelThreshold {
  value?: number
  color: string
  severity: 'normal' | 'warning' | 'critical' | 'unknown'
}

export interface ImportedDashboardDef {
  source_title?: string
  source_uid?: string
  schema_version?: number
  panels: ImportedDashboardPanelDef[]
}

export interface ImportedDashboardVariableBinding {
  name: string
  kind: 'auto' | 'required' | 'rejected'
  grafana_type?: string
  label?: string
  resolved_to?: string
  default?: string
  options?: string[]
  query?: string
  datasource_uid?: string
  reason?: string
  used_by?: string[]
}

export interface ImportedDashboard {
  id: string
  project_id: string
  name: string
  description?: string
  source_title?: string
  source_uid?: string
  schema_version?: number
  enabled: boolean
  panels: ImportedDashboardPanelDef[]
  bindings?: Record<string, string>
  created_at?: string
  updated_at?: string
}

// A resolved panel — line panels carry series (same shape as PrometheusGraph),
// single-value tiles carry value. error is set when that panel's query failed.
export interface ImportedDashboardResolvedPanel {
  panel_id: number
  title: string
  kind: ImportedDashboardPanelKind
  unit?: string
  grid_pos?: { x: number; y: number; w: number; h: number }
  row_title?: string
  series?: PrometheusSeries[]
  value?: number
  error?: string
  thresholds?: ImportedDashboardPanelThreshold[]
  threshold_evaluated?: boolean
  threshold_breached?: boolean
  threshold_detail?: string
}

export interface ImportedDashboardResult {
  dashboard_id: string
  dashboard_title?: string
  datasource_id?: string
  panels: ImportedDashboardResolvedPanel[]
}

export interface ImportedDashboardGraphsResponse {
  enabled: boolean
  configured: boolean
  datasource_id?: string
  generated_at: string
  start?: string
  end?: string
  step_seconds?: number
  dashboards: ImportedDashboardResult[]
}

export interface ImportedDashboardReviewSection {
  key: string
  title: string
  severity: string
  summary: string
  items: string[]
}

export interface ImportedDashboardImportReview {
  import_session_id: string
  expires_at: string
  dashboard: ImportedDashboardDef
  stats: {
    panels_seen: number
    panels_converted: number
    line_charts: number
    single_value_tiles: number
    tables?: number
    heatmaps?: number
    targets: number
    rows_expanded: number
    unsupported_panel_types?: string[]
    variables_resolved: number
    variables_requiring_binding: number
  }
  warnings?: string[]
  assumptions?: string[]
  review_sections?: ImportedDashboardReviewSection[]
  bindings?: ImportedDashboardVariableBinding[]
  required_bindings?: string[]
  has_blockers: boolean
}

// ---------------------------------------------------------------------------
// LRE API-parity families (capacity timeslots, fleet management, execution
// plans, monitoring collectors, entitlements). These mirror the backend JSON
// shapes. CapacityTimeslot is intentionally distinct from the settings-embedded
// CapacityReservation type above (forward-booking reservation vs static pool).
// ---------------------------------------------------------------------------

export type { DiagnosticDistribution } from './types/protocolDiagnostics'

export interface MessagingMoments { count: number; sum: number; max: number }
export interface MessagingDimension { protocol?: string; group?: string; source: string; partition?: string; subscription?: string; operations: number; sent: number; received: number; throttled: number; redelivered: number; age_ms: MessagingMoments; offset_lag: MessagingMoments; ack_ms: MessagingMoments }
export interface MessagingSummary { dimensions: MessagingDimension[]; incomplete?: boolean }

export interface MessagingProgressPosition {
  sequence?: string
  provider_offset?: string
  state: 'observed' | 'missing' | 'disabled' | 'empty' | 'fenced' | 'pending' | 'unconfirmed'
  observed_at: string
  received_at: string
  observation_age_ms?: number
}
export interface MessagingDurationSummary {
  metric: string
  population: string
  eligible: number
  distribution_ms: DiagnosticDistribution
  unavailable?: Record<string, number>
}
export interface MessagingCheckpointProgress {
  attempts: number
  committed: number
  failed: number
  fenced: number
  cancelled_before_write: number
  unconfirmed: number
  retry_attempts: number
  in_flight: number
  duration: MessagingDurationSummary
  last_outcome?: string
  last_completed_at: string
}
export interface MessagingProgressRow {
  origin?: string
  group?: string
  protocol: 'eventhubs'
  mode: 'processor'
  source_id: string
  source: string
  subscription: string
  session_id: string
  partition: string
  generation: string
  revision: string
  capture_revision: string
  captured_at: string
  updated_at: string
  expected_partitions: number
  inventory_complete: boolean
  ownership: 'owned' | 'lost' | 'unverified' | 'released'
  ownership_verified_at: string
  ownership_verified_age_ms?: number
  acquired: number
  lost: number
  delivered: number
  rejected: number
  redelivered: number
  delivered_position: MessagingProgressPosition
  checkpoint_position: MessagingProgressPosition
  tail_position: MessagingProgressPosition
  checkpoint: MessagingCheckpointProgress
  incomplete?: boolean
}
export interface MessagingProgressSnapshot {
  version: 1
  lifecycle?: 'pending' | 'running' | 'draining' | 'released' | 'quarantined'
  captured_at: string
  rows: MessagingProgressRow[]
  incomplete?: boolean
  dropped_rows?: number
  unavailable_reason?: string
}
