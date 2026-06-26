import { useCallback, useEffect, useState } from 'react'

import { API_GUIDE_DEFAULT_PAGE, type ApiGuideContentPageID } from '../apiGuideNavigation'
import { useHasTopbarContributionProvider } from '../hooks/TopbarContributionProvider'
import { DashboardAuthEffectiveAccessResource } from '../types'
import { ApiGuideTopbarToggle, useApiGuideTopbarContribution } from './ApiGuideTopbarToggle'

type EndpointDoc = {
  method: string
  path: string
  purpose?: string
  operations?: string[]
}

type CommandDoc = {
  command: string
  operations?: string[]
}

const quickStartCommands: CommandDoc[] = [
  { command: 'curl http://localhost:8080/api/v1/system/health' },
  { command: 'curl -H "Authorization: Bearer $APEXLOAD_API_KEY" http://localhost:8080/api/v1/projects', operations: ['view_projects'] },
  { command: 'curl -X POST -H "Authorization: Bearer $APEXLOAD_API_KEY" -H "Content-Type: application/json" -d @script.json http://localhost:8080/api/v1/scripts', operations: ['manage_scripts'] },
  { command: 'curl -H "Authorization: Bearer $APEXLOAD_API_KEY" http://localhost:8080/api/v1/integrations/github/diagnostics', operations: ['view_system', 'view_scripts'] },
]

const mcpResources = [
  'apexload://api-guide',
  'apexload://auth/access-matrix',
  'apexload://system/parity-status',
  'apexload://runtime-settings/support-matrix',
  'apexload://protocol-bundles/support-matrix',
]

const authRows: EndpointDoc[] = [
  { method: 'GET', path: '/api/v1/auth/me', purpose: 'Current auth mode, local user status, locally resolved roles, and session metadata.' },
  { method: 'GET', path: '/api/v1/auth/effective-access', purpose: 'Operations allowed for the current principal from local user bindings or service-account roles.' },
  { method: 'GET', path: '/api/v1/auth/access-matrix', purpose: 'Role-to-operation catalog.' },
  { method: 'GET', path: '/api/v1/auth/roles', purpose: 'Role catalog used by delegated access administration.' },
  { method: 'GET', path: '/api/v1/auth/diagnostics', purpose: 'OIDC and trusted-header readiness diagnostics.', operations: ['view_auth_diagnostics'] },
  { method: 'GET', path: '/api/v1/auth/users', purpose: 'Search local human users available for APEX Load access.', operations: ['manage_users', 'manage_user_bindings'] },
  { method: 'POST', path: '/api/v1/auth/users', purpose: 'Onboard a human user subject from OIDC or trusted headers.', operations: ['manage_users'] },
  { method: 'GET', path: '/api/v1/auth/users/{id}', purpose: 'Get one local user and role bindings.', operations: ['manage_users', 'manage_user_bindings'] },
  { method: 'PATCH', path: '/api/v1/auth/users/{id}', purpose: 'Update local user metadata.', operations: ['manage_users'] },
  { method: 'POST', path: '/api/v1/auth/users/{id}/bindings', purpose: 'Grant a global, domain, project, or repository role binding.', operations: ['manage_user_bindings'] },
  { method: 'DELETE', path: '/api/v1/auth/users/{id}/bindings/{binding_id}', purpose: 'Remove a local role binding.', operations: ['manage_user_bindings'] },
  { method: 'POST', path: '/api/v1/auth/users/{id}/disable', purpose: 'Disable a local user while preserving audit history.', operations: ['manage_users'] },
  { method: 'POST', path: '/api/v1/auth/users/{id}/enable', purpose: 'Re-enable a local user record.', operations: ['manage_users'] },
  { method: 'GET', path: '/api/v1/auth/user-activity', purpose: 'Audit events and attributed run usage filtered by user, timeframe, domain, project, action, or status.', operations: ['view_user_activity'] },
  { method: 'GET', path: '/api/v1/auth/user-activity/export', purpose: 'CSV user activity export.', operations: ['view_user_activity'] },
  { method: 'GET', path: '/api/v1/auth/service-accounts', purpose: 'List service-account keys and scopes.', operations: ['manage_service_accounts'] },
  { method: 'POST', path: '/api/v1/auth/service-accounts', purpose: 'Create a service account and return its one-time API key.', operations: ['manage_service_accounts'] },
  { method: 'PATCH', path: '/api/v1/auth/service-accounts/{id}', purpose: 'Update service-account metadata, roles, scopes, or active state.', operations: ['manage_service_accounts'] },
  { method: 'POST', path: '/api/v1/auth/service-accounts/{id}/rotate', purpose: 'Rotate a service-account API key.', operations: ['manage_service_accounts'] },
  { method: 'DELETE', path: '/api/v1/auth/service-accounts/{id}', purpose: 'Delete a service account.', operations: ['manage_service_accounts'] },
  { method: 'GET', path: '/api/v1/auth/audit-events', purpose: 'Filtered governance audit feed.', operations: ['view_audit'] },
  { method: 'GET', path: '/api/v1/auth/audit-events/export', purpose: 'CSV audit export.', operations: ['view_audit'] },
]

