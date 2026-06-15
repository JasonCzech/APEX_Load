from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .client import JsonMap, APEXLoadClient, apply_path_params, guarded_response, json_text


@dataclass(frozen=True)
class EndpointTool:
    name: str
    method: str
    path: str
    description: str
    mutating: bool = False
    destructive: bool = False
    default_project: bool = False


READ = "Read-only APEX Load API call."
WRITE = "Mutates APEX Load state; requires confirm=true."
DESTRUCTIVE = "Destructive or admin action; requires confirm=true and reason."


ENDPOINT_TOOLS: tuple[EndpointTool, ...] = (
    EndpointTool("system_health", "GET", "/system/health", f"{READ} Check controller health."),
    EndpointTool("system_readiness", "GET", "/system/readiness", f"{READ} Check production readiness gates."),
    EndpointTool("system_inventory", "GET", "/system/inventory", f"{READ} Get controller and generator inventory."),
    EndpointTool("system_usage", "GET", "/system/usage", f"{READ} Get live usage accounting.", default_project=True),
    EndpointTool("system_usage_export", "GET", "/system/usage/export", f"{READ} Export usage accounting metadata.", default_project=True),
    EndpointTool("system_usage_snapshots", "GET", "/system/usage/snapshots", f"{READ} List persisted usage snapshots.", default_project=True),
    EndpointTool("system_capture_usage_snapshot", "POST", "/system/usage/snapshots", f"{DESTRUCTIVE} Capture a usage accounting snapshot.", True, True, True),
    EndpointTool("system_parity_status", "GET", "/system/parity-status", f"{READ} Get machine-readable parity status."),
    EndpointTool("system_browser_benchmarks", "GET", "/system/browser-benchmarks", f"{READ} Get browser benchmark evidence."),
    EndpointTool("runtime_settings_support_matrix", "GET", "/runtime-settings/support-matrix", f"{READ} Get runtime settings support matrix."),
    EndpointTool("protocol_bundles_support_matrix", "GET", "/protocol-bundles/support-matrix", f"{READ} Get enterprise protocol bundle support matrix."),
    EndpointTool("fleet_alerts", "GET", "/fleet/alerts", f"{READ} Get fleet alert cards."),
    EndpointTool("fleet_capacity", "GET", "/fleet/capacity", f"{READ} Get fleet capacity fit.", default_project=True),
    EndpointTool("fleet_pools_list", "GET", "/fleet/pools", f"{READ} List managed generator pools."),
    EndpointTool("fleet_pools_create", "POST", "/fleet/pools", f"{WRITE} Create a managed generator pool.", True),
    EndpointTool("fleet_pools_get", "GET", "/fleet/pools/{id}", f"{READ} Get a managed generator pool."),
    EndpointTool("fleet_pools_update", "PUT", "/fleet/pools/{id}", f"{WRITE} Update a managed generator pool.", True),
    EndpointTool("fleet_pools_delete", "DELETE", "/fleet/pools/{id}", f"{DESTRUCTIVE} Delete a managed generator pool.", True, True),
    EndpointTool("fleet_generators_list", "GET", "/fleet/generators", f"{READ} List persistent generator metadata."),
    EndpointTool("fleet_generators_patch", "PATCH", "/fleet/generators/{id}", f"{WRITE} Patch generator metadata (labels, pool, cordon).", True),
    EndpointTool("fleet_provision_requests_create", "POST", "/fleet/provision-requests", f"{WRITE} Request cloud provisioning for a pool.", True),
    EndpointTool("fleet_provision_requests_get", "GET", "/fleet/provision-requests/{id}", f"{READ} Get a provisioning request and its events."),
    EndpointTool("system_settings_get", "GET", "/system/settings", f"{READ} Get system settings."),
    EndpointTool("system_settings_update", "PUT", "/system/settings", f"{DESTRUCTIVE} Update system settings.", True, True),
    EndpointTool("system_generator_drain", "PATCH", "/system/generators/drain", f"{DESTRUCTIVE} Set generator drain or cordon state.", True, True),
    EndpointTool("prometheus_metrics", "GET", "/metrics", f"{READ} Fetch Prometheus metrics metadata."),

    EndpointTool("auth_me", "GET", "/auth/me", f"{READ} Get current auth identity."),
    EndpointTool("auth_effective_access", "GET", "/auth/effective-access", f"{READ} Get effective access for the current principal.", default_project=True),
    EndpointTool("auth_access_matrix", "GET", "/auth/access-matrix", f"{READ} Get role-to-operation matrix."),
    EndpointTool("auth_diagnostics", "GET", "/auth/diagnostics", f"{READ} Get auth diagnostics."),
    EndpointTool("auth_roles", "GET", "/auth/roles", f"{READ} Get built-in role catalogue."),
    EndpointTool("auth_service_accounts", "GET", "/auth/service-accounts", f"{READ} List service accounts."),
    EndpointTool("auth_service_account_create", "POST", "/auth/service-accounts", f"{DESTRUCTIVE} Create a service account and one-time API key.", True, True),
    EndpointTool("auth_service_account_update", "PATCH", "/auth/service-accounts/{id}", f"{DESTRUCTIVE} Update a service account.", True, True),
    EndpointTool("auth_service_account_rotate", "POST", "/auth/service-accounts/{id}/rotate", f"{DESTRUCTIVE} Rotate a service-account key.", True, True),
    EndpointTool("auth_service_account_delete", "DELETE", "/auth/service-accounts/{id}", f"{DESTRUCTIVE} Delete a service account.", True, True),
    EndpointTool("auth_audit_events", "GET", "/auth/audit-events", f"{READ} Query audit events."),
    EndpointTool("auth_audit_events_export", "GET", "/auth/audit-events/export", f"{READ} Export audit events."),

    EndpointTool("project_list", "GET", "/projects", f"{READ} List projects."),
    EndpointTool("project_create", "POST", "/projects", f"{DESTRUCTIVE} Create a project.", True, True),
    EndpointTool("project_get", "GET", "/projects/{id}", f"{READ} Get a project."),
    EndpointTool("project_update", "PUT", "/projects/{id}", f"{DESTRUCTIVE} Update project governance or metadata.", True, True),
    EndpointTool("project_github_policy_get", "GET", "/projects/{id}/github/policy", f"{READ} Get project GitHub Enterprise repository policy."),
    EndpointTool("project_github_policy_update", "PUT", "/projects/{id}/github/policy", f"{DESTRUCTIVE} Update project GitHub Enterprise repository policy.", True, True),
    EndpointTool("project_delete", "DELETE", "/projects/{id}", f"{DESTRUCTIVE} Delete a project.", True, True),
    EndpointTool("folder_list", "GET", "/projects/{pid}/folders", f"{READ} List project folders."),
    EndpointTool("folder_create", "POST", "/projects/{pid}/folders", f"{DESTRUCTIVE} Create a project folder.", True, True),
    EndpointTool("folder_update", "PUT", "/folders/{id}", f"{DESTRUCTIVE} Update a project folder.", True, True),
    EndpointTool("folder_delete", "DELETE", "/folders/{id}", f"{DESTRUCTIVE} Delete a project folder.", True, True),
    EndpointTool("application_list", "GET", "/applications", f"{READ} List applications.", default_project=True),
    EndpointTool("application_create", "POST", "/applications", f"{WRITE} Create an application.", True, False, True),
    EndpointTool("application_get", "GET", "/applications/{id}", f"{READ} Get an application."),
    EndpointTool("application_update", "PUT", "/applications/{id}", f"{WRITE} Update an application.", True),
    EndpointTool("application_delete", "DELETE", "/applications/{id}", f"{DESTRUCTIVE} Delete an application.", True, True),
    EndpointTool("release_list", "GET", "/applications/{id}/releases", f"{READ} List application releases."),
    EndpointTool("release_create", "POST", "/applications/{id}/releases", f"{WRITE} Create a release.", True, False, True),
    EndpointTool("release_get", "GET", "/releases/{id}", f"{READ} Get a release."),
    EndpointTool("release_update", "PUT", "/releases/{id}", f"{WRITE} Update a release.", True),
    EndpointTool("release_delete", "DELETE", "/releases/{id}", f"{DESTRUCTIVE} Delete a release.", True, True),
    EndpointTool("release_archive", "POST", "/releases/{id}/archive", f"{DESTRUCTIVE} Archive a release.", True, True),
    EndpointTool("release_trend_export", "GET", "/releases/{id}/trend/export", f"{READ} Export release trend data."),
    EndpointTool("release_sla_governance", "GET", "/releases/{id}/sla-governance", f"{READ} Get release SLA governance."),
    EndpointTool("release_sla_waiver_create", "POST", "/releases/{id}/sla-governance/waivers", f"{DESTRUCTIVE} Create an SLA waiver.", True, True),

    EndpointTool("script_list", "GET", "/scripts", f"{READ} List scripts.", default_project=True),
    EndpointTool("script_create", "POST", "/scripts", f"{WRITE} Create a script.", True, False, True),
    EndpointTool("script_get", "GET", "/scripts/{id}", f"{READ} Get a script."),
    EndpointTool("script_update", "PUT", "/scripts/{id}", f"{WRITE} Update a script.", True),
    EndpointTool("script_delete", "DELETE", "/scripts/{id}", f"{DESTRUCTIVE} Delete a script.", True, True),
    EndpointTool("script_move", "PATCH", "/scripts/{id}/move", f"{WRITE} Move a script.", True),
    EndpointTool("script_validate_draft", "POST", "/scripts/validate", "Validate a draft script without saving."),
    EndpointTool("script_replay_draft", "POST", "/scripts/replay", f"{WRITE} Replay a draft script.", True, False, True),
    EndpointTool("script_browser_replay_draft", "POST", "/scripts/browser-replay/sessions", f"{WRITE} Start draft browser replay.", True, False, True),
    EndpointTool("script_browser_to_http_preview", "POST", "/scripts/browser-to-http/preview", "Preview browser-to-HTTP conversion."),
    EndpointTool("script_authoring_capabilities", "GET", "/scripts/authoring/capabilities", f"{READ} Get authoring capabilities."),
    EndpointTool("script_authoring_templates", "GET", "/scripts/authoring/templates", f"{READ} Get authoring templates."),
    EndpointTool("script_authoring_components", "GET", "/scripts/authoring/components", f"{READ} Get reusable authoring components."),
    EndpointTool("script_authoring_session_list", "GET", "/scripts/authoring/sessions", f"{READ} List authoring sessions.", default_project=True),
    EndpointTool("script_authoring_session_create", "POST", "/scripts/authoring/sessions", f"{WRITE} Create an authoring review session.", True, False, True),
    EndpointTool("script_authoring_session_get", "GET", "/scripts/authoring/sessions/{id}", f"{READ} Get an authoring session."),
    EndpointTool("script_authoring_session_update", "PUT", "/scripts/authoring/sessions/{id}", f"{WRITE} Update an authoring session.", True),
    EndpointTool("script_authoring_session_delete", "DELETE", "/scripts/authoring/sessions/{id}", f"{DESTRUCTIVE} Delete an authoring session.", True, True),
    EndpointTool("script_authoring_session_approve", "POST", "/scripts/authoring/sessions/{id}/approve", f"{WRITE} Approve and promote an authoring session.", True),
    EndpointTool("script_authoring_correlation_preview", "POST", "/scripts/authoring/sessions/{id}/correlations/preview", "Preview or apply authoring correlation repair; use payload.apply=false unless confirmed."),
    EndpointTool("script_validate_saved", "POST", "/scripts/{id}/validate", "Validate a saved script."),
    EndpointTool("script_replay_saved", "POST", "/scripts/{id}/replay", f"{WRITE} Replay a saved script.", True),
    EndpointTool("script_browser_replay_saved", "POST", "/scripts/{id}/browser-replay/sessions", f"{WRITE} Start saved-script browser replay.", True),
    EndpointTool("script_export", "POST", "/scripts/{id}/export", f"{WRITE} Export a script artifact from the controller.", True),
    EndpointTool("script_download", "GET", "/scripts/{id}/download", f"{READ} Download a script."),
    EndpointTool("script_runtime_settings_report", "GET", "/scripts/{id}/runtime-settings/report", f"{READ} Get script runtime settings report."),
    EndpointTool("script_enterprise_archive_support_matrix", "GET", "/scripts/import/enterprise-script-archive/support-matrix", f"{READ} Get Enterprise Script Archive import support."),
    EndpointTool("script_jmeter_support_matrix", "GET", "/scripts/import/jmeter/support-matrix", f"{READ} Get JMeter import support."),
    EndpointTool("script_web_api_support_matrix", "GET", "/scripts/web-api/support-matrix", f"{READ} Get Web/API execution support."),
    EndpointTool("script_devweb_support_matrix", "GET", "/scripts/import/devweb/support-matrix", f"{READ} Get DevWeb import support."),
    EndpointTool("script_oracle_ebusiness_support_matrix", "GET", "/scripts/import/oracle-ebusiness/support-matrix", f"{READ} Get Oracle E-Business import support."),
    EndpointTool("script_jmeter_save_import", "POST", "/scripts/import/jmeter/save", f"{WRITE} Save a JMeter import result.", True, False, True),
    EndpointTool("script_sample_import", "POST", "/scripts/import/sample", f"{WRITE} Import sample HTTP/cURL content.", True, False, True),
    EndpointTool("script_oracle_ebusiness_review", "POST", "/scripts/import/oracle-ebusiness/review", f"{WRITE} Create Oracle E-Business review artifact.", True, False, True),
    EndpointTool("github_diagnostics", "GET", "/integrations/github/diagnostics", f"{READ} Get GitHub Enterprise integration diagnostics."),
    EndpointTool("github_installations", "GET", "/integrations/github/installations", f"{READ} List cached GitHub App installations; pass refresh=true as query when app auth is configured."),
    EndpointTool("github_sync_jobs", "GET", "/integrations/github/sync-jobs", f"{READ} List GitHub bulk sync jobs.", default_project=True),
    EndpointTool("github_sync_job_get", "GET", "/integrations/github/sync-jobs/{id}", f"{READ} Get one GitHub sync job."),
    EndpointTool("github_sync_job_create", "POST", "/integrations/github/sync-jobs", f"{WRITE} Start a guarded GitHub bulk import or push job.", True, False, True),
    EndpointTool("github_webhook_deliveries", "GET", "/integrations/github/webhooks", f"{READ} List GitHub webhook delivery processing records."),
    EndpointTool("github_check_links", "GET", "/integrations/github/check-links", f"{READ} List GitHub check-run links."),
    EndpointTool("github_import_script", "POST", "/scripts/import/github", f"{WRITE} Import a GitHub script into the standard import-review flow.", True, False, True),
    EndpointTool("github_push_script", "POST", "/scripts/{id}/github/push", f"{WRITE} Push a saved script to GitHub using project policy.", True, False, True),
    EndpointTool("github_script_status", "GET", "/scripts/{id}/github/status", f"{READ} Get GitHub link and drift status for a saved script."),

    EndpointTool("scenario_list", "GET", "/scenarios", f"{READ} List scenarios.", default_project=True),
    EndpointTool("scenario_create", "POST", "/scenarios", f"{WRITE} Create a scenario.", True, False, True),
    EndpointTool("scenario_validate_load_model", "POST", "/scenarios/validate-load-model", "Validate a scenario load model without saving."),
    EndpointTool("scenario_get", "GET", "/scenarios/{id}", f"{READ} Get a scenario."),
    EndpointTool("scenario_update", "PUT", "/scenarios/{id}", f"{WRITE} Update a scenario.", True),
    EndpointTool("scenario_delete", "DELETE", "/scenarios/{id}", f"{DESTRUCTIVE} Delete a scenario.", True, True),
    EndpointTool("scenario_load_model_support_matrix", "GET", "/scenarios/load-models/support-matrix", f"{READ} Get load model support."),
    EndpointTool("scenario_load_model_templates", "GET", "/scenarios/load-models/templates", f"{READ} Get load model templates."),
    EndpointTool("scenario_results", "GET", "/scenarios/{id}/results", f"{READ} List scenario runs/results."),
    EndpointTool("scenario_runtime_settings_report", "GET", "/scenarios/{id}/runtime-settings/report", f"{READ} Get scenario runtime settings report."),
    EndpointTool("scenario_data_pools", "GET", "/scenarios/{id}/data-pools", f"{READ} List scenario data-pool bindings."),
    EndpointTool("scenario_data_pools_update", "PUT", "/scenarios/{id}/data-pools", f"{WRITE} Update scenario data-pool bindings.", True),
    EndpointTool("scenario_schedule_list", "GET", "/scenarios/{id}/schedules", f"{READ} List scenario schedules."),
    EndpointTool("scenario_schedule_create", "POST", "/scenarios/{id}/schedules", f"{WRITE} Create a scenario schedule.", True),
    EndpointTool("scenario_schedule_update", "PUT", "/scenarios/{id}/schedules/{schedule_id}", f"{WRITE} Update a scenario schedule.", True),
    EndpointTool("scenario_schedule_delete", "DELETE", "/scenarios/{id}/schedules/{schedule_id}", f"{DESTRUCTIVE} Delete a scenario schedule.", True, True),
    EndpointTool("scenario_schedule_occurrences", "GET", "/scenarios/{id}/schedules/{schedule_id}/occurrences", f"{READ} List schedule occurrences."),
    EndpointTool("scenario_schedule_approval_approve", "POST", "/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve", f"{DESTRUCTIVE} Approve a schedule occurrence.", True, True),
    EndpointTool("scenario_schedule_approval_reject", "POST", "/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject", f"{DESTRUCTIVE} Reject a schedule occurrence.", True, True),
    EndpointTool("schedule_health", "GET", "/schedules/health", f"{READ} Get schedule health summary."),

    EndpointTool("timeslot_list", "GET", "/capacity/timeslots", f"{READ} List capacity reservations (timeslots).", default_project=True),
    EndpointTool("timeslot_create", "POST", "/capacity/timeslots", f"{WRITE} Create a capacity reservation (timeslot).", True, False, True),
    EndpointTool("timeslot_check_availability", "POST", "/capacity/timeslots/availability", f"{READ} Check capacity reservation conflicts for a window.", default_project=True),
    EndpointTool("timeslot_get", "GET", "/capacity/timeslots/{id}", f"{READ} Get a capacity reservation."),
    EndpointTool("timeslot_update", "PUT", "/capacity/timeslots/{id}", f"{WRITE} Update a capacity reservation.", True),
    EndpointTool("timeslot_delete", "DELETE", "/capacity/timeslots/{id}", f"{DESTRUCTIVE} Delete a capacity reservation.", True, True),
    EndpointTool("timeslot_approve", "POST", "/capacity/timeslots/{id}/approve", f"{DESTRUCTIVE} Approve a capacity reservation (project-admin).", True, True),
    EndpointTool("timeslot_reject", "POST", "/capacity/timeslots/{id}/reject", f"{DESTRUCTIVE} Reject a capacity reservation (project-admin).", True, True),
    EndpointTool("timeslot_bind", "POST", "/capacity/timeslots/{id}/bind", f"{WRITE} Bind an approved timeslot to a test run.", True),

    EndpointTool("execution_plan_list", "GET", "/execution-plans", f"{READ} List execution plans.", default_project=True),
    EndpointTool("execution_plan_create", "POST", "/execution-plans", f"{WRITE} Create an execution plan.", True, False, True),
    EndpointTool("execution_plan_get", "GET", "/execution-plans/{id}", f"{READ} Get an execution plan."),
    EndpointTool("execution_plan_update", "PUT", "/execution-plans/{id}", f"{WRITE} Update an execution plan.", True),
    EndpointTool("execution_plan_delete", "DELETE", "/execution-plans/{id}", f"{DESTRUCTIVE} Delete an execution plan.", True, True),
    EndpointTool("execution_plan_run", "POST", "/execution-plans/{id}/runs", f"{WRITE} Materialize and run an execution plan (creates one test per enabled entry).", True),

    EndpointTool("monitoring_collector_list", "GET", "/monitoring/collectors", f"{READ} List monitoring collectors.", default_project=True),
    EndpointTool("monitoring_collector_create", "POST", "/monitoring/collectors", f"{DESTRUCTIVE} Register a monitoring collector and issue its one-time token.", True, True, True),
    EndpointTool("monitoring_collector_get", "GET", "/monitoring/collectors/{id}", f"{READ} Get a monitoring collector."),
    EndpointTool("monitoring_collector_update", "PATCH", "/monitoring/collectors/{id}", f"{WRITE} Update a monitoring collector (name, packs, health).", True),
    EndpointTool("monitoring_collector_delete", "DELETE", "/monitoring/collectors/{id}", f"{DESTRUCTIVE} Delete a monitoring collector.", True, True),
    EndpointTool("monitoring_collector_rotate_token", "POST", "/monitoring/collectors/{id}/rotate-token", f"{DESTRUCTIVE} Rotate a monitoring collector token (invalidates the old one).", True, True),

    EndpointTool("system_entitlements", "GET", "/system/entitlements", f"{READ} Get edition, enabled protocol bundles, and limits."),
    EndpointTool("system_entitlements_usage", "GET", "/system/entitlements/usage", f"{READ} Get current consumption vs entitlement limits."),

    EndpointTool("data_pool_list", "GET", "/data-pools", f"{READ} List shared data pools.", default_project=True),
    EndpointTool("data_pool_create", "POST", "/data-pools", f"{WRITE} Create a shared data pool.", True, False, True),
    EndpointTool("data_pool_get", "GET", "/data-pools/{id}", f"{READ} Get a shared data pool."),
    EndpointTool("data_pool_update", "PUT", "/data-pools/{id}", f"{WRITE} Update a shared data pool.", True),
    EndpointTool("data_pool_delete", "DELETE", "/data-pools/{id}", f"{DESTRUCTIVE} Delete a shared data pool.", True, True),
    EndpointTool("data_pool_row_list", "GET", "/data-pools/{id}/rows", f"{READ} List data-pool rows."),
    EndpointTool("data_pool_row_create", "POST", "/data-pools/{id}/rows", f"{WRITE} Create a data-pool row.", True),
    EndpointTool("data_pool_row_update", "PUT", "/data-pools/{id}/rows/{row_id}", f"{WRITE} Update a data-pool row.", True),
    EndpointTool("data_pool_row_delete", "DELETE", "/data-pools/{id}/rows/{row_id}", f"{DESTRUCTIVE} Delete a data-pool row.", True, True),

    EndpointTool("run_create", "POST", "/tests", f"{WRITE} Create and queue a test run.", True, False, True),
    EndpointTool("run_estimate", "POST", "/tests/estimate", "Estimate a test run without queueing."),
    EndpointTool("run_list", "GET", "/tests", f"{READ} List runs.", default_project=True),
    EndpointTool("run_get", "GET", "/tests/{id}", f"{READ} Get run status and live summary."),
    EndpointTool("run_sla_status", "GET", "/tests/{id}/sla-status", f"{READ} Get run SLA status."),
    EndpointTool("run_html_report", "GET", "/tests/{id}/report", f"{READ} Get HTML run report metadata."),
    EndpointTool("run_start", "POST", "/tests/{id}/start", f"{WRITE} Start a queued run.", True),
    EndpointTool("run_stop", "POST", "/tests/{id}/stop", f"{WRITE} Gracefully stop a run.", True),
    EndpointTool("run_abort", "POST", "/tests/{id}/abort", f"{DESTRUCTIVE} Abort a run immediately.", True, True),
    EndpointTool("run_pause", "POST", "/tests/{id}/pause", f"{WRITE} Pause a run at action boundary.", True),
    EndpointTool("run_resume", "POST", "/tests/{id}/resume", f"{WRITE} Resume a paused run.", True),
    EndpointTool("github_check_run_publish", "POST", "/tests/{id}/github/check-run", f"{WRITE} Publish or retry an APEX Load result as a GitHub check run.", True, False, True),
    EndpointTool("run_scale_vusers", "PATCH", "/tests/{id}/vusers", f"{WRITE} Scale VUsers for a run group.", True),
    EndpointTool("run_adjust_duration", "PATCH", "/tests/{id}/duration", f"{WRITE} Adjust run duration.", True),
    EndpointTool("run_delete", "DELETE", "/tests/{id}", f"{DESTRUCTIVE} Delete a run.", True, True),
    EndpointTool("run_move", "PATCH", "/tests/{id}/move", f"{WRITE} Move a run to project/folder context.", True),
    EndpointTool("run_metrics", "GET", "/tests/{id}/metrics", f"{READ} Get current metrics snapshot."),
    EndpointTool("run_pin_comparison_baseline", "POST", "/tests/{id}/comparison-baseline/pin", f"{WRITE} Pin comparison baseline.", True),
    EndpointTool("run_unpin_comparison_baseline", "DELETE", "/tests/{id}/comparison-baseline/pin", f"{DESTRUCTIVE} Unpin comparison baseline.", True, True),
    EndpointTool("run_archive_download", "GET", "/tests/{id}/archive", f"{READ} Download run archive metadata."),
    EndpointTool("run_archive_manifest", "GET", "/tests/{id}/archive/manifest", f"{READ} Get run archive manifest."),
    EndpointTool("run_archive_file", "GET", "/tests/{id}/archive/files/{name}", f"{READ} Download run archive artifact metadata."),
    EndpointTool("run_archive_browser_diagnostics", "GET", "/tests/{id}/archive/browser-diagnostics", f"{READ} Download browser diagnostics metadata."),
    EndpointTool("run_archive_transactions", "GET", "/tests/{id}/archive/transactions", f"{READ} Get archived transactions."),
    EndpointTool("run_archive_resources", "GET", "/tests/{id}/archive/resources", f"{READ} Get archived resource metrics."),
    EndpointTool("run_archive_summaries", "GET", "/tests/{id}/archive/summaries", f"{READ} Get archive summaries."),
    EndpointTool("run_archive_report", "GET", "/tests/{id}/archive/report", f"{READ} Get structured archive report."),
    EndpointTool("run_archive_root_cause", "GET", "/tests/{id}/archive/root-cause", f"{READ} Get archive root-cause report."),
    EndpointTool("run_archive_ai_root_cause", "GET", "/tests/{id}/archive/ai-root-cause", f"{READ} Get AI-bound root-cause evidence."),
    EndpointTool("run_archive_comparison", "GET", "/tests/{id}/archive/comparison", f"{READ} Get archive comparison report."),
    EndpointTool("archive_retention_plan", "POST", "/archives/retention/plan", f"{DESTRUCTIVE} Plan archive retention pruning.", True, True),
    EndpointTool("archive_retention_apply", "POST", "/archives/retention/apply", f"{DESTRUCTIVE} Apply archive retention pruning.", True, True),

    EndpointTool("browser_replay_session_get", "GET", "/browser-replay/sessions/{session_id}", f"{READ} Get browser replay session."),
    EndpointTool("browser_replay_session_pause", "POST", "/browser-replay/sessions/{session_id}/pause", f"{WRITE} Pause browser replay session.", True),
    EndpointTool("browser_replay_session_resume", "POST", "/browser-replay/sessions/{session_id}/resume", f"{WRITE} Resume browser replay session.", True),
    EndpointTool("browser_replay_session_stop", "POST", "/browser-replay/sessions/{session_id}/stop", f"{WRITE} Stop browser replay session.", True),
    EndpointTool("recorder_start", "POST", "/recorder/start", f"{WRITE} Start recorder session.", True, False, True),
    EndpointTool("recorder_list", "GET", "/recorder", f"{READ} List recorder sessions.", default_project=True),
    EndpointTool("recorder_status", "GET", "/recorder/status", f"{READ} Get recorder status.", default_project=True),
    EndpointTool("recorder_get", "GET", "/recorder/{id}", f"{READ} Get recorder session."),
    EndpointTool("recorder_pause", "POST", "/recorder/{id}/pause", f"{WRITE} Pause recorder session.", True),
    EndpointTool("recorder_resume", "POST", "/recorder/{id}/resume", f"{WRITE} Resume recorder session.", True),
    EndpointTool("recorder_stop", "POST", "/recorder/{id}/stop", f"{WRITE} Stop recorder session.", True),
    EndpointTool("recorder_delete", "DELETE", "/recorder/{id}", f"{DESTRUCTIVE} Delete recorder session.", True, True),
    EndpointTool("recorder_request_update", "PATCH", "/recorder/{id}/requests/{seq}", f"{WRITE} Update recorded request metadata.", True),
    EndpointTool("recorder_conversion_preview", "POST", "/recorder/{id}/convert/preview", "Preview recorder conversion."),
    EndpointTool("recorder_convert", "POST", "/recorder/{id}/convert", f"{WRITE} Convert recorder session to script.", True),
    EndpointTool("recorder_export", "GET", "/recorder/{id}/export", f"{READ} Export recorder session metadata."),
    EndpointTool("recorder_ca_cert", "GET", "/recorder/ca.pem", f"{READ} Download recorder CA certificate metadata.", default_project=True),

    EndpointTool("ai_authoring_contract", "GET", "/ai/authoring/contract", f"{READ} Get AI authoring contract."),
    EndpointTool("ai_policy", "GET", "/ai/policy", f"{READ} Get AI policy.", default_project=True),
    EndpointTool("ai_review_queue", "GET", "/ai/review-queue", f"{READ} List AI review queue.", default_project=True),
    EndpointTool("ai_drafts", "POST", "/ai/drafts", f"{WRITE} Generate governed AI draft.", True, False, True),
    EndpointTool("ai_repairs", "POST", "/ai/repairs", f"{WRITE} Generate governed repair suggestions.", True, False, True),
    EndpointTool("ai_explain", "POST", "/ai/explain", "Explain scripts, scenarios, runtime settings, or reports."),
    EndpointTool("integrations_apm_profiles", "GET", "/integrations/apm/profiles", f"{READ} List APM integration profiles."),
    EndpointTool("monitoring_packs", "GET", "/monitoring/packs", f"{READ} List infrastructure monitoring packs."),
    EndpointTool("report_profiles", "GET", "/reports/profiles", f"{READ} List report profiles."),
    EndpointTool("results_import", "POST", "/results/import", f"{WRITE} Import external results.", True, False, True),
    EndpointTool("results_imports", "GET", "/results/imports", f"{READ} List external result imports.", default_project=True),

    EndpointTool("list_tests", "GET", "/tests", f"{READ} Compatibility alias for run_list.", default_project=True),
    EndpointTool("get_test_details", "GET", "/tests/{id}", f"{READ} Compatibility alias for run_get."),
    EndpointTool("start_test", "POST", "/tests/{id}/start", f"{WRITE} Compatibility alias for run_start.", True),
    EndpointTool("stop_test", "POST", "/tests/{id}/stop", f"{WRITE} Compatibility alias for run_stop.", True),
    EndpointTool("pause_test", "POST", "/tests/{id}/pause", f"{WRITE} Compatibility alias for run_pause.", True),
    EndpointTool("resume_test", "POST", "/tests/{id}/resume", f"{WRITE} Compatibility alias for run_resume.", True),
    EndpointTool("list_scripts", "GET", "/scripts", f"{READ} Compatibility alias for script_list.", default_project=True),
    EndpointTool("get_system_health", "GET", "/system/health", f"{READ} Compatibility alias for system_health."),
)


