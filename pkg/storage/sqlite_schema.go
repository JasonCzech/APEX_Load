package storage

// SQLite store — schema. Split from sqlite.go (same package, behavior-preserving).

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// schemaCreateTableStatements returns every CREATE TABLE statement in dependency
// order. Extracted from init() (rather than an init()-local slice) so the Postgres
// dialect-rewrite guard test can drive the real schema through rewriteSQL — see
// TestPostgresSchemaBooleanColumnsRewriteToBoolean.
func schemaCreateTableStatements() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS domains (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS application_repositories (
			id TEXT PRIMARY KEY,
			domain_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(domain_id),
			FOREIGN KEY(domain_id) REFERENCES domains(id)
		)`,
		`CREATE TABLE IF NOT EXISTS repository_categories (
			id TEXT PRIMARY KEY,
			domain_id TEXT NOT NULL,
			repository_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			sort_order INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(domain_id) REFERENCES domains(id),
			FOREIGN KEY(repository_id) REFERENCES application_repositories(id)
		)`,
		`CREATE TABLE IF NOT EXISTS applications (
			id TEXT PRIMARY KEY,
			project_id TEXT REFERENCES projects(id),
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS releases (
			id TEXT PRIMARY KEY,
			application_id TEXT NOT NULL,
			project_id TEXT REFERENCES projects(id),
			name TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			archived_at DATETIME,
			FOREIGN KEY(application_id) REFERENCES applications(id)
		)`,
		`CREATE TABLE IF NOT EXISTS release_sla_waivers (
			id TEXT PRIMARY KEY,
			release_id TEXT NOT NULL,
			project_id TEXT,
			scope TEXT NOT NULL,
			reason TEXT NOT NULL,
			approved_by TEXT,
			status TEXT NOT NULL,
			expires_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(release_id) REFERENCES releases(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenarios (
			id TEXT PRIMARY KEY,
			ref_key TEXT,
			name TEXT NOT NULL,
			duration TEXT NOT NULL,
			start_delay TEXT,
			scenario_mode TEXT,
			shakeout_enabled INTEGER DEFAULT 0,
			resource_baseline_enabled INTEGER DEFAULT 0,
			assignment_json TEXT,
			goal_config_json TEXT,
			runtime_settings_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_schedules (
			id TEXT PRIMARY KEY,
			scenario_id TEXT NOT NULL,
			project_id TEXT,
			revision INTEGER DEFAULT 1,
			name TEXT NOT NULL,
			cron_expr TEXT NOT NULL,
			timezone TEXT,
			release_id TEXT,
			timeslot_id TEXT,
			run_name_template TEXT,
			enabled INTEGER DEFAULT 1,
			approval_policy TEXT,
			missed_run_policy TEXT,
			conflict_summary TEXT,
			next_required_action TEXT,
			pending_approval_id TEXT,
			owner_subject TEXT,
			owner_display_name TEXT,
			governance_json TEXT,
			blackout_windows_json TEXT,
			maintenance_windows_json TEXT,
			availability_windows_json TEXT,
			last_run_test_id TEXT,
			last_run_at DATETIME,
			next_run_at DATETIME,
			disabled_from DATETIME,
			disabled_until DATETIME,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(release_id) REFERENCES releases(id),
			FOREIGN KEY(last_run_test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_schedule_occurrences (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			scenario_id TEXT NOT NULL,
			project_id TEXT,
			schedule_revision INTEGER DEFAULT 1,
			planned_at DATETIME NOT NULL,
			materialized_at DATETIME,
			status TEXT NOT NULL,
			reason_code TEXT,
			test_id TEXT,
			approval_id TEXT,
			conflict_summary TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(schedule_id, planned_at),
			FOREIGN KEY(schedule_id) REFERENCES scenario_schedules(id),
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_schedule_approvals (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			scenario_id TEXT NOT NULL,
			project_id TEXT,
			schedule_revision INTEGER DEFAULT 1,
			policy TEXT NOT NULL,
			status TEXT NOT NULL,
			requested_by TEXT,
			requested_at DATETIME NOT NULL,
			resolved_by TEXT,
			resolved_at DATETIME,
			decision_reason TEXT,
			required_role TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(schedule_id) REFERENCES scenario_schedules(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scheduler_leases (
			name TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			acquired_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_scripts (
			scenario_id TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			name TEXT,
			script_id TEXT NOT NULL,
			vusers INTEGER,
			ramp_up TEXT,
			ramp_down TEXT,
			stages_json TEXT,
			initialization TEXT,
			role TEXT,
			runtime_settings_json TEXT,
			assignment_json TEXT,
			PRIMARY KEY (scenario_id, sort_order),
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(script_id) REFERENCES scripts(id)
		)`,
		`CREATE TABLE IF NOT EXISTS shared_data_pools (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			schema_json TEXT,
			project_id TEXT REFERENCES projects(id),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			archived_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS shared_data_pool_rows (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			data_json TEXT NOT NULL,
			status TEXT,
			version INTEGER DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(pool_id) REFERENCES shared_data_pools(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scenario_data_pool_bindings (
			id TEXT PRIMARY KEY,
			scenario_id TEXT NOT NULL,
			pool_id TEXT NOT NULL,
			alias TEXT NOT NULL,
			access_mode TEXT NOT NULL,
			selection_strategy TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(scenario_id, alias),
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(pool_id) REFERENCES shared_data_pools(id)
		)`,
		`CREATE TABLE IF NOT EXISTS projects (
			id TEXT PRIMARY KEY,
			domain_id TEXT REFERENCES domains(id),
			category_id TEXT REFERENCES repository_categories(id),
			name TEXT NOT NULL,
			settings_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS folders (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			parent_id TEXT,
			name TEXT NOT NULL,
			category TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id),
			FOREIGN KEY(parent_id) REFERENCES folders(id)
		)`,
		`CREATE TABLE IF NOT EXISTS scripts (
			id TEXT PRIMARY KEY,
			ref_key TEXT,
			name TEXT,
			protocol TEXT,
			version TEXT,
			project_id TEXT,
			folder_id TEXT,
			generated_by TEXT,
			generated_from TEXT,
			metadata_json TEXT,
			config TEXT,
			data_sources TEXT,
			parameters TEXT,
			init TEXT,
			actions TEXT,
			teardown TEXT,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS tests (
			id TEXT PRIMARY KEY,
			ref_key TEXT,
			name TEXT,
			status TEXT,
			application_id TEXT,
			release_id TEXT,
			scenario_id TEXT,
			project_id TEXT,
			folder_id TEXT,
			scenario_mode TEXT,
			shakeout_enabled INTEGER DEFAULT 0,
			resource_baseline_enabled INTEGER DEFAULT 0,
			assignment_json TEXT,
			total_vusers INTEGER,
			duration_ns INTEGER,
			start_time DATETIME,
			scheduled_start_at DATETIME,
			pending_since DATETIME,
			baseline_pinned INTEGER DEFAULT 0,
			baseline_pinned_at DATETIME,
			end_time DATETIME,
			error TEXT,
			created_by_subject TEXT,
			created_by_display_name TEXT,
			started_by_subject TEXT,
			started_by_display_name TEXT,
			stopped_by_subject TEXT,
			stopped_by_display_name TEXT,
			groups_json TEXT,
			goal_config_json TEXT,
			execution_plan_approval_json TEXT,
			timeslot_id TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS test_history (
			test_id TEXT,
			timestamp DATETIME,
			metrics_json TEXT,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS test_notes (
			id TEXT PRIMARY KEY,
			test_id TEXT NOT NULL,
			body TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			created_by TEXT,
			updated_by TEXT,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS test_interval_summaries (
			test_id TEXT,
			schema_version TEXT,
			timestamp DATETIME,
			active_vusers INTEGER,
			tps REAL,
			avg_response_ms REAL,
			error_pct REAL,
			total_transactions INTEGER,
			total_errors INTEGER,
			p50_ms REAL,
			p90_ms REAL,
			p95_ms REAL,
			p99_ms REAL,
			browser_json TEXT,
			browser_by_device_json TEXT,
			latency_json TEXT,
			dropped_results INTEGER,
			websocket_clients INTEGER,
			websocket_dropped_messages INTEGER,
			websocket_dropped_events INTEGER,
			generator_published_at DATETIME,
			controller_received_at DATETIME,
			collected_at DATETIME,
			published_at DATETIME,
			generator_processing_lag_ms REAL,
			transport_lag_ms REAL,
			controller_fanout_lag_ms REAL,
			sample_age_ms REAL,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS run_archives (
			test_id TEXT PRIMARY KEY,
			ref_key TEXT,
			schema_version TEXT,
			archive_backend TEXT,
			archive_path TEXT,
			manifest_path TEXT,
			manifest_sha256 TEXT,
			archive_uri TEXT,
			manifest_uri TEXT,
			transaction_count INTEGER,
			resource_sample_count INTEGER,
			interval_summary_count INTEGER,
			created_at DATETIME,
			completed_at DATETIME,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS github_connections (
			user_id TEXT PRIMARY KEY,
			github_login TEXT,
			github_user_id TEXT,
			access_token_enc TEXT,
			refresh_token_enc TEXT,
			token_type TEXT,
			scopes TEXT,
			expires_at DATETIME,
			connected_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS github_script_links (
			script_id TEXT PRIMARY KEY,
			ref_key TEXT,
			owner TEXT,
			repo TEXT,
			branch TEXT,
			path TEXT,
			last_commit_sha TEXT,
			remote_blob_sha TEXT,
			remote_commit_sha TEXT,
			content_hash TEXT,
			synced_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS github_app_installations (
			installation_id INTEGER PRIMARY KEY,
			account_login TEXT,
			account_type TEXT,
			repository_selection TEXT,
			permissions_json TEXT,
			events_json TEXT,
			html_url TEXT,
			suspended_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS github_sync_jobs (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			kind TEXT,
			status TEXT,
			owner TEXT,
			repo TEXT,
			branch TEXT,
			path TEXT,
			total INTEGER DEFAULT 0,
			processed INTEGER DEFAULT 0,
			created INTEGER DEFAULT 0,
			updated INTEGER DEFAULT 0,
			conflicts INTEGER DEFAULT 0,
			errors INTEGER DEFAULT 0,
			summary_json TEXT,
			started_by TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS github_webhook_deliveries (
			delivery_id TEXT PRIMARY KEY,
			event TEXT,
			action TEXT,
			owner TEXT,
			repo TEXT,
			ref TEXT,
			commit_sha TEXT,
			status TEXT,
			summary_json TEXT,
			received_at DATETIME NOT NULL,
			processed_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS github_check_links (
			id TEXT PRIMARY KEY,
			test_id TEXT,
			script_id TEXT,
			owner TEXT,
			repo TEXT,
			commit_sha TEXT,
			check_run_id INTEGER,
			check_run_url TEXT,
			status TEXT,
			conclusion TEXT,
			published_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS service_accounts (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			owner_id TEXT,
			contact_email TEXT,
			key_hash TEXT NOT NULL UNIQUE,
			active INTEGER NOT NULL DEFAULT 1,
			global_roles_json TEXT,
			project_roles_json TEXT,
			domain_roles_json TEXT,
			repository_roles_json TEXT,
			scopes_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_used_at DATETIME,
			expires_at DATETIME,
			key_rotated_at DATETIME,
			rotation_interval_days INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			subject TEXT NOT NULL UNIQUE,
			email TEXT,
			display_name TEXT,
			active INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_seen_at DATETIME,
			disabled_at DATETIME,
			session_epoch INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS user_role_bindings (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			scope_type TEXT NOT NULL,
			scope_id TEXT,
			role TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(user_id, scope_type, scope_id, role),
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_subject TEXT NOT NULL,
			actor_display_name TEXT,
			project_id TEXT,
			domain_id TEXT,
			resource_type TEXT,
			resource_id TEXT,
			status TEXT NOT NULL,
			details_json TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS usage_accounting_snapshots (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			generated_at DATETIME NOT NULL,
			payload_json TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS external_result_imports (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			name TEXT NOT NULL,
			project_id TEXT,
			release_id TEXT,
			scenario_id TEXT,
			source TEXT,
			summary_json TEXT,
			raw_json TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS script_recordings (
			script_id TEXT PRIMARY KEY,
			schema_version TEXT,
			source TEXT,
			sensitivity TEXT,
			request_count INTEGER,
			observations_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS authoring_sessions (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			source TEXT NOT NULL,
			name TEXT NOT NULL,
			status TEXT NOT NULL,
			draft_json TEXT,
			review_state_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			last_accessed_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS capacity_reservations (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			scenario_id TEXT,
			start_at DATETIME NOT NULL,
			end_at DATETIME NOT NULL,
			timezone TEXT,
			requested_capacity_json TEXT,
			approval_state TEXT NOT NULL DEFAULT 'pending',
			approval_id TEXT,
			bound_test_id TEXT,
			bound_test_status TEXT,
			conflict_summary TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS fleet_pools (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			cloud_provider TEXT NOT NULL,
			region TEXT,
			min_size INTEGER DEFAULT 0,
			max_size INTEGER DEFAULT 0,
			desired_size INTEGER DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'active',
			tags_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS fleet_pool_members (
			id TEXT PRIMARY KEY,
			pool_id TEXT NOT NULL,
			generator_id TEXT NOT NULL,
			target TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'provisioned',
			joined_at DATETIME NOT NULL,
			UNIQUE(pool_id, generator_id)
		)`,
		`CREATE TABLE IF NOT EXISTS generator_metadata (
			generator_id TEXT PRIMARY KEY,
			target TEXT,
			fleet_pool_id TEXT,
			region TEXT,
			provider_id TEXT,
			provider_status TEXT,
			cordoned INTEGER DEFAULT 0,
			tags_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS provisioning_requests (
			id TEXT PRIMARY KEY,
			fleet_pool_id TEXT NOT NULL,
			project_id TEXT,
			status TEXT NOT NULL DEFAULT 'pending',
			desired_count INTEGER NOT NULL DEFAULT 0,
			reason TEXT,
			requested_by_subject TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS provisioning_events (
			id TEXT PRIMARY KEY,
			request_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			message TEXT,
			timestamp DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS fleet_alert_acks (
			id TEXT PRIMARY KEY,
			alert_type TEXT NOT NULL,
			target TEXT NOT NULL,
			region TEXT,
			severity TEXT,
			summary TEXT,
			note TEXT,
			acknowledged_by_subject TEXT,
			acknowledged_by_display_name TEXT,
			acknowledged_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS execution_plans (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT NOT NULL,
			description TEXT,
			entries_json TEXT,
			run_mode TEXT NOT NULL DEFAULT 'parallel',
			timeslot_id TEXT,
			status TEXT DEFAULT 'draft',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS execution_plan_runs (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			project_id TEXT,
			run_mode TEXT NOT NULL DEFAULT 'sequential',
			status TEXT NOT NULL DEFAULT 'running',
			entries_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS execution_plan_schedules (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			project_id TEXT,
			revision INTEGER DEFAULT 1,
			name TEXT NOT NULL,
			cron_expr TEXT NOT NULL,
			timezone TEXT,
			run_name_template TEXT,
			enabled INTEGER DEFAULT 1,
			missed_run_policy TEXT,
			conflict_summary TEXT,
			next_required_action TEXT,
			last_run_run_id TEXT,
			last_run_at DATETIME,
			next_run_at DATETIME,
			disabled_from DATETIME,
			disabled_until DATETIME,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(plan_id) REFERENCES execution_plans(id)
		)`,
		`CREATE TABLE IF NOT EXISTS execution_plan_approvals (
			id TEXT PRIMARY KEY,
			plan_id TEXT NOT NULL,
			project_id TEXT,
			policy TEXT NOT NULL,
			status TEXT NOT NULL,
			requested_by TEXT,
			requested_at DATETIME NOT NULL,
			resolved_by TEXT,
			resolved_at DATETIME,
			decision_reason TEXT,
			required_role TEXT,
			plan_content_hash TEXT,
			run_id TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(plan_id) REFERENCES execution_plans(id)
		)`,
		`CREATE TABLE IF NOT EXISTS execution_plan_schedule_occurrences (
			id TEXT PRIMARY KEY,
			schedule_id TEXT NOT NULL,
			plan_id TEXT NOT NULL,
			project_id TEXT,
			schedule_revision INTEGER DEFAULT 1,
			planned_at DATETIME NOT NULL,
			materialized_at DATETIME,
			status TEXT NOT NULL,
			reason_code TEXT,
			run_id TEXT,
			conflict_summary TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(schedule_id, planned_at),
			FOREIGN KEY(schedule_id) REFERENCES execution_plan_schedules(id)
		)`,
		`CREATE TABLE IF NOT EXISTS object_repositories (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT NOT NULL,
			description TEXT,
			source TEXT,
			objects_json TEXT,
			auto_promote_on_heal INTEGER DEFAULT 0,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS correlation_rulesets (
			id TEXT PRIMARY KEY,
			project_id TEXT,
			name TEXT NOT NULL,
			description TEXT,
			source TEXT,
			rules_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS monitoring_collectors (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			token_hash TEXT NOT NULL,
			health TEXT NOT NULL DEFAULT 'unreachable',
			last_seen_at DATETIME,
			allowed_metric_packs_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS reusable_blocks (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			kind TEXT,
			component_json TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
	}
}

// schemaColumnMigrations returns the additive ALTER TABLE ... ADD COLUMN statements
// applied on every init (idempotent via isDuplicateColumnErr). Extracted alongside
// schemaCreateTableStatements so the Postgres rewrite guard test can also cover the
// columns added here (e.g. boolean flags added to existing tables).
func schemaColumnMigrations() []optionalSchemaMigration {
	return []optionalSchemaMigration{
		{name: "users.session_epoch", query: `ALTER TABLE users ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0`},
		{name: "tests.runtime_settings_json", query: `ALTER TABLE tests ADD COLUMN runtime_settings_json TEXT`},
		{name: "tests.scenario_mode", query: `ALTER TABLE tests ADD COLUMN scenario_mode TEXT`},
		{name: "tests.goal_config_json", query: `ALTER TABLE tests ADD COLUMN goal_config_json TEXT`},
		{name: "tests.timeslot_id", query: `ALTER TABLE tests ADD COLUMN timeslot_id TEXT`},
		{name: "tests.shakeout_enabled", query: `ALTER TABLE tests ADD COLUMN shakeout_enabled INTEGER DEFAULT 0`},
		{name: "tests.resource_baseline_enabled", query: `ALTER TABLE tests ADD COLUMN resource_baseline_enabled INTEGER DEFAULT 0`},
		{name: "scenarios.assignment_json", query: `ALTER TABLE scenarios ADD COLUMN assignment_json TEXT`},
		{name: "scenarios.start_delay", query: `ALTER TABLE scenarios ADD COLUMN start_delay TEXT`},
		{name: "tests.assignment_json", query: `ALTER TABLE tests ADD COLUMN assignment_json TEXT`},
		{name: "scripts.metadata_json", query: `ALTER TABLE scripts ADD COLUMN metadata_json TEXT`},
		{name: "object_repositories.auto_promote_on_heal", query: `ALTER TABLE object_repositories ADD COLUMN auto_promote_on_heal INTEGER DEFAULT 0`},
		{name: "projects.settings_json", query: `ALTER TABLE projects ADD COLUMN settings_json TEXT`},
		{name: "projects.domain_id", query: `ALTER TABLE projects ADD COLUMN domain_id TEXT REFERENCES domains(id)`},
		{name: "projects.category_id", query: `ALTER TABLE projects ADD COLUMN category_id TEXT REFERENCES repository_categories(id)`},
		{name: "applications.project_id", query: `ALTER TABLE applications ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "releases.project_id", query: `ALTER TABLE releases ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "scripts.project_id", query: `ALTER TABLE scripts ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "scripts.folder_id", query: `ALTER TABLE scripts ADD COLUMN folder_id TEXT REFERENCES folders(id)`},
		{name: "scripts.parameters", query: `ALTER TABLE scripts ADD COLUMN parameters TEXT`},
		{name: "tests.project_id", query: `ALTER TABLE tests ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "tests.folder_id", query: `ALTER TABLE tests ADD COLUMN folder_id TEXT REFERENCES folders(id)`},
		{name: "tests.application_id", query: `ALTER TABLE tests ADD COLUMN application_id TEXT REFERENCES applications(id)`},
		{name: "tests.release_id", query: `ALTER TABLE tests ADD COLUMN release_id TEXT REFERENCES releases(id)`},
		{name: "tests.scenario_id", query: `ALTER TABLE tests ADD COLUMN scenario_id TEXT REFERENCES scenarios(id)`},
		{name: "tests.scheduled_start_at", query: `ALTER TABLE tests ADD COLUMN scheduled_start_at DATETIME`},
		{name: "tests.pending_since", query: `ALTER TABLE tests ADD COLUMN pending_since DATETIME`},
		{name: "tests.baseline_pinned", query: `ALTER TABLE tests ADD COLUMN baseline_pinned INTEGER DEFAULT 0`},
		{name: "tests.baseline_pinned_at", query: `ALTER TABLE tests ADD COLUMN baseline_pinned_at DATETIME`},
		{name: "shared_data_pools.project_id", query: `ALTER TABLE shared_data_pools ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "releases.archived_at", query: `ALTER TABLE releases ADD COLUMN archived_at DATETIME`},
		{name: "test_interval_summaries.schema_version", query: `ALTER TABLE test_interval_summaries ADD COLUMN schema_version TEXT`},
		{name: "run_archives.schema_version", query: `ALTER TABLE run_archives ADD COLUMN schema_version TEXT`},
		{name: "run_archives.archive_backend", query: `ALTER TABLE run_archives ADD COLUMN archive_backend TEXT`},
		{name: "run_archives.archive_uri", query: `ALTER TABLE run_archives ADD COLUMN archive_uri TEXT`},
		{name: "run_archives.manifest_uri", query: `ALTER TABLE run_archives ADD COLUMN manifest_uri TEXT`},
		{name: "run_archives.manifest_sha256", query: `ALTER TABLE run_archives ADD COLUMN manifest_sha256 TEXT`},
		{name: "test_interval_summaries.generator_published_at", query: `ALTER TABLE test_interval_summaries ADD COLUMN generator_published_at DATETIME`},
		{name: "test_interval_summaries.controller_received_at", query: `ALTER TABLE test_interval_summaries ADD COLUMN controller_received_at DATETIME`},
		{name: "test_interval_summaries.generator_processing_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN generator_processing_lag_ms REAL`},
		{name: "test_interval_summaries.transport_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN transport_lag_ms REAL`},
		{name: "test_interval_summaries.controller_fanout_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN controller_fanout_lag_ms REAL`},
		{name: "test_interval_summaries.browser_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN browser_json TEXT`},
		{name: "test_interval_summaries.browser_by_device_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN browser_by_device_json TEXT`},
		{name: "test_interval_summaries.latency_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN latency_json TEXT`},
		{name: "service_accounts.active", query: `ALTER TABLE service_accounts ADD COLUMN active INTEGER NOT NULL DEFAULT 1`},
		{name: "service_accounts.expires_at", query: `ALTER TABLE service_accounts ADD COLUMN expires_at DATETIME`},
		{name: "service_accounts.key_rotated_at", query: `ALTER TABLE service_accounts ADD COLUMN key_rotated_at DATETIME`},
		{name: "service_accounts.rotation_interval_days", query: `ALTER TABLE service_accounts ADD COLUMN rotation_interval_days INTEGER`},
		{name: "service_accounts.scopes_json", query: `ALTER TABLE service_accounts ADD COLUMN scopes_json TEXT`},
		{name: "service_accounts.domain_roles_json", query: `ALTER TABLE service_accounts ADD COLUMN domain_roles_json TEXT`},
		{name: "service_accounts.repository_roles_json", query: `ALTER TABLE service_accounts ADD COLUMN repository_roles_json TEXT`},
		{name: "service_accounts.owner_id", query: `ALTER TABLE service_accounts ADD COLUMN owner_id TEXT`},
		{name: "service_accounts.contact_email", query: `ALTER TABLE service_accounts ADD COLUMN contact_email TEXT`},
		{name: "audit_events.domain_id", query: `ALTER TABLE audit_events ADD COLUMN domain_id TEXT`},
		{name: "tests.created_by_subject", query: `ALTER TABLE tests ADD COLUMN created_by_subject TEXT`},
		{name: "tests.created_by_display_name", query: `ALTER TABLE tests ADD COLUMN created_by_display_name TEXT`},
		{name: "tests.started_by_subject", query: `ALTER TABLE tests ADD COLUMN started_by_subject TEXT`},
		{name: "tests.started_by_display_name", query: `ALTER TABLE tests ADD COLUMN started_by_display_name TEXT`},
		{name: "tests.stopped_by_subject", query: `ALTER TABLE tests ADD COLUMN stopped_by_subject TEXT`},
		{name: "tests.stopped_by_display_name", query: `ALTER TABLE tests ADD COLUMN stopped_by_display_name TEXT`},
		{name: "tests.execution_plan_approval_json", query: `ALTER TABLE tests ADD COLUMN execution_plan_approval_json TEXT`},
		{name: "scenario_scripts.stages_json", query: `ALTER TABLE scenario_scripts ADD COLUMN stages_json TEXT`},
		{name: "scenario_scripts.role", query: `ALTER TABLE scenario_scripts ADD COLUMN role TEXT`},
		{name: "scenario_scripts.runtime_settings_json", query: `ALTER TABLE scenario_scripts ADD COLUMN runtime_settings_json TEXT`},
		{name: "scenario_scripts.assignment_json", query: `ALTER TABLE scenario_scripts ADD COLUMN assignment_json TEXT`},
		{name: "scenario_schedules.project_id", query: `ALTER TABLE scenario_schedules ADD COLUMN project_id TEXT`},
		{name: "scenario_schedules.timeslot_id", query: `ALTER TABLE scenario_schedules ADD COLUMN timeslot_id TEXT`},
		{name: "scenario_schedules.owner_subject", query: `ALTER TABLE scenario_schedules ADD COLUMN owner_subject TEXT`},
		{name: "scenario_schedules.owner_display_name", query: `ALTER TABLE scenario_schedules ADD COLUMN owner_display_name TEXT`},
		{name: "scenario_schedules.governance_json", query: `ALTER TABLE scenario_schedules ADD COLUMN governance_json TEXT`},
		{name: "scenario_schedules.blackout_windows_json", query: `ALTER TABLE scenario_schedules ADD COLUMN blackout_windows_json TEXT`},
		{name: "scenario_schedules.maintenance_windows_json", query: `ALTER TABLE scenario_schedules ADD COLUMN maintenance_windows_json TEXT`},
		{name: "scenario_schedules.availability_windows_json", query: `ALTER TABLE scenario_schedules ADD COLUMN availability_windows_json TEXT`},
		{name: "scenario_schedules.revision", query: `ALTER TABLE scenario_schedules ADD COLUMN revision INTEGER DEFAULT 1`},
		{name: "scenario_schedules.approval_policy", query: `ALTER TABLE scenario_schedules ADD COLUMN approval_policy TEXT`},
		{name: "scenario_schedules.missed_run_policy", query: `ALTER TABLE scenario_schedules ADD COLUMN missed_run_policy TEXT`},
		{name: "scenario_schedules.conflict_summary", query: `ALTER TABLE scenario_schedules ADD COLUMN conflict_summary TEXT`},
		{name: "scenario_schedules.next_required_action", query: `ALTER TABLE scenario_schedules ADD COLUMN next_required_action TEXT`},
		{name: "scenario_schedules.pending_approval_id", query: `ALTER TABLE scenario_schedules ADD COLUMN pending_approval_id TEXT`},
		{name: "scenario_schedules.disabled_from", query: `ALTER TABLE scenario_schedules ADD COLUMN disabled_from DATETIME`},
		{name: "scenario_schedules.disabled_until", query: `ALTER TABLE scenario_schedules ADD COLUMN disabled_until DATETIME`},
		{name: "scripts.ref_key", query: `ALTER TABLE scripts ADD COLUMN ref_key TEXT`},
		{name: "scenarios.ref_key", query: `ALTER TABLE scenarios ADD COLUMN ref_key TEXT`},
		{name: "tests.ref_key", query: `ALTER TABLE tests ADD COLUMN ref_key TEXT`},
		{name: "run_archives.ref_key", query: `ALTER TABLE run_archives ADD COLUMN ref_key TEXT`},
		{name: "github_script_links.remote_blob_sha", query: `ALTER TABLE github_script_links ADD COLUMN remote_blob_sha TEXT`},
		{name: "github_script_links.remote_commit_sha", query: `ALTER TABLE github_script_links ADD COLUMN remote_commit_sha TEXT`},
		{name: "execution_plan_approvals.plan_content_hash", query: `ALTER TABLE execution_plan_approvals ADD COLUMN plan_content_hash TEXT`},
	}
}

// schemaIndex is a CREATE INDEX statement plus the index name used in error reporting.
type schemaIndex struct {
	query string
	name  string
}

// schemaIndexes holds the unique/partial indexes created after the base tables and
// migrations. Unique indexes guard against duplicate reference keys; partial
// indexes skip NULL/empty values so legacy rows awaiting backfill do not collide.
var schemaIndexes = []schemaIndex{
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_ref_key ON scripts(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_scripts_ref_key"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_scenarios_ref_key ON scenarios(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_scenarios_ref_key"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_tests_ref_key ON tests(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_tests_ref_key"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_run_archives_ref_key ON run_archives(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_run_archives_ref_key"},
	{`CREATE INDEX IF NOT EXISTS idx_test_notes_test_id ON test_notes(test_id)`, "idx_test_notes_test_id"},
	{`CREATE INDEX IF NOT EXISTS idx_capacity_reservations_project_time ON capacity_reservations(project_id, start_at, end_at)`, "idx_capacity_reservations_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_fleet_pool_members_pool ON fleet_pool_members(pool_id)`, "idx_fleet_pool_members_pool"},
	{`CREATE INDEX IF NOT EXISTS idx_generator_metadata_pool ON generator_metadata(fleet_pool_id)`, "idx_generator_metadata_pool"},
	{`CREATE INDEX IF NOT EXISTS idx_provisioning_requests_pool ON provisioning_requests(fleet_pool_id)`, "idx_provisioning_requests_pool"},
	{`CREATE INDEX IF NOT EXISTS idx_provisioning_events_request ON provisioning_events(request_id)`, "idx_provisioning_events_request"},
	{`CREATE INDEX IF NOT EXISTS idx_fleet_alert_acks_acknowledged_at ON fleet_alert_acks(acknowledged_at)`, "idx_fleet_alert_acks_acknowledged_at"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plans_project ON execution_plans(project_id)`, "idx_execution_plans_project"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_runs_plan ON execution_plan_runs(plan_id)`, "idx_execution_plan_runs_plan"},
	{`CREATE INDEX IF NOT EXISTS idx_object_repositories_project ON object_repositories(project_id)`, "idx_object_repositories_project"},
	{`CREATE INDEX IF NOT EXISTS idx_correlation_rulesets_project ON correlation_rulesets(project_id)`, "idx_correlation_rulesets_project"},
	{`CREATE INDEX IF NOT EXISTS idx_monitoring_collectors_project ON monitoring_collectors(project_id)`, "idx_monitoring_collectors_project"},
	{`CREATE INDEX IF NOT EXISTS idx_reusable_blocks_project ON reusable_blocks(project_id)`, "idx_reusable_blocks_project"},
	// audit_events and usage_accounting_snapshots grow unbounded on a long-lived
	// controller; index the hot ListAuditEvents path (ORDER BY created_at DESC plus
	// frequent project/actor filters) and the retention-prune scan.
	{`CREATE INDEX IF NOT EXISTS idx_audit_events_created ON audit_events(created_at)`, "idx_audit_events_created"},
	{`CREATE INDEX IF NOT EXISTS idx_audit_events_project_created ON audit_events(project_id, created_at)`, "idx_audit_events_project_created"},
	{`CREATE INDEX IF NOT EXISTS idx_audit_events_actor_created ON audit_events(actor_subject, created_at)`, "idx_audit_events_actor_created"},
	{`CREATE INDEX IF NOT EXISTS idx_audit_events_action_status_created ON audit_events(action, status, created_at)`, "idx_audit_events_action_status_created"},
	{`CREATE INDEX IF NOT EXISTS idx_audit_events_status_created ON audit_events(status, created_at)`, "idx_audit_events_status_created"},
	{`CREATE INDEX IF NOT EXISTS idx_audit_events_resource_created ON audit_events(resource_type, resource_id, created_at)`, "idx_audit_events_resource_created"},
	{`CREATE INDEX IF NOT EXISTS idx_usage_snapshots_created ON usage_accounting_snapshots(created_at)`, "idx_usage_snapshots_created"},
	{`CREATE INDEX IF NOT EXISTS idx_usage_snapshots_project_created ON usage_accounting_snapshots(project_id, created_at)`, "idx_usage_snapshots_project_created"},
}

func (s *SQLiteStore) init() error {
	for _, q := range schemaCreateTableStatements() {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to run migration query: %w", err)
		}
	}

	if err := s.applyOptionalSchemaMigrations(schemaColumnMigrations()); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE github_script_links SET remote_blob_sha = last_commit_sha WHERE (remote_blob_sha IS NULL OR remote_blob_sha = '') AND last_commit_sha IS NOT NULL AND last_commit_sha != ''`); err != nil {
		return fmt.Errorf("failed to backfill github_script_links.remote_blob_sha: %w", err)
	}

	for _, idx := range schemaIndexes {
		if _, err := s.db.Exec(idx.query); err != nil {
			return fmt.Errorf("failed to create %s: %w", idx.name, err)
		}
	}

	if _, err := s.db.Exec(`UPDATE test_interval_summaries SET schema_version = ? WHERE schema_version IS NULL OR TRIM(schema_version) = ''`, intervalSummarySchemaVersionV1); err != nil {
		return fmt.Errorf("failed to backfill test_interval_summaries.schema_version: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE run_archives SET schema_version = ? WHERE schema_version IS NULL OR TRIM(schema_version) = ''`, archiveRecordSchemaVersion); err != nil {
		return fmt.Errorf("failed to backfill run_archives.schema_version: %w", err)
	}

	if err := s.ensureDefaultDomainHierarchy(); err != nil {
		return err
	}
	if err := s.ensureDefaultProject(); err != nil {
		return err
	}
	if err := s.ensureLegacyApplication(); err != nil {
		return err
	}
	return s.applyTimescaleDB()
}

func timescaleSetupStatements(tables []timescaleHypertable) []string {
	statements := []string{`CREATE EXTENSION IF NOT EXISTS timescaledb`}
	for _, ht := range tables {
		statements = append(statements, fmt.Sprintf(
			`SELECT create_hypertable('%s', '%s', if_not_exists => TRUE, migrate_data => TRUE)`,
			ht.table, ht.timeColumn,
		))
	}
	return statements
}

func (s *SQLiteStore) applyTimescaleDB() error {
	if !s.timescaleDB || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	for _, stmt := range timescaleSetupStatements(timescaleHypertables) {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to apply TimescaleDB setup (%q): %w", stmt, err)
		}
	}
	return nil
}

func (s *SQLiteStore) applyOptionalSchemaMigrations(migrations []optionalSchemaMigration) error {
	for _, migration := range migrations {
		if _, err := s.db.Exec(migration.query); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("failed to apply schema migration %s: %w", migration.name, err)
		}
	}
	return nil
}

func isDuplicateColumnErr(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate column name") || strings.Contains(message, "already exists")
}

func (s *SQLiteStore) ensureDefaultDomainHierarchy() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO domains (id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, DefaultDomainID, "Default Domain", "Default migrated APEX Load domain.", now, now); err != nil {
		return fmt.Errorf("failed to create default domain: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO application_repositories (id, domain_id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, DefaultApplicationRepositoryID, DefaultDomainID, "Application Repository", "Default migrated application repository.", now, now); err != nil {
		return fmt.Errorf("failed to create default application repository: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO repository_categories (id, domain_id, repository_id, name, description, sort_order, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, DefaultRepositoryCategoryID, DefaultDomainID, DefaultApplicationRepositoryID, "Uncategorized", "Default category for migrated projects.", 0, now, now); err != nil {
		return fmt.Errorf("failed to create default repository category: %w", err)
	}
	return nil
}

func (s *SQLiteStore) ensureDefaultProject() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO projects (id, domain_id, category_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, DefaultProjectID, DefaultDomainID, DefaultRepositoryCategoryID, "Unassigned", now, now); err != nil {
		return fmt.Errorf("failed to create default project: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE projects SET domain_id = ? WHERE domain_id IS NULL OR domain_id = ''`, DefaultDomainID); err != nil {
		return fmt.Errorf("failed to backfill project domain ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE projects SET category_id = ? WHERE category_id IS NULL OR category_id = ''`, DefaultRepositoryCategoryID); err != nil {
		return fmt.Errorf("failed to backfill project category ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE scripts SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill script project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE applications SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill application project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill test project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE shared_data_pools SET project_id = ? WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill data pool project ids: %w", err)
	}
	return nil
}

func legacyScenarioID(name string) string {
	if strings.TrimSpace(name) == "" {
		name = "Unnamed Scenario"
	}
	sum := sha1.Sum([]byte(name))
	return "legacy-" + hex.EncodeToString(sum[:])[:12]
}

func (s *SQLiteStore) ensureLegacyApplication() error {
	now := time.Now()
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, LegacyApplicationID, DefaultProjectID, "Legacy", now, now); err != nil {
		return fmt.Errorf("failed to create legacy application: %w", err)
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO releases (id, application_id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, LegacyReleaseID, LegacyApplicationID, DefaultProjectID, "Imported Legacy", now, now); err != nil {
		return fmt.Errorf("failed to create legacy release: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE releases SET project_id = COALESCE((SELECT project_id FROM applications WHERE applications.id = releases.application_id), ?) WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill release project ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET application_id = ? WHERE application_id IS NULL OR application_id = ''`, LegacyApplicationID); err != nil {
		return fmt.Errorf("failed to backfill test application ids: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE tests SET release_id = ? WHERE release_id IS NULL OR release_id = ''`, LegacyReleaseID); err != nil {
		return fmt.Errorf("failed to backfill test release ids: %w", err)
	}
	return s.ensureLegacyScenarios()
}

func (s *SQLiteStore) ensureLegacyScenarios() error {
	rows, err := s.db.Query(`SELECT id, COALESCE(name, ''), COALESCE(scenario_mode, ''), COALESCE(shakeout_enabled, 0), COALESCE(resource_baseline_enabled, 0), duration_ns, COALESCE(groups_json, '[]'), COALESCE(runtime_settings_json, ''), COALESCE(goal_config_json, ''), start_time
		FROM tests
		WHERE scenario_id IS NULL OR scenario_id = ''
		ORDER BY CASE WHEN status = 'QUEUED' THEN 0 ELSE 1 END, start_time DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type legacyCandidate struct {
		testID           string
		name             string
		scenarioMode     string
		scriptShakeout   bool
		resourceBaseline bool
		duration         int64
		groupsJSON       string
		runtimeJSON      string
		goalJSON         string
		startTime        time.Time
	}
	seen := map[string]bool{}
	var candidates []legacyCandidate
	for rows.Next() {
		var candidate legacyCandidate
		if err := rows.Scan(&candidate.testID, &candidate.name, &candidate.scenarioMode, &candidate.scriptShakeout, &candidate.resourceBaseline, &candidate.duration, &candidate.groupsJSON, &candidate.runtimeJSON, &candidate.goalJSON, &candidate.startTime); err != nil {
			return err
		}
		if candidate.name == "" {
			candidate.name = "Unnamed Scenario"
		}
		scenarioID := legacyScenarioID(candidate.name)
		if seen[scenarioID] {
			continue
		}
		seen[scenarioID] = true
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, candidate := range candidates {
		scenarioID := legacyScenarioID(candidate.name)
		now := candidate.startTime
		if now.IsZero() {
			now = time.Now()
		}
		duration := time.Duration(candidate.duration).String()
		if candidate.duration <= 0 {
			duration = "1m"
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO scenarios (id, name, duration, scenario_mode, shakeout_enabled, resource_baseline_enabled, goal_config_json, runtime_settings_json, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, candidate.name, duration, candidate.scenarioMode, candidate.scriptShakeout, candidate.resourceBaseline, candidate.goalJSON, candidate.runtimeJSON, now, now); err != nil {
			return err
		}
		if err := s.insertLegacyScenarioScripts(scenarioID, candidate.groupsJSON); err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE tests SET scenario_id = ? WHERE name = ? AND (scenario_id IS NULL OR scenario_id = '')`, scenarioID, candidate.name); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) insertLegacyScenarioScripts(scenarioID string, groupsJSON string) error {
	var groups []struct {
		Name           string      `json:"name"`
		ScriptID       string      `json:"script_id"`
		VUsers         int         `json:"vusers"`
		RampUp         string      `json:"ramp_up"`
		RampDown       string      `json:"ramp_down"`
		Stages         []LoadStage `json:"stages"`
		Initialization string      `json:"initialization"`
		Role           string      `json:"role"`
	}
	if err := json.Unmarshal([]byte(groupsJSON), &groups); err != nil {
		return nil
	}
	for i, group := range groups {
		if group.ScriptID == "" {
			continue
		}
		stagesJSON, _ := json.Marshal(group.Stages)
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, initialization, role)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, i, group.Name, group.ScriptID, group.VUsers, group.RampUp, group.RampDown, string(stagesJSON), group.Initialization, group.Role); err != nil {
			return err
		}
	}
	return nil
}