const endpointGroups = [
  {
    title: 'Domain, Repository, Projects, Releases',
    endpoints: [
      { method: 'GET', path: '/api/v1/domains', operations: ['view_domains'] },
      { method: 'POST', path: '/api/v1/domains', operations: ['manage_domains'] },
      { method: 'GET', path: '/api/v1/domains/{id}', operations: ['view_domains'] },
      { method: 'PUT', path: '/api/v1/domains/{id}', operations: ['manage_domains'] },
      { method: 'DELETE', path: '/api/v1/domains/{id}', operations: ['manage_domains'] },
      { method: 'GET', path: '/api/v1/domains/{id}/application-repository', operations: ['view_application_repository'] },
      { method: 'PUT', path: '/api/v1/domains/{id}/application-repository', operations: ['manage_application_repository'] },
      { method: 'GET', path: '/api/v1/domains/{id}/application-repository/categories', operations: ['view_application_repository'] },
      { method: 'POST', path: '/api/v1/domains/{id}/application-repository/categories', operations: ['manage_application_repository'] },
      { method: 'PUT', path: '/api/v1/application-repository/categories/{id}', operations: ['manage_application_repository'] },
      { method: 'DELETE', path: '/api/v1/application-repository/categories/{id}', operations: ['manage_application_repository'] },
      { method: 'GET', path: '/api/v1/projects', operations: ['view_projects'] },
      { method: 'POST', path: '/api/v1/projects', operations: ['manage_repository_projects'] },
      { method: 'GET', path: '/api/v1/projects/{id}', operations: ['view_projects'] },
      { method: 'PUT', path: '/api/v1/projects/{id}', operations: ['manage_projects'] },
      { method: 'DELETE', path: '/api/v1/projects/{id}', operations: ['manage_projects'] },
      { method: 'GET', path: '/api/v1/projects/{id}/releases', operations: ['view_projects'] },
      { method: 'POST', path: '/api/v1/projects/{id}/releases', operations: ['manage_repository_releases'] },
      { method: 'GET', path: '/api/v1/projects/{pid}/folders', operations: ['view_projects'] },
      { method: 'POST', path: '/api/v1/projects/{pid}/folders', operations: ['manage_projects'] },
      { method: 'PUT', path: '/api/v1/folders/{id}', operations: ['manage_projects'] },
      { method: 'DELETE', path: '/api/v1/folders/{id}', operations: ['manage_projects'] },
      { method: 'GET', path: '/api/v1/applications?project_id=<id>', operations: ['view_applications'] },
      { method: 'POST', path: '/api/v1/applications', operations: ['manage_applications'] },
      { method: 'GET', path: '/api/v1/applications/{id}', operations: ['view_applications'] },
      { method: 'PUT', path: '/api/v1/applications/{id}', operations: ['manage_applications'] },
      { method: 'DELETE', path: '/api/v1/applications/{id}', operations: ['manage_applications'] },
      { method: 'GET', path: '/api/v1/applications/{id}/load-comparison?scope=transaction|group&key=<name>&test_id=<run>', operations: ['view_applications'] },
      { method: 'GET', path: '/api/v1/applications/{id}/releases', operations: ['view_applications'] },
      { method: 'POST', path: '/api/v1/applications/{id}/releases', operations: ['manage_applications', 'manage_repository_releases'] },
      { method: 'GET', path: '/api/v1/releases/{id}', operations: ['view_applications'] },
      { method: 'PUT', path: '/api/v1/releases/{id}', operations: ['manage_applications', 'manage_repository_releases'] },
      { method: 'DELETE', path: '/api/v1/releases/{id}', operations: ['manage_applications', 'manage_repository_releases'] },
      { method: 'POST', path: '/api/v1/releases/{id}/archive', operations: ['manage_applications', 'manage_repository_releases'] },
      { method: 'GET', path: '/api/v1/releases/{id}/trend', operations: ['view_applications'] },
      { method: 'GET', path: '/api/v1/releases/{id}/trend/export', operations: ['view_applications'] },
      { method: 'GET', path: '/api/v1/releases/{id}/sla-governance', operations: ['view_applications'] },
      { method: 'POST', path: '/api/v1/releases/{id}/sla-governance/waivers', operations: ['manage_applications', 'manage_repository_releases'] },
    ],
  },
  {
    title: 'Scripts, Imports, Authoring',
    endpoints: [
      { method: 'GET', path: '/api/v1/scripts', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/{id}', operations: ['view_scripts'] },
      { method: 'PUT', path: '/api/v1/scripts/{id}', operations: ['manage_scripts'] },
      { method: 'DELETE', path: '/api/v1/scripts/{id}', operations: ['manage_scripts'] },
      { method: 'PATCH', path: '/api/v1/scripts/{id}/move', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/validate', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/{id}/validate', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/replay/sessions', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/{id}/replay/sessions', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/browser-to-http/preview', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/authoring/capabilities', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/authoring/templates', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/authoring/components', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/authoring/sessions', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/authoring/sessions', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/authoring/sessions/{id}', operations: ['view_scripts'] },
      { method: 'PUT', path: '/api/v1/scripts/authoring/sessions/{id}', operations: ['manage_scripts'] },
      { method: 'DELETE', path: '/api/v1/scripts/authoring/sessions/{id}', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/authoring/sessions/{id}/approve', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/authoring/sessions/{id}/correlations/preview', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/correlations/rules', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/correlations/diagnose', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/{id}/export', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/{id}/download', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/{id}/runtime-settings/report', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/enterprise-script-archive', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/enterprise-script-archive/validate', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/import/enterprise-script-archive/support-matrix', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/jmeter', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/jmeter/save', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/jmeter/validate', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/import/jmeter/support-matrix', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/sample', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/devweb', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/import/devweb/support-matrix', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/import/oracle-ebusiness/support-matrix', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/oracle-ebusiness/review', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/har', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/openapi', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/postman', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/import/wsdl', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/web-api/support-matrix', operations: ['view_scripts'] },
    ],
  },
  {
    title: 'Replay And Recorder',
    endpoints: [
      { method: 'GET', path: '/api/v1/replay/sessions/{session_id}', operations: ['view_replay', 'view_scripts'] },
      { method: 'GET', path: '/api/v1/replay/sessions/{session_id}/export?format=json|log', operations: ['view_replay', 'view_scripts'] },
      { method: 'POST', path: '/api/v1/replay/sessions/{session_id}/pause', operations: ['manage_replay', 'manage_scripts'] },
      { method: 'POST', path: '/api/v1/replay/sessions/{session_id}/resume', operations: ['manage_replay', 'manage_scripts'] },
      { method: 'POST', path: '/api/v1/replay/sessions/{session_id}/step', operations: ['manage_replay', 'manage_scripts'] },
      { method: 'POST', path: '/api/v1/replay/sessions/{session_id}/stop', operations: ['manage_replay', 'manage_scripts'] },
      { method: 'PATCH', path: '/api/v1/replay/sessions/{session_id}/breakpoints', operations: ['manage_replay', 'manage_scripts'] },
      { method: 'GET', path: '/api/v1/browser-replay/sessions/{session_id}', operations: ['view_replay'] },
      { method: 'POST', path: '/api/v1/browser-replay/sessions/{session_id}/pause', operations: ['manage_replay'] },
      { method: 'POST', path: '/api/v1/browser-replay/sessions/{session_id}/resume', operations: ['manage_replay'] },
      { method: 'POST', path: '/api/v1/browser-replay/sessions/{session_id}/stop', operations: ['manage_replay'] },
      { method: 'GET', path: '/api/v1/recorder', operations: ['view_replay'] },
      { method: 'POST', path: '/api/v1/recorder/start', operations: ['manage_replay'] },
      { method: 'GET', path: '/api/v1/recorder/{id}', operations: ['view_replay'] },
      { method: 'POST', path: '/api/v1/recorder/{id}/pause', operations: ['manage_replay'] },
      { method: 'POST', path: '/api/v1/recorder/{id}/resume', operations: ['manage_replay'] },
      { method: 'POST', path: '/api/v1/recorder/{id}/stop', operations: ['manage_replay'] },
      { method: 'DELETE', path: '/api/v1/recorder/{id}', operations: ['manage_replay'] },
      { method: 'PATCH', path: '/api/v1/recorder/{id}/requests/{seq}', operations: ['manage_replay'] },
      { method: 'POST', path: '/api/v1/recorder/{id}/convert/preview', operations: ['manage_replay'] },
      { method: 'POST', path: '/api/v1/recorder/{id}/convert', operations: ['manage_replay'] },
      { method: 'GET', path: '/api/v1/recorder/{id}/export', operations: ['view_replay'] },
      { method: 'GET', path: '/api/v1/recorder/status', operations: ['view_replay'] },
      { method: 'GET', path: '/api/v1/recorder/ca.pem', operations: ['view_replay'] },
    ],
  },
  {
    title: 'GitHub Enterprise Script Sync And CI',
    endpoints: [
      { method: 'GET', path: '/api/v1/integrations/github/config', purpose: 'GitHub OAuth/App/PAT capability flags and safe configuration.', operations: ['view_scripts', 'view_system'] },
      { method: 'GET', path: '/api/v1/integrations/github/diagnostics', purpose: 'GHES reachability, auth-mode, installation, webhook, PAT, and check-run readiness.', operations: ['view_scripts', 'view_system'] },
      { method: 'GET', path: '/api/v1/integrations/github/installations', purpose: 'Known GitHub App installations visible to automation.', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/integrations/github/login', purpose: 'Begin per-user OAuth connect flow.', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/callback', purpose: 'OAuth callback for the GitHub connect flow.', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/connection', purpose: 'Current user GitHub connection status.', operations: ['view_scripts'] },
      { method: 'DELETE', path: '/api/v1/integrations/github/connection', purpose: 'Disconnect the current user from GitHub.', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/repos', purpose: 'List repositories for the resolved GitHub identity.', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/branches', purpose: 'List branches for one repository.', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/contents', purpose: 'Browse repository contents for import review.', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/projects/{id}/github/policy', purpose: 'Read the authoritative project GitHub repository policy.', operations: ['view_projects', 'view_scripts'] },
      { method: 'PUT', path: '/api/v1/projects/{id}/github/policy', purpose: 'Update allowed repos, branches, paths, PR rules, bulk sync, webhook, and checks policy.', operations: ['manage_projects'] },
      { method: 'POST', path: '/api/v1/scripts/import/github', purpose: 'Import one GitHub JSON DSL file through the review/save path.', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/scripts/{id}/github/push', purpose: 'Push one local script to GitHub by direct commit or pull request.', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/scripts/{id}/github/status', purpose: 'Read link, drift, blob SHA, commit SHA, policy, auth mode, rate-limit, and check-run status.', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/integrations/github/sync-jobs', purpose: 'Create a bulk import or push sync job.', operations: ['manage_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/sync-jobs', purpose: 'List bulk GitHub sync jobs.', operations: ['view_scripts'] },
      { method: 'GET', path: '/api/v1/integrations/github/sync-jobs/{id}', purpose: 'Read one sync job progress model.', operations: ['view_scripts'] },
      { method: 'POST', path: '/api/v1/integrations/github/webhook', purpose: 'Receive and verify GitHub push/PR webhooks with X-Hub-Signature-256.' },
      { method: 'GET', path: '/api/v1/integrations/github/webhooks', purpose: 'List webhook delivery records and dedupe outcomes.', operations: ['view_system'] },
      { method: 'POST', path: '/api/v1/tests/{id}/github/check-run', purpose: 'Publish or retry an APEX Load result as a GitHub Check Run.', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/integrations/github/check-links', purpose: 'List APEX run-to-GitHub check links.', operations: ['view_tests', 'view_system'] },
    ],
  },
  {
    title: 'Scenarios, Schedules, Data Pools',
    endpoints: [
      { method: 'GET', path: '/api/v1/scenarios', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/scenarios', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/scenarios/validate-load-model', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}', operations: ['view_tests'] },
      { method: 'PUT', path: '/api/v1/scenarios/{id}', operations: ['manage_tests'] },
      { method: 'DELETE', path: '/api/v1/scenarios/{id}', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/load-models/support-matrix', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/load-models/templates', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/results', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/runtime-settings/report', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/schedules', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/scenarios/{id}/schedules', operations: ['manage_tests'] },
      { method: 'PUT', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}', operations: ['manage_tests'] },
      { method: 'DELETE', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences', operations: ['view_tests'] },
      { method: 'DELETE', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences/{occurrence_id}', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/schedules/health', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/scenarios/{id}/data-pools', operations: ['view_tests', 'view_data_pools'] },
      { method: 'PUT', path: '/api/v1/scenarios/{id}/data-pools', operations: ['manage_tests', 'manage_data_pools'] },
      { method: 'GET', path: '/api/v1/data-pools', operations: ['view_data_pools'] },
      { method: 'POST', path: '/api/v1/data-pools', operations: ['manage_data_pools'] },
      { method: 'GET', path: '/api/v1/data-pools/{id}', operations: ['view_data_pools'] },
      { method: 'PUT', path: '/api/v1/data-pools/{id}', operations: ['manage_data_pools'] },
      { method: 'DELETE', path: '/api/v1/data-pools/{id}', operations: ['manage_data_pools'] },
      { method: 'GET', path: '/api/v1/data-pools/{id}/rows', operations: ['view_data_pools'] },
      { method: 'POST', path: '/api/v1/data-pools/{id}/rows', operations: ['manage_data_pools'] },
      { method: 'PUT', path: '/api/v1/data-pools/{id}/rows/{row_id}', operations: ['manage_data_pools'] },
      { method: 'DELETE', path: '/api/v1/data-pools/{id}/rows/{row_id}', operations: ['manage_data_pools'] },
    ],
  },
  {
    title: 'Runs, Archives, Results',
    endpoints: [
      { method: 'GET', path: '/api/v1/tests', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/tests', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/estimate', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/manifest', purpose: 'Canonical live or archived run configuration manifest.', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/start', purpose: 'Start queued runs; script shakeout failures return a reviewable 409 payload.', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/stop', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/abort', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/pause', operations: ['manage_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/resume', operations: ['manage_tests'] },
      { method: 'PATCH', path: '/api/v1/tests/{id}/vusers', purpose: 'Run, pause, stop, or ramp a specific VUser group.', operations: ['manage_tests'] },
      { method: 'PATCH', path: '/api/v1/tests/{id}/duration', operations: ['manage_tests'] },
      { method: 'PATCH', path: '/api/v1/tests/{id}/move', operations: ['manage_tests'] },
      { method: 'DELETE', path: '/api/v1/tests/{id}', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/metrics', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/sla-status', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/report', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/tests/{id}/comparison-baseline/pin', operations: ['manage_tests'] },
      { method: 'DELETE', path: '/api/v1/tests/{id}/comparison-baseline/pin', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/manifest', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/files/{name}', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/browser-diagnostics', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/transactions', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/resources', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/summaries', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/report', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/root-cause', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/ai-root-cause', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/archive/comparison', operations: ['view_tests'] },
      { method: 'POST', path: '/api/v1/results/import', operations: ['manage_tests'] },
      { method: 'GET', path: '/api/v1/results/imports', operations: ['view_tests', 'view_system'] },
    ],
  },
  {
    title: 'System, Fleet, Usage, Library',
    endpoints: [
      { method: 'GET', path: '/api/v1/system/health' },
      { method: 'GET', path: '/api/v1/system/readiness' },
      { method: 'GET', path: '/api/v1/system/inventory', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/system/usage', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/system/usage/export', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/system/usage/snapshots', operations: ['view_system'] },
      { method: 'POST', path: '/api/v1/system/usage/snapshots', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/system/settings', operations: ['view_system'] },
      { method: 'PUT', path: '/api/v1/system/settings', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/desktop/status', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/system/browser-benchmarks', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/runtime-settings/support-matrix', operations: ['view_system', 'view_scripts', 'view_tests'] },
      { method: 'GET', path: '/api/v1/protocol-bundles/support-matrix', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/protocols/native-support-matrix', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/integrations/apm/profiles', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/monitoring/packs', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/reports/profiles', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/fleet/alerts', operations: ['view_system'] },
      { method: 'PATCH', path: '/api/v1/fleet/alerts/{id}/ack', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/fleet/capacity', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/fleet/provisioner', operations: ['view_system'] },
      { method: 'POST', path: '/api/v1/fleet/generators/{id}/cordon', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/fleet/generators/{id}/uncordon', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/fleet/generators/{id}/drain', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/fleet/generators/{id}/evacuate', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/fleet/provision-requests', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/fleet/provision-requests/{id}', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/fleet/provision-requests/{id}/cancel', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/fleet/provision-requests/{id}/retry', operations: ['manage_system'] },
      { method: 'PATCH', path: '/api/v1/system/generators/drain', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/system/parity-status', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/library/capabilities', operations: ['view_system', 'view_scripts', 'view_tests'] },
      { method: 'GET', path: '/api/v1/library/catalog', operations: ['view_scripts', 'view_tests'] },
      { method: 'POST', path: '/api/v1/library/export', operations: ['view_scripts', 'view_tests'] },
      { method: 'POST', path: '/api/v1/library/import', operations: ['manage_scripts', 'manage_tests'] },
      { method: 'POST', path: '/api/v1/library/import/archive', operations: ['manage_scripts', 'manage_tests'] },
      { method: 'GET', path: '/api/v1/library/results/{id}/archive', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/library/remotes', operations: ['view_system'] },
      { method: 'POST', path: '/api/v1/library/remotes', operations: ['manage_system'] },
      { method: 'PUT', path: '/api/v1/library/remotes/{id}', operations: ['manage_system'] },
      { method: 'DELETE', path: '/api/v1/library/remotes/{id}', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/library/remotes/{id}/probe', operations: ['manage_system'] },
      { method: 'GET', path: '/api/v1/library/remotes/{id}/catalog', operations: ['view_scripts', 'view_tests'] },
      { method: 'POST', path: '/api/v1/library/remotes/{id}/pull', operations: ['manage_scripts', 'manage_tests'] },
      { method: 'POST', path: '/api/v1/library/remotes/{id}/push', operations: ['manage_scripts', 'manage_tests'] },
      { method: 'POST', path: '/api/v1/library/remotes/{id}/diff', operations: ['view_scripts', 'view_tests'] },
      { method: 'GET', path: '/api/v1/ws', operations: ['view_tests'] },
      { method: 'GET', path: '/api/v1/tests/{id}/stream', operations: ['view_tests'] },
      { method: 'GET', path: '/metrics' },
    ],
  },
  {
    title: 'AI And Governance',
    endpoints: [
      { method: 'GET', path: '/api/v1/ai/authoring/contract', operations: ['view_system'] },
      { method: 'GET', path: '/api/v1/ai/policy', operations: ['view_system', 'view_tests'] },
      { method: 'GET', path: '/api/v1/ai/review-queue', operations: ['view_system', 'view_tests'] },
      { method: 'POST', path: '/api/v1/ai/drafts', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/ai/repairs', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/ai/explain', operations: ['manage_scripts'] },
      { method: 'POST', path: '/api/v1/archives/retention/plan', operations: ['manage_system'] },
      { method: 'POST', path: '/api/v1/archives/retention/apply', operations: ['manage_system'] },
    ],
  },
]

const statusRows = [
  ['200', 'Request succeeded.'],
  ['201', 'Resource created.'],
  ['202', 'Long-running operation accepted.'],
  ['400', 'Invalid request body or query parameter.'],
  ['401', 'Authentication required or token invalid.'],
  ['403', 'Authenticated but blocked by RBAC or service-account scope.'],
  ['404', 'Resource not found.'],
  ['409', 'Conflict, such as a schedule conflict, capacity shortfall, or script_shakeout_failed review gate.'],
]

type ApiGuidePageProps = {
  activePage?: ApiGuideContentPageID
  onNavigate?: (page: 'access') => void
  effectiveAccessResources?: DashboardAuthEffectiveAccessResource[]
}

export default function ApiGuidePage({ activePage = API_GUIDE_DEFAULT_PAGE, onNavigate, effectiveAccessResources = [] }: ApiGuidePageProps) {
  const [activeGuideSection, setActiveGuideSection] = useState<ApiGuideContentPageID>(activePage)
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const allowedOperations = buildAllowedOperationSet(effectiveAccessResources)
  const visibleQuickStartCommands = quickStartCommands.filter(command => canShowEndpoint(command, allowedOperations))
  const visibleAuthRows = authRows.filter(row => canShowEndpoint(row, allowedOperations))
  const visibleEndpointGroups = endpointGroups
    .map(group => ({
      ...group,
      endpoints: group.endpoints.filter(endpoint => canShowEndpoint(endpoint, allowedOperations)),
    }))
    .filter(group => group.endpoints.length > 0)
  const canUseAccessWorkspace = ['manage_users', 'manage_user_bindings', 'view_user_activity', 'manage_service_accounts'].some(operation => allowedOperations.has(operation))
  const canViewFullRepositoryGuide = allowedOperations.has('manage_system') && allowedOperations.has('view_audit')
  const handleContentPageChange = useCallback((page: ApiGuideContentPageID) => {
    setActiveGuideSection(page)
  }, [])
  useApiGuideTopbarContribution(activeGuideSection, 'API Guide', handleContentPageChange)

  useEffect(() => {
    setActiveGuideSection(activePage)
  }, [activePage])

  const activeContentPage = activeGuideSection

  return (
    <div className="page api-guide-page">
      <div className="page-header">
        <div>
          <h2>API Guide</h2>
          <p className="page-subtitle">REST and WebSocket reference for automating APEX Load from CI/CD, integrations, and operator tooling.</p>
        </div>
      </div>
      {!hasTopbarContributionProvider && (
        <div className="inline-create-row api-guide-section-switcher">
          <ApiGuideTopbarToggle active={activeGuideSection} onContentPageChange={handleContentPageChange} />
        </div>
      )}

      {activeContentPage === 'general' && (
        <>
          <section className="api-guide-hero" aria-labelledby="api-guide-base-url">
            <div>
              <span className="api-guide-kicker">Base URL</span>
              <h3 id="api-guide-base-url">http://&lt;controller-host&gt;:8080/api/v1</h3>
              <p>Local dashboard development proxies `/api` from `localhost:3000`; direct clients should call the controller origin.</p>
            </div>
            <div className="api-guide-meta-grid">
              <div><span>Auth</span><strong>Dev, OIDC/trusted headers, local users, service accounts</strong></div>
              <div><span>Payloads</span><strong>JSON plus CSV, HTML, ZIP, and archive downloads</strong></div>
              <div><span>Streams</span><strong>WebSocket at /api/v1/ws</strong></div>
              <div><span>MCP</span><strong>FastMCP bridge for agent and CI automation</strong></div>
            </div>
          </section>

          <section className="page-section">
            <div className="api-guide-section-header">
              <h3>Responses And Errors</h3>
              <p>JSON error responses use <code>{'{"error":"message"}'}</code>. Download endpoints may return CSV, HTML, ZIP, or binary artifacts.</p>
            </div>
            <div className="api-guide-status-grid">
              {statusRows.map(([status, meaning]) => (
                <div className="api-guide-status-card" key={status}>
                  <strong>{status}</strong>
                  <span>{meaning}</span>
                </div>
              ))}
            </div>
          </section>

          <section className="api-guide-callout">
            <h3>Dev Preview Headers</h3>
            <p>In local dev auth mode only, API clients may send <code>X-APEXLoad-Dev-View: admin|user</code> and <code>X-APEXLoad-Dev-Project-ID: &lt;project-id&gt;</code> to simulate dashboard Admin or project editor User behavior. Production auth modes ignore these headers.</p>
          </section>
        </>
      )}

      {activeContentPage === 'mcp' && (
        <section className="page-section api-guide-mcp-section">
          <div className="api-guide-section-header">
            <h3>MCP Server</h3>
            <p>Run the APEX Load MCP bridge for agent-driven automation. It uses service-account auth and adds confirmation guardrails before mutating API calls.</p>
          </div>
          <div className="api-guide-mcp-grid">
            <article className="api-guide-mcp-card">
              <h4>Start The Server</h4>
              <pre><code>{'APEXLOAD_URL=http://localhost:8080/api/v1 \\\nAPEXLOAD_API_KEY=srk_... \\\npython3 mcp/server.py'}</code></pre>
              <a href="https://github.com/jasonczech/apexload/blob/main/mcp/README.md" target="_blank" rel="noreferrer">MCP setup guide</a>
            </article>
            <article className="api-guide-mcp-card">
              <h4>Tool Coverage</h4>
              <p>Structured tools cover system, auth, projects, scripts, scenarios, runs, reports, scheduling, fleet, data pools, imports, and AI assistance.</p>
              <div className="api-guide-mcp-tags">
                <span>service accounts</span>
                <span>confirm=true</span>
                <span>reason required</span>
              </div>
            </article>
            <article className="api-guide-mcp-card">
              <h4>Agent Resources</h4>
              <ul>
                {mcpResources.map(resource => (
                  <li key={resource}><code>{resource}</code></li>
                ))}
              </ul>
            </article>
          </div>
        </section>
      )}

      {activeContentPage === 'quick-start' && (
        <section className="page-section">
          <div className="api-guide-section-header">
            <h3>Quick Start</h3>
            <p>Use a service account key for automation and keep project/application/release ids in your pipeline context.</p>
          </div>
          <div className="api-guide-code-stack">
            {visibleQuickStartCommands.map(({ command }) => (
              <pre key={command}><code>{command}</code></pre>
            ))}
          </div>
        </section>
      )}

      {activeContentPage === 'authentication' && (
        <>
          <section className="page-section">
            <div className="api-guide-section-header">
              <h3>Authentication</h3>
              <p>OIDC or trusted headers prove human identity, but an active local APEX Load user record is required after bootstrap. Human roles come from local bindings; service accounts can use `Authorization: Bearer &lt;api-key&gt;` or `X-APEXLoad-API-Key`, with RBAC roles and token scopes both enforced.</p>
            </div>
            <div className="api-guide-table-card">
              <table>
                <thead>
                  <tr><th>Method</th><th>Endpoint</th><th>Use</th></tr>
                </thead>
                <tbody>
                  {visibleAuthRows.map(({ method, path, purpose }) => (
                    <tr key={`${method}-${path}`}>
                      <td><code>{method}</code></td>
                      <td><code>{path}</code></td>
                      <td>{purpose}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>

          {canUseAccessWorkspace && (
            <section className="api-guide-callout api-guide-key-callout">
              <div>
                <h3>Access Workspace</h3>
                <p>Use Access to onboard human users, assign global/domain/project roles, review activity by user and scope, and generate service-account keys for MCP, CI/CD, reporting exports, and controlled external automation.</p>
              </div>
              {onNavigate && (
                <button className="btn btn-secondary btn-sm btn-accent-action" type="button" onClick={() => onNavigate('access')}>
                  Open Access
                </button>
              )}
            </section>
          )}
        </>
      )}

      {activeContentPage === 'endpoint-map' && (
        <section className="page-section">
          <div className="api-guide-section-header">
            <h3>Endpoint Map</h3>
            <p>{canViewFullRepositoryGuide ? <>The repository guide at <code>docs/API_USER_GUIDE.md</code> includes the longer workflow notes and expanded catalogue.</> : 'This endpoint map is filtered to your current domain, project, and role access.'}</p>
          </div>
          <div className="api-guide-endpoint-grid">
            {visibleEndpointGroups.map(group => (
              <article className="api-guide-card" key={group.title}>
                <h4>{group.title}</h4>
                <ul>
                  {group.endpoints.map(endpoint => (
                    <li key={`${endpoint.method}-${endpoint.path}`}><code>{formatEndpoint(endpoint)}</code></li>
                  ))}
                </ul>
              </article>
            ))}
          </div>
        </section>
      )}
    </div>
  )
}

function buildAllowedOperationSet(resources: DashboardAuthEffectiveAccessResource[]): Set<string> {
  const allowed = new Set<string>()
  resources.forEach(resource => {
    resource.operations.forEach(operation => {
      if (operation.allowed) {
        allowed.add(operation.id)
      }
    })
  })
  return allowed
}

function canShowEndpoint(item: { operations?: string[] }, allowedOperations: Set<string>): boolean {
  if (!item.operations || item.operations.length === 0) {
    return true
  }
  return item.operations.some(operation => allowedOperations.has(operation))
}

function formatEndpoint(endpoint: EndpointDoc): string {
  return `${endpoint.method} ${endpoint.path}`
}
