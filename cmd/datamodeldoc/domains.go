package main

// domain groups related tables into one diagram and one reference table.
// Every table in the schema must belong to exactly one domain; the tests fail
// when a migration adds a table that is not listed here.
type domain struct {
	title   string
	summary string
	tables  []string
}

var domains = []domain{
	{
		title:   "Tenancy, identity & access",
		summary: "Each domain has at most one application repository, which holds the domain's repository categories. A project belongs to a domain and is the scope for almost every other table. A role binding grants a user a role on one scope, named by scope_type (`global`, `domain`, `application_repository` or `project`) and scope_id.",
		tables: []string{"domains", "application_repositories", "repository_categories", "projects", "domain_feature_policies",
			"users", "user_role_bindings", "service_accounts", "local_auth_credentials", "local_auth_sessions",
			"local_auth_runtime", "project_secrets", "project_secret_provider_metadata", "business_key_project_keys"},
	},
	{
		title:   "Applications, releases & library",
		summary: "An application groups its releases. Scripts and scenarios are assigned to an application and, optionally, to specific releases. Runs and imported external results record the application and release they belong to.",
		tables: []string{"applications", "application_secrets", "application_secret_provider_metadata", "releases", "release_sla_waivers",
			"script_library_assignments", "script_library_assignments_releases", "scenario_library_assignments",
			"scenario_library_assignments_releases", "external_result_imports"},
	},
	{
		title:   "Script authoring & import",
		summary: "A script belongs to a project and can sit in a project folder; scripts outside any folder have no folder_id. A script's DSL body (configuration, parameters, data sources, init, actions and teardown) is stored in columns of its own row. Recordings, import receipts and Bruno source revisions record where a script came from.",
		tables: []string{"folders", "scripts", "script_recordings", "reusable_blocks", "correlation_rulesets", "object_repositories",
			"authoring_sessions", "authoring_mutation_receipts", "import_assets", "direct_import_receipts",
			"bruno_source_artifacts", "bruno_source_revisions", "bruno_native_import_batches", "grafana_import_sessions",
			"protected_source_secret_sessions", "replay_session_leases", "replay_artifact_receipts"},
	},
	{
		title:   "Scenarios & test data",
		summary: "A scenario has no project column of its own: its project is the project of the scripts in its ordered scenario_scripts groups. Shared data pools and project live tables are bound to a scenario through scenario_data_pool_bindings.",
		tables: []string{"scenarios", "scenario_scripts", "scenario_drafts", "scenario_comparison_policies", "scenario_data_pool_bindings",
			"monitor_bindings", "shared_data_pools", "shared_data_pool_rows", "project_live_tables",
			"project_live_table_instances", "project_live_table_attachments", "project_live_table_journal",
			"project_live_table_operator_receipts"},
	},
	{
		title:   "Scheduling, plans & capacity",
		summary: "Scenario schedules and execution plan schedules each produce occurrences, and an occurrence may wait for an approval before it starts a run. Capacity reservations are the timeslots that runs, execution plans and scenario schedules point at through timeslot_id.",
		tables: []string{"scenario_schedules", "scenario_schedule_occurrences", "scenario_schedule_approvals", "execution_plans",
			"execution_plan_runs", "execution_plan_approvals", "execution_plan_schedules",
			"execution_plan_schedule_occurrences", "capacity_reservations", "capacity_approval_locks", "scheduler_leases",
			"scheduler_content_revision", "start_admission_revision"},
	},
	{
		title:   "Runs & results",
		summary: "A run is a row in tests. It references its scenario, release and application, and also keeps a copy of the groups, pools, goals and plan it started with, so later edits to the scenario do not change its history. Interval summaries are the per-interval metrics written while the run is live. Telemetry policy segments record each policy change made while a run is live, in order, without rewriting earlier ones.",
		tables: []string{"tests", "test_history", "test_interval_summaries", "test_notes", "run_annotations", "preparations",
			"run_rendezvous", "threshold_evaluations", "threshold_incidents", "baseline_comparison_snapshots",
			"usage_accounting_snapshots", "test_telemetry_policy_segments"},
	},
	{
		title:   "Run analysis & live state",
		summary: "Root-cause analyses are revisioned per run, with critiques and evidence revisions. Run live tables hold shared mutable data while a run executes, journal every change, and can be finalized into a shared data pool.",
		tables: []string{"rca_analyses", "rca_critiques", "rca_evidence_revisions", "runtime_secret_provisioning_attempts",
			"live_tables", "live_table_journal", "live_table_finalizations"},
	},
	{
		title:   "Run archives, evidence & notifications",
		summary: "When a run ends, its raw results are written to an archive in file or object storage and indexed by run_archives. Finalization, refresh and cleanup are durable job rows for a run, so they resume after a controller restart. Cleanup jobs are written when retention expires a run's archive, when a refresh replaces an archive version, and when a run or its project is deleted. Their test_id has no constraint because a job can outlive the tests row, or, for imported results, name a run that was never in tests.",
		tables: []string{"run_archives", "archive_finalization_jobs", "archive_refresh_jobs", "archive_cleanup_jobs",
			"archive_version_cleanup_jobs", "run_archive_collection_intents", "run_archive_failures",
			"run_archive_replica_holds", "run_evidence_receipts", "telemetry_notification_events",
			"telemetry_notification_deliveries", "telemetry_notification_publications"},
	},
	{
		title:   "Execution infrastructure",
		summary: "An execution target runs on a cloud or Kubernetes cluster connection. A fleet pool draws generators from a target, and generator_metadata holds one row per generator. Provisioning requests and capacity instance intents track scale-up work.",
		tables: []string{"cloud_connections", "cluster_connections", "execution_targets", "domain_deployment_targets",
			"domain_execution_target_allowlist", "fleet_pools", "fleet_pool_members", "generator_metadata",
			"reverse_generator_owners", "provisioning_requests", "provisioning_events", "capacity_instance_intents",
			"pod_capacity_reservations", "fleet_alert_acks", "deployment_leases"},
	},
	{
		title:   "Monitoring collection",
		summary: "Monitoring collectors poll monitor sources. During a run, run_monitor_jobs schedule collection, each batch records one poll (a digest, a sample count and the key of the stored artifact), and observation heads track the latest point of each series.",
		tables: []string{"monitoring_collectors", "monitor_sources", "monitor_management_polls", "monitor_collection_leases",
			"run_monitor_jobs", "run_monitor_batches", "run_monitor_observation_heads", "run_monitor_sessions",
			"run_monitor_collector_readiness", "dynatrace_scan_reservations"},
	},
	{
		title:   "APM & observability connections",
		summary: "Per-project connections to Dynatrace, Elastic and Prometheus, and the Grafana and native dashboards a project has imported or defined. AppDynamics monitoring is retired; the appdynamics_* tables preserve historical retained records only. Inspections and operations are durable records of the calls made through a connection.",
		tables: []string{"dynatrace_connections", "dynatrace_operations", "appdynamics_connections", "appdynamics_inspections",
			"appdynamics_quota_pools", "appdynamics_quota_leases", "elk_connections", "elastic_inspections",
			"prometheus_connections", "grafana_dashboards", "grafana_dashboard_snapshots", "dashboard_definitions",
			"dashboard_definition_revisions"},
	},
	{
		title:   "GitHub integration",
		summary: "A user's GitHub connection and the GitHub App installations drive script links, two-way sync targets and their claims, and the check runs posted for run results.",
		tables: []string{"github_connections", "github_connection_revocations", "github_app_installations", "github_script_links",
			"github_script_sync_targets", "github_script_sync_claims", "github_script_sync_target_claims",
			"github_script_sync_target_owners", "github_sync_jobs", "github_check_intents", "github_check_links",
			"github_webhook_deliveries"},
	},
	{
		title:   "Publishing & offline analysis",
		summary: "Confluence and Power BI publications send run results out. Analysis imports bring external result files in for offline analysis, and feature evaluation jobs score acceptance requirements against results.",
		tables: []string{"confluence_connections", "confluence_publications", "powerbi_publishes", "analysis_definitions",
			"analysis_imports", "analysis_attempt_journal", "analysis_upload_writers", "analysis_cleanup_jobs",
			"feature_evaluation_jobs", "feature_acceptance_records"},
	},
	{
		title:   "Audit, lifecycle & system",
		summary: "Audit and activity events point at any resource by resource type and ID, so they carry no foreign keys. Archiving a script or scenario writes an object_archive_states row rather than changing the object's own row; the row counts only while its incarnation matches the object's created_at. Releases and shared data pools archive through their own archived_at column. A deleted run keeps its project in deleted_test_identities, so its ID cannot be reused in another project.",
		tables: []string{"audit_events", "object_activity_events", "object_activity_migrations", "object_archive_states",
			"deleted_test_identities", "app_settings", "schema_migrations"},
	},
}

