// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import ScriptDataUsagePanel from './ScriptDataUsagePanel'
import type { DataAliasUsage } from './scriptDataUsageModel'
import type { ScriptScenarioBindings } from './useScriptScenarioBindings'

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

const usage: DataAliasUsage[] = [
  { alias: 'jobs', columns: ['id'], operations: ['lease', 'ack'], nativeOperations: [], mutates: true, kinds: ['native_queue'], readsEachIteration: false, suppliedByScript: false, steps: [{ position: { phase: 'action', actionIndex: 0 }, name: 'Lease a row', operation: 'lease' }] },
  { alias: 'users', columns: ['email'], operations: [], nativeOperations: [], mutates: false, kinds: [], readsEachIteration: true, suppliedByScript: false, steps: [] },
]
const bindings = (overrides: Partial<ScriptScenarioBindings> = {}): ScriptScenarioBindings => ({
  status: 'ready', scenarios: 1, retry: vi.fn(),
  byAlias: new Map([['jobs', [{ scenarioId: 's', scenarioName: 'Checkout', binding: { alias: 'jobs', pool_id: 'p', access_mode: 'read_only', selection_strategy: 'sequential' }, choice: 'snapshot' }]]]),
  ...overrides,
})

it('lists each alias’s columns, steps and bindings, and flags what a binding cannot run', async () => {
  const onSelectStep = vi.fn()
  await act(async () => root.render(<ScriptDataUsagePanel usage={usage} saved bindings={bindings()} onSelectStep={onSelectStep} />))
  const [jobs, users] = [...container.querySelectorAll('.script-data-alias')]
  expect(jobs.textContent).toContain('Checkout: read-only snapshot')
  expect(jobs.querySelector('.script-data-mismatch')!.textContent).toBe('In Checkout: This script changes rows in jobs with table steps, which a read-only snapshot cannot run. Choose a live table.')
  await act(async () => [...jobs.querySelectorAll('button')].find(button => button.textContent === 'Lease a row')!.click())
  expect(onSelectStep).toHaveBeenCalledWith(usage[0].steps[0])
  expect(users.textContent).toContain('Bind in a scenario before running')
  expect(users.textContent).toContain('Expects a row from the binding')
})

it('says when it cannot tell, and checks again on request', async () => {
  const retry = vi.fn()
  await act(async () => root.render(<ScriptDataUsagePanel usage={usage} saved bindings={bindings({ status: 'failed', byAlias: new Map(), retry })} onSelectStep={() => undefined} />))
  expect(container.textContent).toContain('Not known yet')
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Check again')!.click())
  expect(retry).toHaveBeenCalled()
  await act(async () => root.render(<ScriptDataUsagePanel usage={usage} saved={false} bindings={bindings({ status: 'idle', byAlias: new Map() })} onSelectStep={() => undefined} />))
  expect(container.textContent).toContain('Save the script, then bind these aliases')
})
