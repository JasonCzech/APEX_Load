import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, request } from '../hooks/apiClient'
import { useWebSocket } from '../hooks/useWebSocket'
import { bridgeRequest, type BrowserRecorderBridgeConnection } from '../hooks/api/browserRecorderBridge'
import {
  type WSMessage,
  type BrowserReplayEvent,
  type BrowserReplayReport,
  type BrowserReplaySession,
  type BrowserReplaySessionResponse,
  type ReplayRepairSuggestion,
} from '../types'
import BrowserReplayHARDownloads from './BrowserReplayHARDownloads'
import RepairSuggestionList from './RepairSuggestionList'

export interface BrowserReplayProgressProps {
  replaySessionId: string
  projectId?: string
  onDismiss?: () => void
  onApplySuggestion?: (suggestion: ReplayRepairSuggestion) => Promise<void> | void
  onComplete?: (report: BrowserReplayReport | null, events: BrowserReplayEvent[]) => void
  /** Reports the steps replayed so far and the latest failure, as they arrive. */
  onProgress?: (progress: BrowserReplayProgressSummary) => void
  bridge?: BrowserRecorderBridgeConnection | null
}

export interface BrowserReplayProgressSummary {
  steps: number
  lastError?: string
}

type Phase = 'running' | 'completed' | 'failed' | 'stopped'
type TerminalPhase = Exclude<Phase, 'running'>
const REPLAY_STATUS_POLL_MS = 1500
const REPLAY_NOT_FOUND_RETRY_LIMIT = 4

