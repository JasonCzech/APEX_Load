package storage

// SQLite store — schema. Split from sqlite.go (same package, behavior-preserving).

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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
			materialization_key TEXT,
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
			revision INTEGER NOT NULL DEFAULT 1,
			transition_token TEXT,
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
			generation INTEGER NOT NULL DEFAULT 1 CHECK (generation > 0),
			acquired_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deployment_leases (
			name TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			token_hash TEXT NOT NULL,
			acquired_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			context_json TEXT NOT NULL DEFAULT '{}'
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
		`CREATE TABLE IF NOT EXISTS project_secrets (
			project_id TEXT NOT NULL REFERENCES projects(id),
			secret_name TEXT NOT NULL,
			provider TEXT NOT NULL,
			ciphertext TEXT NOT NULL,
			wrapped_key_json TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			created_by TEXT,
			PRIMARY KEY(project_id, secret_name)
		)`,
		`CREATE TABLE IF NOT EXISTS project_secret_provider_metadata (
			project_id TEXT NOT NULL REFERENCES projects(id),
			secret_name TEXT NOT NULL,
			provider TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			created_by TEXT,
			PRIMARY KEY(project_id, secret_name)
		)`,
		`CREATE TABLE IF NOT EXISTS protected_source_secret_sessions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			actor_subject TEXT NOT NULL,
			capacity_slot INTEGER NOT NULL UNIQUE CHECK(capacity_slot >= 0 AND capacity_slot < 128),
			ciphertext TEXT NOT NULL,
			wrapped_key_json TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			claim_token TEXT,
			claim_expires_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS runtime_secret_provisioning_attempts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			resource_type TEXT NOT NULL DEFAULT 'test',
			test_id TEXT NOT NULL UNIQUE,
			materialization_key TEXT NOT NULL DEFAULT '',
			ref_key TEXT NOT NULL DEFAULT '',
			commit_fingerprint TEXT NOT NULL DEFAULT '',
			actor_subject TEXT NOT NULL UNIQUE,
			audit_actor_type TEXT NOT NULL DEFAULT 'system',
			audit_actor_subject TEXT NOT NULL DEFAULT 'controller:test-creation',
			audit_actor_display_name TEXT NOT NULL DEFAULT '',
			secret_names_json TEXT NOT NULL,
			lease_expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL
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
			project_id TEXT REFERENCES projects(id),
			folder_id TEXT REFERENCES folders(id),
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
		`CREATE TABLE IF NOT EXISTS bruno_source_artifacts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			artifact_kind TEXT NOT NULL,
			backend_key TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			manifest_sha256 TEXT,
			source_format TEXT,
			encryption_json TEXT NOT NULL,
			lifecycle_state TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			UNIQUE(project_id, artifact_kind, sha256)
		)`,
		`CREATE TABLE IF NOT EXISTS bruno_source_revisions (
			id TEXT PRIMARY KEY,
			script_id TEXT NOT NULL REFERENCES scripts(id),
			project_id TEXT NOT NULL REFERENCES projects(id),
			parent_revision_id TEXT REFERENCES bruno_source_revisions(id),
			active_flag INTEGER NOT NULL DEFAULT 0,
			raw_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
			sanitized_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
			source_format TEXT NOT NULL,
			compatibility_version TEXT NOT NULL,
			identity_map_json TEXT NOT NULL,
			import_options_json TEXT NOT NULL,
			dependency_snapshot_ref TEXT,
			security_profile_json TEXT NOT NULL,
			change_summary_json TEXT NOT NULL,
			secret_bindings_json TEXT NOT NULL,
			canonical_script_json TEXT NOT NULL,
			created_by TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS tests (
			id TEXT PRIMARY KEY,
			ref_key TEXT,
			name TEXT,
			status TEXT,
			application_id TEXT REFERENCES applications(id),
			release_id TEXT REFERENCES releases(id),
			scenario_id TEXT REFERENCES scenarios(id),
			project_id TEXT REFERENCES projects(id),
			folder_id TEXT REFERENCES folders(id),
			scenario_mode TEXT,
			shakeout_enabled INTEGER DEFAULT 0,
			resource_baseline_enabled INTEGER DEFAULT 0,
			start_delay TEXT,
			assignment_json TEXT,
			total_vusers INTEGER,
			duration_ns INTEGER,
			start_time DATETIME,
			scheduled_start_at DATETIME,
			pending_since DATETIME,
			paused_at DATETIME,
			cumulative_pause_ns INTEGER NOT NULL DEFAULT 0,
			capacity_window_start_at DATETIME,
			capacity_window_end_at DATETIME,
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
		// Archive payloads live outside the relational store, so their deletion
		// cannot participate in the test/project transaction. Persist the complete
		// cleanup intent before removing run_archives/tests; a controller restart can
		// then retry physical cleanup without needing metadata that no longer exists.
		`CREATE TABLE IF NOT EXISTS archive_cleanup_jobs (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			archive_record_json TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// Immutable object publications are replaced, never overwritten. Retain
		// every superseded pointer independently until physical deletion succeeds.
		`CREATE TABLE IF NOT EXISTS archive_version_cleanup_jobs (
			id TEXT PRIMARY KEY,
			test_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			archive_record_json TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS archive_finalization_jobs (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			archive_record_json TEXT NOT NULL,
			persist_artifacts INTEGER NOT NULL DEFAULT 1,
			generation INTEGER NOT NULL DEFAULT 1,
			intent_token TEXT NOT NULL DEFAULT '',
			refresh_token TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS archive_refresh_jobs (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			generation INTEGER NOT NULL DEFAULT 1,
			intent_token TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
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
		`CREATE TABLE IF NOT EXISTS github_connection_revocations (
			user_id TEXT PRIMARY KEY,
			revoked_at DATETIME
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
			kind TEXT,
			test_id TEXT,
			script_id TEXT,
			owner TEXT,
			repo TEXT,
			commit_sha TEXT,
			check_run_id INTEGER,
			check_run_url TEXT,
			status TEXT,
			conclusion TEXT,
			pending_conclusion TEXT,
			published_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS github_check_intents (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			installation_id INTEGER NOT NULL,
			owner TEXT NOT NULL,
			repo TEXT NOT NULL,
			commit_sha TEXT NOT NULL,
			creation_claim TEXT,
			creation_claim_expires_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id)
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
			correlation_observations_json TEXT,
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
		`CREATE TABLE IF NOT EXISTS capacity_approval_locks (
			lock_key TEXT PRIMARY KEY,
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
			revision INTEGER NOT NULL DEFAULT 1,
			attempt_token TEXT,
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
			revision INTEGER NOT NULL DEFAULT 1,
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
			transition_token TEXT NOT NULL DEFAULT '',
			revision INTEGER NOT NULL DEFAULT 1,
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
			actor_type TEXT NOT NULL DEFAULT 'system',
			actor_subject TEXT NOT NULL DEFAULT 'controller:execution-plan-schedule',
			actor_display_name TEXT NOT NULL DEFAULT '',
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
			revision INTEGER NOT NULL DEFAULT 1,
			transition_token TEXT NOT NULL DEFAULT '',
			planned_at DATETIME NOT NULL,
			materialized_at DATETIME,
			status TEXT NOT NULL,
			reason_code TEXT,
			run_id TEXT,
			conflict_summary TEXT,
			actor_type TEXT NOT NULL DEFAULT 'system',
			actor_subject TEXT NOT NULL DEFAULT 'controller:execution-plan-schedule-occurrence',
			actor_display_name TEXT NOT NULL DEFAULT '',
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
		// The `enabled INTEGER DEFAULT 1` fragment is written byte-for-byte to match a
		// rewriteSQL boolean-column entry (see sqlite.go), so Postgres gets BOOLEAN with
		// zero new rewrite rules; TestPostgresSchemaBooleanColumnsRewriteToBoolean guards
		// this. Any deviation silently breaks Postgres.
		`CREATE TABLE IF NOT EXISTS grafana_dashboards (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			source_title TEXT,
			source_uid TEXT,
			source_sha256 TEXT NOT NULL,
			schema_version INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER DEFAULT 1,
			panels_json TEXT NOT NULL,
			bindings_json TEXT,
			diagnostics_json TEXT,
			created_by TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS grafana_import_sessions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT,
			content TEXT NOT NULL,
			source_sha256 TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			claim_token TEXT,
			claimed_at DATETIME,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS grafana_dashboard_snapshots (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			captured_at DATETIME NOT NULL,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
	}
}

// schemaColumnMigrations returns the additive ALTER TABLE ... ADD COLUMN statements
// applied on every init (idempotent via isDuplicateColumnErr). Extracted alongside
// schemaCreateTableStatements so the Postgres rewrite guard test can also cover the
// columns added here (e.g. boolean flags added to existing tables).
func schemaColumnMigrations() []optionalSchemaMigration {
	return []optionalSchemaMigration{
		{name: "scheduler_leases.generation", query: `ALTER TABLE scheduler_leases ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`},
		{name: "scenario_schedule_occurrences.revision", query: `ALTER TABLE scenario_schedule_occurrences ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "scenario_schedule_occurrences.transition_token", query: `ALTER TABLE scenario_schedule_occurrences ADD COLUMN transition_token TEXT`},
		{name: "users.session_epoch", query: `ALTER TABLE users ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0`},
		{name: "tests.runtime_settings_json", query: `ALTER TABLE tests ADD COLUMN runtime_settings_json TEXT`},
		{name: "tests.materialization_key", query: `ALTER TABLE tests ADD COLUMN materialization_key TEXT`},
		{name: "tests.paused_at", query: `ALTER TABLE tests ADD COLUMN paused_at DATETIME`},
		{name: "tests.cumulative_pause_ns", query: `ALTER TABLE tests ADD COLUMN cumulative_pause_ns INTEGER NOT NULL DEFAULT 0`},
		{name: "tests.capacity_window_start_at", query: `ALTER TABLE tests ADD COLUMN capacity_window_start_at DATETIME`},
		{name: "tests.capacity_window_end_at", query: `ALTER TABLE tests ADD COLUMN capacity_window_end_at DATETIME`},
		{name: "tests.scenario_mode", query: `ALTER TABLE tests ADD COLUMN scenario_mode TEXT`},
		{name: "tests.goal_config_json", query: `ALTER TABLE tests ADD COLUMN goal_config_json TEXT`},
		{name: "tests.timeslot_id", query: `ALTER TABLE tests ADD COLUMN timeslot_id TEXT`},
		{name: "tests.shakeout_enabled", query: `ALTER TABLE tests ADD COLUMN shakeout_enabled INTEGER DEFAULT 0`},
		{name: "tests.resource_baseline_enabled", query: `ALTER TABLE tests ADD COLUMN resource_baseline_enabled INTEGER DEFAULT 0`},
		{name: "tests.start_delay", query: `ALTER TABLE tests ADD COLUMN start_delay TEXT`},
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
		{name: "script_recordings.correlation_observations_json", query: `ALTER TABLE script_recordings ADD COLUMN correlation_observations_json TEXT`},
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
		{name: "archive_finalization_jobs.generation", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`},
		{name: "archive_finalization_jobs.intent_token", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN intent_token TEXT NOT NULL DEFAULT ''`},
		{name: "archive_finalization_jobs.refresh_token", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN refresh_token TEXT NOT NULL DEFAULT ''`},
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
		{name: "grafana_import_sessions.source_sha256", query: `ALTER TABLE grafana_import_sessions ADD COLUMN source_sha256 TEXT NOT NULL DEFAULT ''`},
		{name: "github_script_links.remote_blob_sha", query: `ALTER TABLE github_script_links ADD COLUMN remote_blob_sha TEXT`},
		{name: "github_script_links.remote_commit_sha", query: `ALTER TABLE github_script_links ADD COLUMN remote_commit_sha TEXT`},
		{name: "github_check_links.pending_conclusion", query: `ALTER TABLE github_check_links ADD COLUMN pending_conclusion TEXT`},
		{name: "github_check_links.kind", query: `ALTER TABLE github_check_links ADD COLUMN kind TEXT`},
		{name: "github_check_intents.creation_claim", query: `ALTER TABLE github_check_intents ADD COLUMN creation_claim TEXT`},
		{name: "github_check_intents.creation_claim_expires_at", query: `ALTER TABLE github_check_intents ADD COLUMN creation_claim_expires_at DATETIME`},
		{name: "execution_plan_approvals.plan_content_hash", query: `ALTER TABLE execution_plan_approvals ADD COLUMN plan_content_hash TEXT`},
		{name: "execution_plans.revision", query: `ALTER TABLE execution_plans ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "execution_plan_runs.revision", query: `ALTER TABLE execution_plan_runs ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "execution_plan_runs.transition_token", query: `ALTER TABLE execution_plan_runs ADD COLUMN transition_token TEXT NOT NULL DEFAULT ''`},
		{name: "execution_plan_schedule_occurrences.revision", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "execution_plan_schedule_occurrences.transition_token", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN transition_token TEXT NOT NULL DEFAULT ''`},
		{name: "execution_plan_schedule_occurrences.actor_type", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'legacy'`},
		{name: "execution_plan_schedule_occurrences.actor_subject", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN actor_subject TEXT NOT NULL DEFAULT 'controller:execution-plan-schedule-occurrence-legacy'`},
		{name: "execution_plan_schedule_occurrences.actor_display_name", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN actor_display_name TEXT NOT NULL DEFAULT ''`},
		{name: "execution_plan_schedules.actor_type", query: `ALTER TABLE execution_plan_schedules ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'legacy'`},
		{name: "execution_plan_schedules.actor_subject", query: `ALTER TABLE execution_plan_schedules ADD COLUMN actor_subject TEXT NOT NULL DEFAULT 'controller:execution-plan-schedule-legacy'`},
		{name: "execution_plan_schedules.actor_display_name", query: `ALTER TABLE execution_plan_schedules ADD COLUMN actor_display_name TEXT NOT NULL DEFAULT ''`},
		{name: "provisioning_requests.revision", query: `ALTER TABLE provisioning_requests ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "provisioning_requests.attempt_token", query: `ALTER TABLE provisioning_requests ADD COLUMN attempt_token TEXT`},
		{name: "protected_source_secret_sessions.claim_token", query: `ALTER TABLE protected_source_secret_sessions ADD COLUMN claim_token TEXT`},
		{name: "protected_source_secret_sessions.claim_expires_at", query: `ALTER TABLE protected_source_secret_sessions ADD COLUMN claim_expires_at DATETIME`},
		{name: "runtime_secret_provisioning_attempts.materialization_key", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN materialization_key TEXT NOT NULL DEFAULT ''`},
		{name: "runtime_secret_provisioning_attempts.ref_key", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN ref_key TEXT NOT NULL DEFAULT ''`},
		{name: "runtime_secret_provisioning_attempts.resource_type", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN resource_type TEXT NOT NULL DEFAULT 'test'`},
		{name: "runtime_secret_provisioning_attempts.commit_fingerprint", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN commit_fingerprint TEXT NOT NULL DEFAULT ''`},
		{name: "runtime_secret_provisioning_attempts.audit_actor_type", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN audit_actor_type TEXT NOT NULL DEFAULT 'system'`},
		{name: "runtime_secret_provisioning_attempts.audit_actor_subject", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN audit_actor_subject TEXT NOT NULL DEFAULT 'controller:test-creation'`},
		{name: "runtime_secret_provisioning_attempts.audit_actor_display_name", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN audit_actor_display_name TEXT NOT NULL DEFAULT ''`},
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
	{`CREATE INDEX IF NOT EXISTS idx_archive_cleanup_jobs_updated ON archive_cleanup_jobs(updated_at)`, "idx_archive_cleanup_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_version_cleanup_jobs_updated ON archive_version_cleanup_jobs(updated_at)`, "idx_archive_version_cleanup_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_version_cleanup_jobs_test ON archive_version_cleanup_jobs(test_id)`, "idx_archive_version_cleanup_jobs_test"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_finalization_jobs_updated ON archive_finalization_jobs(updated_at)`, "idx_archive_finalization_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_refresh_jobs_updated ON archive_refresh_jobs(updated_at)`, "idx_archive_refresh_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_test_notes_test_id ON test_notes(test_id)`, "idx_test_notes_test_id"},
	{`CREATE INDEX IF NOT EXISTS idx_test_history_test_time ON test_history(test_id, timestamp)`, "idx_test_history_test_time"},
	{`CREATE INDEX IF NOT EXISTS idx_test_interval_summaries_test_time ON test_interval_summaries(test_id, timestamp)`, "idx_test_interval_summaries_test_time"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_schedule_time ON scenario_schedule_occurrences(schedule_id, planned_at)`, "idx_scenario_schedule_occurrences_schedule_time"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_project_time ON scenario_schedule_occurrences(project_id, planned_at)`, "idx_scenario_schedule_occurrences_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_status ON scenario_schedule_occurrences(status)`, "idx_scenario_schedule_occurrences_status"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_schedule_occurrences_schedule_time ON execution_plan_schedule_occurrences(schedule_id, planned_at)`, "idx_execution_plan_schedule_occurrences_schedule_time"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_schedule_occurrences_project_time ON execution_plan_schedule_occurrences(project_id, planned_at)`, "idx_execution_plan_schedule_occurrences_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_capacity_reservations_project_time ON capacity_reservations(project_id, start_at, end_at)`, "idx_capacity_reservations_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_capacity_reservations_bound_test ON capacity_reservations(bound_test_id)`, "idx_capacity_reservations_bound_test"},
	{`CREATE INDEX IF NOT EXISTS idx_tests_timeslot ON tests(timeslot_id)`, "idx_tests_timeslot"},
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
	{`CREATE INDEX IF NOT EXISTS idx_grafana_dashboards_project ON grafana_dashboards(project_id)`, "idx_grafana_dashboards_project"},
	{`CREATE INDEX IF NOT EXISTS idx_grafana_import_sessions_project_expiry ON grafana_import_sessions(project_id, expires_at)`, "idx_grafana_import_sessions_project_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_grafana_dashboard_snapshots_project ON grafana_dashboard_snapshots(project_id)`, "idx_grafana_dashboard_snapshots_project"},
	{`CREATE INDEX IF NOT EXISTS idx_reusable_blocks_project ON reusable_blocks(project_id)`, "idx_reusable_blocks_project"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_artifacts_project_hash ON bruno_source_artifacts(project_id, artifact_kind, sha256)`, "idx_bruno_artifacts_project_hash"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_revisions_script_created ON bruno_source_revisions(script_id, created_at)`, "idx_bruno_revisions_script_created"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_revisions_project_active ON bruno_source_revisions(project_id, active_flag)`, "idx_bruno_revisions_project_active"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_bruno_revisions_one_active ON bruno_source_revisions(script_id, project_id) WHERE active_flag = 1`, "idx_bruno_revisions_one_active"},
	{`CREATE INDEX IF NOT EXISTS idx_project_secrets_project_updated ON project_secrets(project_id, updated_at)`, "idx_project_secrets_project_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_project_secret_provider_metadata_project_updated ON project_secret_provider_metadata(project_id, updated_at)`, "idx_project_secret_provider_metadata_project_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_protected_source_secret_sessions_expiry ON protected_source_secret_sessions(expires_at)`, "idx_protected_source_secret_sessions_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_protected_source_secret_sessions_project ON protected_source_secret_sessions(project_id)`, "idx_protected_source_secret_sessions_project"},
	{`CREATE INDEX IF NOT EXISTS idx_runtime_secret_attempts_expiry ON runtime_secret_provisioning_attempts(lease_expires_at)`, "idx_runtime_secret_attempts_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_runtime_secret_attempts_project ON runtime_secret_provisioning_attempts(project_id)`, "idx_runtime_secret_attempts_project"},
	{`CREATE INDEX IF NOT EXISTS idx_github_check_intents_updated ON github_check_intents(updated_at)`, "idx_github_check_intents_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_github_check_intents_project ON github_check_intents(project_id)`, "idx_github_check_intents_project"},
	{`CREATE INDEX IF NOT EXISTS idx_github_check_links_test ON github_check_links(test_id)`, "idx_github_check_links_test"},
	{`CREATE INDEX IF NOT EXISTS idx_github_check_links_reconcile ON github_check_links(kind, updated_at)`, "idx_github_check_links_reconcile"},
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
	if s.db.dialect == storeDialectPostgres {
		if _, err := s.db.Exec(`ALTER TABLE bruno_source_artifacts ALTER COLUMN size_bytes TYPE BIGINT`); err != nil {
			return fmt.Errorf("migrate Bruno artifact size to BIGINT: %w", err)
		}
	}

	if err := s.applyOptionalSchemaMigrations(schemaColumnMigrations()); err != nil {
		return err
	}
	if err := s.backfillArchiveFinalizationIntentTokens(); err != nil {
		return err
	}
	if err := s.backfillGitHubCheckLinkKinds(); err != nil {
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
	if err := s.ensurePostgresForeignKeys(); err != nil {
		return err
	}
	return s.applyTimescaleDB()
}

func (s *SQLiteStore) backfillArchiveFinalizationIntentTokens() error {
	rows, err := s.db.Query(`SELECT test_id FROM archive_finalization_jobs WHERE intent_token IS NULL OR TRIM(intent_token) = ''`)
	if err != nil {
		return fmt.Errorf("list legacy archive finalization intents: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE archive_finalization_jobs SET intent_token = ?, updated_at = ?
			WHERE test_id = ? AND (intent_token IS NULL OR TRIM(intent_token) = '')`, uuid.NewString(), time.Now().UTC(), id); err != nil {
			return fmt.Errorf("upgrade legacy archive finalization intent %s: %w", id, err)
		}
	}
	return nil
}

func (s *SQLiteStore) backfillGitHubCheckLinkKinds() error {
	rows, err := s.db.Query(`SELECT l.id, COALESCE(l.test_id, '')
		FROM github_check_links l
		JOIN tests t ON t.id = l.test_id
		WHERE (l.kind IS NULL OR TRIM(l.kind) = '')
			AND t.materialization_key LIKE 'ci:v1:%'`)
	if err != nil {
		return fmt.Errorf("list legacy github check link kinds: %w", err)
	}
	ciLinkIDs := make([]string, 0)
	for rows.Next() {
		var id, testID string
		if err := rows.Scan(&id, &testID); err != nil {
			rows.Close()
			return fmt.Errorf("scan legacy github check link kind: %w", err)
		}
		expectedID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload-ci-check:"+testID)).String()
		if id == expectedID {
			ciLinkIDs = append(ciLinkIDs, id)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list legacy github check link kinds: %w", err)
	}
	rows.Close()
	if _, err := s.db.Exec(`UPDATE github_check_links SET kind = 'manual' WHERE kind IS NULL OR TRIM(kind) = ''`); err != nil {
		return fmt.Errorf("backfill manual github check link kinds: %w", err)
	}
	for _, id := range ciLinkIDs {
		if _, err := s.db.Exec(`UPDATE github_check_links SET kind = 'ci' WHERE id = ? AND kind = 'manual'`, id); err != nil {
			return fmt.Errorf("backfill CI github check link kind %s: %w", id, err)
		}
	}
	return nil
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

func legacyScenarioID(projectID, name string) string {
	projectID = normalizeProjectID(projectID)
	if strings.TrimSpace(name) == "" {
		name = "Unnamed Scenario"
	}
	sum := sha1.Sum([]byte(projectID + "\x00" + name))
	return "legacy-" + hex.EncodeToString(sum[:])[:12]
}

// legacyGlobalScenarioID reproduces the identifier generated by releases that
// predated project-scoped legacy scenarios. It is intentionally retained as a
// migration-only helper so databases initialized by those releases can be
// repaired without guessing from a legacy- prefix alone.
func legacyGlobalScenarioID(name string) string {
	if strings.TrimSpace(name) == "" {
		name = "Unnamed Scenario"
	}
	sum := sha1.Sum([]byte(name))
	return "legacy-" + hex.EncodeToString(sum[:])[:12]
}

func legacyApplicationID(projectID string) string {
	projectID = normalizeProjectID(projectID)
	if projectID == DefaultProjectID {
		return LegacyApplicationID
	}
	sum := sha1.Sum([]byte(projectID))
	return LegacyApplicationID + "-" + hex.EncodeToString(sum[:])[:12]
}

func legacyReleaseID(projectID string) string {
	projectID = normalizeProjectID(projectID)
	if projectID == DefaultProjectID {
		return LegacyReleaseID
	}
	sum := sha1.Sum([]byte(projectID))
	return LegacyReleaseID + "-" + hex.EncodeToString(sum[:])[:12]
}

func (s *SQLiteStore) ensureLegacyApplication() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	projectRows, err := tx.Query(`SELECT id FROM projects ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list projects for legacy hierarchy: %w", err)
	}
	var projectIDs []string
	for projectRows.Next() {
		var projectID string
		if err := projectRows.Scan(&projectID); err != nil {
			projectRows.Close()
			return fmt.Errorf("scan project for legacy hierarchy: %w", err)
		}
		projectIDs = append(projectIDs, normalizeProjectID(projectID))
	}
	if err := projectRows.Err(); err != nil {
		projectRows.Close()
		return fmt.Errorf("list projects for legacy hierarchy: %w", err)
	}
	projectRows.Close()

	for _, projectID := range projectIDs {
		applicationID := legacyApplicationID(projectID)
		releaseID := legacyReleaseID(projectID)
		if _, err := tx.Exec(`INSERT OR IGNORE INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, applicationID, projectID, "Legacy", now, now); err != nil {
			return fmt.Errorf("create legacy application for project %s: %w", projectID, err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO releases (id, application_id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, releaseID, applicationID, projectID, "Imported Legacy", now, now); err != nil {
			return fmt.Errorf("create legacy release for project %s: %w", projectID, err)
		}
		if _, err := tx.Exec(`UPDATE tests SET application_id = ?
			WHERE project_id = ? AND (application_id IS NULL OR application_id = '' OR (application_id = ? AND ? <> ?))`,
			applicationID, projectID, LegacyApplicationID, projectID, DefaultProjectID); err != nil {
			return fmt.Errorf("backfill test application ids for project %s: %w", projectID, err)
		}
		if _, err := tx.Exec(`UPDATE tests SET release_id = ?
			WHERE project_id = ? AND (release_id IS NULL OR release_id = '' OR (release_id = ? AND ? <> ?))`,
			releaseID, projectID, LegacyReleaseID, projectID, DefaultProjectID); err != nil {
			return fmt.Errorf("backfill test release ids for project %s: %w", projectID, err)
		}
	}
	if _, err := tx.Exec(`UPDATE releases SET project_id = COALESCE((SELECT project_id FROM applications WHERE applications.id = releases.application_id), ?) WHERE project_id IS NULL OR project_id = ''`, DefaultProjectID); err != nil {
		return fmt.Errorf("failed to backfill release project ids: %w", err)
	}
	if err := s.ensureLegacyScenariosTx(tx); err != nil {
		return err
	}
	return tx.Commit()
}

type legacyScenarioCandidate struct {
	testID             string
	projectID          string
	name               string
	scenarioMode       string
	scriptShakeout     bool
	resourceBaseline   bool
	duration           int64
	groupsJSON         string
	runtimeJSON        string
	goalJSON           string
	startTime          time.Time
	existingScenarioID string
}

func (s *SQLiteStore) ensureLegacyScenariosTx(tx *storeTx) error {
	rows, err := tx.Query(`SELECT test_row.id, COALESCE(test_row.project_id, ?),
			COALESCE(NULLIF(scenario_row.name, ''), test_row.name, ''),
			COALESCE(test_row.scenario_mode, ''), COALESCE(test_row.shakeout_enabled, 0),
			COALESCE(test_row.resource_baseline_enabled, 0), test_row.duration_ns,
			COALESCE(test_row.groups_json, '[]'), COALESCE(test_row.runtime_settings_json, ''),
			COALESCE(test_row.goal_config_json, ''), test_row.start_time,
			COALESCE(test_row.scenario_id, '')
		FROM tests AS test_row
		LEFT JOIN scenarios AS scenario_row ON scenario_row.id = test_row.scenario_id
		WHERE test_row.scenario_id IS NULL OR test_row.scenario_id = '' OR test_row.scenario_id LIKE 'legacy-%'
		ORDER BY CASE WHEN test_row.status = 'QUEUED' THEN 0 ELSE 1 END, test_row.start_time DESC`, DefaultProjectID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var candidates []legacyScenarioCandidate
	for rows.Next() {
		var candidate legacyScenarioCandidate
		if err := rows.Scan(&candidate.testID, &candidate.projectID, &candidate.name, &candidate.scenarioMode, &candidate.scriptShakeout, &candidate.resourceBaseline, &candidate.duration, &candidate.groupsJSON, &candidate.runtimeJSON, &candidate.goalJSON, &candidate.startTime, &candidate.existingScenarioID); err != nil {
			return err
		}
		candidate.projectID = normalizeProjectID(candidate.projectID)
		if candidate.name == "" {
			candidate.name = "Unnamed Scenario"
		}
		targetScenarioID := legacyScenarioID(candidate.projectID, candidate.name)
		if candidate.existingScenarioID != "" {
			// A matching project-scoped identifier is already repaired. A reserved
			// legacy identifier that no longer matches can be either the released
			// global form or a scoped scenario renamed after migration; both need
			// one deterministic re-key to the current project/name tuple.
			if candidate.existingScenarioID == targetScenarioID {
				continue
			}
			if !strings.HasPrefix(candidate.existingScenarioID, "legacy-") {
				continue
			}
		}
		if err := validateLegacyScenarioJSON(candidate); err != nil {
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	migratedScenarios := make(map[string]struct{}, len(candidates))
	migratedReferences := make(map[string]struct{}, len(candidates))
	oldScenarioIDs := make(map[string]struct{}, len(candidates))
	legacyRefKeys := make(map[string]string, len(candidates))
	legacyRefKeysTransferred := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		scenarioID := legacyScenarioID(candidate.projectID, candidate.name)
		if candidate.existingScenarioID != "" {
			if _, loaded := legacyRefKeys[candidate.existingScenarioID]; !loaded {
				var legacyRefKey string
				if err := tx.QueryRow(`SELECT COALESCE(ref_key, '') FROM scenarios WHERE id = ?`, candidate.existingScenarioID).Scan(&legacyRefKey); err != nil {
					return fmt.Errorf("read legacy scenario %s reference key: %w", candidate.existingScenarioID, err)
				}
				legacyRefKeys[candidate.existingScenarioID] = legacyRefKey
			}
		}
		if _, migrated := migratedScenarios[scenarioID]; !migrated {
			now := candidate.startTime
			if now.IsZero() {
				now = time.Now()
			}
			duration := time.Duration(candidate.duration).String()
			if candidate.duration <= 0 {
				duration = "1m"
			}
			if candidate.existingScenarioID != "" {
				// Preserve edits made to the globally migrated scenario after its
				// original import. The test snapshot remains the source for scripts,
				// because the old global migration selected scripts from only one
				// project when same-named tests existed in several projects.
				if _, err := tx.Exec(`INSERT OR IGNORE INTO scenarios
					(id, ref_key, name, duration, start_delay, scenario_mode, shakeout_enabled,
					 resource_baseline_enabled, assignment_json, goal_config_json,
					 runtime_settings_json, created_at, updated_at)
					SELECT ?, NULL, name, duration, start_delay, scenario_mode, shakeout_enabled,
						resource_baseline_enabled, assignment_json, goal_config_json,
						runtime_settings_json, created_at, updated_at
					FROM scenarios WHERE id = ?`, scenarioID, candidate.existingScenarioID); err != nil {
					return fmt.Errorf("clone legacy scenario %s for project %s: %w", candidate.existingScenarioID, candidate.projectID, err)
				}
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO scenarios (id, name, duration, scenario_mode, shakeout_enabled, resource_baseline_enabled, goal_config_json, runtime_settings_json, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, candidate.name, duration, candidate.scenarioMode, candidate.scriptShakeout, candidate.resourceBaseline, candidate.goalJSON, candidate.runtimeJSON, now, now); err != nil {
				return err
			}
			if err := insertLegacyScenarioScriptsTx(tx, scenarioID, candidate.testID, candidate.groupsJSON); err != nil {
				return err
			}
			migratedScenarios[scenarioID] = struct{}{}
		}
		if candidate.existingScenarioID != "" && !legacyRefKeysTransferred[candidate.existingScenarioID] {
			legacyRefKey := legacyRefKeys[candidate.existingScenarioID]
			if legacyRefKey != "" {
				var targetRefKey string
				if err := tx.QueryRow(`SELECT COALESCE(ref_key, '') FROM scenarios WHERE id = ?`, scenarioID).Scan(&targetRefKey); err != nil {
					return fmt.Errorf("read migrated scenario %s reference key: %w", scenarioID, err)
				}
				if targetRefKey == legacyRefKey {
					legacyRefKeysTransferred[candidate.existingScenarioID] = true
				} else if targetRefKey == "" {
					if _, err := tx.Exec(`UPDATE scenarios SET ref_key = NULL WHERE id = ?`, candidate.existingScenarioID); err != nil {
						return fmt.Errorf("release legacy scenario %s reference key: %w", candidate.existingScenarioID, err)
					}
					if _, err := tx.Exec(`UPDATE scenarios SET ref_key = ? WHERE id = ? AND (ref_key IS NULL OR ref_key = '')`, legacyRefKey, scenarioID); err != nil {
						return fmt.Errorf("transfer legacy scenario %s reference key: %w", scenarioID, err)
					}
					legacyRefKeysTransferred[candidate.existingScenarioID] = true
				}
			}
		}
		if _, err := tx.Exec(`UPDATE tests SET scenario_id = ?
			WHERE id = ? AND project_id = ?
				AND (scenario_id IS NULL OR scenario_id = '' OR scenario_id = ?)`,
			scenarioID, candidate.testID, candidate.projectID, candidate.existingScenarioID); err != nil {
			return err
		}
		if candidate.existingScenarioID == "" {
			continue
		}
		keepLegacyRefOwner := legacyRefKeys[candidate.existingScenarioID] != "" && !legacyRefKeysTransferred[candidate.existingScenarioID]
		if !keepLegacyRefOwner {
			oldScenarioIDs[candidate.existingScenarioID] = struct{}{}
		} else {
			// Keep the old row as the stable ref-key owner when the scoped target
			// already has a different key; deleting it would make the public key
			// unreachable.
		}
		referenceKey := candidate.existingScenarioID + "\x00" + scenarioID + "\x00" + candidate.projectID
		if _, migrated := migratedReferences[referenceKey]; migrated {
			continue
		}
		if err := migrateLegacyScenarioReferencesTx(tx, candidate.existingScenarioID, scenarioID, candidate.projectID); err != nil {
			return err
		}
		migratedReferences[referenceKey] = struct{}{}
	}
	for oldScenarioID := range oldScenarioIDs {
		if err := deleteLegacyScenarioIfUnreferencedTx(tx, oldScenarioID); err != nil {
			return err
		}
	}
	return nil
}

func validateLegacyScenarioJSON(candidate legacyScenarioCandidate) error {
	for field, payload := range map[string]string{
		"groups_json":           candidate.groupsJSON,
		"runtime_settings_json": candidate.runtimeJSON,
		"goal_config_json":      candidate.goalJSON,
	} {
		if strings.TrimSpace(payload) == "" {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
			return fmt.Errorf("decode legacy test %s %s: %w", candidate.testID, field, err)
		}
	}
	return nil
}

func migrateLegacyScenarioReferencesTx(tx *storeTx, oldScenarioID, scenarioID, projectID string) error {
	projectID = normalizeProjectID(projectID)
	updates := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name:  "scenario schedules",
			query: `UPDATE scenario_schedules SET scenario_id = ? WHERE scenario_id = ? AND COALESCE(NULLIF(project_id, ''), ?) = ?`,
			args:  []any{scenarioID, oldScenarioID, DefaultProjectID, projectID},
		},
		{
			name: "scenario schedule occurrences",
			query: `UPDATE scenario_schedule_occurrences SET scenario_id = ?
				WHERE scenario_id = ? AND (
					COALESCE(NULLIF(project_id, ''), ?) = ? OR
					schedule_id IN (
						SELECT id FROM scenario_schedules
						WHERE scenario_id = ? AND COALESCE(NULLIF(project_id, ''), ?) = ?
					)
				)`,
			args: []any{scenarioID, oldScenarioID, DefaultProjectID, projectID, scenarioID, DefaultProjectID, projectID},
		},
		{
			name: "scenario schedule approvals",
			query: `UPDATE scenario_schedule_approvals SET scenario_id = ?
				WHERE scenario_id = ? AND (
					COALESCE(NULLIF(project_id, ''), ?) = ? OR
					schedule_id IN (
						SELECT id FROM scenario_schedules
						WHERE scenario_id = ? AND COALESCE(NULLIF(project_id, ''), ?) = ?
					)
				)`,
			args: []any{scenarioID, oldScenarioID, DefaultProjectID, projectID, scenarioID, DefaultProjectID, projectID},
		},
		{
			name:  "capacity reservations",
			query: `UPDATE capacity_reservations SET scenario_id = ? WHERE scenario_id = ? AND COALESCE(NULLIF(project_id, ''), ?) = ?`,
			args:  []any{scenarioID, oldScenarioID, DefaultProjectID, projectID},
		},
		{
			name:  "external result imports",
			query: `UPDATE external_result_imports SET scenario_id = ? WHERE scenario_id = ? AND COALESCE(NULLIF(project_id, ''), ?) = ?`,
			args:  []any{scenarioID, oldScenarioID, DefaultProjectID, projectID},
		},
	}
	for _, update := range updates {
		if _, err := tx.Exec(update.query, update.args...); err != nil {
			return fmt.Errorf("migrate legacy %s from scenario %s for project %s: %w", update.name, oldScenarioID, projectID, err)
		}
	}

	// Bindings do not carry project_id, so ownership is derived from the bound
	// pool. Remove a duplicate alias first when an earlier interrupted repair
	// already created the target binding, then move the remaining rows intact.
	if _, err := tx.Exec(`DELETE FROM scenario_data_pool_bindings AS old_binding
		WHERE old_binding.scenario_id = ?
			AND old_binding.pool_id IN (
				SELECT id FROM shared_data_pools WHERE COALESCE(NULLIF(project_id, ''), ?) = ?
			)
			AND EXISTS (
				SELECT 1 FROM scenario_data_pool_bindings AS target_binding
				WHERE target_binding.scenario_id = ? AND target_binding.alias = old_binding.alias
			)`, oldScenarioID, DefaultProjectID, projectID, scenarioID); err != nil {
		return fmt.Errorf("deduplicate legacy scenario data bindings for project %s: %w", projectID, err)
	}
	if _, err := tx.Exec(`UPDATE scenario_data_pool_bindings SET scenario_id = ?
		WHERE scenario_id = ? AND pool_id IN (
			SELECT id FROM shared_data_pools WHERE COALESCE(NULLIF(project_id, ''), ?) = ?
		)`, scenarioID, oldScenarioID, DefaultProjectID, projectID); err != nil {
		return fmt.Errorf("migrate legacy scenario data bindings for project %s: %w", projectID, err)
	}

	if err := migrateLegacyScenarioJSONReferencesTx(tx, oldScenarioID, scenarioID, projectID); err != nil {
		return err
	}
	return nil
}

func migrateLegacyScenarioJSONReferencesTx(tx *storeTx, oldScenarioID, scenarioID, projectID string) error {
	sources := []struct {
		name        string
		selectQuery string
		updateQuery string
	}{
		{
			name:        "execution plan",
			selectQuery: `SELECT id, COALESCE(entries_json, '') FROM execution_plans WHERE COALESCE(NULLIF(project_id, ''), ?) = ?`,
			updateQuery: `UPDATE execution_plans SET entries_json = ? WHERE id = ?`,
		},
		{
			name:        "execution plan run",
			selectQuery: `SELECT id, COALESCE(entries_json, '') FROM execution_plan_runs WHERE COALESCE(NULLIF(project_id, ''), ?) = ?`,
			updateQuery: `UPDATE execution_plan_runs SET entries_json = ? WHERE id = ?`,
		},
	}
	for _, source := range sources {
		rows, err := tx.Query(source.selectQuery, DefaultProjectID, projectID)
		if err != nil {
			return fmt.Errorf("list %s references for legacy scenario migration: %w", source.name, err)
		}
		type entryDocument struct {
			id      string
			payload string
		}
		var documents []entryDocument
		for rows.Next() {
			var document entryDocument
			if err := rows.Scan(&document.id, &document.payload); err != nil {
				rows.Close()
				return fmt.Errorf("scan %s references for legacy scenario migration: %w", source.name, err)
			}
			documents = append(documents, document)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("list %s references for legacy scenario migration: %w", source.name, err)
		}
		rows.Close()

		for _, document := range documents {
			payload, changed, err := rewriteLegacyScenarioEntries(document.payload, oldScenarioID, scenarioID)
			if err != nil {
				return fmt.Errorf("rewrite %s %s entries for legacy scenario migration: %w", source.name, document.id, err)
			}
			if !changed {
				continue
			}
			if _, err := tx.Exec(source.updateQuery, payload, document.id); err != nil {
				return fmt.Errorf("save %s %s after legacy scenario migration: %w", source.name, document.id, err)
			}
		}
	}
	return nil
}

func rewriteLegacyScenarioEntries(payload, oldScenarioID, scenarioID string) (string, bool, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" || trimmed == "null" {
		return payload, false, nil
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &entries); err != nil {
		return "", false, err
	}
	changed := false
	for _, entry := range entries {
		rawScenarioID, ok := entry["scenario_id"]
		if !ok {
			continue
		}
		var entryScenarioID string
		if err := json.Unmarshal(rawScenarioID, &entryScenarioID); err != nil {
			return "", false, fmt.Errorf("decode scenario_id: %w", err)
		}
		if entryScenarioID != oldScenarioID {
			continue
		}
		replacement, err := json.Marshal(scenarioID)
		if err != nil {
			return "", false, err
		}
		entry["scenario_id"] = replacement
		changed = true
	}
	if !changed {
		return payload, false, nil
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func deleteLegacyScenarioIfUnreferencedTx(tx *storeTx, scenarioID string) error {
	var referenceCount int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM tests WHERE scenario_id = ?) +
		(SELECT COUNT(*) FROM scenario_schedules WHERE scenario_id = ?) +
		(SELECT COUNT(*) FROM scenario_schedule_occurrences WHERE scenario_id = ?) +
		(SELECT COUNT(*) FROM scenario_schedule_approvals WHERE scenario_id = ?) +
		(SELECT COUNT(*) FROM scenario_data_pool_bindings WHERE scenario_id = ?) +
		(SELECT COUNT(*) FROM capacity_reservations WHERE scenario_id = ?) +
		(SELECT COUNT(*) FROM external_result_imports WHERE scenario_id = ?)`,
		scenarioID, scenarioID, scenarioID, scenarioID, scenarioID, scenarioID, scenarioID).Scan(&referenceCount); err != nil {
		return fmt.Errorf("count remaining references to legacy scenario %s: %w", scenarioID, err)
	}
	if referenceCount != 0 {
		return nil
	}
	for _, query := range []string{
		`SELECT COALESCE(entries_json, '') FROM execution_plans WHERE entries_json LIKE ?`,
		`SELECT COALESCE(entries_json, '') FROM execution_plan_runs WHERE entries_json LIKE ?`,
	} {
		rows, err := tx.Query(query, "%"+scenarioID+"%")
		if err != nil {
			return fmt.Errorf("check JSON references to legacy scenario %s: %w", scenarioID, err)
		}
		for rows.Next() {
			var payload string
			if err := rows.Scan(&payload); err != nil {
				rows.Close()
				return fmt.Errorf("scan JSON references to legacy scenario %s: %w", scenarioID, err)
			}
			_, changed, err := rewriteLegacyScenarioEntries(payload, scenarioID, "")
			if err != nil || changed {
				rows.Close()
				return nil
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("check JSON references to legacy scenario %s: %w", scenarioID, err)
		}
		rows.Close()
	}
	if _, err := tx.Exec(`DELETE FROM scenario_scripts WHERE scenario_id = ?`, scenarioID); err != nil {
		return fmt.Errorf("delete scripts for retired legacy scenario %s: %w", scenarioID, err)
	}
	if _, err := tx.Exec(`DELETE FROM scenarios WHERE id = ?`, scenarioID); err != nil {
		return fmt.Errorf("delete retired legacy scenario %s: %w", scenarioID, err)
	}
	return nil
}

func insertLegacyScenarioScriptsTx(tx *storeTx, scenarioID, testID, groupsJSON string) error {
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
		return fmt.Errorf("decode legacy test %s groups_json: %w", testID, err)
	}
	for i, group := range groups {
		if group.ScriptID == "" {
			continue
		}
		stagesJSON, err := json.Marshal(group.Stages)
		if err != nil {
			return fmt.Errorf("marshal legacy test %s group %d stages: %w", testID, i, err)
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, initialization, role)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, i, group.Name, group.ScriptID, group.VUsers, group.RampUp, group.RampDown, string(stagesJSON), group.Initialization, group.Role); err != nil {
			return err
		}
	}
	return nil
}
