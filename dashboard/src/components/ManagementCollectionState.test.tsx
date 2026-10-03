// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import ManagementCollectionState from './ManagementCollectionState'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
const state = { state: 'draining_unverified', poll_id: 'poll', collector_id: 'collector', collector_instance_id: 'instance', generation: '9007199254740993', grant_issued: true }
it('requires explicit process stop and preserves the exact ownership attestation', async () => {
  request.mockImplementation(async (_path: string, options?: { method?: string }) => options?.method === 'POST' ? { ...state, state: 'abandoned' } : state)
  await act(async () => root.render(<ManagementCollectionState projectId="project" sourceId="source" canManage />))
  const button = Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Attest process stop and release ownership')!
  expect(button.disabled).toBe(true)
  await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
  await act(async () => button.click())
  expect(request).toHaveBeenCalledWith('/projects/project/monitor-sources/source/management-state', expect.objectContaining({ method: 'POST', body: JSON.stringify({ poll_id: 'poll', collector_id: 'collector', collector_instance_id: 'instance', generation: '9007199254740993', process_stopped: true }) }))
})
it('viewers see ownership without process-stop controls', async () => {
  request.mockResolvedValue(state)
  await act(async () => root.render(<ManagementCollectionState projectId="project" sourceId="source" canManage={false} />))
  expect(container.textContent).toContain('draining_unverified')
  expect(container.textContent).toContain('9007199254740993')
  expect(container.querySelector('input')).toBeNull()
})
it('does not apply a delayed release response to a different source', async () => {
  let resolve: (value: unknown) => void = () => {}
  request.mockImplementation(async (_path: string, options?: { method?: string }) => options?.method === 'POST' ? new Promise(done => { resolve = done }) : state)
  await act(async () => root.render(<ManagementCollectionState projectId="project" sourceId="source" canManage />))
  await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
  await act(async () => Array.from(container.querySelectorAll('button')).find(item => item.textContent === 'Attest process stop and release ownership')!.click())
  await act(async () => root.render(<ManagementCollectionState projectId="other" sourceId="other" canManage={false} />))
  await act(async () => resolve({ ...state, state: 'abandoned' }))
  expect(container.querySelector('input')).toBeNull()
  expect(container.textContent).not.toContain('abandoned')
})