// BrowserReplayProgress is a compact, read-only verification view: it replays
// the just-recorded script and shows per-step pass/fail with selector self-heal
// suggestions, subscribing to the same browser_replay_* events the full
// debugger uses.
export default function BrowserReplayProgress({ replaySessionId, projectId, onDismiss, onApplySuggestion, onComplete, onProgress, bridge }: BrowserReplayProgressProps) {
  const [events, setEvents] = useState<BrowserReplayEvent[]>([])
  const [phase, setPhase] = useState<Phase>('running')
  const [report, setReport] = useState<BrowserReplayReport | null>(null)
  const [expanded, setExpanded] = useState<string | null>(null)
  const [rejected, setRejected] = useState<Record<string, boolean>>({})
  const [applying, setApplying] = useState<Record<string, boolean>>({})
  const [inactive, setInactive] = useState(false)
  const [unavailable, setUnavailable] = useState(false)
  const timeoutRef = useRef<number | null>(null)
  const eventsRef = useRef<BrowserReplayEvent[]>([])
  const completedRef = useRef(false)
  const notFoundCountRef = useRef(0)
  const onCompleteRef = useRef(onComplete)
  const onApplySuggestionRef = useRef(onApplySuggestion)
  const onProgressRef = useRef(onProgress)
  onCompleteRef.current = onComplete
  onApplySuggestionRef.current = onApplySuggestion
  onProgressRef.current = onProgress
  const replayScopeGenerationRef = useRef(0)
  const applyingSuggestionIdsRef = useRef<Set<string>>(new Set())
  const bridgeScopeKey = bridge
    ? `${bridge.origin}\u0000${bridge.session_id}\u0000${bridge.token}`
    : 'controller'

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
    replayScopeGenerationRef.current += 1
    applyingSuggestionIdsRef.current = new Set()
    setEvents([])
    setPhase('running')
    setReport(null)
    setRejected({})
    setApplying({})
    setInactive(false)
    setUnavailable(false)
    eventsRef.current = []
    completedRef.current = false
    notFoundCountRef.current = 0
    armInactivityTimer()
    return clearInactivityTimer
  }, [armInactivityTimer, bridgeScopeKey, clearInactivityTimer, projectId, replaySessionId])

  const adoptSessionEvents = useCallback((incoming: BrowserReplayEvent[] | undefined, authoritative = false) => {
    if (!incoming) return eventsRef.current.filter(Boolean)
    const next = authoritative ? [] as BrowserReplayEvent[] : eventsRef.current.slice()
    incoming.forEach((event, position) => {
      if (!isBrowserReplayEvent(event)) return
      const index = Number.isInteger(event.event_index) && (event.event_index ?? -1) >= 0
        ? event.event_index!
        : position
      next[index] = event
    })
    eventsRef.current = next
    setEvents(next)
    return next.filter(Boolean)
  }, [])

  const complete = useCallback((nextPhase: TerminalPhase, nextReport: BrowserReplayReport | null) => {
    if (completedRef.current) return
    completedRef.current = true
    clearInactivityTimer()
    setInactive(false)
    setPhase(nextPhase)
    if (nextReport) setReport(nextReport)
    onCompleteRef.current?.(nextReport, eventsRef.current.filter(Boolean))
  }, [clearInactivityTimer])

  const reportFromSession = useCallback((session: BrowserReplaySession, nextPhase: TerminalPhase): BrowserReplayReport | null => {
    if (isBrowserReplayReport(session.report)) return session.report
    const currentEvents = eventsRef.current.filter(Boolean)
    if (currentEvents.length === 0 && nextPhase === 'stopped') return null
    const first = currentEvents[0]
    const last = currentEvents[currentEvents.length - 1]
    return {
      replay_session_id: session.id || replaySessionId,
      group: session.group || 'Replay verification',
      protocol: session.protocol || 'browser',
      started_at: session.started_at || first?.started_at || '',
      completed_at: session.completed_at || last?.completed_at || new Date().toISOString(),
      success: nextPhase === 'completed' && currentEvents.every(event => event.success),
      error: session.error,
      events: currentEvents,
    }
  }, [replaySessionId])

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
        const finalReport = reportFromMessage(msg, 'completed')
        adoptSessionEvents(finalReport?.events, true)
        complete('completed', finalReport)
      } else if (msg.type === 'browser_replay_failed') {
        const finalReport = reportFromMessage(msg, 'failed')
        adoptSessionEvents(finalReport?.events, true)
        complete('failed', finalReport)
      } else if (msg.type === 'browser_replay_stopped') {
        const finalReport = reportFromMessage(msg, 'stopped')
        adoptSessionEvents(finalReport?.events, true)
        complete('stopped', finalReport)
      }
    },
    [adoptSessionEvents, armInactivityTimer, complete, replaySessionId, reportFromMessage],
  )

  useWebSocket({ projectId, onMessage: handleMessage, bridge })

  const adoptSession = useCallback((session: BrowserReplaySession) => {
    if (completedRef.current || session.id !== replaySessionId) return
    const finalReport = isBrowserReplayReport(session.report) ? session.report : null
    const nextPhase: TerminalPhase | null = session.status === 'completed'
      ? 'completed'
      : session.status === 'failed'
        ? 'failed'
        : session.status === 'stopped'
          ? 'stopped'
          : null
    // Stop/failure is first recorded as a cancellation request; the worker
    // publishes completed_at (and, when available, the final report) only
    // after it has actually exited. Keep polling an early terminal-looking
    // snapshot so it cannot suppress the authoritative final WebSocket event.
    const finalized = Boolean(session.completed_at) || finalReport !== null
    adoptSessionEvents(finalReport?.events ?? session.events, Boolean(nextPhase && finalized))
    if (nextPhase && finalized) complete(nextPhase, finalReport || reportFromSession(session, nextPhase))
  }, [adoptSessionEvents, complete, replaySessionId, reportFromSession])

  // The replay can finish before this component's debounced WebSocket opens.
  // Reconcile immediately and then poll while live so a missed terminal event
  // cannot leave recorder/import workflows permanently blocked.
  useEffect(() => {
    if (completedRef.current) return
    let cancelled = false
    let pollTimer: number | null = null
    const abortController = new AbortController()
    const path = `/browser-replay/sessions/${encodeURIComponent(replaySessionId)}`

    const poll = async () => {
      try {
        const response = bridge
          ? await bridgeRequest<BrowserReplaySessionResponse>(bridge, path, { signal: abortController.signal })
          : await request<BrowserReplaySessionResponse>(path, { signal: abortController.signal })
        notFoundCountRef.current = 0
        if (!cancelled && !completedRef.current && response?.session) adoptSession(response.session)
      } catch (cause: unknown) {
        if (
          !cancelled
          && !completedRef.current
          && cause instanceof ApiError
          && cause.status === 404
        ) {
          notFoundCountRef.current += 1
          if (notFoundCountRef.current > REPLAY_NOT_FOUND_RETRY_LIMIT) {
            setUnavailable(true)
            complete('failed', null)
            return
          }
        }
        // WebSocket delivery remains the fast path; transient status failures
        // are retried while the replay is still live.
      }
      if (!cancelled && !completedRef.current) {
        pollTimer = window.setTimeout(poll, REPLAY_STATUS_POLL_MS)
      }
    }

    void poll()
    return () => {
      cancelled = true
      abortController.abort()
      if (pollTimer !== null) window.clearTimeout(pollTimer)
    }
  }, [adoptSession, bridge, complete, replaySessionId])

  const real = events.filter(Boolean)
  const passed = real.filter(e => e.success).length
  const failed = real.filter(e => !e.success).length
  let lastError: string | undefined
  for (const event of real) {
    if (!event.success && event.error_message) lastError = event.error_message
  }
  useEffect(() => {
    onProgressRef.current?.({ steps: real.length, lastError })
  }, [real.length, lastError])
  const reportSuggestions: ReplayRepairSuggestion[] = report?.repair_suggestions ?? []

  const applySuggestion = async (suggestion: ReplayRepairSuggestion) => {
    const applyCurrentSuggestion = onApplySuggestionRef.current
    if (!applyCurrentSuggestion) return
    const inFlightSuggestionIds = applyingSuggestionIdsRef.current
    if (inFlightSuggestionIds.has(suggestion.id)) return
    const replayScopeGeneration = replayScopeGenerationRef.current
    // React does not commit the disabled state until after this event turn.
    // Fence the callback synchronously so rapid clicks (including duplicate
    // report/event renderings of the same suggestion) cannot apply twice.
    inFlightSuggestionIds.add(suggestion.id)
    setApplying(prev => ({ ...prev, [suggestion.id]: true }))
    try {
      await applyCurrentSuggestion(suggestion)
    } finally {
      inFlightSuggestionIds.delete(suggestion.id)
      if (replayScopeGenerationRef.current === replayScopeGeneration) {
        setApplying(prev => ({ ...prev, [suggestion.id]: false }))
      }
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

      {/* A controller replay's HAR (native when it was recorded, by record_har
          or the script's own capture policy, else synthetic) and evidence ZIP;
          a bridge replay has none here, nor does a session the controller
          could not confirm. */}
      {!bridge && !unavailable && phase !== 'running' && <BrowserReplayHARDownloads sessionId={replaySessionId} />}
      {report && <details><summary>Verified output and scope</summary><p>{report.group} · {report.protocol}</p><p>Script fingerprint: {report.script_hash || 'Unavailable'}</p><p>{report.completed_at}</p></details>}
      {real.length === 0 && phase === 'running' && (
        <div className="brp-replay-empty">Launching replay…</div>
      )}
      {inactive && phase === 'running' && (
        <div className="brp-replay-empty" role="status">Still waiting for replay events…</div>
      )}
      {unavailable && (
        <div className="brp-replay-empty" role="alert">Replay session could not be confirmed. Start verification again.</div>
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
