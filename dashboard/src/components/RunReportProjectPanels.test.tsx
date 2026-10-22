// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import RunReportProjectPanels from './RunReportProjectPanels'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
let container: HTMLElement, root: Root
beforeEach(() => { request.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
const render = (canManage = false, projectId = 'project') => act(async () => root.render(<RunReportProjectPanels testId="run" projectId={projectId} history={[]} properties={[]} canManage={canManage} />))
it('permits viewer inspection without exposing authoring', async () => {
 request.mockResolvedValue({ dashboards: [], count: 0 })
 await render()
 expect(request.mock.calls[0][0]).toContain('project_id=project')
 expect(container.textContent).toContain('No project panels yet.')
 expect(container.querySelector('form')).toBeNull()
})
it('saves with the report project and opens the saved definition', async () => {
 const definition = { id: 'saved', name: 'Saved report panel', project_id: 'project', revision: 1, tabs: [] }
 request.mockImplementation(async (path: string, options?: RequestInit) => options?.method === 'POST' || path.endsWith('/saved') ? definition : { dashboards: [], count: 0 })
 await render(true)
 await act(async () => { container.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
 const saved = request.mock.calls.find(([, options]) => options?.method === 'POST')!
 expect(JSON.parse(saved[1].body)).toMatchObject({ project_id: 'project', visibility: 'project' })
 expect(saved[1].signal.aborted).toBe(false)
 expect(container.querySelector('select[aria-label="Project dashboard"]')?.textContent).toContain('Saved report panel')
})
it('distinguishes loading and denied reads from an empty project', async () => {
 let reject!: (error: Error) => void
 request.mockImplementation(() => new Promise((_resolve, no) => { reject = no }))
 await render(true)
 expect(container.textContent).toContain('Loading project panels…')
 expect(container.querySelector('form')).toBeNull()
 await act(async () => reject(new Error('Access denied')))
 expect(container.querySelector('[role=alert]')?.textContent).toBe('Access denied')
 expect(container.textContent).not.toContain('No project panels yet.')
 expect(container.querySelector('form')).toBeNull()
})
it('aborts old scope reads and ignores late results', async () => {
 let oldResolve!: (value: unknown) => void
 request.mockImplementationOnce(() => new Promise(resolve => { oldResolve = resolve })).mockResolvedValue({ dashboards: [], count: 0 })
 await render(false, 'old')
 const signal = request.mock.calls[0][1].signal as AbortSignal
 await render(false, 'new')
 expect(signal.aborted).toBe(true)
 await act(async () => oldResolve({ dashboards: [{ id: 'old', name: 'Foreign panel', tabs: [] }], count: 1 }))
 expect(container.textContent).not.toContain('Foreign panel')
 expect(container.textContent).toContain('No project panels yet.')
})
