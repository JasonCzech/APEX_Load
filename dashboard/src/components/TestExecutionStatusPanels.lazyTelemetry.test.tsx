// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { TestSummary } from '../types'
import type { TelemetryBudgetPolicy } from '../types/telemetryPolicy'
import TestExecutionStatusPanels from './TestExecutionStatusPanels'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// The segments chunk arrives only when the test releases it.
const chunk = vi.hoisted(() => {
  let release!: () => void
  const ready = new Promise<void>(resolve => { release = resolve })
  return { ready, release, started: 0 }
})
vi.mock('./TelemetryPolicySegments', async importOriginal => {
  chunk.started++
  await chunk.ready
  return importOriginal()
})
vi.mock('./TelemetryPolicySelect', () => ({ default: () => <select data-testid="budget-select" /> }))

const policy = { policy_id: 'system-telemetry-budgets', hash: 'a'.repeat(64), cadence_scope: 'local-controller-ticker', budgets: [] } as unknown as TelemetryBudgetPolicy
const run = (id: string, status: string, segments?: unknown[]) => ({ id, name: 'Run', status, project_id: 'project-a', groups: [], total_vusers: 1, duration: '1m',
  created_at: '2026-10-23T12:00:00Z', telemetry_policy_segments: segments } as unknown as TestSummary)
const twoSegments = [{ sequence: 1 }, { sequence: 2 }]

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  container = document.createElement('div')
  document.body.append(container)
  root = createRoot(container)
})
afterEach(async () => {
  await act(async () => root.unmount())
  container.remove()
})

const render = async (test: TestSummary, canChange = true) => {
  await act(async () => root.render(<TestExecutionStatusPanels isGoalOriented={false} goalDescription="" test={test}
    metrics={{ telemetry_policy: { availability: 'available', policy } }} isMultiGroup={false} groupMetrics={[]} groupColors={[]} canChangeTelemetryPolicy={canChange} />))
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
}
const present = (testId: string) => container.querySelector(`[data-testid="${testId}"]`) !== null

it('loads the segments list and the change form on use, and resets the form for another run', async () => {
  // Neither renders for one segment, a viewer or a finished run, so nothing loads.
  await render(run('run-1', 'RUNNING', [{ sequence: 1 }]), false)
  await render(run('run-1', 'COMPLETE', twoSegments.slice(0, 1)))
  expect(chunk.started).toBe(0)

  await render(run('run-1', 'RUNNING', twoSegments))
  expect(chunk.started).toBe(1)
  expect(present('telemetry-policy-segments')).toBe(false)
  expect(present('telemetry-budget-change-open')).toBe(false)

  await act(async () => {
    chunk.release()
    await import('./TelemetryPolicySegments')
    await new Promise(resolve => setTimeout(resolve, 0))
  })
  await render(run('run-1', 'RUNNING', twoSegments))
  expect(present('telemetry-policy-segments')).toBe(true)
  expect(present('telemetry-budget-change-open')).toBe(true)

  await act(async () => container.querySelector<HTMLButtonElement>('[data-testid="telemetry-budget-change-open"]')!.click())
  expect(present('telemetry-budget-change')).toBe(true)
  // The same run, paused, keeps the open form.
  await render(run('run-1', 'PAUSED', twoSegments))
  expect(present('telemetry-budget-change')).toBe(true)
  // Another run starts with the form closed.
  await render(run('run-2', 'RUNNING', twoSegments))
  expect(present('telemetry-budget-change')).toBe(false)
  expect(present('telemetry-budget-change-open')).toBe(true)
  expect(present('telemetry-policy-segments')).toBe(true)
})
