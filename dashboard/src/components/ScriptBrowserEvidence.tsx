import { useState } from 'react'
import type { BrowserReplayEvent } from '../types/replay'
import SnapshotBodyPreview from './SnapshotBodyPreview'
import SnapshotTextPreview from './SnapshotTextPreview'
import RepairSuggestionList from './RepairSuggestionList'
import { BrowserReplayScreenshotDialog } from './BrowserReplayDebugger'
import { formatConsoleEntries, formatNetworkEntries } from './scriptEditorUtils'

const tabs = ['Screenshot', 'Assertions', 'DOM', 'Network', 'Browser console', 'Variables'] as const

/** Evidence for exactly one selected action. Navigation belongs to the action rail. */
export default function ScriptBrowserEvidence({ event, running, variables, onEdit, onApplyLocator }: {
  event?: BrowserReplayEvent | null
  running: boolean
  variables?: Record<string, string> | null
  onEdit?: () => void
  onApplyLocator?: (event: BrowserReplayEvent, locator: string) => void
}) {
  const [tab, setTab] = useState<typeof tabs[number]>('Screenshot')
  const [fullSize, setFullSize] = useState(false)
  if (!event) return <section className="script-browser-evidence"><h3>Browser evidence</h3><p>{running ? 'Waiting for the first action. Select a completed action to inspect its evidence.' : 'Debug one iteration to capture screenshots, assertions, DOM, network, and variables. Configure the target and capture options in Runtime Settings.'}</p></section>
  const snapshot = event.snapshot
  const screenshot = event.screenshot_base64 || snapshot?.screenshot_base64
  const failure = event.protocol_evidence?.assertion_failure as { expected?: unknown; actual?: unknown; type?: string } | undefined
  const showValue = (value: unknown) => typeof value === 'string' ? value : JSON.stringify(value) ?? 'Not captured'
  return <section className="script-browser-evidence" aria-label="Selected browser action evidence">
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
  </section>
}
