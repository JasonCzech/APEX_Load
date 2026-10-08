// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import JourneyRiskPanel from './JourneyRiskPanel'
import { formatJourneyFraction, journeyIncompleteText, journeyUnavailableText, journeyUnscoredText } from './journeyRiskModel'

const mocks = vi.hoisted(() => ({ api: { getTestArchiveJourneys: vi.fn() } }))
vi.mock('../../hooks/useApi', () => ({ useApi: () => mocks.api }))

const report = {
  schema_version: 'apexload.journey_risk.v1',
  run_id: 'run-1',
  policy: { id: 'journeys', name: 'Checkout journeys', revision: 2, sha256: 'abc', source: 'pinned' },
  availability: 'available',
  journeys: [{
    id: 'purchase', name: 'Purchase', weight: 3, start: { transaction: 'Browse' }, success: { transaction: 'Checkout' },
    completed: 2, failed: 1, aborted: 2, skipped: 1, censored: 1, eligible: 5, steps: 13, step_failures: 2, failure_rate: 0.2,
    examples: [{ group: 'web', vuser_id: 1, iteration: 2, started_at: '2026-10-08T09:00:00Z', outcome: 'failed', failed_step: 'pay' }],
  }],
  weighted_failure_index: 0.2,
  step_risk_enabled: true,
  step_risk_unscored: 'no_weighted_steps',
  rows_outside_iterations: 2,
  notes: ['Weighted test-journey risk from the run\'s own transactions; never revenue or conversion impact.'],
}

async function flush() {
  await act(async () => { await Promise.resolve() })
  await act(async () => { await Promise.resolve() })
}

describe('JourneyRiskPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    mocks.api.getTestArchiveJourneys.mockReset()
  })
  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('shows the pinned accounting and the weighted index, never revenue', async () => {
    mocks.api.getTestArchiveJourneys.mockResolvedValue(report)
    act(() => root.render(<JourneyRiskPanel testId="run-1" />))
    await flush()
    expect(mocks.api.getTestArchiveJourneys).toHaveBeenCalledWith('run-1', {}, expect.objectContaining({ signal: expect.any(AbortSignal) }))
    const text = container.textContent || ''
    expect(text).toContain("Checkout journeys revision 2 (the run's pinned policy)")
    expect(text).toContain('Weighted failure index: 20.0%')
    expect(text).toContain('Step risk index: Unscored: no weighted journey recorded a step.')
    const cells = Array.from(container.querySelectorAll('tbody td')).map(cell => cell.textContent)
    expect(cells).toEqual(['Purchase', '3', '2', '1', '2', '1', '1', '20.0%', '2 of 13'])
    expect(text).toContain('Purchase: failed — group web, VUser 1, iteration 2, first failed step pay')
    expect(text).toContain('never revenue or conversion impact')
  })

  it('applies a named policy revision', async () => {
    mocks.api.getTestArchiveJourneys.mockResolvedValue(report)
    act(() => root.render(<JourneyRiskPanel testId="run-1" />))
    await flush()
    const [policy, revision] = Array.from(container.querySelectorAll('input'))
    const setValue = (input: HTMLInputElement, value: string) => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    act(() => { setValue(policy, ' other '); setValue(revision, '4') })
    act(() => { container.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(mocks.api.getTestArchiveJourneys).toHaveBeenLastCalledWith('run-1', { policy: 'other', revision: 4 }, expect.anything())
  })

  it('explains unavailable accounting and load failures', async () => {
    mocks.api.getTestArchiveJourneys.mockResolvedValue({ ...report, availability: 'unavailable', reason: 'archive_lacks_vuser_iteration_identity', journeys: [] })
    act(() => root.render(<JourneyRiskPanel testId="run-1" />))
    await flush()
    expect(container.textContent).toContain('written before runs recorded VUser iterations')
    expect(container.querySelector('table')).toBeNull()

    mocks.api.getTestArchiveJourneys.mockRejectedValue(new Error('journey policy not found'))
    act(() => root.render(<JourneyRiskPanel testId="run-2" />))
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('journey policy not found')
  })

  it('formats fractions and reasons', () => {
    expect(formatJourneyFraction(undefined)).toBe('—')
    expect(formatJourneyFraction(0.25)).toBe('25.0%')
    expect(journeyUnavailableText('no_journey_policy')).toContain('pinned no journey policy')
    expect(journeyUnavailableText('other')).toContain('(other)')
    expect(journeyUnscoredText('accounting_incomplete')).toContain('incomplete')
    expect(journeyUnavailableText('no_iterations')).toContain('ran no VUser iteration')
    expect(journeyIncompleteText(['sources_not_collected', 'novel_reason'])).toBe('The counts are incomplete (generator sources whose archives were not collected; novel reason), so the indexes are withheld.')
  })
})
