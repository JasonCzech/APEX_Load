// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import RunPolicySelector from './RunPolicySelector'
import type { RunAnalysisPolicySelections } from './runPolicySelection'
const request = vi.hoisted(() => vi.fn())
vi.mock('../../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root
let container: HTMLElement
beforeEach(() => {
  request.mockReset()
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
  request.mockImplementation(async (path: string) => {
    const query = new URL(path, 'http://fixture').searchParams
    return query.get('kind') === 'browser_policy' ? [{ id: 'browser', project_id: query.get('project_id'), kind: 'browser_policy', name: 'Browser choice', revision: 3, deleted: false }] : []
  })
})
afterEach(() => { act(() => root.unmount()); container.remove() })
async function open() {
  await act(async () => {
    const details = container.querySelector('details')!
    details.open = true
    details.dispatchEvent(new Event('toggle', { bubbles: true }))
  })
}

it('keeps default forms independent of policy fetching and allows exact older revisions', async () => {
  const changed = vi.fn()
  function Harness() {
    const [value, setValue] = useState<RunAnalysisPolicySelections>()
    return <RunPolicySelector projectId="project" value={value} onChange={next => { changed(next); setValue(next) }} />
  }
  await act(async () => root.render(<Harness />))
  expect(request).not.toHaveBeenCalled()
  await open()
  const select = container.querySelector('select')!
  await act(async () => { select.value = 'browser'; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(changed).toHaveBeenLastCalledWith({ browser: { id: 'browser', revision: 3 } })
  const input = container.querySelector('input')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, '1')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  expect(changed).toHaveBeenLastCalledWith({ browser: { id: 'browser', revision: 1 } })
  await act(async () => { select.value = ''; select.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(changed).toHaveBeenLastCalledWith({})
})

it('retains an unavailable choice until the user explicitly clears it', async () => {
  const changed = vi.fn()
  await act(async () => root.render(<RunPolicySelector projectId="project" value={{ browser: { id: 'deleted', revision: 2 } }} onChange={changed} />))
  await open()
  expect(container.textContent).toContain('The selected browser policy is unavailable.')
  expect(container.querySelector('select')!.value).toBe('deleted')
  expect(changed).not.toHaveBeenCalled()
})

it('discloses loading and refuses to install a late list from another project', async () => {
  const releases: Array<(value: unknown[]) => void> = []
  request.mockImplementation((path: string) => path.includes('project_id=old') ? new Promise<unknown[]>(resolve => { releases.push(resolve) }) : Promise.resolve([]))
  const changed = vi.fn()
  await act(async () => root.render(<RunPolicySelector projectId="old" value={undefined} onChange={changed} />))
  await open()
  expect(container.querySelector('[role=status]')?.textContent).toBe('Loading analysis policies…')
  await act(async () => root.render(<RunPolicySelector projectId="new" value={undefined} onChange={changed} />))
  await act(async () => {
    expect(releases).toHaveLength(2)
    for (const release of releases) release([{ id: 'old-policy', project_id: 'old', kind: 'browser_policy', name: 'Do not install', revision: 1 }])
  })
  expect(container.textContent).toContain('No analysis policies yet.')
  expect(container.textContent).not.toContain('Do not install')
  expect(changed).not.toHaveBeenCalled()
})
