import {
  BrowserReplayEvent,
  BrowserReplayReport,
  BrowserReplaySession,
} from '../types'

interface Props {
  sessionId?: string | null
  session?: BrowserReplaySession | null
  report?: BrowserReplayReport | null
  running: boolean
  selectedEventIndex?: number | null
  onSelectEvent?: (index: number) => void
}

function eventLabel(event: BrowserReplayEvent, index: number): string {
  return event.action_name || `${event.action_type || 'Action'} ${index + 1}`
}

function findViewportEvent(events: BrowserReplayEvent[], selectedIndex?: number | null) {
  if (typeof selectedIndex === 'number' && selectedIndex >= 0) {
    const selected = events[selectedIndex]
    if (selected?.screenshot_base64 || selected?.snapshot?.screenshot_base64) return selected
  }
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i]
    if (event.screenshot_base64 || event.snapshot?.screenshot_base64 || event.page_url || event.dom_summary) {
      return event
    }
  }
  return null
}

function formatDuration(ms?: number): string {
  if (typeof ms !== 'number') return 'n/a'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`
  return `${ms.toFixed(1)}ms`
}

export default function BrowserReplayDebugger({
  sessionId,
  session,
  report,
  running,
  selectedEventIndex,
  onSelectEvent,
}: Props) {
  const events = session?.events?.length ? session.events : report?.events || []
  const viewportEvent = findViewportEvent(events, selectedEventIndex)
  const screenshot = viewportEvent?.screenshot_base64 || viewportEvent?.snapshot?.screenshot_base64
  const pageURL = viewportEvent?.page_url || viewportEvent?.snapshot?.page_url
  const pageTitle = viewportEvent?.page_title || viewportEvent?.snapshot?.page_title || viewportEvent?.dom_summary?.title
  const status = session?.status || (running ? 'running' : report?.success ? 'completed' : report ? 'failed' : 'idle')

  return (
    <div className="browser-debugger">
      <div className="browser-debugger-header">
        <div>
          <h4>Browser Debugger</h4>
          <p>{pageURL || sessionId || 'Waiting for the first browser event'}</p>
        </div>
        <span className={`browser-debugger-status ${status}`}>{status}</span>
      </div>

      <div className="browser-debugger-layout">
        <div className="browser-viewport">
          <div className="browser-viewport-bar">
            <span className="browser-dot red" />
            <span className="browser-dot yellow" />
            <span className="browser-dot green" />
            <span className="browser-address">{pageTitle || pageURL || 'No page captured yet'}</span>
          </div>
          <div className="browser-viewport-stage">
            {screenshot ? (
              <img src={`data:image/png;base64,${screenshot}`} alt="Browser replay viewport" />
            ) : (
              <div className="browser-viewport-empty">
                <strong>No screenshot yet</strong>
                <span>Use a screenshot action or enable per-action capture for a live viewport.</span>
              </div>
            )}
          </div>
        </div>

        <div className="browser-debugger-side">
          <div className="browser-debugger-metrics">
            <div>
              <span>Events</span>
              <strong>{events.length}</strong>
            </div>
            <div>
              <span>Current</span>
              <strong>{typeof session?.current_event_index === 'number' && session.current_event_index >= 0 ? session.current_event_index + 1 : '-'}</strong>
            </div>
            <div>
              <span>Last Duration</span>
              <strong>{formatDuration(viewportEvent?.duration_ms)}</strong>
            </div>
          </div>

          <div className="browser-debugger-events">
            {events.length === 0 ? (
              <div className="browser-debugger-empty">No browser events streamed yet.</div>
            ) : events.map((event, index) => (
              <button
                key={`${event.phase}-${event.action_name}-${index}`}
                className={`browser-debugger-event ${selectedEventIndex === index ? 'active' : ''} ${event.success ? 'success' : 'error'}`}
                onClick={() => onSelectEvent?.(index)}
              >
                <span>{index + 1}</span>
                <div>
                  <strong>{eventLabel(event, index)}</strong>
                  <small>{event.action_type} {event.target || event.selector || ''}</small>
                </div>
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
