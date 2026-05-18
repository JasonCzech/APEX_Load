// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SettingsPage from './SettingsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { downloadBlobMock } = vi.hoisted(() => ({
  downloadBlobMock: vi.fn(),
}))

const apiMock = {
  getAuthEffectiveAccess: vi.fn(),
  getAuthAccessMatrix: vi.fn(),
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
  listTests: vi.fn(),
}

const applicationApiMock = {
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

const applicationContextMock = {
  applications: [],
  releases: [],
  activeApplication: null,
  activeRelease: null,
  setActiveApplication: vi.fn(),
  setActiveRelease: vi.fn(),
  refreshApplications: vi.fn(),
  refreshReleases: vi.fn(),
  createAndSelectApplication: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('../hooks/api/useApplicationApi', () => ({
  useApplicationApi: () => applicationApiMock,
}))

vi.mock('../hooks/api/useProjectApi', () => ({
  useProjectApi: () => projectApiMock,
}))

vi.mock('../hooks/useApplication', () => ({
  useApplication: () => applicationContextMock,
}))

vi.mock('../hooks/useProject', () => ({
  useProject: () => ({
    activeProject: { id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  }),
}))

vi.mock('../utils/fileUtils', () => ({
  downloadBlob: downloadBlobMock,
}))

describe('SettingsPage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getSystemSettings.mockResolvedValue({
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
        roles_header: 'X-Stormrunner-Roles',
        project_roles_header: 'X-Stormrunner-Project-Roles',
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
          summary: 'Stormrunner is running with local auth bypass enabled.',
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
          target: 'stormrunner-generator-0.stormrunner-generator:9090',
          generator_id: 'stormrunner-generator-0',
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
          target: 'stormrunner-generator-1.stormrunner-generator:9090',
          generator_id: 'stormrunner-generator-1',
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
          details: { archive_root: '/tmp/stormrunner-archives' },
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
    applicationApiMock.listReleases.mockResolvedValue({ releases: [] })
    projectApiMock.listProjects.mockResolvedValue({
      projects: [
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
      ],
      count: 1,
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
    downloadBlobMock.mockReset()
  })

  it('defaults generator capacity fields to the connected live pod capacity when settings are still at the stock defaults', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    const maxVUsersInput = findSettingInput(capacitySection, 'Generator Max VUsers')
    const cpuCoresInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    const memoryInput = findSettingInput(capacitySection, 'Generator Memory (MB)')
    const availableGeneratorsInput = findSettingInput(capacitySection, 'Configured Available Generators')

    expect(cpuCoresInput?.value).toBe('2')
    expect(memoryInput?.value).toBe('4096')
    expect(maxVUsersInput?.value).toBe('8000')
    expect(availableGeneratorsInput?.value).toBe('2')
    expect(container.textContent).toContain('2 CPU / 4096 MB')
    expect(container.textContent).toContain('Up to date')
    expect(apiMock.getSystemInventory).toHaveBeenCalledTimes(1)
  })

  it('plans archive retention pruning from the capacity settings section', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    expect(capacitySection.textContent).toContain('Archive Retention Executor')
    expect(capacitySection.textContent).toContain('Default Retention Days')
    expect(findSettingInput(capacitySection, 'Default Retention Days')?.value).toBe('45')

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
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

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

    await act(async () => {
      exportButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.downloadUsageAccounting).toHaveBeenCalledWith('project-1')
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^stormrunner-usage-accounting-\d{4}-\d{2}-\d{2}\.csv$/))
  })

  it('surfaces production readiness checks and refreshes the backend payload', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

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
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
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

    const saveButton = findButton('Save Changes')
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
        roles_header: 'X-Stormrunner-Roles',
        project_roles_header: 'X-Stormrunner-Project-Roles',
      },
    })

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const livePresetButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Live Pod Capacity'))
    expect(livePresetButton).toBeDefined()
    if (!livePresetButton) return

    await act(async () => {
      livePresetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const capacitySection = container.querySelector('#settings-section-capacity')
    expect(capacitySection).toBeTruthy()
    if (!capacitySection) return

    const maxVUsersInput = findSettingInput(capacitySection, 'Generator Max VUsers')
    const cpuCoresInput = findSettingInput(capacitySection, 'Generator CPU (cores)')
    const memoryInput = findSettingInput(capacitySection, 'Generator Memory (MB)')
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
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    const projectsTab = findButton('Projects & Compliance')
    expect(projectsTab).toBeDefined()
    if (projectsTab) {
      await act(async () => {
        projectsTab.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    expect(projectSection?.textContent).toContain('Customer Portal')
    expect(projectSection?.textContent).toContain('Custom Redaction Rules')
    if (!projectSection) return

    const quotaInputs = Array.from(projectSection.querySelectorAll('input')).filter(input => input.placeholder === 'Unlimited') as HTMLInputElement[]
    expect(quotaInputs).toHaveLength(2)
    if (quotaInputs.length !== 2) return

    await setFieldValue(quotaInputs[0], '3')
    await setFieldValue(quotaInputs[1], '400')

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

    const saveButton = findButton('Save Project Governance')
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
      },
    })
    expect(container.textContent).toContain('Saved project governance defaults for Customer Portal.')
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
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    const newProjectInput = Array.from(projectSection.querySelectorAll('input')).find(input => input.placeholder?.includes('Create project')) as HTMLInputElement | undefined
    expect(newProjectInput).toBeDefined()
    if (!newProjectInput) return
    await setFieldValue(newProjectInput, 'Restricted Project')

    expect(findButton('Add Project')?.disabled).toBe(true)
    expect(findButton('Add Rule')?.disabled).toBe(true)
    expect(projectSection.textContent).toContain('Current session can view this project but cannot change project governance.')

    const quotaInputs = Array.from(projectSection.querySelectorAll('input')).filter(input => input.placeholder === 'Unlimited') as HTMLInputElement[]
    expect(quotaInputs).toHaveLength(2)
    if (quotaInputs.length !== 2) return

    await setFieldValue(quotaInputs[0], '3')
    const saveButton = findButton('Save Project Governance')
    expect(saveButton?.disabled).toBe(true)
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
  })

  it('blocks saving project compliance when a rule is missing its regex pattern', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()
    await flushPromises()

    const addRuleButton = findButton('Add Rule')
    expect(addRuleButton).toBeDefined()
    if (!addRuleButton) return

    await act(async () => {
      addRuleButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const projectSection = container.querySelector('#settings-section-projects')
    expect(projectSection).toBeTruthy()
    if (!projectSection) return

    const ruleCards = Array.from(projectSection.querySelectorAll('.project-redaction-rule-card'))
    const secondRuleInputs = ruleCards[1].querySelectorAll('input')
    expect(secondRuleInputs).toHaveLength(2)
    if (secondRuleInputs.length < 1) return

    await setFieldValue(secondRuleInputs[0] as HTMLInputElement, 'broken rule')

    await flushPromises()

    const saveButton = findButton('Save Project Governance')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    expect(saveButton.disabled).toBe(true)
    expect(projectApiMock.updateProject).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Redaction rule 2 needs a regex pattern.')
  })

  it('saves trusted-header auth settings through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'trusted_headers')

    const saveButton = findButton('Save Changes')
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
        user_header: 'X-Auth-Request-User',
        roles_header: 'X-Stormrunner-Roles',
        project_roles_header: 'X-Stormrunner-Project-Roles',
      }),
    }))
    expect(generalSection.textContent).toContain('Microsoft Entra ID Troubleshooting')
    expect(generalSection.textContent).toContain('Subject Header Check')
    expect(generalSection.textContent).toContain('Role Header Check')
    expect(container.textContent).toContain('Settings saved.')
  })

  it('disables settings, service-account, and audit actions when effective RBAC denies them', async () => {
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
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const apiEndpointInput = findSettingInput(generalSection, 'API Endpoint')
    expect(apiEndpointInput).toBeTruthy()
    if (!apiEndpointInput) return
    await setFieldValue(apiEndpointInput, ':9090')

    const saveButton = findButton('Save Changes')
    const resetButton = findButton('Reset All Settings to Defaults')
    expect(saveButton?.disabled).toBe(true)
    expect(resetButton?.disabled).toBe(true)
    expect(container.textContent).toContain('Current session can view settings but cannot save global configuration changes.')

    const serviceAccountNameInput = findSettingInput(generalSection, 'Service Account Name')
    expect(serviceAccountNameInput).toBeTruthy()
    if (!serviceAccountNameInput) return
    await setFieldValue(serviceAccountNameInput, 'Restricted CI')

    expect(findButton('Create Service Account')?.disabled).toBe(true)
    expect(findButton('Rotate Key')?.disabled).toBe(true)
    expect(findButton('Disable')?.disabled).toBe(true)
    expect(findButton('Delete')?.disabled).toBe(true)
    expect(findButton('Export CSV')?.disabled).toBe(true)
  })

  it('saves native OIDC auth settings through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

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
    await setFieldValue(clientIDInput, 'stormrunner-dashboard')
    await setFieldValue(redirectURLInput, 'https://stormrunner.example.test/api/v1/auth/callback')

    const saveButton = findButton('Save Changes')
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
        client_id: 'stormrunner-dashboard',
        client_secret_env_var: 'STORMRUNNER_OIDC_CLIENT_SECRET',
        redirect_url: 'https://stormrunner.example.test/api/v1/auth/callback',
        scopes: ['openid', 'profile', 'email'],
        roles_claim: 'roles',
        project_roles_claim: 'project_roles',
        session_secret_env_var: 'STORMRUNNER_AUTH_SESSION_SECRET',
      }),
    }))
    expect(generalSection.textContent).toContain('Native OIDC Session Flow')
    expect(container.textContent).toContain('Settings saved.')
  })

  it('applies OIDC provider presets without overwriting deployment-specific values', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

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
    await setFieldValue(redirectURLInput, 'https://stormrunner.example.test/api/v1/auth/callback')

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
    expect(redirectURLInput.value).toBe('https://stormrunner.example.test/api/v1/auth/callback')
    expect(generalSection.textContent).toContain('Issuer URL, client ID, and redirect URL stay deployment-specific.')
  })

  it('shows dev-mode auth admin panels and provisions service accounts', async () => {
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

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} authStatus={{ enabled: false, mode: 'dev', authenticated: false }} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    expect(generalSection.textContent).toContain('Development Bypass')
    expect(generalSection.textContent).toContain('Service Account API Keys')
    expect(generalSection.textContent).toContain('Recent Audit Events')
    expect(generalSection.textContent).toContain('Nightly CI')
    expect(generalSection.textContent).toContain('system.settings.update')

    const nameInput = Array.from(generalSection.querySelectorAll('input')).find(input => (input as HTMLInputElement).placeholder === 'Nightly CI') as HTMLInputElement | undefined
    expect(nameInput).toBeDefined()
    if (!nameInput) return
    await setFieldValue(nameInput, 'Release Automation')

    const descriptionInput = Array.from(generalSection.querySelectorAll('input')).find(input => (input as HTMLInputElement).placeholder === 'Runs nightly smoke coverage') as HTMLInputElement | undefined
    expect(descriptionInput).toBeDefined()
    if (!descriptionInput) return
    await setFieldValue(descriptionInput, 'Runs release checks')

    const createButton = findButton('Create Service Account')
    expect(createButton).toBeDefined()
    if (!createButton) return

    await act(async () => {
      createButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createAuthServiceAccount).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Release Automation',
      description: 'Runs release checks',
      global_roles: ['viewer'],
      scopes: ['automation:*'],
      rotation_interval_days: 90,
    }))
    expect(generalSection.textContent).toContain('Copy the issued API key now')
    expect(generalSection.textContent).toContain('srk_test_key')
    expect(generalSection.textContent).toContain('Rotation policy')
    expect(generalSection.textContent).toContain('Token scopes')

    const rotateButton = Array.from(generalSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Rotate Key') as HTMLButtonElement | undefined
    expect(rotateButton).toBeDefined()
    if (!rotateButton) return
    await act(async () => {
      rotateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.rotateAuthServiceAccount).toHaveBeenCalledWith('svc-1')
    expect(generalSection.textContent).toContain('srk_rotated_key')

    const disableButton = Array.from(generalSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Disable') as HTMLButtonElement | undefined
    expect(disableButton).toBeDefined()
    if (!disableButton) return
    await act(async () => {
      disableButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(apiMock.updateAuthServiceAccount).toHaveBeenCalledWith('svc-1', { active: false })

    const auditFilterInputs = Array.from(generalSection.querySelectorAll('input')).filter(input => {
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

    const exportAuditButton = Array.from(generalSection.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Export CSV') as HTMLButtonElement | undefined
    expect(exportAuditButton).toBeDefined()
    if (!exportAuditButton) return
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
    expect(downloadBlobMock).toHaveBeenCalledWith(expect.any(Blob), expect.stringMatching(/^stormrunner-audit-events-\d{4}-\d{2}-\d{2}\.csv$/))
    expect(generalSection.textContent).toContain('Audit export downloaded.')
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
        roles_header: 'X-Stormrunner-Roles',
        project_roles_header: 'X-Stormrunner-Project-Roles',
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
        { id: 'role_headers', label: 'Role Header Mapping', status: 'pass', summary: 'Configured', detail: 'X-Stormrunner-Roles | X-Stormrunner-Project-Roles' },
        { id: 'current_request_identity', label: 'Current Request Identity', status: 'pass', summary: 'Trusted headers authenticated the current request.', detail: 'alice-admin' },
      ],
    })

    await act(async () => {
      root.render(
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
    expect(generalSection.textContent).toContain('Built-In Role Reference')
    expect(generalSection.textContent).toContain('Manage global settings')
    expect(generalSection.textContent).toContain('Global only')
    expect(generalSection.textContent).toContain('Route Access Matrix')
    expect(generalSection.textContent).toContain('System Configuration')
    expect(generalSection.textContent).toContain('Update system settings: Global: System Admin')
    expect(generalSection.textContent).toContain('Effective Access For Current Session')
    expect(generalSection.textContent).toContain('Tests and Scenarios')
    expect(generalSection.textContent).toContain('Allowed via project role')
    expect(generalSection.textContent).toContain('Microsoft Entra ID Gateway Contract')
    expect(generalSection.textContent).toContain('Required Subject Header')
    expect(generalSection.textContent).toContain('X-Auth-Request-User')
    expect(generalSection.textContent).toContain('Global Role Mapping')
    expect(generalSection.textContent).toContain('X-Stormrunner-Roles')
    expect(generalSection.textContent).toContain('Microsoft Entra ID Troubleshooting')
    expect(generalSection.textContent).toContain('Headers detected')
    expect(generalSection.textContent).toContain('Auth Diagnostics')
    expect(generalSection.textContent).toContain('Auth mode is ready with 1 advisory check(s) still worth reviewing.')
    expect(generalSection.textContent).toContain('Current Request Identity')
    expect(generalSection.textContent).toContain('Pass | alice-admin')
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
        client_id: 'stormrunner-dashboard',
        client_secret_env_var: 'STORMRUNNER_OIDC_CLIENT_SECRET',
        redirect_url: 'https://stormrunner.example.test/api/v1/auth/callback',
        scopes: ['openid', 'profile', 'email'],
        roles_claim: 'roles',
        project_roles_claim: 'project_roles',
        session_secret_env_var: 'STORMRUNNER_AUTH_SESSION_SECRET',
        logout_url: '/',
        login_url: '',
        user_header: 'X-Auth-Request-User',
        name_header: 'X-Auth-Request-Name',
        email_header: 'X-Auth-Request-Email',
        roles_header: 'X-Stormrunner-Roles',
        project_roles_header: 'X-Stormrunner-Project-Roles',
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
        { id: 'client_secret_env', label: 'Client Secret Environment Variable', status: 'fail', summary: 'Environment variable is not set on the controller.', detail: 'STORMRUNNER_OIDC_CLIENT_SECRET' },
        { id: 'issuer_discovery', label: 'OIDC Discovery', status: 'fail', summary: 'Stormrunner could not reach the issuer discovery document.', detail: 'dial tcp: lookup login.example.test: no such host' },
      ],
    })

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    expect(generalSection.textContent).toContain('Native OIDC Session Flow')
    expect(generalSection.textContent).toContain('Auth Diagnostics')
    expect(generalSection.textContent).toContain('Needs attention')
    expect(generalSection.textContent).toContain('Auth mode is not ready yet. Resolve 2 failing check(s) before relying on this configuration.')
    expect(generalSection.textContent).toContain('Client Secret Environment Variable')
    expect(generalSection.textContent).toContain('Action Required | STORMRUNNER_OIDC_CLIENT_SECRET')
    expect(generalSection.textContent).toContain('OIDC Discovery')
  })

  it('saves notification webhook settings through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const addWebhookButton = findButton('Add Webhook')
    expect(addWebhookButton).toBeDefined()
    if (!addWebhookButton) return

    await act(async () => {
      addWebhookButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const integrationsSection = container.querySelector('#settings-section-integrations')
    expect(integrationsSection).toBeTruthy()
    if (!integrationsSection) return

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

    await setFieldValue(nameInput, 'Slack Alerts')
    await setFieldValue(urlInput, 'https://hooks.slack.test/services/T000/B000/abc123')
    await setFieldValue(formatSelect, 'slack')

    const saveButton = findButton('Save Changes')
    expect(saveButton).toBeDefined()
    if (!saveButton) return

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.updateSystemSettings).toHaveBeenCalledWith(expect.objectContaining({
      notifications: {
        webhooks: [expect.objectContaining({
          name: 'Slack Alerts',
          enabled: true,
          url: 'https://hooks.slack.test/services/T000/B000/abc123',
          format: 'slack',
        })],
      },
    }))
    expect(container.textContent).toContain('Settings saved.')
  })

  it('saves Dynatrace notification metadata through system settings', async () => {
    apiMock.updateSystemSettings.mockImplementation(async (settings) => settings)

    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const addWebhookButton = findButton('Add Webhook')
    expect(addWebhookButton).toBeDefined()
    if (!addWebhookButton) return

    await act(async () => {
      addWebhookButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const integrationsSection = container.querySelector('#settings-section-integrations')
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

    const saveButton = findButton('Save Changes')
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

  it('validates trusted-header auth when no roles headers are configured', async () => {
    await act(async () => {
      root.render(<SettingsPage theme="dark" onThemeChange={vi.fn()} />)
    })

    await flushPromises()

    const generalSection = container.querySelector('#settings-section-general')
    expect(generalSection).toBeTruthy()
    if (!generalSection) return

    const authModeSelect = findSettingSelect(generalSection, 'Access Control Mode')
    expect(authModeSelect).toBeTruthy()
    if (!authModeSelect) return

    await setFieldValue(authModeSelect, 'trusted_headers')

    const authInputs = Array.from(generalSection.querySelectorAll('input')).filter(input => {
      const element = input as HTMLInputElement
      return element.placeholder === 'X-Stormrunner-Roles' || element.placeholder === 'X-Stormrunner-Project-Roles'
    }) as HTMLInputElement[]
    expect(authInputs).toHaveLength(2)
    if (authInputs.length !== 2) return

    await setFieldValue(authInputs[0], '')
    await setFieldValue(authInputs[1], '')

    expect(container.textContent).toContain('Trusted header auth requires a roles header or project roles header.')
    const saveButton = findButton('Save Changes')
    expect(saveButton?.disabled).toBe(true)
  })
})

function findButton(label: string) {
  return Array.from(document.querySelectorAll('button')).find(button => button.textContent?.trim() === label) as HTMLButtonElement | undefined
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
