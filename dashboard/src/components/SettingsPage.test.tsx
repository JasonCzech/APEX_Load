// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SettingsPage from './SettingsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getAuthEffectiveAccess: vi.fn(),
  getAuthAccessMatrix: vi.fn(),
  getAuthRoleCatalog: vi.fn(),
  getSystemSettings: vi.fn(),
  getHealth: vi.fn(),
  getSystemInventory: vi.fn(),
  updateSystemSettings: vi.fn(),
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
      notifications: {
        webhooks: [],
      },
      auth: {
        mode: 'disabled',
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
    apiMock.listDataPools.mockResolvedValue({ data_pools: [] })
    apiMock.getDataPool.mockResolvedValue({ rows: [] })
    apiMock.listTests.mockResolvedValue({ tests: [] })
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
        mode: 'disabled',
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

  it('shows the current authenticated identity and effective roles when trusted-header auth is enabled', async () => {
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