FILE_IMPORTERS = {
    "enterprise_script_archive": "/scripts/import/enterprise-script-archive",
    "jmeter": "/scripts/import/jmeter",
    "devweb": "/scripts/import/devweb",
    "har": "/scripts/import/har",
    "recorder": "/scripts/import/recorder",
    "openapi": "/scripts/import/openapi",
    "wsdl": "/scripts/import/wsdl",
}

VALIDATE_IMPORTERS = {
    "enterprise_script_archive": "/scripts/import/enterprise-script-archive/validate",
    "jmeter": "/scripts/import/jmeter/validate",
}


def with_default_project(client: APEXLoadClient, query: JsonMap | None, enabled: bool) -> JsonMap | None:
    merged = dict(query or {})
    if enabled and client.config.default_project_id and not merged.get("project_id"):
        merged["project_id"] = client.config.default_project_id
    return merged or None


async def run_endpoint_tool(
    spec: EndpointTool,
    *,
    path_params: JsonMap | None = None,
    query: JsonMap | None = None,
    payload: Any | None = None,
    confirm: bool = False,
    reason: str = "",
    client: APEXLoadClient | None = None,
) -> str:
    if spec.name == "script_authoring_correlation_preview" and isinstance(payload, dict) and payload.get("apply") and not confirm:
        return json_text(guarded_response("Applying an authoring correlation repair changes the review session and requires confirm=true."))
    if spec.mutating and not confirm:
        return json_text(guarded_response(f"{spec.name} changes APEX Load state and requires confirm=true."))
    if spec.destructive and not reason.strip():
        return json_text(guarded_response(f"{spec.name} requires a reason for auditability.", code="reason_required", remediation="Pass reason with a concise business justification."))
    resolved_path, error = apply_path_params(spec.path, path_params)
    if error is not None or resolved_path is None:
        return json_text(error or guarded_response("Unable to resolve path parameters."))
    active_client = client or APEXLoadClient()
    response = await active_client.request(
        spec.method,
        resolved_path,
        query=with_default_project(active_client, query, spec.default_project),
        payload=payload,
    )
    return json_text(response)


