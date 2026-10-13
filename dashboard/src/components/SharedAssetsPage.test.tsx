// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { Application, Project } from '../types'
import SharedAssetsPage, { type SharedAssetsPermissions } from './SharedAssetsPage'
import { prepareProjectSecretsNavigation, PROJECT_SECRETS_SETTINGS_PROJECT_KEY } from './projectSecretsNavigation'
import type { SharedAssetsSection } from './sharedAssetsNavigation'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// Each section's panel is tested on its own; here, what the page hands it.
const { stub } = vi.hoisted(() => ({ stub: (name: string) => ({ default: (props: Record<string, unknown>) => <div data-testid={name} data-props={JSON.stringify(props, (_key, value) => typeof value === 'object' && value && 'created_at' in value ? (value as { id: string }).id : value)} /> }) }))
vi.mock('./DataPoolsManager', () => stub('pools'))
vi.mock('./ProjectLiveTablesPanel', () => stub('live-tables'))
vi.mock('./ProjectSecretsPanel', () => stub('secrets'))
vi.mock('./CorrelationRulesetPanel', () => stub('correlation-rules'))
vi.mock('./ObjectRepositoryPanel', () => stub('object-repositories'))
vi.mock('./SavedBlocksPanel', () => stub('saved-blocks'))

const project = { id: 'project-1', name: 'Retail', created_at: '', updated_at: '' } as Project
const applications = [
  { id: 'app-1', project_id: 'project-1', name: 'Checkout', created_at: '', updated_at: '' },
  { id: 'app-9', project_id: 'project-9', name: 'Elsewhere', created_at: '', updated_at: '' },
] as Application[]
const editor: SharedAssetsPermissions = { manageDataPools: true, manageScripts: true, manageProjects: false }
const viewer: SharedAssetsPermissions = { manageDataPools: false, manageScripts: false, manageProjects: false }

