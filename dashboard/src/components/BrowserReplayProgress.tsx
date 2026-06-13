import { useEffect, useMemo, useState } from 'react'

import { useWebSocket } from '../hooks/useWebSocket'
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
}

type Phase = 'running' | 'completed' | 'failed' | 'stopped'

// BrowserReplayProgress is a compact, read-only verification view: it replays
// the just-recorded script and shows per-step pass/fail with selector self-heal
// suggestions, subscribing to the same browser_replay_* events the full
// debugger uses.
export default function BrowserReplayProgress({ replaySessionId, projectId, onDismiss }: BrowserReplayProgressProps) {
  const [events, setEvents] = useState<BrowserReplayEvent[]>([])
  const [phase, setPhase] = useState<Phase>('running')
  const [report, setReport] = useState<BrowserReplayReport | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)

  useEffect(() => {
    setEvents([])
    setPhase('running')
    setReport(null)
  }, [replaySessionId])

  const handleMessage = useMemo(
    () => (msg: WSMessage) => {
      if (msg.replay_session_id !== replaySessionId) return
      if (msg.type === 'browser_replay_event' && msg.event) {
        const event = msg.event as BrowserReplayEvent
        setEvents(prev => {
          const idx = event.event_index ?? prev.length
          const next = prev.slice()
          next[idx] = event
          return next
        })
      } else if (msg.type === 'browser_replay_completed') {
        setPhase('completed')
        if (msg.report) setReport(msg.report as BrowserReplayReport)
      } else if (msg.type === 'browser_replay_failed') {
        setPhase('failed')
        if (msg.report) setReport(msg.report as BrowserReplayReport)
      } else if (msg.type === 'browser_replay_stopped') {
        setPhase('stopped')
        if (msg.report) setReport(msg.report as BrowserReplayReport)
      }
    },
    [replaySessionId],
  )

  useWebSocket({ projectId, onMessage: handleMessage })

  const real = events.filter(Boolean)
  const passed = real.filter(e => e.success).length
  const failed = real.filter(e => !e.success).length
  const reportSuggestions: ReplayRepairSuggestion[] = report?.repair_suggestions ?? []

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
                  {suggestions.map(s => (
                    <div key={s.id} className="brp-repair-suggestion">
                      <strong>{s.title}</strong>
                      {typeof s.confidence === 'number' && <span className="brp-repair-confidence"> {Math.round(s.confidence * 100)}%</span>}
                      <div className="brp-repair-desc">{s.description}</div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>

      {reportSuggestions.length > 0 && (
        <div className="brp-replay-suggestions">
          <div className="brp-warnings-title">Self-heal suggestions</div>
          {reportSuggestions.map(s => (
            <div key={s.id} className="brp-repair-suggestion">
              <strong>{s.title}</strong>
              {typeof s.confidence === 'number' && <span className="brp-repair-confidence"> {Math.round(s.confidence * 100)}%</span>}
              <div className="brp-repair-desc">{s.description}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
