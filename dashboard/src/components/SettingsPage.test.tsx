// @vitest-environment jsdom

import { act, useState, type ReactElement } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SettingsPage from './SettingsPage'
import { reconcileSettingsProjectSnapshots } from './settingsProjectCatalog'
import type { WailsDesktopBridge } from '../desktop/desktopBridge'
import type { Application, DashboardAuthEffectiveAccessResource, Domain, GitHubActionsPolicy, GitHubProjectPolicy, Project, ProjectSettings } from '../types'
import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import { NavigationGuardRegisterContext, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { ApiError } from '../hooks/apiClient'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { downloadBlobMock } = vi.hoisted(() => ({
  downloadBlobMock: vi.fn(),
}))

const apiMock = {
  getAuthEffectiveAccess: vi.fn(),
  getAuthAccessMatrix: vi.fn(),
  getAuthPolicy: vi.fn(),
  getAuthDiagnostics: vi.fn(),
  getAuthRoleCatalog: vi.fn(),
  listAuthServiceAccounts: vi.fn(),
  createAuthServiceAccount: vi.fn(),
  updateAuthServiceAccount: vi.fn(),
  rotateAuthServiceAccount: vi.fn(),
  deleteAuthServiceAccount: vi.fn(),
  listAuthAuditEvents: vi.fn(),
  downloadAuthAuditEvents: vi.fn(),
  getSystemSettings: vi.fn(),
  getHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  getSystemReadiness: vi.fn(),
  getUsageAccounting: vi.fn(),
  listUsageAccountingSnapshots: vi.fn(),
  captureUsageAccountingSnapshot: vi.fn(),
  downloadUsageAccounting: vi.fn(),
  updateSystemSettings: vi.fn(),
  planArchiveRetention: vi.fn(),
  applyArchiveRetention: vi.fn(),
  listDataPools: vi.fn(),
  getDataPool: vi.fn(),
  createDataPool: vi.fn(),
  updateDataPool: vi.fn(),
  deleteDataPool: vi.fn(),
  createDataPoolRow: vi.fn(),
  updateDataPoolRow: vi.fn(),
  deleteDataPoolRow: vi.fn(),
  importDataPoolRows: vi.fn(),
  exportDataPoolRows: vi.fn(),
  listTests: vi.fn(),
  listProjectSecrets: vi.fn(),
  getProjectSecretUsage: vi.fn(),
  createProjectSecrets: vi.fn(),
  rotateProjectSecret: vi.fn(),
  listApplicationSecrets: vi.fn(),
  getApplicationSecretStatuses: vi.fn(),
  getApplicationSecretUsage: vi.fn(),
}
const apiProviderState = vi.hoisted(() => ({ current: undefined as unknown }))

const applicationApiMock = {
  listApplications: vi.fn(),
  listReleases: vi.fn(),
  updateApplication: vi.fn(),
  deleteApplication: vi.fn(),
  updateRelease: vi.fn(),
  archiveRelease: vi.fn(),
  deleteRelease: vi.fn(),
}

const projectApiMock = {
  listProjects: vi.fn(),
  createProject: vi.fn(),
  updateProject: vi.fn(),
  deleteProject: vi.fn(),
  listFolders: vi.fn(),
  createFolder: vi.fn(),
  updateFolder: vi.fn(),
  deleteFolder: vi.fn(),
}

const repositoryApiMock = {
  listDomains: vi.fn(),
  createDomain: vi.fn(),
  updateDomain: vi.fn(),
  deleteDomain: vi.fn(),
}

const applicationContextMock = {
  applications: [],
  applicationsLoading: false,
  applicationsError: '',
  releases: [],
  activeApplication: null,
  activeRelease: null,
  setActiveApplication: vi.fn(),
  setActiveRelease: vi.fn(),
  removeApplication: vi.fn(),
  removeRelease: vi.fn(),
  refreshApplications: vi.fn(),
  refreshReleases: vi.fn(),
  createAndSelectApplication: vi.fn(),
}

// SettingsPage reads its project catalog from this context, which App provides
// already filtered to the active domain — it no longer calls listProjects.
const projectContextMock = {
  projects: [{ id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }] as Project[],
  projectsError: '',
  activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' } as Project | null,
  setActiveProject: vi.fn(),
  createAndSelectProject: vi.fn(),
  updateProject: vi.fn(),
  deleteProject: vi.fn(),
}

// Seeds the project catalog SettingsPage renders. It reads ProjectContext, which
// App provides already filtered to the active domain; listProjects is still
// stubbed for the tests that assert on the API surface itself.
function seedProjectCatalog(projects: Project[]) {
  projectContextMock.projects = projects
  projectApiMock.listProjects.mockResolvedValue({ projects, count: projects.length })
}

const fleetApiMock = {
  listFleetPools: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiProviderState.current,
}))

vi.mock('../hooks/api/useFleetApi', () => ({
  useFleetApi: () => fleetApiMock,
}))

vi.mock('../hooks/api/useApplicationApi', () => ({
  useApplicationApi: () => applicationApiMock,
}))

vi.mock('../hooks/api/useProjectApi', () => ({
  useProjectApi: () => projectApiMock,
}))

vi.mock('../hooks/api/useRepositoryApi', () => ({
  useRepositoryApi: () => repositoryApiMock,
}))

vi.mock('../hooks/useApplication', () => ({
  useApplication: () => applicationContextMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => projectContextMock,
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: downloadBlobMock,
}))

const defaultDomain: Domain = {
  id: 'default',
  name: 'Default Domain',
  description: 'Default migrated domain',
  created_at: '2026-05-01T00:00:00Z',
  updated_at: '2026-05-01T00:00:00Z',
}

const retailDomain: Domain = {
  id: 'domain-retail',
  name: 'Retail',
  description: 'Retail apps',
  created_at: '2026-05-02T00:00:00Z',
  updated_at: '2026-05-02T00:00:00Z',
}

function domainAccessResources(canManageDomains = true): DashboardAuthEffectiveAccessResource[] {
  return [
    {
      id: 'domains_and_application_repository',
      name: 'Domains and Application Repository',
      description: 'Domain tenancy.',
      operations: [
        { id: 'view_domains', label: 'View domains', allowed: true, grant_source: 'global' },
        { id: 'manage_domains', label: 'Manage domains', allowed: canManageDomains, grant_source: canManageDomains ? 'global' : undefined },
      ],
    },
  ]
}

function SettingsPageDomainHarness({ initialDomains = [defaultDomain, retailDomain] }: { initialDomains?: Domain[] }) {
  const [domains, setDomains] = useState<Domain[]>(initialDomains)
  const [activeDomain, setActiveDomain] = useState<Domain | null>(initialDomains[0] || null)

  const refreshDomains = async (preferredId?: string) => {
    const response = await repositoryApiMock.listDomains()
    const nextDomains: Domain[] = response.domains || []
    const nextActive = nextDomains.find(domain => domain.id === preferredId)
      || nextDomains.find(domain => domain.id === activeDomain?.id)
      || nextDomains[0]
      || null
    setDomains(nextDomains)
    setActiveDomain(nextActive)
    return nextDomains
  }

  return (
    <SettingsPage
      theme="dark"
      onThemeChange={vi.fn()}
      domains={domains}
      activeDomain={activeDomain}
      onDomainChange={setActiveDomain}
      onDomainsRefresh={refreshDomains}
    />
  )
}

function SettingsTopbarHarness() {
  const contribution = useActiveTopbarContribution()
  const visibleSecondaryActions = (contribution?.secondaryActions || []).filter(action => !action.hidden)
  const visiblePrimaryActions = (contribution?.primaryActions || []).filter(action => !action.hidden)

  return (
    <div data-testid="settings-topbar">
      {contribution?.titleNode || <span>{contribution?.title}</span>}
      {contribution?.controls}
      {visibleSecondaryActions.map(action => (
        <button key={action.id} type="button" disabled={action.disabled} onClick={action.onClick}>
          {action.label}
        </button>
      ))}
      {visiblePrimaryActions.map(action => (
        <button key={action.id} type="button" disabled={action.disabled} onClick={action.onClick}>
          {action.label}
        </button>
      ))}
    </div>
  )
}

function renderWithSettingsTopbar(testRoot: Root, page: ReactElement) {
  testRoot.render(
    <TopbarContributionProvider>
      <SettingsTopbarHarness />
      {page}
    </TopbarContributionProvider>,
  )
}

