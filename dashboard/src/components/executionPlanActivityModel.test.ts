import { describe, expect, it } from 'vitest'

import type { ExecutionPlanRunView } from '../types/executionPlans'
import {
  effectiveOverlapPolicy,
  formatPlanDuration,
  occurrenceStatusLabel,
  overlapPolicyLabel,
  planCardState,
  planEntryStatusLabel,
  planHeldState,
  planLastRunSentence,
  planReleaseName,
  planReleaseSentence,
  planRunOutcomeLabel,
  planRunTiming,
  scheduleHoldSentence,
} from './executionPlanActivityModel'

const now = Date.parse('2026-10-12T10:20:00Z')

function runView(overrides: Partial<ExecutionPlanRunView> = {}): ExecutionPlanRunView {
  return {
    id: 'run-1',
    plan_id: 'plan-1',
    run_mode: 'parallel',
    status: 'completed',
    created_at: '2026-10-12T10:00:00Z',
    updated_at: '2026-10-12T10:00:00Z',
    active: true,
    outcome: 'running',
    started_at: '2026-10-12T10:08:00Z',
    estimated_end_at: '2026-10-12T10:28:00Z',
    entries: [
      { order: 0, scenario_id: 's1', status: 'created', test_id: 't1', test_status: 'RUNNING' },
      { order: 1, scenario_id: 's2', status: 'created', test_id: 't2', test_status: 'COMPLETE' },
    ],
    ...overrides,
  }
}

