import { useEffect, useMemo, useState } from 'react'

import {
  BrowserReplayEvent,
  BrowserReplayReport,
  BrowserReplaySession,
  ClientMetrics,
  type ReplayRuntimeProfile,
  type ScriptDebugAction,
} from '../types'
import { formatConsoleEntries, formatNetworkEntries } from './scriptEditorUtils'

function eventStatus(event?: BrowserReplayEvent | null): string {
  if (!event) return 'idle'
  return event.success ? 'success' : 'error'
}

type BrowserReplayDebuggerVariant = 'panel' | 'main'
type BrowserReplaySessionLike = BrowserReplaySession & {
  current_action?: ScriptDebugAction
  log_level?: string
}

interface Props {
  sessionId?: string | null
  session?: BrowserReplaySessionLike | null
  report?: BrowserReplayReport | null
  running: boolean
  variant?: BrowserReplayDebuggerVariant
  selectedEventIndex?: number | null
  onSelectEvent?: (index: number) => void
  onApplySuggestedLocator?: (event: BrowserReplayEvent, locator: string) => void
}

interface BrowserReplayScreenshotDialogProps {
  onClose: () => void
  src: string
}

function eventLabel(event: BrowserReplayEvent, index: number): string {
  return event.action_name || `${event.action_type || 'Action'} ${index + 1}`
}

