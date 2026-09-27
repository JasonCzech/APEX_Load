// @vitest-environment jsdom
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { describe, expect, it } from 'vitest'
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
    expect(host.textContent).toContain('No observation')
    expect(host.textContent).toContain('No sample available')
    act(() => root.unmount())
  })
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