type columnRef struct{ table, column string }

// logicalRefs are *_id columns that the storage code joins on without a
// FOREIGN KEY constraint, mapped to the table they hold IDs of. Nothing keeps
// the referenced row in place, so it may be missing.
var logicalRefs = map[columnRef]string{
	{"analysis_attempt_journal", "import_id"}:                "analysis_imports",
	{"analysis_upload_writers", "import_id"}:                 "analysis_imports",
	{"appdynamics_inspections", "connection_id"}:             "appdynamics_connections",
	{"appdynamics_inspections", "collector_id"}:              "monitoring_collectors",
	{"dynatrace_operations", "connection_id"}:                "dynatrace_connections",
	{"dynatrace_operations", "collector_id"}:                 "monitoring_collectors",
	{"elastic_inspections", "connection_id"}:                 "elk_connections",
	{"elastic_inspections", "collector_id"}:                  "monitoring_collectors",
	{"archive_cleanup_jobs", "test_id"}:                      "tests",
	{"archive_version_cleanup_jobs", "test_id"}:              "tests",
	{"capacity_instance_intents", "execution_target_id"}:     "execution_targets",
	{"capacity_instance_intents", "fleet_pool_id"}:           "fleet_pools",
	{"capacity_instance_intents", "test_id"}:                 "tests",
	{"capacity_instance_intents", "provisioning_request_id"}: "provisioning_requests",
	{"capacity_instance_intents", "generator_id"}:            "generator_metadata",
	{"capacity_reservations", "scenario_id"}:                 "scenarios",
	{"capacity_reservations", "bound_test_id"}:               "tests",
	{"confluence_publications", "connection_id"}:             "confluence_connections",
	{"dashboard_definition_revisions", "definition_id"}:      "dashboard_definitions",
	{"direct_import_receipts", "scenario_id"}:                "scenarios",
	{"execution_plan_approvals", "run_id"}:                   "execution_plan_runs",
	{"execution_plan_runs", "plan_id"}:                       "execution_plans",
	{"execution_plan_schedule_occurrences", "plan_id"}:       "execution_plans",
	{"execution_plan_schedule_occurrences", "run_id"}:        "execution_plan_runs",
	{"execution_plan_schedule_occurrences", "approval_id"}:   "execution_plan_approvals",
	{"execution_plan_schedules", "release_id"}:               "releases",
	{"execution_plan_schedules", "last_run_run_id"}:          "execution_plan_runs",
	{"execution_plans", "timeslot_id"}:                       "capacity_reservations",
	{"execution_plans", "application_id"}:                    "applications",
	{"execution_plans", "release_id"}:                        "releases",
	{"external_result_imports", "release_id"}:                "releases",
	{"external_result_imports", "scenario_id"}:               "scenarios",
	{"fleet_pool_members", "pool_id"}:                        "fleet_pools",
	{"fleet_pool_members", "generator_id"}:                   "generator_metadata",
	{"fleet_pools", "cluster_id"}:                            "cluster_connections",
	{"fleet_pools", "execution_target_id"}:                   "execution_targets",
	{"fleet_pools", "domain_id"}:                             "domains",
	{"generator_metadata", "fleet_pool_id"}:                  "fleet_pools",
	{"github_check_intents", "test_id"}:                      "tests",
	{"github_check_intents", "installation_id"}:              "github_app_installations",
	{"github_check_links", "test_id"}:                        "tests",
	{"github_check_links", "script_id"}:                      "scripts",
	{"github_connection_revocations", "user_id"}:             "users",
	{"github_connections", "user_id"}:                        "users",
	{"github_script_links", "script_id"}:                     "scripts",
	{"live_table_finalizations", "table_id"}:                 "live_tables",
	{"live_table_finalizations", "output_pool_id"}:           "shared_data_pools",
	{"live_table_journal", "table_id"}:                       "live_tables",
	{"live_tables", "test_id"}:                               "tests",
	{"monitor_collection_leases", "source_id"}:               "monitor_sources",
	{"monitor_collection_leases", "collector_id"}:            "monitoring_collectors",
	{"monitor_collection_leases", "poll_id"}:                 "monitor_management_polls",
	{"monitor_management_polls", "source_id"}:                "monitor_sources",
	{"monitor_management_polls", "collector_id"}:             "monitoring_collectors",
	{"pod_capacity_reservations", "test_id"}:                 "tests",
	{"powerbi_publishes", "test_id"}:                         "tests",
	{"project_live_table_attachments", "table_id"}:           "project_live_tables",
	{"project_live_table_attachments", "test_id"}:            "tests",
	{"project_live_tables", "current_instance_id"}:           "project_live_table_instances",
	{"provisioning_events", "request_id"}:                    "provisioning_requests",
	{"provisioning_requests", "fleet_pool_id"}:               "fleet_pools",
	{"provisioning_requests", "execution_target_id"}:         "execution_targets",
	{"reverse_generator_owners", "generator_id"}:             "generator_metadata",
	{"reverse_generator_owners", "execution_target_id"}:      "execution_targets",
	{"run_evidence_receipts", "test_id"}:                     "tests",
	{"run_monitor_batches", "management_poll_id"}:            "monitor_management_polls",
	{"run_monitor_jobs", "collector_id"}:                     "monitoring_collectors",
	{"run_monitor_jobs", "source_id"}:                        "monitor_sources",
	{"runtime_secret_provisioning_attempts", "test_id"}:      "tests",
	{"scenario_data_pool_bindings", "project_table_id"}:      "project_live_tables",
	{"scenario_drafts", "scenario_id"}:                       "scenarios",
	{"scenario_schedule_approvals", "scenario_id"}:           "scenarios",
	{"scenario_schedule_occurrences", "scenario_id"}:         "scenarios",
	{"scenario_schedule_occurrences", "approval_id"}:         "scenario_schedule_approvals",
	{"scenario_schedules", "timeslot_id"}:                    "capacity_reservations",
	{"scenario_schedules", "pending_approval_id"}:            "scenario_schedule_approvals",
	{"script_recordings", "script_id"}:                       "scripts",
	{"telemetry_notification_publications", "event_id"}:      "telemetry_notification_events",
	{"tests", "timeslot_id"}:                                 "capacity_reservations",
}

