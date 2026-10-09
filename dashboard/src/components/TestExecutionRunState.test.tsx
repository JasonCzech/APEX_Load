// @vitest-environment jsdom
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { describe, expect, it, vi } from 'vitest'
import TestExecutionRunState from './TestExecutionRunState'
import { RetainedRunMetrics } from './runReport/RunExecutionDetails'
import type { TestSummary } from '../types'

describe('terminal observations', () => {
  for (const status of ['FAILED', 'ABORTED', 'COMPLETE']) {
    it(`labels ${status} samples without erasing their evidence`, () => {
      const host = document.createElement('div')
      const root = createRoot(host)
      const test = { status, total_vusers: 120, groups: [], live_metrics: { timestamp: '2026-09-27T12:00:00Z', active_vusers: 120 } } as unknown as TestSummary
      act(() => root.render(<TestExecutionRunState test={test} groupMetrics={[]} remainingSeconds={100} />))
      expect(host.textContent).toContain('Last observed load120')
      expect(host.textContent).toContain('Historical sample')
      expect(host.textContent).toContain('Run ended')
      expect(host.textContent).not.toContain('Live sample received')
      expect(host.querySelector('[role="status"]')?.textContent).toContain('do not indicate currently running VUsers')
      act(() => root.unmount())
    })
  }
  it('does not invent an observation for a failed run without samples', () => {
    const host = document.createElement('div')
    const root = createRoot(host)
    act(() => root.render(<TestExecutionRunState test={{ status: 'FAILED', total_vusers: 120 } as TestSummary} groupMetrics={[]} remainingSeconds={0} />))
    expect(host.textContent).toContain('Last observed load—')
    expect(host.textContent).toContain('Last telemetry—')
    expect(host.textContent).not.toContain('Awaiting data')
    act(() => root.unmount())
  })
})

it('expires an active observation without network updates and clears on fresh evidence', () => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-09-27T12:00:00Z'))
  const host = document.createElement('div')
  const root = createRoot(host)
  const test = { status: 'RUNNING', total_vusers: 120, live_metrics: { timestamp: new Date().toISOString(), active_vusers: 120 } } as TestSummary
  try {
    act(() => root.render(<TestExecutionRunState test={test} groupMetrics={[]} remainingSeconds={100} />))
    expect(host.querySelector('[role="status"]')).toBeNull()
    act(() => vi.advanceTimersByTime(5000))
    expect(host.querySelector('[role="status"]')?.textContent).toContain('current load and recovery are unconfirmed')
    expect(host.textContent).toContain('Last observed load120')
    expect(host.textContent).toContain('Stale sample')
    const fresh = { ...test, live_metrics: { ...test.live_metrics!, timestamp: new Date().toISOString() } }
    act(() => root.render(<TestExecutionRunState test={fresh} groupMetrics={[]} remainingSeconds={95} />))
    expect(host.querySelector('[role="status"]')).toBeNull()
    expect(host.textContent).toContain('Observed load120')
    act(() => root.render(<TestExecutionRunState test={{ ...fresh, status: 'FAILED' }} groupMetrics={[]} remainingSeconds={0} />))
    expect(host.querySelector('[role="status"]')?.textContent).toContain('Run ended')
  } finally {
    act(() => root.unmount())
    expect(vi.getTimerCount()).toBe(0)
    vi.useRealTimers()
  }
})

it('preserves the captured load in Results while explicitly identifying historical evidence', () => {
  const host = document.createElement('div')
  const root = createRoot(host)
  const test = { status: 'FAILED', live_metrics: { active_vusers: 120, timestamp: '2026-09-27T12:00:00Z' } } as TestSummary
  act(() => root.render(<RetainedRunMetrics test={test} />))
  expect(host.textContent).toContain('VUsers at capture120')
  expect(host.querySelector('[role="status"]')?.textContent).toContain('Historical sample')
  expect(host.textContent).toContain('do not indicate currently running VUsers')
  act(() => root.unmount())
})
