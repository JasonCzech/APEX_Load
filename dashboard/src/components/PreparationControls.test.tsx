// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import PreparationControls from './PreparationControls'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })

it('starts with the ready preparation identity and expected execution revision', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { preparation_enabled: true } : { id: 'prep', state: 'ready', execution_revision: 'fenced-script-hash', expires_at: '2026-01-01T00:00:00Z' })
  await act(async () => root.render(<PreparationControls projectId="p" testId="run" status="INITIALIZING" canEdit />))
  const button = [...container.querySelectorAll('button')].find(b => b.textContent === 'Start prepared run')!
  expect(button).toBeTruthy()
  await act(async () => button.click())
  expect(request).toHaveBeenCalledWith('/tests/run/start', expect.objectContaining({ method: 'POST', body: JSON.stringify({ preparation_id: 'prep', expected_preparation_revision: 'fenced-script-hash' }) }))
  expect(container.textContent).toContain('consumed')
})

it('keeps preparation details readable without exposing viewer mutation controls', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { preparation_enabled: true } : { id: 'prep', state: 'ready', execution_revision: 'hash' })
  await act(async () => root.render(<PreparationControls projectId="p" testId="run" status="INITIALIZING" />))
  expect(container.textContent).toContain('ready'); expect(container.querySelectorAll('button')).toHaveLength(0)
})

it('hides unavailable project capability', async () => {
  request.mockResolvedValue({ preparation_enabled: false })
  await act(async () => root.render(<PreparationControls projectId="p" testId="run" status="QUEUED" canEdit />))
  expect(container.textContent).toBe(''); expect(request).toHaveBeenCalledTimes(1)
})

it('clears a pending operation when switching to another run', async () => {
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return { preparation_enabled: true }
    if (options?.method === 'POST') return new Promise(() => {})
    return { state: 'none' }
  })
  await act(async () => root.render(<PreparationControls projectId="p" testId="one" status="QUEUED" canEdit />))
  await act(async () => container.querySelector('button')!.click())
  expect(container.querySelector('button')!.disabled).toBe(true)
  await act(async () => root.render(<PreparationControls projectId="p" testId="two" status="QUEUED" canEdit />))
  expect(container.querySelector('button')!.disabled).toBe(false)
})

it('submits explicit ephemeral capacity and spot limits', async () => {
  request.mockImplementation(async (path: string, options?: RequestInit) => path.includes('capabilities') ? { preparation_enabled: true } : options?.method === 'POST' ? { state: 'preparing' } : { state: 'none' })
  await act(async () => root.render(<PreparationControls projectId="p" testId="run" status="QUEUED" canEdit />))
  const selection = container.querySelector('select')!
  await act(async () => { selection.value = 'ephemeral'; selection.dispatchEvent(new Event('change', { bubbles: true })) })
  const spot = container.querySelectorAll('select')[1]
  await act(async () => { spot.value = 'required'; spot.dispatchEvent(new Event('change', { bubbles: true })) })
  const button = [...container.querySelectorAll('button')].find(b => b.textContent === 'Prepare')!
  await act(async () => button.click())
  const posted = request.mock.calls.find(([, opts]) => opts?.method === 'POST')!
  expect(JSON.parse(posted[1].body)).toMatchObject({ capacity: { selection: 'ephemeral', spot: 'required', spot_ceiling_percent: 50 } })
})

it('shows immutable requested and admitted processor resources on a prepared run', async () => {
  const claim = { contract: 'eventhubs_processor_reservations_v1', protocol: 'eventhubs', peak_vusers: 2, requested: { processors: 2, workers: 8, max_partitions_per_processor: 4 } }
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { preparation_enabled: true } : { id: 'prep', state: 'ready', execution_revision: 'hash', allocation: [{ group: 'processor', target: 'generator-a', vusers: 2, runtime_resource_claim: claim, runtime_resource_admission: { claim, admitted: claim.requested, current: true } }] })
  await act(async () => root.render(<PreparationControls projectId="p" testId="run" status="INITIALIZING" />))
  expect(container.textContent).toContain('Requested processors / workers2 / 8')
  expect(container.textContent).toContain('Admitted processors / workers2 / 8')
  expect(container.textContent).toContain('Partition ceiling per processor4')
})

it('shows optional monitoring warnings while preserving ready Start and required failure details', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { preparation_enabled: true } : { id: 'prep', state: 'ready', execution_revision: 'hash', monitors: { warnings: ['Optional Elastic source: forbidden'] } })
  await act(async () => root.render(<PreparationControls projectId="p" testId="run" status="INITIALIZING" canEdit />))
  expect(container.querySelector('[aria-label="Monitoring readiness warnings"]')?.textContent).toContain('Optional Elastic source: forbidden')
  expect([...container.querySelectorAll('button')].find(button => button.textContent === 'Start prepared run')?.disabled).toBe(false)
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { preparation_enabled: true } : { id: 'required', state: 'failed', execution_revision: 'hash', failure: 'Required Elastic source is unavailable' })
  await act(async () => root.render(<PreparationControls projectId="p" testId="another" status="INITIALIZING" canEdit />))
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('Required Elastic source is unavailable')
  expect([...container.querySelectorAll('button')].some(button => button.textContent === 'Start prepared run')).toBe(false)
})
