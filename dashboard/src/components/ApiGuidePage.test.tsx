// @vitest-environment jsdom

import { act, type ComponentProps } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import endpointCatalogRaw from '../../../docs/api-endpoint-catalog.json?raw'

import { loadApiEndpointCatalog } from '../apiDocsClient'
import { parseEndpointCatalog } from '../apiEndpointCatalog'
import { useActiveTopbarContribution } from '../hooks/TopbarContributionProvider'
import { TopbarContributionProvider } from '../hooks/TopbarContributionRoot'
import ApiGuidePage from './ApiGuidePage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../apiDocsClient', () => ({
  loadApiEndpointCatalog: vi.fn(),
}))

const endpointCatalogFixture = parseEndpointCatalog(endpointCatalogRaw)
const loadApiEndpointCatalogMock = vi.mocked(loadApiEndpointCatalog)

function accessResources(operationIDs: string[]) {
  return [{
    id: 'test',
    name: 'Test',
    description: 'Test operations',
    operations: operationIDs.map(id => ({ id, label: id, allowed: true })),
  }]
}

const allGuideOperations = accessResources([
  'view_projects',
  'manage_projects',
  'manage_repository_projects',
  'view_applications',
  'manage_applications',
  'manage_repository_releases',
  'view_scripts',
  'manage_scripts',
  'view_tests',
  'manage_tests',
  'view_data_pools',
  'manage_data_pools',
  'view_replay',
  'manage_replay',
  'view_system',
  'manage_system',
  'view_domains',
  'manage_domains',
  'view_application_repository',
  'manage_application_repository',
  'view_audit',
  'view_auth_diagnostics',
  'view_user_activity',
  'manage_users',
  'manage_user_bindings',
  'manage_service_accounts',
])

function TopbarContributionHarness() {
  const contribution = useActiveTopbarContribution()
  return (
    <div data-testid="topbar-contribution">
      {contribution?.titleNode || contribution?.title}
      {contribution?.controls}
    </div>
  )
}