describe('SharedAssetsPage', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    window.localStorage.clear()
    window.location.hash = '#/shared-assets?projectId=project-1'
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    window.location.hash = ''
  })
  const render = async (props: { section?: SharedAssetsSection; resourceId?: string; permissions?: SharedAssetsPermissions; activeProject?: Project | null } = {}) => {
    await act(async () => root.render(<SharedAssetsPage activeProject={'activeProject' in props ? props.activeProject : project} applications={applications} section={props.section} resourceId={props.resourceId} permissions={props.permissions ?? editor} />))
  }
  const propsOf = (name: string) => JSON.parse(container.querySelector(`[data-testid="${name}"]`)!.getAttribute('data-props')!) as Record<string, unknown>

  it('lists every section, marks the open one and says how scripts read it', async () => {
    await render({ section: 'live-tables' })
    const links = [...container.querySelectorAll<HTMLAnchorElement>('nav[aria-label="Shared assets"] a')]
    expect(links.map(link => link.textContent)).toEqual(['Test data', 'Live tables', 'Secrets', 'Correlation rules', 'Object repositories', 'Saved blocks'])
    expect(links.map(link => link.getAttribute('href'))).toEqual(['test-data', 'live-tables', 'secrets', 'correlation-rules', 'object-repositories', 'saved-blocks'].map(section => `#/shared-assets?section=${section}&projectId=project-1`))
    expect(links.find(link => link.getAttribute('aria-current') === 'page')?.textContent).toBe('Live tables')
    expect(container.querySelector('h2')?.textContent).toBe('Live tables')
    // Outside the Settings-style heading, which only screen readers get.
    expect(container.querySelector('.settings-task-heading')?.textContent).toBe('Live tables')
    expect(container.querySelector('#shared-assets-usage')?.textContent).toContain('{{data.alias.column}}')
    expect(container.querySelector('section')?.getAttribute('aria-describedby')).toBe('shared-assets-usage')
    // The panels of the other kinds say what they hold; nothing repeats it.
    await render({ section: 'object-repositories' })
    expect(container.querySelector('#shared-assets-usage')).toBeNull()
    expect(container.querySelector('section')?.hasAttribute('aria-describedby')).toBe(false)
  })

  it('opens Test data first', async () => {
    await render()
    expect(container.querySelector('h2')?.textContent).toBe('Test data')
    expect(propsOf('pools')).toMatchObject({ activeProject: 'project-1', canManage: true })
  })

  it('hands each section the row the address names and what the role may change', async () => {
    await render({ section: 'test-data', resourceId: 'pool-1', permissions: viewer })
    expect(propsOf('pools')).toMatchObject({ preferredPoolId: 'pool-1', canManage: false })
    await render({ section: 'live-tables', resourceId: 'table-1' })
    expect(propsOf('live-tables')).toMatchObject({ projectId: 'project-1', selectedId: 'table-1', canCreate: true })
    await render({ section: 'correlation-rules', permissions: viewer })
    expect(propsOf('correlation-rules')).toMatchObject({ projectId: 'project-1', canManage: false })
    await render({ section: 'object-repositories' })
    expect(propsOf('object-repositories')).toMatchObject({ projectId: 'project-1', canManage: true })
    await render({ section: 'saved-blocks', permissions: viewer })
    expect(propsOf('saved-blocks')).toMatchObject({ projectId: 'project-1', canManage: false })
  })

  it('lets editors add secrets and project admins rotate them', async () => {
    await render({ section: 'secrets' })
    expect(propsOf('secrets')).toMatchObject({ projectId: 'project-1', canCreate: true, canRotate: false })
    await render({ section: 'secrets', permissions: { ...editor, manageProjects: true } })
    expect(propsOf('secrets')).toMatchObject({ canCreate: true, canRotate: true })
  })

  it('shows the project’s secrets or one of its applications’', async () => {
    await render({ section: 'secrets' })
    const scope = container.querySelector<HTMLSelectElement>('.shared-assets-scope select')!
    expect([...scope.options].map(option => option.textContent)).toEqual(['Project Retail', 'Application Checkout'])
    expect(propsOf('secrets').applicationId).toBeUndefined()
    await act(async () => { scope.value = 'app-1'; scope.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(propsOf('secrets')).toMatchObject({ applicationId: 'app-1', applicationName: 'Checkout' })
  })

  it('opens an application’s secrets from a link that names it', async () => {
    await render({ section: 'secrets', resourceId: 'app-1' })
    expect(propsOf('secrets')).toMatchObject({ applicationId: 'app-1' })
    expect(container.querySelector<HTMLSelectElement>('.shared-assets-scope select')!.value).toBe('app-1')
  })

  it('opens the named application once the applications load after the page', async () => {
    const renderWith = (apps: Application[]) => act(async () => root.render(<SharedAssetsPage activeProject={project} applications={apps} section="secrets" resourceId="app-1" permissions={editor} />))
    await renderWith([])
    expect(propsOf('secrets').applicationId).toBeUndefined()
    await renderWith(applications)
    expect(propsOf('secrets')).toMatchObject({ applicationId: 'app-1', applicationName: 'Checkout' })
    // The viewer's own choice then holds.
    const scope = container.querySelector<HTMLSelectElement>('.shared-assets-scope select')!
    await act(async () => { scope.value = ''; scope.dispatchEvent(new Event('change', { bubbles: true })) })
    await renderWith([...applications])
    expect(propsOf('secrets').applicationId).toBeUndefined()
  })

  it('never configures another project’s secrets in place of the one a link named, StrictMode included', async () => {
    prepareProjectSecretsNavigation('project-2')
    await act(async () => root.render(<StrictMode><SharedAssetsPage activeProject={project} section="secrets" permissions={editor} /></StrictMode>))
    expect(container.textContent).toContain('belong to another project')
    expect(container.querySelector('[data-testid="secrets"]')).toBeNull()
    // The link is used once.
    expect(window.localStorage.getItem(PROJECT_SECRETS_SETTINGS_PROJECT_KEY)).toBeNull()
  })

  it('opens the named project’s secrets when it is the active one', async () => {
    prepareProjectSecretsNavigation('project-1')
    await render({ section: 'secrets' })
    expect(container.querySelector('[data-testid="secrets"]')).not.toBeNull()
  })

  it('asks for a project when none is active', async () => {
    await render({ activeProject: null })
    expect(container.textContent).toContain('Select a project to see what its scripts and scenarios share.')
    expect(container.querySelector('[data-testid="pools"]')).toBeNull()
  })
})
