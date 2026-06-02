// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ApiGuidePage from './ApiGuidePage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

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
  'manage_service_accounts',
])

describe('ApiGuidePage', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
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

  it('renders the API guide entry points and endpoint catalogue', async () => {
    await act(async () => {
      root.render(<ApiGuidePage effectiveAccessResources={allGuideOperations} />)
    })

    expect(container.textContent).toContain('API Guide')
    expect(container.textContent).toContain('http://<controller-host>:8080/api/v1')
    expect(container.textContent).toContain('/api/v1/auth/me')
    expect(container.textContent).toContain('/api/v1/auth/diagnostics')
    expect(container.textContent).toContain('/api/v1/auth/service-accounts')
    expect(container.textContent).toContain('/api/v1/auth/users')
    expect(container.textContent).toContain('/api/v1/auth/users/{id}/bindings')
    expect(container.textContent).toContain('/api/v1/auth/user-activity')
    expect(container.textContent).toContain('/api/v1/auth/user-activity/export')
    expect(container.textContent).toContain('/api/v1/scripts/import/loadrunner')
    expect(container.textContent).toContain('/api/v1/scripts/import/jmeter/save')
    expect(container.textContent).toContain('/api/v1/scripts/import/oracle-ebusiness/review')
    expect(container.textContent).toContain('/api/v1/scripts/authoring/sessions')
    expect(container.textContent).toContain('/api/v1/scenarios/validate-load-model')
    expect(container.textContent).toContain('/api/v1/scenarios/load-models/templates')
    expect(container.textContent).toContain('/api/v1/scenarios/{id}/schedules')
    expect(container.textContent).toContain('/api/v1/data-pools/{id}/rows')
    expect(container.textContent).toContain('/api/v1/replay/sessions/{session_id}')
    expect(container.textContent).toContain('/api/v1/recorder/start')
    expect(container.textContent).toContain('/api/v1/library/remotes/{id}/pull')
    expect(container.textContent).toContain('/api/v1/fleet/provisioner')
    expect(container.textContent).toContain('/api/v1/system/settings')
    expect(container.textContent).toContain('/api/v1/protocols/native-support-matrix')
    expect(container.textContent).toContain('/api/v1/ai/review-queue')
    expect(container.textContent).toContain('script_shakeout_failed')
    expect(container.textContent).toContain('MCP Server')
    expect(container.textContent).toContain('python3 mcp/server.py')
    expect(container.textContent).toContain('apexload://api-guide')
    expect(container.textContent).toContain('confirm=true')
    expect(container.textContent).toContain('Access Workspace')
    expect(container.textContent).toContain('active local APEX Load user record')
    expect(container.textContent).toContain('Human roles come from local bindings')
    expect(container.textContent).toContain('X-APEXLoad-Dev-View')
    expect(container.textContent).toContain('project editor User behavior')
    expect(container.textContent).toContain('/api/v1/scripts/import/postman')

    const repositoryLink = container.querySelector('a[href*="API_USER_GUIDE.md"]')
    expect(repositoryLink?.textContent).toContain('Repository Guide')
    const mcpLink = container.querySelector('a[href*="mcp/README.md"]')
    expect(mcpLink?.textContent).toContain('MCP setup guide')
  })

  it('links access guidance to the Access page when dashboard navigation is available', async () => {
    const onNavigate = vi.fn()
    await act(async () => {
      root.render(<ApiGuidePage onNavigate={onNavigate} effectiveAccessResources={allGuideOperations} />)
    })

    const accessButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Access'))
    expect(accessButton).toBeDefined()
    if (!accessButton) return

    await act(async () => {
      accessButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onNavigate).toHaveBeenCalledWith('access')
  })

  it('filters endpoint rows to the current effective access', async () => {
    await act(async () => {
      root.render(<ApiGuidePage onNavigate={vi.fn()} effectiveAccessResources={accessResources(['view_scripts'])} />)
    })

    const codeValues = Array.from(container.querySelectorAll('code')).map(node => node.textContent || '')

    expect(container.textContent).toContain('/api/v1/auth/me')
    expect(codeValues).toContain('GET /api/v1/scripts')
    expect(codeValues).toContain('POST /api/v1/scripts/validate')
    expect(codeValues).toContain('GET /api/v1/scripts/web-api/support-matrix')
    expect(codeValues).toContain('GET /api/v1/scripts/authoring/templates')
    expect(codeValues).toContain('GET /api/v1/replay/sessions/{session_id}')
    expect(codeValues).toContain('GET /api/v1/runtime-settings/support-matrix')
    expect(codeValues).not.toContain('POST /api/v1/scripts')
    expect(codeValues).not.toContain('POST /api/v1/scripts/import/loadrunner')
    expect(container.textContent).not.toContain('/api/v1/auth/users')
    expect(container.textContent).not.toContain('/api/v1/auth/user-activity')
    expect(container.textContent).not.toContain('/api/v1/scenarios')
    expect(codeValues).not.toContain('POST /api/v1/library/remotes')
    expect(container.textContent).not.toContain('Access Workspace')
    expect(container.textContent).not.toContain('Open Access')
    expect(container.textContent).not.toContain('Repository Guide')
    expect(container.textContent).toContain('filtered to your current domain, project, and role access')
  })
})
