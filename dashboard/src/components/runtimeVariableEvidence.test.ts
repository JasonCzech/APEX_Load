import { describe, expect, it } from 'vitest'
import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugSession } from '../types/replay'
import { omitUnavailableReplayReport, omitUnavailableReplaySession, replayVariablesForInspection, runtimeEvidenceUnavailable } from './runtimeVariableEvidence'

const report = (extra: Partial<BrowserReplayReport> = {}): BrowserReplayReport => ({
  group: 'Synthetic', protocol: 'http', success: true, started_at: '2026-10-03T00:00:00Z',
  completed_at: '', events: [{ phase: 'action', action_index: 0, action_name: 'Read', action_type: 'GET', target: '/read', success: true,
    duration_ms: 1, started_at: '2026-10-03T00:00:00Z', completed_at: '2026-10-03T00:00:01Z', extractions: { extracted: 'retained-value' } }],
  ...extra,
})

describe('runtime variable evidence admission', () => {
  it('recognizes the signal on any session, report or progress source without upgrading legacy absence', () => {
    expect(runtimeEvidenceUnavailable(undefined, null, {}, { runtime_evidence_unavailable: false })).toBe(false)
    expect(runtimeEvidenceUnavailable({}, { runtime_evidence_unavailable: true }, { runtime_evidence_unavailable: false })).toBe(true)
  })

  it.each([false, true])('omits all retained and reconstructed variables when unavailable (terminal=%s)', terminal => {
    const retained = report(terminal ? { completed_at: '2026-10-03T00:00:02Z', final_variables: { final: 'final-value' } } : {})
    expect(replayVariablesForInspection(retained, { seed: 'seed-value' }, true)).toBeNull()
    expect(replayVariablesForInspection({ ...retained, runtime_evidence_unavailable: true }, { seed: 'seed-value' }, false)).toBeNull()
    expect(replayVariablesForInspection({ ...retained, final_variables: undefined }, { seed: 'seed-value' }, true)).toBeNull()
  })

  it('preserves existing inspection for available live and terminal reports', () => {
    expect(replayVariablesForInspection(null, { seed: 'seed-value' }, false)).toBeNull()
    expect(replayVariablesForInspection(report(), { seed: 'seed-value' }, false)).toEqual({ seed: 'seed-value', extracted: 'retained-value' })
    expect(replayVariablesForInspection(report({ completed_at: '2026-10-03T00:00:02Z', final_variables: { final: 'final-value' } }), { seed: 'seed-value' }, false)).toEqual({ final: 'final-value' })
  })
})

describe('runtime evidence omission projection', () => {
  it('retains structural execution outcomes while dropping known and future detail fields without mutating the input', () => {
    const detail = 'synthetic-unclassified-detail'
    const input = { ...report({ dropped_event_count: 4, truncated_event_evidence_count: 2, final_variables: { ordinary: detail }, artifact_manifest: { seed_variables: [detail] },
      diagnosis_summary: { failure_count: 1, first_failure_cause: detail }, repair_suggestions: [], runtime_profile: { name: detail } }),
      future_detail: detail,
      events: [{ ...report().events[0], event_id: 'event-1', request_index: 1, request_count: 2, leaf_ordinal: 3, step_path: ['requests[1]'],
        status_code: 200, parent_action_name: detail, source_action_name: detail, body: detail, headers: { echo: detail },
        extractions: { ordinary: detail }, snapshot: { response_body: detail }, protocol_evidence: { future_detail: detail },
        diagnosis: { category: 'runtime', severity: 'error', cause: detail }, future_detail: detail }],
    }
    const projected = omitUnavailableReplayReport(input)
    expect(projected).toMatchObject({ runtime_evidence_unavailable: true, dropped_event_count: 4, truncated_event_evidence_count: 2 })
    expect(projected.events[0]).toMatchObject({ event_id: 'event-1', phase: 'action', action_index: 0, request_index: 1,
      request_count: 2, leaf_ordinal: 3, step_path: ['requests[1]'], status_code: 200, success: true, duration_ms: 1 })
    expect(JSON.stringify(projected)).not.toContain(detail)
    expect(projected.final_variables).toBeUndefined()
    expect(omitUnavailableReplayReport(projected)).toEqual(projected)
    expect(JSON.stringify(input)).toContain(detail)
  })

  it('omits holes in indexed transport deltas without losing available execution coordinates', () => {
    const sparse: BrowserReplayEvent[] = []
    sparse[2] = { ...report().events[0], event_index: 2 }
    const materialized = [...sparse]
    const projected = omitUnavailableReplayReport(report({ events: materialized, dropped_event_count: 4 }))
    expect(projected.events).toHaveLength(1)
    expect(projected.events[0].event_index).toBe(2)
    expect(projected.dropped_event_count).toBe(4)
    const session = { id: 'run-1', status: 'running', group: 'Synthetic', protocol: 'http', log_level: 'info', current_event_index: 2,
      events: materialized } as ScriptDebugSession
    expect(omitUnavailableReplaySession(session).events).toEqual(projected.events)
  })

  it('omits historical and live session sidecars, including unknown extensions', () => {
    const detail = 'synthetic-unclassified-detail'
    const session = { id: 'run-1', status: 'paused', group: detail, protocol: 'http', log_level: 'trace', current_event_index: 0, dropped_event_count: 4,
      current_action: { phase: 'action', action_index: 0, event_index: 0, action_name: detail, action_type: 'GET', target: detail,
        step_path: ['requests[0]'], leaf_ordinal: 3, parent_action_name: detail, future_detail: detail },
      current_event: { ...report().events[0], body: detail }, events: [{ ...report().events[0], extractions: { ordinary: detail } }],
      report: report({ final_variables: { ordinary: detail } }), logs: [{ message: detail }], raw_output: [detail],
      condition_evidence: { 'action#0#': { expr: detail, result: true } }, future_detail: detail,
    } as unknown as ScriptDebugSession
    const projected = omitUnavailableReplaySession(session)
    expect(projected).toMatchObject({ runtime_evidence_unavailable: true, id: 'run-1', status: 'paused', current_event_index: 0, dropped_event_count: 4,
      current_action: { phase: 'action', action_index: 0, event_index: 0, step_path: ['requests[0]'], leaf_ordinal: 3 } })
    expect(JSON.stringify(projected)).not.toContain(detail)
    expect(projected.logs).toBeUndefined()
    expect(projected.raw_output).toBeUndefined()
    expect(projected.condition_evidence).toBeUndefined()
    expect(omitUnavailableReplaySession(projected)).toEqual(projected)
  })
})