describe('SettingsPage', () => {
  let container: HTMLDivElement
  let root: Root
  type BridgeWindow = typeof window & { go?: { main?: { App?: WailsDesktopBridge } } }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    window.sessionStorage.clear()
    window.localStorage.removeItem('apexload.settings.section')
    window.localStorage.removeItem('apexload.settings.generalSubsection')
    window.localStorage.removeItem('apexload.settings.governanceSubsection')
    window.localStorage.removeItem('apexload.settings.capacitySubsection')
    window.localStorage.removeItem('apexload.settings.projectSubsection')
    window.localStorage.removeItem('apexload.settings.projectId')
    window.localStorage.removeItem('apexload.settings.integrationSubsection')
    delete (window as BridgeWindow).go
    projectContextMock.activeProject = { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    projectContextMock.setActiveProject.mockReset()
    projectContextMock.createAndSelectProject.mockReset()
    projectContextMock.updateProject.mockReset()
    projectContextMock.deleteProject.mockReset()
    projectContextMock.updateProject.mockImplementation(
      (projectID: string, project: unknown) => projectApiMock.updateProject(projectID, project),
    )
    Object.assign(applicationContextMock, {
      applications: [],
      applicationsLoading: false,
      applicationsError: '',
      releases: [],
      activeApplication: null,
      activeRelease: null,
    })
    apiProviderState.current = apiMock
    fleetApiMock.listFleetPools.mockReset()
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [] })

    apiMock.getSystemSettings.mockResolvedValue({
      revision: `ss-${'a'.repeat(64)}`,
      general: {
        api_endpoint: ':8080',
        max_concurrent_tests: 10,
        metrics_interval_seconds: 1,
        usage_snapshot_interval_seconds: 3600,
      },
      thresholds: {
        max_error_rate_pct: undefined,
        max_p99_latency_ms: undefined,
        max_avg_server_processing_ms: undefined,
        max_avg_transfer_ms: undefined,
      },
      capacity_planning: {
        headroom_pct: 20,
        generator: {
          cpu_cores: 4,
          memory_mb: 8192,
          network_mbps: 1000,
          max_vusers: 10000,
        },
        availability: {
          source: 'configured',
          configured_available_generators: 1,
        },
      },
      archive_retention: {
        enabled: true,
        default_retention_days: 45,
        policies: [],
      },
      schedule_defaults: {
        approval_policy: 'auto',
        missed_run_policy: 'skip',
        blackout_start_time: '',
        blackout_end_time: '',
        availability_start_time: '',
        availability_end_time: '',
      },
      notifications: {
        webhooks: [],
      },
      auth: {
        mode: 'dev',
        provider_name: '',
        login_url: '',
        logout_url: '',
        user_header: 'X-Auth-Request-User',
        name_header: 'X-Auth-Request-Name',
        email_header: 'X-Auth-Request-Email',
        roles_header: 'X-APEXLoad-Roles',
        project_roles_header: 'X-APEXLoad-Project-Roles',
      },
      github: {
        enabled: false,
        client_id: '',
        client_secret_env_var: 'APEXLOAD_GITHUB_CLIENT_SECRET',
        app_id: 0,
        app_slug: '',
        app_private_key_env_var: 'APEXLOAD_GITHUB_APP_PRIVATE_KEY',
        app_private_key_file: '',
        webhook_secret_env_var: 'APEXLOAD_GITHUB_WEBHOOK_SECRET',
        pat_env_var: '',
        automation_mode: 'app_oauth_pat',
        checks_enabled: false,
        api_base_url: 'https://api.github.com',
        web_base_url: 'https://github.com',
        ca_cert_file: '',
        tls_insecure_skip_verify: false,
        scopes: ['repo'],
        default_repo: '',
        default_branch: 'main',
        path_template: 'apexload/scripts/{project}/{ref_key}.json',
      },
      prometheus: {
        enabled: false,
        base_url: '',
        timeout_seconds: 5,
      },
    })
    apiMock.getAuthRoleCatalog.mockResolvedValue({
      provider_model: 'upstream_gateway',
      roles: [
        {
          id: 'system_admin',
          name: 'System Admin',
          scope: 'global',
          description: 'Full platform administration across settings, projects, and executions.',
          capabilities: ['Manage global settings', 'Create and delete projects'],
        },
        {
          id: 'project_admin',
          name: 'Project Admin',
          scope: 'global_or_project',
          description: 'Govern a project\'s assets, quotas, and execution lifecycle.',
          capabilities: ['Manage project governance'],
        },
      ],
    })
    apiMock.getAuthAccessMatrix.mockResolvedValue({
      version: 'trusted_headers_v1',
      resources: [
        {
          id: 'system_configuration',
          name: 'System Configuration',
          description: 'Controller-wide settings, infrastructure inventory, and global observer surfaces.',
          operations: [
            {
              id: 'view_system',
              label: 'View settings and inventory',
              global_roles: ['viewer', 'editor', 'project_admin', 'system_admin'],
            },
            {
              id: 'manage_system',
              label: 'Update system settings',
              global_roles: ['system_admin'],
            },
          ],
        },
      ],
    })
    // /auth/policy folds the role catalog + access matrix; SettingsPage now uses it.
    apiMock.getAuthPolicy.mockImplementation(async () => {
      const [roles, matrix] = await Promise.all([apiMock.getAuthRoleCatalog(), apiMock.getAuthAccessMatrix()])
      return { provider_model: roles.provider_model, version: matrix.version, roles: roles.roles, resources: matrix.resources }
    })
    apiMock.getAuthDiagnostics.mockResolvedValue({
      enabled: false,
      mode: 'dev',
      provider_model: 'development_bypass',
      ready: true,
      summary: 'Development bypass is active. Switch to Trusted Headers or Native OIDC before production use.',
      checks: [
        {
          id: 'development_bypass',
          label: 'Development Bypass',
          status: 'info',
          summary: 'APEX Load is running with local auth bypass enabled.',
          detail: 'All requests are allowed locally until you switch to Trusted Headers or Native OIDC.',
        },
      ],
    })
    apiMock.listAuthServiceAccounts.mockResolvedValue({
      service_accounts: [
        {
          id: 'svc-1',
          name: 'Nightly CI',
          description: 'Runs nightly smoke coverage',
          active: true,
          global_roles: ['viewer'],
          project_roles: { 'project-1': ['editor'] },
          scopes: ['automation:*'],
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-16T00:00:00Z',
        },
      ],
      count: 1,
    })
    apiMock.listAuthAuditEvents.mockResolvedValue({
      events: [
        {
          id: 'audit-1',
          action: 'system.settings.update',
          actor_type: 'dev_mode',
          actor_subject: 'dev-mode',
          actor_display_name: 'Development Mode',
          resource_type: 'system_settings',
          resource_id: 'global',
          status: 'success',
          created_at: '2026-05-16T01:00:00Z',
        },
      ],
      count: 1,
    })
    apiMock.downloadAuthAuditEvents.mockResolvedValue(new Blob(['id,action\n'], { type: 'text/csv' }))
    apiMock.createAuthServiceAccount.mockResolvedValue({
      service_account: {
        id: 'svc-2',
        name: 'Release Automation',
        description: 'Runs release checks',
        active: true,
        global_roles: ['editor'],
        project_roles: {},
        scopes: ['automation:*'],
        created_at: '2026-05-16T02:00:00Z',
        updated_at: '2026-05-16T02:00:00Z',
      },
      api_key: 'srk_test_key',
    })
    apiMock.updateAuthServiceAccount.mockResolvedValue({})
    apiMock.rotateAuthServiceAccount.mockResolvedValue({
      service_account: {
        id: 'svc-1',
        name: 'Nightly CI',
        description: 'Runs nightly smoke coverage',
        active: true,
        global_roles: ['viewer'],
        project_roles: { 'project-1': ['editor'] },
        scopes: ['automation:*'],
        created_at: '2026-05-16T00:00:00Z',
        updated_at: '2026-05-16T03:00:00Z',
      },
      api_key: 'srk_rotated_key',
    })
    apiMock.deleteAuthServiceAccount.mockResolvedValue({ deleted: 'svc-1' })
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [
        {
          id: 'tests_and_scenarios',
          name: 'Tests and Scenarios',
          description: 'Scenario definitions, queued runs, live controls, archives, and comparison baselines.',
          operations: [
            {
              id: 'view_tests',
              label: 'View scenarios, tests, metrics, and archives',
              allowed: true,
              grant_source: 'global',
            },
            {
              id: 'manage_tests',
              label: 'Create, start, stop, pause, move, scale, and delete tests or scenarios',
              allowed: true,
              grant_source: 'project',
            },
          ],
        },
        {
          id: 'system_configuration',
          name: 'System Configuration',
          description: '',
          operations: [
            { id: 'view_system', label: 'View settings and inventory', allowed: true, grant_source: 'global' },
            { id: 'manage_system', label: 'Update system settings', allowed: true, grant_source: 'global' },
            { id: 'manage_domains', label: 'Manage domains', allowed: true, grant_source: 'global' },
            { id: 'manage_projects', label: 'Manage projects', allowed: true, grant_source: 'global' },
            { id: 'manage_service_accounts', label: 'Manage service accounts', allowed: true, grant_source: 'global' },
            { id: 'view_audit', label: 'View audit', allowed: true, grant_source: 'global' },
          ],
        },
      ],
    })
    apiMock.getHealth.mockResolvedValue({ status: 'healthy', version: '0.1.0', ws_clients: 1, test_count: 0 })
    apiMock.getSystemInventory.mockResolvedValue({
      execution_mode: 'distributed',
      controller: {
        id: 'controller-1',
        status: 'healthy',
        address: ':8080',
        version: '0.1.0',
        execution_mode: 'distributed',
      },
      generators: [
        {
          target: 'apexload-generator-0.apexload-generator:9090',
          generator_id: 'apexload-generator-0',
          status: 'connected',
          available_capacity: 9600,
          active_vusers: 400,
          cpu_percent: 12,
          memory_bytes: 128 * 1024 * 1024,
          goroutine_count: 120,
	          cpu_limit_cores: 4,
	          memory_limit_bytes: 8 * 1024 * 1024 * 1024,
	          max_vusers: 10000,
        },
        {
          target: 'apexload-generator-1.apexload-generator:9090',
          generator_id: 'apexload-generator-1',
          status: 'connected',
          available_capacity: 7500,
          active_vusers: 500,
          cpu_percent: 15,
          memory_bytes: 140 * 1024 * 1024,
          goroutine_count: 122,
	          cpu_limit_cores: 2,
	          memory_limit_bytes: 4 * 1024 * 1024 * 1024,
	          max_vusers: 8000,
        },
      ],
    })
    apiMock.getSystemReadiness.mockResolvedValue({
      status: 'degraded',
      generated_at: '2026-05-18T12:05:00Z',
      checks: [
        {
          name: 'metadata_store',
          status: 'ready',
          message: 'metadata store is reachable',
        },
        {
          name: 'archive_writer',
          status: 'ready',
          message: 'archive root is writable',
          details: { archive_root: '/tmp/apexload-archives' },
        },
        {
          name: 'generator_rpc',
          status: 'degraded',
          message: 'some generator RPC targets are not reachable',
          details: { generators: 2, available: 1, unreachable: 1 },
        },
        {
          name: 'telemetry_fanout',
          status: 'ready',
          message: 'telemetry fan-out is initialized',
          details: { ws_clients: 1, metrics_interval_seconds: 1 },
        },
        {
          name: 'auth_mode',
          status: 'degraded',
          message: 'development auth bypass is enabled',
        },
        {
          name: 'scheduler_state',
          status: 'ready',
          message: 'scheduler state is initialized',
          details: { schedules_enabled: 1, schedules_due: 0 },
        },
      ],
    })
    apiMock.listDataPools.mockResolvedValue({ data_pools: [] })
    apiMock.getDataPool.mockResolvedValue({ rows: [] })
    apiMock.listTests.mockResolvedValue({ tests: [] })
    apiMock.getUsageAccounting.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      project_id: 'project-1',
      totals: {
        run_count: 2,
        total_vusers: 15,
        duration_seconds: 5400,
        vuser_seconds: 81000,
        archive_bytes: 2097152,
        status_counts: { COMPLETE: 2 },
      },
      by_protocol: [
        { key: 'http', run_count: 2, group_count: 2, total_vusers: 15, duration_seconds: 5400, vuser_seconds: 81000, status_counts: { COMPLETE: 2 } },
      ],
      by_region: [
        { key: 'us-east', run_count: 2, total_vusers: 15, duration_seconds: 5400, vuser_seconds: 81000, status_counts: { COMPLETE: 2 } },
      ],
    })
    apiMock.listUsageAccountingSnapshots.mockResolvedValue({
      snapshots: [
        {
          id: 'usage-snapshot-1',
          project_id: 'project-1',
          generated_at: '2026-05-18T12:00:00Z',
          created_at: '2026-05-18T12:01:00Z',
          usage: {
            generated_at: '2026-05-18T12:00:00Z',
            project_id: 'project-1',
            totals: {
              run_count: 2,
              total_vusers: 15,
              duration_seconds: 5400,
              vuser_seconds: 81000,
              archive_bytes: 2097152,
              status_counts: { COMPLETE: 2 },
            },
          },
        },
      ],
      count: 1,
      limit: 5,
      offset: 0,
    })
    apiMock.captureUsageAccountingSnapshot.mockResolvedValue({
      id: 'usage-snapshot-2',
      project_id: 'project-1',
      generated_at: '2026-05-18T13:00:00Z',
      created_at: '2026-05-18T13:01:00Z',
      usage: {
        generated_at: '2026-05-18T13:00:00Z',
        project_id: 'project-1',
        totals: {
          run_count: 3,
          total_vusers: 25,
          duration_seconds: 7200,
          vuser_seconds: 120000,
          archive_bytes: 3145728,
          status_counts: { COMPLETE: 3 },
        },
      },
    })
    apiMock.downloadUsageAccounting.mockResolvedValue(new Blob(['usage csv'], { type: 'text/csv' }))
    apiMock.planArchiveRetention.mockResolvedValue({
      generated_at: '2026-05-18T12:00:00Z',
      dry_run: true,
      candidates: [
        {
          test_id: 'run-expired',
          project_id: 'project-1',
          release_id: 'rel-1',
          run_status: 'COMPLETE',
          archive_path: '/tmp/run-expired',
          policy_name: 'default',
          artifact_type: 'bundle',
          compliance_class: 'standard',
          reason: 'expired_by_policy',
          retention_days: 45,
          completed_at: '2026-03-01T12:00:00Z',
          expired_at: '2026-04-15T12:00:00Z',
        },
      ],
      summary: { candidate_count: 1, pruned_count: 0, failed_count: 0 },
    })
    apiMock.applyArchiveRetention.mockResolvedValue({
      generated_at: '2026-05-18T12:01:00Z',
      dry_run: false,
      candidates: [
        {
          test_id: 'run-expired',
          project_id: 'project-1',
          release_id: 'rel-1',
          run_status: 'COMPLETE',
          archive_path: '/tmp/run-expired',
          policy_name: 'default',
          artifact_type: 'bundle',
          compliance_class: 'standard',
          reason: 'expired_by_policy',
          retention_days: 45,
          completed_at: '2026-03-01T12:00:00Z',
          expired_at: '2026-04-15T12:00:00Z',
        },
      ],
      results: [{ test_id: 'run-expired', archive_path: '/tmp/run-expired', status: 'pruned', compliance_class: 'standard', reason: 'expired_by_policy' }],
      summary: { candidate_count: 1, pruned_count: 1, failed_count: 0 },
    })
    applicationApiMock.listApplications.mockResolvedValue({ applications: [] })
    applicationApiMock.listReleases.mockResolvedValue({ releases: [] })
    repositoryApiMock.listDomains.mockResolvedValue({ domains: [defaultDomain], count: 1 })
    repositoryApiMock.createDomain.mockResolvedValue({ domain: retailDomain, application_repository: { id: 'repo-retail', domain_id: retailDomain.id, name: 'Application Repository', created_at: '', updated_at: '' }, default_category: { id: 'category-retail', domain_id: retailDomain.id, repository_id: 'repo-retail', name: 'Uncategorized', created_at: '', updated_at: '' } })
    repositoryApiMock.updateDomain.mockResolvedValue(retailDomain)
    repositoryApiMock.deleteDomain.mockResolvedValue({ deleted: retailDomain.id })
    seedProjectCatalog([
        {
          id: 'project-1',
          name: 'Customer Portal',
          settings: {
            max_concurrent_tests: 2,
            max_vusers: 250,
            redaction_rules: [
              {
                name: 'customer id',
                pattern: 'customer-[0-9]+',
              },
            ],
          },
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
      ])
    apiMock.listProjectSecrets.mockImplementation(async (projectId: string) => ({
      project_id: projectId,
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
      count: 0,
    }))
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 1 })
    apiMock.getProjectSecretUsage.mockImplementation(async (projectId: string, name: string) => ({
      project_id: projectId, name, usage_revision: 'usage-1', consumer_count: 0,
      reference_count: 0, consumers: [], limit: 50, offset: 0, has_more: false,
    }))
    apiMock.listApplicationSecrets.mockResolvedValue({
      project_id: 'project-1', application_id: 'app-a',
      capabilities: { provider: 'encrypted_database', writable: true, create_supported: true, replace_supported: true },
      secrets: [], count: 0,
    })
    apiMock.getApplicationSecretStatuses.mockResolvedValue({ project_id: 'project-1', application_id: 'app-a', secrets: [] })
    apiMock.getApplicationSecretUsage.mockResolvedValue({
      project_id: 'project-1', application_id: 'app-a', name: '', usage_revision: 'usage-1', consumer_count: 0,
      reference_count: 0, consumers: [], limit: 50, offset: 0, has_more: false,
    })
    apiMock.rotateProjectSecret.mockResolvedValue({
      project_id: 'project-1',
      secret: { name: 'LOGIN_TOKEN', provider: 'encrypted_database', writable: true },
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    downloadBlobMock.mockReset()
    window.sessionStorage.clear()
    window.localStorage.removeItem('apexload.settings.focusSection')
    window.localStorage.removeItem('apexload.settings.section')
    window.localStorage.removeItem('apexload.settings.generalSubsection')
    window.localStorage.removeItem('apexload.settings.governanceSubsection')
    window.localStorage.removeItem('apexload.settings.capacitySubsection')
    window.localStorage.removeItem('apexload.settings.projectSubsection')
    window.localStorage.removeItem('apexload.settings.projectId')
    window.localStorage.removeItem('apexload.settings.integrationSubsection')
    delete (window as BridgeWindow).go
  })

  it('keeps a feature destination visible while permissions load and after denial', async () => {
    let resolve!: (value: unknown) => void
    apiMock.getAuthEffectiveAccess.mockReturnValue(new Promise(done => { resolve = done }))
    await act(async () => root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} destinationId="features" />))
    expect(container.querySelector<HTMLElement>('#settings-section-feature-access')?.hidden).toBe(false)
    expect(container.querySelector<HTMLElement>('#settings-section-general')?.hidden).toBe(true)
    expect(container.textContent).toContain('Loading feature policy permissions')
    expect(container.querySelector('nav[aria-label="Access administration"] a[aria-current="page"]')?.textContent).toBe('Feature policy')
    await act(async () => resolve({enabled:true,authenticated:true,resources:[]}))
    await flushPromises()
    expect(container.querySelector<HTMLElement>('#settings-section-feature-access')?.hidden).toBe(false)
    expect(container.textContent).toContain('You do not have permission to manage domain feature policy.')
    expect(container.querySelector<HTMLElement>('#settings-task-navigation')?.hidden).toBe(true)
  })

  it('edits and deletes a project from the catalog with confirmation', async () => {
    projectContextMock.deleteProject.mockResolvedValue({ deleted: 'project-1' })
    projectApiMock.updateProject.mockResolvedValue({ id: 'project-1', name: 'Checkout renamed', created_at: '', updated_at: '' })
    await act(async () => root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />))
    await flushPromises()
    await selectTopLevelSettingsTab('Projects')
    const editor = container.querySelector('[aria-label="Edit selected project"]')!
    expect(editor).not.toBeNull()
    const input = editor.querySelector('input')!
    await setFieldValue(input, 'Checkout renamed')
    await act(async () => (Array.from(editor.querySelectorAll('button')).find(button => button.textContent === 'Save') as HTMLButtonElement).click())
    expect(projectApiMock.updateProject).toHaveBeenCalledWith('project-1', expect.objectContaining({ name: 'Checkout renamed' }))
    await act(async () => (Array.from(editor.querySelectorAll('button')).find(button => button.textContent === 'Delete') as HTMLButtonElement).click())
    expect(projectContextMock.deleteProject).not.toHaveBeenCalled()
    const dialog = document.body.querySelector('[role="dialog"]')!
    expect(dialog.textContent).toContain('Checkout renamed')
    await act(async () => (Array.from(dialog.querySelectorAll('button')).find(button => button.textContent === 'Delete Project') as HTMLButtonElement).click())
    expect(projectContextMock.deleteProject).toHaveBeenCalledWith('project-1')
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('locks duplicate project deletion requests and unlocks retry after failure', async () => {
    let rejectDelete!: (error: Error) => void
    projectContextMock.deleteProject.mockImplementationOnce(() => new Promise((_, reject) => { rejectDelete = reject }))
    await act(async () => root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />))
    await flushPromises()
    await selectTopLevelSettingsTab('Projects')
    const editor = container.querySelector('[aria-label="Edit selected project"]')!
    await act(async () => (Array.from(editor.querySelectorAll('button')).find(button => button.textContent === 'Delete') as HTMLButtonElement).click())
    const dialog = document.body.querySelector('[role="dialog"]')!
    const confirm = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent === 'Delete Project') as HTMLButtonElement
    await act(async () => { confirm.click(); confirm.click() })
    expect(projectContextMock.deleteProject).toHaveBeenCalledTimes(1)
    expect(confirm.disabled).toBe(true)
    await act(async () => rejectDelete(new Error('Project has active runs')))
    expect(dialog.textContent).toContain('Project has active runs')
    expect(confirm.disabled).toBe(false)
    expect(document.body.textContent).not.toContain('Deleted project Checkout')
    projectContextMock.deleteProject.mockResolvedValueOnce({ deleted: 'project-1' })
    await act(async () => confirm.click())
    expect(projectContextMock.deleteProject).toHaveBeenCalledTimes(2)
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
  })

  it('defaults General to the Platform panel and switches panels accessibly', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const topbar = container.querySelector('[data-testid="settings-topbar"]')
    expect(topbar?.textContent).not.toContain('Performance Thresholds')
    expect(container.querySelector('nav[aria-label="Settings sections"]')?.textContent).toContain('Alert & stop policies')
    expect(container.querySelector('.settings-section-switcher')).toBeNull()

    const platformButton = findGeneralSubnavButton(generalSection, 'Platform')
    const usageButton = findGeneralSubnavButton(generalSection, 'Usage')
    const platformPanel = generalSection.querySelector('#settings-general-panel-platform')
    const usagePanel = generalSection.querySelector('#settings-general-panel-usage')

    expect(platformButton?.getAttribute('aria-selected')).toBe('true')
    expect(platformPanel?.hasAttribute('hidden')).toBe(false)
    expect(usagePanel?.hasAttribute('hidden')).toBe(true)

    expect(usageButton).toBeTruthy()
    if (!usageButton) return
    await act(async () => {
      usageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(usageButton.getAttribute('aria-selected')).toBe('true')
    expect(platformPanel?.hasAttribute('hidden')).toBe(true)
    expect(usagePanel?.hasAttribute('hidden')).toBe(false)
  })

  it('keeps edited platform settings while switching General panels', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const maxConcurrentInput = findSettingInput(generalSection, 'Max Concurrent Tests')
    expect(maxConcurrentInput).toBeTruthy()
    if (!maxConcurrentInput) return

    await setFieldValue(maxConcurrentInput, '12')

    const usageButton = findGeneralSubnavButton(generalSection, 'Usage')
    const platformButton = findGeneralSubnavButton(generalSection, 'Platform')
    expect(usageButton).toBeTruthy()
    expect(platformButton).toBeTruthy()
    if (!usageButton || !platformButton) return

    await act(async () => {
      usageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      platformButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(findSettingInput(generalSection, 'Max Concurrent Tests')?.value).toBe('12')
    expect(findButton('Save pending settings')?.disabled).toBe(false)
  })

  it('edits administrator-owned schedule governance and calendar defaults from Settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Governance')

    const governancePanel = generalSection.querySelector('#settings-general-panel-governance')
    expect(governancePanel).toBeTruthy()
    const governanceTabs = Array.from(governancePanel?.querySelectorAll(':scope > .settings-subsection-layout > .settings-subnav .settings-subnav-label') || [])
      .map(tab => tab.textContent)
    expect(governanceTabs).toEqual(['Schedule Defaults', 'Script Imports', 'Service Account APIs', 'Recent Audit Events'])
    expect(findSettingsSubnavButton(governancePanel || generalSection, 'Schedule Defaults')?.getAttribute('aria-selected')).toBe('true')
    expect(governancePanel?.querySelector('#settings-governance-panel-schedule-defaults')?.hasAttribute('hidden')).toBe(false)
    expect(governancePanel?.querySelector('#settings-governance-panel-service-account-apis')?.hasAttribute('hidden')).toBe(true)
    expect(governancePanel?.querySelector('#settings-governance-panel-recent-audit-events')?.hasAttribute('hidden')).toBe(true)

    expect(generalSection.textContent).toContain('Scenario Schedule Defaults')
    const approvalSelect = findSettingSelect(generalSection, 'Approval Policy')
    const missedRunSelect = findSettingSelect(generalSection, 'Missed Run Policy')
    expect(approvalSelect?.value).toBe('auto')
    expect(missedRunSelect?.value).toBe('skip')
    if (!approvalSelect || !missedRunSelect) return

    await setFieldValue(approvalSelect, 'production')
    await setFieldValue(missedRunSelect, 'catch_up_once')

    const blackoutRow = Array.from(generalSection.querySelectorAll('.setting-row')).find(row => row.textContent?.includes('Blackout Window'))
    const availabilityRow = Array.from(generalSection.querySelectorAll('.setting-row')).find(row => row.textContent?.includes('Availability Window'))
    const blackoutInputs = Array.from(blackoutRow?.querySelectorAll('input') || []) as HTMLInputElement[]
    const availabilityInputs = Array.from(availabilityRow?.querySelectorAll('input') || []) as HTMLInputElement[]
    expect(blackoutInputs).toHaveLength(2)
    expect(availabilityInputs).toHaveLength(2)

    await setFieldValue(blackoutInputs[0], '00:00')
    await setFieldValue(blackoutInputs[1], '06:00')
    await setFieldValue(availabilityInputs[0], '09:00')
    await setFieldValue(availabilityInputs[1], '17:00')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      schedule_defaults: expect.objectContaining({
        approval_policy: 'production',
        missed_run_policy: 'catch_up_once',
        blackout_start_time: '00:00',
        blackout_end_time: '06:00',
        availability_start_time: '09:00',
        availability_end_time: '17:00',
      }),
    }))
  })

  it('keeps source credential migration default-off and saves an explicit system-admin opt-in', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return
    await selectGeneralSubnav(generalSection, 'Governance')
    await selectSettingsSubnav(generalSection, 'Script Imports')

    const policyInput = findSettingInput(generalSection, 'Allow source secret migration')
    expect(policyInput?.checked).toBe(false)
    expect(policyInput?.disabled).toBe(false)
    await act(async () => policyInput?.click())

    const saveButton = findButton('Save pending settings')
    expect(saveButton?.disabled).toBe(false)
    await act(async () => saveButton?.click())
    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      script_imports: { allow_source_secret_migration: true },
    }))
  })

  it('subdivides capacity, project, and integration settings into focused panels', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return
    expect(findSettingsSubnavButton(capacitySection, 'Availability')?.getAttribute('aria-selected')).toBe('true')
    expect(capacitySection.querySelector('#settings-capacity-panel-availability')?.hasAttribute('hidden')).toBe(false)
    await selectSettingsSubnav(capacitySection, 'Generator Profile')
    expect(capacitySection.querySelector('#settings-capacity-panel-availability')?.hasAttribute('hidden')).toBe(true)
    expect(capacitySection.querySelector('#settings-capacity-panel-generator')?.hasAttribute('hidden')).toBe(false)

    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return
    expect(findSettingsSubnavButton(projectSection, 'Project Catalog')?.getAttribute('aria-selected')).toBe('true')
    await selectSettingsSubnav(projectSection, 'Redaction Rules')
    expect(projectSection.querySelector('#settings-projects-panel-catalog')?.hasAttribute('hidden')).toBe(true)
    expect(projectSection.querySelector('#settings-projects-panel-redaction')?.hasAttribute('hidden')).toBe(false)

    await selectTopLevelSettingsTab('Integrations')
    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return
    expect(findSettingsSubnavButton(integrationsSection, 'Library Federation')?.getAttribute('aria-selected')).toBe('true')
    await selectSettingsSubnav(integrationsSection, 'Notifications')
    expect(integrationsSection.querySelector('#settings-integrations-panel-library')?.hasAttribute('hidden')).toBe(true)
    expect(integrationsSection.querySelector('#settings-integrations-panel-notifications')?.hasAttribute('hidden')).toBe(false)
    await selectSettingsSubnav(integrationsSection, 'Prometheus')
    expect(integrationsSection.querySelector('#settings-integrations-panel-prometheus')?.hasAttribute('hidden')).toBe(false)
  })

  it('opens Project Secrets for the requested project and scopes permissions to that selection', async () => {
    seedProjectCatalog([
        {
          id: 'project-1',
          domain_id: 'domain-retail',
          name: 'Alpha Checkout',
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
        {
          id: 'project-2',
          domain_id: 'domain-payments',
          name: 'Zulu Payments',
          created_at: '2026-05-16T00:00:00Z',
          updated_at: '2026-05-16T00:00:00Z',
        },
      ])
    apiMock.getAuthEffectiveAccess.mockImplementation(async (projectId?: string) => ({
      enabled: true,
      authenticated: true,
      project_id: projectId,
      resources: [{
        id: 'scripts_and_projects',
        name: 'Scripts and projects',
        description: '',
        operations: [
          { id: 'manage_scripts', label: 'Manage scripts', allowed: projectId === 'project-2' },
          { id: 'manage_projects', label: 'Manage projects', allowed: false },
          { id: 'manage_system', label: 'Manage system', allowed: false },
        ],
      }],
    }))
    apiMock.listProjectSecrets.mockImplementation(async (projectId: string) => ({
      project_id: projectId,
      capabilities: {
        provider: 'encrypted_database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [{
        project_id: projectId,
        name: 'PAYMENTS_TOKEN',
        provider: 'encrypted_database',
        writable: true,
        created_at: '2026-05-16T00:00:00Z',
        updated_at: '2026-05-16T00:00:00Z',
      }],
      count: 1,
    }))
    window.localStorage.setItem('apexload.settings.focusSection', 'projects')
    window.localStorage.setItem('apexload.settings.projectSubsection', JSON.stringify('secrets'))
    window.localStorage.setItem('apexload.settings.projectId', 'project-2')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).not.toBeNull()
    expect(projectSection?.querySelector('.settings-app-card.active')?.textContent).toContain('Zulu Payments')
    expect(projectSection?.querySelector('#settings-projects-panel-secrets')?.hasAttribute('hidden')).toBe(false)
    expect(projectSection?.textContent).toContain('Project Secret Vault')
    expect(projectSection?.textContent).toContain('Inventory, provider health, consumers, orphaned entries, and administrative rotation for Zulu Payments')
    expect(projectSection?.textContent).toContain('Editors may create missing secrets')
    const rotateButton = Array.from(projectSection?.querySelectorAll<HTMLButtonElement>('button') || [])
      .find(button => button.textContent?.trim() === 'Rotate')
    expect(rotateButton?.disabled).toBe(true)
    expect(apiMock.getAuthEffectiveAccess).toHaveBeenCalledWith('project-2', expect.objectContaining({
      domain_id: 'domain-payments',
    }))
    expect(apiMock.listProjectSecrets).toHaveBeenCalledWith('project-2')
    expect(window.localStorage.getItem('apexload.settings.projectId')).toBeNull()
  })

  it('fails closed when a Project Secrets navigation target is no longer available', async () => {
    window.localStorage.setItem('apexload.settings.focusSection', 'projects')
    window.localStorage.setItem('apexload.settings.projectSubsection', JSON.stringify('secrets'))
    window.localStorage.setItem('apexload.settings.projectId', 'deleted-project')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).not.toBeNull()
    expect(projectSection?.querySelector('.settings-app-card.active')).toBeNull()
    expect(projectSection?.textContent).toContain('No fallback project was selected')
    expect(container.textContent).toContain('The project requested for secret configuration is no longer available')
    expect(apiMock.listProjectSecrets).not.toHaveBeenCalled()
    expect(window.localStorage.getItem('apexload.settings.projectId')).toBeNull()

    const recoveryButton = Array.from(projectSection?.querySelectorAll<HTMLButtonElement>('button') || [])
      .find(button => button.textContent?.trim() === 'Choose an available project')
    expect(recoveryButton).toBeDefined()
    await act(async () => recoveryButton?.click())
    expect(projectSection?.querySelector('#settings-projects-panel-catalog')?.hasAttribute('hidden')).toBe(false)
  })

  it('edits Prometheus integration settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    await selectTopLevelSettingsTab('Integrations')
    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return

    await selectSettingsSubnav(integrationsSection, 'Prometheus')
    expect(integrationsSection.textContent).toContain('Prometheus Graphs')

    const enabledInput = findSettingInput(integrationsSection, 'Enable Prometheus Graphs')
    const urlInput = findSettingInput(integrationsSection, 'Server URL')
    const timeoutInput = findSettingInput(integrationsSection, 'Timeout (seconds)')
    expect(enabledInput).toBeTruthy()
    expect(urlInput).toBeTruthy()
    expect(timeoutInput).toBeTruthy()
    if (!enabledInput || !urlInput || !timeoutInput) return

    await act(async () => {
      enabledInput.click()
    })
    await setFieldValue(urlInput, 'http://prometheus:9090')
    await setFieldValue(timeoutInput, '12')

    await act(async () => {
      findButton('Save pending settings')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalled()
    const calls = apiMock.updateSystemSettings.mock.calls
    const submitted = calls[calls.length - 1]?.[0]
    expect(submitted.prometheus).toEqual({
      enabled: true,
      base_url: 'http://prometheus:9090',
      bearer_token_env_var: '',
      allow_label_values: false,
      timeout_seconds: 12,
    })
  })

  it('creates a domain from the top-level Domains settings section and selects it', async () => {
    const paymentsDomain: Domain = {
      id: 'domain-payments',
      name: 'Payments',
      description: 'Payment systems',
      created_at: '2026-05-03T00:00:00Z',
      updated_at: '2026-05-03T00:00:00Z',
    }
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: domainAccessResources(true),
    })
    repositoryApiMock.createDomain.mockResolvedValue({
      domain: paymentsDomain,
      application_repository: { id: 'repo-payments', domain_id: paymentsDomain.id, name: 'Application Repository', created_at: '', updated_at: '' },
      default_category: { id: 'category-payments', domain_id: paymentsDomain.id, repository_id: 'repo-payments', name: 'Uncategorized', created_at: '', updated_at: '' },
    })
    repositoryApiMock.listDomains.mockResolvedValue({ domains: [defaultDomain, paymentsDomain], count: 2 })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness initialDomains={[defaultDomain]} />)
    })
    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection).toBeTruthy()
    if (!domainSection) return

    const createRow = domainSection.querySelector('.inline-create-row')
    const inputs = Array.from(createRow?.querySelectorAll('input') || []) as HTMLInputElement[]
    expect(inputs).toHaveLength(2)
    if (inputs.length !== 2) return
    await setFieldValue(inputs[0], 'Payments')
    await setFieldValue(inputs[1], 'Payment systems')

    const addButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Add Domain') as HTMLButtonElement | undefined
    expect(addButton?.disabled).toBe(false)
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(repositoryApiMock.createDomain).toHaveBeenCalledWith({ name: 'Payments', description: 'Payment systems' })
    expect(repositoryApiMock.listDomains).toHaveBeenCalled()
    expect(container.textContent).toContain('Domain "Payments" created.')
    expect(domainSection.querySelector('.settings-app-card.active')?.textContent).toContain('Payments')
  })

  it('does not repeat a committed domain create when catalog reconciliation fails', async () => {
    const paymentsDomain: Domain = {
      id: 'domain-payments',
      name: 'Payments',
      description: 'Payment systems',
      created_at: '2026-05-03T00:00:00Z',
      updated_at: '2026-05-03T00:00:00Z',
    }
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: domainAccessResources(true),
    })
    repositoryApiMock.createDomain.mockResolvedValue({
      domain: paymentsDomain,
      application_repository: { id: 'repo-payments', domain_id: paymentsDomain.id, name: 'Application Repository', created_at: '', updated_at: '' },
      default_category: { id: 'category-payments', domain_id: paymentsDomain.id, repository_id: 'repo-payments', name: 'Uncategorized', created_at: '', updated_at: '' },
    })
    repositoryApiMock.listDomains
      .mockRejectedValueOnce(new Error('catalog unavailable'))
      .mockResolvedValueOnce({ domains: [defaultDomain, paymentsDomain], count: 2 })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness initialDomains={[defaultDomain]} />)
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')

    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection).toBeTruthy()
    if (!domainSection) return
    const inputs = Array.from(domainSection.querySelector('.inline-create-row')?.querySelectorAll('input') || []) as HTMLInputElement[]
    await setFieldValue(inputs[0], 'Payments')
    await setFieldValue(inputs[1], 'Payment systems')
    const addButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Add Domain') as HTMLButtonElement

    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(repositoryApiMock.createDomain).toHaveBeenCalledTimes(1)
    expect(domainSection.textContent).toContain('The create succeeded—do not create it again.')
    const retryButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Retry') as HTMLButtonElement
    await act(async () => {
      retryButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(repositoryApiMock.createDomain).toHaveBeenCalledTimes(1)
    expect(repositoryApiMock.listDomains).toHaveBeenCalledTimes(2)
    expect(domainSection.textContent).not.toContain('The create succeeded—do not create it again.')
  })

  it('renames and describes the selected domain from Settings', async () => {
    const updatedDomain: Domain = {
      ...retailDomain,
      name: 'Retail Banking',
      description: 'Digital banking journeys',
      updated_at: '2026-05-04T00:00:00Z',
    }
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: domainAccessResources(true),
    })
    let finishSave!: (domain: Domain) => void
    repositoryApiMock.updateDomain.mockImplementation(() => new Promise<Domain>(resolve => { finishSave = resolve }))
    repositoryApiMock.listDomains.mockResolvedValue({ domains: [defaultDomain, updatedDomain], count: 2 })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness />)
    })
    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection).toBeTruthy()
    if (!domainSection) return

    const retailButton = Array.from(domainSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes('Retail')) as HTMLButtonElement | undefined
    expect(retailButton).toBeTruthy()
    if (!retailButton) return
    await act(async () => {
      retailButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const selectedRow = Array.from(domainSection.querySelectorAll('.setting-row')).find(row => row.textContent?.includes('Selected Domain'))
    const nameInput = selectedRow?.querySelector('input') as HTMLInputElement | null
    const descriptionInput = selectedRow?.querySelector('textarea') as HTMLTextAreaElement | null
    expect(nameInput).toBeTruthy()
    expect(descriptionInput).toBeTruthy()
    if (!nameInput || !descriptionInput) return
    await setFieldValue(nameInput, 'Retail Banking')
    await setFieldValue(descriptionInput, 'Digital banking journeys')

    const saveButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save') as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(false)
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(nameInput.matches(':disabled')).toBe(true)
    expect(descriptionInput.matches(':disabled')).toBe(true)
    expect(findButton('Cancel domain changes')?.disabled).toBe(true)
    expect(findButton('Save domain changes')?.disabled).toBe(true)
    await act(async () => finishSave(updatedDomain))
    await flushPromises()

    expect(repositoryApiMock.updateDomain).toHaveBeenCalledWith(retailDomain.id, {
      name: 'Retail Banking',
      description: 'Digital banking journeys',
    })
    expect(container.textContent).toContain('Domain "Retail Banking" saved.')
    expect(domainSection.querySelector('.settings-app-card.active')?.textContent).toContain('Retail Banking')
    expect(JSON.parse(window.sessionStorage.getItem('apexload.settings.domainMessage') || '{}').value).toBe('Domain "Retail Banking" saved.')
  })

  it('guards domain deletion for default and non-empty domains', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: domainAccessResources(true),
    })
    seedProjectCatalog([
        {
          id: 'project-retail',
          domain_id: retailDomain.id,
          name: 'Retail Checkout',
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
      ])

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness />)
    })
    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection).toBeTruthy()
    if (!domainSection) return

    let deleteButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Delete') as HTMLButtonElement | undefined
    expect(deleteButton?.disabled).toBe(true)
    expect(domainSection.textContent).toContain('Default domain cannot be deleted.')

    const retailButton = Array.from(domainSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes('Retail')) as HTMLButtonElement | undefined
    expect(retailButton).toBeTruthy()
    if (!retailButton) return
    await act(async () => {
      retailButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    deleteButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Delete') as HTMLButtonElement | undefined
    expect(deleteButton?.disabled).toBe(true)
    expect(domainSection.textContent).toContain('1 project must be moved or deleted first.')
    expect(repositoryApiMock.deleteDomain).not.toHaveBeenCalled()
  })

  it('deletes an empty non-default domain after confirmation and selects a fallback', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: domainAccessResources(true),
    })
    repositoryApiMock.listDomains.mockResolvedValue({ domains: [defaultDomain], count: 1 })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness />)
    })
    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection).toBeTruthy()
    if (!domainSection) return

    const retailButton = Array.from(domainSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes('Retail')) as HTMLButtonElement | undefined
    expect(retailButton).toBeTruthy()
    if (!retailButton) return
    await act(async () => {
      retailButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const deleteButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Delete') as HTMLButtonElement | undefined
    expect(deleteButton?.disabled).toBe(false)
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const confirmButton = Array.from(container.querySelectorAll('[role="dialog"] button')).find(button => button.textContent?.trim() === 'Delete Domain') as HTMLButtonElement | undefined
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return
    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(repositoryApiMock.deleteDomain).toHaveBeenCalledWith(retailDomain.id)
    expect(container.textContent).toContain('Domain "Retail" deleted.')
    expect(domainSection.querySelector('.settings-app-card.active')?.textContent).toContain('Default Domain')
  })

  it('keeps failed domain deletion reviewable and prevents duplicate pending requests', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({ enabled: true, authenticated: true, resources: domainAccessResources(true) })
    let rejectDelete!: (error: Error) => void
    repositoryApiMock.deleteDomain.mockImplementationOnce(() => new Promise((_, reject) => { rejectDelete = reject }))
    await act(async () => { renderWithSettingsTopbar(root, <SettingsPageDomainHarness />) })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')
    const section = container.querySelector('#settings-section-domains')!
    const retail = Array.from(section.querySelectorAll<HTMLButtonElement>('.settings-app-card')).find(button => button.textContent?.includes('Retail'))!
    await act(async () => retail.click())
    await act(async () => Array.from(section.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.trim() === 'Delete')!.click())
    const confirm = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find(button => button.textContent === 'Delete Domain')!
    await act(async () => { confirm.click(); confirm.click() })
    expect(repositoryApiMock.deleteDomain).toHaveBeenCalledTimes(1)
    expect(confirm.disabled).toBe(true)
    await act(async () => rejectDelete(new Error('Domain service unavailable')))
    expect(container.querySelector('[role="dialog"] [role="alert"]')?.textContent).toContain('Domain service unavailable')
    expect(confirm.disabled).toBe(false)
    expect(confirm.textContent).toBe('Delete Domain')
  })

  it('disables domain write controls when manage_domains is denied', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: domainAccessResources(false),
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness />)
    })
    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection).toBeTruthy()
    if (!domainSection) return

    const createInput = domainSection.querySelector('.inline-create-row input') as HTMLInputElement | null
    expect(createInput).toBeTruthy()
    if (!createInput) return
    await setFieldValue(createInput, 'Restricted')

    const addButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Add Domain') as HTMLButtonElement | undefined
    const saveButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Save') as HTMLButtonElement | undefined
    const deleteButton = Array.from(domainSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Delete') as HTMLButtonElement | undefined

    expect(addButton?.disabled).toBe(true)
    expect(saveButton?.disabled).toBe(true)
    expect(deleteButton?.disabled).toBe(true)
    expect(domainSection.textContent).toContain('Current session can view domains but cannot change them.')
    expect(repositoryApiMock.createDomain).not.toHaveBeenCalled()
  })

  it('offers Solarized Dark in the dashboard theme selector', async () => {
    const onThemeChange = vi.fn()

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage destinationId="preferences" theme="dark" onThemeChange={onThemeChange} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('[aria-label="Personal preferences"]')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const themeSelect = generalSection.querySelector('select')
    expect(Array.from(themeSelect?.options || []).map(option => option.value)).toEqual(['system', 'dark', 'light', 'solarized-dark', 'solarized-light', 'monokai-dimmed'])

    if (!themeSelect) return
    await setFieldValue(themeSelect, 'solarized-dark')

    expect(onThemeChange).toHaveBeenCalledWith('solarized-dark')
  })

  it('opens the requested integrations section from navigation handoff', async () => {
    window.localStorage.setItem('apexload.settings.focusSection', 'integrations')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const integrationsSection = container.querySelector('#settings-section-integrations')
    const generalSection = container.querySelector('#settings-section-general')
    expect(integrationsSection).toBeTruthy()
    expect(generalSection).toBeTruthy()
    expect(integrationsSection?.hasAttribute('hidden')).toBe(false)
    expect(generalSection?.hasAttribute('hidden')).toBe(true)
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBeNull()
  })

  it('keeps stock capacity settings until the operator explicitly applies live pod capacity', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    await selectTopLevelSettingsTab('Capacity Planning')
    await selectSettingsSubnav(capacitySection, 'Generator Profile')

    const maxVUsersInput = findSettingInput(capacitySection, 'Generator Max VUsers')
    const cpuCoresInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    const memoryInput = findSettingInput(capacitySection, 'Generator Memory (MB)')

    await selectSettingsSubnav(capacitySection, 'Availability')
    const availableGeneratorsInput = findSettingInput(capacitySection, 'Configured Available Generators')

    expect(cpuCoresInput?.value).toBe('4')
    expect(memoryInput?.value).toBe('8192')
    expect(maxVUsersInput?.value).toBe('10000')
    expect(availableGeneratorsInput?.value).toBe('1')
    expect(container.textContent).toContain('4 CPU / 8192 MB')
    expect(container.querySelector('.settings-overview')).toBeNull()
    expect(container.textContent).not.toContain('Save State')
    expect(container.textContent).not.toContain('Up to date')
    expect(apiMock.getSystemInventory).not.toHaveBeenCalled()
  })

  it('plans archive retention pruning from the capacity settings section', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    await selectTopLevelSettingsTab('Capacity Planning')
    await selectSettingsSubnav(capacitySection, 'Retention')

    expect(capacitySection.textContent).toContain('Archive Retention Executor')
    expect(capacitySection.textContent).toContain('Default Retention Days')
    expect(findSettingInput(capacitySection, 'Default Retention Days')?.value).toBe('45')
    expect(capacitySection.textContent).toContain('HAR Retention Days')
    expect(findSettingInput(capacitySection, 'HAR Retention Days')?.value).toBe('7')
    expect(capacitySection.textContent).toContain('HAR Artifact Directory')

    const planButton = Array.from(capacitySection.querySelectorAll('button')).find(button => button.textContent?.includes('Plan Retention'))
    expect(planButton).toBeDefined()
    if (!planButton) return

    await act(async () => {
      planButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.planArchiveRetention).toHaveBeenCalledTimes(1)
    expect(capacitySection.textContent).toContain('Retention plan found 1 prune candidate.')
    expect(capacitySection.textContent).toContain('run-expired')
    expect(capacitySection.textContent).toContain('default / bundle / standard / expired_by_policy')
  })

  it('surfaces usage accounting snapshots and exports chargeback CSV', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Usage')

    expect(generalSection.textContent).toContain('Usage Accounting')
    expect(findSettingInput(generalSection, 'Usage Snapshot Cadence (seconds)')?.value).toBe('3600')
    expect(generalSection.textContent).toContain('81,000')
    expect(generalSection.textContent).toContain('1h 30m')
    expect(generalSection.textContent).toContain('2.00 MB')
    expect(generalSection.textContent).toContain('COMPLETE=2')
    expect(generalSection.textContent).toContain('usage-snapshot-1')
    expect(generalSection.textContent).toContain('project-1 / 2 runs / 81,000 VUser seconds')

    const captureButton = Array.from(generalSection.querySelectorAll('button')).find(button => button.textContent?.includes('Capture Snapshot'))
    expect(captureButton).toBeDefined()
    if (!captureButton) return

    await act(async () => {
      captureButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.captureUsageAccountingSnapshot).toHaveBeenCalledWith('project-1')
    expect(generalSection.textContent).toContain('Usage snapshot captured for 3 runs.')

    const exportButton = Array.from(generalSection.querySelectorAll('button')).find(button => button.textContent?.includes('Export Usage CSV'))
    expect(exportButton).toBeDefined()
    if (!exportButton) return

    downloadBlobMock.mockResolvedValueOnce('cancelled')
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.downloadUsageAccounting).toHaveBeenCalledWith('project-1')
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^apexload-usage-accounting-\d{4}-\d{2}-\d{2}\.csv$/))
    expect(generalSection.textContent).not.toContain('Usage accounting export downloaded.')

    downloadBlobMock.mockResolvedValueOnce('saved')
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(generalSection.textContent).toContain('Usage accounting export downloaded.')
  })

  it('publishes a committed usage snapshot and retries only reconciliation', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    if (!generalSection) return
    await selectGeneralSubnav(generalSection, 'Usage')
    apiMock.listUsageAccountingSnapshots.mockRejectedValueOnce(new Error('snapshot list unavailable'))

    const capture = Array.from(generalSection.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Capture Snapshot')) as HTMLButtonElement | undefined
    await act(async () => capture?.click())
    await flushPromises()

    expect(apiMock.captureUsageAccountingSnapshot).toHaveBeenCalledTimes(1)
    expect(generalSection.textContent).toContain('usage-snapshot-2')
    expect(generalSection.textContent).toContain('The capture succeeded—do not capture again.')

    const refresh = Array.from(generalSection.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Refresh Usage')) as HTMLButtonElement | undefined
    await act(async () => refresh?.click())
    await flushPromises()

    expect(apiMock.captureUsageAccountingSnapshot).toHaveBeenCalledTimes(1)
    expect(apiMock.listUsageAccountingSnapshots).toHaveBeenCalledTimes(3)
  })

  it('surfaces production readiness checks and refreshes the backend payload', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Readiness')

    expect(generalSection.textContent).toContain('Production Readiness')
    expect(generalSection.textContent).toContain('Degraded')
    expect(generalSection.textContent).toContain('Metadata Store')
    expect(generalSection.textContent).toContain('metadata store is reachable')
    expect(generalSection.textContent).toContain('Generator RPC')
    expect(generalSection.textContent).toContain('some generator RPC targets are not reachable')
    expect(generalSection.textContent).toContain('available: 1')
    expect(generalSection.textContent).toContain('Blocked')
    expect(apiMock.getSystemReadiness).toHaveBeenCalledTimes(1)

    apiMock.getSystemReadiness.mockResolvedValueOnce({
      status: 'ready',
      generated_at: '2026-05-18T12:10:00Z',
      checks: [
        {
          name: 'metadata_store',
          status: 'ready',
          message: 'metadata store is reachable',
        },
      ],
    })

    const refreshButton = Array.from(generalSection.querySelectorAll('button')).find(button => button.textContent?.includes('Refresh Readiness'))
    expect(refreshButton).toBeDefined()
    if (!refreshButton) return

    await act(async () => {
      refreshButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getSystemReadiness).toHaveBeenCalledTimes(2)
    expect(generalSection.textContent).toContain('Production readiness refreshed.')
  })

  it('edits telemetry SLA budgets in performance thresholds', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const thresholdSection = container.querySelector('#settings-section-thresholds')
    expect(thresholdSection).toBeTruthy()
    if (!thresholdSection) return

    const sampleAgeInput = findSettingInput(thresholdSection, 'Max Telemetry Sample Age (ms)')
    const generatorInput = findSettingInput(thresholdSection, 'Max Generator Lag (ms)')
    const transportInput = findSettingInput(thresholdSection, 'Max Transport Lag (ms)')
    const fanoutInput = findSettingInput(thresholdSection, 'Max Controller Fan-out (ms)')
    const browserInput = findSettingInput(thresholdSection, 'Max Browser Render Lag (ms)')
    expect(sampleAgeInput?.value).toBe('2000')
    expect(generatorInput?.value).toBe('500')
    expect(transportInput?.value).toBe('500')
    expect(fanoutInput?.value).toBe('500')
    expect(browserInput?.value).toBe('500')
    if (!sampleAgeInput || !fanoutInput || !browserInput) return

    await setFieldValue(sampleAgeInput, '1750')
    await setFieldValue(fanoutInput, '275')
    await setFieldValue(browserInput, '325')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      thresholds: expect.objectContaining({
        max_telemetry_sample_age_ms: 1750,
        max_telemetry_generator_lag_ms: 500,
        max_telemetry_transport_lag_ms: 500,
        max_telemetry_controller_fanout_ms: 275,
        max_telemetry_browser_render_lag_ms: 325,
      }),
    }))
  })

  it('adds a reserved capacity pool and saves it in the capacity settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    // Reserving 4 generators requires the configured inventory to cover them;
    // the form now mirrors the backend overbooking rule before save.
    await selectSettingsSubnav(capacitySection, 'Availability')
    const configuredGeneratorsInput = findSettingInput(capacitySection, 'Configured Available Generators')
    expect(configuredGeneratorsInput).toBeTruthy()
    if (!configuredGeneratorsInput) return
    await setFieldValue(configuredGeneratorsInput, '6')

    await selectSettingsSubnav(capacitySection, 'Standing allocations')

    const reservationSection = container.querySelector('[data-testid="capacity-reservations"]')
    expect(reservationSection).toBeTruthy()
    if (!reservationSection) return

    const addButton = Array.from(reservationSection.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Add standing allocation') as HTMLButtonElement | undefined
    expect(addButton).toBeTruthy()
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const projectInput = reservationSection.querySelector('select[aria-label="Reservation project"]') as HTMLSelectElement | null
    const generatorsInput = reservationSection.querySelector('input[aria-label="Reserved generators"]') as HTMLInputElement | null
    expect(projectInput).toBeTruthy()
    expect(generatorsInput).toBeTruthy()
    if (!projectInput || !generatorsInput) return

    await setFieldValue(projectInput, 'project-1')
    await setFieldValue(generatorsInput, '4')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      capacity_planning: expect.objectContaining({
        reservations: [
          expect.objectContaining({ project_id: 'project-1', reserved_generators: 4 }),
        ],
      }),
    }))
  })

  it('applies live pod capacity to the generator cpu, memory, max vusers, and available generators fields when requested manually', async () => {
    apiMock.getSystemSettings.mockResolvedValue({
      general: {
        api_endpoint: ':8080',
        max_concurrent_tests: 10,
        metrics_interval_seconds: 1,
      },
      thresholds: {
        max_error_rate_pct: undefined,
        max_p99_latency_ms: undefined,
        max_avg_server_processing_ms: undefined,
        max_avg_transfer_ms: undefined,
      },
      capacity_planning: {
        headroom_pct: 20,
        generator: {
          cpu_cores: 6,
          memory_mb: 12288,
          network_mbps: 1000,
          max_vusers: 12000,
        },
        availability: {
          source: 'configured',
          configured_available_generators: 3,
        },
      },
      notifications: {
        webhooks: [],
      },
      auth: {
        mode: 'dev',
        provider_name: '',
        login_url: '',
        logout_url: '',
        user_header: 'X-Auth-Request-User',
        name_header: 'X-Auth-Request-Name',
        email_header: 'X-Auth-Request-Email',
        roles_header: 'X-APEXLoad-Roles',
        project_roles_header: 'X-APEXLoad-Project-Roles',
      },
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Generator Profile')

    const livePresetButton = Array.from(capacitySection.querySelectorAll('button')).find(button => button.textContent?.includes('Live Pod Capacity'))
    expect(livePresetButton).toBeDefined()
    if (!livePresetButton) return

    await act(async () => {
      livePresetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const maxVUsersInput = findSettingInput(capacitySection, 'Generator Max VUsers')
    const cpuCoresInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    const memoryInput = findSettingInput(capacitySection, 'Generator Memory (MB)')

    await selectSettingsSubnav(capacitySection, 'Availability')
    const availableGeneratorsInput = findSettingInput(capacitySection, 'Configured Available Generators')

    expect(cpuCoresInput?.value).toBe('2')
    expect(memoryInput?.value).toBe('4096')
    expect(maxVUsersInput?.value).toBe('8000')
    expect(availableGeneratorsInput?.value).toBe('2')
    expect(container.textContent).toContain('CPU set to 2')
    expect(container.textContent).toContain('memory set to 4,096 MB')
    expect(apiMock.getSystemInventory).toHaveBeenCalledTimes(1)
  })

  it('loads project compliance defaults and saves edited redaction rules and quotas', async () => {
    projectApiMock.updateProject.mockImplementation(async (id: string, data: { name: string; settings?: { redaction_rules?: Array<{ name?: string; pattern: string; replacement?: string }>; max_concurrent_tests?: number; max_vusers?: number } }) => ({
      id,
      name: data.name,
      settings: data.settings,
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T12:00:00Z',
    }))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Projects & Compliance')

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    expect(projectSection?.textContent).toContain('Customer Portal')
    if (!projectSection) return

    await selectSettingsSubnav(projectSection, 'Limits')
    const quotaInputs = Array.from(projectSection.querySelectorAll('input')).filter(input => input.placeholder === 'Unlimited') as HTMLInputElement[]
    expect(quotaInputs).toHaveLength(4)
    if (quotaInputs.length !== 4) return

    await setFieldValue(quotaInputs[0], '3')
    await setFieldValue(quotaInputs[1], '400')
    await setFieldValue(quotaInputs[2], '150')
    await setFieldValue(quotaInputs[3], '30')

    await selectSettingsSubnav(projectSection, 'Redaction Rules')
    expect(projectSection.textContent).toContain('Custom Redaction Rules')
    const addRuleButton = findButton('Add Rule')
    expect(addRuleButton).toBeDefined()
    if (!addRuleButton) return

    await act(async () => {
      addRuleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const ruleCards = Array.from(projectSection.querySelectorAll('.project-redaction-rule-card'))
    expect(ruleCards).toHaveLength(2)
    const secondRuleInputs = ruleCards[1].querySelectorAll('input')
    const secondRulePattern = ruleCards[1].querySelector('textarea') as HTMLTextAreaElement | null
    expect(secondRuleInputs).toHaveLength(2)
    expect(secondRulePattern).toBeTruthy()
    if (secondRuleInputs.length < 2 || !secondRulePattern) return

    await setFieldValue(secondRuleInputs[0] as HTMLInputElement, 'account token')
    await setFieldValue(secondRuleInputs[1] as HTMLInputElement, '[ACCOUNT]')
    await setFieldValue(secondRulePattern, 'account-[A-Z0-9]+')

    const saveButton = findButton('Save redaction rules')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(projectApiMock.updateProject).toHaveBeenCalledWith('project-1', {
      name: 'Customer Portal',
      settings: {
        redaction_rules: [
          {
            name: 'customer id',
            pattern: 'customer-[0-9]+',
          },
          {
            name: 'account token',
            pattern: 'account-[A-Z0-9]+',
            replacement: '[ACCOUNT]',
          },
        ],
        max_concurrent_tests: 3,
        max_vusers: 400,
        max_vusers_per_test: 150,
        max_test_duration_minutes: 30,
        github: {
          enabled: false,
          allowed_repos: [],
          allowed_branches: [],
          path_template: '',
          require_pull_request: true,
          allow_direct_commit: false,
          allow_import: true,
          allow_push: true,
          bulk_sync_enabled: false,
          auto_sync_enabled: false,
          checks_enabled: false,
          app_installation_id: undefined,
          default_repo: '',
          default_branch: '',
          default_sync_directory: '',
          automation_identity: '',
        },
      },
    })
    expect(container.textContent).toContain('Saved project governance defaults for Customer Portal.')
    expect(projectContextMock.updateProject).toHaveBeenCalledWith(
      'project-1',
      expect.objectContaining({ name: 'Customer Portal' }),
    )
  })

  it('preserves an existing GitHub automation_identity when saving project governance defaults', async () => {
    seedProjectCatalog([
        {
          id: 'project-1',
          name: 'Customer Portal',
          settings: {
            max_concurrent_tests: 2,
            max_vusers: 250,
            redaction_rules: [
              {
                name: 'customer id',
                pattern: 'customer-[0-9]+',
              },
            ],
            github: {
              enabled: true,
              automation_identity: 'apexload-bot',
              default_repo: 'org/perf-scripts',
            },
          },
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
      ])
    projectApiMock.updateProject.mockImplementation(async (id: string, data: { name: string; settings?: ProjectSettings }) => ({
      id,
      name: data.name,
      settings: data.settings,
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T12:00:00Z',
    }))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Projects & Compliance')

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    // The automation identity loaded from the project policy is shown in the form
    // and round-trips back out on save (regression: it used to be dropped).
    await selectSettingsSubnav(projectSection, 'GitHub Policy')
    const identityInput = Array.from(projectSection.querySelectorAll('input')).find(
      input => input.value === 'apexload-bot',
    ) as HTMLInputElement | undefined
    expect(identityInput).toBeDefined()

    await selectSettingsSubnav(projectSection, 'Limits')
    const quotaInputs = Array.from(projectSection.querySelectorAll('input')).filter(input => input.placeholder === 'Unlimited') as HTMLInputElement[]
    expect(quotaInputs).toHaveLength(4)
    if (quotaInputs.length !== 4) return
    await setFieldValue(quotaInputs[1], '300')

    const saveButton = findButton('Save Project Governance')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(projectApiMock.updateProject).toHaveBeenCalledTimes(1)
    expect(projectApiMock.updateProject.mock.calls[0][1].settings?.max_vusers).toBe(300)
    const savedPolicy = projectApiMock.updateProject.mock.calls[0][1].settings?.github
    expect(savedPolicy?.automation_identity).toBe('apexload-bot')
  })

  // Invariant, not a single field: a governance save that never touches the GitHub
  // form must round-trip the project's stored GitHub policy in full. Both known
  // regressions here (dropped automation_identity, destroyed Actions trust policy)
  // were instances of that one invariant, and pinning them field-by-field cannot
  // catch the next field that gets left out of buildProjectGitHubDraft.
  //
  // Required<> is load-bearing at BOTH levels: adding a field to either policy
  // type without adding it to these fixtures is a compile error, and adding it
  // to the fixture without wiring it through the form fails the round-trip
  // assert. Required<T> is shallow, so the nested Actions policy needs its own
  // Required<GitHubActionsPolicy> annotation - without it the trust-boundary
  // claim below would only hold at the granularity of "the object still exists".
  it('round-trips every stored GitHub policy field through a governance save that does not touch the GitHub form', async () => {
    // GitHub Actions authorization is a separate trust boundary with no control
    // in this form at all; it must survive a source-policy save untouched. Every
    // field is populated because the narrowing ones (allowed_target_hosts,
    // allowed_protocols, allowed_environments, required_runner_environment)
    // widen what an Actions OIDC token may launch if they are silently dropped.
    const storedActionsPolicy: Required<GitHubActionsPolicy> = {
      enabled: true,
      issuer: 'https://token.actions.githubusercontent.com',
      server_url: 'https://github.com',
      audience: 'apexload',
      allowed_repository_ids: ['123456'],
      allowed_repositories: ['org/perf-scripts'],
      allowed_refs: ['refs/heads/main'],
      allowed_workflow_refs: ['org/perf-scripts/.github/workflows/load.yml@refs/heads/main'],
      allowed_environments: ['staging'],
      required_runner_environment: 'github-hosted',
      max_vusers: 500,
      max_duration_minutes: 30,
      allowed_protocols: ['http', 'grpc'],
      allowed_target_hosts: ['staging.internal.example.com'],
      checks_enabled: true,
      revision: 7,
      updated_at: '2026-05-14T00:00:00Z',
      updated_by: 'security-admin',
    }
    const storedGitHubPolicy: Required<GitHubProjectPolicy> = {
      enabled: true,
      allowed_repos: ['org/perf-scripts', 'org/other-scripts'],
      allowed_branches: ['main', 'release/*'],
      path_template: 'apexload/scripts/{project}/{ref_key}.json',
      // Deliberately the opposite of every form default, so a field that is
      // rebuilt from defaults instead of preserved shows up as a diff.
      require_pull_request: false,
      allow_direct_commit: true,
      allow_import: false,
      allow_push: false,
      bulk_sync_enabled: true,
      auto_sync_enabled: true,
      checks_enabled: true,
      app_installation_id: 12345678,
      automation_identity: 'apexload-bot',
      default_repo: 'org/perf-scripts',
      default_branch: 'main',
      default_sync_directory: 'apexload/scripts',
      actions: storedActionsPolicy,
    }
    // Expected must not alias the fixture the component reads: seedProjectCatalog
    // hands this very object to the component as selectedProject.settings.github,
    // so asserting against it directly would let an in-place mutation on the save
    // path change expected and actual together and stay invisible.
    const expectedGitHubPolicy = structuredClone(storedGitHubPolicy)

    seedProjectCatalog([
      {
        id: 'project-1',
        name: 'Customer Portal',
        settings: {
          max_concurrent_tests: 2,
          max_vusers: 250,
          redaction_rules: [
            {
              name: 'customer id',
              pattern: 'customer-[0-9]+',
            },
          ],
          github: storedGitHubPolicy,
        },
        created_at: '2026-05-15T00:00:00Z',
        updated_at: '2026-05-15T00:00:00Z',
      },
    ])
    projectApiMock.updateProject.mockImplementation(async (id: string, data: { name: string; settings?: ProjectSettings }) => ({
      id,
      name: data.name,
      settings: data.settings,
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T12:00:00Z',
    }))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Projects & Compliance')

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    // Edit an unrelated governance field only. The GitHub Policy subsection is
    // never opened, so nothing here is an intentional GitHub policy change.
    await selectSettingsSubnav(projectSection, 'Limits')
    const quotaInputs = Array.from(projectSection.querySelectorAll('input')).filter(input => input.placeholder === 'Unlimited') as HTMLInputElement[]
    expect(quotaInputs).toHaveLength(4)
    if (quotaInputs.length !== 4) return
    await setFieldValue(quotaInputs[1], '300')

    const saveButton = findButton('Save Project Governance')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(projectApiMock.updateProject).toHaveBeenCalledTimes(1)
    const savedSettings = projectApiMock.updateProject.mock.calls[0][1].settings
    // The intended edit landed, so the round-trip below is a real save and not a no-op.
    expect(savedSettings?.max_vusers).toBe(300)
    expect(savedSettings?.github).toEqual(expectedGitHubPolicy)
    // No key silently disappears from the persisted policy either, at the top
    // level or inside the Actions trust boundary.
    expect(Object.keys(savedSettings?.github ?? {}).sort()).toEqual(Object.keys(expectedGitHubPolicy).sort())
    expect(Object.keys(savedSettings?.github?.actions ?? {}).sort()).toEqual(Object.keys(expectedGitHubPolicy.actions).sort())
  })

  it('disables project governance actions when effective RBAC denies project management', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [
        {
          id: 'projects_and_folders',
          name: 'Projects and Folders',
          description: 'Project catalog.',
          operations: [
            { id: 'view_projects', label: 'View projects', allowed: true, grant_source: 'project' },
            { id: 'manage_projects', label: 'Manage projects', allowed: false },
          ],
        },
      ],
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    const newProjectInput = Array.from(projectSection.querySelectorAll('input')).find(input => input.placeholder?.includes('Create project')) as HTMLInputElement | undefined
    expect(newProjectInput).toBeDefined()
    if (!newProjectInput) return
    await setFieldValue(newProjectInput, 'Restricted Project')

    expect(findButton('Add Project')?.disabled).toBe(true)
    await selectSettingsSubnav(projectSection, 'Redaction Rules')
    expect(findButton('Add Rule')?.disabled).toBe(true)
    expect(projectSection.textContent).toContain('Current session can view this project but cannot change project governance.')

    await selectSettingsSubnav(projectSection, 'Limits')
    const quotaInputs = Array.from(projectSection.querySelectorAll('input')).filter(input => input.placeholder === 'Unlimited') as HTMLInputElement[]
    expect(quotaInputs).toHaveLength(4)
    if (quotaInputs.length !== 4) return

    await setFieldValue(quotaInputs[0], '3')
    const saveButton = findButton('Save Project Governance')
    expect(saveButton?.disabled).toBe(true)
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
  })

  it('creates projects through the scoped project context used by the sidebar', async () => {
    const created = {
      id: 'project-new',
      name: 'New Project',
      domain_id: 'default',
      created_at: '2026-07-20T00:00:00Z',
      updated_at: '2026-07-20T00:00:00Z',
    }
    projectContextMock.createAndSelectProject.mockResolvedValue(created)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    const input = Array.from(projectSection?.querySelectorAll('input') || [])
      .find(candidate => candidate.placeholder?.includes('Create project')) as HTMLInputElement | undefined
    expect(input).toBeDefined()
    if (!input) return

    await setFieldValue(input, 'New Project')
    await act(async () => {
      findButton('Add Project')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(projectContextMock.createAndSelectProject).toHaveBeenCalledWith({ name: 'New Project', domain_id: undefined })
    expect(projectApiMock.createProject).not.toHaveBeenCalled()
    expect(projectSection?.textContent).toContain('2 projects')
    expect(projectSection?.textContent).toContain('New Project')
  })

  it('does not report unsaved changes for a project that has per-pool quotas (no edits)', async () => {
    seedProjectCatalog([
        {
          id: 'project-pool',
          name: 'Pooled Project',
          settings: {
            max_concurrent_tests: 2,
            max_vusers: 250,
            redaction_rules: [
              {
                name: 'customer id',
                pattern: 'customer-[0-9]+',
              },
            ],
            // A drift-revealing GitHub policy: whitespace in trimmed fields and OMITTED
            // `?? true` fields (require_pull_request/allow_import/allow_push). For the
            // baseline to read clean post-fix, projectGitHubDraftFromPolicy must trim
            // those fields and apply the same defaults as loadProjectGitHubDraft +
            // buildProjectGitHubDraft — so this pins the normalizer, not just pool_quotas.
            github: {
              enabled: true,
              allowed_repos: ['org/repo-a', 'org/repo-b'],
              allowed_branches: ['main'],
              path_template: '  scripts/{name}.json  ',
              allow_direct_commit: true,
              bulk_sync_enabled: true,
              checks_enabled: true,
              app_installation_id: 12345,
              default_repo: '  org/repo-a  ',
              default_branch: 'main',
              default_sync_directory: '  scripts  ',
              // This server-owned policy is preserved by the form but not
              // edited here. It must participate symmetrically in the saved
              // baseline and live draft or Settings starts falsely dirty.
              actions: {
                updated_at: '0001-01-01T00:00:00Z',
              },
            },
            pool_quotas: { 'pool-east': { max_vusers: 500, max_concurrent_tests: 3 } },
          },
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
      ])

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    await selectSettingsSubnav(projectSection, 'Limits')

    // The dirty-check baseline must include pool_quotas symmetrically with the draft;
    // otherwise the governance Save button is permanently enabled (always "dirty")
    // for any project that has per-pool quotas, even with no edits.
    const saveButton = findButton('Save Project Governance')
    expect(saveButton?.disabled).toBe(true)
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
  })

  it('keeps a committed non-active project over a stale settings catalog read', () => {
    const active = {
      id: 'project-active',
      name: 'Active Project',
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    } as Project
    const stale = {
      id: 'project-secondary',
      name: 'Secondary Project',
      settings: { max_vusers: 100 },
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    } as Project
    const committed = {
      ...stale,
      name: 'Secondary Project Renamed',
      settings: { max_vusers: 300 },
      updated_at: '2026-05-16T00:00:00Z',
    }
    const committedById = new Map([[committed.id, committed]])

    const reconciled = reconcileSettingsProjectSnapshots([active, stale], committedById)

    expect(reconciled.find(project => project.id === committed.id)).toEqual(committed)
    expect(committedById.get(committed.id)).toEqual(committed)
  })

  it('routes a non-active project governance save through the authoritative project context', async () => {
    const active = {
      id: 'project-active',
      name: 'Active Project',
      settings: {},
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    }
    const secondary = {
      id: 'project-secondary',
      name: 'Secondary Project',
      settings: { max_vusers: 100 },
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    }
    const updated = {
      ...secondary,
      name: 'Secondary Project Renamed',
      updated_at: '2026-05-16T00:00:00Z',
    }
    projectContextMock.activeProject = active
    projectContextMock.updateProject.mockResolvedValue(updated)
    seedProjectCatalog([active, secondary])

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    if (!projectSection) return
    const secondaryButton = Array.from(projectSection.querySelectorAll<HTMLButtonElement>('.settings-app-card'))
      .find(button => button.textContent?.includes(secondary.name))
    await act(async () => secondaryButton?.click())
    await selectSettingsSubnav(projectSection, 'Limits')
    const nameInput = findSettingInput(projectSection, 'Selected Project')
    if (!nameInput) return
    await setFieldValue(nameInput, updated.name)
    await act(async () => findButton('Save Project Governance')?.click())
    await flushPromises()

    expect(projectContextMock.updateProject).toHaveBeenCalledWith(
      secondary.id,
      expect.objectContaining({ name: updated.name }),
    )
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
    expect(projectSection.textContent).toContain(updated.name)
  })

  it('commits an authoritative pool-quota response through the project context', async () => {
    const active = {
      id: 'project-active',
      name: 'Active Project',
      settings: {},
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    }
    const original = {
      id: 'project-pool',
      name: 'Pooled Project',
      settings: { pool_quotas: { 'pool-east': { max_vusers: 100 } } },
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    }
    const updated = {
      ...original,
      settings: { pool_quotas: { 'pool-east': { max_vusers: 250 } } },
      updated_at: '2026-05-16T00:00:00Z',
    }
    projectContextMock.activeProject = active
    seedProjectCatalog([active, original])
    projectApiMock.updateProject.mockResolvedValue(updated)
    projectContextMock.updateProject.mockResolvedValue(updated)
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [{ id: 'pool-east', name: 'East', status: 'active' }], count: 1 })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    if (!projectSection) return
    const pooledProject = Array.from(projectSection.querySelectorAll<HTMLButtonElement>('.settings-app-card'))
      .find(button => button.textContent?.includes(original.name))
    await act(async () => pooledProject?.click())
    await selectSettingsSubnav(projectSection, 'Limits')

    const quotaInput = projectSection.querySelector<HTMLInputElement>('.pp-row input[type="number"]')
    expect(quotaInput).toBeTruthy()
    if (!quotaInput) return
    await setFieldValue(quotaInput, '250')
    const saveQuota = Array.from(projectSection.querySelectorAll('button'))
      .find(button => button.textContent === 'Save pool quotas') as HTMLButtonElement | undefined
    await act(async () => saveQuota?.click())
    await flushPromises()

    expect(projectContextMock.updateProject).toHaveBeenCalledWith(
      original.id,
      expect.objectContaining({ settings: updated.settings }),
    )
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
  })

	it('preserves project governance and pool-quota drafts when Save Changes commits both', async () => {
		const original = {
			id: 'project-pool',
			name: 'Pooled Project',
			settings: {
				max_vusers: 100,
				pool_quotas: { 'pool-east': { max_vusers: 100 } },
			},
			created_at: '2026-05-15T00:00:00Z',
			updated_at: '2026-05-15T00:00:00Z',
		}
		projectContextMock.activeProject = original
		seedProjectCatalog([original])
		projectApiMock.updateProject.mockImplementation(async (id: string, data: { name: string; settings?: ProjectSettings }) => ({
			...original,
			id,
			name: data.name,
			settings: data.settings,
			updated_at: '2026-05-16T00:00:00Z',
		}))
		fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [{ id: 'pool-east', name: 'East', status: 'active' }], count: 1 })

		await act(async () => {
			renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
		})
		await flushPromises()
		await flushPromises()
		await selectTopLevelSettingsTab('Projects & Compliance')
		const projectSection = container.querySelector('#settings-section-projects')
		if (!projectSection) return
		await selectSettingsSubnav(projectSection, 'Limits')

		const governanceInputs = Array.from(projectSection.querySelectorAll<HTMLInputElement>('input'))
			.filter(input => input.placeholder === 'Unlimited')
		const quotaInput = projectSection.querySelector<HTMLInputElement>('.pp-row input[type="number"]')
		expect(governanceInputs).toHaveLength(4)
		expect(quotaInput).toBeTruthy()
		if (governanceInputs.length !== 4 || !quotaInput) return
		await setFieldValue(governanceInputs[1], '300')
		await setFieldValue(quotaInput, '250')

		const saveAll = findButton('Save pending settings')
		expect(saveAll?.disabled).toBe(false)
		await act(async () => saveAll?.click())
		await flushPromises()
		await flushPromises()

		expect(projectApiMock.updateProject).toHaveBeenCalledTimes(2)
		const firstSettings = projectApiMock.updateProject.mock.calls[0][1].settings as ProjectSettings
		const secondSettings = projectApiMock.updateProject.mock.calls[1][1].settings as ProjectSettings
		expect(firstSettings.max_vusers).toBe(300)
		expect(firstSettings.pool_quotas?.['pool-east']?.max_vusers).toBe(100)
		expect(secondSettings.max_vusers).toBe(300)
		expect(secondSettings.pool_quotas?.['pool-east']?.max_vusers).toBe(250)
	})

  it('blocks saving project compliance when a rule is missing its regex pattern', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    await selectSettingsSubnav(projectSection, 'Redaction Rules')
    const addRuleButton = findButton('Add Rule')
    expect(addRuleButton).toBeDefined()
    if (!addRuleButton) return

    await act(async () => {
      addRuleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const ruleCards = Array.from(projectSection.querySelectorAll('.project-redaction-rule-card'))
    const secondRuleInputs = ruleCards[1].querySelectorAll('input')
    expect(secondRuleInputs).toHaveLength(2)
    if (secondRuleInputs.length < 1) return

    await setFieldValue(secondRuleInputs[0] as HTMLInputElement, 'broken rule')

    await flushPromises()

    const saveButton = findButton('Save redaction rules')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    expect(saveButton.disabled).toBe(true)
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Redaction rule 2 needs a regex pattern.')
  })

  it('saves trusted-header auth settings through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'trusted_headers')

    // Trusted headers without a proxy gate is refused before save, mirroring the
    // controller's production boot check.
    expect(findButton('Save pending settings')?.hasAttribute('disabled')).toBe(true)
    expect(generalSection.textContent).toContain('requires a trusted proxy CIDR allowlist')

    const trustedProxyCIDRs = findSettingInput(generalSection, 'Trusted Proxy CIDRs')
    expect(trustedProxyCIDRs).toBeTruthy()
    if (!trustedProxyCIDRs) return
    await setFieldValue(trustedProxyCIDRs, '10.0.0.0/8')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      auth: expect.objectContaining({
        mode: 'trusted_headers',
        provider_name: '',
        login_url: '',
        logout_url: '',
        trusted_proxy_cidrs: ['10.0.0.0/8'],
        user_header: 'X-Auth-Request-User',
        roles_header: 'X-APEXLoad-Roles',
        project_roles_header: 'X-APEXLoad-Project-Roles',
      }),
    }))
    expect(generalSection.textContent).toContain('Microsoft Entra ID Troubleshooting')
    expect(generalSection.textContent).toContain('Subject Header Check')
    expect(generalSection.textContent).toContain('Role Header Check')
    expect(container.textContent).toContain('Settings saved.')
  })

  it('prevents selecting browser-only auth modes from the embedded desktop settings', async () => {
    ;(window as BridgeWindow).go = { main: { App: {} } }
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return
    await selectGeneralSubnav(generalSection, 'Authentication')
    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect?.querySelector<HTMLOptionElement>('option[value="oidc"]')?.disabled).toBe(true)
    expect(authModeSelect?.querySelector<HTMLOptionElement>('option[value="trusted_headers"]')?.disabled).toBe(true)
    expect(authModeSelect?.querySelector<HTMLOptionElement>('option[value="local"]')?.disabled).toBe(false)
    expect(generalSection.textContent).toContain('Local Test Login and Dev Bypass work in the embedded desktop workspace')
  })

  it('distinguishes a saved auth mode awaiting restart from the active runtime mode', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)
    const onThemeChange = vi.fn()

    await act(async () => {
      renderWithSettingsTopbar(
        root,
        <SettingsPage
          theme="dark"
          onThemeChange={onThemeChange}
          authStatus={{ enabled: false, mode: 'dev', authenticated: false }}
        />,
      )
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return
    await selectGeneralSubnav(generalSection, 'Authentication')

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return
    await setFieldValue(authModeSelect, 'local')

    expect(generalSection.textContent).toContain('Draft: Local Test Login')
    expect(generalSection.querySelector('[data-testid="auth-mode-restart-pending"]')).toBeNull()

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const restartPending = generalSection.querySelector('[data-testid="auth-mode-restart-pending"]')
    expect(restartPending?.textContent).toContain('Authentication restart pending.')
    expect(restartPending?.textContent).toContain('Saved mode: Local Test Login.')
    expect(restartPending?.textContent).toContain('Active runtime mode: Dev Bypass.')
    expect(generalSection.textContent).toContain('Saved: Local Test Login')

    await act(async () => {
      renderWithSettingsTopbar(
        root,
        <SettingsPage
          theme="dark"
          onThemeChange={onThemeChange}
          authStatus={{ enabled: true, mode: 'local', authenticated: false }}
        />,
      )
    })

    expect(generalSection.querySelector('[data-testid="auth-mode-restart-pending"]')).toBeNull()
  })

  it('disables settings, access handoff, and audit actions when effective RBAC denies them', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [
        {
          id: 'system_configuration',
          name: 'System Configuration',
          description: 'Controller-wide settings.',
          operations: [
            { id: 'view_system', label: 'View settings', allowed: true, grant_source: 'global' },
            { id: 'manage_system', label: 'Update settings', allowed: false },
          ],
        },
        {
          id: 'identity_and_audit',
          name: 'Identity, Service Accounts, and Audit',
          description: 'Identity operations.',
          operations: [
            { id: 'view_audit', label: 'View audit events', allowed: false },
            { id: 'manage_service_accounts', label: 'Manage service accounts', allowed: false },
          ],
        },
      ],
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const apiEndpointInput = findSettingInput(generalSection, 'API Endpoint')
    expect(apiEndpointInput).toBeTruthy()
    if (!apiEndpointInput) return
    await setFieldValue(apiEndpointInput, ':9090')

    const saveButton = findButton('Save pending settings')
    const resetButton = findButton('Reset All Settings to Defaults')
    expect(saveButton?.disabled).toBe(true)
    expect(resetButton?.disabled).toBe(true)
    expect(container.textContent).toContain('Current session can view settings but cannot save global configuration changes.')

    await selectGeneralSubnav(generalSection, 'Governance')
    expect(findSettingsSubnavButton(generalSection, 'Schedule Defaults')?.getAttribute('aria-selected')).toBe('true')
    expect(generalSection.textContent).toContain('Global Settings Permission Required')

    await selectSettingsSubnav(generalSection, 'Script Imports')
    const migrationPolicyInput = findSettingInput(generalSection, 'Allow source secret migration')
    expect(migrationPolicyInput?.disabled).toBe(true)
    expect(generalSection.textContent).toContain('Only a system administrator can change source-secret migration policy.')

    await selectSettingsSubnav(generalSection, 'Service Account APIs')
    expect(generalSection.textContent).toContain('Access & API Keys')
    expect(generalSection.textContent).toContain('Current session can view auth settings but cannot manage service-account API keys.')
    expect(findButton('Open Access & API Keys')?.disabled).toBe(true)

    await selectSettingsSubnav(generalSection, 'Recent Audit Events')
    expect(generalSection.textContent).toContain('Recent Audit Events')
    expect(findButton('Export CSV')?.disabled).toBe(true)
  })

  it('lets audit viewers navigate to read-only governance while system mutations stay disabled', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      project_id: 'project-1',
      resources: [
        {
          id: 'system_configuration',
          name: 'System Configuration',
          description: 'Controller-wide settings.',
          operations: [
            { id: 'view_system', label: 'View settings', allowed: true, grant_source: 'global' },
            { id: 'manage_system', label: 'Update settings', allowed: false },
          ],
        },
        {
          id: 'identity_and_audit',
          name: 'Identity, Service Accounts, and Audit',
          description: 'Identity operations.',
          operations: [
            { id: 'view_audit', label: 'View audit events', allowed: true, grant_source: 'global' },
            { id: 'manage_service_accounts', label: 'Manage service accounts', allowed: false },
          ],
        },
      ],
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const apiEndpointInput = findSettingInput(generalSection, 'API Endpoint')
    expect(apiEndpointInput?.closest('fieldset')?.disabled).toBe(true)

    const usageButton = findGeneralSubnavButton(generalSection, 'Usage')
    await act(async () => usageButton?.click())
    expect(findButton('Refresh Usage')?.disabled).toBe(false)
    expect(findButton('Export Usage CSV')?.disabled).toBe(false)
    expect(findButton('Capture Snapshot')?.disabled).toBe(true)

    const governanceButton = findGeneralSubnavButton(generalSection, 'Governance')
    expect(governanceButton?.closest('fieldset')).toBeNull()
    await act(async () => governanceButton?.click())
    expect(governanceButton?.getAttribute('aria-selected')).toBe('true')

    const auditButton = findSettingsSubnavButton(generalSection, 'Recent Audit Events')
    expect(auditButton?.closest('fieldset')).toBeNull()
    await act(async () => auditButton?.click())
    expect(auditButton?.getAttribute('aria-selected')).toBe('true')

    const auditPanel = generalSection.querySelector('#settings-governance-panel-recent-audit-events')
    expect(auditPanel?.hasAttribute('hidden')).toBe(false)
    expect(findButton('Export CSV')?.disabled).toBe(false)
    expect(Array.from(auditPanel?.querySelectorAll('input') || []).every(input => !(input as HTMLInputElement).disabled)).toBe(true)
  })

  it('saves native OIDC auth settings through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'oidc')

    const issuerInput = findSettingInput(generalSection, 'Issuer URL')
    const clientIDInput = findSettingInput(generalSection, 'Client ID')
    const redirectURLInput = findSettingInput(generalSection, 'Redirect URL')
    expect(issuerInput).toBeTruthy()
    expect(clientIDInput).toBeTruthy()
    expect(redirectURLInput).toBeTruthy()
    if (!issuerInput || !clientIDInput || !redirectURLInput) return

    await setFieldValue(issuerInput, 'https://login.example.test/tenant/v2.0')
    await setFieldValue(clientIDInput, 'apexload-dashboard')
    await setFieldValue(redirectURLInput, 'https://apexload.example.test/api/v1/auth/callback')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      auth: expect.objectContaining({
        mode: 'oidc',
        issuer_url: 'https://login.example.test/tenant/v2.0',
        client_id: 'apexload-dashboard',
        client_secret_env_var: 'APEXLOAD_OIDC_CLIENT_SECRET',
        redirect_url: 'https://apexload.example.test/api/v1/auth/callback',
        scopes: ['openid', 'profile', 'email'],
        roles_claim: 'roles',
        project_roles_claim: 'project_roles',
        session_secret_env_var: 'APEXLOAD_AUTH_SESSION_SECRET',
      }),
    }))
    expect(generalSection.textContent).toContain('Native OIDC Session Flow')
    expect(container.textContent).toContain('Settings saved.')
  })

  it('applies OIDC provider presets without overwriting deployment-specific values', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'oidc')

    const issuerInput = findSettingInput(generalSection, 'Issuer URL')
    const clientIDInput = findSettingInput(generalSection, 'Client ID')
    const redirectURLInput = findSettingInput(generalSection, 'Redirect URL')
    expect(issuerInput).toBeTruthy()
    expect(clientIDInput).toBeTruthy()
    expect(redirectURLInput).toBeTruthy()
    if (!issuerInput || !clientIDInput || !redirectURLInput) return

    await setFieldValue(issuerInput, 'https://idp.example.test/tenant')
    await setFieldValue(clientIDInput, 'existing-client')
    await setFieldValue(redirectURLInput, 'https://apexload.example.test/api/v1/auth/callback')

    const entraPresetButton = findButton('Microsoft Entra ID OIDC')
    expect(entraPresetButton).toBeDefined()
    if (!entraPresetButton) return

    await act(async () => {
      entraPresetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const providerInput = findSettingInput(generalSection, 'Provider Label')
    const scopesInput = findSettingInput(generalSection, 'Scopes')
    const rolesClaimInput = findSettingInput(generalSection, 'Roles Claim')
    const projectRolesClaimInput = findSettingInput(generalSection, 'Project Roles Claim')
    const logoutInput = findSettingInput(generalSection, 'Post-Logout Redirect URL')

    expect(providerInput?.value).toBe('Microsoft Entra ID')
    expect(scopesInput?.value).toBe('openid, profile, email')
    expect(rolesClaimInput?.value).toBe('roles')
    expect(projectRolesClaimInput?.value).toBe('project_roles')
    expect(logoutInput?.value).toBe('/')
    expect(issuerInput.value).toBe('https://idp.example.test/tenant')
    expect(clientIDInput.value).toBe('existing-client')
    expect(redirectURLInput.value).toBe('https://apexload.example.test/api/v1/auth/callback')
    expect(generalSection.textContent).toContain('Issuer URL, client ID, and redirect URL stay deployment-specific.')
  })

  it('shows dev-mode auth admin panels and links service-account management to Access', async () => {
    apiMock.listAuthServiceAccounts
      .mockResolvedValueOnce({
        service_accounts: [
          {
            id: 'svc-1',
            name: 'Nightly CI',
            description: 'Runs nightly smoke coverage',
            active: true,
            global_roles: ['viewer'],
            project_roles: { 'project-1': ['editor'] },
            created_at: '2026-05-16T00:00:00Z',
            updated_at: '2026-05-16T00:00:00Z',
          },
        ],
        count: 1,
      })
      .mockResolvedValueOnce({
        service_accounts: [
          {
            id: 'svc-1',
            name: 'Nightly CI',
            description: 'Runs nightly smoke coverage',
            active: true,
            global_roles: ['viewer'],
            project_roles: { 'project-1': ['editor'] },
            created_at: '2026-05-16T00:00:00Z',
            updated_at: '2026-05-16T00:00:00Z',
          },
          {
            id: 'svc-2',
            name: 'Release Automation',
            description: 'Runs release checks',
            active: true,
            global_roles: ['editor'],
            project_roles: {},
            created_at: '2026-05-16T02:00:00Z',
            updated_at: '2026-05-16T02:00:00Z',
          },
        ],
        count: 2,
      })
    apiMock.listAuthAuditEvents
      .mockResolvedValueOnce({
        events: [
          {
            id: 'audit-1',
            action: 'system.settings.update',
            actor_type: 'dev_mode',
            actor_subject: 'dev-mode',
            actor_display_name: 'Development Mode',
            resource_type: 'system_settings',
            resource_id: 'global',
            status: 'success',
            created_at: '2026-05-16T01:00:00Z',
          },
        ],
        count: 1,
      })
      .mockResolvedValueOnce({
        events: [
          {
            id: 'audit-2',
            action: 'auth.service_account.create',
            actor_type: 'dev_mode',
            actor_subject: 'dev-mode',
            actor_display_name: 'Development Mode',
            resource_type: 'service_account',
            resource_id: 'svc-2',
            status: 'success',
            created_at: '2026-05-16T02:00:00Z',
          },
        ],
        count: 1,
      })

    const onNavigate = vi.fn()
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} authStatus={{ enabled: false, mode: 'dev', authenticated: false }} onNavigate={onNavigate} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')
    expect(generalSection.textContent).toContain('Development Bypass')

    await selectGeneralSubnav(generalSection, 'Governance')
    expect(findSettingsSubnavButton(generalSection, 'Schedule Defaults')?.getAttribute('aria-selected')).toBe('true')

    await selectSettingsSubnav(generalSection, 'Service Account APIs')
    expect(generalSection.textContent).toContain('Service Account API Keys')
    expect(generalSection.textContent).toContain('Generate, rotate, disable, and delete scoped API keys from Access & API Keys.')
    expect(generalSection.querySelector('#settings-governance-panel-service-account-apis')?.hasAttribute('hidden')).toBe(false)

    const openAccessButton = findButton('Open Access & API Keys')
    expect(openAccessButton).toBeDefined()
    if (!openAccessButton) return

    await act(async () => {
      openAccessButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('access')
    expect(apiMock.createAuthServiceAccount).not.toHaveBeenCalled()
    expect(apiMock.rotateAuthServiceAccount).not.toHaveBeenCalled()

    await selectSettingsSubnav(generalSection, 'Recent Audit Events')
    const auditPanel = generalSection.querySelector('#settings-governance-panel-recent-audit-events')
    expect(auditPanel?.hasAttribute('hidden')).toBe(false)
    expect(auditPanel?.textContent).toContain('Recent Audit Events')
    expect(auditPanel?.textContent).toContain('system.settings.update')

    const auditFilterInputs = Array.from(auditPanel?.querySelectorAll('input') || []).filter(input => {
      const element = input as HTMLInputElement
      return element.placeholder === 'action' || element.placeholder === 'actor_type' || element.placeholder === 'status'
    }) as HTMLInputElement[]
    expect(auditFilterInputs).toHaveLength(3)
    if (auditFilterInputs.length !== 3) return
    await setFieldValue(auditFilterInputs[0], 'auth.service_account.rotate')
    await setFieldValue(auditFilterInputs[1], 'dev_mode')
    await setFieldValue(auditFilterInputs[2], 'success')
    await flushPromises()
    expect(apiMock.listAuthAuditEvents).toHaveBeenLastCalledWith(expect.objectContaining({
      limit: 20,
      action: 'auth.service_account.rotate',
      actor_type: 'dev_mode',
      status: 'success',
    }))

    const exportAuditButton = Array.from(auditPanel?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === 'Export CSV') as HTMLButtonElement | undefined
    expect(exportAuditButton).toBeDefined()
    if (!exportAuditButton) return
    downloadBlobMock.mockResolvedValueOnce('cancelled')
    await act(async () => {
      exportAuditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.downloadAuthAuditEvents).toHaveBeenCalledWith(expect.objectContaining({
      limit: 1000,
      action: 'auth.service_account.rotate',
      actor_type: 'dev_mode',
      status: 'success',
    }))
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^apexload-audit-events-\d{4}-\d{2}-\d{2}\.csv$/))
    expect(generalSection.textContent).not.toContain('Audit export downloaded (up to 1,000 matching events).')

    downloadBlobMock.mockResolvedValueOnce('saved')
    await act(async () => {
      exportAuditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(generalSection.textContent).toContain('Audit export downloaded (up to 1,000 matching events).')
  })

  it('shows the current authenticated identity and effective roles when trusted-header auth is enabled', async () => {
    apiMock.getSystemSettings.mockResolvedValueOnce({
      general: {
        api_endpoint: ':8080',
        max_concurrent_tests: 10,
        metrics_interval_seconds: 1,
      },
      thresholds: {},
      capacity_planning: {
        headroom_pct: 20,
        generator: {
          cpu_cores: 4,
          memory_mb: 8192,
          network_mbps: 1000,
          max_vusers: 10000,
        },
        availability: {
          source: 'configured',
          configured_available_generators: 1,
        },
      },
      notifications: { webhooks: [] },
      auth: {
        mode: 'trusted_headers',
        provider_name: 'Microsoft Entra ID',
        login_url: 'https://login.example.test/start',
        logout_url: '',
        user_header: 'X-Auth-Request-User',
        name_header: 'X-Auth-Request-Name',
        email_header: 'X-Auth-Request-Email',
        roles_header: 'X-APEXLoad-Roles',
        project_roles_header: 'X-APEXLoad-Project-Roles',
      },
    })
    apiMock.getAuthDiagnostics.mockResolvedValueOnce({
      enabled: true,
      mode: 'trusted_headers',
      provider_name: 'Microsoft Entra ID',
      provider_model: 'upstream_gateway',
      ready: true,
      summary: 'Auth mode is ready with 1 advisory check(s) still worth reviewing.',
      checks: [
        { id: 'subject_header', label: 'Subject Header', status: 'pass', summary: 'Configured', detail: 'X-Auth-Request-User' },
        { id: 'role_headers', label: 'Role Header Mapping', status: 'pass', summary: 'Configured', detail: 'X-APEXLoad-Roles | X-APEXLoad-Project-Roles' },
        { id: 'current_request_identity', label: 'Current Request Identity', status: 'pass', summary: 'Trusted headers authenticated the current request.', detail: 'alice-admin' },
      ],
    })

    await act(async () => {
      renderWithSettingsTopbar(root,
        <SettingsPage
          theme="dark"
          onThemeChange={vi.fn()}
          authStatus={{
            enabled: true,
            mode: 'trusted_headers',
            authenticated: true,
            user: {
              subject: 'alice-admin',
              display_name: 'Alice Admin',
              email: 'alice@example.com',
              global_roles: ['system_admin', 'viewer'],
              project_roles: {
                'project-1': ['editor', 'viewer'],
                'project-2': ['project_admin'],
              },
            },
          }}
        />,
      )
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'trusted_headers')

    expect(generalSection.textContent).toContain('Current Session Access')
    expect(generalSection.textContent).toContain('Authenticated')
    expect(generalSection.textContent).toContain('alice-admin')
    expect(generalSection.textContent).toContain('Alice Admin')
    expect(generalSection.textContent).toContain('alice@example.com')
    expect(generalSection.textContent).toContain('System Admin, Viewer')
    expect(generalSection.textContent).toContain('project-1: Editor, Viewer')
    expect(generalSection.textContent).toContain('project-2: Project Admin')
    expect(generalSection.textContent).toContain('Microsoft Entra ID Gateway Contract')
    expect(generalSection.textContent).toContain('Required Subject Header')
    expect(generalSection.textContent).toContain('X-Auth-Request-User')
    expect(generalSection.textContent).toContain('Global Role Mapping')
    expect(generalSection.textContent).toContain('X-APEXLoad-Roles')
    expect(generalSection.textContent).toContain('Microsoft Entra ID Troubleshooting')
    expect(generalSection.textContent).toContain('Headers detected')
    expect(generalSection.textContent).toContain('Auth Diagnostics')
    expect(generalSection.textContent).toContain('Auth mode is ready with 1 advisory check(s) still worth reviewing.')
    expect(generalSection.textContent).toContain('Current Request Identity')
    expect(generalSection.textContent).toContain('Pass | alice-admin')

    await selectGeneralSubnav(generalSection, 'Governance')
    await selectSettingsSubnav(generalSection, 'Service Account APIs')
    expect(generalSection.querySelector('#settings-governance-panel-service-account-apis')?.hasAttribute('hidden')).toBe(false)
    expect(generalSection.textContent).toContain('Built-In Role Reference')
    expect(generalSection.textContent).toContain('Manage global settings')
    expect(generalSection.textContent).toContain('Global only')
    expect(generalSection.textContent).toContain('Route Access Matrix')
    expect(generalSection.textContent).toContain('System Configuration')
    expect(generalSection.textContent).toContain('Update system settings: Global: System Admin')
    expect(generalSection.textContent).toContain('Effective Access For Current Session')
    expect(generalSection.textContent).toContain('Tests and Scenarios')
    expect(generalSection.textContent).toContain('Allowed via project role')
  })

  it('renders OIDC diagnostics from the backend readiness payload', async () => {
    apiMock.getSystemSettings.mockResolvedValueOnce({
      general: {
        api_endpoint: ':8080',
        max_concurrent_tests: 10,
        metrics_interval_seconds: 1,
      },
      thresholds: {},
      capacity_planning: {
        headroom_pct: 20,
        generator: {
          cpu_cores: 4,
          memory_mb: 8192,
          network_mbps: 1000,
          max_vusers: 10000,
        },
        availability: {
          source: 'configured',
          configured_available_generators: 1,
        },
      },
      notifications: { webhooks: [] },
      auth: {
        mode: 'oidc',
        provider_name: 'Example OIDC',
        issuer_url: 'https://login.example.test/tenant/v2.0',
        client_id: 'apexload-dashboard',
        client_secret_env_var: 'APEXLOAD_OIDC_CLIENT_SECRET',
        redirect_url: 'https://apexload.example.test/api/v1/auth/callback',
        scopes: ['openid', 'profile', 'email'],
        roles_claim: 'roles',
        project_roles_claim: 'project_roles',
        session_secret_env_var: 'APEXLOAD_AUTH_SESSION_SECRET',
        logout_url: '/',
        login_url: '',
        user_header: 'X-Auth-Request-User',
        name_header: 'X-Auth-Request-Name',
        email_header: 'X-Auth-Request-Email',
        roles_header: 'X-APEXLoad-Roles',
        project_roles_header: 'X-APEXLoad-Project-Roles',
      },
    })
    apiMock.getAuthDiagnostics.mockResolvedValueOnce({
      enabled: true,
      mode: 'oidc',
      provider_name: 'Example OIDC',
      provider_model: 'native_oidc',
      ready: false,
      summary: 'Auth mode is not ready yet. Resolve 2 failing check(s) before relying on this configuration.',
      checks: [
        { id: 'client_secret_env', label: 'Client Secret Environment Variable', status: 'fail', summary: 'Environment variable is not set on the controller.', detail: 'APEXLOAD_OIDC_CLIENT_SECRET' },
        { id: 'issuer_discovery', label: 'OIDC Discovery', status: 'fail', summary: 'APEX Load could not reach the issuer discovery document.', detail: 'dial tcp: lookup login.example.test: no such host' },
      ],
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')

    expect(generalSection.textContent).toContain('Native OIDC Session Flow')
    expect(generalSection.textContent).toContain('Auth Diagnostics')
    expect(generalSection.textContent).toContain('Needs attention')
    expect(generalSection.textContent).toContain('Auth mode is not ready yet. Resolve 2 failing check(s) before relying on this configuration.')
    expect(generalSection.textContent).toContain('Client Secret Environment Variable')
    expect(generalSection.textContent).toContain('Action Required | APEXLOAD_OIDC_CLIENT_SECRET')
    expect(generalSection.textContent).toContain('OIDC Discovery')
  })

  it('saves notification webhook settings through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    await selectTopLevelSettingsTab('Integrations')
    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return

    await selectSettingsSubnav(integrationsSection, 'Notifications')
    const addWebhookButton = findButton('Add Webhook')
    expect(addWebhookButton).toBeDefined()
    if (!addWebhookButton) return

    await act(async () => {
      addWebhookButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const webhookCard = integrationsSection.querySelector('.settings-notification-webhook-card')
    expect(webhookCard).toBeTruthy()
    if (!webhookCard) return

    const nameInput = webhookCard.querySelector('input[placeholder="Perf Alerts"]') as HTMLInputElement | null
    const urlInput = webhookCard.querySelector('input[placeholder="https://hooks.slack.com/services/..."]') as HTMLInputElement | null
    const formatSelect = webhookCard.querySelector('select') as HTMLSelectElement | null
    expect(nameInput).toBeTruthy()
    expect(urlInput).toBeTruthy()
    expect(formatSelect).toBeTruthy()
    if (!nameInput || !urlInput || !formatSelect) return

    await setFieldValue(nameInput, 'Slack Alerts Updated')
    await setFieldValue(urlInput, 'https://hooks.slack.test/services/T000/B000/abc123')
    await setFieldValue(formatSelect, 'slack')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      notifications: {
        webhooks: [expect.objectContaining({
          name: 'Slack Alerts Updated',
          enabled: true,
          url: 'https://hooks.slack.test/services/T000/B000/abc123',
          format: 'slack',
        })],
      },
    }))
    expect(container.textContent).toContain('Settings saved.')
  })

  it('persists selected notification webhook event filters', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)
    const baseline = await apiMock.getSystemSettings()
    const webhook = {
      name: 'Slack Alerts',
      enabled: true,
      url: 'https://hooks.slack.test/services/T000/B000/abc123',
      format: 'slack' as const,
      events: ['test_created', 'test_failed'],
      api_token: '',
      entity_selector: '',
    }
    apiMock.getSystemSettings.mockResolvedValue({
      ...baseline,
      notifications: {
        webhooks: [webhook],
      },
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    await selectTopLevelSettingsTab('Integrations')
    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return

    await selectSettingsSubnav(integrationsSection, 'Notifications')

    const webhookCard = integrationsSection.querySelector('.settings-notification-webhook-card')
    expect(webhookCard).toBeTruthy()
    if (!webhookCard) return

    const testCreatedCheckbox = webhookCard.querySelector<HTMLInputElement>('input[data-testid="notification-webhook-0-event-test_created"]')
    const testFailedCheckbox = webhookCard.querySelector<HTMLInputElement>('input[data-testid="notification-webhook-0-event-test_failed"]')
    expect(testCreatedCheckbox).toBeTruthy()
    expect(testFailedCheckbox).toBeTruthy()
    expect(testCreatedCheckbox?.disabled).toBe(false)
    expect(testFailedCheckbox?.disabled).toBe(false)
    if (!testCreatedCheckbox || !testFailedCheckbox) return

    const nameInput = webhookCard.querySelector('input[placeholder="Perf Alerts"]') as HTMLInputElement | null
    const urlInput = webhookCard.querySelector('input[placeholder="https://hooks.slack.com/services/..."]') as HTMLInputElement | null
    const formatSelect = webhookCard.querySelector('select') as HTMLSelectElement | null
    expect(nameInput).toBeTruthy()
    expect(urlInput).toBeTruthy()
    expect(formatSelect).toBeTruthy()
    if (!nameInput || !urlInput || !formatSelect) return

    expect(testCreatedCheckbox.checked).toBe(true)
    expect(testFailedCheckbox.checked).toBe(true)
    await setFieldValue(nameInput, 'Slack Alerts Updated')
    expect(nameInput.value).toBe('Slack Alerts Updated')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      notifications: {
        webhooks: [expect.objectContaining({
          events: ['test_created', 'test_failed'],
        })],
      },
    }))
  })

  it('saves Dynatrace notification metadata through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    await selectTopLevelSettingsTab('Integrations')
    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return

    await selectSettingsSubnav(integrationsSection, 'Notifications')
    const addWebhookButton = findButton('Add Webhook')
    expect(addWebhookButton).toBeDefined()
    if (!addWebhookButton) return

    await act(async () => {
      addWebhookButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    let webhookCard = integrationsSection?.querySelector('.settings-notification-webhook-card')
    expect(webhookCard).toBeTruthy()
    if (!webhookCard) return

    const formatSelect = webhookCard.querySelector('select') as HTMLSelectElement | null
    expect(formatSelect).toBeTruthy()
    if (!formatSelect) return

    await setFieldValue(formatSelect, 'dynatrace')
    await flushPromises()

    webhookCard = container.querySelector('#settings-section-integrations .settings-notification-webhook-card')
    expect(webhookCard).toBeTruthy()
    if (!webhookCard) return

    const urlInput = webhookCard.querySelector('input[placeholder="https://{environment}.live.dynatrace.com/api/v2/events/ingest"]') as HTMLInputElement | null
    let tokenInput = webhookCard.querySelector('input[placeholder="dt0c01..."]') as HTMLInputElement | null
    let timeoutInput = webhookCard.querySelector('input[placeholder="15"]') as HTMLInputElement | null
    let entityInput = webhookCard.querySelector('input[placeholder="type(SERVICE),entityName.equals(CheckoutService)"]') as HTMLInputElement | null
    expect(urlInput).toBeTruthy()
    expect(tokenInput).toBeTruthy()
    expect(timeoutInput).toBeTruthy()
    expect(entityInput).toBeTruthy()
    if (!urlInput || !tokenInput || !timeoutInput || !entityInput) return

    await setFieldValue(urlInput, 'https://example.live.dynatrace.com/api/v2/events/ingest')
    await flushPromises()
    tokenInput = container.querySelector('#settings-section-integrations input[placeholder="dt0c01..."]') as HTMLInputElement | null
    timeoutInput = container.querySelector('#settings-section-integrations input[placeholder="15"]') as HTMLInputElement | null
    entityInput = container.querySelector('#settings-section-integrations input[placeholder="type(SERVICE),entityName.equals(CheckoutService)"]') as HTMLInputElement | null
    expect(tokenInput).toBeTruthy()
    expect(timeoutInput).toBeTruthy()
    expect(entityInput).toBeTruthy()
    if (!tokenInput || !timeoutInput || !entityInput) return
    await setFieldValue(tokenInput, 'dt-token')
    await flushPromises()
    timeoutInput = container.querySelector('#settings-section-integrations input[placeholder="15"]') as HTMLInputElement | null
    entityInput = container.querySelector('#settings-section-integrations input[placeholder="type(SERVICE),entityName.equals(CheckoutService)"]') as HTMLInputElement | null
    expect(timeoutInput).toBeTruthy()
    expect(entityInput).toBeTruthy()
    if (!timeoutInput || !entityInput) return
    await setFieldValue(timeoutInput, '30')
    await flushPromises()
    entityInput = container.querySelector('#settings-section-integrations input[placeholder="type(SERVICE),entityName.equals(CheckoutService)"]') as HTMLInputElement | null
    expect(entityInput).toBeTruthy()
    if (!entityInput) return
    await setFieldValue(entityInput, 'type(SERVICE),entityName.equals(CheckoutService)')
    await flushPromises()

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      notifications: {
        webhooks: [expect.objectContaining({
          enabled: true,
          url: 'https://example.live.dynatrace.com/api/v2/events/ingest',
          format: 'dynatrace',
          api_token: 'dt-token',
          timeout_minutes: 30,
          entity_selector: 'type(SERVICE),entityName.equals(CheckoutService)',
        })],
      },
    }))
    expect(container.textContent).toContain('Settings saved.')
  })

  it('keeps the write-only Dynatrace token marker when only the display name changes', async () => {
    const baseline = await apiMock.getSystemSettings()
    apiMock.getSystemSettings.mockResolvedValueOnce({
      ...baseline,
      notifications: {
        webhooks: [{
          name: 'Old Dynatrace name',
          enabled: true,
          url: 'https://example.live.dynatrace.com/api/v2/events/ingest',
          format: 'dynatrace',
          events: ['telemetry_breached'],
          api_token: '',
          has_api_token: true,
          timeout_minutes: 15,
        }],
      },
    })
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Integrations')
    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return
    await selectSettingsSubnav(integrationsSection, 'Notifications')

    const webhookCard = integrationsSection.querySelector('.settings-notification-webhook-card')
    const nameInput = webhookCard?.querySelector('input[placeholder="Perf Alerts"]') as HTMLInputElement | null
    const tokenInput = webhookCard?.querySelector('input[placeholder="Configured; enter a new token to rotate"]') as HTMLInputElement | null
    expect(nameInput).toBeTruthy()
    expect(tokenInput).toBeTruthy()
    if (!nameInput || !tokenInput) return
    expect(tokenInput.value).toBe('')

    await setFieldValue(nameInput, 'Renamed Dynatrace')
    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      notifications: {
        webhooks: [expect.objectContaining({
          name: 'Renamed Dynatrace',
          format: 'dynatrace',
          url: 'https://example.live.dynatrace.com/api/v2/events/ingest',
          api_token: '',
          has_api_token: true,
        })],
      },
    }))
  })

  it('validates trusted-header auth when no roles headers are configured', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    await selectGeneralSubnav(generalSection, 'Authentication')

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'trusted_headers')

    const authInputs = Array.from(generalSection.querySelectorAll('input')).filter(input => {
      const element = input as HTMLInputElement
      return element.placeholder === 'X-APEXLoad-Roles' || element.placeholder === 'X-APEXLoad-Project-Roles'
    }) as HTMLInputElement[]
    expect(authInputs).toHaveLength(2)
    if (authInputs.length !== 2) return

    await setFieldValue(authInputs[0], '')
    await setFieldValue(authInputs[1], '')

    expect(container.textContent).toContain('Trusted header auth requires a roles header or project roles header.')
    const saveButton = findButton('Save pending settings')
    expect(saveButton?.disabled).toBe(true)
  })

  it('names confirmation dialogs, moves focus inside, and closes with Escape', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const resetButton = findButton('Reset All Settings to Defaults')
    expect(resetButton).toBeTruthy()
    if (!resetButton) return
    resetButton.focus()

    await act(async () => {
      resetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const dialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(dialog).toBeTruthy()
    expect(dialog?.getAttribute('aria-labelledby')).toBeTruthy()
    expect(document.activeElement?.textContent).toBe('Close')
    expect(document.activeElement?.getAttribute('aria-label')).toBe('Close dialog')

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(document.activeElement).toBe(resetButton)
  })

  it('shows a threshold validation error inline under its field and badges the Thresholds chip', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    expect(thresholdsSection).toBeTruthy()
    if (!thresholdsSection) return

    const p99Input = findSettingInput(thresholdsSection, 'Max Response Time (P99 ms)')
    expect(p99Input).toBeTruthy()
    if (!p99Input) return
    await setFieldValue(p99Input, '-5')

    const fieldErrors = Array.from(thresholdsSection.querySelectorAll('.settings-field-error')).map(el => el.textContent)
    expect(fieldErrors).toContain('Max P99 latency must be greater than 0 ms.')

    const topbar = document.querySelector('[data-testid="settings-topbar"]')
    // With a badge showing, the chip's aria-label folds in the issue count so
    // assistive tech announces it.
    expect(topbar).toBeTruthy()
    expect(container.querySelector('nav[aria-label="Settings sections"] [aria-label="Validation issues"]')).toBeTruthy()
  })

  it('summarizes validation errors from other sections with a jump control', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const p99Input = findSettingInput(thresholdsSection, 'Max Response Time (P99 ms)')
    if (!p99Input) return
    await setFieldValue(p99Input, '-5')

    await selectTopLevelSettingsTab('General')
    const errorsBlock = container.querySelector('.settings-errors')
    expect(errorsBlock?.textContent).toContain('1 issue in Performance Thresholds')

    const reviewButton = Array.from(errorsBlock?.querySelectorAll('button') ?? []).find(button => button.textContent === 'Review')
    expect(reviewButton).toBeTruthy()
    if (!reviewButton) return
    await act(async () => {
      reviewButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.querySelector('#settings-section-thresholds')?.hasAttribute('hidden')).toBe(false)
  })

  it('badges the capacity subnav tab that owns a validation error', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Generator Profile')
    const cpuInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    if (!cpuInput) return
    await setFieldValue(cpuInput, '0')

    const generatorTab = findSettingsSubnavButton(capacitySection, 'Generator Profile')
    expect(generatorTab?.querySelector('.settings-subnav-badge')?.textContent).toBe('1')
    expect(capacitySection.textContent).toContain('Generator CPU cores must be greater than 0.')
  })

  it('clearing Configured Available Generators shows a required-field error instead of snapping to 0', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Availability')
    const configuredInput = findSettingInput(capacitySection, 'Configured Available Generators')
    expect(configuredInput).toBeTruthy()
    if (!configuredInput) return

    await setFieldValue(configuredInput, '')
    expect(configuredInput.value).toBe('')
    expect(capacitySection.textContent).toContain('Configured available generators is required.')

    const saveButton = findButton('Save pending settings')
    expect(saveButton?.disabled).toBe(true)
    expect(findButton('Discard Changes')).toBeTruthy()
    expect(findButton('No pending settings')).toBeFalsy()
  })

  it('registers a blank capacity draft as dirty until it is discarded', async () => {
    const registered: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const registrar = (guard: NavigationGuard, dirty: boolean) => {
      registered.push({ guard, dirty })
      return () => {}
    }

    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={registrar}>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Availability')
    const configuredInput = findSettingInput(capacitySection, 'Configured Available Generators')
    if (!configuredInput) return
    await setFieldValue(configuredInput, '')

    const latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    expect(latest?.dirty).toBe(true)
    expect(latest?.guard.isDirty()).toBe(true)
    expect(findButton('Save pending settings')?.disabled).toBe(true)
    expect(findButton('Discard Changes')).toBeTruthy()

    await act(async () => {
      latest?.guard.discard()
    })
    expect(latest?.guard.isDirty()).toBe(false)
    expect(configuredInput.value).toBe('1')
  })

  it('discarding changes clears capacity field drafts', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Availability')
    const configuredInput = findSettingInput(capacitySection, 'Configured Available Generators')
    if (!configuredInput) return

    await setFieldValue(configuredInput, '4')
    expect(configuredInput.value).toBe('4')

    const discardButton = findButton('Discard Changes')
    expect(discardButton).toBeTruthy()
    if (!discardButton) return
    await act(async () => {
      discardButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    // Live pod capacity is only applied through the explicit preset action.
    expect(configuredInput.value).toBe('1')
  })

  it('disables threshold and capacity inputs for sessions without manage_system but keeps subnav browsable', async () => {
    apiMock.getAuthEffectiveAccess.mockResolvedValue({
      enabled: true,
      authenticated: true,
      resources: [{
        id: 'system_configuration',
        name: 'System Configuration',
        description: '',
        operations: [
          { id: 'view_system', label: 'View settings', allowed: true, grant_source: 'global' },
          { id: 'manage_system', label: 'Update system settings', allowed: false },
        ],
      }],
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const errorRateInput = findSettingInput(thresholdsSection, 'Max Error Rate (%)')
    // Fieldset-inherited disabling: the input's own attribute stays false, so
    // assert on the wrapping fieldset.
    expect(errorRateInput?.closest('fieldset')?.disabled).toBe(true)

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Generator Profile')
    const cpuInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    expect(cpuInput?.closest('fieldset')?.disabled).toBe(true)
  })

  it('fails closed immediately while effective access for a new scope is loading', async () => {
    let resolveSecondScope: ((value: {
      enabled: boolean
      authenticated: boolean
      resources: DashboardAuthEffectiveAccessResource[]
    }) => void) | undefined
    apiMock.getAuthEffectiveAccess.mockImplementation((
      _projectId?: string,
      options?: { application_repository_id?: string },
    ) => {
      if (options?.application_repository_id === 'repo-2') {
        return new Promise(resolve => {
          resolveSecondScope = resolve
        })
      }
      return Promise.resolve({
        enabled: true,
        authenticated: true,
        resources: [{
          id: 'system_configuration',
          name: 'System Configuration',
          description: '',
          operations: [{ id: 'manage_system', label: 'Update system settings', allowed: true }],
        }],
      })
    })
    const firstRepository = {
      id: 'repo-1', domain_id: 'domain-1', name: 'Repository', created_at: '', updated_at: '',
    }
    const secondRepository = { ...firstRepository, id: 'repo-2' }

    await act(async () => {
      renderWithSettingsTopbar(root, (
        <SettingsPage theme="dark" onThemeChange={vi.fn()} applicationRepository={firstRepository} />
      ))
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Performance Thresholds')
    let errorRateInput = findSettingInput(container, 'Max Error Rate (%)')
    expect(errorRateInput?.closest('fieldset')?.disabled).toBe(false)
    if (!errorRateInput) return
    await setFieldValue(errorRateInput, '5')
    expect(findButton('Save pending settings')?.disabled).toBe(false)
    expect(findButton('Reset All Settings to Defaults')?.disabled).toBe(false)

    await act(async () => {
      renderWithSettingsTopbar(root, (
        <SettingsPage theme="dark" onThemeChange={vi.fn()} applicationRepository={secondRepository} />
      ))
    })

    errorRateInput = findSettingInput(container, 'Max Error Rate (%)')
    expect(errorRateInput?.closest('fieldset')?.disabled).toBe(true)
    expect(findButton('Save pending settings')?.disabled).toBe(true)
    expect(findButton('Reset All Settings to Defaults')?.disabled).toBe(true)
    expect(apiMock.updateSystemSettings).not.toHaveBeenCalled()

    await act(async () => {
      resolveSecondScope?.({ enabled: true, authenticated: true, resources: [] })
      await Promise.resolve()
    })
  })

  it('keeps the form disabled and offers Retry when settings fail to load', async () => {
    apiMock.getSystemSettings.mockRejectedValueOnce(new Error('boom'))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const retryButton = findButton('Retry Loading Settings')
    expect(retryButton).toBeTruthy()
    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const errorRateInput = findSettingInput(thresholdsSection, 'Max Error Rate (%)')
    expect(errorRateInput?.closest('fieldset')?.disabled).toBe(true)

    if (!retryButton) return
    await act(async () => {
      retryButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(findButton('Retry Loading Settings')).toBeUndefined()
    expect(errorRateInput?.closest('fieldset')?.disabled).toBe(false)
  })

  it('ignores an older settings load after the API client changes', async () => {
    const baseline = await apiMock.getSystemSettings()
    let resolveOlder: ((value: typeof baseline) => void) | undefined
    apiMock.getSystemSettings.mockImplementationOnce(() => new Promise<typeof baseline>(resolve => {
      resolveOlder = resolve
    }))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    apiProviderState.current = {
      ...apiMock,
      getSystemSettings: vi.fn().mockResolvedValue({
        ...baseline,
        general: {
          ...baseline.general,
          api_endpoint: ':9090',
          max_concurrent_tests: 22,
          metrics_interval_seconds: 2,
          usage_snapshot_interval_seconds: 120,
        },
      }),
    }
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await act(async () => {
      resolveOlder?.({
        ...baseline,
        general: {
          ...baseline.general,
          api_endpoint: ':7070',
          max_concurrent_tests: 7,
          metrics_interval_seconds: 7,
          usage_snapshot_interval_seconds: 700,
        },
      })
    })
    await flushPromises()

    const maxConcurrent = findSettingInput(
      container.querySelector('#settings-section-general') ?? container,
      'Max Concurrent Tests',
    )
    expect(maxConcurrent?.value).toBe('22')
  })

  it('registers a dirty navigation guard for unsaved settings edits', async () => {
    const registered: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const registrar = (guard: NavigationGuard, dirty: boolean) => {
      registered.push({ guard, dirty })
      return () => {}
    }

    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={registrar}>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flushPromises()

    const settingsGuards = registered.filter(entry => entry.guard.id === 'system-settings')
    expect(settingsGuards.length).toBeGreaterThan(0)
    expect(settingsGuards[settingsGuards.length - 1].guard.isDirty()).toBe(false)

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const p99Input = findSettingInput(thresholdsSection, 'Max Response Time (P99 ms)')
    if (!p99Input) return
    await setFieldValue(p99Input, '1500')

    const latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    expect(latest?.dirty).toBe(true)
    expect(latest?.guard.isDirty()).toBe(true)

    await act(async () => {
      latest?.guard.discard()
    })
    expect(latest?.guard.isDirty()).toBe(false)
  })

  it('includes domain drafts in the page navigation guard and discards them to the saved value', async () => {
    const registered: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const registrar = (guard: NavigationGuard, dirty: boolean) => {
      registered.push({ guard, dirty })
      return () => {}
    }

    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={registrar}>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPageDomainHarness />
          </TopbarContributionProvider>
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    const selectedRow = Array.from(domainSection?.querySelectorAll('.setting-row') || []).find(row => row.textContent?.includes('Selected Domain'))
    const nameInput = selectedRow?.querySelector('input') as HTMLInputElement | null
    expect(nameInput).toBeTruthy()
    if (!nameInput) return

    await setFieldValue(nameInput, 'Unsaved Default Domain')
    const latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    expect(latest?.dirty).toBe(true)
    expect(latest?.guard.isDirty()).toBe(true)

    await act(async () => {
      latest?.guard.discard()
    })
    expect(nameInput.value).toBe(defaultDomain.name)
    expect(latest?.guard.isDirty()).toBe(false)
  })

  it('guards creation drafts and commits them to the scope where editing began', async () => {
    const registered: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const registrar = (guard: NavigationGuard, dirty: boolean) => {
      registered.push({ guard, dirty })
      return () => {}
    }
    const retailDomain: Domain = { id: 'retail', name: 'Retail', created_at: '', updated_at: '' }
    const createdProject = { id: 'project-new', name: 'New Project', domain_id: defaultDomain.id, created_at: '', updated_at: '' }
    applicationContextMock.createAndSelectApplication.mockResolvedValue({
      id: 'app-new',
      project_id: 'project-1',
      name: 'New Application',
      created_at: '',
      updated_at: '',
    })
    projectContextMock.createAndSelectProject.mockResolvedValue(createdProject)
    apiMock.createDataPool.mockResolvedValue({
      id: 'pool-new',
      project_id: 'project-1',
      name: 'Customers',
      description: '',
      created_at: '',
      updated_at: '',
    })

    const renderPage = (activeDomain: Domain) => root.render(
      <NavigationGuardRegisterContext.Provider value={registrar}>
        <TopbarContributionProvider>
          <SettingsTopbarHarness />
          <SettingsPage
            theme="dark"
            onThemeChange={vi.fn()}
            domains={[defaultDomain, retailDomain]}
            activeDomain={activeDomain}
          />
        </TopbarContributionProvider>
      </NavigationGuardRegisterContext.Provider>,
    )

    await act(async () => renderPage(defaultDomain))
    await flushPromises()
    const applicationInput = container.querySelector<HTMLInputElement>('input[placeholder="Create application, e.g. ACC"]')
    const projectInput = container.querySelector<HTMLInputElement>('input[placeholder="Create project, e.g. Customer Portal"]')
    const poolInput = container.querySelector<HTMLInputElement>('input[placeholder="Pool name, e.g. Customer Accounts"]')
    expect(applicationInput && projectInput && poolInput).toBeTruthy()
    if (!applicationInput || !projectInput || !poolInput) return
    await setFieldValue(applicationInput, 'New Application')
    await setFieldValue(projectInput, 'New Project')
    await setFieldValue(poolInput, 'Customers')

    let latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    expect(latest?.dirty).toBe(true)
    expect(latest?.guard.isDirty()).toBe(true)

    projectContextMock.activeProject = { id: 'project-2', name: 'Retail Project', created_at: '', updated_at: '' }
    await act(async () => renderPage(retailDomain))
    await flushPromises()
    latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    await act(async () => {
      expect(await latest?.guard.save()).toBe(true)
    })
    await flushPromises()

    expect(applicationContextMock.createAndSelectApplication).toHaveBeenCalledWith('New Application', 'project-1')
    expect(apiMock.createDataPool).toHaveBeenCalledWith(expect.objectContaining({ name: 'Customers', project_id: 'project-1' }))
    expect(projectContextMock.createAndSelectProject).toHaveBeenCalledWith({ name: 'New Project', domain_id: defaultDomain.id })
    expect(latest?.guard.isDirty()).toBe(false)
  })

  it('guards a domain switch and applies the pending selection only after discard', async () => {
    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPageDomainHarness />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    if (!domainSection) return
    const retailButton = Array.from(domainSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes('Retail')) as HTMLButtonElement | undefined
    const defaultButton = Array.from(domainSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes(defaultDomain.name)) as HTMLButtonElement | undefined
    expect(retailButton && defaultButton).toBeTruthy()
    if (!retailButton || !defaultButton) return
    await act(async () => retailButton.click())

    const selectedRow = Array.from(domainSection.querySelectorAll('.setting-row')).find(row => row.textContent?.includes('Selected Domain'))
    const nameInput = selectedRow?.querySelector('input') as HTMLInputElement | null
    if (!nameInput) return
    await setFieldValue(nameInput, 'Retail draft')
    await act(async () => defaultButton.click())

    expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
    expect(domainSection.querySelector('.settings-app-card.active')?.textContent).toContain('Retail')
    const discard = Array.from(document.querySelectorAll('.unsaved-changes-modal button')).find(button => button.textContent === 'Discard changes') as HTMLButtonElement | undefined
    await act(async () => discard?.click())
    expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
    expect(domainSection.querySelector('.settings-app-card.active')?.textContent).toContain(defaultDomain.name)
    expect(nameInput.value).toBe(defaultDomain.name)
  })

  it('saves a project draft before completing a guarded project switch', async () => {
    const secondProject = {
      id: 'project-2',
      name: 'Payments',
      settings: { redaction_rules: [], max_vusers: 50 },
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-15T00:00:00Z',
    }
    seedProjectCatalog([
        {
          id: 'project-1',
          name: 'Customer Portal',
          settings: { redaction_rules: [], max_vusers: 250 },
          created_at: '2026-05-15T00:00:00Z',
          updated_at: '2026-05-15T00:00:00Z',
        },
        secondProject,
      ])
    projectApiMock.updateProject.mockImplementation(async (id: string, data: { name: string; settings?: ProjectSettings }) => ({
      id,
      name: data.name,
      settings: data.settings,
      created_at: '2026-05-15T00:00:00Z',
      updated_at: '2026-05-16T00:00:00Z',
    }))

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    if (!projectSection) return
    await selectSettingsSubnav(projectSection, 'Limits')
    const nameInput = findSettingInput(projectSection, 'Selected Project')
    expect(nameInput).toBeTruthy()
    if (!nameInput) return
    await setFieldValue(nameInput, 'Customer Portal Renamed')

    const paymentsButton = Array.from(projectSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes('Payments')) as HTMLButtonElement | undefined
    await act(async () => paymentsButton?.click())
    expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
    const save = Array.from(document.querySelectorAll('.unsaved-changes-modal button')).find(button => button.textContent === 'Save changes') as HTMLButtonElement | undefined
    await act(async () => save?.click())
    await flushPromises()

    expect(projectApiMock.updateProject).toHaveBeenCalledWith('project-1', expect.objectContaining({ name: 'Customer Portal Renamed' }))
    expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
    expect(projectSection.querySelector('.settings-app-card.active')?.textContent).toContain('Payments')
    // The catalog cursor and the workspace project are one selection: every
    // other surface reads the context, not this tab's local highlight.
    expect(projectContextMock.setActiveProject).toHaveBeenCalledWith(expect.objectContaining({ id: 'project-2' }))
  })

  it('promotes a project card click to the workspace project scope', async () => {
    seedProjectCatalog([
      { id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' },
      { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' },
    ])

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    if (!projectSection) return

    const payments = Array.from(projectSection.querySelectorAll('.settings-app-card'))
      .find(button => button.textContent?.includes('Payments')) as HTMLButtonElement | undefined
    await act(async () => payments?.click())

    expect(projectContextMock.setActiveProject).toHaveBeenCalledWith(expect.objectContaining({ id: 'project-2' }))
  })

  it('keeps a manual project selection when the catalog refreshes without a workspace project', async () => {
    const seed = () => [
      { id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' },
      { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' },
    ]
    seedProjectCatalog(seed())
    projectContextMock.activeProject = null

    const renderPage = () => root.render(
      <NavigationGuardProvider>
        <TopbarContributionProvider>
          <SettingsTopbarHarness />
          <SettingsPage theme="dark" onThemeChange={vi.fn()} />
        </TopbarContributionProvider>
      </NavigationGuardProvider>,
    )
    await act(async () => renderPage())
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    if (!projectSection) return

    const payments = Array.from(projectSection.querySelectorAll('.settings-app-card'))
      .find(button => button.textContent?.includes('Payments')) as HTMLButtonElement | undefined
    await act(async () => payments?.click())
    expect(projectSection.querySelector('.settings-app-card.active')?.textContent).toContain('Payments')

    // A background catalog refresh delivers a new array identity. Without the
    // manual-pick fallback the cursor snaps back to the first project.
    seedProjectCatalog(seed())
    await act(async () => renderPage())
    await flushPromises()

    expect(projectSection.querySelector('.settings-app-card.active')?.textContent).toContain('Payments')
  })

  it('creates an application in the project chosen on the create row', async () => {
    seedProjectCatalog([
      { id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' },
      { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' },
    ])
    applicationContextMock.createAndSelectApplication.mockResolvedValue({
      id: 'app-new', project_id: 'project-2', name: 'Wallet', created_at: '', updated_at: '',
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    if (!applicationSection) return

    const nameInput = applicationSection.querySelector<HTMLInputElement>('input[placeholder="Create application, e.g. ACC"]')
    const projectSelect = applicationSection.querySelector<HTMLSelectElement>('select[aria-label="Project for the new application"]')
    expect(nameInput && projectSelect).toBeTruthy()
    if (!nameInput || !projectSelect) return
    expect(projectSelect.value).toBe('project-1')
    expect(Array.from(projectSelect.options).map(option => option.textContent)).toEqual(['Customer Portal', 'Payments'])

    await setFieldValue(nameInput, 'Wallet')
    await setFieldValue(projectSelect, 'project-2')
    const addButton = Array.from(applicationSection.querySelectorAll('button'))
      .find(button => button.textContent === 'Add Application') as HTMLButtonElement | undefined
    await act(async () => addButton?.click())
    await flushPromises()

    expect(applicationContextMock.createAndSelectApplication).toHaveBeenCalledWith('Wallet', 'project-2')
  })

  it('routes to project creation instead of offering an application in an empty domain', async () => {
    seedProjectCatalog([])
    projectContextMock.activeProject = null

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage
              theme="dark"
              onThemeChange={vi.fn()}
              domains={[retailDomain]}
              activeDomain={retailDomain}
            />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    if (!applicationSection) return

    expect(applicationSection.querySelector('input[placeholder="Create application, e.g. ACC"]')).toBeNull()
    expect(applicationSection.textContent).toContain('No projects in Retail yet')
    const cta = Array.from(applicationSection.querySelectorAll('button'))
      .find(button => button.textContent === 'Create a project') as HTMLButtonElement | undefined
    expect(cta).toBeDefined()
    await act(async () => cta?.click())

    expect(container.querySelector('#settings-section-projects')?.hasAttribute('hidden')).toBe(false)
  })

  it('creates a domain without a spurious unsaved-changes prompt when the refresh takes real time', async () => {
    repositoryApiMock.createDomain.mockResolvedValue({
      domain: retailDomain,
      application_repository: { id: 'repo-r', domain_id: retailDomain.id, name: 'Application Repository', created_at: '', updated_at: '' },
      default_category: { id: 'cat-r', domain_id: retailDomain.id, repository_id: 'repo-r', name: 'Uncategorized', created_at: '', updated_at: '' },
    })
    // Stale warning from an earlier failed refresh: a successful refresh must
    // retire the crumb too, or a remount would resurrect it.
    window.sessionStorage.setItem('apexload.settings.domainWarning', JSON.stringify({
      at: Date.now(),
      value: { message: 'stale warning' },
    }))

    // The created domain joins the catalog only when the refresh resolves —
    // one macrotask later, so React renders during the flight exactly as it
    // does against a real network.
    function DeferredRefreshHarness() {
      const [domains, setDomains] = useState<Domain[]>([defaultDomain])
      const [active, setActive] = useState<Domain>(defaultDomain)
      const { runGuarded } = useNavigationGuardRunner()
      return (
        <SettingsPage
          theme="dark"
          onThemeChange={vi.fn()}
          domains={domains}
          activeDomain={active}
          onDomainChange={domain => {
            if (domain.id === active.id) {
              setActive(domain)
              return
            }
            runGuarded(() => setActive(domain))
          }}
          onDomainsRefresh={async () => {
            await new Promise(resolve => setTimeout(resolve, 0))
            setDomains([defaultDomain, retailDomain])
            return [defaultDomain, retailDomain]
          }}
        />
      )
    }

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <DeferredRefreshHarness />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    const nameInput = domainSection?.querySelector<HTMLInputElement>('input[placeholder="Create domain, e.g. Retail"]')
    expect(nameInput).toBeTruthy()
    if (!nameInput || !domainSection) return
    await setFieldValue(nameInput, 'Retail')
    const addButton = Array.from(domainSection.querySelectorAll('button'))
      .find(button => button.textContent === 'Add Domain') as HTMLButtonElement | undefined
    await act(async () => addButton?.click())
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 0))
    })
    await flushPromises()

    expect(document.querySelector('.unsaved-changes-modal')).toBeNull()
    expect(domainSection.querySelector('.settings-app-card.active')?.textContent).toContain('Retail')
    expect(window.sessionStorage.getItem('apexload.settings.domainWarning')).toBeNull()
  })

  it('writes the do-not-retry warning breadcrumb before the switch that remounts the page', async () => {
    repositoryApiMock.createDomain.mockResolvedValue({
      domain: retailDomain,
      application_repository: { id: 'repo-r', domain_id: retailDomain.id, name: 'Application Repository', created_at: '', updated_at: '' },
      default_category: { id: 'cat-r', domain_id: retailDomain.id, repository_id: 'repo-r', name: 'Uncategorized', created_at: '', updated_at: '' },
    })
    const onDomainsRefresh = vi.fn().mockRejectedValue(new Error('list unavailable'))
    // Captures what a remounting instance would see at switch time.
    const crumbsAtSwitch: Record<string, string | null> = {}
    const onDomainChange = vi.fn().mockImplementation(() => {
      crumbsAtSwitch.warning = window.sessionStorage.getItem('apexload.settings.domainWarning')
      crumbsAtSwitch.message = window.sessionStorage.getItem('apexload.settings.domainMessage')
    })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage
              theme="dark"
              onThemeChange={vi.fn()}
              domains={[defaultDomain]}
              activeDomain={defaultDomain}
              onDomainChange={onDomainChange}
              onDomainsRefresh={onDomainsRefresh}
            />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    const nameInput = domainSection?.querySelector<HTMLInputElement>('input[placeholder="Create domain, e.g. Retail"]')
    expect(nameInput).toBeTruthy()
    if (!nameInput || !domainSection) return
    await setFieldValue(nameInput, 'Retail')
    const addButton = Array.from(domainSection.querySelectorAll('button'))
      .find(button => button.textContent === 'Add Domain') as HTMLButtonElement | undefined
    await act(async () => addButton?.click())
    await flushPromises()

    expect(onDomainChange).toHaveBeenCalledWith(retailDomain)
    // The reconciliation ran and failed BEFORE the switch, so the remounting
    // instance can read both outcomes.
    expect(crumbsAtSwitch.warning).toBeTruthy()
    expect(crumbsAtSwitch.message).toBeTruthy()
    expect(onDomainsRefresh).toHaveBeenCalled()
  })

  it('retiring the reconciliation warning by retry also clears its breadcrumb', async () => {
    window.sessionStorage.setItem('apexload.settings.domainWarning', JSON.stringify({
      at: Date.now(),
      value: { message: 'Domain "Retail" was created, but the domain list could not be refreshed. The create succeeded—do not create it again. Retry synchronization below.', preferredId: retailDomain.id },
    }))
    const onDomainsRefresh = vi.fn().mockResolvedValue([defaultDomain, retailDomain])

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage
              theme="dark"
              onThemeChange={vi.fn()}
              domains={[defaultDomain, retailDomain]}
              activeDomain={retailDomain}
              onDomainsRefresh={onDomainsRefresh}
            />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')
    const domainSection = container.querySelector('#settings-section-domains')
    expect(domainSection?.textContent).toContain('The create succeeded—do not create it again.')

    const retry = Array.from(domainSection?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Retry') as HTMLButtonElement | undefined
    expect(retry).toBeDefined()
    await act(async () => retry?.click())
    await flushPromises()

    expect(domainSection?.textContent).not.toContain('The create succeeded—do not create it again.')
    expect(window.sessionStorage.getItem('apexload.settings.domainWarning')).toBeNull()
  })

  it('surfaces a data-pool breadcrumb left by a create that preceded a remount', async () => {
    seedProjectCatalog([{ id: 'project-2', name: 'Payments', created_at: '', updated_at: '' }])
    projectContextMock.activeProject = { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' }
    window.sessionStorage.setItem('apexload.settings.dataPoolNotice', JSON.stringify({
      at: Date.now(),
      value: 'Created data pool "Orders" in project "Customer Portal". Switch to that project to manage it.',
    }))

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Data Pools')

    expect(container.querySelector('#settings-section-data-pools')?.textContent)
      .toContain('Created data pool "Orders" in project "Customer Portal". Switch to that project to manage it.')
    // The crumb is peeked, not consumed: the remount chain a scope switch
    // causes must be able to re-read it until the freshness bound retires it.
    expect(window.sessionStorage.getItem('apexload.settings.dataPoolNotice')).not.toBeNull()
  })

  it('surfaces a domain outcome breadcrumb after the switch-driven remount', async () => {
    window.sessionStorage.setItem('apexload.settings.domainMessage', JSON.stringify({
      at: Date.now(),
      value: 'Domain "Retail" created.',
    }))
    window.sessionStorage.setItem('apexload.settings.domainWarning', JSON.stringify({
      at: Date.now(),
      value: { message: 'Domain "Retail" was created, but the domain list could not be refreshed. The create succeeded—do not create it again. Retry synchronization below.' },
    }))

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage
              theme="dark"
              onThemeChange={vi.fn()}
              domains={[defaultDomain, retailDomain]}
              activeDomain={retailDomain}
            />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Domains')

    // The outcome message surfaces through the section-message aggregation,
    // the do-not-retry warning renders inline in the domain section.
    expect(container.textContent).toContain('Domain "Retail" created.')
    expect(container.querySelector('#settings-section-domains')?.textContent)
      .toContain('The create succeeded—do not create it again.')
  })

  it('ignores a double-click on Add Application while the create is in flight', async () => {
    seedProjectCatalog([{ id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }])
    let resolveCreate: (value: Application) => void = () => {}
    applicationContextMock.createAndSelectApplication.mockImplementation(
      () => new Promise<Application>(resolve => { resolveCreate = resolve }),
    )

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const nameInput = applicationSection?.querySelector<HTMLInputElement>('input[placeholder="Create application, e.g. ACC"]')
    if (!applicationSection || !nameInput) return
    await setFieldValue(nameInput, 'Wallet')

    const addButton = Array.from(applicationSection.querySelectorAll('button'))
      .find(button => button.textContent === 'Add Application') as HTMLButtonElement | undefined
    await act(async () => addButton?.click())
    await act(async () => addButton?.click())
    expect(applicationContextMock.createAndSelectApplication).toHaveBeenCalledTimes(1)
    expect(addButton?.disabled).toBe(true)

    await act(async () => {
      resolveCreate({ id: 'app-new', project_id: 'project-1', name: 'Wallet', created_at: '', updated_at: '' })
    })
    await flushPromises()
    expect(applicationContextMock.createAndSelectApplication).toHaveBeenCalledTimes(1)
  })

  it('asks for a project instead of offering to create one when the domain has projects but none is active', async () => {
    seedProjectCatalog([{ id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }])
    projectContextMock.activeProject = null

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Data Pools')
    const poolSection = container.querySelector('#settings-section-data-pools')

    expect(poolSection?.textContent).toContain('Select a project to create data pools.')
    expect(poolSection?.textContent).not.toContain('No projects in')
  })

  it('names the project a new data pool lands in', async () => {
    seedProjectCatalog([{ id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }])
    projectContextMock.activeProject = { id: 'project-1', name: 'Customer Portal', created_at: '', updated_at: '' }

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Data Pools')
    const poolSection = container.querySelector('#settings-section-data-pools')

    expect(poolSection?.textContent).toContain('Creates in project "Customer Portal".')
  })

  it('guards an edited pool quota before switching the selected project', async () => {
    const firstProject = {
      id: 'project-1',
      name: 'Customer Portal',
      settings: { pool_quotas: { 'pool-east': { max_vusers: 100 } } },
      created_at: '',
      updated_at: '',
    }
    const secondProject = { id: 'project-2', name: 'Payments', settings: {}, created_at: '', updated_at: '' }
    seedProjectCatalog([firstProject, secondProject])
    fleetApiMock.listFleetPools.mockResolvedValue({ fleet_pools: [{ id: 'pool-east', name: 'East', status: 'active' }], count: 1 })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Projects & Compliance')
    const projectSection = container.querySelector('#settings-section-projects')
    if (!projectSection) return
    await selectSettingsSubnav(projectSection, 'Limits')
    const quotaInput = projectSection.querySelector<HTMLInputElement>('.pp-row input[type="number"]')
    if (!quotaInput) return
    await setFieldValue(quotaInput, '250')

    const payments = Array.from(projectSection.querySelectorAll('.settings-app-card'))
      .find(button => button.textContent?.includes('Payments')) as HTMLButtonElement | undefined
    await act(async () => payments?.click())
    expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
    expect(projectSection.querySelector('.settings-app-card.active')?.textContent).toContain('Customer Portal')

    const discard = Array.from(document.querySelectorAll('.unsaved-changes-modal button'))
      .find(button => button.textContent === 'Discard changes') as HTMLButtonElement | undefined
    await act(async () => discard?.click())
    expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
    expect(projectSection.querySelector('.settings-app-card.active')?.textContent).toContain('Payments')
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
    expect(projectContextMock.setActiveProject).toHaveBeenCalledWith(expect.objectContaining({ id: 'project-2' }))
  })

  it('saves a release rename in its originating application before completing a guarded application switch', async () => {
    const applicationA = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const applicationB = { id: 'app-b', project_id: 'project-1', name: 'Payments', created_at: '', updated_at: '' }
    const releaseA = { id: 'release-a', application_id: applicationA.id, name: '1.0', created_at: '', updated_at: '' }
    const releaseB = { id: 'release-b', application_id: applicationB.id, name: '2.0', created_at: '', updated_at: '' }
    const renamedReleaseA = { ...releaseA, name: '1.0 Renamed' }
    let renameCommitted = false
    Object.assign(applicationContextMock, {
      applications: [applicationA, applicationB],
      activeApplication: applicationA,
      releases: [releaseA],
      activeRelease: releaseA,
    })
    applicationApiMock.updateRelease.mockImplementation(async () => {
      renameCommitted = true
      return renamedReleaseA
    })
    applicationApiMock.listReleases.mockImplementation(async (applicationId: string) => ({
      releases: applicationId === applicationA.id
        ? [renameCommitted ? renamedReleaseA : releaseA]
        : [releaseB],
    }))

    const renderPage = () => root.render(
      <NavigationGuardProvider>
        <TopbarContributionProvider>
          <SettingsTopbarHarness />
          <SettingsPage theme="dark" onThemeChange={vi.fn()} />
        </TopbarContributionProvider>
      </NavigationGuardProvider>,
    )
    await act(async () => renderPage())
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const releaseRow = applicationSection?.querySelector('.settings-release-row')
    const rename = Array.from(releaseRow?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Edit') as HTMLButtonElement | undefined
    await act(async () => rename?.click())
    const renameInput = releaseRow?.querySelector('input') as HTMLInputElement | null
    expect(renameInput).toBeTruthy()
    if (!renameInput || !applicationSection) return
    await setFieldValue(renameInput, renamedReleaseA.name)

    const payments = Array.from(applicationSection.querySelectorAll('.settings-app-card'))
      .find(button => button.textContent?.includes(applicationB.name)) as HTMLButtonElement | undefined
    await act(async () => payments?.click())
    expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
    expect(applicationContextMock.setActiveApplication).not.toHaveBeenCalled()

    // Simulate another application-scope source changing while the guarded
    // transition is pending. The hidden draft must remain bound to app A.
    Object.assign(applicationContextMock, {
      activeApplication: applicationB,
      releases: [releaseB],
      activeRelease: releaseB,
    })
    await act(async () => renderPage())
    await flushPromises()
    const save = Array.from(document.querySelectorAll('.unsaved-changes-modal button'))
      .find(button => button.textContent === 'Save changes') as HTMLButtonElement | undefined
    await act(async () => save?.click())
    await flushPromises()
    await flushPromises()

    expect(applicationApiMock.updateRelease).toHaveBeenCalledWith(releaseA.id, { name: renamedReleaseA.name, updated_at: releaseA.updated_at })
    expect(applicationContextMock.refreshReleases).toHaveBeenCalledWith(applicationA.id, { preserveRelease: renamedReleaseA })
    expect(applicationApiMock.listReleases).toHaveBeenCalledWith(applicationA.id, true)
    expect(applicationContextMock.setActiveApplication).toHaveBeenCalledWith(applicationB)
    expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
    expect(applicationSection.textContent).toContain(releaseB.name)
    expect(applicationSection.textContent).not.toContain(renamedReleaseA.name)
  })

  it('hides the previous applications releases while the next catalog is loading', async () => {
    const applicationA = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const applicationB = { id: 'app-b', project_id: 'project-1', name: 'Payments', created_at: '', updated_at: '' }
    const releaseA = { id: 'release-a', application_id: applicationA.id, name: 'App A Only', created_at: '', updated_at: '' }
    Object.assign(applicationContextMock, {
      applications: [applicationA, applicationB],
      activeApplication: applicationA,
      releases: [releaseA],
      activeRelease: releaseA,
    })
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [releaseA] })
      .mockImplementationOnce(() => new Promise(() => undefined))

    const renderPage = () => root.render(
      <NavigationGuardProvider>
        <TopbarContributionProvider>
          <SettingsTopbarHarness />
          <SettingsPage theme="dark" onThemeChange={vi.fn()} />
        </TopbarContributionProvider>
      </NavigationGuardProvider>,
    )
    await act(async () => renderPage())
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    expect(applicationSection?.textContent).toContain(releaseA.name)

    Object.assign(applicationContextMock, {
      activeApplication: applicationB,
      releases: [],
      activeRelease: null,
    })
    await act(async () => renderPage())

    expect(applicationSection?.textContent).not.toContain(releaseA.name)
    expect(applicationSection?.querySelectorAll('.settings-release-row')).toHaveLength(0)
  })

  it('discards a release rename before completing a guarded application switch', async () => {
    const applicationA = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const applicationB = { id: 'app-b', project_id: 'project-1', name: 'Payments', created_at: '', updated_at: '' }
    const releaseA = { id: 'release-a', application_id: applicationA.id, name: '1.0', created_at: '', updated_at: '' }
    Object.assign(applicationContextMock, {
      applications: [applicationA, applicationB],
      activeApplication: applicationA,
      releases: [releaseA],
      activeRelease: releaseA,
    })
    applicationApiMock.listReleases.mockResolvedValue({ releases: [releaseA] })

    await act(async () => {
      root.render(
        <NavigationGuardProvider>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardProvider>,
      )
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const releaseRow = applicationSection?.querySelector('.settings-release-row')
    const rename = Array.from(releaseRow?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Edit') as HTMLButtonElement | undefined
    await act(async () => rename?.click())
    const renameInput = releaseRow?.querySelector('input') as HTMLInputElement | null
    expect(renameInput).toBeTruthy()
    if (!renameInput || !applicationSection) return
    await setFieldValue(renameInput, '1.0 Draft')

    const payments = Array.from(applicationSection.querySelectorAll('.settings-app-card'))
      .find(button => button.textContent?.includes(applicationB.name)) as HTMLButtonElement | undefined
    await act(async () => payments?.click())
    expect(document.querySelector('.unsaved-changes-modal')).toBeTruthy()
    expect(applicationContextMock.setActiveApplication).not.toHaveBeenCalled()
    const discard = Array.from(document.querySelectorAll('.unsaved-changes-modal button'))
      .find(button => button.textContent === 'Discard changes') as HTMLButtonElement | undefined
    await act(async () => discard?.click())

    expect(applicationApiMock.updateRelease).not.toHaveBeenCalled()
    expect(applicationContextMock.setActiveApplication).toHaveBeenCalledWith(applicationB)
    expect(document.querySelector('.unsaved-changes-modal')).toBeFalsy()
    expect(releaseRow?.querySelector('input')).toBeNull()
    expect(releaseRow?.textContent).toContain(releaseA.name)
  })

  it('locks an application rename to the application that started the edit', async () => {
    const applicationA = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const applicationB = { id: 'app-b', project_id: 'project-1', name: 'Payments', created_at: '', updated_at: '' }
    const renamedA = { ...applicationA, name: 'Checkout Renamed' }
    Object.assign(applicationContextMock, {
      applications: [applicationA, applicationB],
      activeApplication: applicationA,
    })
    applicationApiMock.updateApplication.mockResolvedValue(renamedA)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    if (!applicationSection) return
    await act(async () => findButton('Edit')?.click())

    const currentRow = Array.from(applicationSection.querySelectorAll('.setting-row')).find(row => row.textContent?.includes('Current Application'))
    const renameInput = currentRow?.querySelector('input') as HTMLInputElement | null
    expect(renameInput).toBeTruthy()
    if (!renameInput) return
    const paymentsButton = Array.from(applicationSection.querySelectorAll('.settings-app-card')).find(button => button.textContent?.includes('Payments')) as HTMLButtonElement | undefined
    expect(paymentsButton?.disabled).toBe(true)

    Object.assign(applicationContextMock, { activeApplication: applicationB })
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await setFieldValue(renameInput, 'Checkout Renamed')
    const save = Array.from(currentRow?.querySelectorAll('button') || []).find(button => button.textContent === 'Save') as HTMLButtonElement | undefined
    await act(async () => save?.click())
    await flushPromises()

    expect(applicationApiMock.updateApplication).toHaveBeenCalledWith('app-a', { name: 'Checkout Renamed', updated_at: applicationA.updated_at })
    expect(applicationContextMock.setActiveApplication).toHaveBeenCalledWith(renamedA)
  })

  it('keeps a committed application rename successful when list reconciliation fails', async () => {
    const original = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const updated = { ...original, name: 'Checkout Renamed' }
    Object.assign(applicationContextMock, {
      applications: [original],
      activeApplication: original,
    })
    applicationApiMock.updateApplication.mockResolvedValue(updated)
    applicationContextMock.refreshApplications.mockRejectedValueOnce(new Error('list unavailable'))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    expect(applicationSection).toBeTruthy()
    if (!applicationSection) return

    await act(async () => findButton('Edit')?.click())
    const currentRow = Array.from(applicationSection.querySelectorAll('.setting-row')).find(row => row.textContent?.includes('Current Application'))
    const renameInput = currentRow?.querySelector('input') as HTMLInputElement | null
    expect(renameInput).toBeTruthy()
    if (!renameInput) return
    await setFieldValue(renameInput, 'Checkout Renamed')
    const save = Array.from(currentRow?.querySelectorAll('button') || []).find(button => button.textContent === 'Save') as HTMLButtonElement | undefined
    await act(async () => save?.click())
    await flushPromises()

    expect(applicationApiMock.updateApplication).toHaveBeenCalledTimes(1)
    expect(applicationContextMock.setActiveApplication).toHaveBeenCalledWith(updated)
    expect(currentRow?.querySelector('input')).toBeNull()
    expect(applicationSection.textContent).toContain('Application "Checkout Renamed" was renamed, but the application list could not be refreshed.')
    expect(applicationSection.textContent).not.toContain('Unable to rename application')

    const retry = Array.from(applicationSection.querySelectorAll('button')).find(button => button.textContent === 'Retry') as HTMLButtonElement | undefined
    await act(async () => retry?.click())
    await flushPromises()

    expect(applicationContextMock.refreshApplications).toHaveBeenCalledTimes(2)
    expect(applicationApiMock.updateApplication).toHaveBeenCalledTimes(1)
    expect(applicationSection.textContent).not.toContain('was renamed, but the application list could not be refreshed')
  })

  it('does not repeat a committed application delete when list reconciliation fails', async () => {
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
    })
    applicationApiMock.deleteApplication.mockResolvedValue({
      deleted: application.id,
      external_secret_cleanup_required: true,
      external_secret_cleanup_names: ['EXTERNAL_LOGIN'],
      external_secret_cleanup_prefix: 'apexload-p-example-',
    })
    applicationContextMock.refreshApplications.mockRejectedValueOnce(new Error('list unavailable'))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const currentRow = Array.from(applicationSection?.querySelectorAll('.setting-row') || []).find(row => row.textContent?.includes('Current Application'))
    const deleteButton = Array.from(currentRow?.querySelectorAll('button') || []).find(button => button.textContent === 'Delete') as HTMLButtonElement | undefined
    expect(deleteButton).toBeTruthy()
    if (!deleteButton || !applicationSection) return
    await act(async () => deleteButton.click())
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button')).find(button => button.textContent === 'Delete Application') as HTMLButtonElement | undefined
    expect(confirm).toBeTruthy()
    await act(async () => confirm?.click())
    await flushPromises()

    expect(applicationApiMock.deleteApplication).toHaveBeenCalledTimes(1)
    expect(applicationContextMock.removeApplication).toHaveBeenCalledWith(application.id)
    expect(applicationSection.textContent).toContain('The delete succeeded—do not delete again.')
    expect(applicationSection.textContent).toContain('An administrator must remove external secrets: EXTERNAL_LOGIN')
    expect(applicationSection.textContent).not.toContain('Unable to delete application')

    const retry = Array.from(applicationSection.querySelectorAll('button')).find(button => button.textContent === 'Retry') as HTMLButtonElement | undefined
    await act(async () => retry?.click())
    await flushPromises()

    expect(applicationContextMock.refreshApplications).toHaveBeenCalledTimes(2)
    expect(applicationApiMock.deleteApplication).toHaveBeenCalledTimes(1)
  })

  it('keeps a committed release rename visible when release reconciliation fails', async () => {
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const original = { id: 'release-a', application_id: application.id, name: '1.0', created_at: '', updated_at: '' }
    const updated = { ...original, name: '1.0 Renamed' }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
      releases: [original],
      activeRelease: original,
    })
    applicationApiMock.listReleases.mockResolvedValue({ releases: [updated] })
    applicationApiMock.updateRelease.mockResolvedValue(updated)
    applicationContextMock.refreshReleases.mockRejectedValueOnce(new Error('release list unavailable'))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const releaseRow = applicationSection?.querySelector('.settings-release-row')
    const renameButton = Array.from(releaseRow?.querySelectorAll('button') || []).find(button => button.textContent === 'Edit') as HTMLButtonElement | undefined
    expect(renameButton).toBeTruthy()
    if (!renameButton || !releaseRow || !applicationSection) return
    await act(async () => renameButton.click())
    const renameInput = releaseRow.querySelector('input') as HTMLInputElement | null
    expect(renameInput).toBeTruthy()
    if (!renameInput) return
    await setFieldValue(renameInput, updated.name)
    const save = Array.from(releaseRow.querySelectorAll('button')).find(button => button.textContent === 'Save') as HTMLButtonElement | undefined
    await act(async () => save?.click())
    await flushPromises()

    expect(applicationApiMock.updateRelease).toHaveBeenCalledTimes(1)
    expect(releaseRow.querySelector('input')).toBeNull()
    expect(releaseRow.textContent).toContain(updated.name)
    expect(applicationSection.textContent).toContain('The rename succeeded—do not rename again.')
    expect(applicationSection.textContent).not.toContain('Unable to rename release')

    const retry = Array.from(applicationSection.querySelectorAll('button')).find(button => button.textContent === 'Retry') as HTMLButtonElement | undefined
    await act(async () => retry?.click())
    await flushPromises()

    expect(applicationContextMock.refreshReleases).toHaveBeenCalledTimes(2)
    expect(applicationApiMock.updateRelease).toHaveBeenCalledTimes(1)
  })

  it('keeps a committed release rename through a stale successful catalog and accepts a newer snapshot', async () => {
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const original = { id: 'release-a', application_id: application.id, name: '1.0', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const committed = { ...original, name: '1.0 Renamed', updated_at: '2026-05-16T00:00:00Z' }
    const newer = { ...original, name: '1.0 Final', updated_at: '2026-05-17T00:00:00Z' }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
      releases: [original],
      activeRelease: original,
    })
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [original] })
      .mockResolvedValueOnce({ releases: [original] })
      .mockResolvedValueOnce({ releases: [newer] })
    applicationApiMock.updateRelease.mockResolvedValue(committed)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const releaseRow = applicationSection?.querySelector('.settings-release-row')
    const rename = Array.from(releaseRow?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Edit') as HTMLButtonElement | undefined
    await act(async () => rename?.click())
    const renameInput = releaseRow?.querySelector('input') as HTMLInputElement | null
    expect(renameInput).toBeTruthy()
    if (!renameInput || !releaseRow) return
    await setFieldValue(renameInput, committed.name)
    const save = Array.from(releaseRow.querySelectorAll('button'))
      .find(button => button.textContent === 'Save') as HTMLButtonElement | undefined
    await act(async () => save?.click())
    await flushPromises()

    expect(releaseRow.textContent).toContain(committed.name)

    Object.assign(applicationContextMock, { releases: [newer], activeRelease: newer })
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    expect(applicationSection?.querySelector('.settings-release-row')?.textContent).toContain(newer.name)
  })

  it('keeps a deleted release tombstoned when successful reconciliation is stale', async () => {
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const original = { id: 'release-a', application_id: application.id, name: '1.0', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
      releases: [original],
      activeRelease: original,
    })
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [original] })
      .mockResolvedValueOnce({ releases: [original] })
    applicationApiMock.deleteRelease.mockResolvedValue({ deleted: original.id })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const deleteButton = Array.from(applicationSection?.querySelectorAll('.settings-release-row button') || [])
      .find(button => button.textContent === 'Delete') as HTMLButtonElement | undefined
    await act(async () => deleteButton?.click())
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete Release') as HTMLButtonElement | undefined
    await act(async () => confirm?.click())
    await flushPromises()

    expect(applicationApiMock.deleteRelease).toHaveBeenCalledTimes(1)
    expect(applicationContextMock.removeRelease).toHaveBeenCalledWith(original.id)
    expect(applicationSection?.querySelector('.settings-release-row')).toBeNull()
    expect(applicationSection?.textContent).toContain('No releases yet for this application.')
  })

  it('keeps a committed release archive visible when successful reconciliation is stale', async () => {
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const original = { id: 'release-a', application_id: application.id, name: '1.0', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const archived = {
      ...original,
      updated_at: '2026-05-16T00:00:00Z',
      archived_at: '2026-05-16T00:00:00Z',
    }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
      releases: [original],
      activeRelease: original,
    })
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [original] })
      .mockResolvedValueOnce({ releases: [original] })
    applicationApiMock.archiveRelease.mockResolvedValue(archived)

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const archive = Array.from(applicationSection?.querySelectorAll('.settings-release-row button') || [])
      .find(button => button.textContent === 'Archive') as HTMLButtonElement | undefined
    await act(async () => archive?.click())
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Archive Release') as HTMLButtonElement | undefined
    await act(async () => confirm?.click())
    await flushPromises()

    const releaseRow = applicationSection?.querySelector('.settings-release-row')
    expect(applicationApiMock.archiveRelease).toHaveBeenCalledTimes(1)
    expect(releaseRow?.textContent).toContain('Archived')
    expect(Array.from(releaseRow?.querySelectorAll('button') || []).some(button => button.textContent === 'Archive')).toBe(false)
  })

  it('saves an application rename draft through the page navigation guard', async () => {
    const registered: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const registrar = (guard: NavigationGuard, dirty: boolean) => {
      registered.push({ guard, dirty })
      return () => {}
    }
    const original = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const updated = { ...original, name: 'Checkout Renamed', updated_at: '2026-05-16T00:00:00Z' }
    Object.assign(applicationContextMock, { applications: [original], activeApplication: original })
    applicationApiMock.updateApplication.mockResolvedValue(updated)

    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={registrar}>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const currentRow = Array.from(applicationSection?.querySelectorAll('.setting-row') || [])
      .find(row => row.textContent?.includes('Current Application'))
    const rename = Array.from(currentRow?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Edit') as HTMLButtonElement | undefined
    await act(async () => rename?.click())
    const input = currentRow?.querySelector('input') as HTMLInputElement | null
    expect(input).toBeTruthy()
    if (!input) return
    await setFieldValue(input, updated.name)

    const latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    expect(latest?.dirty).toBe(true)
    expect(latest?.guard.isDirty()).toBe(true)
    await act(async () => {
      expect(await latest?.guard.save()).toBe(true)
    })
    await flushPromises()

    expect(applicationApiMock.updateApplication).toHaveBeenCalledWith(original.id, { name: updated.name, updated_at: original.updated_at })
    expect(applicationContextMock.setActiveApplication).toHaveBeenCalledWith(updated)
    expect(currentRow?.querySelector('input')).toBeNull()
    expect(latest?.guard.isDirty()).toBe(false)
  })

  it('discards a release rename draft through the page navigation guard', async () => {
    const registered: Array<{ guard: NavigationGuard; dirty: boolean }> = []
    const registrar = (guard: NavigationGuard, dirty: boolean) => {
      registered.push({ guard, dirty })
      return () => {}
    }
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    const release = { id: 'release-a', application_id: application.id, name: '1.0', created_at: '2026-05-15T00:00:00Z', updated_at: '2026-05-15T00:00:00Z' }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
      releases: [release],
      activeRelease: release,
    })
    applicationApiMock.listReleases.mockResolvedValue({ releases: [release] })

    await act(async () => {
      root.render(
        <NavigationGuardRegisterContext.Provider value={registrar}>
          <TopbarContributionProvider>
            <SettingsTopbarHarness />
            <SettingsPage theme="dark" onThemeChange={vi.fn()} />
          </TopbarContributionProvider>
        </NavigationGuardRegisterContext.Provider>,
      )
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const releaseRow = applicationSection?.querySelector('.settings-release-row')
    const rename = Array.from(releaseRow?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Edit') as HTMLButtonElement | undefined
    await act(async () => rename?.click())
    const input = releaseRow?.querySelector('input') as HTMLInputElement | null
    expect(input).toBeTruthy()
    if (!input) return
    await setFieldValue(input, '1.0 Draft')

    const latest = registered.filter(entry => entry.guard.id === 'system-settings').pop()
    expect(latest?.dirty).toBe(true)
    expect(latest?.guard.isDirty()).toBe(true)
    await act(async () => latest?.guard.discard())

    expect(applicationApiMock.updateRelease).not.toHaveBeenCalled()
    expect(releaseRow?.querySelector('input')).toBeNull()
    expect(releaseRow?.textContent).toContain(release.name)
    expect(latest?.guard.isDirty()).toBe(false)
  })

  it('tombstones an archived release before failed list reconciliation', async () => {
    const application = { id: 'app-a', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' }
    const original = { id: 'release-a', application_id: application.id, name: '1.0', created_at: '', updated_at: '' }
    const archived = { ...original, archived_at: '2026-08-03T12:00:00Z' }
    Object.assign(applicationContextMock, {
      applications: [application],
      activeApplication: application,
      releases: [original],
      activeRelease: original,
    })
    applicationApiMock.listReleases
      .mockResolvedValueOnce({ releases: [original] })
      .mockResolvedValue({ releases: [archived] })
    applicationApiMock.archiveRelease.mockResolvedValue(archived)
    applicationContextMock.refreshReleases.mockRejectedValueOnce(new Error('release list unavailable'))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()
    await selectTopLevelSettingsTab('Applications & Releases')
    const applicationSection = container.querySelector('#settings-section-applications')
    const archive = Array.from(applicationSection?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Archive') as HTMLButtonElement | undefined
    await act(async () => archive?.click())
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Archive Release') as HTMLButtonElement | undefined
    await act(async () => confirm?.click())
    await flushPromises()

    expect(applicationApiMock.archiveRelease).toHaveBeenCalledTimes(1)
    expect(applicationContextMock.removeRelease).toHaveBeenCalledWith(original.id)
    expect(applicationSection?.textContent).toContain('The archive succeeded—do not archive again.')

    const retry = Array.from(applicationSection?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Retry') as HTMLButtonElement | undefined
    await act(async () => retry?.click())
    await flushPromises()
    expect(applicationApiMock.archiveRelease).toHaveBeenCalledTimes(1)
  })

  it('restores a telemetry budget via Reset to default and accepts explicit 0 as disabled', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const sampleAgeInput = findSettingInput(thresholdsSection, 'Max Telemetry Sample Age (ms)')
    expect(sampleAgeInput).toBeTruthy()
    if (!sampleAgeInput) return

    await setFieldValue(sampleAgeInput, '900')
    const resetButton = Array.from(thresholdsSection.querySelectorAll('button')).find(button => button.textContent?.includes('Reset to default (2000 ms)'))
    expect(resetButton).toBeTruthy()
    if (!resetButton) return
    await act(async () => {
      resetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(sampleAgeInput.value).toBe('2000')

    await setFieldValue(sampleAgeInput, '')
    expect(thresholdsSection.textContent).toContain('Max telemetry sample age is always enforced.')
    const saveButton = findButton('Save pending settings') || findButton('No pending settings')
    expect(saveButton?.disabled).toBe(true)

    await setFieldValue(sampleAgeInput, '0')
    expect(thresholdsSection.textContent).not.toContain('Max telemetry sample age is always enforced.')
  })

  it('allows 0 P99 stop samples as the documented alert-only setting', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const samplesInput = findSettingInput(thresholdsSection, 'P99 Stop After Breached Samples')
    expect(samplesInput?.min).toBe('0')
    if (!samplesInput) return

    await setFieldValue(samplesInput, '0')
    expect(thresholdsSection.textContent).not.toContain('P99 stop samples must be an integer')
  })

  it('shows the fleet ordering error under the saturated slots field', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Performance Thresholds')
    const thresholdsSection = container.querySelector('#settings-section-thresholds')
    if (!thresholdsSection) return
    const constrainedInput = findSettingInput(thresholdsSection, 'Fleet Constrained Free Slots')
    const saturatedInput = findSettingInput(thresholdsSection, 'Fleet Saturated Free Slots')
    if (!constrainedInput || !saturatedInput) return

    await setFieldValue(constrainedInput, '5')
    await setFieldValue(saturatedInput, '5')
    expect(thresholdsSection.textContent).toContain('Fleet saturated free slots must be less than fleet constrained free slots.')

    await setFieldValue(saturatedInput, '2')
    expect(thresholdsSection.textContent).not.toContain('Fleet saturated free slots must be less than fleet constrained free slots.')
  })

  it('imports CSV rows through the dry-run preview and exports the pool', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockResolvedValue({ data_pool: { id: 'pool-1', name: 'Customers' }, rows: [], total: 0 })
    apiMock.importDataPoolRows
      .mockResolvedValueOnce({
        dry_run: true,
        mode: 'append',
        columns: ['customer_id', 'region'],
        row_count: 2,
        sample_rows: [{ customer_id: 'C-1', region: 'east' }, { customer_id: 'C-2', region: 'west' }],
        warnings: [],
      })
      .mockResolvedValueOnce({ mode: 'append', columns: ['customer_id', 'region'], imported: 2, total: 2, warnings: [] })
    const exportBlob = new Blob(['customer_id,region\nC-1,east\n'], { type: 'text/csv' })
    apiMock.exportDataPoolRows.mockResolvedValue({ blob: exportBlob, filename: 'Customers.csv' })
    downloadBlobMock.mockResolvedValue('browser_started')
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section).toBeTruthy()
    if (!section) return
    const fileInput = section.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return

    const file = new File(['customer_id,region\nC-1,east\nC-2,west\n'], 'customers.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-1', file, { dryRun: true })
    expect(section.textContent).toContain('Import Preview')
    expect(section.textContent).toContain('customers.csv')

    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [
        { id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1', region: 'east' }, version: 1, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:00:00Z' },
        { id: 'row-2', pool_id: 'pool-1', data: { customer_id: 'C-2', region: 'west' }, version: 1, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:00:00Z' },
      ],
      total: 2,
    })

    const confirmButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent?.includes('Append 2 Row(s)'))
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return
    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-1', file, { mode: 'append' })
    expect(section.textContent).toContain('Imported 2 row(s)')
    expect(section.textContent).toContain('2 rows')

    const exportButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Export CSV')
    expect(exportButton).toBeTruthy()
    if (!exportButton) return
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.exportDataPoolRows).toHaveBeenCalledWith('pool-1')
    expect(downloadBlobMock).toHaveBeenCalledWith(exportBlob, 'Customers.csv')
    expect(section.textContent).toContain('Exported Customers.csv.')
  })

  it('starts a replace-only empty APEX backup preview in replace mode', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [{ id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1' }, version: 1, created_at: '', updated_at: '' }],
      total: 1,
    })
    apiMock.importDataPoolRows.mockReset()
    apiMock.importDataPoolRows
      .mockResolvedValueOnce({
        dry_run: true,
        mode: 'append',
        columns: [],
        row_count: 0,
        sample_rows: [],
        warnings: [],
        append_warning: 'an empty APEX data pool export can only be imported with mode=replace',
      })
      .mockResolvedValueOnce({
        mode: 'replace',
        columns: [],
        imported: 0,
        deleted: 1,
        total: 0,
        warnings: [],
      })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const fileInput = section?.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return
    const file = new File(['# APEXLOAD-DATA-POOL-EMPTY\n'], 'empty.apexload.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const modeSelect = section?.querySelector('select[aria-label="Import mode"]') as HTMLSelectElement | null
    expect(modeSelect?.value).toBe('replace')
    expect(section?.textContent).toContain('can only be imported with mode=replace')
    const confirmButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Replace With 0 Row(s)'))
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return

    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenNthCalledWith(1, 'pool-1', file, { dryRun: true })
    expect(apiMock.importDataPoolRows).toHaveBeenNthCalledWith(2, 'pool-1', file, { mode: 'replace' })
  })

  it('reports a created data pool as committed when only the list reconciliation fails', async () => {
    apiMock.listDataPools.mockReset()
    apiMock.listDataPools
      .mockResolvedValueOnce({ data_pools: [], count: 0 })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.createDataPool.mockReset()
    apiMock.createDataPool.mockResolvedValue({
      id: 'pool-1',
      name: 'Customers',
      description: '',
      project_id: 'project-1',
      created_at: '',
      updated_at: '',
    })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const nameInput = section?.querySelector('input[placeholder^="Pool name"]') as HTMLInputElement | null
    expect(nameInput).toBeTruthy()
    if (!nameInput) return
    await setFieldValue(nameInput, 'Customers')
    const addPool = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add Pool')
    expect(addPool).toBeTruthy()
    if (!addPool) return
    await act(async () => {
      addPool.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      addPool.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.createDataPool).toHaveBeenCalledWith({
      name: 'Customers',
      description: '',
      project_id: 'project-1',
    })
    expect(apiMock.createDataPool).toHaveBeenCalledTimes(1)
    expect(section?.textContent).toContain('The create succeeded—do not retry.')
    expect(section?.textContent).not.toContain('Unable to create data pool')
  })

  it('serializes a pool delete and reports it as committed when list reconciliation fails', async () => {
    apiMock.listDataPools.mockReset()
    apiMock.listDataPools
      .mockResolvedValueOnce({
        data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
        count: 1,
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [],
      total: 0,
    })
    apiMock.deleteDataPool.mockReset()
    apiMock.deleteDataPool.mockResolvedValue({ deleted: 'pool-1' })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const deleteButton = Array.from(section?.querySelectorAll('.settings-release-row > .settings-inline-actions button') || [])
      .find(button => button.textContent === 'Delete')
    expect(deleteButton).toBeTruthy()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete Data Pool')
    expect(confirm).toBeTruthy()
    if (!confirm) return
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.deleteDataPool).toHaveBeenCalledWith('pool-1')
    expect(apiMock.deleteDataPool).toHaveBeenCalledTimes(1)
    expect(section?.textContent).toContain('The delete succeeded—do not retry.')
    expect(section?.textContent).not.toContain('Unable to delete data pool')
  })

  it('opens a repository-linked pool and never silently selects a different pool for an unavailable link', async () => {
    apiMock.listDataPools.mockResolvedValue({ data_pools: [{ id: 'pool-linked', name: 'Linked pool', project_id: 'project-1', created_at: '', updated_at: '' }, { id: 'pool-other', name: 'Other pool', project_id: 'project-1', created_at: '', updated_at: '' }], count: 2 })
    const render = (resourceId: string) => {
      window.location.hash = `#/repository?setting=test-data&resourceId=${resourceId}`
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} destinationId="test-data" />)
    }
    await act(async () => render('pool-linked'))
    await flushPromises()
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1', true)
    expect(container.querySelector('#settings-section-data-pools .settings-release-row.active')?.textContent).toContain('Linked pool')
    await act(async () => render('missing'))
    await flushPromises()
    expect(container.querySelector('#settings-section-data-pools .settings-release-row.active')).toBeNull()
    expect(container.textContent).toContain('The requested data pool is not available in this project')
    window.location.hash = ''
  })

  it('does not let a slow data-pool response from the previous project overwrite the active project', async () => {
    type DataPoolListResponse = { data_pools: Array<{ id: string; name: string; project_id: string; created_at: string; updated_at: string }>; count: number }
    let resolveProjectOne: ((response: DataPoolListResponse) => void) | undefined
    let resolveProjectTwo: ((response: DataPoolListResponse) => void) | undefined
    apiMock.listDataPools.mockImplementation((projectId?: string) => new Promise<DataPoolListResponse>(resolve => {
      if (projectId === 'project-1') resolveProjectOne = resolve
      if (projectId === 'project-2') resolveProjectTwo = resolve
    }))
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-1')

    projectContextMock.activeProject = { id: 'project-2', name: 'Payments', created_at: '', updated_at: '' }
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    expect(apiMock.listDataPools).toHaveBeenCalledWith('project-2')

    await act(async () => {
      resolveProjectTwo?.({
        data_pools: [{ id: 'pool-2', name: 'Payment Accounts', project_id: 'project-2', created_at: '', updated_at: '' }],
        count: 1,
      })
    })
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section?.textContent).toContain('Payment Accounts')
    expect(section?.textContent).not.toContain('Checkout Customers')

    await act(async () => {
      resolveProjectOne?.({
        data_pools: [{ id: 'pool-1', name: 'Checkout Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
        count: 1,
      })
    })
    await flushPromises()

    expect(section?.textContent).toContain('Payment Accounts')
    expect(section?.textContent).not.toContain('Checkout Customers')
  })

  it('loads data-pool rows beyond the first bounded page', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    const firstPage = Array.from({ length: 100 }, (_, index) => ({
      id: `row-${index + 1}`,
      pool_id: 'pool-1',
      data: { customer_id: `C-${index + 1}` },
      version: 1,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    }))
    apiMock.getDataPool.mockImplementation((_poolId: string, page?: { limit?: number; cursor?: string }) => {
      if (page?.cursor === 'cursor-after-row-100') {
        return Promise.resolve({
          data_pool: { id: 'pool-1', name: 'Customers' },
          rows: [{
            id: 'row-101',
            pool_id: 'pool-1',
            data: { customer_id: 'C-101' },
            version: 1,
            created_at: '2026-05-18T12:00:00Z',
            updated_at: '2026-05-18T12:00:00Z',
          }],
          total: 101,
          limit: 100,
          offset: 100,
          revision: 'revision-1',
          has_more: false,
          next_cursor: '',
        })
      }
      return Promise.resolve({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: firstPage,
        total: 101,
        limit: 100,
        offset: 0,
        revision: 'revision-1',
        has_more: true,
        next_cursor: 'cursor-after-row-100',
      })
    })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section?.textContent).toContain('showing 100 of 101 rows')
    const loadMore = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Load more rows (1 remaining)'))
    expect(loadMore).toBeTruthy()
    if (!loadMore) return

    await act(async () => {
      loadMore.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.getDataPool).toHaveBeenCalledWith('pool-1', { limit: 100, cursor: 'cursor-after-row-100' })
    expect(section?.textContent).toContain('C-101')
    expect(section?.textContent).toContain('101 rows')
    expect(section?.textContent).not.toContain('Load more rows')
  })

  it('restarts data-pool pagination when the revision-bound cursor becomes stale', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{
          id: 'row-stale',
          pool_id: 'pool-1',
          data: { customer_id: 'C-stale' },
          version: 1,
          created_at: '2026-05-18T12:00:00Z',
          updated_at: '2026-05-18T12:00:00Z',
        }],
        total: 2,
        limit: 100,
        offset: 0,
        revision: 'revision-1',
        has_more: true,
        next_cursor: 'cursor-after-stale-row',
      })
      .mockRejectedValueOnce(new ApiError('data pool rows changed while pages were being loaded', 409))
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{
          id: 'row-fresh',
          pool_id: 'pool-1',
          data: { customer_id: 'C-fresh' },
          version: 1,
          created_at: '2026-05-18T12:01:00Z',
          updated_at: '2026-05-18T12:01:00Z',
        }],
        total: 1,
        limit: 100,
        offset: 0,
        revision: 'revision-2',
        has_more: false,
        next_cursor: '',
      })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const loadMore = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Load more rows'))
    expect(loadMore).toBeTruthy()
    if (!loadMore) return

    await act(async () => {
      loadMore.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.getDataPool).toHaveBeenNthCalledWith(2, 'pool-1', { limit: 100, cursor: 'cursor-after-stale-row' })
    expect(apiMock.getDataPool).toHaveBeenNthCalledWith(3, 'pool-1', { limit: 100, cursor: '' })
    expect(section?.textContent).toContain('C-fresh')
    expect(section?.textContent).not.toContain('C-stale')
    expect(section?.textContent).toContain('refreshed from the beginning')
  })

  it('ignores a stale initial row-load failure after a newer mutation refresh succeeds', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    let rejectInitialLoad: ((reason?: unknown) => void) | undefined
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool
      .mockImplementationOnce(() => new Promise((_resolve, reject) => {
        rejectInitialLoad = reject
      }))
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [{
          id: 'row-1',
          pool_id: 'pool-1',
          data: { customer_id: 'C-10001' },
          version: 1,
          created_at: '',
          updated_at: '',
        }],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
    apiMock.createDataPoolRow.mockReset()
    apiMock.createDataPoolRow.mockResolvedValue({
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-10001' },
      version: 1,
      created_at: '',
      updated_at: '',
    })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const addButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add Row')
    expect(addButton).toBeTruthy()
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()
    expect(section?.textContent).toContain('C-10001')

    await act(async () => {
      rejectInitialLoad?.(new Error('stale initial row read failed'))
    })
    await flushPromises()

    expect(section?.textContent).toContain('C-10001')
    expect(section?.textContent).not.toContain('stale initial row read failed')
    expect(section?.textContent).not.toContain('Unable to load data pool rows')
  })

  it('reports a created data-pool row as saved when only the reconciliation read fails', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [],
        total: 0,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.createDataPoolRow.mockReset()
    apiMock.createDataPoolRow.mockResolvedValue({
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-10001' },
      version: 1,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const addButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add Row')
    expect(addButton).toBeTruthy()
    if (!addButton) return

    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.createDataPoolRow).toHaveBeenCalledWith('pool-1', { data: { customer_id: 'C-10001' } })
    expect(container.textContent).toContain('The save succeeded—do not add it again.')
    expect(container.textContent).not.toContain('Unable to save data row')
  })

  it('reports an updated data-pool row as saved when only the reconciliation read fails', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    const row = {
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-1' },
      version: 3,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    }
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [row],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.updateDataPoolRow.mockReset()
    apiMock.updateDataPoolRow.mockResolvedValue({ ...row, version: 4 })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const editButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Edit')
    expect(editButton).toBeTruthy()
    if (!editButton) return
    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const updateButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Update Row')
    expect(updateButton).toBeTruthy()
    if (!updateButton) return

    await act(async () => {
      updateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.updateDataPoolRow).toHaveBeenCalledWith('pool-1', 'row-1', { data: { customer_id: 'C-1' }, version: 3 })
    expect(container.textContent).toContain('The save succeeded—do not retry the update.')
    expect(container.textContent).not.toContain('Unable to save data row')
  })

  it('reports a deleted data-pool row as committed when only the reconciliation read fails', async () => {
    const row = {
      id: 'row-1',
      pool_id: 'pool-1',
      data: { customer_id: 'C-1' },
      version: 3,
      created_at: '2026-05-18T12:00:00Z',
      updated_at: '2026-05-18T12:00:00Z',
    }
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [row],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.deleteDataPoolRow.mockReset()
    apiMock.deleteDataPoolRow.mockResolvedValue({ deleted: 'row-1' })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const deleteButton = Array.from(section?.querySelectorAll('td[aria-label="Data pool actions"] button') || [])
      .find(button => button.textContent === 'Delete')
    expect(deleteButton).toBeTruthy()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete Row')
    expect(confirm).toBeTruthy()
    if (!confirm) return
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.deleteDataPoolRow).toHaveBeenCalledWith('pool-1', 'row-1')
    expect(apiMock.deleteDataPoolRow).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('The delete succeeded—do not retry.')
    expect(container.textContent).not.toContain('Unable to delete data row')
  })

  it('reports an imported data-pool file as committed when only the reconciliation read fails', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool
      .mockReset()
      .mockResolvedValueOnce({
        data_pool: { id: 'pool-1', name: 'Customers' },
        rows: [],
        total: 0,
        has_more: false,
        next_cursor: '',
      })
      .mockRejectedValueOnce(new Error('read replica unavailable'))
    apiMock.importDataPoolRows
      .mockReset()
      .mockResolvedValueOnce({
        dry_run: true,
        mode: 'append',
        columns: ['customer_id'],
        row_count: 2,
        sample_rows: [{ customer_id: 'C-1' }, { customer_id: 'C-2' }],
        warnings: [],
      })
      .mockResolvedValueOnce({
        mode: 'append',
        columns: ['customer_id'],
        imported: 2,
        total: 2,
        warnings: [],
      })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const fileInput = section?.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return
    const file = new File(['customer_id\nC-1\nC-2\n'], 'customers.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const confirmButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent?.includes('Append 2 Row(s)'))
    expect(confirmButton).toBeTruthy()
    if (!confirmButton) return
    await act(async () => {
      confirmButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-1', file, { mode: 'append' })
    expect(container.textContent).toContain('The import succeeded—do not import the file again.')
    expect(container.textContent).not.toContain('Unable to import rows')
  })

  it('keeps a confirmed row delete bound to its row when another pool becomes selected', async () => {
    const rowA = {
      id: 'row-a',
      pool_id: 'pool-a',
      data: { customer_id: 'A-visible' },
      version: 1,
      created_at: '',
      updated_at: '',
    }
    const rowB = {
      id: 'row-b',
      pool_id: 'pool-b',
      data: { customer_id: 'B-visible' },
      version: 1,
      created_at: '',
      updated_at: '',
    }
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => Promise.resolve({
      data_pool: { id: poolId, name: poolId === 'pool-a' ? 'Primary Customers' : 'Secondary Customers' },
      rows: poolId === 'pool-a' ? [rowA] : [rowB],
      total: 1,
      has_more: false,
      next_cursor: '',
    }))
    apiMock.deleteDataPoolRow.mockReset()
    apiMock.deleteDataPoolRow.mockResolvedValue({ deleted: 'row-a' })
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const deleteButton = Array.from(section?.querySelectorAll('td[aria-label="Data pool actions"] button') || [])
      .find(button => button.textContent === 'Delete')
    expect(deleteButton).toBeTruthy()
    if (!deleteButton) return
    await act(async () => {
      deleteButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const confirm = Array.from(container.querySelectorAll('[role="dialog"] button'))
      .find(button => button.textContent === 'Delete Row')
    expect(confirm).toBeTruthy()
    if (!confirm) return
    await act(async () => {
      confirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.deleteDataPoolRow).toHaveBeenCalledWith('pool-a', 'row-a')
    expect(apiMock.getDataPool.mock.calls.filter(([poolId]) => poolId === 'pool-a')).toHaveLength(1)
    expect(section?.textContent).toContain('B-visible')
    expect(section?.textContent).not.toContain('Row deleted.')
    expect(section?.textContent).not.toContain('Unable to delete data row')
  })

  it('does not let a late row mutation invalidate the newly selected pool or erase its draft', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    let resolvePoolB: ((value: {
      data_pool: { id: string; name: string }
      rows: Array<{
        id: string
        pool_id: string
        data: { customer_id: string }
        version: number
        created_at: string
        updated_at: string
      }>
      total: number
      has_more: boolean
      next_cursor: string
    }) => void) | undefined
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => {
      if (poolId === 'pool-b') {
        return new Promise(resolve => {
          resolvePoolB = resolve
        })
      }
      return Promise.resolve({
        data_pool: { id: 'pool-a', name: 'Primary Customers' },
        rows: [],
        total: 0,
        has_more: false,
        next_cursor: '',
      })
    })
    let resolveCreate: ((value: {
      id: string
      pool_id: string
      data: { customer_id: string }
      version: number
      created_at: string
      updated_at: string
    }) => void) | undefined
    apiMock.createDataPoolRow.mockReset()
    apiMock.createDataPoolRow.mockImplementation(() => new Promise(resolve => {
      resolveCreate = resolve
    }))
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const addButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Add Row')
    expect(addButton).toBeTruthy()
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const rowEditor = section?.querySelector('textarea') as HTMLTextAreaElement | null
    expect(rowEditor).toBeTruthy()
    if (!rowEditor) return
    const poolBDraft = '{\n  "customer_id": "B-draft"\n}'
    await setFieldValue(rowEditor, poolBDraft)

    await act(async () => {
      resolveCreate?.({
        id: 'row-a',
        pool_id: 'pool-a',
        data: { customer_id: 'C-10001' },
        version: 1,
        created_at: '2026-05-18T12:00:00Z',
        updated_at: '2026-05-18T12:00:00Z',
      })
    })
    await flushPromises()
    expect(apiMock.getDataPool.mock.calls.filter(([poolId]) => poolId === 'pool-a')).toHaveLength(1)

    await act(async () => {
      resolvePoolB?.({
        data_pool: { id: 'pool-b', name: 'Secondary Customers' },
        rows: [{
          id: 'row-b',
          pool_id: 'pool-b',
          data: { customer_id: 'B-visible' },
          version: 1,
          created_at: '2026-05-18T12:01:00Z',
          updated_at: '2026-05-18T12:01:00Z',
        }],
        total: 1,
        has_more: false,
        next_cursor: '',
      })
    })
    await flushPromises()

    expect(section?.textContent).toContain('B-visible')
    expect(rowEditor.value).toBe(poolBDraft)
  })

  it('discards a late import preview after the user selects another data pool', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => Promise.resolve({
      data_pool: { id: poolId, name: poolId === 'pool-a' ? 'Primary Customers' : 'Secondary Customers' },
      rows: [],
      total: 0,
      has_more: false,
      next_cursor: '',
    }))
    let resolvePreview: ((value: {
      dry_run: boolean
      mode: string
      columns: string[]
      row_count: number
      sample_rows: Array<{ customer_id: string }>
      warnings: string[]
    }) => void) | undefined
    apiMock.importDataPoolRows.mockReset()
    apiMock.importDataPoolRows.mockImplementation(() => new Promise(resolve => {
      resolvePreview = resolve
    }))
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const fileInput = section?.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).toBeTruthy()
    if (!fileInput) return
    const file = new File(['customer_id\nC-1\n'], 'primary.csv', { type: 'text/csv' })
    Object.defineProperty(fileInput, 'files', { value: [file], configurable: true })
    await act(async () => {
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    await act(async () => {
      resolvePreview?.({
        dry_run: true,
        mode: 'append',
        columns: ['customer_id'],
        row_count: 1,
        sample_rows: [{ customer_id: 'C-1' }],
        warnings: [],
      })
    })
    await flushPromises()

    expect(apiMock.importDataPoolRows).toHaveBeenCalledTimes(1)
    expect(apiMock.importDataPoolRows).toHaveBeenCalledWith('pool-a', file, { dryRun: true })
    expect(section?.textContent).not.toContain('Import Preview')
    expect(section?.textContent).not.toContain('Append 1 Row(s)')
  })

  it('does not surface a late export failure against a newly selected data pool', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [
        { id: 'pool-a', name: 'Primary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
        { id: 'pool-b', name: 'Secondary Customers', project_id: 'project-1', created_at: '', updated_at: '' },
      ],
      count: 2,
    })
    apiMock.getDataPool.mockReset()
    apiMock.getDataPool.mockImplementation((poolId: string) => Promise.resolve({
      data_pool: { id: poolId, name: poolId === 'pool-a' ? 'Primary Customers' : 'Secondary Customers' },
      rows: poolId === 'pool-a'
        ? [{
            id: 'row-a',
            pool_id: 'pool-a',
            data: { customer_id: 'C-1' },
            version: 1,
            created_at: '2026-05-18T12:00:00Z',
            updated_at: '2026-05-18T12:00:00Z',
          }]
        : [],
      total: poolId === 'pool-a' ? 1 : 0,
      has_more: false,
      next_cursor: '',
    }))
    let rejectExport: ((reason?: unknown) => void) | undefined
    apiMock.exportDataPoolRows.mockReset()
    apiMock.exportDataPoolRows.mockImplementation(() => new Promise((_resolve, reject) => {
      rejectExport = reject
    }))
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    const exportButton = Array.from(section?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Export CSV')
    expect(exportButton).toBeTruthy()
    if (!exportButton) return
    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const poolBButton = Array.from(section?.querySelectorAll('button.settings-release-main') || [])
      .find(button => button.textContent?.includes('Secondary Customers'))
    expect(poolBButton).toBeTruthy()
    if (!poolBButton) return
    await act(async () => {
      poolBButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    await act(async () => {
      rejectExport?.(new Error('export backend unavailable'))
    })
    await flushPromises()

    expect(apiMock.exportDataPoolRows).toHaveBeenCalledWith('pool-a')
    expect(container.textContent).not.toContain('Unable to export data pool')
    expect(container.textContent).not.toContain('export backend unavailable')
  })

  it('sends the edit-time version token and closes the row editor on a 409 conflict', async () => {
    apiMock.listDataPools.mockResolvedValue({
      data_pools: [{ id: 'pool-1', name: 'Customers', project_id: 'project-1', created_at: '', updated_at: '' }],
      count: 1,
    })
    apiMock.getDataPool.mockResolvedValue({
      data_pool: { id: 'pool-1', name: 'Customers' },
      rows: [{ id: 'row-1', pool_id: 'pool-1', data: { customer_id: 'C-1' }, version: 3, created_at: '2026-05-18T12:00:00Z', updated_at: '2026-05-18T12:00:00Z' }],
      total: 1,
    })
    apiMock.updateDataPoolRow.mockRejectedValue(new ApiError('data pool row changed while the update was in flight; reload and retry', 409))
    window.localStorage.setItem('apexload.settings.focusSection', 'data-pools')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()
    await flushPromises()

    const section = container.querySelector('#settings-section-data-pools')
    expect(section).toBeTruthy()
    if (!section) return
    const editButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Edit')
    expect(editButton).toBeTruthy()
    if (!editButton) return
    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(section.textContent).toContain('Edit Row')

    const updateButton = Array.from(section.querySelectorAll('button')).find(button => button.textContent === 'Update Row')
    expect(updateButton).toBeTruthy()
    if (!updateButton) return
    await act(async () => {
      updateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.updateDataPoolRow).toHaveBeenCalledWith('pool-1', 'row-1', { data: { customer_id: 'C-1' }, version: 3 })
    expect(section.textContent).not.toContain('Edit Row')
    // The conflict message renders in the shared section-messages banner, which
    // sits outside the section element itself.
    expect(container.textContent).toContain('reopen the row to apply your change')
  })

  it('offers fleet pools for reservation scoping and excludes pool-scoped rows from the usage summary', async () => {
    fleetApiMock.listFleetPools.mockResolvedValue({
      fleet_pools: [{ id: 'pool-1', name: 'US East', cloud_provider: 'aws', status: 'active' }],
    })

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Standing allocations')

    const reservationSection = container.querySelector('[data-testid="capacity-reservations"]')
    if (!reservationSection) return
    const addButton = Array.from(reservationSection.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Add standing allocation')
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const projectInput = reservationSection.querySelector('select[aria-label="Reservation project"]') as HTMLSelectElement | null
    if (!projectInput) return
    await setFieldValue(projectInput, 'project-1')

    const summary = container.querySelector('[data-testid="reservation-usage-summary"]')
    expect(summary?.textContent).toContain('1 of 1 generator reserved')

    const poolSelect = reservationSection.querySelector('select[aria-label="Reservation pool"]') as HTMLSelectElement | null
    expect(poolSelect).toBeTruthy()
    if (!poolSelect) return
    expect(Array.from(poolSelect.options).map(option => option.textContent)).toEqual(['Global envelope', 'US East'])

    // Pool-scoped reservations draw from the pool envelope, not the global sum.
    await setFieldValue(poolSelect, 'pool-1')
    expect(summary?.textContent).toContain('0 of 1 generator reserved')
  })

  it('shows the overbooking validation error before save', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    await selectTopLevelSettingsTab('Capacity Planning')
    const capacitySection = container.querySelector('#settings-section-capacity')
    if (!capacitySection) return
    await selectSettingsSubnav(capacitySection, 'Standing allocations')

    const reservationSection = container.querySelector('[data-testid="capacity-reservations"]')
    if (!reservationSection) return
    const addButton = Array.from(reservationSection.querySelectorAll('button')).find(b => b.textContent?.trim() === 'Add standing allocation')
    if (!addButton) return
    await act(async () => {
      addButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const projectInput = reservationSection.querySelector('select[aria-label="Reservation project"]') as HTMLSelectElement | null
    const generatorsInput = reservationSection.querySelector('input[aria-label="Reserved generators"]') as HTMLInputElement | null
    if (!projectInput || !generatorsInput) return
    await setFieldValue(projectInput, 'project-1')
    await setFieldValue(generatorsInput, '4')

    expect(capacitySection.textContent).toContain('Always-active global reservations (4) exceed configured available generators (1).')
    const saveButton = findButton('Save pending settings') || findButton('No pending settings')
    expect(saveButton?.disabled).toBe(true)
  })

  it('serializes save and reset mutations while freezing editable and recovery controls', async () => {
    let resolveUpdate: (() => void) | undefined
    apiMock.updateSystemSettings.mockReset()
    apiMock.updateSystemSettings.mockImplementation((nextSettings) => new Promise(resolve => {
      resolveUpdate = () => resolve({
        ...nextSettings,
        revision: `ss-${'b'.repeat(64)}`,
      })
    }))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    const maxConcurrentInput = generalSection
      ? findSettingInput(generalSection, 'Max Concurrent Tests')
      : null
    expect(maxConcurrentInput).toBeTruthy()
    if (!maxConcurrentInput) return
    await setFieldValue(maxConcurrentInput, '12')

    const resetButton = findButton('Reset All Settings to Defaults')
    expect(resetButton).toBeTruthy()
    if (!resetButton) return
    await act(async () => {
      resetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    const dialog = container.querySelector('[role="dialog"]')
    const resetConfirm = Array.from(dialog?.querySelectorAll('button') || [])
      .find(button => button.textContent === 'Reset All Settings')
    const saveButton = findButton('Save pending settings')
    expect(resetConfirm).toBeTruthy()
    expect(saveButton).toBeTruthy()
    if (!resetConfirm || !saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      resetConfirm.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.updateSystemSettings).toHaveBeenCalledTimes(1)
    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      general: expect.objectContaining({ max_concurrent_tests: 12 }),
    }))
    expect(maxConcurrentInput.matches(':disabled')).toBe(true)
    expect(findButton('Discard Changes')?.disabled).toBe(true)
    expect(findButton('Reset All Settings to Defaults')?.disabled).toBe(true)
    expect(resetConfirm.disabled).toBe(true)

    await act(async () => {
      resolveUpdate?.()
    })
    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledTimes(1)
    expect(maxConcurrentInput.matches(':disabled')).toBe(false)
    expect(maxConcurrentInput.value).toBe('12')
    expect(container.textContent).toContain('Settings saved.')
  })

  it('focuses the Reservations tab with guidance when the backend rejects a capacity save', async () => {
    apiMock.updateSystemSettings.mockRejectedValue(new ApiError('capacity_planning update rejected: approved timeslot demand exceeds the configured envelope', 400))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    if (!generalSection) return
    const maxConcurrentInput = findSettingInput(generalSection, 'Max Concurrent Tests')
    if (!maxConcurrentInput) return
    await setFieldValue(maxConcurrentInput, '12')

    const saveButton = findButton('Save pending settings')
    expect(saveButton).toBeTruthy()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.querySelector('#settings-section-capacity')?.hasAttribute('hidden')).toBe(false)
    expect(container.querySelector('#settings-capacity-panel-reservations')?.hasAttribute('hidden')).toBe(false)
    expect(container.textContent).toContain('Approved capacity timeslots reserve part of the envelope')
  })

  it('keeps the draft and offers reload recovery after a stale If-Match save', async () => {
    apiMock.updateSystemSettings.mockRejectedValue(new ApiError('system settings changed concurrently; refresh and retry', 412))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    if (!generalSection) return
    const maxConcurrentInput = findSettingInput(generalSection, 'Max Concurrent Tests')
    if (!maxConcurrentInput) return
    await setFieldValue(maxConcurrentInput, '12')

    const saveButton = findButton('Save pending settings')
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Settings changed in another session. Reload settings and re-apply your edits.')
    expect(findButton('Reload Settings')).toBeTruthy()
    expect((maxConcurrentInput as HTMLInputElement).value).toBe('12')
    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      revision: `ss-${'a'.repeat(64)}`,
      general: expect.objectContaining({ max_concurrent_tests: 12 }),
    }))
  })

  it('keeps the draft and loaded revision when a reset loses its concurrency race', async () => {
    apiMock.updateSystemSettings.mockRejectedValue(new ApiError('system settings changed concurrently; refresh and retry', 412))

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    if (!generalSection) return
    const maxConcurrentInput = findSettingInput(generalSection, 'Max Concurrent Tests')
    if (!maxConcurrentInput) return
    await setFieldValue(maxConcurrentInput, '12')

    const resetButton = findButton('Reset All Settings to Defaults')
    if (!resetButton) return
    await act(async () => resetButton.click())
    const dialog = container.querySelector('[role="dialog"]')
    const confirm = Array.from(dialog?.querySelectorAll('button') || []).find(button => button.textContent === 'Reset All Settings')
    expect(confirm).toBeTruthy()
    await act(async () => confirm?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    expect((maxConcurrentInput as HTMLInputElement).value).toBe('12')
    expect(container.textContent).toContain('Settings changed in another session. Reload settings and re-apply your edits.')
    expect(findButton('Reload Settings')).toBeTruthy()
    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      revision: `ss-${'a'.repeat(64)}`,
      general: expect.objectContaining({ max_concurrent_tests: 10 }),
    }))
  })

  it('focuses the requested capacity subsection from a stored deep link', async () => {
    window.localStorage.setItem('apexload.settings.focusSection', 'capacity')
    window.localStorage.setItem('apexload.settings.focusSubsection', 'reservations')

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })
    await flushPromises()

    expect(container.querySelector('#settings-section-capacity')?.hasAttribute('hidden')).toBe(false)
    expect(container.querySelector('#settings-capacity-panel-reservations')?.hasAttribute('hidden')).toBe(false)
    expect(window.localStorage.getItem('apexload.settings.focusSection')).toBeNull()
    expect(window.localStorage.getItem('apexload.settings.focusSubsection')).toBeNull()
  })

  it('renders the project catalog from the domain-scoped context instead of listing every project', async () => {
    // App provides ProjectContext already filtered to the active domain. Reading
    // it here is what keeps the Settings catalog in step with the domain picker;
    // fetching independently is how it used to show every project in the install.
    seedProjectCatalog([
      { id: 'project-1', name: 'Customer Portal', domain_id: 'default', created_at: '', updated_at: '' },
      { id: 'project-2', name: 'Retail Checkout', domain_id: 'default', created_at: '', updated_at: '' },
    ])

    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness />)
    })
    await flushPromises()

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    const cards = Array.from(projectSection?.querySelectorAll('.settings-app-card') || [])
    expect(cards.map(card => card.querySelector('span')?.textContent)).toEqual([
      'Customer Portal',
      'Retail Checkout',
    ])
    // The catalog cannot show an out-of-scope project because it never asks for one.
    expect(projectApiMock.listProjects).not.toHaveBeenCalled()
  })

  it('shows the active workspace scope as a breadcrumb in the page header', async () => {
    await act(async () => {
      renderWithSettingsTopbar(root, <SettingsPageDomainHarness />)
    })
    await flushPromises()

    const breadcrumb = container.querySelector('[data-testid="settings-scope-breadcrumb"]')
    expect(breadcrumb).toBeTruthy()
    const text = breadcrumb?.textContent || ''
    expect(text).toContain(defaultDomain.name)
    expect(text).toContain('Checkout')
  })
})