describe('ApiGuidePage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.history.replaceState(null, '', '/')
    loadApiEndpointCatalogMock.mockReset()
    loadApiEndpointCatalogMock.mockResolvedValue(endpointCatalogFixture)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  async function renderApiGuidePage(props: ComponentProps<typeof ApiGuidePage> = {}) {
    await act(async () => {
      root.render(
        <TopbarContributionProvider>
          <TopbarContributionHarness />
          <ApiGuidePage {...props} />
        </TopbarContributionProvider>,
      )
    })
    await act(async () => {})
  }

  function topbarText() {
    return container.querySelector('[data-testid="topbar-contribution"]')?.textContent || ''
  }

  function findTopbarButton(label: string) {
    const topbar = container.querySelector('[data-testid="topbar-contribution"]')
    return Array.from(topbar?.querySelectorAll('button') || []).find(button => button.textContent?.trim() === label)
  }

  async function selectTopbarPage(label: string) {
    const button = findTopbarButton(label)
    expect(button).toBeDefined()
    await act(async () => {
      button?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    return button
  }

  it('renders the API guide title toggle and General page by default', async () => {
    await renderApiGuidePage({ effectiveAccessResources: allGuideOperations })

    expect(container.textContent).toContain('API Guide')
    expect(topbarText()).toContain('API Guide')
    expect(topbarText()).toContain('General')
    expect(topbarText()).toContain('MCP Server')
    expect(topbarText()).toContain('Quick Start')
    expect(topbarText()).toContain('Authentication')
    expect(topbarText()).toContain('Endpoint Map')
    expect(topbarText()).not.toContain('Response/Errors')
    expect(topbarText()).toContain('Swagger')
    expect(topbarText()).toContain('Repo Guide')
    expect(container.textContent).toContain('http://<controller-host>:8080/api/v1')
    expect(container.textContent).toContain('Responses And Errors')
    expect(container.textContent).toContain('script_shakeout_failed')
    expect(container.textContent).toContain('Dev Preview Headers')
    expect(container.textContent).toContain('X-APEXLoad-Dev-View')
    expect(container.textContent).toContain('project editor User behavior')
    expect(container.textContent).not.toContain('/api/v1/auth/me')
    expect(container.textContent).not.toContain('python3 mcp/server.py')
    expect(container.textContent).not.toContain('/api/v1/scripts/import/postman')

    expect(container.querySelector('.api-guide-header-actions')).toBeNull()
    expect(container.querySelector('a[href="#/repository-guide"]')).toBeNull()
    expect(container.querySelector('a[href="#/swagger"]')).toBeNull()
  })

  it('swaps API guide pages from the title toggle and keeps companion pages routed', async () => {
    await renderApiGuidePage({ effectiveAccessResources: allGuideOperations })

    const mcpButton = await selectTopbarPage('MCP Server')
    expect(mcpButton?.getAttribute('aria-pressed')).toBe('true')
    expect(window.location.hash).toBe('#/docs?guide=mcp')
    expect(container.textContent).toContain('python3 mcp/server.py')
    expect(container.textContent).toContain('apexload://api-guide')
    expect(container.textContent).toContain('confirm=true')
    expect(container.textContent).not.toContain('Responses And Errors')
    const mcpLink = container.querySelector('a[href*="mcp/README.md"]')
    expect(mcpLink?.textContent).toContain('MCP setup guide')

    const quickStartButton = await selectTopbarPage('Quick Start')
    expect(quickStartButton?.getAttribute('aria-pressed')).toBe('true')
    expect(window.location.hash).toBe('#/docs?guide=quick-start')
    expect(container.textContent).toContain('curl http://localhost:8080/api/v1/system/health')
    expect(container.textContent).not.toContain('python3 mcp/server.py')

    await selectTopbarPage('Endpoint Map')
    expect(container.textContent).toContain('/api/v1/scripts/import/enterprise-script-archive')
    expect(container.textContent).toContain('/api/v1/scripts/import/jmeter/save')
    expect(container.textContent).toContain('/api/v1/scripts/import/oracle-ebusiness/review')
    expect(container.textContent).toContain('/api/v1/scripts/authoring/sessions')
    expect(container.textContent).toContain('/api/v1/scenarios/validate-load-model')
    expect(container.textContent).toContain('/api/v1/scenarios/load-models/templates')
    expect(container.textContent).toContain('/api/v1/scenarios/{id}/schedules')
    expect(container.textContent).toContain('/api/v1/data-pools/{id}/rows')
    expect(container.textContent).toContain('/api/v1/replay/sessions/{session_id}')
    expect(container.textContent).toContain('/api/v1/recorder/start')
    expect(container.textContent).toContain('/api/v1/integrations/github/diagnostics')
    expect(container.textContent).toContain('/api/v1/integrations/github/installations')
    expect(container.textContent).toContain('/api/v1/projects/{id}/github/policy')
    expect(container.textContent).toContain('/api/v1/scripts/import/github')
    expect(container.textContent).toContain('/api/v1/scripts/{id}/github/status')
    expect(container.textContent).toContain('/api/v1/integrations/github/sync-jobs/{id}')
    expect(container.textContent).toContain('/api/v1/integrations/github/webhook')
    expect(container.textContent).toContain('/api/v1/tests/{id}/github/check-run')
    expect(container.textContent).toContain('/api/v1/integrations/github/check-links')
    expect(container.textContent).toContain('/api/v1/library/remotes/{id}/pull')
    expect(container.textContent).toContain('/api/v1/fleet/provisioner')
    expect(container.textContent).toContain('/api/v1/fleet/load-zones')
    expect(container.textContent).toContain('/api/v1/execution-plans')
    expect(container.textContent).toContain('/api/v1/capacity/timeslots')
    expect(container.textContent).toContain('/api/v1/object-repositories')
    expect(container.textContent).toContain('/api/v1/correlation-rulesets')
    expect(container.textContent).toContain('/api/v1/downloads/recording-agent/latest')
    expect(container.textContent).toContain('/api/v1/system/settings')
    expect(container.textContent).toContain('/api/v1/protocols/native-support-matrix')
    expect(container.textContent).toContain('/api/v1/ai/review-queue')

    await selectTopbarPage('Authentication')
    expect(window.location.hash).toBe('#/docs?guide=authentication')
    expect(container.textContent).toContain('/api/v1/auth/me')
    expect(container.textContent).toContain('/api/v1/auth/diagnostics')
    expect(container.textContent).toContain('/api/v1/auth/service-accounts')
    expect(container.textContent).toContain('/api/v1/auth/users')
    expect(container.textContent).toContain('/api/v1/auth/users/onboard')
    expect(container.textContent).toContain('/api/v1/auth/users/{id}/bindings')
    expect(container.textContent).toContain('/api/v1/auth/users/{id}/revoke-sessions')
    expect(container.textContent).toContain('/api/v1/auth/user-activity')
    expect(container.textContent).toContain('/api/v1/auth/user-activity/export')
    expect(container.textContent).toContain('active local APEX Load user record')
    expect(container.textContent).toContain('Human roles come from local bindings')

    await selectTopbarPage('Swagger')
    expect(window.location.hash).toBe('#/swagger')

    await selectTopbarPage('Repo Guide')
    expect(window.location.hash).toBe('#/repository-guide')
  })

  it('links access guidance to the Access page when dashboard navigation is available', async () => {
    const onNavigate = vi.fn()
    await renderApiGuidePage({ onNavigate, effectiveAccessResources: allGuideOperations })
    await selectTopbarPage('Authentication')

    const accessButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Access'))
    expect(accessButton).toBeDefined()
    if (!accessButton) return

    await act(async () => {
      accessButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('access')
  })

  it('filters endpoint rows to the current effective access', async () => {
    await renderApiGuidePage({ onNavigate: vi.fn(), effectiveAccessResources: accessResources(['view_scripts']) })
    await selectTopbarPage('Endpoint Map')

    const codeValues = Array.from(container.querySelectorAll('code')).map(node => node.textContent || '')

    expect(codeValues).toContain('GET /api/v1/scripts')
    expect(codeValues).toContain('POST /api/v1/scripts/validate')
    expect(codeValues).toContain('GET /api/v1/scripts/web-api/support-matrix')
    expect(codeValues).toContain('GET /api/v1/scripts/authoring/templates')
    expect(codeValues).toContain('GET /api/v1/integrations/github/config')
    expect(codeValues).toContain('GET /api/v1/integrations/github/diagnostics')
    expect(codeValues).toContain('GET /api/v1/integrations/github/repos')
    expect(codeValues).toContain('GET /api/v1/scripts/{id}/github/status')
    expect(codeValues).toContain('GET /api/v1/integrations/github/sync-jobs')
    expect(codeValues).toContain('GET /api/v1/replay/sessions/{session_id}')
    expect(codeValues).toContain('GET /api/v1/runtime-settings/support-matrix')
    expect(codeValues).not.toContain('POST /api/v1/scripts')
    expect(codeValues).not.toContain('POST /api/v1/scripts/import/enterprise-script-archive')
    expect(codeValues).not.toContain('POST /api/v1/scripts/import/github')
    expect(codeValues).not.toContain('POST /api/v1/scripts/{id}/github/push')
    expect(codeValues).not.toContain('PUT /api/v1/projects/{id}/github/policy')
    expect(container.textContent).not.toContain('/api/v1/scenarios')
    expect(codeValues).not.toContain('POST /api/v1/library/remotes')
    expect(container.textContent).toContain('filtered using your current effective operations')

    await selectTopbarPage('Authentication')
    expect(container.textContent).toContain('/api/v1/auth/me')
    expect(container.textContent).not.toContain('/api/v1/auth/users')
    expect(container.textContent).not.toContain('/api/v1/auth/user-activity')
    expect(container.textContent).not.toContain('Access Workspace')
    expect(container.textContent).not.toContain('Open Access')
    expect(topbarText()).toContain('Repo Guide')
    expect(topbarText()).toContain('Swagger')
    expect(container.querySelector('a[href="#/swagger"]')).toBeNull()
  })

  it('shows binding endpoints without lifecycle endpoints for delegated user-binding access', async () => {
    const onNavigate = vi.fn()
    await renderApiGuidePage({ onNavigate, effectiveAccessResources: accessResources(['manage_user_bindings']) })
    await selectTopbarPage('Authentication')

    const authEndpointRows = Array.from(container.querySelectorAll('.api-guide-table-card tbody tr')).map(row => {
      const codes = Array.from(row.querySelectorAll('code')).map(node => node.textContent || '')
      return `${codes[0]} ${codes[1]}`
    })
    expect(authEndpointRows).toContain('GET /api/v1/auth/users')
    expect(authEndpointRows).toContain('GET /api/v1/auth/users/{id}')
    expect(authEndpointRows).toContain('POST /api/v1/auth/users/{id}/bindings')
    expect(authEndpointRows).toContain('DELETE /api/v1/auth/users/{id}/bindings/{binding_id}')
    expect(authEndpointRows).not.toContain('POST /api/v1/auth/users')
    expect(authEndpointRows).not.toContain('PATCH /api/v1/auth/users/{id}')
    expect(authEndpointRows).not.toContain('POST /api/v1/auth/users/{id}/disable')
    expect(authEndpointRows).not.toContain('POST /api/v1/auth/users/{id}/enable')
    expect(container.textContent).toContain('Access Workspace')
    expect(container.textContent).toContain('Open Access')
  })

  it('renders distinct access loading and error states for filtered guide sections', async () => {
    await renderApiGuidePage({
      activePage: 'endpoint-map',
      effectiveAccessResources: [],
      effectiveAccessLoaded: false,
    })

    expect(container.textContent).toContain('Checking Access')
    expect(container.querySelector('.api-guide-endpoint-grid')).toBeNull()

    await renderApiGuidePage({
      activePage: 'endpoint-map',
      effectiveAccessResources: [],
      effectiveAccessLoaded: true,
      effectiveAccessError: true,
    })

    expect(container.textContent).toContain('Unable To Filter Access')
    expect(container.textContent).toContain('/api/v1/system/health')
  })

  it('shows a controller docs error when the endpoint catalog cannot be loaded', async () => {
    loadApiEndpointCatalogMock.mockRejectedValueOnce(new Error('catalog route unavailable'))

    await renderApiGuidePage({
      activePage: 'endpoint-map',
      effectiveAccessResources: allGuideOperations,
    })

    expect(container.textContent).toContain('API Docs Unavailable')
    expect(container.textContent).toContain('catalog route unavailable')
    expect(container.querySelector('.api-guide-endpoint-grid')).toBeNull()
  })

  it('shows the User Guide tab to everyone and renders its content', async () => {
    await renderApiGuidePage({ effectiveAccessResources: accessResources(['view_scripts']) })

    expect(topbarText()).toContain('User Guide')
    expect(findTopbarButton('Admin Guide')).toBeUndefined()

    const userGuideButton = await selectTopbarPage('User Guide')
    expect(userGuideButton?.getAttribute('aria-pressed')).toBe('true')
    expect(window.location.hash).toBe('#/docs?guide=user-guide')
    expect(container.textContent).toContain('What APEX Load Does')
    expect(container.textContent).not.toContain('Responses And Errors')
  })

  it('hides the Admin Guide tab from non-admins and guards direct navigation', async () => {
    await renderApiGuidePage({ activePage: 'admin-guide', effectiveAccessResources: accessResources(['view_scripts']) })

    expect(findTopbarButton('Admin Guide')).toBeUndefined()
    // Direct navigation to the admin guide falls back to the default General page.
    expect(container.textContent).toContain('Responses And Errors')
    expect(container.textContent).not.toContain('The Access Console')
  })

  it('shows the Admin Guide tab to administrators and renders its content', async () => {
    await renderApiGuidePage({ effectiveAccessResources: accessResources(['manage_users']) })

    expect(topbarText()).toContain('Admin Guide')

    const adminButton = await selectTopbarPage('Admin Guide')
    expect(adminButton?.getAttribute('aria-pressed')).toBe('true')
    expect(window.location.hash).toBe('#/docs?guide=admin-guide')
    expect(container.textContent).toContain('The Access Console')
    expect(container.textContent).not.toContain('Responses And Errors')
  })

  it('shows the Admin Guide tab for system administrators without user-management access', async () => {
    await renderApiGuidePage({ effectiveAccessResources: accessResources(['manage_system']) })

    expect(topbarText()).toContain('Admin Guide')
  })
})
