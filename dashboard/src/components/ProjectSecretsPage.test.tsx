// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Project } from '../types'
import ProjectSecretsPage from './ProjectSecretsPage'
import { PROJECT_SECRETS_SETTINGS_PROJECT_KEY } from './projectSecretsNavigation'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({ listProjectSecrets: vi.fn(), getProjectSecretUsage: vi.fn(), createProjectSecrets: vi.fn(), rotateProjectSecret: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useApi: () => apiMock }))

const project = { id: 'project-1', name: 'Retail' } as Project

describe('ProjectSecretsPage', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    vi.clearAllMocks()
    window.localStorage.clear()
    apiMock.listProjectSecrets.mockResolvedValue({ project_id: 'project-1', capabilities: { provider: 'encrypted_database', writable: true, create_supported: true, replace_supported: true }, secrets: [], count: 0 })
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const render = async (props: Partial<Parameters<typeof ProjectSecretsPage>[0]>) => {
    await act(async () => root.render(<ProjectSecretsPage activeProject={project} canCreate={false} canRotate={false} {...props} />))
    await act(async () => { await Promise.resolve() })
  }
  const createButton = () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Create secret') as HTMLButtonElement | undefined

  it('lets an editor add the active project’s secrets without the administrator Settings page', async () => {
    await render({ canCreate: true })
    expect(apiMock.listProjectSecrets).toHaveBeenCalledWith('project-1')
    expect(createButton()).toBeDefined()
    expect(container.textContent).toContain('Editors may create missing secrets. Rotation requires a project or system administrator.')
    expect(container.textContent).not.toContain('Administrator settings')
  })

  it('shows a viewer the inventory without the right to add', async () => {
    await render({})
    expect(container.textContent).toContain('Current session can view secret metadata but cannot create values.')
    expect(createButton()?.disabled).toBe(true)
  })

  it('never configures another project in place of the one a link named', async () => {
    window.localStorage.setItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, 'project-2')
    await render({ canCreate: true })
    expect(container.textContent).toContain('belong to another project')
    expect(apiMock.listProjectSecrets).not.toHaveBeenCalled()
    // The link is used once.
    expect(window.localStorage.getItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)).toBeNull()
  })

  it('keeps the link’s project through StrictMode’s second render', async () => {
    window.localStorage.setItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, 'project-2')
    await act(async () => root.render(<StrictMode><ProjectSecretsPage activeProject={project} canCreate canRotate={false} /></StrictMode>))
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('belong to another project')
    expect(apiMock.listProjectSecrets).not.toHaveBeenCalled()
  })

  it('opens the named project when it is the active one', async () => {
    window.localStorage.setItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY, 'project-1')
    await render({ canCreate: true })
    expect(apiMock.listProjectSecrets).toHaveBeenCalledWith('project-1')
  })

  it('asks for a project when none is active', async () => {
    await render({ activeProject: null })
    expect(container.textContent).toContain('Select a project to see its secrets.')
  })
})
