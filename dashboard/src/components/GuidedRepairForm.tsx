import SourceCodeViewer from './SourceCodeViewer'
import { useMemo, useState, type ReactNode } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import { buildRepairOperation, changedRepairPaths, type ActionEntry, type ImportFinding, type RepairFields, type RepairOperation } from './importReviewIssues'

// What a repair preview reports, from the import candidate route and the
// saved-script route alike.
export type RepairPreviewModel = {
  readiness: { replay_ready: boolean; unresolved_bindings?: number }
  before: ScriptDefinition
  script: ScriptDefinition
  allocation?: { name: string; rows: number; block_size: number; population: number }[]
}

const labels: Record<string, string> = { restore_body: 'Restored body', add_parameter: 'Parameter values, one per line', add_extractor: 'Extractor expression', bind_variable: 'Existing producer or parameter name', replace_proxy_secret: 'Project secret name' }

/**
 * A repair preview: the outcome, the changed field paths and the redacted
 * before/after. Apply is offered once a previewed operation is pending.
 */
export function RepairPreview({ preview, blockers, apply }: {
  preview: RepairPreviewModel
  // Blocking issues after the change, as the caller lists them.
  blockers: number
  apply?: { disabled: boolean; title?: string; onApply: () => void; note?: ReactNode }
}) {
  const [showComparison, setShowComparison] = useState(false)
  // A full before/after walk, so once per preview, not on every render.
  const changedPaths = useMemo(() => changedRepairPaths(preview.before, preview.script), [preview.before, preview.script])
  // The server's readiness never sees a configuration reference or a
  // preflight-only refusal, which the caller's grouping counts as blockers:
  // replay checks pass only when both agree.
  const replayReady = preview.readiness.replay_ready && blockers === 0
  return <section className="lr-repair-preview" aria-label="Redacted repair preview">
    <header className="lr-repair-preview-heading"><div><span className="script-create-kicker">Repair preview</span><h4>Review this change</h4><p>Check the outcome and changed fields before applying this repair.</p></div><span className="repository-status-pill imported-runtime-file-tag">Preview only</span></header>
    <div className="lr-repair-preview-outcome" role="group" aria-label="Repair outcome">
      <div><span>Replay checks</span><strong className={replayReady ? 'healthy' : 'danger'}>{replayReady ? 'Passed' : 'Blocked'}</strong><small>{replayReady ? 'Target replay is not verified' : 'Execution remains blocked'}</small></div>
      <div><span>Blocking issues</span><strong>{blockers}</strong><small>grouped issues after this change</small></div>
      <div><span>Credential requirements</span><strong>{preview.readiness.unresolved_bindings || 0}</strong><small>unresolved after this change</small></div>
    </div>
    <div className="lr-repair-preview-fields"><div className="lr-repair-preview-section-heading"><h5>Fields changed</h5><span>{changedPaths.length} shown</span></div>{changedPaths.length ? <ul>{changedPaths.map(path => <li key={path}><code>{path}</code></li>)}</ul> : <p>No field paths changed in this preview.</p>}<p>Only field paths are summarized here. Protected values stay hidden; up to 100 fields are listed.</p></div>
    {preview.allocation && <div className="lr-issue-table"><table><thead><tr><th>Parameter</th><th>Usable rows</th><th>Block per VUser</th><th>VUsers</th></tr></thead><tbody>{preview.allocation.map(p => <tr key={p.name}><td>{p.name}</td><td>{p.rows}</td><td>{p.block_size}</td><td>{p.population}</td></tr>)}</tbody></table></div>}
    <details className="lr-repair-preview-comparison" onToggle={event => setShowComparison(event.currentTarget.open)}><summary>Redacted before / after</summary>{showComparison && <div className="lr-repair-preview-code-grid"><div><h6>Before repair</h6><SourceCodeViewer ariaLabel="Before repair JSON" sourcePath="before.json" value={JSON.stringify(preview.before, null, 2)} /></div><div><h6>After repair</h6><SourceCodeViewer ariaLabel="After repair JSON" sourcePath="after.json" value={JSON.stringify(preview.script, null, 2)} /></div></div>}</details>
    {apply && <div className="lr-repair-preview-footer">{apply.note ?? <p>Preview only. Nothing changes until you apply.</p>}<button className="btn btn-primary" type="button" disabled={apply.disabled} title={apply.title} onClick={apply.onApply}>Apply reviewed repair</button></div>}
  </section>
}

/**
 * The guided repair form for one finding: the operation, the consumer when
 * the issue has several, the operation's value, and Preview. The caller owns
 * the fields (so a draft survives navigation) and sends the operation; the
 * preview, when there is one, follows the form.
 */
