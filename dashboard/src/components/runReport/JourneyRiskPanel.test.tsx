// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../../hooks/apiClient'
import JourneyRiskPanel from './JourneyRiskPanel'
import { formatJourneyBoundary, formatJourneyFraction, journeyIncompleteText, journeyUnavailableText, journeyUnscoredText } from './journeyRiskModel'

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
    expect(text).toContain('Weighted failure index: 20.00%')
    expect(text).toContain('Step risk index: Unscored: no weighted journey recorded a step.')
    const cells = Array.from(container.querySelectorAll('tbody td')).map(cell => cell.textContent)
    expect(cells).toEqual(['Purchase', '3', '2', '1', '2', '1', '1', '20.00%', '2 of 13'])
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

    mocks.api.getTestArchiveJourneys.mockRejectedValue(new ApiError('failed to read archive transactions: disk', 500))
    act(() => root.render(<JourneyRiskPanel testId="run-2" />))
    await flush()
    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Journey accounting could not be loaded')
    expect(alert?.querySelector('details')?.textContent).toContain('failed to read archive transactions')
    mocks.api.getTestArchiveJourneys.mockResolvedValue(report)
    act(() => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry journeys')?.click() })
    await flush()
    expect(mocks.api.getTestArchiveJourneys).toHaveBeenCalledTimes(3)
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain('Checkout journeys revision 2')
  })

  it('says plainly why a policy request was refused, without a retry', async () => {
    const setValue = (input: HTMLInputElement, value: string) => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    mocks.api.getTestArchiveJourneys.mockResolvedValueOnce(report).mockRejectedValueOnce(new ApiError('journey policy not found', 404))
    act(() => root.render(<JourneyRiskPanel testId="run-1" />))
    await flush()
    const [policy, revision] = Array.from(container.querySelectorAll('input'))
    act(() => { setValue(policy, 'missing'); setValue(revision, '2') })
    act(() => { container.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('No journey policy "missing" revision 2 exists in this project.')
    expect(Array.from(container.querySelectorAll('button')).map(button => button.textContent)).not.toContain('Retry journeys')

    mocks.api.getTestArchiveJourneys.mockRejectedValueOnce(new ApiError('journey policy revision is invalid: bad', 409))
    act(() => { container.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Journey policy "missing" revision 2 is not a valid journey policy.')
    mocks.api.getTestArchiveJourneys.mockRejectedValueOnce(new ApiError('policy id is too long', 400))
    act(() => { container.querySelector('form')?.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('The journey policy request was refused.')
    expect(Array.from(container.querySelectorAll('button')).map(button => button.textContent)).not.toContain('Retry journeys')
  })

  it('says why the run\'s pinned policy cannot be accounted, without a retry', async () => {
    const refusals: Array<[ApiError, string]> = [
      [new ApiError('journey policy not found', 404), "The run's pinned journey policy no longer exists in this project."],
      [new ApiError('the pinned journey policy revision no longer matches its pin', 409), "The run's pinned journey policy revision changed after the run pinned it"],
      [new ApiError('journey policy revision is invalid: bad', 409), "The run's pinned journey policy revision is not a valid journey policy."],
    ]
    for (const [error, message] of refusals) {
      // A fresh panel: the request names no policy, so the run's pin applies.
      act(() => root.unmount())
      root = createRoot(container)
      mocks.api.getTestArchiveJourneys.mockRejectedValueOnce(error)
      act(() => root.render(<JourneyRiskPanel testId="run-1" />))
      await flush()
      expect(mocks.api.getTestArchiveJourneys).toHaveBeenLastCalledWith('run-1', {}, expect.anything())
      expect(container.querySelector('[role="alert"]')?.textContent).toContain(message)
      expect(container.textContent).not.toContain("once the run's archive is finalized")
      expect(Array.from(container.querySelectorAll('button')).map(button => button.textContent)).not.toContain('Retry journeys')
    }
  })

  it('offers a retry when policies are unavailable', async () => {
    mocks.api.getTestArchiveJourneys.mockRejectedValueOnce(new ApiError('internal server error', 503)).mockResolvedValueOnce(report)
    act(() => root.render(<JourneyRiskPanel testId="run-1" />))
    await flush()
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Journey accounting could not be loaded')
    act(() => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Retry journeys')?.click() })
    await flush()
    expect(container.textContent).toContain('Checkout journeys revision 2')
  })

  it('describes a missing or unfinalized archive without echoing the server', async () => {
    mocks.api.getTestArchiveJourneys.mockRejectedValue(new ApiError('archive not found', 404))
    act(() => root.render(<JourneyRiskPanel testId="run-1" />))
    await flush()
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain("This run's saved archive is not available yet (it may still be publishing)")
    expect(container.textContent).not.toContain('archive not found')
    // Once the archive is published, checking again accounts the journeys.
    mocks.api.getTestArchiveJourneys.mockResolvedValueOnce(report)
    act(() => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Check again')?.click() })
    await flush()
    expect(container.textContent).toContain('Checkout journeys revision 2')

    mocks.api.getTestArchiveJourneys.mockRejectedValueOnce(new ApiError('the run archive is not finalized', 409)).mockResolvedValueOnce(report)
    act(() => root.render(<JourneyRiskPanel testId="run-2" />))
    await flush()
    expect(container.textContent).toContain("Journeys are accounted once the run's archive is finalized.")
    expect(container.textContent).not.toContain('the run archive is not finalized')
    act(() => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Check again')?.click() })
    await flush()
    expect(container.textContent).toContain('Checkout journeys revision 2')
  })

  it('formats fractions and reasons', () => {
    expect(formatJourneyFraction(undefined)).toBe('—')
    expect(formatJourneyFraction(0.25)).toBe('25.00%')
    expect(journeyUnavailableText('no_journey_policy')).toContain('pinned no journey policy')
    expect(journeyUnavailableText('other')).toContain('(other)')
    expect(journeyUnscoredText('accounting_incomplete')).toContain('incomplete')
    expect(journeyUnavailableText('no_iterations')).toContain('ran no VUser iteration')
    expect(journeyIncompleteText(['sources_not_collected', 'novel_reason'])).toBe('The counts are incomplete (generator sources whose archives were not collected; novel reason), so the indexes are withheld.')
    expect(journeyIncompleteText(['sources_not_completed', 'live_truncated'])).toBe('The counts are incomplete (active VUser iterations have not completed yet; live provisional buffer reached its bound), so the indexes are withheld.')
    expect(formatJourneyBoundary({ action: 'Submit', step_path: 'actions[2]' })).toBe('action Submit (actions[2])')
  })

  it('polls provisional live journey accounting and replaces it when the archive is finalized', async () => {
    vi.useFakeTimers()
    try {
      mocks.api.getTestArchiveJourneys
        .mockResolvedValueOnce({ ...report, provisional: true, live_truncated: true })
        .mockResolvedValueOnce(report)
      act(() => root.render(<JourneyRiskPanel testId="run-1" />))
      await act(async () => { await Promise.resolve() })
      await act(async () => { await Promise.resolve() })
      expect(container.querySelector('[data-provisional="true"]')).not.toBeNull()
      expect(container.textContent).toContain('Provisional live journey accounting (non-verdict)')
      expect(container.textContent).toContain('live_truncated')

      await act(async () => { vi.advanceTimersByTime(3000) })
      await act(async () => { await Promise.resolve() })
      await act(async () => { await Promise.resolve() })
      expect(container.querySelector('[data-provisional="true"]')).toBeNull()
      expect(container.textContent).not.toContain('Provisional live journey accounting')
    } finally {
      vi.useRealTimers()
    }
  })
})
