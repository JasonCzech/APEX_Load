import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugAction, ScriptDebugSession } from '../types/replay'
import { mergeReplayVariables } from './scriptEditorUtils'

export const RUNTIME_VARIABLE_EVIDENCE_UNAVAILABLE = 'Runtime variable evidence unavailable. Credential classification could not be completed; retained values and reconstructed history are hidden.'

export function runtimeEvidenceUnavailable(...sources: Array<{ runtime_evidence_unavailable?: boolean } | null | undefined>): boolean {
  return sources.some(source => source?.runtime_evidence_unavailable === true)
}

export function replayVariablesForInspection(report: BrowserReplayReport | null, seed: Record<string, string>, unavailable: boolean): Record<string, string> | null {
  if (unavailable || !report || runtimeEvidenceUnavailable(report)) return null
  return report.completed_at && report.final_variables ? report.final_variables : mergeReplayVariables(seed, report.events)
}

// An incomplete credential classification cannot safely repair previously
// truncated evidence. Retain execution coordinates and outcomes, omit details.
export function omitUnavailableReplayEvent(event: BrowserReplayEvent): BrowserReplayEvent {
  return {
    event_id: event.event_id, phase: event.phase, action_index: event.action_index, event_index: event.event_index,
    action_name: 'Action evidence unavailable', action_type: event.action_type, method: event.method, target: '',
    status_code: event.status_code, success: event.success, started_at: event.started_at, completed_at: event.completed_at,
    duration_ms: event.duration_ms, error_message: event.error_message ? 'Replay error detail unavailable' : undefined,
    request_index: event.request_index, request_count: event.request_count, leaf_ordinal: event.leaf_ordinal, step_path: event.step_path,
    iteration: event.iteration, iteration_path: event.iteration_path, response_body_state: 'not_retained',
  }
}

export function omitUnavailableReplayAction(action: ScriptDebugAction): ScriptDebugAction {
  return {
    event_id: action.event_id, phase: action.phase, action_index: action.action_index, event_index: action.event_index,
    action_name: 'Action evidence unavailable', action_type: action.action_type, method: action.method,
    request_index: action.request_index, request_count: action.request_count, leaf_ordinal: action.leaf_ordinal, step_path: action.step_path,
    iteration: action.iteration, iteration_path: action.iteration_path,
  }
}

export function omitUnavailableReplayReport(report: BrowserReplayReport): BrowserReplayReport {
  return {
    runtime_evidence_unavailable: true, dropped_event_count: report.dropped_event_count, truncated_event_evidence_count: report.truncated_event_evidence_count,
    stopped: report.stopped, replay_session_id: report.replay_session_id,
    script_hash: report.script_hash, script_revision: report.script_revision,
    group: 'replay', protocol: report.protocol, started_at: report.started_at, completed_at: report.completed_at,
    success: false, error: 'Runtime evidence unavailable', events: report.events.filter(event => event != null).map(omitUnavailableReplayEvent),
  }
}

export function omitUnavailableReplaySession(session: ScriptDebugSession): ScriptDebugSession {
  return {
    runtime_evidence_unavailable: true, dropped_event_count: session.dropped_event_count,
    session_incarnation: session.session_incarnation, state_revision: session.state_revision,
    runtime_state: session.session_incarnation && session.state_revision ? { version: 1, kind: 'full', session_id: session.id, session_incarnation: session.session_incarnation, state_revision: session.state_revision, state: 'unavailable', reason: 'runtime_evidence_unavailable', snapshot: null } : undefined,
    id: session.id, mode: session.mode, script_id: session.script_id, project_id: session.project_id,
    status: session.status, group: 'script debug', protocol: session.protocol, log_level: session.log_level,
    preflight: session.preflight, started_at: session.started_at, completed_at: session.completed_at,
    current_event_index: session.current_event_index,
    current_action: session.current_action ? omitUnavailableReplayAction(session.current_action) : undefined,
    current_event: session.current_event ? omitUnavailableReplayEvent(session.current_event) : undefined,
    events: session.events?.filter(event => event != null).map(omitUnavailableReplayEvent), report: session.report ? omitUnavailableReplayReport(session.report) : undefined,
    breakpoints: session.breakpoints, break_on_failure: session.break_on_failure, error: 'Runtime evidence unavailable',
  }
}