// hubColumns are reference columns named after a tenancy table. Where a table
// has one without a FOREIGN KEY, it still holds that table's IDs.
var hubColumns = map[string]string{"project_id": "projects", "domain_id": "domains"}

// notReferences are *_id columns that hold identifiers with no table of their
// own: external system IDs, lease owners, session and attempt tokens, and
// catalogue keys. validate() requires every unmatched *_id column to be here,
// in logicalRefs, or in polymorphicRefs.
var notReferences = map[columnRef]bool{
	// Cloud provider identities.
	{"cloud_connections", "aws_access_key_id"}: true, {"cloud_connections", "aws_account_id"}: true,
	{"cloud_connections", "azure_client_id"}: true, {"cloud_connections", "azure_subscription_id"}: true,
	{"cloud_connections", "azure_tenant_id"}: true, {"cluster_connections", "aws_access_key_id"}: true,
	{"cluster_connections", "azure_client_id"}: true, {"cluster_connections", "azure_tenant_id"}: true,
	{"capacity_instance_intents", "provider_id"}: true, {"generator_metadata", "provider_id"}: true,
	{"provisioning_requests", "provider_operation_id"}: true,
	// IDs in GitHub, Confluence and Power BI.
	{"github_check_links", "check_run_id"}: true, {"github_connections", "github_user_id"}: true,
	{"confluence_connections", "parent_page_id"}: true, {"confluence_connections", "space_id"}: true,
	{"confluence_publications", "page_id"}: true, {"powerbi_publishes", "dataset_id"}: true,
	{"powerbi_publishes", "workspace_id"}: true,
	// Lease owners and controller instances.
	{"deployment_leases", "owner_id"}: true, {"scheduler_leases", "owner_id"}: true,
	{"live_tables", "owner_id"}: true, {"project_live_table_instances", "owner_id"}: true,
	{"run_rendezvous", "owner_id"}: true, {"appdynamics_quota_leases", "executor_id"}: true,
	{"monitor_collection_leases", "instance_id"}: true, {"monitor_management_polls", "instance_id"}: true,
	{"run_monitor_collector_readiness", "instance_id"}: true, {"run_archive_replica_holds", "fleet_id"}: true,
	{"service_accounts", "owner_id"}: true,
	// Claim, attempt, session and upload tokens.
	{"github_script_sync_claims", "claim_id"}: true, {"github_script_sync_target_claims", "claim_id"}: true,
	{"dynatrace_scan_reservations", "attempt_id"}: true, {"replay_session_leases", "session_id"}: true,
	{"analysis_upload_writers", "file_id"}: true, {"import_assets", "import_id"}: true,
	{"run_monitor_observation_heads", "evidence_trace_id"}: true,
	// Reserved: the API only ever clears it.
	{"capacity_reservations", "approval_id"}: true,
	// Keys into built-in catalogues or JSON documents.
	{"scenarios", "workload_shape_id"}: true, {"fleet_pools", "runtime_profile_id"}: true,
	{"releases", "group_id"}: true, {"feature_acceptance_records", "requirement_id"}: true,
	{"scenarios", "telemetry_acceptance_policy_id"}: true, {"scenarios", "telemetry_budget_policy_id"}: true,
	{"tests", "telemetry_acceptance_policy_id"}: true, {"tests", "telemetry_budget_policy_id"}: true,
	{"test_telemetry_policy_segments", "policy_id"}: true,
	// A client's retry key, unique per run.
	{"test_telemetry_policy_segments", "request_id"}: true,
}

