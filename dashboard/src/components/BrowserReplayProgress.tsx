import { useEffect, useMemo, useRef, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
import { type BrowserRecorderBridgeConnection } from '../hooks/api/browserRecorderBridge'
import {
  type WSMessage,
  type BrowserReplayEvent,
  type BrowserReplayReport,
  type ReplayRepairSuggestion,
} from '../types'

export interface BrowserReplayProgressProps {
  replaySessionId: string
  projectId?: string
  onDismiss?: () => void
  onApplySuggestion?: (suggestion: ReplayRepairSuggestion) => Promise<void> | void
  onComplete?: (report: BrowserReplayReport | null, events: BrowserReplayEvent[]) => void
  bridge?: BrowserRecorderBridgeConnection | null
}

type Phase = 'running' | 'completed' | 'failed' | 'stopped'

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
  const timeoutRef = useRef<number | null>(null)
  const eventsRef = useRef<BrowserReplayEvent[]>([])

  const clearInactivityTimer = () => {
    if (timeoutRef.current !== null) {
      window.clearTimeout(timeoutRef.current)
      timeoutRef.current = null
    }
  }

  const armInactivityTimer = () => {
    clearInactivityTimer()
    timeoutRef.current = window.setTimeout(() => {
      setPhase('stopped')
      onComplete?.(null, eventsRef.current.filter(Boolean))
    }, 30000)
  }

  useEffect(() => {
    setEvents([])
    setPhase('running')
    setReport(null)
    setRejected({})
    setApplying({})
    eventsRef.current = []
    armInactivityTimer()
    return clearInactivityTimer
  }, [replaySessionId])

  const handleMessage = useMemo(
    () => (msg: WSMessage) => {
      if (msg.replay_session_id !== replaySessionId) return
      armInactivityTimer()
      if (msg.type === 'browser_replay_event' && msg.event) {
        if (!isBrowserReplayEvent(msg.event)) return
        const event = msg.event
        setEvents(prev => {
          const idx = event.event_index ?? prev.length
          const next = prev.slice()
          next[idx] = event
          eventsRef.current = next.filter(Boolean)
          return next
        })
      } else if (msg.type === 'browser_replay_completed') {
        clearInactivityTimer()
        setPhase('completed')
        const nextReport = isBrowserReplayReport(msg.report) ? msg.report : null
        if (nextReport) setReport(nextReport)
        onComplete?.(nextReport, eventsRef.current.filter(Boolean))
      } else if (msg.type === 'browser_replay_failed') {
        clearInactivityTimer()
        setPhase('failed')
        const nextReport = isBrowserReplayReport(msg.report) ? msg.report : null
        if (nextReport) setReport(nextReport)
        onComplete?.(nextReport, eventsRef.current.filter(Boolean))
      } else if (msg.type === 'browser_replay_stopped') {
        clearInactivityTimer()
        setPhase('stopped')
        const nextReport = isBrowserReplayReport(msg.report) ? msg.report : null
        if (nextReport) setReport(nextReport)
        onComplete?.(nextReport, eventsRef.current.filter(Boolean))
      }
    },
    [replaySessionId, onComplete],
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

  const renderSuggestion = (suggestion: ReplayRepairSuggestion) => {
    if (rejected[suggestion.id]) return null
    return (
      <div key={suggestion.id} className="brp-repair-suggestion">
        <strong>{suggestion.title}</strong>
        {typeof suggestion.confidence === 'number' && <span className="brp-repair-confidence"> {Math.round(suggestion.confidence * 100)}%</span>}
        <div className="brp-repair-desc">{suggestion.description}</div>
        {(onApplySuggestion || suggestion.draft_patch_type === 'promote_selector') && (
          <div className="brp-repair-actions">
            {onApplySuggestion && (
              <button type="button" className="rec-inline-action" disabled={!!applying[suggestion.id]} onClick={() => applySuggestion(suggestion)}>
                {applying[suggestion.id] ? 'Applying...' : 'Accept'}
              </button>
            )}
            <button type="button" className="rec-inline-action" onClick={() => setRejected(prev => ({ ...prev, [suggestion.id]: true }))}>Reject</button>
          </div>
        )}
      </div>
    )
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
                  {suggestions.map(renderSuggestion)}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {reportSuggestions.length > 0 && (
        <div className="brp-replay-suggestions">
          <div className="brp-warnings-title">Self-heal suggestions</div>
          {reportSuggestions.map(renderSuggestion)}
        </div>
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