function sourceStepLabel(event: BrowserReplayEvent): string {
  const parts = [
    event.source_transaction ? `Txn ${event.source_transaction}` : '',
    typeof event.source_step_index === 'number' ? `Step ${event.source_step_index + 1}` : event.source_step_id ? `Step ${event.source_step_id}` : '',
    event.source_action_name || event.source_action || '',
  ].filter(Boolean)
  return parts.join(' · ')
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

function formatWholeMs(ms?: number): string {
  if (typeof ms !== 'number' || !Number.isFinite(ms) || ms <= 0) return 'n/a'
  return `${Math.round(ms)}ms`
}

function eventClientMetrics(event?: BrowserReplayEvent | null): ClientMetrics | undefined {
  return event?.client_metrics || event?.snapshot?.client_metrics
}

function timelineNetworkSummary(event: BrowserReplayEvent): string {
  const entries = event.network_entries || event.snapshot?.network_entries || []
  if (entries.length === 0) return 'Network 0'
  const totalDuration = entries.reduce((sum, entry) => sum + (entry.duration_ms || 0), 0)
  const failed = entries.filter(entry => (entry.status_code || 0) >= 400).length
  return failed > 0
    ? `Network ${entries.length} / ${formatDuration(totalDuration)} / ${failed} failed`
    : `Network ${entries.length} / ${formatDuration(totalDuration)}`
}

function timelineConsoleSummary(event: BrowserReplayEvent): string {
  const entries = event.console_entries || event.snapshot?.console_entries || []
  if (entries.length === 0) return 'Console 0'
  const errors = entries.filter(entry => entry.level === 'error').length
  return errors > 0 ? `Console ${entries.length} / ${errors} error` : `Console ${entries.length}`
}

function timelineVitalsSummary(metrics?: ClientMetrics): string {
  if (!metrics) return 'Core Web Vitals n/a'
  const parts = [
    `LCP ${formatWholeMs(metrics.lcp_ms)}`,
    `INP ${formatWholeMs(metrics.inp_ms)}`,
    typeof metrics.cls_score === 'number' ? `CLS ${metrics.cls_score.toFixed(3)}` : 'CLS n/a',
  ]
  return parts.join(' / ')
}

function timelineSnapshotSummary(event: BrowserReplayEvent): string {
  const hasScreenshot = Boolean(event.screenshot_base64 || event.snapshot?.screenshot_base64)
  const hasHTML = Boolean(event.html || event.snapshot?.html)
  if (hasScreenshot && hasHTML) return 'Screenshot + DOM'
  if (hasScreenshot) return 'Screenshot'
  if (hasHTML) return 'DOM HTML'
  return 'No screenshot'
}

function timelineDOMSummary(event: BrowserReplayEvent): string {
  const domSummary = event.dom_summary || event.snapshot?.dom_summary
  if (!domSummary) return 'DOM n/a'
  const textCount = domSummary.visible_text?.length || 0
  return `DOM ${domSummary.ready_state || 'unknown'} / ${textCount} lines`
}

function formatSelectedEventDetails(event: BrowserReplayEvent, index: number): string {
  const pageURL = event.page_url || event.snapshot?.page_url || 'n/a'
  const pageTitle = event.page_title || event.snapshot?.page_title || event.dom_summary?.title || 'n/a'
  const domSummary = event.dom_summary || event.snapshot?.dom_summary
  const consoleEntries = event.console_entries || event.snapshot?.console_entries || []
  const networkEntries = event.network_entries || event.snapshot?.network_entries || []
  const metrics = eventClientMetrics(event)
  const htmlCaptured = Boolean(event.html || event.snapshot?.html)

  return [
    `Event: ${eventLabel(event, index)}`,
    `Status: ${eventStatus(event)}`,
    sourceStepLabel(event) ? `Source Step: ${sourceStepLabel(event)}` : '',
    event.source_profile ? `Source Profile: ${event.source_profile}` : '',
    `Action: ${event.action_type} ${event.target || event.selector || ''}`.trim(),
    `Duration: ${formatDuration(event.duration_ms)}`,
    `Page Title: ${pageTitle}`,
    `Page URL: ${pageURL}`,
    `DOM Ready State: ${domSummary?.ready_state || 'n/a'}`,
    `Visible Text Lines: ${domSummary?.visible_text?.length || 0}`,
    `Console Entries: ${consoleEntries.length}`,
    `Network Entries: ${networkEntries.length}`,
    `Core Web Vitals: ${timelineVitalsSummary(metrics)}`,
    `HTML Capture: ${htmlCaptured ? 'Captured' : 'Not captured'}`,
    `Error: ${event.error_message || 'None'}`,
  ].filter(Boolean).join('\n')
}

function suggestedLocatorForEvent(event?: BrowserReplayEvent | null): string {
  if (!event) return ''
  const diagnosedLocator = event.diagnosis?.suggested_locator?.trim()
  if (diagnosedLocator) return diagnosedLocator
  for (const suggestion of event.repair_suggestions || []) {
    const fields = suggestion.fields || {}
    const locator = (fields.selector || fields.locator || fields.target || '').trim()
    if (locator) return locator
  }
  return ''
}

function runtimeProfileChips(profile?: ReplayRuntimeProfile, fallbackLogLevel?: string) {
  if (!profile) return []
  return [
    profile.browser_engine ? `Engine ${profile.browser_engine}` : '',
    profile.device_profile ? `Device ${profile.device_profile}` : '',
    profile.capture_mode ? `Capture ${profile.capture_mode}` : '',
    typeof profile.headless === 'boolean' ? (profile.headless ? 'Headless' : 'Headed') : '',
    profile.log_level || fallbackLogLevel ? `Log ${profile.log_level || fallbackLogLevel}` : '',
  ].filter(Boolean)
}

export default function BrowserReplayDebugger({
  sessionId,
  session,
  report,
  running,
  variant = 'panel',
  selectedEventIndex,
  onSelectEvent,
  onApplySuggestedLocator,
}: Props) {
  const mainVariant = variant === 'main'
  const events = session?.events?.length ? session.events : report?.events || []
  const viewportEvent = findViewportEvent(events, selectedEventIndex)
  const selectedEvent = typeof selectedEventIndex === 'number' && selectedEventIndex >= 0
    ? events[selectedEventIndex] || viewportEvent || events[0] || null
    : viewportEvent || events[0] || null
  const screenshot = viewportEvent?.screenshot_base64 || viewportEvent?.snapshot?.screenshot_base64
  const pageURL = viewportEvent?.page_url || viewportEvent?.snapshot?.page_url
  const pageTitle = viewportEvent?.page_title || viewportEvent?.snapshot?.page_title || viewportEvent?.dom_summary?.title
  const status = session?.status || (running ? 'running' : report?.success ? 'completed' : report ? 'failed' : 'idle')
  const selectedPageURL = selectedEvent?.page_url || selectedEvent?.snapshot?.page_url
  const selectedPageTitle = selectedEvent?.page_title || selectedEvent?.snapshot?.page_title || selectedEvent?.dom_summary?.title
  const selectedDOMSummary = selectedEvent?.dom_summary || selectedEvent?.snapshot?.dom_summary
  const selectedConsoleEntries = useMemo(
    () => selectedEvent?.console_entries || selectedEvent?.snapshot?.console_entries || [],
    [selectedEvent],
  )
  const selectedNetworkEntries = useMemo(
    () => selectedEvent?.network_entries || selectedEvent?.snapshot?.network_entries || [],
    [selectedEvent],
  )
  const selectedHTML = selectedEvent?.html || selectedEvent?.snapshot?.html
  const selectedClientMetrics = eventClientMetrics(selectedEvent)
  const suggestedLocator = suggestedLocatorForEvent(selectedEvent)
  const failingEvent = events.find(event => !event.success) || null
  const runtimeProfile = session?.runtime_profile || report?.runtime_profile || report?.artifact_manifest?.runtime_profile
  const runtimeChips = runtimeProfileChips(runtimeProfile, session?.log_level)
  const currentAction = session?.current_action
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const [expandedPreviewKeys, setExpandedPreviewKeys] = useState<Record<string, boolean>>({})
  const [isScreenshotDialogOpen, setIsScreenshotDialogOpen] = useState(false)
  const copySupported = typeof navigator !== 'undefined' && typeof navigator.clipboard?.writeText === 'function'
  const selectedEventIndexValue = selectedEvent ? events.indexOf(selectedEvent) : -1
  const selectedEventDetails = useMemo(
    () => (selectedEvent && selectedEventIndexValue >= 0 ? formatSelectedEventDetails(selectedEvent, selectedEventIndexValue) : ''),
    [selectedEvent, selectedEventIndexValue],
  )

  useEffect(() => {
    setCopiedKey(null)
  }, [selectedEventDetails])

  useEffect(() => {
    if (!screenshot) {
      setIsScreenshotDialogOpen(false)
    }
  }, [screenshot])

  useEffect(() => {
    if (!isScreenshotDialogOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsScreenshotDialogOpen(false)
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [isScreenshotDialogOpen])

  const handleCopy = async (key: string, value: string) => {
    if (!copySupported || !value) return
    await navigator.clipboard.writeText(value)
    setCopiedKey(key)
  }

  const togglePreview = (key: string) => {
    setExpandedPreviewKeys(prev => ({
      ...prev,
      [key]: !prev[key],
    }))
  }

  const selectedArtifactKey = (artifact: string) => `${selectedEventIndexValue}:${artifact}`
  const previewArtifacts = [
    selectedConsoleEntries.length > 0 ? 'console' : null,
    selectedNetworkEntries.length > 0 ? 'network' : null,
    selectedHTML ? 'html' : null,
  ].filter((artifact): artifact is 'console' | 'network' | 'html' => Boolean(artifact))
  const hasPreviewArtifacts = previewArtifacts.length > 0
  const allPreviewArtifactsExpanded = hasPreviewArtifacts && previewArtifacts.every(artifact => expandedPreviewKeys[selectedArtifactKey(artifact)])

  const setAllPreviewsExpanded = (expanded: boolean) => {
    setExpandedPreviewKeys(prev => {
      const next = { ...prev }
      previewArtifacts.forEach(artifact => {
        next[selectedArtifactKey(artifact)] = expanded
      })
      return next
    })
  }

  return (
    <div className={`browser-debugger${mainVariant ? ' browser-debugger-main' : ''}`}>
      <div className="browser-debugger-header">
        <div>
          <h4>{mainVariant ? 'Browser Replay Cockpit' : 'Browser Debugger'}</h4>
          <p>{pageURL || sessionId || 'Waiting for the first browser event'}</p>
          {mainVariant && runtimeChips.length > 0 && (
            <div className="browser-debugger-runtime-chips" aria-label="Replay Runtime">
              {runtimeChips.map(chip => <span key={chip}>{chip}</span>)}
            </div>
          )}
        </div>
        <span className={`browser-debugger-status ${status}`}>{status}</span>
      </div>

      {mainVariant && (currentAction || failingEvent) && (
        <div className="browser-debugger-state-strip">
          {currentAction && (
            <div className="browser-debugger-current-action">
              <span>Selected Action</span>
              <strong>{currentAction.action_name || `${currentAction.phase} ${currentAction.action_index + 1}`}</strong>
              <small>{currentAction.action_type} {currentAction.target || currentAction.selector || ''}</small>
            </div>
          )}
          {failingEvent && (
            <div className="browser-debugger-failure-pin">
              <span>Failure</span>
              <strong>{eventLabel(failingEvent, events.indexOf(failingEvent))}</strong>
              <small>{failingEvent.error_message || failingEvent.diagnosis?.cause || 'Review the selected event evidence.'}</small>
            </div>
          )}
        </div>
      )}

      <div className="browser-debugger-layout">
        <div className="browser-viewport">
          <div className="browser-viewport-bar">
            <span className="browser-dot red" />
            <span className="browser-dot yellow" />
            <span className="browser-dot green" />
            <span className="browser-address">{pageTitle || pageURL || 'No page captured yet'}</span>
            {screenshot && (
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => setIsScreenshotDialogOpen(true)}
              >
                View full size
              </button>
            )}
          </div>
          <div className="browser-viewport-stage">
            {screenshot ? (
              <button
                type="button"
                className="browser-viewport-image-button"
                onClick={() => setIsScreenshotDialogOpen(true)}
              >
                <img src={`data:image/png;base64,${screenshot}`} alt="Browser replay viewport" />
              </button>
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

          <div className="browser-debugger-details">
            <div className="browser-debugger-details-header">
              <div>
                <span className="browser-debugger-section-label">Selected Event</span>
                <strong>{selectedEvent ? eventLabel(selectedEvent, events.indexOf(selectedEvent)) : 'No event selected'}</strong>
              </div>
              <div className="browser-debugger-detail-actions">
                {selectedEvent && hasPreviewArtifacts && (
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    onClick={() => setAllPreviewsExpanded(!allPreviewArtifactsExpanded)}
                  >
                    {allPreviewArtifactsExpanded ? 'Collapse all' : 'Expand all'}
                  </button>
                )}
                {selectedEvent && suggestedLocator && onApplySuggestedLocator && (
                  <button
                    type="button"
                    className="btn btn-sm btn-secondary"
                    onClick={() => onApplySuggestedLocator(selectedEvent, suggestedLocator)}
                  >
                    Use suggested selector
                  </button>
                )}
                {selectedEvent && (
                  <button
                    type="button"
                    className="btn btn-sm btn-ghost"
                    onClick={() => void handleCopy('details', selectedEventDetails)}
                    disabled={!copySupported}
                  >
                    {copiedKey === 'details' ? 'Copied' : 'Copy details'}
                  </button>
                )}
                <span className={`browser-debugger-detail-status ${eventStatus(selectedEvent)}`}>{eventStatus(selectedEvent)}</span>
              </div>
            </div>
            {events.length > 0 && (
              <div className="browser-debugger-timeline" aria-label="Browser Replay Timeline">
                <div className="browser-debugger-timeline-header">
                  <span className="browser-debugger-section-label">Action Timeline</span>
                  <strong>Network waterfall, console, screenshots, Core Web Vitals, and DOM snapshots</strong>
                </div>
                <div className="browser-debugger-timeline-list">
                  {events.map((event, index) => {
                    const metrics = eventClientMetrics(event)
                    const sourceLabel = sourceStepLabel(event)
                    return (
                      <button
                        key={`${event.phase}-${event.action_name}-${index}-timeline`}
                        type="button"
                        className={`browser-debugger-timeline-row ${(selectedEvent === event) ? 'active' : ''} ${event.success ? 'success' : 'error'}`}
                        onClick={() => onSelectEvent?.(index)}
                      >
                        <span className="browser-debugger-timeline-index">{index + 1}</span>
                        <span className="browser-debugger-timeline-action">
                          <strong>{eventLabel(event, index)}</strong>
                          <small>{sourceLabel || `${event.action_type} ${formatDuration(event.duration_ms)}`}</small>
                          {sourceLabel && <small>{event.action_type} {formatDuration(event.duration_ms)}</small>}
                        </span>
                        <span>{timelineNetworkSummary(event)}</span>
                        <span>{timelineConsoleSummary(event)}</span>
                        <span>{timelineVitalsSummary(metrics)}</span>
                        <span>{timelineSnapshotSummary(event)}</span>
                        <span>{timelineDOMSummary(event)}</span>
                      </button>
                    )
                  })}
                </div>
              </div>
            )}
            {selectedEvent ? (
              <div className="browser-debugger-detail-grid">
                <div className="browser-debugger-detail-item">
                  <span>Action</span>
                  <strong>{selectedEvent.action_type} {selectedEvent.target || selectedEvent.selector || ''}</strong>
                </div>
                {sourceStepLabel(selectedEvent) && (
                  <div className="browser-debugger-detail-item">
                    <span>Source Syntax</span>
                    <strong>{sourceStepLabel(selectedEvent)}</strong>
                    {selectedEvent.source_profile && <small>{selectedEvent.source_profile}</small>}
                  </div>
                )}
                <div className="browser-debugger-detail-item">
                  <span>Duration</span>
                  <strong>{formatDuration(selectedEvent.duration_ms)}</strong>
                </div>
                <div className="browser-debugger-detail-item browser-debugger-detail-item-wide">
                  <div className="browser-debugger-detail-item-header">
                    <span>Page</span>
                    {selectedPageURL && (
                      <button
                        type="button"
                        className="btn btn-sm btn-ghost"
                        onClick={() => void handleCopy('url', selectedPageURL)}
                        disabled={!copySupported}
                      >
                        {copiedKey === 'url' ? 'URL copied' : 'Copy URL'}
                      </button>
                    )}
                  </div>
                  <strong>{selectedPageTitle || selectedPageURL || 'No page metadata captured'}</strong>
                  {selectedPageURL && <small>{selectedPageURL}</small>}
                </div>
                <div className="browser-debugger-detail-item">
                  <span>DOM Ready State</span>
                  <strong>{selectedDOMSummary?.ready_state || 'n/a'}</strong>
                </div>
                <div className="browser-debugger-detail-item">
                  <span>Visible Text</span>
                  <strong>{selectedDOMSummary?.visible_text?.length || 0} lines</strong>
                </div>
                <div className="browser-debugger-detail-item browser-debugger-detail-item-wide">
                  <span>Core Web Vitals</span>
                  <strong>{timelineVitalsSummary(selectedClientMetrics)}</strong>
                </div>
                {suggestedLocator && (
                  <div className="browser-debugger-detail-item browser-debugger-detail-item-wide suggested">
                    <span>Suggested Selector</span>
                    <strong>{suggestedLocator}</strong>
                    {selectedEvent.diagnosis?.evidence && <small>{selectedEvent.diagnosis.evidence}</small>}
                  </div>
                )}
                <div className="browser-debugger-detail-item">
                  <div className="browser-debugger-detail-item-header">
                    <span>Console Entries</span>
                    <div className="browser-debugger-detail-actions-group">
                      {selectedConsoleEntries.length > 0 && (
                        <>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            onClick={() => togglePreview(selectedArtifactKey('console'))}
                          >
                            {expandedPreviewKeys[selectedArtifactKey('console')] ? 'Hide preview' : 'Show preview'}
                          </button>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            onClick={() => void handleCopy('console', formatConsoleEntries(selectedConsoleEntries))}
                            disabled={!copySupported}
                          >
                            {copiedKey === 'console' ? 'Console copied' : 'Copy console'}
                          </button>
                        </>
                      )}
                    </div>
                  </div>
                  <strong>{selectedConsoleEntries.length}</strong>
                  {selectedConsoleEntries.length > 0 && expandedPreviewKeys[selectedArtifactKey('console')] && (
                    <pre className="browser-debugger-detail-preview">{formatConsoleEntries(selectedConsoleEntries)}</pre>
                  )}
                </div>
                <div className="browser-debugger-detail-item">
                  <div className="browser-debugger-detail-item-header">
                    <span>Network Entries</span>
                    <div className="browser-debugger-detail-actions-group">
                      {selectedNetworkEntries.length > 0 && (
                        <>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            onClick={() => togglePreview(selectedArtifactKey('network'))}
                          >
                            {expandedPreviewKeys[selectedArtifactKey('network')] ? 'Hide preview' : 'Show preview'}
                          </button>
                          <button
                            type="button"
                            className="btn btn-sm btn-ghost"
                            onClick={() => void handleCopy('network', formatNetworkEntries(selectedNetworkEntries))}
                            disabled={!copySupported}
                          >
                            {copiedKey === 'network' ? 'Network copied' : 'Copy network'}
                          </button>
                        </>
                      )}
                    </div>
                  </div>
                  <strong>{selectedNetworkEntries.length}</strong>
                  {selectedNetworkEntries.length > 0 && expandedPreviewKeys[selectedArtifactKey('network')] && (
                    <pre className="browser-debugger-detail-preview">{formatNetworkEntries(selectedNetworkEntries)}</pre>
                  )}
                </div>
                {selectedHTML && (
                  <div className="browser-debugger-detail-item browser-debugger-detail-item-wide">
                    <div className="browser-debugger-detail-item-header">
                      <span>HTML Capture</span>
                      <div className="browser-debugger-detail-actions-group">
                        <button
                          type="button"
                          className="btn btn-sm btn-ghost"
                          onClick={() => togglePreview(selectedArtifactKey('html'))}
                        >
                          {expandedPreviewKeys[selectedArtifactKey('html')] ? 'Hide preview' : 'Show preview'}
                        </button>
                        <button
                          type="button"
                          className="btn btn-sm btn-ghost"
                          onClick={() => void handleCopy('html', selectedHTML)}
                          disabled={!copySupported}
                        >
                          {copiedKey === 'html' ? 'HTML copied' : 'Copy HTML'}
                        </button>
                      </div>
                    </div>
                    <strong>Captured</strong>
                    {expandedPreviewKeys[selectedArtifactKey('html')] && (
                      <pre className="browser-debugger-detail-preview">{selectedHTML}</pre>
                    )}
                  </div>
                )}
                {selectedEvent.error_message && (
                  <div className="browser-debugger-detail-item browser-debugger-detail-item-wide error">
                    <div className="browser-debugger-detail-item-header">
                      <span>Error</span>
                      <button
                        type="button"
                        className="btn btn-sm btn-ghost"
                        onClick={() => void handleCopy('error', selectedEvent.error_message || '')}
                        disabled={!copySupported}
                      >
                        {copiedKey === 'error' ? 'Error copied' : 'Copy error'}
                      </button>
                    </div>
                    <strong>{selectedEvent.error_message}</strong>
                  </div>
                )}
              </div>
            ) : (
              <div className="browser-debugger-empty">Event details will appear once a browser replay step is available.</div>
            )}
          </div>

          <div className="browser-debugger-events">
            {events.length === 0 ? (
              <div className="browser-debugger-empty">No browser events streamed yet.</div>
            ) : events.map((event, index) => (
              <button
                key={`${event.phase}-${event.action_name}-${index}`}
                type="button"
                className={`browser-debugger-event ${(selectedEvent === event) ? 'active' : ''} ${event.success ? 'success' : 'error'}`}
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

      {isScreenshotDialogOpen && screenshot && (
        <BrowserReplayScreenshotDialog
          onClose={() => setIsScreenshotDialogOpen(false)}
          src={`data:image/png;base64,${screenshot}`}
        />
      )}
    </div>
  )
}

function BrowserReplayScreenshotDialog({ onClose, src }: BrowserReplayScreenshotDialogProps) {
  return (
    <div className="editor-dialog-backdrop" onClick={onClose}>
      <div
        className="editor-dialog replay-screenshot-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Browser Replay Screenshot"
        onClick={event => event.stopPropagation()}
      >
        <div className="editor-dialog-header">
          <div>
            <h3 className="panel-title">Browser Replay Screenshot</h3>
            <p className="editor-dialog-subtitle">Inspect the current browser debugger viewport at full size.</p>
          </div>
          <button className="btn btn-sm btn-ghost" onClick={onClose}>Close</button>
        </div>
        <div className="editor-dialog-body replay-screenshot-dialog-body">
          <img className="replay-screenshot-dialog-image" src={src} alt="Browser replay viewport" />
        </div>
      </div>
    </div>
  )
}