export function GuidedRepairForm({ finding, occurrences, options, entries, fields, onChange, onOccurrence, locked, busy, onPreview, previewBlockedReason, subject = 'draft', children }: {
  finding: ImportFinding
  // The issue's findings; a consumer picker shows when there are several.
  occurrences: ImportFinding[]
  options: { value: string; label: string }[]
  // The script's steps, for an extractor's preceding response.
  entries: ActionEntry[]
  fields: RepairFields
  // A field changed: the caller keeps it and drops any preview.
  onChange: (change: Partial<RepairFields>) => void
  onOccurrence: (path: string) => void
  locked: boolean
  busy: boolean
  onPreview: (operation: RepairOperation) => void
  // Why Preview cannot run now while the fields stay editable, as its title.
  previewBlockedReason?: string
  // What a script-wide repair changes: the import draft or the saved script.
  subject?: 'draft' | 'script'
  children?: ReactNode
}) {
  const { kind, value, responsePath, extractorType } = fields
  const proxyExclusion = kind === 'omit_proxy_credentials' || kind === 'disable_imported_proxy'
  const preceding = entries.slice(0, entries.findIndex(e => e.path === finding.action_path)).filter(e => !!e.action.target && e.action.type !== 'http_sequence')
  return <div className="lr-repair-form"><h5>Fix this issue</h5>
    <label>Repair operation<select aria-label="Repair operation" value={kind} disabled={locked} onChange={e => onChange({ kind: e.target.value, value: '' })}>{options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}</select></label>
    {occurrences.length > 1 && <label>Consumer to repair<select aria-label="Repair finding" value={finding.action_path || ''} disabled={locked} onChange={e => onOccurrence(e.target.value)}>{occurrences.map(f => <option key={f.action_path} value={f.action_path}>{f.source_ref || f.action_path} — {entries.find(e => e.path === f.action_path)?.action.name || f.action_path}</option>)}</select></label>}
    {finding.code === 'encrypted_proxy_password' && <p>Proxy settings contain an encrypted password. Choose how to import these settings. The encrypted password cannot be used directly.</p>}
    {proxyExclusion && <p role="status">{kind === 'disable_imported_proxy' ? 'Disables proxying for this script and removes its imported proxy URL, PAC configuration, credentials, and bypass settings. Protected source evidence is retained as ignored during import.' : 'Keeps proxy routing and removes its username and password, including URL userinfo. The proxy must support unauthenticated access. Protected source evidence is retained.'}</p>}
    <p className="lr-subtitle">{proxyExclusion ? 'Applies to the script-wide proxy configuration. Preview shows the changed fields and remaining requirements.' : kind === 'add_parameter' ? 'Adds a script-wide parameter. Preview checks which occurrences it satisfies.' : kind === 'select_tls' ? `Sets the script's TLS range, which resolves every legacy TLS step in this ${subject} at once.` : 'Repairs the selected consumer or setting. Other occurrences stay unresolved until the updated checks prove they are satisfied.'}</p>
    {kind === 'add_extractor' && <><label>Preceding response<select aria-label="Preceding response path" value={responsePath} disabled={locked} onChange={e => onChange({ responsePath: e.target.value })}><option value="">Choose a preceding response</option>{preceding.map(e => <option key={e.path} value={e.path}>{e.action.name || e.path} · {e.path}</option>)}</select></label><label>Extractor type<select value={extractorType} disabled={locked} onChange={e => onChange({ extractorType: e.target.value })}><option value="jsonpath">JSONPath</option><option value="regex">Regular expression</option></select></label></>}
    {kind === 'select_tls' ? <label>Minimum TLS<select value={value || '1.2'} disabled={locked} onChange={e => onChange({ value: e.target.value })}><option value="1.2">TLS 1.2 (up to 1.3)</option><option value="1.3">TLS 1.3 only</option></select><span>This records an intentional migration change, not exact SSL 2/3 parity.</span></label> : !proxyExclusion && <label>{labels[kind]}<textarea aria-label="Repair value" autoComplete="off" value={value} onChange={e => onChange({ value: e.target.value })} disabled={locked} rows={kind === 'restore_body' ? 6 : 3} /></label>}
    <div className="lr-issue-action-row"><button className="btn btn-primary" type="button" disabled={locked || Boolean(previewBlockedReason) || (!proxyExclusion && kind !== 'restore_body' && kind !== 'select_tls' && !value.trim()) || (kind === 'add_extractor' && !responsePath)} title={previewBlockedReason} onClick={() => onPreview(buildRepairOperation(kind, finding, fields))}>{busy ? 'Checking repair…' : 'Preview repair'}</button><span>Preview first. Nothing changes until you apply.</span></div>
    {children}
  </div>
}
