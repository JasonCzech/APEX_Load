import { useEffect, useRef, useState } from 'react'
import type { BrowserReplayEvent, BrowserReplayReport, ScriptDebugSession } from '../types/replay'

export interface DebugCompletionSnapshot {
  key: string
  scriptName: string
  draft: boolean
  report: BrowserReplayReport | null
  session: ScriptDebugSession
}
export interface DebugSummaryIssue {
  key: string
  severity: 'error' | 'warning'
  message: string
  actionName?: string
  phase?: string
  statusCode?: number
  eventIndex?: number
}

export function debugCompletionKey(session: ScriptDebugSession | null, report: BrowserReplayReport | null, running: boolean): string | null {
  if (running || !session?.completed_at || !['completed', 'failed', 'stopped'].includes(session.status)) return null
  // A terminal report accompanies normal completion. Session-level failures can
  // terminate before a report exists; never substitute a local connection error.
  if (session.status === 'completed' && !report && !session.report) return null
  if (report && (!report.completed_at || report.replay_session_id && report.replay_session_id !== session.id)) return null
  return `${session.id}:${session.session_incarnation || session.started_at || ''}`
}

export function useDebugCompletionSummary({ session, report, running, scriptName, draft, onComplete }: {
  session: ScriptDebugSession | null; report: BrowserReplayReport | null; running: boolean
  scriptName: string; draft: boolean; onComplete: () => void
}) {
  const seen = useRef(new Set<string>())
  const [snapshot, setSnapshot] = useState<DebugCompletionSnapshot | null>(null)
  useEffect(() => {
    const key = debugCompletionKey(session, report, running)
    if (!key || !session || seen.current.has(key)) return
    seen.current.add(key)
    setSnapshot(structuredClone({ key, session, report: report || session.report || null, scriptName, draft }))
    onComplete()
  }, [session, report, running, scriptName, draft, onComplete])
  return snapshot
}

export function debugSummaryModel(snapshot: DebugCompletionSnapshot) {
  const { session, report } = snapshot
  const unavailable = Boolean(session.runtime_evidence_unavailable || report?.runtime_evidence_unavailable)
  const unique = new Set<string>()
  const events: Array<{ event: BrowserReplayEvent; index: number }> = []
  if (!unavailable) (report?.events || session.events || []).forEach((event, index) => {
    const key = event.event_id || `${event.event_index}:${event.phase}:${event.action_index}:${event.started_at}`
    if (unique.has(key) || ['http_sequence', 'control_if', 'control_switch', 'control_loop', 'control_parallel'].includes(event.action_type)) return
    unique.add(key)
    events.push({ event, index })
  })
  const issues: DebugSummaryIssue[] = events.filter(({ event }) => !event.success).map(({ event, index }) => ({
    key: event.event_id || `event-${index}`, severity: 'error', message: event.error_message || 'Action failed.',
    actionName: event.action_name, phase: event.phase, statusCode: event.status_code, eventIndex: index,
  }))
  const messages = new Set(issues.map(issue => issue.message.trim()))
  if (!unavailable) {
    for (const message of [report?.error, session.error]) {
      if (message?.trim() && !messages.has(message.trim())) {
        messages.add(message.trim())
        issues.push({ key: `session-${issues.length}`, severity: 'error', message })
      }
    }
    const seenLogs = new Set<string>()
    for (const log of session.logs || []) {
      if (!['error', 'warn'].includes(log.level)) continue
      if (log.level === 'error' && messages.has(log.message.trim())) continue
      const logKey = JSON.stringify([log.level, log.phase, log.action_index, log.action_name, log.message.trim()])
      if (seenLogs.has(logKey)) continue
      seenLogs.add(logKey)
      // Action completion logs describe the same failure already in the report.
      if (log.level === 'error' && log.action_name && issues.some(issue => issue.actionName === log.action_name && issue.phase === log.phase)) continue
      if (log.level === 'error') messages.add(log.message.trim())
      issues.push({ key: `log-${log.id}`, severity: log.level === 'warn' ? 'warning' : 'error', message: log.message, actionName: log.action_name, phase: log.phase })
    }
  }
  const startedAt = report?.started_at || session.started_at
  const completedAt = report?.completed_at || session.completed_at
  const elapsed = startedAt && completedAt ? Date.parse(completedAt) - Date.parse(startedAt) : NaN
  return {
    events, issues, unavailable,
    incomplete: unavailable || Boolean(session.dropped_event_count || report?.dropped_event_count || report?.truncated_event_evidence_count),
    passed: events.filter(({ event }) => event.success).length,
    failed: events.filter(({ event }) => !event.success).length,
    outcome: session.status === 'stopped' || report?.stopped ? 'Stopped' : session.status === 'failed' || report?.success === false ? 'Failed' : 'Passed',
    startedAt,
    elapsedMs: Number.isFinite(elapsed) && elapsed >= 0 ? elapsed : null,
  }
}