describe('executionPlanActivityModel', () => {
  it('formats plan durations briefly', () => {
    expect(formatPlanDuration(12)).toBe('12 s')
    expect(formatPlanDuration(0.2)).toBe('1 s')
    expect(formatPlanDuration(720)).toBe('12 min')
    expect(formatPlanDuration(3900)).toBe('1 h 5 min')
    expect(formatPlanDuration(7200)).toBe('2 h')
    expect(formatPlanDuration(Number.NaN)).toBe('')
  })

  it('treats plans saved before the policy as allowing overlap', () => {
    expect(effectiveOverlapPolicy(undefined)).toBe('allow')
    expect(effectiveOverlapPolicy('')).toBe('allow')
    expect(effectiveOverlapPolicy('queue')).toBe('queue')
    expect(overlapPolicyLabel('skip')).toBe('Skip the new run')
  })

  it('reports elapsed and remaining time for an active run and the total for a finished one', () => {
    expect(planRunTiming(runView(), now)).toMatchObject({ elapsedSeconds: 720, remainingSeconds: 480 })
    expect(planRunTiming(runView({ estimated_end_at: undefined }), now).remainingSeconds).toBeNull()
    const finished = planRunTiming(runView({ active: false, outcome: 'completed', ended_at: '2026-10-12T10:26:00Z' }), now)
    expect(finished.tookSeconds).toBe(1080)
    expect(finished.elapsedSeconds).toBeNull()
  })

  it('leads the card with the running run, then a held run, then nothing', () => {
    const running = planCardState({ active_run: runView(), active_runs: 2 }, now)
    expect(running.kind).toBe('running')
    if (running.kind === 'running') {
      expect(running.summary).toContain('1 of 2 runs active')
      expect(running.summary).toContain('12 min elapsed')
      expect(running.summary).toContain('about 8 min left')
      expect(running.extraRuns).toBe(1)
    }
    const sequential = planCardState({ active_run: runView({ run_mode: 'sequential', entries: [
      { order: 0, scenario_id: 's1', status: 'complete' },
      { order: 1, scenario_id: 's2', status: 'running', test_status: 'RUNNING' },
      { order: 2, scenario_id: 's3', status: 'pending' },
    ] }) }, now)
    expect(sequential.kind === 'running' && sequential.summary.startsWith('entry 2 of 3')).toBe(true)
    const stopping = planCardState({ active_run: runView({ run_mode: 'sequential', entries: [
      { order: 0, scenario_id: 's1', status: 'running', test_status: 'STOPPING', stopped: true, detail: 'plan stopped by Alice' },
      { order: 1, scenario_id: 's2', status: 'skipped', detail: 'plan stopped by Alice', stopped: true },
    ] }) }, now)
    expect(stopping.kind === 'running' && stopping.stopping && stopping.summary.startsWith('entry 1 of 2')).toBe(true)
    // Stopping the last entry skips nothing; the running entry carries the stop.
    expect(planRunOutcomeLabel({ outcome: 'running', entries: [{ order: 0, scenario_id: 's1', status: 'running', stopped: true }] })).toBe('Stopping')
    expect(planRunOutcomeLabel({ outcome: 'running', entries: [{ order: 0, scenario_id: 's1', status: 'skipped', detail: 'plan stopped by a script message' }] })).toBe('Running')

    const approval = planCardState({
      pending_approval: { id: 'appr-1', plan_id: 'plan-1', policy: 'quota_burst', status: 'pending', requested_at: '', created_at: '', updated_at: '' },
      waiting: [{ schedule_id: 'sch-1', schedule_name: 'Nightly', planned_at: '2026-10-12T06:30:00Z', reason_code: 'approval_required', approval_id: 'appr-1' }],
    }, now)
    expect(approval.kind).toBe('waiting_approval')
    if (approval.kind === 'waiting_approval') {
      expect(approval.approvalId).toBe('appr-1')
      expect(approval.summary).toContain('“Nightly” run planned for')
      expect(approval.summary).toContain('held until a project admin approves it')
    }
    const manualApproval = planCardState({ pending_approval: { id: 'appr-2', plan_id: 'plan-1', policy: 'quota_burst', status: 'pending', requested_at: '', created_at: '', updated_at: '', decision_reason: 'Aggregate peak 8 VUsers exceeds project limit 5' } }, now)
    expect(manualApproval.kind === 'waiting_approval' && manualApproval.summary.includes('Aggregate peak 8 VUsers')).toBe(true)

    const queued = planCardState({ waiting: [{ schedule_id: 'sch-1', planned_at: '2026-10-12T06:30:00Z', reason_code: 'waiting_for_previous_run' }] }, now)
    expect(queued.kind === 'queued' && queued.summary.includes("starts when the plan's previous run finishes")).toBe(true)

    expect(planCardState(undefined).kind).toBe('idle')
    expect(planCardState({}).kind).toBe('idle')
  })

  it('names how the last run ended', () => {
    expect(planLastRunSentence({ last_run: runView({ active: false, outcome: 'stopped', ended_at: '2026-10-12T10:26:00Z' }) })).toContain('Last run: Stopped')
    expect(planLastRunSentence({})).toBe('')
  })

  it('reads an entry state from its run first', () => {
    expect(planEntryStatusLabel({ order: 0, scenario_id: 's', status: 'created', test_status: 'QUEUED' }).label).toBe('Waiting to start')
    expect(planEntryStatusLabel({ order: 0, scenario_id: 's', status: 'created', test_status: 'ABORTED' })).toEqual({ label: 'Aborted', tone: 'danger' })
    expect(planEntryStatusLabel({ order: 0, scenario_id: 's', status: 'skipped' }).label).toBe('Skipped')
    expect(planEntryStatusLabel({ order: 0, scenario_id: 's', status: 'pending' }).label).toBe('Not started')
  })

  it('says where a plan files its runs', () => {
    const names = { applications: { 'app-1': 'Storefront' }, releases: { 'rel-1': 'R2026.11' } }
    expect(planReleaseSentence({ application_id: 'app-1', release_id: 'rel-1' }, names)).toBe('Runs are filed under Storefront › R2026.11')
    expect(planReleaseSentence({}, names)).toBe('Runs are filed under Imported Legacy (no release chosen)')
    expect(planReleaseName({ application_id: 'app-1', release_id: 'rel-1' }, names)).toBe('Storefront › R2026.11')
    expect(planReleaseName({ application_id: 'app-1' }, names)).toBe('')
  })

  it('words held schedule runs and occurrence states plainly', () => {
    expect(scheduleHoldSentence('approval_required', '2026-10-12T06:30:00Z')).toMatch(/^Waiting for approval: the .* run is held/)
    expect(scheduleHoldSentence('waiting_for_previous_run', '2026-10-12T06:30:00Z')).toMatch(/^Queued:/)
    expect(scheduleHoldSentence('', '2026-10-12T06:30:00Z')).toBe('')
    // After a failed run the schedule asks for a decision but has moved on.
    expect(scheduleHoldSentence('operator_decision_required', '2026-10-13T06:30:00Z')).toBe('')
    expect(planHeldState({ active_run: runView(), waiting: [{ schedule_id: 's', planned_at: '2026-10-12T06:30:00Z', reason_code: 'waiting_for_previous_run' }] }, now).kind).toBe('queued')
    expect(occurrenceStatusLabel('blocked', 'approval_required')).toBe('Waiting for approval')
    expect(occurrenceStatusLabel('skipped', 'previous_run_active')).toBe('Skipped — previous run still running')
    expect(occurrenceStatusLabel('materialized')).toBe('Started')
  })
})
