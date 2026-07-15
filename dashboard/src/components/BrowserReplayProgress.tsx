import { useCallback, useEffect, useRef, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
import { type BrowserRecorderBridgeConnection } from '../hooks/api/browserRecorderBridge'
import {
  type WSMessage,
  type BrowserReplayEvent,
  type BrowserReplayReport,
  type ReplayRepairSuggestion,
} from '../types'
import RepairSuggestionList from './RepairSuggestionList'

export interface BrowserReplayProgressProps {
  replaySessionId: string
  projectId?: string
  onDismiss?: () => void
  onApplySuggestion?: (suggestion: ReplayRepairSuggestion) => Promise<void> | void
  onComplete?: (report: BrowserReplayReport | null, events: BrowserReplayEvent[]) => void
  bridge?: BrowserRecorderBridgeConnection | null
}

type Phase = 'running' | 'completed' | 'failed' | 'stopped'
type TerminalPhase = Exclude<Phase, 'running'>

// BrowserReplayProgress is a compact, read-only verification view: it replays
// the just-recorded script and shows per-step pass/fail with selector self-heal
// suggestions, subscribing to the same browser_replay_* events the full
// debugger uses.
export default function BrowserReplayProgress({ replaySessionId, projectId, onDismiss, onApplySuggestion, onComplete, bridge }: BrowserReplayProgressProps) {
  const [events, setEvents] = useState<BrowserReplayEvent[]>([])
  const [phase, setPhase] = useState<Phase>('running')
  const [report, setReport] = useState<BrowserReplayReport | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [rejected, setRejected] = useState<Record<string, boolean>>({})
  const [applying, setApplying] = useState<Record<string, boolean>>({})
  const [inactive, setInactive] = useState(false)
  const timeoutRef = useRef<number | null>(null)
  const eventsRef = useRef<BrowserReplayEvent[]>([])
  const completedRef = useRef(false)

  const clearInactivityTimer = useCallback(() => {
    if (timeoutRef.current !== null) {
      window.clearTimeout(timeoutRef.current)
      timeoutRef.current = null
    }
  }, [])

  const armInactivityTimer = useCallback(() => {
    clearInactivityTimer()
    timeoutRef.current = window.setTimeout(() => {
      setInactive(true)
    }, 30000)
  }, [clearInactivityTimer])

  useEffect(() => {
    setEvents([])
    setPhase('running')
    setReport(null)
    setRejected({})
    setApplying({})
    setInactive(false)
    eventsRef.current = []
    completedRef.current = false
    armInactivityTimer()
    return clearInactivityTimer
  }, [armInactivityTimer, clearInactivityTimer, replaySessionId])

  const complete = useCallback((nextPhase: TerminalPhase, nextReport: BrowserReplayReport | null) => {
    if (completedRef.current) return
    completedRef.current = true
    clearInactivityTimer()
    setInactive(false)
    setPhase(nextPhase)
    if (nextReport) setReport(nextReport)
    onComplete?.(nextReport, eventsRef.current.filter(Boolean))
  }, [clearInactivityTimer, onComplete])

  const reportFromMessage = useCallback((msg: WSMessage, nextPhase: TerminalPhase): BrowserReplayReport | null => {
    if (isBrowserReplayReport(msg.report)) return msg.report
    const sessionReport = typeof msg.session === 'object' && msg.session !== null
      ? (msg.session as { report?: unknown }).report
      : null
    if (isBrowserReplayReport(sessionReport)) return sessionReport
    const currentEvents = eventsRef.current.filter(Boolean)
    if (currentEvents.length === 0 && nextPhase === 'stopped') return null
    const first = currentEvents[0]
    const last = currentEvents[currentEvents.length - 1]
    return {
      replay_session_id: msg.replay_session_id || replaySessionId,
      group: 'Replay verification',
      protocol: 'browser',
      started_at: first?.started_at || '',
      completed_at: last?.completed_at || new Date().toISOString(),
      success: nextPhase === 'completed' && currentEvents.every(event => event.success),
      events: currentEvents,
    }
  }, [replaySessionId])

  const handleMessage = useCallback(
    (msg: WSMessage) => {
      if (msg.replay_session_id !== replaySessionId) return
      if (completedRef.current) return
      armInactivityTimer()
      setInactive(false)
      if (msg.type === 'browser_replay_event' && msg.event) {
        if (!isBrowserReplayEvent(msg.event)) return
        const event = msg.event
        const idx = event.event_index ?? eventsRef.current.length
        const next = eventsRef.current.slice()
        next[idx] = event
        // Update the ref before scheduling React state so a terminal WebSocket
        // message in the same batch includes the final event in its report.
        eventsRef.current = next
        setEvents(next)
      } else if (msg.type === 'browser_replay_completed') {
        complete('completed', reportFromMessage(msg, 'completed'))
      } else if (msg.type === 'browser_replay_failed') {
        complete('failed', reportFromMessage(msg, 'failed'))
      } else if (msg.type === 'browser_replay_stopped') {
        complete('stopped', reportFromMessage(msg, 'stopped'))
      }
    },
    [armInactivityTimer, complete, replaySessionId, reportFromMessage],
  )

  useWebSocket({ projectId, onMessage: handleMessage, bridge })

  const real = events.filter(Boolean)
  const passed = real.filter(e => e.success).length
  const failed = real.filter(e => !e.success).length
  const reportSuggestions: ReplayRepairSuggestion[] = report?.repair_suggestions ?? []

  const applySuggestion = async (suggestion: ReplayRepairSuggestion) => {
    if (!onApplySuggestion) return
    setApplying(prev => ({ ...prev, [suggestion.id]: true }))
    try {
      await onApplySuggestion(suggestion)
    } finally {
      setApplying(prev => ({ ...prev, [suggestion.id]: false }))
    }
  }

  return (
    <div className="brp-replay-progress" data-testid="replay-progress">
      <div className="brp-replay-header">
        <span className={`brp-status brp-replay-${phase}`}>
          {phase === 'running' ? 'Verifying…' : phase === 'completed' ? 'Verified ✓' : phase === 'failed' ? 'Issues found' : 'Stopped'}
        </span>
        <span className="brp-replay-counts">{passed} passed · {failed} failed</span>
        {onDismiss && <button type="button" className="rec-inline-action" onClick={onDismiss}>Close</button>}
      </div>

      {real.length === 0 && phase === 'running' && (
        <div className="brp-replay-empty">Launching replay…</div>
      )}
      {inactive && phase === 'running' && (
        <div className="brp-replay-empty" role="status">Still waiting for replay events…</div>
      )}

      <div className="brp-replay-events">
        {real.map((event, idx) => {
          const key = event.event_id || `evt-${idx}`
          const suggestions = event.repair_suggestions ?? []
          return (
            <div key={key} className={`brp-replay-event ${event.success ? 'ok' : 'fail'}`}>
              <span className="brp-replay-icon">{event.success ? '✓' : '✕'}</span>
              <span className="brp-replay-action" title={event.target || event.selector}>{event.action_name || event.action_type}</span>
              <span className="brp-replay-dur">{Math.round(event.duration_ms || 0)}ms</span>
              {suggestions.length > 0 && (
                <button type="button" className="rec-inline-action" onClick={() => setExpanded(expanded === key ? null : key)}>
                  {suggestions.length} fix{suggestions.length === 1 ? '' : 'es'}
                </button>
              )}
              {!event.success && event.error_message && expanded !== key && (
                <span className="brp-replay-error" title={event.error_message}>{event.error_message}</span>
              )}
              {expanded === key && (
                <div className="brp-replay-suggestions">
                  {event.error_message && <div className="brp-replay-error-full">{event.error_message}</div>}
                  <RepairSuggestionList
                    suggestions={suggestions}
                    itemClassName="brp-repair-suggestion"
                    confidenceClassName="brp-repair-confidence"
                    descriptionClassName="brp-repair-desc"
                    actionsClassName="brp-repair-actions"
                    actionButtonClassName="rec-inline-action"
                    rejected={rejected}
                    applying={applying}
                    onApplySuggestion={onApplySuggestion ? applySuggestion : undefined}
                    onRejectSuggestion={suggestion => setRejected(prev => ({ ...prev, [suggestion.id]: true }))}
                  />
                </div>
              )}
            </div>
          )
        })}
      </div>

      {reportSuggestions.length > 0 && (
        <RepairSuggestionList
          suggestions={reportSuggestions}
          className="brp-replay-suggestions"
          itemClassName="brp-repair-suggestion"
          label="Self-heal suggestions"
          labelClassName="brp-warnings-title"
          confidenceClassName="brp-repair-confidence"
          descriptionClassName="brp-repair-desc"
          actionsClassName="brp-repair-actions"
          actionButtonClassName="rec-inline-action"
          rejected={rejected}
          applying={applying}
          onApplySuggestion={onApplySuggestion ? applySuggestion : undefined}
          onRejectSuggestion={suggestion => setRejected(prev => ({ ...prev, [suggestion.id]: true }))}
        />
      )}
    </div>
  )
}

function isBrowserReplayEvent(value: unknown): value is BrowserReplayEvent {
  return !!value && typeof value === 'object' && typeof (value as BrowserReplayEvent).action_type === 'string'
}

function isBrowserReplayReport(value: unknown): value is BrowserReplayReport {
  return !!value && typeof value === 'object' && Array.isArray((value as BrowserReplayReport).events)
}
