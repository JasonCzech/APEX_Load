import { useRef, useState } from 'react'
import type { BrowserReplayEvent } from '../types/replay'
import BrowserReplayProgress from './BrowserReplayProgress'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import RepairSuggestionList from './RepairSuggestionList'
import { BrowserReplayScreenshotDialog } from './BrowserReplayDebugger'
import { formatConsoleEntries, formatNetworkEntries } from './scriptEditorUtils'

const tabs = ['Screenshot', 'Assertions', 'DOM', 'Network', 'Browser console', 'Variables'] as const

/** Evidence for exactly one selected action. Navigation belongs to the action rail. */
export default function ScriptBrowserEvidence({ event, running, variables, projectId, nativeReplayDisabled, nativeReplayTitle, onStartNativeReplay, onEdit, onApplyLocator }: {
  event?: BrowserReplayEvent | null
  running: boolean
  variables?: Record<string, string> | null
  projectId?: string
  nativeReplayDisabled?: boolean
  nativeReplayTitle?: string
  onStartNativeReplay?: () => Promise<string | null>
  onEdit?: () => void
  onApplyLocator?: (event: BrowserReplayEvent, locator: string) => void
}) {
  const [tab, setTab] = useState<typeof tabs[number]>('Screenshot')
  const [fullSize, setFullSize] = useState(false)
  const [nativeReplaySessionId, setNativeReplaySessionId] = useState<string | null>(null)
  const [nativeReplayStarting, setNativeReplayStarting] = useState(false)
  const [nativeReplayRunning, setNativeReplayRunning] = useState(false)
  const [nativeReplayError, setNativeReplayError] = useState('')
  const nativeReplayInFlightRef = useRef(false)
  const nativeReplaySection = onStartNativeReplay ? (
    <section className="script-browser-native-replay" aria-label="Native browser replay">
      <div className="selected-evidence-header">
        <div>
          <h4>Native browser replay</h4>
          <p>Run one browser iteration and retain a native HAR and replay evidence archive.</p>
        </div>
        <button
          type="button"
          className="btn btn-secondary btn-sm"
          disabled={running || nativeReplayStarting || nativeReplayRunning || Boolean(nativeReplayDisabled)}
          title={nativeReplayTitle}
          onClick={() => {
            if (nativeReplayInFlightRef.current || nativeReplayRunning) return
            nativeReplayInFlightRef.current = true
            setNativeReplayStarting(true)
            setNativeReplayError('')
            void onStartNativeReplay()
              .then(sessionId => {
                if (sessionId) {
                  setNativeReplaySessionId(sessionId)
                  setNativeReplayRunning(true)
                }
              })
              .catch((reason: unknown) => {
                setNativeReplayError(reason instanceof Error ? reason.message : 'Could not start native browser replay.')
              })
              .finally(() => {
                nativeReplayInFlightRef.current = false
                setNativeReplayStarting(false)
              })
          }}
        >
          {nativeReplayStarting ? 'Starting replay…' : 'Run native browser replay'}
        </button>
      </div>
      {nativeReplayError && <p role="alert">{nativeReplayError}</p>}
      {nativeReplaySessionId && (
        <BrowserReplayProgress
          key={nativeReplaySessionId}
          replaySessionId={nativeReplaySessionId}
          projectId={projectId}
          onComplete={() => setNativeReplayRunning(false)}
          onDismiss={() => {
            setNativeReplayRunning(false)
            setNativeReplaySessionId(null)
          }}
        />
      )}
    </section>
  ) : null
  const snapshot = event?.snapshot
  const screenshot = event?.screenshot_base64 || snapshot?.screenshot_base64
  const failure = event?.protocol_evidence?.assertion_failure as { expected?: unknown; actual?: unknown; type?: string } | undefined
  const showValue = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value) ?? 'Not captured'
  return <section className="script-browser-evidence" aria-label={event ? 'Selected browser action evidence' : undefined}>
    {!event ? (
      <>
        <h3>Browser evidence</h3>
        <p>{running ? 'Waiting for the first action. Select a completed action to inspect its evidence.' : 'Debug one iteration to capture screenshots, assertions, DOM, network, and variables. Configure the target and capture options in Runtime Settings.'}</p>
      </>
    ) : (
      <>
        <div className="selected-evidence-header">
          <div><small>{event.phase} {(event.action_index ?? 0) + 1} · {(event.duration_ms || 0).toFixed(1)} ms · {event.success ? 'Passed' : 'Failed'}</small><h3>{event.action_name || event.action_type}</h3><p>{event.page_title || snapshot?.page_title} · {event.page_url || snapshot?.page_url}</p></div>
          {onEdit && <button type="button" className="btn btn-secondary btn-sm" onClick={onEdit}>{failure ? 'Edit this assertion' : 'Edit this action'}</button>}
        </div>
        {!event.success && <div className="selected-evidence-failure" role="status">
          <strong>{event.diagnosis?.category === 'assertion' ? 'Assertion failed' : 'Action failed'}</strong>
          {failure ? <dl className="browser-assertion-result" aria-label="Assertion result"><dt>Expected</dt><dd>{showValue(failure.expected)}</dd><dt>Actual</dt><dd>{showValue(failure.actual)}</dd></dl> : <p>{event.diagnosis?.cause || event.error_message}</p>}
          {event.diagnosis?.suggested_locator && onApplyLocator && <button type="button" className="btn btn-sm btn-secondary" onClick={() => onApplyLocator(event, event.diagnosis!.suggested_locator!)}>Use suggested selector</button>}
        </div>}
        <RepairSuggestionList className="selected-evidence-repairs" descriptionClassName="selected-evidence-repair-description" suggestions={event.repair_suggestions || []} renderExtraAction={suggestion => {
          const locator = suggestion.fields?.selector || suggestion.fields?.locator
          return suggestion.draft_patch_type === 'promote_selector' && locator && onApplyLocator ? <button type="button" className="btn btn-secondary btn-sm" onClick={() => onApplyLocator(event, locator)}>Apply suggested selector</button> : null
        }} />

        <div className="selected-evidence-tabs" role="group" aria-label="Browser evidence views">
          {tabs.map(name => <button key={name} type="button" aria-pressed={tab === name} className={tab === name ? 'active' : ''} onClick={() => setTab(name)}>{name}</button>)}
        </div>
        <div className="selected-evidence-body" aria-label={tab}>
          {tab === 'Screenshot' && (screenshot ? <><button type="button" className="btn btn-sm btn-secondary" onClick={() => setFullSize(true)}>View full size</button><img className="selected-evidence-screenshot" src={`data:image/png;base64,${screenshot}`} alt={`Screenshot after ${event.action_name || event.action_type}`} /></> : <p>No screenshot was captured for this action. Enable per-action capture in Runtime Settings and debug again.</p>)}
          {tab === 'Assertions' && <SnapshotBodyPreview body={JSON.stringify(event.protocol_evidence || { result: event.success ? 'Action passed; no detailed assertion result captured.' : event.error_message }, null, 2)} headers={{ 'Content-Type': 'application/json' }} />}
          {tab === 'DOM' && <><SnapshotBodyPreview body={JSON.stringify(event.dom_summary || snapshot?.dom_summary || {}, null, 2)} headers={{ 'Content-Type': 'application/json' }} /><SnapshotTextPreview text={event.html || snapshot?.html || 'No HTML captured for this action.'} /></>}
          {tab === 'Network' && <SnapshotTextPreview text={formatNetworkEntries(event.network_entries || snapshot?.network_entries || []) || 'No network entries captured for this action.'} />}
          {tab === 'Browser console' && <SnapshotTextPreview text={formatConsoleEntries(event.console_entries || snapshot?.console_entries || []) || 'No browser console entries captured for this action.'} />}
          {tab === 'Variables' && <SnapshotBodyPreview body={JSON.stringify(variables || event.extractions || {}, null, 2)} headers={{ 'Content-Type': 'application/json' }} />}
        </div>
        {fullSize && screenshot && <BrowserReplayScreenshotDialog src={`data:image/png;base64,${screenshot}`} onClose={() => setFullSize(false)} />}
      </>
    )}
    {nativeReplaySection}
  </section>
}