function findButton(label: string) {
  return Array.from(document.querySelectorAll('button')).find(button => (
    button.textContent?.trim() === label || button.getAttribute('aria-label') === label
  )) as HTMLButtonElement | undefined
}

function findSettingsSubnavButton(root: Element, label: string) {
  return Array.from(root.querySelectorAll('.settings-subnav-item')).find(button => (
    button.querySelector('.settings-subnav-label')?.textContent === label
  )) as HTMLButtonElement | undefined
}

function findGeneralSubnavButton(root: Element, label: string) {
  return findSettingsSubnavButton(root, label)
}

async function selectTopLevelSettingsTab(label: string) {
  const destinationLabels: Record<string, string> = { General: 'Controller & runtime', 'Performance Thresholds': 'Alert & stop policies', 'Capacity Planning': 'Capacity source', Domains: 'Domains & placement defaults', 'Projects & Compliance': 'Projects', 'Applications & Releases': 'Applications & releases', 'Data Pools': 'Shared data pools', Integrations: 'Library federation', 'Feature Access': 'Domain feature policy' }
  const button = findButton(destinationLabels[label] || label)
  expect(button).toBeTruthy()
  if (!button) return
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
}

async function selectSettingsSubnav(root: Element, label: string) {
  const button = findSettingsSubnavButton(root, label)
  expect(button).toBeTruthy()
  if (!button) return
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  expect(button.getAttribute('aria-selected')).toBe('true')
}

async function selectGeneralSubnav(root: Element, label: string) {
  await selectSettingsSubnav(root, label)
}

function findSettingInput(root: Element, label: string) {
  const rows = Array.from(root.querySelectorAll('.setting-row'))
  const row = rows.find(item => item.textContent?.includes(label))
  return row?.querySelector('input') as HTMLInputElement | null
}

function findSettingSelect(root: Element, label: string) {
  const rows = Array.from(root.querySelectorAll('.setting-row'))
  const row = rows.find(item => item.textContent?.includes(label))
  return row?.querySelector('select') as HTMLSelectElement | null
}

async function setFieldValue(element: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
  await act(async () => {
    const prototype = Object.getPrototypeOf(element)
    const descriptor = Object.getOwnPropertyDescriptor(prototype, 'value')
    descriptor?.set?.call(element, value)
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}
