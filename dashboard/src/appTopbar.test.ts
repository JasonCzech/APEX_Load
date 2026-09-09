import { describe, expect, it } from 'vitest'

import {
  pageTitleForScriptSubView,
  pageTitleForView,
  selectTopbarActiveRuns,
  shouldShowTopbarClientDownloads,
  topbarRunStatusLabel,
  topbarRunTone,
  updateTopbarActiveRuns,
} from './appTopbar'
import type { TestSummary } from './types'

describe('app topbar helpers', () => {
  it('maps app routes to stable topbar page titles', () => {
    expect(pageTitleForView({ page: 'dashboard' })).toBe('Dashboard')
    expect(pageTitleForView({ page: 'docs' })).toBe('Help & documentation')
    expect(pageTitleForView({ page: 'command-center' })).toBe('Command Center')
    expect(pageTitleForView({ page: 'tests' })).toBe('Scenario Library')
    expect(pageTitleForView({ page: 'tests', scenarioView: 'builder' })).toBe('Scenario Builder')
    expect(pageTitleForView({ page: 'scripts', mode: 'create' })).toBe('Create Script')
    expect(pageTitleForView({ page: 'report', testId: 'run-1' })).toBe('Run Results')
    expect(pageTitleForView({ page: 'results' })).toBe('Results Library')
    expect(pageTitleForView({ page: 'fleet' })).toBe('Fleet')
    expect(pageTitleForView({ page: 'monitoring', category: 'telemetry' })).toBe('Telemetry')
    expect(pageTitleForView({ page: 'repository' }, { repositoryName: 'Payments Repository' })).toBe('Payments Repository')
    expect(pageTitleForView({ page: 'test', testId: 'run-1' }, { testName: 'Checkout Peak' })).toBe('Checkout Peak')
  })

  it('maps script authoring substates to topbar page titles', () => {
    expect(pageTitleForScriptSubView('list')).toBe('Script Library')
    expect(pageTitleForScriptSubView('create-options')).toBe('Script Builder')
    expect(pageTitleForScriptSubView('create')).toBe('Create Script')
    expect(pageTitleForScriptSubView('edit')).toBe('Edit Script')
    expect(pageTitleForScriptSubView('openapi-import')).toBe('Import OpenAPI')
    expect(pageTitleForScriptSubView('bruno-import')).toBe('Import Bruno')
    expect(pageTitleForScriptSubView('raml-import')).toBe('Import RAML')
    expect(pageTitleForScriptSubView('protocol-import')).toBe('Import From Source')
    expect(pageTitleForScriptSubView('oracle-review')).toBe('Oracle E-Business Review')
    expect(pageTitleForScriptSubView('salesforce-import')).toBe('Script Library')
    expect(pageTitleForScriptSubView('recorder')).toBe('Record Browser')
  })

  it('keeps client downloads in the topbar only for API guide pages', () => {
    expect(shouldShowTopbarClientDownloads({ page: 'docs' })).toBe(true)
    expect(shouldShowTopbarClientDownloads({ page: 'docs', guidePage: 'authentication' })).toBe(true)
    expect(shouldShowTopbarClientDownloads({ page: 'swagger' })).toBe(true)
    expect(shouldShowTopbarClientDownloads({ page: 'repository-guide' })).toBe(true)
    expect(shouldShowTopbarClientDownloads({ page: 'dashboard' })).toBe(false)
    expect(shouldShowTopbarClientDownloads({ page: 'scripts' })).toBe(false)
    expect(shouldShowTopbarClientDownloads({ page: 'settings' })).toBe(false)
  })

  it('selects active and pending run statuses while prioritizing live work', () => {
    const runs = selectTopbarActiveRuns([
      buildRun({ id: 'complete', name: 'Completed', status: 'COMPLETE', started_at: '2026-06-07T10:00:00Z' }),
      buildRun({ id: 'pending', name: 'Pending API Burst', status: 'PENDING', created_at: '2026-06-07T12:50:00Z' }),
      buildRun({ id: 'paused', name: 'Paused Soak', status: 'PAUSED', started_at: '2026-06-07T12:00:00Z' }),
      buildRun({ id: 'running-old', name: 'Checkout Baseline', status: 'RUNNING', started_at: '2026-06-07T11:00:00Z' }),
      buildRun({ id: 'running-new', name: 'Checkout Peak', status: 'RUNNING', started_at: '2026-06-07T12:30:00Z' }),
      buildRun({ id: 'queued', name: 'Queued API Burst', status: 'QUEUED', created_at: '2026-06-07T12:45:00Z' }),
      buildRun({ id: 'shakeout', name: 'Preflight', status: 'SHAKEOUT', created_at: '2026-06-07T12:46:00Z' }),
      buildRun({ id: 'shakeout-review', name: 'Preflight Review', status: 'SHAKEOUT_REVIEW', created_at: '2026-06-07T12:47:00Z' }),
    ])

    expect(runs.map(run => run.id)).toEqual(['running-new', 'running-old', 'shakeout', 'paused', 'shakeout-review', 'pending', 'queued'])
  })

  it('applies live status overrides before filtering active runs', () => {
    const runs = selectTopbarActiveRuns([
      buildRun({ id: 'stopped', name: 'Stopped', status: 'RUNNING' }),
      buildRun({ id: 'resumed', name: 'Resumed', status: 'PAUSED' }),
      buildRun({ id: 'failed', name: 'Failed', status: 'FAILED' }),
      buildRun({ id: 'complete-stale', name: 'Complete Stale', status: 'COMPLETE' }),
    ], {
      stopped: 'COMPLETE',
      resumed: 'RUNNING',
      failed: 'running',
      'complete-stale': 'INITIALIZING',
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

    // Queued/pending runs are surfaced in the topbar alongside live work, so a
    // QUEUED message adds the run rather than dropping it.
    const queued = updateTopbarActiveRuns(completed, {
      id: 'run-2',
      name: 'Queued API Burst',
      status: 'QUEUED',
      timestamp: '2026-06-07T12:06:00Z',
    })
    expect(queued.map(run => run.id)).toEqual(['run-2'])
    expect(queued[0].status).toBe('QUEUED')

    const aborted = updateTopbarActiveRuns(queued, {
      id: 'run-2',
      status: 'ABORTED',
      timestamp: '2026-06-07T12:07:00Z',
    })
    expect(aborted).toEqual([])
  })

  it('formats run chip labels and tones', () => {
    expect(topbarRunStatusLabel('SHAKEOUT_REVIEW')).toBe('Shakeout Review')
    expect(topbarRunStatusLabel('RUNNING')).toBe('Running')
    expect(topbarRunTone('RUNNING')).toBe('running')
    expect(topbarRunTone('PAUSED')).toBe('paused')
    expect(topbarRunTone('QUEUED')).toBe('pending')
    expect(topbarRunTone('SHAKEOUT')).toBe('pending')
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
