// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import RunMonitorBindingSelector from './RunMonitorBindingSelector'
import type { MonitorBinding } from '../types/runMonitoring'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let root: Root; let container: HTMLElement
beforeEach(() => { request.mockReset(); container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockImplementation(async (path: string) => path.includes('/analysis/capabilities') ? { monitoring_enabled: true } : [{ id: 'source', name: 'Dynatrace Checkout', pack: 'dynatrace', enabled: true }]) })
afterEach(() => { act(() => root.unmount()); container.remove() })
it('preserves distinct inherited, disabled, and selected required source contracts', async () => {
  const onChange = vi.fn()
  function Harness() { const [value, setValue] = useState<MonitorBinding[] | undefined>(); return <RunMonitorBindingSelector projectId="p" value={value} onChange={next => { onChange(next); setValue(next) }} /> }
  await act(async () => root.render(<Harness />))
  const mode = container.querySelector('select')!
  await act(async () => { mode.value = 'off'; mode.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(onChange).toHaveBeenLastCalledWith([])
  await act(async () => { mode.value = 'custom'; mode.dispatchEvent(new Event('change', { bubbles: true })) })
  await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
  await act(async () => container.querySelectorAll<HTMLInputElement>('input[type=checkbox]')[1].click())
  expect(onChange).toHaveBeenLastCalledWith([{ source_id: 'source', required: true }])
  await act(async () => { mode.value = 'inherit'; mode.dispatchEvent(new Event('change', { bubbles: true })) })
  expect(onChange).toHaveBeenLastCalledWith(undefined)
})
it('retains missing source identities until explicitly removed', async () => {
  const onChange = vi.fn()
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" value={[{ source_id: 'deleted', required: true }]} onChange={onChange} />))
  expect(container.textContent).toContain('Selected source deleted retired or unavailable — remove it before launch.')
  await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Remove unavailable source')!.click())
  expect(onChange).toHaveBeenCalledWith([])
})

it('reports duplicate legacy collection for inherited Elastic sources and leaves explicit off unaffected', async () => {
  request.mockImplementation(async (path: string) => path.includes('/analysis/capabilities') ? { monitoring_enabled: true } : path.includes('monitor-bindings') ? { sources: [{ source_id: 'elastic', required: false }] } : [{ id: 'elastic', name: 'Elastic logs', pack: 'elastic', connection_id: 'connection', enabled: true }])
  const onConflictChange = vi.fn()
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" scenarioId="scenario" legacyELKConnectionId="connection" value={undefined} onChange={vi.fn()} onConflictChange={onConflictChange} />))
  expect(container.textContent).toContain('Turn legacy Elastic log capture off')
  expect(onConflictChange).toHaveBeenLastCalledWith(true)
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" scenarioId="scenario" legacyELKConnectionId="connection" value={[]} onChange={vi.fn()} onConflictChange={onConflictChange} />))
  expect(container.textContent).not.toContain('Turn legacy Elastic log capture off')
  expect(onConflictChange).toHaveBeenLastCalledWith(false)
})

it('reports how many scenario bindings an inheriting run keeps', async () => {
  request.mockImplementation(async (path: string) => path.includes('/analysis/capabilities') ? { monitoring_enabled: true } : path.includes('monitor-bindings') ? { sources: [{ source_id: 'a', required: false }, { source_id: 'b', required: true }] } : [])
  const onInheritedCountChange = vi.fn()
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" scenarioId="scenario" value={undefined} onChange={vi.fn()} onInheritedCountChange={onInheritedCountChange} />))
  expect(onInheritedCountChange).toHaveBeenLastCalledWith(2)
  // A run that sets its own bindings inherits none.
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" scenarioId="scenario" value={[]} onChange={vi.fn()} onInheritedCountChange={onInheritedCountChange} />))
  expect(onInheritedCountChange).toHaveBeenLastCalledWith(0)
})

it('asks for a scenario\'s bindings only while run monitoring is on', async () => {
  request.mockImplementation(async (path: string) => path.includes('/analysis/capabilities') ? { monitoring_enabled: false } : { sources: [] })
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" scenarioId="scenario" value={undefined} onChange={vi.fn()} />))
  expect(request.mock.calls.map(([path]) => path)).toEqual(['/analysis/capabilities?project_id=p'])
  expect(container.textContent).toBe('')
})

it('still reads a scenario\'s bindings, and offers a retry, when monitoring could not be checked', async () => {
  request.mockImplementation(async (path: string) => {
    if (path.includes('/analysis/capabilities')) throw new Error('capabilities unavailable')
    return { sources: [{ source_id: 'a', required: true }] }
  })
  const onInheritedCountChange = vi.fn()
  await act(async () => root.render(<RunMonitorBindingSelector projectId="p" scenarioId="scenario" value={undefined} onChange={vi.fn()} onInheritedCountChange={onInheritedCountChange} />))
  expect(request.mock.calls.map(([path]) => path)).toContain('/scenarios/scenario/monitor-bindings')
  expect(onInheritedCountChange).toHaveBeenLastCalledWith(1)
  expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Retry monitor sources')).toBe(true)
})