async def run_file_import_tool(
    importer: str,
    file_path: str,
    *,
    options: JsonMap | None = None,
    validate_only: bool = False,
    confirm: bool = False,
    client: APEXLoadClient | None = None,
) -> str:
    normalized = importer.strip().lower()
    endpoints = VALIDATE_IMPORTERS if validate_only else FILE_IMPORTERS
    endpoint = endpoints.get(normalized)
    if not endpoint:
        return json_text(guarded_response(f"Unsupported importer {importer!r}.", code="unsupported_importer", remediation=f"Use one of: {', '.join(sorted(endpoints))}."))
    if not validate_only and not confirm:
        return json_text(guarded_response("Importing a file can create scripts or review artifacts and requires confirm=true."))
    active_client = client or APEXLoadClient()
    fields = dict(options or {})
    if active_client.config.default_project_id and not fields.get("project_id"):
        fields["project_id"] = active_client.config.default_project_id
    return json_text(await active_client.upload_file(endpoint, file_path, fields=fields))


def register_endpoint_tools(mcp: Any) -> None:
    for spec in ENDPOINT_TOOLS:
        def make_endpoint_handler(active_spec: EndpointTool):
            async def endpoint_handler(
                path_params: dict[str, Any] | None = None,
                query: dict[str, Any] | None = None,
                payload: dict[str, Any] | list[Any] | None = None,
                confirm: bool = False,
                reason: str = "",
            ) -> str:
                return await run_endpoint_tool(
                    active_spec,
                    path_params=path_params,
                    query=query,
                    payload=payload,
                    confirm=confirm,
                    reason=reason,
                )

            return endpoint_handler

        endpoint_handler = make_endpoint_handler(spec)
        endpoint_handler.__name__ = spec.name
        endpoint_handler.__doc__ = spec.description
        mcp.tool(name=spec.name, description=spec.description)(endpoint_handler)

    async def script_import_file(
        importer: str,
        file_path: str,
        options: dict[str, Any] | None = None,
        confirm: bool = False,
    ) -> str:
        """Import an Enterprise Script Archive, JMeter, DevWeb, HAR, OpenAPI, or WSDL file. Mutates APEX Load and requires confirm=true."""
        return await run_file_import_tool(importer, file_path, options=options, confirm=confirm)

    async def script_validate_import_file(
        importer: str,
        file_path: str,
        options: dict[str, Any] | None = None,
    ) -> str:
        """Validate an Enterprise Script Archive or JMeter file import without saving a script."""
        return await run_file_import_tool(importer, file_path, options=options, validate_only=True)

    mcp.tool(name="script_import_file", description=script_import_file.__doc__)(script_import_file)
    mcp.tool(name="script_validate_import_file", description=script_validate_import_file.__doc__)(script_validate_import_file)