// polymorphicRefs name any resource by a type column plus this ID column.
var polymorphicRefs = map[columnRef]bool{
	{"audit_events", "resource_id"}:                true,
	{"object_activity_events", "resource_id"}:      true,
	{"object_archive_states", "resource_id"}:       true,
	{"authoring_mutation_receipts", "resource_id"}: true,
	{"user_role_bindings", "scope_id"}:             true,
	{"analysis_cleanup_jobs", "resource_id"}:       true,
	{"feature_evaluation_jobs", "subject_id"}:      true,
}

// quietHubs are referenced from nearly every domain. Their edges are drawn only
// inside their own domain; elsewhere the column still shows as a foreign key.
var quietHubs = map[string]bool{"projects": true, "users": true, "domains": true}

// spineEdge is one relationship in the overview diagram: table.column holds
// the ID of parent. A spine edge must exist in the schema, except jsonOnly
// edges, which the run carries inside a JSON column.
type spineEdge struct {
	table, column, parent string
	jsonOnly              bool
}

var spine = []spineEdge{
	{"projects", "domain_id", "domains", false},
	{"folders", "project_id", "projects", false},
	{"scripts", "folder_id", "folders", false},
	{"scripts", "project_id", "projects", false},
	{"applications", "project_id", "projects", false},
	{"releases", "application_id", "applications", false},
	{"scenario_scripts", "script_id", "scripts", false},
	{"scenario_scripts", "scenario_id", "scenarios", false},
	{"tests", "scenario_id", "scenarios", false},
	{"tests", "release_id", "releases", false},
	{"tests", "timeslot_id", "capacity_reservations", false},
	{"scenario_schedules", "scenario_id", "scenarios", false},
	{"scenario_schedules", "last_run_test_id", "tests", false},
	{"test_interval_summaries", "test_id", "tests", false},
	{"run_archives", "test_id", "tests", false},
	{"rca_analyses", "test_id", "tests", false},
	{"run_monitor_jobs", "test_id", "tests", false},
	{"tests", "assignment_json", "fleet_pools", true},
	{"fleet_pools", "execution_target_id", "execution_targets", false},
	{"generator_metadata", "fleet_pool_id", "fleet_pools", false},
	{"execution_targets", "cluster_connection_id", "cluster_connections", false},
	{"execution_targets", "cloud_connection_id", "cloud_connections", false},
}
