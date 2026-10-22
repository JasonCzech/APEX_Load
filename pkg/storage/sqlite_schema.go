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
	return schemaCreateTableStatementsFor(storeDialectSQLite)
}

func schemaCreateTableStatementsFor(dialect storeDialect) []string {
	return append([]string{
		schemaMigrationLedgerDDL,
		`CREATE TABLE IF NOT EXISTS telemetry_notification_publications (
            test_id TEXT PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            manifest_sha256 TEXT NOT NULL,
            event_id TEXT NOT NULL,
            decision TEXT NOT NULL,
            suppressed_publications INTEGER NOT NULL,
            updated_at DATETIME NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS telemetry_notification_events (
 id TEXT PRIMARY KEY,
 test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 format_version INTEGER NOT NULL,
 envelope TEXT NOT NULL,
 created_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS telemetry_notification_deliveries (
 event_id TEXT NOT NULL REFERENCES telemetry_notification_events(id) ON DELETE CASCADE,
 destination TEXT NOT NULL,
 state TEXT NOT NULL,
 attempts INTEGER NOT NULL,
 owner_token TEXT NOT NULL,
 next_attempt DATETIME NOT NULL,
 PRIMARY KEY(event_id,destination))`,

		`CREATE TABLE IF NOT EXISTS prometheus_connections (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name TEXT NOT NULL,revision BIGINT NOT NULL,verified_revision BIGINT NOT NULL DEFAULT 0,
 payload_json TEXT NOT NULL,created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS dynatrace_operations (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL,
 connection_revision BIGINT NOT NULL,
 collector_id TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL,
 state TEXT NOT NULL,
 payload_json TEXT NOT NULL,
 result_json TEXT NOT NULL DEFAULT '',
 error TEXT NOT NULL DEFAULT '',
 owner_token TEXT NOT NULL DEFAULT '',
 lease_expires_at DATETIME,
 created_at DATETIME NOT NULL,
 expires_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS dynatrace_scan_reservations (
 job_id TEXT NOT NULL REFERENCES run_monitor_jobs(id) ON DELETE CASCADE,
 attempt_id TEXT NOT NULL,
 reserved_bytes BIGINT NOT NULL,
 consumed_bytes BIGINT NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'pending',
 created_at DATETIME NOT NULL,
 PRIMARY KEY(job_id,attempt_id))`,
		`CREATE TABLE IF NOT EXISTS appdynamics_quota_pools (resource_key TEXT PRIMARY KEY,updated_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS appdynamics_quota_leases (
 resource_key TEXT NOT NULL REFERENCES appdynamics_quota_pools(resource_key) ON DELETE CASCADE,
 executor_id TEXT NOT NULL,expires_at DATETIME NOT NULL,PRIMARY KEY(resource_key,executor_id))`,
		`CREATE TABLE IF NOT EXISTS appdynamics_connections (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 name TEXT NOT NULL, revision BIGINT NOT NULL, verified_revision BIGINT NOT NULL DEFAULT 0,
 payload_json TEXT NOT NULL, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS appdynamics_inspections (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL,connection_revision BIGINT NOT NULL,collector_id TEXT NOT NULL DEFAULT '',provider TEXT NOT NULL DEFAULT 'appdynamics',kind TEXT NOT NULL,state TEXT NOT NULL,payload_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',owner_token TEXT NOT NULL DEFAULT '',lease_expires_at DATETIME,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS elastic_inspections (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
 connection_id TEXT NOT NULL,connection_revision BIGINT NOT NULL,collector_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL,state TEXT NOT NULL,payload_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',cleanup_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',owner_token TEXT NOT NULL DEFAULT '',lease_expires_at DATETIME,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS run_monitor_observation_heads (
 job_id TEXT NOT NULL REFERENCES run_monitor_jobs(id) ON DELETE CASCADE,
 record_key TEXT NOT NULL,
 sequence BIGINT NOT NULL,
 record_index INTEGER NOT NULL,
 record_kind TEXT NOT NULL,
 content_digest TEXT NOT NULL,
 series_key TEXT NOT NULL DEFAULT '',
 diagnostic_kind TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(job_id,record_key))`,
		`CREATE TABLE IF NOT EXISTS run_annotations (
            test_id TEXT PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            revision BIGINT NOT NULL, payload TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS object_activity_migrations (id TEXT PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS object_activity_events (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, resource_name TEXT NOT NULL, action TEXT NOT NULL, actor_subject TEXT NOT NULL, created_at DATETIME NOT NULL, status TEXT NOT NULL DEFAULT 'success', payload TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS object_archive_states (resource_type TEXT NOT NULL, resource_id TEXT NOT NULL, incarnation TEXT NOT NULL, archived_at DATETIME, restored_at DATETIME, PRIMARY KEY(resource_type, resource_id))`,
		`CREATE TABLE IF NOT EXISTS analysis_cleanup_jobs (
            kind TEXT NOT NULL, project_id TEXT NOT NULL, resource_id TEXT NOT NULL,
            not_before DATETIME NOT NULL, PRIMARY KEY(kind,resource_id)
        )`,
		`CREATE TABLE IF NOT EXISTS analysis_upload_writers (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL, import_id TEXT NOT NULL,
            file_id TEXT NOT NULL, chunk_offset BIGINT NOT NULL, payload TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS analysis_attempt_journal (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL,
            import_id TEXT NOT NULL, payload TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS analysis_definitions (
            id TEXT NOT NULL,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            kind TEXT NOT NULL, revision BIGINT NOT NULL, payload TEXT NOT NULL,
            PRIMARY KEY(id,revision)
        )`,
		`CREATE TABLE IF NOT EXISTS run_monitor_collector_readiness (
            collector_id TEXT PRIMARY KEY REFERENCES monitoring_collectors(id) ON DELETE CASCADE,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            seen_at DATETIME NOT NULL,
            packs TEXT NOT NULL,
            destinations TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS preparations (
            id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE, revision BIGINT NOT NULL,
            execution_revision TEXT NOT NULL, state TEXT NOT NULL, idempotency_key TEXT NOT NULL,
            owner_fence TEXT NOT NULL, created_at DATETIME NOT NULL, expires_at DATETIME NOT NULL, payload TEXT NOT NULL,
            UNIQUE(test_id, idempotency_key)
        )`,
		`CREATE TABLE IF NOT EXISTS analysis_imports (
            id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            revision BIGINT NOT NULL, state TEXT NOT NULL, payload TEXT NOT NULL,
            owner TEXT NOT NULL, lease_expires_at DATETIME NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS monitor_collection_leases (
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, source_id TEXT NOT NULL,
 collector_id TEXT NOT NULL, instance_id TEXT NOT NULL, generation BIGINT NOT NULL,
 poll_sequence BIGINT NOT NULL, source_revision BIGINT NOT NULL, scope_hash TEXT NOT NULL, identity_hash TEXT NOT NULL,
 state TEXT NOT NULL, poll_id TEXT NOT NULL, expires_at DATETIME NOT NULL,
 PRIMARY KEY(project_id,source_id)
 )`,
		`CREATE TABLE IF NOT EXISTS monitor_management_polls (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, source_id TEXT NOT NULL,
 collector_id TEXT NOT NULL, instance_id TEXT NOT NULL, nonce TEXT NOT NULL,
 generation BIGINT NOT NULL, poll_sequence BIGINT NOT NULL, source_revision BIGINT NOT NULL,
 scope_hash TEXT NOT NULL, identity_hash TEXT NOT NULL, state TEXT NOT NULL,
 created_at DATETIME NOT NULL, deadline DATETIME NOT NULL, core_digest TEXT, payload TEXT NOT NULL,
 UNIQUE(project_id,collector_id,instance_id,nonce)
 )`,
		`CREATE TABLE IF NOT EXISTS monitor_sources (
            id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            revision BIGINT NOT NULL, payload TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS monitor_bindings (
            scenario_id TEXT PRIMARY KEY REFERENCES scenarios(id) ON DELETE CASCADE,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            revision BIGINT NOT NULL, payload TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS run_monitor_sessions (
            test_id TEXT PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            started_at DATETIME NOT NULL, stopped_at DATETIME, binding_revision BIGINT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS run_monitor_jobs (
            id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
            collector_id TEXT NOT NULL, state TEXT NOT NULL, next_sequence BIGINT NOT NULL DEFAULT 0,
            expires_at DATETIME NOT NULL, payload TEXT NOT NULL
        )`,
		`CREATE TABLE IF NOT EXISTS run_monitor_batches (
            job_id TEXT NOT NULL REFERENCES run_monitor_jobs(id) ON DELETE CASCADE,
            sequence BIGINT NOT NULL, digest TEXT NOT NULL, artifact_key TEXT NOT NULL,
            sample_count INTEGER NOT NULL, received_at DATETIME NOT NULL,
            PRIMARY KEY(job_id, sequence)
        )`,
		`CREATE TABLE IF NOT EXISTS baseline_comparison_snapshots (
            id TEXT PRIMARY KEY, project_id TEXT NOT NULL, test_id TEXT NOT NULL,
            payload TEXT NOT NULL, created_at DATETIME NOT NULL,
            FOREIGN KEY (test_id) REFERENCES tests(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS scenario_comparison_policies (
            scenario_id TEXT PRIMARY KEY, project_id TEXT NOT NULL,
            revision BIGINT NOT NULL, payload TEXT NOT NULL,
            FOREIGN KEY (scenario_id) REFERENCES scenarios(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS script_library_assignments (
            item_id TEXT PRIMARY KEY,
            application_id TEXT,
            revision BIGINT NOT NULL DEFAULT 0,
            FOREIGN KEY (item_id) REFERENCES scripts(id) ON DELETE CASCADE,
            FOREIGN KEY (application_id) REFERENCES applications(id)
        )`,
		`CREATE TABLE IF NOT EXISTS script_library_assignments_releases (
            item_id TEXT NOT NULL,
            release_id TEXT NOT NULL,
            PRIMARY KEY(item_id, release_id),
            FOREIGN KEY (item_id) REFERENCES script_library_assignments(item_id) ON DELETE CASCADE,
            FOREIGN KEY (release_id) REFERENCES releases(id)
        )`,
		`CREATE TABLE IF NOT EXISTS scenario_library_assignments (
            item_id TEXT PRIMARY KEY,
            application_id TEXT,
            revision BIGINT NOT NULL DEFAULT 0,
            FOREIGN KEY (item_id) REFERENCES scenarios(id) ON DELETE CASCADE,
            FOREIGN KEY (application_id) REFERENCES applications(id)
        )`,
		`CREATE TABLE IF NOT EXISTS scenario_library_assignments_releases (
            item_id TEXT NOT NULL,
            release_id TEXT NOT NULL,
            PRIMARY KEY(item_id, release_id),
            FOREIGN KEY (item_id) REFERENCES scenario_library_assignments(item_id) ON DELETE CASCADE,
            FOREIGN KEY (release_id) REFERENCES releases(id)
        )`,
		`CREATE TABLE IF NOT EXISTS pod_capacity_reservations (
			target TEXT NOT NULL,
			test_id TEXT NOT NULL,
			incarnation TEXT NOT NULL,
			generation BIGINT NOT NULL,
			state TEXT NOT NULL,
			reservation_json TEXT NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY(target, test_id)
		)`,
		`CREATE TABLE IF NOT EXISTS domains (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS domain_feature_policies (
			domain_id TEXT PRIMARY KEY,
			disabled_feature_ids_json TEXT NOT NULL,
			revision TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(domain_id) REFERENCES domains(id)
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
			group_id TEXT,
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
			content_revision TEXT,
			ref_key TEXT,
			materialization_key TEXT,
			name TEXT NOT NULL,
			workload_shape_id TEXT,
			duration TEXT NOT NULL,
			start_delay TEXT,
			scenario_mode TEXT,
 termination_mode TEXT NOT NULL DEFAULT 'duration',
			shakeout_enabled ` + dialect.booleanColumnDDL(false, false) + `,
			resource_baseline_enabled ` + dialect.booleanColumnDDL(false, false) + `,
			assignment_json TEXT,
			goal_config_json TEXT,
			runtime_settings_json TEXT,
			traffic_profile_json TEXT,
			joint_evidence_json TEXT,
			telemetry_acceptance_policy_id TEXT,
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
			enabled ` + dialect.booleanColumnDDL(false, true) + `,
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
			generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
			acquired_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS scheduler_content_revision (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			revision BIGINT NOT NULL CHECK (revision > 0)
		)`,
		`CREATE TABLE IF NOT EXISTS start_admission_revision (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			revision BIGINT NOT NULL CHECK (revision > 0)
		)`,
		`CREATE TABLE IF NOT EXISTS replay_session_leases (
			session_id TEXT PRIMARY KEY,
			owner_token TEXT NOT NULL,
			project_id TEXT NOT NULL,
			state TEXT NOT NULL CHECK (state IN ('active', 'retained')),
			generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
			expires_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS replay_artifact_receipts (
            session_id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL,
            generation TEXT NOT NULL UNIQUE,
            owner_token TEXT NOT NULL,
            state TEXT NOT NULL CHECK (state IN ('staging','ready','cleanup')),
            spec_json TEXT NOT NULL,
            reserved_bytes BIGINT NOT NULL CHECK (reserved_bytes > 0),
            writer_joined INTEGER NOT NULL CHECK (writer_joined IN (0,1)),
            expires_at DATETIME NOT NULL
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
			start_offset TEXT,
			stages_json TEXT,
			iterations INTEGER,
			initialization TEXT,
			role TEXT,
			runtime_settings_json TEXT,
			assignment_json TEXT,
			transaction_response_time_sla_ms REAL,
			PRIMARY KEY (scenario_id, sort_order),
			FOREIGN KEY(scenario_id) REFERENCES scenarios(id),
			FOREIGN KEY(script_id) REFERENCES scripts(id)
		)`,
		`CREATE TABLE IF NOT EXISTS shared_data_pools (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			schema_json TEXT,
			columns_json TEXT,
			project_id TEXT REFERENCES projects(id),
			rows_revision BIGINT NOT NULL DEFAULT 0,
			row_count BIGINT NOT NULL DEFAULT 0,
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
			pool_id TEXT,
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
		`CREATE TABLE IF NOT EXISTS application_secrets (
			application_id TEXT NOT NULL REFERENCES applications(id),
			secret_name TEXT NOT NULL,
			provider TEXT NOT NULL,
			ciphertext TEXT NOT NULL,
			wrapped_key_json TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			created_by TEXT,
			PRIMARY KEY(application_id, secret_name)
		)`,
		`CREATE TABLE IF NOT EXISTS application_secret_provider_metadata (
			application_id TEXT NOT NULL REFERENCES applications(id),
			secret_name TEXT NOT NULL,
			provider TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			created_by TEXT,
			PRIMARY KEY(application_id, secret_name)
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
			content_revision TEXT,
			ref_key TEXT,
			name TEXT,
			protocol TEXT,
			version TEXT,
			project_id TEXT REFERENCES projects(id),
			folder_id TEXT REFERENCES folders(id),
			generated_by TEXT,
			generated_from TEXT,
			metadata_json TEXT,
			goal_config_json TEXT,
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
			-- Whether sanitized_artifact_id points at a genuinely sanitized
			-- document. Formats that retain source raw-only satisfy the NOT NULL
			-- constraint by pointing both columns at the raw artifact, so this
			-- flag -- not the column being populated -- is what authorizes serving
			-- the editor-visible sanitized download. Defaults to 0 so a row
			-- written by code that does not know about this column cannot
			-- accidentally publish raw source to a wider audience.
			sanitized_available INTEGER NOT NULL DEFAULT 0,
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
		`CREATE TABLE IF NOT EXISTS bruno_native_import_batches (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			source_profile TEXT NOT NULL DEFAULT 'bruno_native',
			receipt_version INTEGER NOT NULL DEFAULT 1,
			idempotency_key TEXT NOT NULL,
			request_digest_profile TEXT NOT NULL DEFAULT 'bruno-native-commit-v1',
			request_sha256 TEXT NOT NULL,
			raw_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
			sanitized_artifact_id TEXT NOT NULL REFERENCES bruno_source_artifacts(id),
			candidate_ids_json TEXT NOT NULL,
			script_ids_json TEXT NOT NULL,
			scripts_json TEXT NOT NULL DEFAULT '[]',
			created_by TEXT,
			created_at DATETIME NOT NULL,
			-- Deliberately omits source_profile. It reads like a bug waiting for a
			-- second profile to register -- two formats picking the same key would
			-- collide instead of coexisting -- but the collision is unreachable:
			-- every caller sets idempotency_key to a protected import session id,
			-- which is a UUID, so no two profiles can choose the same string.
			-- Adding source_profile here would mean a full table rebuild in SQLite
			-- (a UNIQUE table constraint cannot be altered) on the table that holds
			-- commit receipts, which is the guard against a retry creating
			-- duplicate scripts. Verified 2026-07-27: real risk, no real benefit.
			-- Revisit only if some profile starts deriving keys from client input.
			UNIQUE(project_id, idempotency_key)
		)`,
		`CREATE TABLE IF NOT EXISTS direct_import_receipts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			source_profile TEXT NOT NULL,
			operation_key_hash TEXT NOT NULL,
			request_sha256 TEXT NOT NULL,
			state TEXT NOT NULL CHECK(state IN ('pending', 'complete')),
			owner_token TEXT NOT NULL DEFAULT '',
			lease_expires_at DATETIME,
			script_ids_json TEXT NOT NULL DEFAULT '[]',
			scenario_id TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(project_id, source_profile, operation_key_hash)
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
 termination_mode TEXT NOT NULL DEFAULT 'duration',
			shakeout_enabled ` + dialect.booleanColumnDDL(false, false) + `,
			resource_baseline_enabled ` + dialect.booleanColumnDDL(false, false) + `,
			start_delay TEXT,
			assignment_json TEXT,
			total_vusers INTEGER,
			duration_ns INTEGER,
			start_time DATETIME,
			scheduled_start_at DATETIME,
			started_at DATETIME,
			pending_since DATETIME,
			paused_at DATETIME,
			cumulative_pause_ns INTEGER NOT NULL DEFAULT 0,
			capacity_window_start_at DATETIME,
			capacity_window_end_at DATETIME,
			baseline_pinned ` + dialect.booleanColumnDDL(false, false) + `,
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
			data_pools_json TEXT,
			goal_config_json TEXT,
			traffic_profile_json TEXT,
			elk_capture_json TEXT,
			dynatrace_capture_json TEXT,
			monitor_bindings_json TEXT,
			trace_context_json TEXT,
			joint_evidence_json TEXT,
			analysis_policy_selections_json TEXT,
			telemetry_acceptance_policy_id TEXT,
			start_authorization_json TEXT,
			execution_plan_approval_json TEXT,
			execution_plan_json TEXT,
			initial_provenance_json TEXT,
			timeslot_id TEXT,
			telemetry_checkpoint_json TEXT,
			telemetry_checkpoint_revision BIGINT NOT NULL DEFAULT 0,
			continuity_checkpoint_json TEXT,
			execution_generation BIGINT NOT NULL DEFAULT 0
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
			native_evidence_json TEXT,
			java_http_json TEXT,
			messaging_json TEXT,
			messaging_progress_json TEXT,
			transactions_json TEXT,
			by_region_json TEXT,
			browser_json TEXT,
			browser_by_device_json TEXT,
			latency_json TEXT,
			dropped_results_available BOOLEAN,
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
		`CREATE TABLE IF NOT EXISTS confluence_connections (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, name TEXT NOT NULL,
			enabled BOOLEAN NOT NULL DEFAULT FALSE, site_url TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '', space_id TEXT NOT NULL DEFAULT '',
			parent_page_id TEXT NOT NULL DEFAULT '', token_secret_name TEXT NOT NULL DEFAULT '',
			auto_publish BOOLEAN NOT NULL DEFAULT FALSE,
			last_checked_at DATETIME, last_check_status TEXT NOT NULL DEFAULT '',
			last_check_message TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS confluence_publications (
			test_id TEXT NOT NULL, project_id TEXT NOT NULL, connection_id TEXT NOT NULL,
			page_id TEXT NOT NULL DEFAULT '', page_url TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0,
			automatic BOOLEAN NOT NULL DEFAULT FALSE,
			requested_by TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL, PRIMARY KEY(test_id, connection_id),
			FOREIGN KEY(test_id) REFERENCES tests(id) ON DELETE CASCADE,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
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
			trend_point_json TEXT,
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
			prepared_manifest_json TEXT NOT NULL DEFAULT '',
			attempts INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS run_archive_collection_intents (
            test_id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL,
            test_start_time DATETIME NOT NULL,
            intent_token TEXT NOT NULL,
            sources_json TEXT NOT NULL,
            cache_limit_bytes BIGINT NOT NULL DEFAULT 0,
            cached_sources_json TEXT NOT NULL DEFAULT '{}',
            attempts INTEGER NOT NULL DEFAULT 0,
            exhausted INTEGER NOT NULL DEFAULT 0,
            resolved INTEGER NOT NULL DEFAULT 0,
            created_at DATETIME NOT NULL,
            updated_at DATETIME NOT NULL,
            FOREIGN KEY(test_id) REFERENCES tests(id) ON DELETE CASCADE,
            FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		`CREATE TABLE IF NOT EXISTS run_archive_replica_holds (
            test_id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL,
            test_start_time DATETIME NOT NULL,
            intent_token TEXT NOT NULL,
            fleet_id TEXT NOT NULL,
            namespace TEXT NOT NULL,
            workload_uid TEXT NOT NULL,
            replica_floor INTEGER NOT NULL,
            FOREIGN KEY(test_id) REFERENCES tests(id) ON DELETE CASCADE,
            FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
        )`,
		// A deleted test's ID keeps its project, so a later create cannot move the
		// ID to another project. No foreign keys: the record outlives the test and
		// its project.
		`CREATE TABLE IF NOT EXISTS deleted_test_identities (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			deleted_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS run_archive_failures (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			test_start_time DATETIME NOT NULL,
			test_end_time DATETIME NOT NULL,
			test_status TEXT NOT NULL,
			reason TEXT NOT NULL,
			recorded_at DATETIME NOT NULL,
			FOREIGN KEY(test_id) REFERENCES tests(id) ON DELETE CASCADE,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
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
			base_branch TEXT,
			base_blob_sha TEXT,
			base_missing ` + dialect.booleanColumnDDL(true, false) + `,
			content_hash TEXT,
			synced_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS github_script_sync_claims (
			script_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			claim_id TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(script_id) REFERENCES scripts(id) ON DELETE CASCADE,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS github_script_sync_targets (
			target_key TEXT PRIMARY KEY,
			owner_norm TEXT NOT NULL,
			repo_norm TEXT NOT NULL,
			branch TEXT NOT NULL,
			path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(owner_norm, repo_norm, branch, path)
		)`,
		`CREATE TABLE IF NOT EXISTS github_script_sync_target_owners (
			target_key TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			script_id TEXT NOT NULL,
			owner_norm TEXT NOT NULL,
			repo_norm TEXT NOT NULL,
			branch TEXT NOT NULL,
			path TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(owner_norm, repo_norm, branch, path),
			FOREIGN KEY(target_key) REFERENCES github_script_sync_targets(target_key) ON DELETE CASCADE,
			FOREIGN KEY(script_id) REFERENCES scripts(id) ON DELETE CASCADE,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS github_script_sync_target_claims (
			target_key TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			script_id TEXT NOT NULL,
			claim_id TEXT NOT NULL,
			owner_norm TEXT NOT NULL,
			repo_norm TEXT NOT NULL,
			branch TEXT NOT NULL,
			path TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(owner_norm, repo_norm, branch, path),
			FOREIGN KEY(target_key) REFERENCES github_script_sync_targets(target_key) ON DELETE CASCADE,
			FOREIGN KEY(script_id) REFERENCES scripts(id) ON DELETE CASCADE,
			FOREIGN KEY(project_id) REFERENCES projects(id) ON DELETE CASCADE
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
		`CREATE TABLE IF NOT EXISTS powerbi_publishes (
			id TEXT PRIMARY KEY,
			test_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			dataset_id TEXT,
			dataset_name TEXT,
			schema_version INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL,
			run_rows_pushed INTEGER NOT NULL DEFAULT 0,
			transaction_rows_pushed INTEGER NOT NULL DEFAULT 0,
			transaction_rows_total INTEGER NOT NULL DEFAULT 0,
			error TEXT,
			requested_by TEXT,
			published_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS service_accounts (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			owner_id TEXT,
			contact_email TEXT,
			key_hash TEXT NOT NULL UNIQUE,
			active ` + dialect.booleanColumnDDL(true, true) + `,
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
			active ` + dialect.booleanColumnDDL(true, true) + `,
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
		`CREATE TABLE IF NOT EXISTS local_auth_credentials (
			username TEXT PRIMARY KEY,
			user_id TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS local_auth_sessions (
			token_hash TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			session_epoch INTEGER NOT NULL,
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS local_auth_runtime (
			singleton_key TEXT PRIMARY KEY,
			generation TEXT NOT NULL,
			activated_at DATETIME NOT NULL
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
			application_id TEXT REFERENCES applications(id),
			release_id TEXT,
			scenario_id TEXT,
			source TEXT,
			summary_json TEXT,
			raw_json TEXT,
			archive_record_json TEXT,
			created_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS script_recordings (
			script_id TEXT PRIMARY KEY,
			project_id TEXT,
			script_incarnation TEXT,
			schema_version TEXT,
			action_graph_digest TEXT,
			source TEXT,
			sensitivity TEXT,
			request_count INTEGER,
			observations_json TEXT,
			observations_projection_version INTEGER NOT NULL DEFAULT 0,
			observations_projected_at DATETIME,
			correlation_observations_json TEXT,
			correlation_status_version INTEGER NOT NULL DEFAULT 0,
			correlation_observation_count INTEGER NOT NULL DEFAULT 0,
			correlation_sidecar_version INTEGER NOT NULL DEFAULT 0,
			correlation_status_projected_at DATETIME,
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
		// cluster_connections registers the Kubernetes control planes a system
		// administrator has connected. Credentials are stored only as keywrap
		// envelopes (ciphertext + wrapped DEK), never in plaintext.
		//
		// tls_mode and aws_credential_mode are deliberately TEXT rather than
		// booleans ("use system roots", "use ambient credentials"): a TEXT
		// column passes through rewriteSQL untouched and needs none of the
		// three coordinated Postgres boolean entries, and neither
		// use_system_roots nor aws_ambient would match looksBooleanColumn, so
		// they would silently escape the Postgres boolean rewrite guard test.
		`CREATE TABLE IF NOT EXISTS cluster_connections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			kind TEXT NOT NULL DEFAULT 'generic',
			api_server_url TEXT,
			namespace TEXT NOT NULL,
			generator_selector TEXT,
			generator_statefulset TEXT,
			generator_container TEXT,
			ca_certificate_pem TEXT,
			tls_mode TEXT NOT NULL DEFAULT 'custom_ca',
			auth_mode TEXT NOT NULL DEFAULT 'in_cluster',
			azure_tenant_id TEXT,
			azure_client_id TEXT,
			azure_scope TEXT,
			azure_authority TEXT,
			client_secret_ciphertext TEXT,
			client_secret_wrapped_key TEXT,
			client_secret_env_var TEXT,
			static_token_ciphertext TEXT,
			static_token_wrapped_key TEXT,
			aws_region TEXT,
			aws_cluster_name TEXT,
			aws_credential_mode TEXT,
			aws_access_key_id TEXT,
			aws_role_arn TEXT,
			aws_sts_endpoint TEXT,
			aws_secret_key_ciphertext TEXT,
			aws_secret_key_wrapped_key TEXT,
			aws_external_id_ciphertext TEXT,
			aws_external_id_wrapped_key TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			last_checked_at DATETIME,
			last_check_status TEXT,
			last_check_message TEXT,
			labels_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// Cloud connections hold reusable provider control-plane identity while
		// execution targets below name concrete placements. Keeping these rows
		// separate prevents one credential envelope being copied for every warm
		// app, burst job, ECS service, or task definition.
		`CREATE TABLE IF NOT EXISTS cloud_connections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			provider TEXT NOT NULL,
			auth_mode TEXT NOT NULL,
			azure_cloud TEXT,
			azure_tenant_id TEXT,
			azure_subscription_id TEXT,
			azure_resource_group TEXT,
			azure_client_id TEXT,
			aws_partition TEXT,
			aws_account_id TEXT,
			aws_region TEXT,
			aws_access_key_id TEXT,
			aws_role_arn TEXT,
			client_secret_ciphertext TEXT,
			client_secret_wrapped_key TEXT,
			aws_secret_key_ciphertext TEXT,
			aws_secret_key_wrapped_key TEXT,
			aws_external_id_ciphertext TEXT,
			aws_external_id_wrapped_key TEXT,
			credential_env_var TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			last_checked_at DATETIME,
			last_check_status TEXT,
			last_check_message TEXT,
			labels_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS execution_targets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			kind TEXT NOT NULL,
			cloud_connection_id TEXT REFERENCES cloud_connections(id),
			cluster_connection_id TEXT REFERENCES cluster_connections(id),
			region TEXT,
			connectivity_mode TEXT NOT NULL DEFAULT 'direct',
			standing_owner TEXT NOT NULL DEFAULT 'apex',
			burst_owner TEXT NOT NULL DEFAULT 'disabled',
			archive_mode TEXT NOT NULL DEFAULT 'controller_pull',
			reverse_token_hash TEXT NOT NULL DEFAULT '',
			reverse_token_expires_at TEXT,
			reverse_previous_token_hash TEXT NOT NULL DEFAULT '',
			reverse_previous_token_valid_until TEXT,
			spec_json TEXT NOT NULL DEFAULT '',
			runtime_profiles_json TEXT NOT NULL DEFAULT '[]',
			capabilities_json TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active',
			last_checked_at DATETIME,
			last_check_status TEXT,
			last_check_message TEXT,
			labels_json TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS import_assets (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id),
			import_id TEXT NOT NULL,
			object_key TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			size_bytes INTEGER NOT NULL,
			content_type TEXT,
			state TEXT NOT NULL,
			last_error TEXT,
			revision INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// domain_deployment_targets is the superuser's per-domain deployment
		// constraint: which cloud a domain's fleet pools may scale into, and
		// the cluster connection new pools inherit.
		//
		// The ABSENCE of a row means "unconstrained". No row is ever seeded, so
		// every existing domain keeps exactly the behavior it has today — that
		// is the entire backward-compatibility story for this feature.
		//
		// provider is TEXT for the same reason tls_mode is: it passes through
		// rewriteSQL untouched and needs none of the coordinated Postgres
		// boolean entries.
		//
		// default_cluster_id is NULLable rather than '' NOT NULL because SQLite
		// runs with _pragma=foreign_keys(ON): an empty string satisfies no
		// reference, but NULL does.
		`CREATE TABLE IF NOT EXISTS domain_deployment_targets (
			domain_id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			default_cluster_id TEXT REFERENCES cluster_connections(id),
			default_execution_target_id TEXT REFERENCES execution_targets(id),
			updated_by_subject TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			FOREIGN KEY(domain_id) REFERENCES domains(id)
		)`,
		`CREATE TABLE IF NOT EXISTS domain_execution_target_allowlist (
			domain_id TEXT NOT NULL REFERENCES domains(id),
			execution_target_id TEXT NOT NULL REFERENCES execution_targets(id),
			PRIMARY KEY(domain_id, execution_target_id)
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
			cluster_id TEXT NOT NULL DEFAULT '',
			execution_target_id TEXT NOT NULL DEFAULT '',
			allocation_json TEXT NOT NULL DEFAULT '',
			domain_id TEXT NOT NULL DEFAULT '',
			lifecycle_mode TEXT NOT NULL DEFAULT 'standing',
			warm_floor INTEGER NOT NULL DEFAULT 0,
			readiness_timeout_seconds INTEGER NOT NULL DEFAULT 300,
			idle_grace_seconds INTEGER NOT NULL DEFAULT 300,
			runtime_profile_id TEXT NOT NULL DEFAULT '',
			capacity_admission TEXT NOT NULL DEFAULT 'strict',
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
			execution_target_id TEXT,
			provider_operation_id TEXT,
			idempotency_key TEXT,
			observed_count INTEGER NOT NULL DEFAULT 0,
			ready_count INTEGER NOT NULL DEFAULT 0,
			pending_count INTEGER NOT NULL DEFAULT 0,
			draining_count INTEGER NOT NULL DEFAULT 0,
			failed_count INTEGER NOT NULL DEFAULT 0,
			deadline_at DATETIME,
			last_error TEXT,
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
		`CREATE TABLE IF NOT EXISTS capacity_instance_intents (
			id TEXT PRIMARY KEY,
			execution_target_id TEXT NOT NULL,
			fleet_pool_id TEXT NOT NULL,
			test_id TEXT,
			provisioning_request_id TEXT,
			client_token TEXT NOT NULL,
			launch_nonce TEXT NOT NULL,
			generator_id TEXT NOT NULL,
			provider_id TEXT,
			state TEXT NOT NULL DEFAULT 'planned',
			archive_state TEXT NOT NULL DEFAULT 'pending',
			cleanup_deadline DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(execution_target_id, client_token)
		)`,
		`CREATE TABLE IF NOT EXISTS reverse_generator_owners (
			generator_id TEXT PRIMARY KEY,
			execution_target_id TEXT NOT NULL,
			created_at DATETIME NOT NULL
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
			application_id TEXT,
			release_id TEXT,
			overlap_policy TEXT,
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
			release_id TEXT,
			run_name_template TEXT,
			enabled ` + dialect.booleanColumnDDL(false, true) + `,
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
			approval_id TEXT,
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
			auto_promote_on_heal ` + dialect.booleanColumnDDL(false, false) + `,
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
			rules_projection_version INTEGER NOT NULL DEFAULT 0,
			rules_projected_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS threshold_evaluations (
            test_id TEXT PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            evaluated_at DATETIME NOT NULL,
            covered INTEGER NOT NULL DEFAULT 0
        )`,
		`CREATE TABLE IF NOT EXISTS threshold_incidents (
            id TEXT PRIMARY KEY,
            project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
            test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
            test_name TEXT NOT NULL,
            incident_key TEXT NOT NULL,
            severity TEXT NOT NULL,
            payload_json TEXT NOT NULL,
            active ` + dialect.booleanColumnDDL(true, true) + `,
            first_seen_at DATETIME NOT NULL,
            last_seen_at DATETIME NOT NULL,
            resolved_at DATETIME,
            acknowledged_by TEXT NOT NULL DEFAULT '',
            acknowledged_at DATETIME
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
		`CREATE TABLE IF NOT EXISTS elk_connections (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			enabled BOOLEAN NOT NULL DEFAULT FALSE,
			endpoint TEXT NOT NULL DEFAULT '',
			index_pattern TEXT NOT NULL DEFAULT '',
			timestamp_field TEXT NOT NULL DEFAULT '@timestamp',
			level_field TEXT NOT NULL DEFAULT '',
			message_field TEXT NOT NULL DEFAULT '',
			test_id_field TEXT NOT NULL DEFAULT '',
			trace_id_field TEXT NOT NULL DEFAULT '',
			base_filter TEXT NOT NULL DEFAULT '',
			error_filter TEXT NOT NULL DEFAULT '',
			auth_type TEXT NOT NULL DEFAULT '',
			api_key_secret_name TEXT NOT NULL DEFAULT '',
			username_secret_name TEXT NOT NULL DEFAULT '',
			password_secret_name TEXT NOT NULL DEFAULT '',
			metric_aggregations_json TEXT NOT NULL DEFAULT '[]',
            elastic_profile_json TEXT NOT NULL DEFAULT '',
			last_checked_at DATETIME,
			last_check_status TEXT NOT NULL DEFAULT '',
			last_check_message TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(project_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS dynatrace_connections (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			enabled BOOLEAN NOT NULL DEFAULT FALSE,
			environment_url TEXT NOT NULL DEFAULT '',
			platform_url TEXT NOT NULL DEFAULT '',
			metrics_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			problems_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			logs_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			environment_token_secret TEXT NOT NULL DEFAULT '',
			platform_auth_type TEXT NOT NULL DEFAULT '',
			platform_token_secret TEXT NOT NULL DEFAULT '',
			oauth_client_id_secret TEXT NOT NULL DEFAULT '',
			oauth_client_secret_secret TEXT NOT NULL DEFAULT '',
			oauth_resource TEXT NOT NULL DEFAULT '',
			oauth_scopes_json TEXT NOT NULL DEFAULT '[]',
			metric_queries_json TEXT NOT NULL DEFAULT '[]',
			problem_selector TEXT NOT NULL DEFAULT '',
			problem_entity_selector TEXT NOT NULL DEFAULT '',
			log_base_filter TEXT NOT NULL DEFAULT '',
			log_test_id_field TEXT NOT NULL DEFAULT '',
			log_timestamp_field TEXT NOT NULL DEFAULT 'timestamp',
			log_status_field TEXT NOT NULL DEFAULT 'status',
			log_message_field TEXT NOT NULL DEFAULT 'content',
			log_trace_id_field TEXT NOT NULL DEFAULT 'trace_id',
			poll_interval_seconds INTEGER NOT NULL DEFAULT 15,
			last_checked_at DATETIME,
			last_check_status TEXT NOT NULL DEFAULT '',
			last_check_message TEXT NOT NULL DEFAULT '',
			last_capability_health_json TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(project_id, name)
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
		// Scenario-builder drafts saved to the project: not scenarios, so nothing
		// can launch one. scenario_id names the scenario an edit draft changes.
		`CREATE TABLE IF NOT EXISTS scenario_drafts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			scenario_id TEXT,
			name TEXT NOT NULL,
			document_json TEXT NOT NULL,
			revision INTEGER NOT NULL DEFAULT 1,
			created_by TEXT,
			updated_by TEXT,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		// The dialect selects this boolean type and default explicitly; the
		// schema parity tests guard compatibility with existing databases.
		`CREATE TABLE IF NOT EXISTS grafana_dashboards (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			source_title TEXT,
			source_uid TEXT,
			source_sha256 TEXT NOT NULL,
			schema_version INTEGER NOT NULL DEFAULT 0,
			enabled ` + dialect.booleanColumnDDL(false, true) + `,
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
		`CREATE TABLE IF NOT EXISTS dashboard_definitions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			owner TEXT,
			visibility TEXT NOT NULL DEFAULT 'project',
			revision BIGINT NOT NULL DEFAULT 1,
			definition_json TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS dashboard_definition_revisions (
			definition_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			revision BIGINT NOT NULL,
			name TEXT NOT NULL,
			description TEXT,
			owner TEXT,
			visibility TEXT NOT NULL,
			definition_json TEXT NOT NULL,
			saved_at DATETIME NOT NULL,
			PRIMARY KEY (definition_id, revision)
		)`,
		`CREATE TABLE IF NOT EXISTS grafana_dashboard_snapshots (
			test_id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			captured_at DATETIME NOT NULL,
			FOREIGN KEY(test_id) REFERENCES tests(id)
		)`,
		`CREATE TABLE IF NOT EXISTS rca_evidence_revisions (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
			revision BIGINT NOT NULL,
			created_at DATETIME NOT NULL,
			payload TEXT NOT NULL,
			UNIQUE(test_id, revision)
		)`,
		`CREATE TABLE IF NOT EXISTS rca_analyses (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
			revision BIGINT NOT NULL,
			state TEXT NOT NULL,
			created_at DATETIME NOT NULL,
			completed_at DATETIME,
			input_digest TEXT NOT NULL,
			payload TEXT NOT NULL,
			owner TEXT NOT NULL DEFAULT '',
			lease_expires_at DATETIME,
			origin TEXT NOT NULL DEFAULT 'requested',
			UNIQUE(test_id, revision)
		)`,
		// Reviews of an analysis, kept apart from it.
		`CREATE TABLE IF NOT EXISTS rca_critiques (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			test_id TEXT NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
			analysis_id TEXT NOT NULL REFERENCES rca_analyses(id) ON DELETE CASCADE,
			created_at DATETIME NOT NULL,
			created_by TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL
		)`,
		// One wrapped HMAC key per project, for business-key digests.
		`CREATE TABLE IF NOT EXISTS business_key_project_keys (
			project_id TEXT PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
			wrapped_key TEXT NOT NULL,
			created_at DATETIME NOT NULL
		)`,
		// Retry fences for ordinary script/scenario creates. Keys and request
		// bodies are stored only as keyed digests.
		`CREATE TABLE IF NOT EXISTS authoring_mutation_receipts (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			actor TEXT NOT NULL,
			purpose TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			request_digest TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			resource_revision TEXT NOT NULL DEFAULT '',
			outcome_version INTEGER NOT NULL DEFAULT 1,
			original_outcome TEXT NOT NULL DEFAULT '',
			state TEXT NOT NULL CHECK(state IN ('pending', 'complete')),
			owner_token TEXT NOT NULL DEFAULT '',
			lease_expires_at DATETIME,
			expires_at DATETIME NOT NULL,
			tombstoned_at DATETIME,
			tombstone_reason TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			UNIQUE(project_id, actor, purpose, key_hash)
		)`,
		// Bounded analytical evaluations run by the shared leased-job runner.
		`CREATE TABLE IF NOT EXISTS feature_evaluation_jobs (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			actor TEXT NOT NULL,
			kind TEXT NOT NULL,
			idempotency_key_hash TEXT NOT NULL DEFAULT '',
			request TEXT NOT NULL,
			input_fingerprint TEXT NOT NULL,
			state TEXT NOT NULL CHECK(state IN ('queued', 'running', 'ready', 'failed', 'cancelled', 'expired')),
			revision BIGINT NOT NULL DEFAULT 1,
			owner TEXT NOT NULL DEFAULT '',
			lease_expires_at DATETIME,
			progress TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL DEFAULT '',
			result_sha256 TEXT NOT NULL DEFAULT '',
			failure TEXT NOT NULL DEFAULT '',
			hidden_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			completed_at DATETIME,
			subject_id TEXT NOT NULL DEFAULT ''
		)`,
		// Append-only qualification evidence behind support claims.
		`CREATE TABLE IF NOT EXISTS feature_acceptance_records (
			id TEXT PRIMARY KEY,
			requirement_id TEXT NOT NULL,
			feature TEXT NOT NULL,
			profile TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			platform TEXT NOT NULL DEFAULT '',
			commit_sha TEXT NOT NULL,
			binary_sha256 TEXT NOT NULL DEFAULT '',
			suite TEXT NOT NULL,
			result TEXT NOT NULL CHECK(result IN ('passed', 'failed', 'pending', 'revoked')),
			artifacts TEXT NOT NULL DEFAULT '[]',
			notes TEXT NOT NULL DEFAULT '',
			recorded_at DATETIME NOT NULL,
			recorded_by TEXT NOT NULL
		)`,
		// Durable receipts of optional evidence artifacts a generator produced
		// (native HARs): accepted once the controller durably holds one,
		// published with the run's archive, tombstoned when received after
		// publication or deletion. No foreign key: tombstones outlive the run.
		// The store validates kind, so a new evidence kind needs no DDL.
		`CREATE TABLE IF NOT EXISTS run_evidence_receipts (
			test_id TEXT NOT NULL,
			execution_generation BIGINT NOT NULL DEFAULT 0,
			source_identity TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			source_generation TEXT NOT NULL,
			sequence BIGINT NOT NULL DEFAULT 0,
			kind TEXT NOT NULL,
			name TEXT NOT NULL,
			sha256 TEXT NOT NULL,
			bytes BIGINT NOT NULL,
			state TEXT NOT NULL CHECK(state IN ('accepted', 'published', 'tombstoned')),
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (test_id, source_identity, source_generation)
		)`,
	}, append(append(liveTableSchemaStatements(), projectLiveTableSchemaStatements()...), rendezvousSchemaStatements()...)...)
}

// schemaColumnMigrations returns the additive ALTER TABLE ... ADD COLUMN statements
// applied on every init (idempotent via isDuplicateColumnErr). Extracted alongside
// schemaCreateTableStatements so the Postgres rewrite guard test can also cover the
// columns added here (e.g. boolean flags added to existing tables).
func schemaColumnMigrations() []optionalSchemaMigration {
	return []optionalSchemaMigration{
		{name: "scenario_data_pool_bindings.scope", query: `ALTER TABLE scenario_data_pool_bindings ADD COLUMN scope TEXT`},
		{name: "scenario_data_pool_bindings.project_table_id", query: `ALTER TABLE scenario_data_pool_bindings ADD COLUMN project_table_id TEXT`},
		{name: "scenario_data_pool_bindings.live_options_json", query: `ALTER TABLE scenario_data_pool_bindings ADD COLUMN live_options_json TEXT`},
		{name: "scripts.content_revision", query: `ALTER TABLE scripts ADD COLUMN content_revision TEXT`},
		{name: "scenarios.content_revision", query: `ALTER TABLE scenarios ADD COLUMN content_revision TEXT`},
		{name: "scenarios.materialization_key", query: `ALTER TABLE scenarios ADD COLUMN materialization_key TEXT`},
		{name: "scripts.goal_config_json", query: `ALTER TABLE scripts ADD COLUMN goal_config_json TEXT`},
		{name: "run_monitor_collector_readiness.instance_id", query: `ALTER TABLE run_monitor_collector_readiness ADD COLUMN instance_id TEXT`},
		{name: "run_monitor_collector_readiness.capabilities_json", query: `ALTER TABLE run_monitor_collector_readiness ADD COLUMN capabilities_json TEXT`},
		{name: "run_monitor_collector_readiness.management_scopes_json", query: `ALTER TABLE run_monitor_collector_readiness ADD COLUMN management_scopes_json TEXT`},
		{name: "run_monitor_jobs.source_id", query: `ALTER TABLE run_monitor_jobs ADD COLUMN source_id TEXT`},
		{name: "run_monitor_jobs.management_config_key", query: `ALTER TABLE run_monitor_jobs ADD COLUMN management_config_key TEXT`},
		{name: "run_monitor_batches.evidence_version", query: `ALTER TABLE run_monitor_batches ADD COLUMN evidence_version BIGINT`},
		{name: "run_monitor_batches.management_snapshot_count", query: `ALTER TABLE run_monitor_batches ADD COLUMN management_snapshot_count BIGINT`},
		{name: "run_monitor_batches.management_row_count", query: `ALTER TABLE run_monitor_batches ADD COLUMN management_row_count BIGINT`},
		{name: "run_monitor_batches.collection_generation", query: `ALTER TABLE run_monitor_batches ADD COLUMN collection_generation BIGINT`},
		{name: "run_monitor_batches.management_poll_id", query: `ALTER TABLE run_monitor_batches ADD COLUMN management_poll_id TEXT`},

		{name: "appdynamics_inspections.provider", query: `ALTER TABLE appdynamics_inspections ADD COLUMN provider TEXT NOT NULL DEFAULT 'appdynamics'`},
		{name: "dynatrace_connections.revision", query: `ALTER TABLE dynatrace_connections ADD COLUMN revision BIGINT NOT NULL DEFAULT 1`},
		{name: "dynatrace_connections.verified_revision", query: `ALTER TABLE dynatrace_connections ADD COLUMN verified_revision BIGINT NOT NULL DEFAULT 0`},
		{name: "dynatrace_connections.native_config_json", query: `ALTER TABLE dynatrace_connections ADD COLUMN native_config_json TEXT NOT NULL DEFAULT '{}'`},
		{name: "object_activity_events.status", query: `ALTER TABLE object_activity_events ADD COLUMN status TEXT NOT NULL DEFAULT 'success'`},
		{name: "object_archive_states.restored_at", query: `ALTER TABLE object_archive_states ADD COLUMN restored_at DATETIME`},
		{name: "run_archive_collection_intents.cache_limit_bytes", query: `ALTER TABLE run_archive_collection_intents ADD COLUMN cache_limit_bytes BIGINT NOT NULL DEFAULT 0`},
		{name: "run_archive_collection_intents.cached_sources_json", query: `ALTER TABLE run_archive_collection_intents ADD COLUMN cached_sources_json TEXT NOT NULL DEFAULT '{}'`},
		{name: "scenarios.termination_mode", query: `ALTER TABLE scenarios ADD COLUMN termination_mode TEXT NOT NULL DEFAULT 'duration'`},
		{name: "tests.termination_mode", query: `ALTER TABLE tests ADD COLUMN termination_mode TEXT NOT NULL DEFAULT 'duration'`},
		// Defaults to 0 (no sanitized document). Existing rows are all Bruno
		// imports that do have one, and are backfilled to 1 immediately after
		// this runs -- see backfillSanitizedSourceAvailability. Doing it in that
		// order means a database interrupted between the two steps withholds a
		// download it could have served, rather than serving one it should not.
		{name: "bruno_source_revisions.sanitized_available", query: `ALTER TABLE bruno_source_revisions ADD COLUMN sanitized_available INTEGER NOT NULL DEFAULT 0`},
		{name: "scheduler_leases.generation", query: `ALTER TABLE scheduler_leases ADD COLUMN generation BIGINT NOT NULL DEFAULT 1`},
		{name: "scenario_schedule_occurrences.revision", query: `ALTER TABLE scenario_schedule_occurrences ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "scenario_schedule_occurrences.transition_token", query: `ALTER TABLE scenario_schedule_occurrences ADD COLUMN transition_token TEXT`},
		{name: "users.session_epoch", query: `ALTER TABLE users ADD COLUMN session_epoch INTEGER NOT NULL DEFAULT 0`},
		{name: "tests.runtime_settings_json", query: `ALTER TABLE tests ADD COLUMN runtime_settings_json TEXT`},
		{name: "tests.data_pools_json", query: `ALTER TABLE tests ADD COLUMN data_pools_json TEXT`},
		{name: "tests.materialization_key", query: `ALTER TABLE tests ADD COLUMN materialization_key TEXT`},
		{name: "tests.paused_at", query: `ALTER TABLE tests ADD COLUMN paused_at DATETIME`},
		{name: "tests.cumulative_pause_ns", query: `ALTER TABLE tests ADD COLUMN cumulative_pause_ns INTEGER NOT NULL DEFAULT 0`},
		{name: "tests.capacity_window_start_at", query: `ALTER TABLE tests ADD COLUMN capacity_window_start_at DATETIME`},
		{name: "tests.capacity_window_end_at", query: `ALTER TABLE tests ADD COLUMN capacity_window_end_at DATETIME`},
		{name: "tests.scenario_mode", query: `ALTER TABLE tests ADD COLUMN scenario_mode TEXT`},
		{name: "tests.goal_config_json", query: `ALTER TABLE tests ADD COLUMN goal_config_json TEXT`},
		{name: "tests.traffic_profile_json", query: `ALTER TABLE tests ADD COLUMN traffic_profile_json TEXT`},
		{name: "run_monitor_observation_heads.evidence_service", query: `ALTER TABLE run_monitor_observation_heads ADD COLUMN evidence_service TEXT NOT NULL DEFAULT ''`},
		{name: "run_monitor_observation_heads.evidence_severity", query: `ALTER TABLE run_monitor_observation_heads ADD COLUMN evidence_severity TEXT NOT NULL DEFAULT ''`},
		{name: "run_monitor_observation_heads.evidence_trace_id", query: `ALTER TABLE run_monitor_observation_heads ADD COLUMN evidence_trace_id TEXT NOT NULL DEFAULT ''`},
		{name: "run_monitor_observation_heads.evidence_time", query: `ALTER TABLE run_monitor_observation_heads ADD COLUMN evidence_time TEXT NOT NULL DEFAULT ''`},
		{name: "run_monitor_observation_heads.record_bytes", query: `ALTER TABLE run_monitor_observation_heads ADD COLUMN record_bytes BIGINT NOT NULL DEFAULT 0`},
		{name: "elk_connections.elastic_profile_json", query: `ALTER TABLE elk_connections ADD COLUMN elastic_profile_json TEXT NOT NULL DEFAULT ''`},
		{name: "elastic_inspections.cleanup_json", query: `ALTER TABLE elastic_inspections ADD COLUMN cleanup_json TEXT NOT NULL DEFAULT ''`},
		{name: "tests.elk_capture_json", query: `ALTER TABLE tests ADD COLUMN elk_capture_json TEXT`},
		{name: "tests.dynatrace_capture_json", query: `ALTER TABLE tests ADD COLUMN dynatrace_capture_json TEXT`},
		{name: "tests.monitor_bindings_json", query: `ALTER TABLE tests ADD COLUMN monitor_bindings_json TEXT`},
		{name: "tests.trace_context_json", query: `ALTER TABLE tests ADD COLUMN trace_context_json TEXT`},
		{name: "tests.joint_evidence_json", query: `ALTER TABLE tests ADD COLUMN joint_evidence_json TEXT`},
		{name: "tests.analysis_policy_selections_json", query: `ALTER TABLE tests ADD COLUMN analysis_policy_selections_json TEXT`},
		{name: "tests.telemetry_acceptance_policy_id", query: `ALTER TABLE tests ADD COLUMN telemetry_acceptance_policy_id TEXT`},
		{name: "tests.start_authorization_json", query: `ALTER TABLE tests ADD COLUMN start_authorization_json TEXT`},
		{name: "tests.timeslot_id", query: `ALTER TABLE tests ADD COLUMN timeslot_id TEXT`},
		booleanColumnMigration("tests", "shakeout_enabled", false, false),
		booleanColumnMigration("tests", "resource_baseline_enabled", false, false),
		{name: "tests.start_delay", query: `ALTER TABLE tests ADD COLUMN start_delay TEXT`},
		{name: "tests.initial_provenance_json", query: `ALTER TABLE tests ADD COLUMN initial_provenance_json TEXT`},
		{name: "tests.telemetry_checkpoint_json", query: `ALTER TABLE tests ADD COLUMN telemetry_checkpoint_json TEXT`},
		{name: "tests.telemetry_checkpoint_revision", query: `ALTER TABLE tests ADD COLUMN telemetry_checkpoint_revision BIGINT NOT NULL DEFAULT 0`},
		{name: "tests.continuity_checkpoint_json", query: `ALTER TABLE tests ADD COLUMN continuity_checkpoint_json TEXT`},
		{name: "tests.execution_generation", query: `ALTER TABLE tests ADD COLUMN execution_generation BIGINT NOT NULL DEFAULT 0`},
		{name: "scenarios.assignment_json", query: `ALTER TABLE scenarios ADD COLUMN assignment_json TEXT`},
		{name: "scenarios.start_delay", query: `ALTER TABLE scenarios ADD COLUMN start_delay TEXT`},
		{name: "scenarios.traffic_profile_json", query: `ALTER TABLE scenarios ADD COLUMN traffic_profile_json TEXT`},
		{name: "tests.assignment_json", query: `ALTER TABLE tests ADD COLUMN assignment_json TEXT`},
		{name: "scripts.metadata_json", query: `ALTER TABLE scripts ADD COLUMN metadata_json TEXT`},
		booleanColumnMigration("object_repositories", "auto_promote_on_heal", false, false),
		{name: "projects.settings_json", query: `ALTER TABLE projects ADD COLUMN settings_json TEXT`},
		{name: "projects.domain_id", query: `ALTER TABLE projects ADD COLUMN domain_id TEXT REFERENCES domains(id)`},
		{name: "projects.category_id", query: `ALTER TABLE projects ADD COLUMN category_id TEXT REFERENCES repository_categories(id)`},
		{name: "applications.project_id", query: `ALTER TABLE applications ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "releases.project_id", query: `ALTER TABLE releases ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "releases.group_id", query: `ALTER TABLE releases ADD COLUMN group_id TEXT`},
		{name: "scripts.project_id", query: `ALTER TABLE scripts ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "scripts.folder_id", query: `ALTER TABLE scripts ADD COLUMN folder_id TEXT REFERENCES folders(id)`},
		{name: "scripts.parameters", query: `ALTER TABLE scripts ADD COLUMN parameters TEXT`},
		{name: "script_recordings.correlation_observations_json", query: `ALTER TABLE script_recordings ADD COLUMN correlation_observations_json TEXT`},
		{name: "script_recordings.action_graph_digest", query: `ALTER TABLE script_recordings ADD COLUMN action_graph_digest TEXT`},
		{name: "script_recordings.project_id", query: `ALTER TABLE script_recordings ADD COLUMN project_id TEXT`},
		{name: "script_recordings.script_incarnation", query: `ALTER TABLE script_recordings ADD COLUMN script_incarnation TEXT`},
		{name: "script_recordings.observations_projection_version", query: `ALTER TABLE script_recordings ADD COLUMN observations_projection_version INTEGER NOT NULL DEFAULT 0`},
		{name: "script_recordings.observations_projected_at", query: `ALTER TABLE script_recordings ADD COLUMN observations_projected_at DATETIME`},
		{name: "script_recordings.correlation_status_version", query: `ALTER TABLE script_recordings ADD COLUMN correlation_status_version INTEGER NOT NULL DEFAULT 0`},
		{name: "script_recordings.correlation_observation_count", query: `ALTER TABLE script_recordings ADD COLUMN correlation_observation_count INTEGER NOT NULL DEFAULT 0`},
		{name: "script_recordings.correlation_sidecar_version", query: `ALTER TABLE script_recordings ADD COLUMN correlation_sidecar_version INTEGER NOT NULL DEFAULT 0`},
		{name: "script_recordings.correlation_status_projected_at", query: `ALTER TABLE script_recordings ADD COLUMN correlation_status_projected_at DATETIME`},
		{name: "correlation_rulesets.rules_projection_version", query: `ALTER TABLE correlation_rulesets ADD COLUMN rules_projection_version INTEGER NOT NULL DEFAULT 0`},
		{name: "correlation_rulesets.rules_projected_at", query: `ALTER TABLE correlation_rulesets ADD COLUMN rules_projected_at DATETIME`},
		{name: "tests.project_id", query: `ALTER TABLE tests ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "tests.folder_id", query: `ALTER TABLE tests ADD COLUMN folder_id TEXT REFERENCES folders(id)`},
		{name: "tests.application_id", query: `ALTER TABLE tests ADD COLUMN application_id TEXT REFERENCES applications(id)`},
		{name: "tests.release_id", query: `ALTER TABLE tests ADD COLUMN release_id TEXT REFERENCES releases(id)`},
		{name: "external_result_imports.application_id", query: `ALTER TABLE external_result_imports ADD COLUMN application_id TEXT REFERENCES applications(id)`},
		{name: "external_result_imports.archive_record_json", query: `ALTER TABLE external_result_imports ADD COLUMN archive_record_json TEXT`},
		{name: "tests.scenario_id", query: `ALTER TABLE tests ADD COLUMN scenario_id TEXT REFERENCES scenarios(id)`},
		{name: "tests.scheduled_start_at", query: `ALTER TABLE tests ADD COLUMN scheduled_start_at DATETIME`},
		{name: "tests.started_at", query: `ALTER TABLE tests ADD COLUMN started_at DATETIME`},
		{name: "tests.pending_since", query: `ALTER TABLE tests ADD COLUMN pending_since DATETIME`},
		booleanColumnMigration("tests", "baseline_pinned", false, false),
		{name: "tests.baseline_pinned_at", query: `ALTER TABLE tests ADD COLUMN baseline_pinned_at DATETIME`},
		{name: "shared_data_pools.project_id", query: `ALTER TABLE shared_data_pools ADD COLUMN project_id TEXT REFERENCES projects(id)`},
		{name: "shared_data_pools.rows_revision", query: `ALTER TABLE shared_data_pools ADD COLUMN rows_revision BIGINT NOT NULL DEFAULT 0`},
		{name: "shared_data_pools.row_count", query: `ALTER TABLE shared_data_pools ADD COLUMN row_count BIGINT NOT NULL DEFAULT 0`},
		{name: "shared_data_pools.columns_json", query: `ALTER TABLE shared_data_pools ADD COLUMN columns_json TEXT`},
		{name: "releases.archived_at", query: `ALTER TABLE releases ADD COLUMN archived_at DATETIME`},
		{name: "test_interval_summaries.schema_version", query: `ALTER TABLE test_interval_summaries ADD COLUMN schema_version TEXT`},
		{name: "run_archives.schema_version", query: `ALTER TABLE run_archives ADD COLUMN schema_version TEXT`},
		{name: "run_archives.archive_backend", query: `ALTER TABLE run_archives ADD COLUMN archive_backend TEXT`},
		{name: "run_archives.archive_uri", query: `ALTER TABLE run_archives ADD COLUMN archive_uri TEXT`},
		{name: "run_archives.manifest_uri", query: `ALTER TABLE run_archives ADD COLUMN manifest_uri TEXT`},
		{name: "run_archives.manifest_sha256", query: `ALTER TABLE run_archives ADD COLUMN manifest_sha256 TEXT`},
		{name: "run_archives.trend_point_json", query: `ALTER TABLE run_archives ADD COLUMN trend_point_json TEXT`},
		{name: "archive_finalization_jobs.generation", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN generation INTEGER NOT NULL DEFAULT 1`},
		{name: "archive_finalization_jobs.intent_token", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN intent_token TEXT NOT NULL DEFAULT ''`},
		{name: "archive_finalization_jobs.refresh_token", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN refresh_token TEXT NOT NULL DEFAULT ''`},
		{name: "archive_finalization_jobs.prepared_manifest_json", query: `ALTER TABLE archive_finalization_jobs ADD COLUMN prepared_manifest_json TEXT NOT NULL DEFAULT ''`},
		{name: "test_interval_summaries.dropped_results_available", query: `ALTER TABLE test_interval_summaries ADD COLUMN dropped_results_available BOOLEAN`},
		{name: "test_interval_summaries.generator_published_at", query: `ALTER TABLE test_interval_summaries ADD COLUMN generator_published_at DATETIME`},
		{name: "test_interval_summaries.controller_received_at", query: `ALTER TABLE test_interval_summaries ADD COLUMN controller_received_at DATETIME`},
		{name: "test_interval_summaries.generator_processing_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN generator_processing_lag_ms REAL`},
		{name: "test_interval_summaries.transport_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN transport_lag_ms REAL`},
		{name: "test_interval_summaries.controller_fanout_lag_ms", query: `ALTER TABLE test_interval_summaries ADD COLUMN controller_fanout_lag_ms REAL`},
		{name: "test_interval_summaries.java_http_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN java_http_json TEXT`},
		{name: "test_interval_summaries.messaging_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN messaging_json TEXT`},
		{name: "test_interval_summaries.messaging_progress_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN messaging_progress_json TEXT`},
		{name: "test_interval_summaries.native_evidence_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN native_evidence_json TEXT`},
		{name: "test_interval_summaries.transactions_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN transactions_json TEXT`},
		{name: "test_interval_summaries.by_region_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN by_region_json TEXT`},
		{name: "test_interval_summaries.browser_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN browser_json TEXT`},
		{name: "test_interval_summaries.browser_by_device_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN browser_by_device_json TEXT`},
		{name: "test_interval_summaries.latency_json", query: `ALTER TABLE test_interval_summaries ADD COLUMN latency_json TEXT`},
		booleanColumnMigration("service_accounts", "active", true, true),
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
		{name: "scenario_scripts.iterations", query: `ALTER TABLE scenario_scripts ADD COLUMN iterations INTEGER`},
		{name: "scenario_scripts.role", query: `ALTER TABLE scenario_scripts ADD COLUMN role TEXT`},
		{name: "scenario_scripts.runtime_settings_json", query: `ALTER TABLE scenario_scripts ADD COLUMN runtime_settings_json TEXT`},
		{name: "scenario_scripts.assignment_json", query: `ALTER TABLE scenario_scripts ADD COLUMN assignment_json TEXT`},
		{name: "scenario_scripts.start_offset", query: `ALTER TABLE scenario_scripts ADD COLUMN start_offset TEXT`},
		{name: "scenario_scripts.transaction_response_time_sla_ms", query: `ALTER TABLE scenario_scripts ADD COLUMN transaction_response_time_sla_ms REAL`},
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
		{name: "scenarios.workload_shape_id", query: `ALTER TABLE scenarios ADD COLUMN workload_shape_id TEXT`},
		{name: "tests.ref_key", query: `ALTER TABLE tests ADD COLUMN ref_key TEXT`},
		{name: "run_archives.ref_key", query: `ALTER TABLE run_archives ADD COLUMN ref_key TEXT`},
		{name: "grafana_import_sessions.source_sha256", query: `ALTER TABLE grafana_import_sessions ADD COLUMN source_sha256 TEXT NOT NULL DEFAULT ''`},
		{name: "github_script_links.remote_blob_sha", query: `ALTER TABLE github_script_links ADD COLUMN remote_blob_sha TEXT`},
		{name: "github_script_links.remote_commit_sha", query: `ALTER TABLE github_script_links ADD COLUMN remote_commit_sha TEXT`},
		{name: "github_script_links.base_branch", query: `ALTER TABLE github_script_links ADD COLUMN base_branch TEXT`},
		{name: "github_script_links.base_blob_sha", query: `ALTER TABLE github_script_links ADD COLUMN base_blob_sha TEXT`},
		booleanColumnMigration("github_script_links", "base_missing", true, false),
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
		{name: "execution_plan_schedule_occurrences.approval_id", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN approval_id TEXT`},
		{name: "execution_plan_schedule_occurrences.actor_type", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'legacy'`},
		{name: "execution_plan_schedule_occurrences.actor_subject", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN actor_subject TEXT NOT NULL DEFAULT 'controller:execution-plan-schedule-occurrence-legacy'`},
		{name: "execution_plan_schedule_occurrences.actor_display_name", query: `ALTER TABLE execution_plan_schedule_occurrences ADD COLUMN actor_display_name TEXT NOT NULL DEFAULT ''`},
		{name: "execution_plan_schedules.actor_type", query: `ALTER TABLE execution_plan_schedules ADD COLUMN actor_type TEXT NOT NULL DEFAULT 'legacy'`},
		{name: "execution_plan_schedules.actor_subject", query: `ALTER TABLE execution_plan_schedules ADD COLUMN actor_subject TEXT NOT NULL DEFAULT 'controller:execution-plan-schedule-legacy'`},
		{name: "execution_plan_schedules.actor_display_name", query: `ALTER TABLE execution_plan_schedules ADD COLUMN actor_display_name TEXT NOT NULL DEFAULT ''`},
		{name: "execution_plans.application_id", query: `ALTER TABLE execution_plans ADD COLUMN application_id TEXT`},
		{name: "execution_plans.release_id", query: `ALTER TABLE execution_plans ADD COLUMN release_id TEXT`},
		{name: "execution_plans.overlap_policy", query: `ALTER TABLE execution_plans ADD COLUMN overlap_policy TEXT`},
		{name: "execution_plan_schedules.release_id", query: `ALTER TABLE execution_plan_schedules ADD COLUMN release_id TEXT`},
		{name: "tests.execution_plan_json", query: `ALTER TABLE tests ADD COLUMN execution_plan_json TEXT`},
		{name: "provisioning_requests.revision", query: `ALTER TABLE provisioning_requests ADD COLUMN revision INTEGER NOT NULL DEFAULT 1`},
		{name: "provisioning_requests.attempt_token", query: `ALTER TABLE provisioning_requests ADD COLUMN attempt_token TEXT`},
		{name: "provisioning_requests.execution_target_id", query: `ALTER TABLE provisioning_requests ADD COLUMN execution_target_id TEXT`},
		{name: "provisioning_requests.provider_operation_id", query: `ALTER TABLE provisioning_requests ADD COLUMN provider_operation_id TEXT`},
		{name: "provisioning_requests.idempotency_key", query: `ALTER TABLE provisioning_requests ADD COLUMN idempotency_key TEXT`},
		{name: "provisioning_requests.observed_count", query: `ALTER TABLE provisioning_requests ADD COLUMN observed_count INTEGER NOT NULL DEFAULT 0`},
		{name: "provisioning_requests.ready_count", query: `ALTER TABLE provisioning_requests ADD COLUMN ready_count INTEGER NOT NULL DEFAULT 0`},
		{name: "provisioning_requests.pending_count", query: `ALTER TABLE provisioning_requests ADD COLUMN pending_count INTEGER NOT NULL DEFAULT 0`},
		{name: "provisioning_requests.draining_count", query: `ALTER TABLE provisioning_requests ADD COLUMN draining_count INTEGER NOT NULL DEFAULT 0`},
		{name: "provisioning_requests.failed_count", query: `ALTER TABLE provisioning_requests ADD COLUMN failed_count INTEGER NOT NULL DEFAULT 0`},
		{name: "provisioning_requests.deadline_at", query: `ALTER TABLE provisioning_requests ADD COLUMN deadline_at DATETIME`},
		{name: "provisioning_requests.last_error", query: `ALTER TABLE provisioning_requests ADD COLUMN last_error TEXT`},
		{name: "capacity_instance_intents.generator_id", query: `ALTER TABLE capacity_instance_intents ADD COLUMN generator_id TEXT NOT NULL DEFAULT ''`},
		{name: "protected_source_secret_sessions.claim_token", query: `ALTER TABLE protected_source_secret_sessions ADD COLUMN claim_token TEXT`},
		{name: "protected_source_secret_sessions.claim_expires_at", query: `ALTER TABLE protected_source_secret_sessions ADD COLUMN claim_expires_at DATETIME`},
		{name: "runtime_secret_provisioning_attempts.materialization_key", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN materialization_key TEXT NOT NULL DEFAULT ''`},
		{name: "runtime_secret_provisioning_attempts.ref_key", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN ref_key TEXT NOT NULL DEFAULT ''`},
		{name: "runtime_secret_provisioning_attempts.resource_type", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN resource_type TEXT NOT NULL DEFAULT 'test'`},
		{name: "runtime_secret_provisioning_attempts.commit_fingerprint", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN commit_fingerprint TEXT NOT NULL DEFAULT ''`},
		{name: "runtime_secret_provisioning_attempts.audit_actor_type", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN audit_actor_type TEXT NOT NULL DEFAULT 'system'`},
		{name: "runtime_secret_provisioning_attempts.audit_actor_subject", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN audit_actor_subject TEXT NOT NULL DEFAULT 'controller:test-creation'`},
		{name: "runtime_secret_provisioning_attempts.audit_actor_display_name", query: `ALTER TABLE runtime_secret_provisioning_attempts ADD COLUMN audit_actor_display_name TEXT NOT NULL DEFAULT ''`},
		{name: "bruno_native_import_batches.source_profile", query: `ALTER TABLE bruno_native_import_batches ADD COLUMN source_profile TEXT NOT NULL DEFAULT 'bruno_native'`},
		{name: "bruno_native_import_batches.receipt_version", query: `ALTER TABLE bruno_native_import_batches ADD COLUMN receipt_version INTEGER NOT NULL DEFAULT 1`},
		{name: "bruno_native_import_batches.request_digest_profile", query: `ALTER TABLE bruno_native_import_batches ADD COLUMN request_digest_profile TEXT NOT NULL DEFAULT 'bruno-native-commit-v1'`},
		{name: "bruno_native_import_batches.scripts_json", query: `ALTER TABLE bruno_native_import_batches ADD COLUMN scripts_json TEXT NOT NULL DEFAULT '[]'`},
		// An empty cluster_id means "use the controller's env-configured
		// provisioner", which is exactly the behavior every pre-existing pool
		// had. That default is what keeps the existing deployment path intact.
		{name: "fleet_pools.cluster_id", query: `ALTER TABLE fleet_pools ADD COLUMN cluster_id TEXT NOT NULL DEFAULT ''`},
		{name: "fleet_pools.execution_target_id", query: `ALTER TABLE fleet_pools ADD COLUMN execution_target_id TEXT NOT NULL DEFAULT ''`},
		{name: "fleet_pools.allocation_json", query: `ALTER TABLE fleet_pools ADD COLUMN allocation_json TEXT NOT NULL DEFAULT ''`},
		{name: "fleet_pools.lifecycle_mode", query: `ALTER TABLE fleet_pools ADD COLUMN lifecycle_mode TEXT NOT NULL DEFAULT 'standing'`},
		{name: "fleet_pools.warm_floor", query: `ALTER TABLE fleet_pools ADD COLUMN warm_floor INTEGER NOT NULL DEFAULT 0`},
		{name: "fleet_pools.readiness_timeout_seconds", query: `ALTER TABLE fleet_pools ADD COLUMN readiness_timeout_seconds INTEGER NOT NULL DEFAULT 300`},
		{name: "fleet_pools.idle_grace_seconds", query: `ALTER TABLE fleet_pools ADD COLUMN idle_grace_seconds INTEGER NOT NULL DEFAULT 300`},
		{name: "fleet_pools.runtime_profile_id", query: `ALTER TABLE fleet_pools ADD COLUMN runtime_profile_id TEXT NOT NULL DEFAULT ''`},
		{name: "fleet_pools.capacity_admission", query: `ALTER TABLE fleet_pools ADD COLUMN capacity_admission TEXT NOT NULL DEFAULT 'strict'`},
		// An empty domain_id means the pool is NOT domain-scoped: the per-domain
		// deployment-target constraint simply does not apply to it. That is
		// deliberately the value every pre-existing row gets, so a superuser
		// setting a target can never break a pool created before domain scoping
		// existed. It mirrors cluster_id, which carries the same "empty means
		// the old behavior" contract.
		//
		// Backfilling to DefaultDomainID (as projects.domain_id does) would be
		// wrong here: project domain scoping is an org-chart concern, but pool
		// domain scoping is a deploy blast-radius concern, and a backfill would
		// silently subject every legacy pool to a constraint nobody chose.
		//
		// NO FOREIGN KEY is declared: SQLite runs with foreign_keys=ON and ''
		// satisfies no reference. Integrity for a non-empty value is enforced
		// on the write path instead.
		{name: "fleet_pools.domain_id", query: `ALTER TABLE fleet_pools ADD COLUMN domain_id TEXT NOT NULL DEFAULT ''`},
		{name: "domain_deployment_targets.default_execution_target_id", query: `ALTER TABLE domain_deployment_targets ADD COLUMN default_execution_target_id TEXT`},
		{name: "execution_targets.reverse_token_hash", query: `ALTER TABLE execution_targets ADD COLUMN reverse_token_hash TEXT NOT NULL DEFAULT ''`},
		{name: "execution_targets.reverse_token_expires_at", query: `ALTER TABLE execution_targets ADD COLUMN reverse_token_expires_at TEXT`},
		{name: "execution_targets.reverse_previous_token_hash", query: `ALTER TABLE execution_targets ADD COLUMN reverse_previous_token_hash TEXT NOT NULL DEFAULT ''`},
		{name: "execution_targets.reverse_previous_token_valid_until", query: `ALTER TABLE execution_targets ADD COLUMN reverse_previous_token_valid_until TEXT`},
		// EKS support. Every column is TEXT so none of them needs a coordinated
		// entry in rewriteSQL's boolean replacement list.
		{name: "cluster_connections.aws_region", query: `ALTER TABLE cluster_connections ADD COLUMN aws_region TEXT`},
		{name: "cluster_connections.aws_cluster_name", query: `ALTER TABLE cluster_connections ADD COLUMN aws_cluster_name TEXT`},
		{name: "cluster_connections.aws_credential_mode", query: `ALTER TABLE cluster_connections ADD COLUMN aws_credential_mode TEXT`},
		{name: "cluster_connections.aws_access_key_id", query: `ALTER TABLE cluster_connections ADD COLUMN aws_access_key_id TEXT`},
		{name: "cluster_connections.aws_role_arn", query: `ALTER TABLE cluster_connections ADD COLUMN aws_role_arn TEXT`},
		{name: "cluster_connections.aws_sts_endpoint", query: `ALTER TABLE cluster_connections ADD COLUMN aws_sts_endpoint TEXT`},
		{name: "cluster_connections.aws_secret_key_ciphertext", query: `ALTER TABLE cluster_connections ADD COLUMN aws_secret_key_ciphertext TEXT`},
		{name: "cluster_connections.aws_secret_key_wrapped_key", query: `ALTER TABLE cluster_connections ADD COLUMN aws_secret_key_wrapped_key TEXT`},
		{name: "cluster_connections.aws_external_id_ciphertext", query: `ALTER TABLE cluster_connections ADD COLUMN aws_external_id_ciphertext TEXT`},
		{name: "cluster_connections.aws_external_id_wrapped_key", query: `ALTER TABLE cluster_connections ADD COLUMN aws_external_id_wrapped_key TEXT`},
		{name: "feature_evaluation_jobs.request_digest", query: `ALTER TABLE feature_evaluation_jobs ADD COLUMN request_digest TEXT NOT NULL DEFAULT ''`},
		{name: "feature_evaluation_jobs.attempts", query: `ALTER TABLE feature_evaluation_jobs ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`},
		{name: "feature_evaluation_jobs.failure_code", query: `ALTER TABLE feature_evaluation_jobs ADD COLUMN failure_code TEXT NOT NULL DEFAULT ''`},
		{name: "feature_evaluation_jobs.result_bytes", query: `ALTER TABLE feature_evaluation_jobs ADD COLUMN result_bytes INTEGER NOT NULL DEFAULT 0`},
		{name: "feature_evaluation_jobs.subject_id", query: `ALTER TABLE feature_evaluation_jobs ADD COLUMN subject_id TEXT NOT NULL DEFAULT ''`},
		{name: "rca_analyses.origin", query: `ALTER TABLE rca_analyses ADD COLUMN origin TEXT NOT NULL DEFAULT 'requested'`},
		{name: "feature_acceptance_records.cohort_sha256", query: `ALTER TABLE feature_acceptance_records ADD COLUMN cohort_sha256 TEXT NOT NULL DEFAULT ''`},
		{name: "feature_acceptance_records.qualification", query: `ALTER TABLE feature_acceptance_records ADD COLUMN qualification TEXT NOT NULL DEFAULT 'null'`},
		{name: "feature_acceptance_records.seq", query: `ALTER TABLE feature_acceptance_records ADD COLUMN seq BIGINT NOT NULL DEFAULT 0`},
		// Original-outcome (v2) authoring receipts. Existing receipts read as
		// legacy (version 1) and keep replaying the current resource.
		{name: "authoring_mutation_receipts.outcome_version", query: `ALTER TABLE authoring_mutation_receipts ADD COLUMN outcome_version INTEGER NOT NULL DEFAULT 1`},
		{name: "authoring_mutation_receipts.original_outcome", query: `ALTER TABLE authoring_mutation_receipts ADD COLUMN original_outcome TEXT NOT NULL DEFAULT ''`},
		// A scenario's joint evidence projection, carried to every run created
		// from it. Existing scenarios have none.
		{name: "scenarios.joint_evidence_json", query: `ALTER TABLE scenarios ADD COLUMN joint_evidence_json TEXT`},
		// The telemetry acceptance policy every run created from a scenario
		// inherits (TEL-B). Existing scenarios use their project's default.
		{name: "scenarios.telemetry_acceptance_policy_id", query: `ALTER TABLE scenarios ADD COLUMN telemetry_acceptance_policy_id TEXT`},
	}
}

// schemaIndex is a CREATE INDEX statement plus the index name used in error reporting.
type schemaIndex struct {
	query string
	name  string
}

var dataPoolRowsPageIndex = schemaIndex{
	query: `CREATE INDEX IF NOT EXISTS idx_shared_data_pool_rows_pool_created_id ON shared_data_pool_rows(pool_id, created_at, id)`,
	name:  "idx_shared_data_pool_rows_pool_created_id",
}

// schemaIndexes holds the unique/partial indexes created after the base tables and
// migrations. Unique indexes guard against duplicate reference keys; partial
// indexes skip NULL/empty values so legacy rows awaiting backfill do not collide.
var schemaIndexes = []schemaIndex{
	{`CREATE INDEX IF NOT EXISTS idx_telemetry_notification_run ON telemetry_notification_events(test_id,id)`, "idx_telemetry_notification_run"},
	{`CREATE INDEX IF NOT EXISTS idx_telemetry_notification_due ON telemetry_notification_deliveries(state,next_attempt,event_id,destination)`, "idx_telemetry_notification_due"},

	{`CREATE INDEX IF NOT EXISTS idx_prometheus_connections_project ON prometheus_connections(project_id,name,id)`, "idx_prometheus_connections_project"},
	{name: "idx_monitor_evidence_scope_time", query: `CREATE INDEX IF NOT EXISTS idx_monitor_evidence_scope_time ON run_monitor_observation_heads(job_id,diagnostic_kind,evidence_time,record_key) WHERE record_kind='diagnostic'`},
	{name: "idx_monitor_evidence_trace", query: `CREATE INDEX IF NOT EXISTS idx_monitor_evidence_trace ON run_monitor_observation_heads(job_id,evidence_trace_id,record_key) WHERE record_kind='diagnostic'`},
	{`CREATE INDEX IF NOT EXISTS idx_elastic_inspections_poll ON elastic_inspections(collector_id,state,expires_at)`, "idx_elastic_inspections_poll"},
	{`CREATE INDEX IF NOT EXISTS idx_appdynamics_quota_expiry ON appdynamics_quota_leases(resource_key,expires_at)`, "idx_appdynamics_quota_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_object_activity_scope_time ON object_activity_events(project_id,created_at,id)`, "idx_object_activity_scope_time"},
	{`CREATE INDEX IF NOT EXISTS idx_object_activity_resource_time ON object_activity_events(project_id,resource_type,resource_id,created_at)`, "idx_object_activity_resource_time"},
	{`CREATE INDEX IF NOT EXISTS idx_object_activity_summary ON object_activity_events(project_id,resource_type,resource_id,action,status,created_at)`, "idx_object_activity_summary"},
	{`CREATE INDEX IF NOT EXISTS idx_preparations_test ON preparations(test_id, expires_at)`, "idx_preparations_test"},
	{`CREATE INDEX IF NOT EXISTS idx_analysis_upload_cursor ON analysis_upload_writers(project_id,import_id,file_id,chunk_offset)`, "idx_analysis_upload_cursor"},
	{`CREATE INDEX IF NOT EXISTS idx_analysis_attempt_project_import ON analysis_attempt_journal(project_id,import_id)`, "idx_analysis_attempt_project_import"},
	{`CREATE INDEX IF NOT EXISTS idx_analysis_imports_project ON analysis_imports(project_id, state)`, "idx_analysis_imports_project"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_jobs_source ON run_monitor_jobs(project_id,source_id,state)`, "idx_monitor_jobs_source"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_jobs_management ON run_monitor_jobs(project_id,source_id,management_config_key,state,expires_at)`, "idx_monitor_jobs_management"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_polls_source ON monitor_management_polls(project_id,source_id,poll_sequence)`, "idx_monitor_polls_source"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_polls_retention ON monitor_management_polls(created_at,id,project_id) WHERE state IN ('accepted','abandoned')`, "idx_monitor_polls_retention"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_polls_configuration ON monitor_management_polls(project_id,source_id,collector_id,source_revision,scope_hash,identity_hash,poll_sequence)`, "idx_monitor_polls_configuration"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_batches_poll ON run_monitor_batches(management_poll_id)`, "idx_monitor_batches_poll"},
	{`CREATE INDEX IF NOT EXISTS idx_monitor_sources_project ON monitor_sources(project_id)`, "idx_monitor_sources_project"},
	{`CREATE INDEX IF NOT EXISTS idx_run_monitor_jobs_poll ON run_monitor_jobs(collector_id, state, expires_at)`, "idx_run_monitor_jobs_poll"},
	{`CREATE INDEX IF NOT EXISTS idx_run_monitor_jobs_test ON run_monitor_jobs(test_id)`, "idx_run_monitor_jobs_test"},
	{`CREATE INDEX IF NOT EXISTS idx_elk_connections_project ON elk_connections(project_id)`, "idx_elk_connections_project"},
	{`CREATE INDEX IF NOT EXISTS idx_dynatrace_connections_project ON dynatrace_connections(project_id)`, "idx_dynatrace_connections_project"},
	{`CREATE INDEX IF NOT EXISTS idx_local_auth_sessions_user ON local_auth_sessions(user_id)`, "idx_local_auth_sessions_user"},
	{`CREATE INDEX IF NOT EXISTS idx_local_auth_sessions_expiry ON local_auth_sessions(expires_at)`, "idx_local_auth_sessions_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_import_assets_project ON import_assets(project_id)`, "idx_import_assets_project"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_releases_application_group ON releases(application_id, group_id) WHERE group_id IS NOT NULL AND group_id != ''`, "idx_releases_application_group"},
	{`CREATE INDEX IF NOT EXISTS idx_releases_project_group_updated ON releases(project_id, group_id, updated_at DESC, id)`, "idx_releases_project_group_updated"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_ref_key ON scripts(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_scripts_ref_key"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_scenarios_ref_key ON scenarios(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_scenarios_ref_key"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_tests_ref_key ON tests(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_tests_ref_key"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_run_archives_ref_key ON run_archives(ref_key) WHERE ref_key IS NOT NULL AND ref_key != ''`, "idx_run_archives_ref_key"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_cleanup_jobs_updated ON archive_cleanup_jobs(updated_at)`, "idx_archive_cleanup_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_version_cleanup_jobs_updated ON archive_version_cleanup_jobs(updated_at)`, "idx_archive_version_cleanup_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_version_cleanup_jobs_test ON archive_version_cleanup_jobs(test_id)`, "idx_archive_version_cleanup_jobs_test"},
	{`CREATE INDEX IF NOT EXISTS idx_run_archive_collection_pending ON run_archive_collection_intents(resolved, exhausted, updated_at)`, "idx_run_archive_collection_pending"},
	{`CREATE INDEX IF NOT EXISTS idx_run_archive_replica_holds_scope ON run_archive_replica_holds(fleet_id, namespace, workload_uid)`, "idx_run_archive_replica_holds_scope"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_finalization_jobs_updated ON archive_finalization_jobs(updated_at)`, "idx_archive_finalization_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_archive_refresh_jobs_updated ON archive_refresh_jobs(updated_at)`, "idx_archive_refresh_jobs_updated"},
	{`CREATE INDEX IF NOT EXISTS idx_test_notes_test_id ON test_notes(test_id)`, "idx_test_notes_test_id"},
	{`CREATE INDEX IF NOT EXISTS idx_powerbi_publishes_test ON powerbi_publishes(test_id)`, "idx_powerbi_publishes_test"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_confluence_auto_project ON confluence_connections(project_id) WHERE auto_publish=TRUE`, "idx_confluence_auto_project"},
	{`CREATE INDEX IF NOT EXISTS idx_confluence_publications_pending ON confluence_publications(status,updated_at)`, "idx_confluence_publications_pending"},
	{`CREATE INDEX IF NOT EXISTS idx_powerbi_publishes_dataset ON powerbi_publishes(dataset_id)`, "idx_powerbi_publishes_dataset"},
	{`CREATE INDEX IF NOT EXISTS idx_test_history_test_time ON test_history(test_id, timestamp)`, "idx_test_history_test_time"},
	{`CREATE INDEX IF NOT EXISTS idx_test_interval_summaries_test_time ON test_interval_summaries(test_id, timestamp)`, "idx_test_interval_summaries_test_time"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_schedule_time ON scenario_schedule_occurrences(schedule_id, planned_at)`, "idx_scenario_schedule_occurrences_schedule_time"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_project_time ON scenario_schedule_occurrences(project_id, planned_at)`, "idx_scenario_schedule_occurrences_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_status ON scenario_schedule_occurrences(status)`, "idx_scenario_schedule_occurrences_status"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_test_id ON scenario_schedule_occurrences(test_id)`, "idx_scenario_schedule_occurrences_test_id"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_schedule_occurrences_schedule_time ON execution_plan_schedule_occurrences(schedule_id, planned_at)`, "idx_execution_plan_schedule_occurrences_schedule_time"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_schedule_occurrences_project_time ON execution_plan_schedule_occurrences(project_id, planned_at)`, "idx_execution_plan_schedule_occurrences_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_schedule_occurrences_retention ON execution_plan_schedule_occurrences(planned_at, status)`, "idx_execution_plan_schedule_occurrences_retention"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_schedule_occurrences_retention ON scenario_schedule_occurrences(planned_at, status)`, "idx_scenario_schedule_occurrences_retention"},
	{`CREATE INDEX IF NOT EXISTS idx_replay_session_leases_state_expiry ON replay_session_leases(state, expires_at)`, "idx_replay_session_leases_state_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_replay_session_leases_project_state_expiry ON replay_session_leases(project_id, state, expires_at)`, "idx_replay_session_leases_project_state_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_replay_artifact_receipts_project ON replay_artifact_receipts(project_id)`, "idx_replay_artifact_receipts_project"},
	{`CREATE INDEX IF NOT EXISTS idx_replay_artifact_receipts_cleanup ON replay_artifact_receipts(writer_joined,expires_at,session_id)`, "idx_replay_artifact_receipts_cleanup"},
	{`CREATE INDEX IF NOT EXISTS idx_replay_session_leases_expiry ON replay_session_leases(expires_at)`, "idx_replay_session_leases_expiry"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_cluster_connections_name ON cluster_connections(name)`, "idx_cluster_connections_name"},
	{`CREATE INDEX IF NOT EXISTS idx_fleet_pools_cluster ON fleet_pools(cluster_id)`, "idx_fleet_pools_cluster"},
	{`CREATE INDEX IF NOT EXISTS idx_fleet_pools_domain ON fleet_pools(domain_id)`, "idx_fleet_pools_domain"},
	{`CREATE INDEX IF NOT EXISTS idx_domain_deployment_targets_cluster ON domain_deployment_targets(default_cluster_id)`, "idx_domain_deployment_targets_cluster"},
	{`CREATE INDEX IF NOT EXISTS idx_capacity_reservations_project_time ON capacity_reservations(project_id, start_at, end_at)`, "idx_capacity_reservations_project_time"},
	{`CREATE INDEX IF NOT EXISTS idx_capacity_reservations_bound_test ON capacity_reservations(bound_test_id)`, "idx_capacity_reservations_bound_test"},
	{`CREATE INDEX IF NOT EXISTS idx_tests_browser_baseline_inventory ON tests(COALESCE(NULLIF(project_id, ''), 'default'), COALESCE(application_id, ''), COALESCE(scenario_id, ''), status, id)`, "idx_tests_browser_baseline_inventory"},
	{`CREATE INDEX IF NOT EXISTS idx_tests_browser_baseline_inventory_v2 ON tests(` + browserBaselineProjectExpression + `, COALESCE(application_id, ''), COALESCE(scenario_id, ''), status, id)`, "idx_tests_browser_baseline_inventory_v2"},
	{`CREATE INDEX IF NOT EXISTS idx_tests_timeslot ON tests(timeslot_id)`, "idx_tests_timeslot"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_data_pool_bindings_pool ON scenario_data_pool_bindings(pool_id)`, "idx_scenario_data_pool_bindings_pool"},
	{`CREATE INDEX IF NOT EXISTS idx_shared_data_pools_project ON shared_data_pools(project_id)`, "idx_shared_data_pools_project"},
	{`CREATE INDEX IF NOT EXISTS idx_fleet_pool_members_pool ON fleet_pool_members(pool_id)`, "idx_fleet_pool_members_pool"},
	{`CREATE INDEX IF NOT EXISTS idx_generator_metadata_pool ON generator_metadata(fleet_pool_id)`, "idx_generator_metadata_pool"},
	// One generator, one pool. The table's own UNIQUE(pool_id, generator_id) only
	// stops a duplicate row within a pool; this is what stops the same generator
	// being claimed by two pools — and therefore by two domains.
	// consolidateFleetPoolMembership deduplicates before this runs.
	//
	// PARTIAL on a non-empty generator_id: a member may legitimately be
	// identified by target alone (see "target or generator_id is required"), and
	// those rows all carry '' — a total index would treat every one of them as
	// the same generator and reject the second pool that has one.
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_fleet_pool_members_generator ON fleet_pool_members(generator_id) WHERE generator_id != ''`, "idx_fleet_pool_members_generator"},
	{`CREATE INDEX IF NOT EXISTS idx_provisioning_requests_pool ON provisioning_requests(fleet_pool_id)`, "idx_provisioning_requests_pool"},
	{`CREATE INDEX IF NOT EXISTS idx_provisioning_events_request ON provisioning_events(request_id)`, "idx_provisioning_events_request"},
	{`CREATE INDEX IF NOT EXISTS idx_threshold_incidents_project ON threshold_incidents(project_id, last_seen_at, id)`, "idx_threshold_incidents_project"},
	{`CREATE INDEX IF NOT EXISTS idx_threshold_incidents_active ON threshold_incidents(test_id, active)`, "idx_threshold_incidents_active"},
	{`CREATE INDEX IF NOT EXISTS idx_threshold_evaluations_project ON threshold_evaluations(project_id, evaluated_at)`, "idx_threshold_evaluations_project"},
	{`CREATE INDEX IF NOT EXISTS idx_fleet_alert_acks_acknowledged_at ON fleet_alert_acks(acknowledged_at)`, "idx_fleet_alert_acks_acknowledged_at"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plans_project ON execution_plans(project_id)`, "idx_execution_plans_project"},
	{`CREATE INDEX IF NOT EXISTS idx_execution_plan_runs_plan ON execution_plan_runs(plan_id)`, "idx_execution_plan_runs_plan"},
	{`CREATE INDEX IF NOT EXISTS idx_object_repositories_project ON object_repositories(project_id)`, "idx_object_repositories_project"},
	{`CREATE INDEX IF NOT EXISTS idx_correlation_rulesets_project ON correlation_rulesets(project_id)`, "idx_correlation_rulesets_project"},
	{`CREATE INDEX IF NOT EXISTS idx_monitoring_collectors_project ON monitoring_collectors(project_id)`, "idx_monitoring_collectors_project"},
	{`CREATE INDEX IF NOT EXISTS idx_grafana_dashboards_project ON grafana_dashboards(project_id)`, "idx_grafana_dashboards_project"},
	{`CREATE INDEX IF NOT EXISTS idx_dashboard_definitions_project ON dashboard_definitions(project_id)`, "idx_dashboard_definitions_project"},
	{`CREATE INDEX IF NOT EXISTS idx_dashboard_definition_revisions_project ON dashboard_definition_revisions(project_id)`, "idx_dashboard_definition_revisions_project"},
	{`CREATE INDEX IF NOT EXISTS idx_grafana_import_sessions_project_expiry ON grafana_import_sessions(project_id, expires_at)`, "idx_grafana_import_sessions_project_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_grafana_dashboard_snapshots_project ON grafana_dashboard_snapshots(project_id)`, "idx_grafana_dashboard_snapshots_project"},
	{`CREATE INDEX IF NOT EXISTS idx_reusable_blocks_project ON reusable_blocks(project_id)`, "idx_reusable_blocks_project"},
	{`CREATE INDEX IF NOT EXISTS idx_scenario_drafts_project ON scenario_drafts(project_id, updated_at)`, "idx_scenario_drafts_project"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_artifacts_project_hash ON bruno_source_artifacts(project_id, artifact_kind, sha256)`, "idx_bruno_artifacts_project_hash"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_revisions_script_created ON bruno_source_revisions(script_id, created_at)`, "idx_bruno_revisions_script_created"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_revisions_project_active ON bruno_source_revisions(project_id, active_flag)`, "idx_bruno_revisions_project_active"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_bruno_revisions_one_active ON bruno_source_revisions(script_id, project_id) WHERE active_flag = 1`, "idx_bruno_revisions_one_active"},
	{`CREATE INDEX IF NOT EXISTS idx_bruno_native_batches_project_created ON bruno_native_import_batches(project_id, created_at)`, "idx_bruno_native_batches_project_created"},
	{`CREATE INDEX IF NOT EXISTS idx_direct_import_receipts_pending_lease ON direct_import_receipts(state, lease_expires_at)`, "idx_direct_import_receipts_pending_lease"},
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
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_claims_expiry ON github_script_sync_claims(expires_at)`, "idx_github_script_sync_claims_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_claims_project ON github_script_sync_claims(project_id)`, "idx_github_script_sync_claims_project"},
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_target_claims_operation ON github_script_sync_target_claims(script_id, claim_id)`, "idx_github_script_sync_target_claims_operation"},
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_target_claims_expiry ON github_script_sync_target_claims(expires_at)`, "idx_github_script_sync_target_claims_expiry"},
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_target_claims_project ON github_script_sync_target_claims(project_id)`, "idx_github_script_sync_target_claims_project"},
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_target_owners_script ON github_script_sync_target_owners(script_id)`, "idx_github_script_sync_target_owners_script"},
	{`CREATE INDEX IF NOT EXISTS idx_github_script_sync_target_owners_project ON github_script_sync_target_owners(project_id)`, "idx_github_script_sync_target_owners_project"},
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
	{`CREATE INDEX IF NOT EXISTS idx_rca_analyses_test_digest ON rca_analyses(test_id, input_digest)`, "idx_rca_analyses_test_digest"},
	{`CREATE INDEX IF NOT EXISTS idx_rca_analyses_claim ON rca_analyses(state, lease_expires_at)`, "idx_rca_analyses_claim"},
	{`CREATE INDEX IF NOT EXISTS idx_rca_critiques_analysis ON rca_critiques(analysis_id, created_at)`, "idx_rca_critiques_analysis"},
	{`CREATE INDEX IF NOT EXISTS idx_authoring_mutation_receipts_resource ON authoring_mutation_receipts(resource_type, resource_id)`, "idx_authoring_mutation_receipts_resource"},
	{`CREATE INDEX IF NOT EXISTS idx_authoring_mutation_receipts_unscrubbed_expiry ON authoring_mutation_receipts(expires_at) WHERE request_digest <> ''`, "idx_authoring_mutation_receipts_unscrubbed_expiry"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_feature_evaluation_jobs_key ON feature_evaluation_jobs(project_id, actor, kind, idempotency_key_hash) WHERE idempotency_key_hash <> ''`, "idx_feature_evaluation_jobs_key"},
	{`CREATE INDEX IF NOT EXISTS idx_feature_evaluation_jobs_claim ON feature_evaluation_jobs(state, created_at)`, "idx_feature_evaluation_jobs_claim"},
	{`CREATE INDEX IF NOT EXISTS idx_feature_evaluation_jobs_project ON feature_evaluation_jobs(project_id, created_at)`, "idx_feature_evaluation_jobs_project"},
	{`CREATE INDEX IF NOT EXISTS idx_feature_evaluation_jobs_subject ON feature_evaluation_jobs(project_id, kind, subject_id, created_at)`, "idx_feature_evaluation_jobs_subject"},
	{`CREATE UNIQUE INDEX IF NOT EXISTS idx_feature_acceptance_records_seq ON feature_acceptance_records(seq) WHERE seq > 0`, "idx_feature_acceptance_records_seq"},
	{`CREATE INDEX IF NOT EXISTS idx_feature_acceptance_records_subject ON feature_acceptance_records(requirement_id, feature, recorded_at)`, "idx_feature_acceptance_records_subject"},
}

func (s *SQLiteStore) init() error {
	if err := s.ensureSchemaMigrationLedger(); err != nil {
		return err
	}
	// Import tables retain their original Bruno-era physical names. Restore a
	// database opened by the short-lived format-neutral release before CREATE
	// TABLE can silently create empty legacy tables beside its populated ones.
	if err := s.ensureCompatibleImportPhysicalTableLayout(); err != nil {
		return err
	}
	for _, q := range schemaCreateTableStatementsFor(s.db.dialect) {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("failed to run migration query: %w", err)
		}
	}
	if s.db.dialect == storeDialectPostgres {
		if _, err := s.db.Exec(`ALTER TABLE bruno_source_artifacts ALTER COLUMN size_bytes TYPE BIGINT`); err != nil {
			return fmt.Errorf("migrate import artifact size to BIGINT: %w", err)
		}
	}

	if err := s.applyOptionalSchemaMigrations(schemaColumnMigrations()); err != nil {
		return err
	}
	if err := s.ensureLiveTableOptionalSeedBindings(); err != nil {
		return err
	}
	if err := s.validateScriptRevisionInitialization(); err != nil {
		return fmt.Errorf("validate persisted script content revisions: %w", err)
	}
	if err := s.validateScenarioRevisionInitialization(); err != nil {
		return fmt.Errorf("validate persisted scenario content revisions: %w", err)
	}
	if err := s.ensurePostgresIntegerColumnsBigint(); err != nil {
		return err
	}
	if err := s.backfillKubernetesExecutionTargets(); err != nil {
		return err
	}
	if err := s.backfillExecutionTargetEnrollmentExpiry(); err != nil {
		return err
	}
	if err := s.ensurePostgresSchedulerLeaseGenerationBigint(); err != nil {
		return err
	}
	// On PostgreSQL backfillObjectActivity reads audit history ordered by
	// audit_events.insert_seq, and its copies take
	// object_activity_events.insert_seq in the order they are written, so add
	// both columns before it runs.
	for _, table := range []insertSeqTable{auditEventsTable, objectActivityTable} {
		if err := s.ensurePostgresInsertSeq(table); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE releases SET group_id = id WHERE group_id IS NULL OR TRIM(group_id) = ''`); err != nil {
		return fmt.Errorf("backfill release groups: %w", err)
	}
	if err := s.ensureSchedulerContentRevisionTracking(); err != nil {
		return err
	}
	if err := s.ensureStartAdmissionRevisionTracking(); err != nil {
		return err
	}
	// The legacy paging-state backfill counts and probes rows by pool. Create
	// its covering index first so opening an existing installation remains
	// linear in the row count rather than pools × rows.
	if _, err := s.db.Exec(dataPoolRowsPageIndex.query); err != nil {
		return fmt.Errorf("failed to create %s: %w", dataPoolRowsPageIndex.name, err)
	}
	if err := s.backfillDataPoolRowPagingState(); err != nil {
		return err
	}
	if err := s.repairLegacyImportForeignKeyTargets(); err != nil {
		return err
	}
	if err := s.backfillSanitizedSourceAvailability(); err != nil {
		return err
	}
	if err := s.backfillGitHubCheckLinkKinds(); err != nil {
		return err
	}
	if err := s.backfillBlankScriptIDs(); err != nil {
		return err
	}
	if err := s.RepairLegacyScriptRecordings(); err != nil {
		return err
	}
	if err := s.repairLegacyCorrelationRulesets(); err != nil {
		return err
	}
	if err := s.backfillArchiveFinalizationIntentTokens(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE github_script_links SET remote_blob_sha = last_commit_sha WHERE (remote_blob_sha IS NULL OR remote_blob_sha = '') AND last_commit_sha IS NOT NULL AND last_commit_sha != ''`); err != nil {
		return fmt.Errorf("failed to backfill github_script_links.remote_blob_sha: %w", err)
	}
	if err := s.backfillGitHubScriptSyncTargetOwners(); err != nil {
		return err
	}
	// Must run before schemaIndexes: it deduplicates fleet_pool_members so the
	// unique index on generator_id can be created on an existing installation.
	if err := s.consolidateFleetPoolMembership(); err != nil {
		return err
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
	if err := s.applyTimescaleDB(); err != nil {
		return err
	}
	if err := s.backfillObjectActivity(); err != nil {
		return err
	}
	if err := s.ensureScriptContentRevisionTracking(); err != nil {
		return fmt.Errorf("initialize script content revision tracking: %w", err)
	}
	if err := s.ensureScenarioContentRevisionTracking(); err != nil {
		return fmt.Errorf("initialize scenario content revision tracking: %w", err)
	}
	return s.recordSchemaReleases()
}

func (s *SQLiteStore) backfillExecutionTargetEnrollmentExpiry() error {
	expiresAt := time.Now().UTC().Add(DefaultExecutionTargetEnrollmentTTL).Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE execution_targets SET reverse_token_expires_at = ?
		WHERE TRIM(COALESCE(reverse_token_hash, '')) != '' AND TRIM(COALESCE(reverse_token_expires_at, '')) = ''`, expiresAt); err != nil {
		return fmt.Errorf("backfill execution-target enrollment expiry: %w", err)
	}
	return nil
}

// backfillKubernetesExecutionTargets transactionally projects every legacy
// cluster connection into the provider-neutral placement model, then binds
// existing pools and domain defaults to that projection. Deterministic IDs and
// ON CONFLICT make reopening a database after an interrupted migration safe.
func (s *SQLiteStore) backfillKubernetesExecutionTargets() error {
	type legacyCluster struct {
		id, name, description, kind, region string
		createdAt, updatedAt                time.Time
	}
	rows, err := s.db.Query(`SELECT id, name, COALESCE(description, ''), kind,
		COALESCE(aws_region, ''), created_at, updated_at FROM cluster_connections ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list legacy clusters for execution-target backfill: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
	clusters := make([]legacyCluster, 0)
	for rows.Next() {
		var cluster legacyCluster
		if err := rows.Scan(&cluster.id, &cluster.name, &cluster.description, &cluster.kind,
			&cluster.region, &cluster.createdAt, &cluster.updatedAt); err != nil {
			rows.Close()
			return err
		}
		clusters = append(clusters, cluster)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	capabilities := marshalJSONOrDefault(TargetCapabilities{CanScale: true, CanReachDirectly: true}, "{}")
	for _, cluster := range clusters {
		targetID := uuid.NewSHA1(uuid.NameSpaceURL, []byte("apexload:execution-target:kubernetes:"+cluster.id)).String()
		if _, err := tx.Exec(`INSERT INTO execution_targets (
			id, name, description, kind, cluster_connection_id, region, connectivity_mode,
			standing_owner, burst_owner, archive_mode, spec_json, runtime_profiles_json,
			capabilities_json, status, labels_json, created_at, updated_at)
			VALUES (?, ?, ?, 'kubernetes', ?, ?, 'direct', 'apex', 'disabled', 'controller_pull', '{}', '[]', ?, 'active', '', ?, ?)
			ON CONFLICT(id) DO NOTHING`, targetID, cluster.name, cluster.description, cluster.id,
			cluster.region, capabilities, cluster.createdAt, cluster.updatedAt); err != nil {
			return fmt.Errorf("backfill execution target for cluster %s: %w", cluster.id, err)
		}
	}
	if _, err := tx.Exec(`UPDATE fleet_pools SET execution_target_id = (
		SELECT id FROM execution_targets WHERE execution_targets.cluster_connection_id = fleet_pools.cluster_id
	) WHERE TRIM(COALESCE(execution_target_id, '')) = '' AND TRIM(COALESCE(cluster_id, '')) != ''`); err != nil {
		return fmt.Errorf("backfill fleet-pool execution targets: %w", err)
	}
	if _, err := tx.Exec(`UPDATE domain_deployment_targets SET default_execution_target_id = (
		SELECT id FROM execution_targets WHERE execution_targets.cluster_connection_id = domain_deployment_targets.default_cluster_id
	) WHERE default_execution_target_id IS NULL AND default_cluster_id IS NOT NULL`); err != nil {
		return fmt.Errorf("backfill domain default execution targets: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO domain_execution_target_allowlist (domain_id, execution_target_id)
		SELECT domain_id, default_execution_target_id FROM domain_deployment_targets
		WHERE default_execution_target_id IS NOT NULL
		ON CONFLICT(domain_id, execution_target_id) DO NOTHING`); err != nil {
		return fmt.Errorf("backfill domain execution-target allowlists: %w", err)
	}
	return tx.Commit()
}

// backfillDataPoolRowPagingState initializes the O(1) cursor fence for
// databases created before rows_revision/row_count existed. Once initialized,
// every supported row mutation updates both values in its own transaction.
func (s *SQLiteStore) backfillDataPoolRowPagingState() error {
	if _, err := s.db.Exec(`UPDATE shared_data_pools
		SET row_count = (
				SELECT COUNT(*) FROM shared_data_pool_rows
				WHERE shared_data_pool_rows.pool_id = shared_data_pools.id
			),
			rows_revision = CASE WHEN EXISTS (
				SELECT 1 FROM shared_data_pool_rows
				WHERE shared_data_pool_rows.pool_id = shared_data_pools.id
			) THEN 1 ELSE 0 END
		WHERE rows_revision = 0 AND row_count = 0`); err != nil {
		return fmt.Errorf("backfill data pool row paging state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) backfillArchiveFinalizationIntentTokens() error {
	rows, err := s.db.Query(`SELECT test_id FROM archive_finalization_jobs WHERE intent_token IS NULL OR TRIM(intent_token) = ''`)
	if err != nil {
		return fmt.Errorf("list legacy archive finalization intents: %w", err)
	}
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
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
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer rows.Close()
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
		if _, err := s.db.Exec(migration.statement(s.db.dialect)); err != nil && !isDuplicateColumnErr(err) {
			return fmt.Errorf("failed to apply schema migration %s: %w", migration.name, err)
		}
	}
	return nil
}

// postgresSchedulerLeaseGenerationBigintMigration widens the fencing generation
// created by releases that declared it as PostgreSQL INTEGER. The conditional
// avoids taking an ACCESS EXCLUSIVE table lock on every controller restart once
// the column is already BIGINT. INTEGER -> BIGINT is lossless, so existing lease
// generations and their CHECK/default constraints remain intact.
const postgresSchedulerLeaseGenerationBigintMigration = `DO $apexload$
BEGIN
	IF EXISTS (
		SELECT 1
		FROM pg_attribute
		WHERE attrelid = 'scheduler_leases'::regclass
			AND attname = 'generation'
			AND atttypid <> 'int8'::regtype
			AND NOT attisdropped
	) THEN
		ALTER TABLE scheduler_leases ALTER COLUMN generation TYPE BIGINT;
	END IF;
END
$apexload$;`

func (s *SQLiteStore) ensurePostgresSchedulerLeaseGenerationBigint() error {
	if s == nil || s.db == nil || s.db.dialect != storeDialectPostgres {
		return nil
	}
	if _, err := s.db.Exec(postgresSchedulerLeaseGenerationBigintMigration); err != nil {
		return fmt.Errorf("migrate scheduler lease generation to BIGINT: %w", err)
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

// LegacyApplicationIDForProject is the project's own legacy application: the
// bare ID in the default project, suffixed with a hash of the project elsewhere.
func LegacyApplicationIDForProject(projectID string) string { return legacyApplicationID(projectID) }

// LegacyReleaseIDForProject is the project's own legacy release.
func LegacyReleaseIDForProject(projectID string) string { return legacyReleaseID(projectID) }

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

// ensureLegacyProjectHierarchyTx provisions the deterministic compatibility
// hierarchy for one project inside the caller's transaction. Keeping this in
// the project write transaction means a newly-created project is never visible
// without the application and release used by legacy/unscoped records.
func ensureLegacyProjectHierarchyTx(tx *storeTx, projectID string, now time.Time) error {
	projectID = normalizeProjectID(projectID)
	applicationID := legacyApplicationID(projectID)
	releaseID := legacyReleaseID(projectID)
	if _, err := tx.Exec(`INSERT OR IGNORE INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, applicationID, projectID, "Legacy", now, now); err != nil {
		return fmt.Errorf("create legacy application for project %s: %w", projectID, err)
	}
	var applicationProjectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, applicationID).Scan(&applicationProjectID); err != nil {
		return fmt.Errorf("validate legacy application %s for project %s: %w", applicationID, projectID, err)
	}
	applicationProjectID = normalizeProjectID(applicationProjectID)
	if applicationProjectID != projectID {
		return fmt.Errorf("legacy application %q belongs to project %q, not %q", applicationID, applicationProjectID, projectID)
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO releases (id, application_id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, releaseID, applicationID, projectID, "Imported Legacy", now, now); err != nil {
		return fmt.Errorf("create legacy release for project %s: %w", projectID, err)
	}
	var releaseApplicationID, releaseProjectID string
	var releaseArchivedAt *time.Time
	if err := tx.QueryRow(`SELECT application_id, COALESCE(project_id, ''), archived_at FROM releases WHERE id = ?`, releaseID).
		Scan(&releaseApplicationID, &releaseProjectID, &releaseArchivedAt); err != nil {
		return fmt.Errorf("validate legacy release %s for project %s: %w", releaseID, projectID, err)
	}
	releaseProjectID = normalizeProjectID(releaseProjectID)
	if releaseApplicationID != applicationID || releaseProjectID != projectID {
		return fmt.Errorf("legacy release %q belongs to application %q in project %q, not application %q in project %q", releaseID, releaseApplicationID, releaseProjectID, applicationID, projectID)
	}
	if releaseArchivedAt != nil {
		return fmt.Errorf("legacy release %q is archived", releaseID)
	}
	return nil
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
	// Idempotent belt-and-braces: the explicit Close below is kept because it
	// surfaces the Close error, which a bare defer discards. The defer covers
	// the paths that never reach it — notably a panic in a scan helper, which
	// the controller recovers, pinning this connection for the process's life.
	defer projectRows.Close()
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
		if err := ensureLegacyProjectHierarchyTx(tx, projectID, now); err != nil {
			return err
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
		// Keep the old row as the stable ref-key owner when the scoped target
		// already has a different key; deleting it would make the public key
		// unreachable.
		keepLegacyRefOwner := legacyRefKeys[candidate.existingScenarioID] != "" && !legacyRefKeysTransferred[candidate.existingScenarioID]
		if !keepLegacyRefOwner {
			oldScenarioIDs[candidate.existingScenarioID] = struct{}{}
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
				return nil //nolint:nilerr // a payload that cannot be parsed may still reference the scenario, so it is kept
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
		Iterations     *int        `json:"iterations"`
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
		if _, err := tx.Exec(`INSERT OR IGNORE INTO scenario_scripts (scenario_id, sort_order, name, script_id, vusers, ramp_up, ramp_down, stages_json, iterations, initialization, role)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, scenarioID, i, group.Name, group.ScriptID, group.VUsers, group.RampUp, group.RampDown, string(stagesJSON), group.Iterations, group.Initialization, group.Role); err != nil {
			return err
		}
	}
	return nil
}
