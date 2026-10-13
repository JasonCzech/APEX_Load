// @vitest-environment jsdom
import { act, useMemo } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuardRunner } from '../hooks/NavigationGuardProvider'
import { NavigationGuardProvider } from '../hooks/NavigationGuardRoot'
import { ProjectContext, type ProjectContextValue } from '../hooks/projectContext'
import SharedAssetsDialog from './SharedAssetsDialog'
import type { SharedAssetsPermissions } from './SharedAssetsPage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const { api, sectionProps, dirty } = vi.hoisted(() => ({
  api: { getAuthEffectiveAccess: vi.fn() },
  sectionProps: [] as Array<Record<string, unknown>>,
  dirty: { value: false },
}))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
// The section's panels are the page's own; here a stand-in that can hold unsaved work.
vi.mock('./SharedAssetsPage', () => ({
  SharedAssetSection: (props: Record<string, unknown>) => {
    sectionProps.push(props)
    const guard = useMemo(() => ({ id: 'stub-panel', label: 'Pool rows', isDirty: () => dirty.value, save: () => true, discard: () => { dirty.value = false } }), [])
    useNavigationGuard(guard, dirty.value)
    return <p data-testid="section">{String(props.section)}</p>
  },
}))

const project = { id: 'project-1', name: 'Checkout', domain_id: 'domain-1', created_at: '', updated_at: '' }
const access = (allowed: string[]) => ({ resources: [{ operations: ['manage_data_pools', 'manage_scripts', 'manage_projects'].map(id => ({ id, label: id, allowed: allowed.includes(id) })) }] })

describe('SharedAssetsDialog', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    sectionProps.length = 0
    dirty.value = false
    api.getAuthEffectiveAccess.mockReset().mockResolvedValue(access(['manage_data_pools', 'manage_scripts']))
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })
  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
  })
  const render = async (onClose = vi.fn(), resourceId?: string) => {
    const context = { projects: [project], activeProject: project } as unknown as ProjectContextValue
    await act(async () => root.render(<ProjectContext.Provider value={context}><SharedAssetsDialog projectId="project-1" section="test-data" resourceId={resourceId} onClose={onClose} /></ProjectContext.Provider>))
    await act(async () => { await Promise.resolve() })
    return onClose
  }
  const dialog = () => document.body.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]')!
  const button = (name: string) => [...document.body.querySelectorAll<HTMLButtonElement>('button')].find(item => item.textContent === name || item.getAttribute('aria-label') === name)!

  it('opens the section over the page with what the viewer may change there', async () => {
    await render(vi.fn(), 'pool-7')
    expect(dialog().getAttribute('aria-labelledby')).toBeTruthy()
    expect(dialog().textContent).toContain('Shared assets · Test data')
    expect(dialog().textContent).toContain('Project Checkout.')
    expect(api.getAuthEffectiveAccess).toHaveBeenCalledWith('project-1', { domain_id: 'domain-1' })
    const props = sectionProps[sectionProps.length - 1]
    expect(props).toMatchObject({ section: 'test-data', resourceId: 'pool-7', project: { id: 'project-1' } })
    expect(props.permissions as SharedAssetsPermissions).toEqual({ manageDataPools: true, manageScripts: true, manageProjects: false })
    // The full page, for more room, names the same row and project.
    expect(dialog().querySelector('a')!.getAttribute('href')).toBe('#/shared-assets?section=test-data&resourceId=pool-7&projectId=project-1')
  })

  it('lets the page’s navigation see the section’s unsaved work, as Back or a link in a panel would leave it', async () => {
    dirty.value = true
    let pageRunner!: NavigationGuardRunner
    const CapturePage = () => { pageRunner = useNavigationGuardRunner(); return null }
    const context = { projects: [project], activeProject: project } as unknown as ProjectContextValue
    const onClose = vi.fn()
    await act(async () => root.render(<NavigationGuardProvider><CapturePage /><ProjectContext.Provider value={context}><SharedAssetsDialog projectId="project-1" section="test-data" onClose={onClose} /></ProjectContext.Provider></NavigationGuardProvider>))
    await act(async () => { await Promise.resolve() })
    const leave = vi.fn()
    await act(async () => pageRunner.runGuarded(leave))
    expect(leave).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Pool rows')
    await act(async () => button('Discard changes').click())
    expect(leave).toHaveBeenCalledOnce()
    expect(onClose).not.toHaveBeenCalled()
  })

  it('waits for access, and stays read-only when it cannot be checked', async () => {
    let fail!: (cause: Error) => void
    api.getAuthEffectiveAccess.mockReturnValue(new Promise((_, reject) => { fail = reject }))
    await render()
    expect(dialog().textContent).toContain('Checking access…')
    expect(sectionProps).toHaveLength(0)
    await act(async () => { fail(new Error('offline')); await Promise.resolve() })
    expect(sectionProps[sectionProps.length - 1].permissions).toEqual({ manageDataPools: false, manageScripts: false, manageProjects: false })
  })

  it('closes with Done, Close or Escape', async () => {
    const onClose = await render()
    await act(async () => button('Done').click())
    await act(async () => button('Close').click())
    await act(async () => { document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })) })
    expect(onClose).toHaveBeenCalledTimes(3)
  })

  it('asks about the section’s unsaved work before closing, and only about that', async () => {
    dirty.value = true
    const onClose = await render()
    await act(async () => button('Done').click())
    expect(onClose).not.toHaveBeenCalled()
    expect(document.body.textContent).toContain('Pool rows')
    await act(async () => button('Discard changes').click())
    expect(onClose).toHaveBeenCalledOnce()
  })
})