def local_api_guide_text() -> str:
    repo_root = Path(__file__).resolve().parents[2]
    guide = repo_root / "docs" / "API_USER_GUIDE.md"
    if guide.exists():
        return guide.read_text(encoding="utf-8")
    return "APEX Load API guide is not available from this MCP server checkout."


def register_resources(mcp: Any) -> None:
    @mcp.resource("apexload://api-guide", name="APEX Load API Guide", mime_type="text/markdown")
    async def api_guide() -> str:
        return local_api_guide_text()

    @mcp.resource("apexload://auth/access-matrix", name="APEX Load Auth Access Matrix", mime_type="application/json")
    async def auth_access_matrix() -> str:
        return json_text(await APEXLoadClient().request("GET", "/auth/access-matrix"))

    @mcp.resource("apexload://system/parity-status", name="APEX Load Parity Status", mime_type="application/json")
    async def parity_status() -> str:
        return json_text(await APEXLoadClient().request("GET", "/system/parity-status"))

    @mcp.resource("apexload://runtime-settings/support-matrix", name="APEX Load Runtime Settings Support", mime_type="application/json")
    async def runtime_settings_support() -> str:
        return json_text(await APEXLoadClient().request("GET", "/runtime-settings/support-matrix"))

    @mcp.resource("apexload://protocol-bundles/support-matrix", name="APEX Load Protocol Bundle Support", mime_type="application/json")
    async def protocol_bundle_support() -> str:
        return json_text(await APEXLoadClient().request("GET", "/protocol-bundles/support-matrix"))
