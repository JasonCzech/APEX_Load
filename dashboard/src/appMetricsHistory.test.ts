import { describe, expect, it } from 'vitest'

import { appendBoundedMetricsHistory, MAX_LIVE_METRICS_POINTS_PER_TEST } from './appMetricsHistory'
import type { MetricsHistoryPoint } from './types'

function point(time: string): MetricsHistoryPoint {
  return { time } as MetricsHistoryPoint
}

describe('appendBoundedMetricsHistory', () => {
  it('keeps only the newest samples for each test', () => {
    let history: Record<string, MetricsHistoryPoint[]> = {}
    history = appendBoundedMetricsHistory(history, 'run-1', point('one'), 3, 2)
    history = appendBoundedMetricsHistory(history, 'run-1', point('two'), 3, 2)
    history = appendBoundedMetricsHistory(history, 'run-1', point('three'), 3, 2)

    expect(history['run-1'].map(sample => sample.time)).toEqual(['two', 'three'])
  })

  it('evicts the least-recently-updated test when the test limit is reached', () => {
    let history: Record<string, MetricsHistoryPoint[]> = {
      'run-1': [point('one')],
      'run-2': [point('two')],
    }

    history = appendBoundedMetricsHistory(history, 'run-1', point('one-again'), 2, 3)
    history = appendBoundedMetricsHistory(history, 'run-3', point('three'), 2, 3)

    expect(Object.keys(history)).toEqual(['run-1', 'run-3'])
    expect(history['run-2']).toBeUndefined()
  })

  it('retains every protected active test when live concurrency exceeds the inactive cache limit', () => {
    const activeRuns = new Set(['run-1', 'run-2', 'run-3'])
    let history: Record<string, MetricsHistoryPoint[]> = {}

    for (let iteration = 0; iteration < 3; iteration += 1) {
      for (const runId of activeRuns) {
        history = appendBoundedMetricsHistory(history, runId, point(`${runId}-${iteration}`), 2, 3, activeRuns)
      }
    }

    expect(Object.keys(history).sort()).toEqual(Array.from(activeRuns).sort())
    expect(history['run-1']).toHaveLength(3)
    expect(history['run-2']).toHaveLength(3)
    expect(history['run-3']).toHaveLength(3)
  })

  it('evicts inactive history before protected active runs', () => {
    const activeRuns = new Set(['run-2', 'run-3'])
    const history = appendBoundedMetricsHistory(
      {
        'run-1': [point('inactive')],
        'run-2': [point('active')],
      },
      'run-3',
      point('active-new'),
      2,
      3,
      activeRuns,
    )

    expect(history['run-1']).toBeUndefined()
    expect(history['run-2']).toBeDefined()
    expect(history['run-3']).toBeDefined()
  })

  it('retains a long single-run timeline by default (not just a 5-minute tail)', () => {
    expect(MAX_LIVE_METRICS_POINTS_PER_TEST).toBeGreaterThan(300)
    let history: Record<string, MetricsHistoryPoint[]> = {}
    for (let index = 0; index < 1300; index += 1) {
      history = appendBoundedMetricsHistory(history, 'run-1', point(`sample-${index}`))
    }

    expect(history['run-1']).toHaveLength(1300)
    expect(history['run-1'][0].time).toBe('sample-0')
    expect(history['run-1'][1299].time).toBe('sample-1299')
  })
})
