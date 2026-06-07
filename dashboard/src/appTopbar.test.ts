import { describe, expect, it } from 'vitest'

import {
  pageTitleForView,
  selectTopbarActiveRuns,
  topbarRunStatusLabel,
  topbarRunTone,
  updateTopbarActiveRuns,
} from './appTopbar'
import type { TestSummary } from './types'

describe('app topbar helpers', () => {
  it('maps app routes to stable topbar page titles', () => {
    expect(pageTitleForView({ page: 'dashboard' })).toBe('Dashboard')
    expect(pageTitleForView({ page: 'tests' })).toBe('Scenario Library')
    expect(pageTitleForView({ page: 'tests', scenarioView: 'builder' })).toBe('Scenario Builder')
    expect(pageTitleForView({ page: 'scripts', mode: 'create' })).toBe('Create Script')
    expect(pageTitleForView({ page: 'repository' }, { repositoryName: 'Payments Repository' })).toBe('Payments Repository')
    expect(pageTitleForView({ page: 'test', testId: 'run-1' }, { testName: 'Checkout Peak' })).toBe('Checkout Peak')
  })

  it('selects only active run statuses and prioritizes live work', () => {
    const runs = selectTopbarActiveRuns([
      buildRun({ id: 'complete', name: 'Completed', status: 'COMPLETE', started_at: '2026-06-07T10:00:00Z' }),
      buildRun({ id: 'pending', name: 'Pending API Burst', status: 'PENDING', created_at: '2026-06-07T12:50:00Z' }),
      buildRun({ id: 'paused', name: 'Paused Soak', status: 'PAUSED', started_at: '2026-06-07T12:00:00Z' }),
      buildRun({ id: 'running-old', name: 'Checkout Baseline', status: 'RUNNING', started_at: '2026-06-07T11:00:00Z' }),
      buildRun({ id: 'running-new', name: 'Checkout Peak', status: 'RUNNING', started_at: '2026-06-07T12:30:00Z' }),
      buildRun({ id: 'queued', name: 'Queued API Burst', status: 'QUEUED', created_at: '2026-06-07T12:45:00Z' }),
    ])

    expect(runs.map(run => run.id)).toEqual(['running-new', 'running-old', 'paused'])
  })

  it('applies live status overrides before filtering active runs', () => {
    const runs = selectTopbarActiveRuns([
      buildRun({ id: 'stopped', name: 'Stopped', status: 'RUNNING' }),
      buildRun({ id: 'resumed', name: 'Resumed', status: 'PAUSED' }),
    ], {
      stopped: 'COMPLETE',
      resumed: 'RUNNING',
    })

    expect(runs.map(run => `${run.id}:${run.status}`)).toEqual(['resumed:RUNNING'])
  })

  it('updates and removes runs from websocket-style status messages', () => {
    const first = updateTopbarActiveRuns([], {
      id: 'run-1',
      name: 'Checkout Peak',
      status: 'RUNNING',
      timestamp: '2026-06-07T12:00:00Z',
    })
    expect(first).toHaveLength(1)
    expect(first[0].name).toBe('Checkout Peak')

    const paused = updateTopbarActiveRuns(first, {
      id: 'run-1',
      status: 'PAUSED',
      timestamp: '2026-06-07T12:02:00Z',
    })
    expect(paused[0].status).toBe('PAUSED')
    expect(paused[0].name).toBe('Checkout Peak')

    const completed = updateTopbarActiveRuns(paused, {
      id: 'run-1',
      status: 'COMPLETE',
      timestamp: '2026-06-07T12:05:00Z',
    })
    expect(completed).toEqual([])

    const queued = updateTopbarActiveRuns(completed, {
      id: 'run-2',
      name: 'Queued API Burst',
      status: 'QUEUED',
      timestamp: '2026-06-07T12:06:00Z',
    })
    expect(queued).toEqual([])
  })

  it('formats run chip labels and tones', () => {
    expect(topbarRunStatusLabel('SHAKEOUT_REVIEW')).toBe('Shakeout Review')
    expect(topbarRunStatusLabel('RUNNING')).toBe('Running')
    expect(topbarRunTone('RUNNING')).toBe('running')
    expect(topbarRunTone('PAUSED')).toBe('paused')
    expect(topbarRunTone('QUEUED')).toBe('pending')
    expect(topbarRunTone('STOPPING')).toBe('stopping')
  })
})

function buildRun(overrides: Partial<TestSummary>): TestSummary {
  return {
    id: overrides.id || 'run-1',
    name: overrides.name || 'Run',
    status: overrides.status || 'RUNNING',
    groups: [],
    total_vusers: 1,
    duration: '1m',
    created_at: overrides.created_at || '2026-06-07T09:00:00Z',
    started_at: overrides.started_at,
    pending_since: overrides.pending_since,
    scheduled_start_at: overrides.scheduled_start_at,
  }
}
