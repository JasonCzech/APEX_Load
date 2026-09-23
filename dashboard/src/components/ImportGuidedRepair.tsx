import { useEffect, useMemo, useRef, useState } from 'react'
import { useImportSettings } from './ImportSettingsContext'
import { request } from '../hooks/apiClient'
import type { EnterpriseArchiveFidelityReport, SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
import { draftImportFindings, groupImportFindings, importActionEntries, issueCategories, issueRepairOptions, type ImportFinding, type ImportIssue, type IssueCategory } from './importReviewIssues'

export type RepairResult = { secret_binding_requirements: SecretBindingRequirement[]; fidelity_report: EnterpriseArchiveFidelityReport; candidate_id: string; revision: string; script: ScriptDefinition; before: ScriptDefinition; findings: ImportFinding[]; readiness: { replay_ready: boolean; promotion_ready: boolean; blocking_codes?: string[]; validation_issues?: string[]; unresolved_bindings?: number }; allocation?: { name: string; rows: number; block_size: number; population: number }[]; expires_at: string }
type Operation = { type: string; [key: string]: unknown }
type Props = {
  script: ScriptDefinition; projectId: string; disabled?: boolean; onApplied: (result: RepairResult) => void; onViewSource?: (path: string) => void
  onSection?: (section: 'credentials' | 'data' | 'preserved' | 'source') => void
  focusPath?: string; focusRequest?: number; onBusyChange?: (busy: boolean) => void
  view?: 'overview' | 'data' | 'hidden'; additionalFindings?: ImportFinding[]; onReview?: (result: RepairResult) => void
}
const labels: Record<string, string> = { restore_body: 'Restored body', add_parameter: 'Parameter values, one per line', add_extractor: 'Extractor expression', bind_variable: 'Existing producer or parameter name', replace_proxy_secret: 'Project secret name' }

export function ImportGuidedRepair({ script, projectId, disabled, onApplied, onViewSource, onSection, view = 'overview', additionalFindings = [], onReview, focusPath, focusRequest, onBusyChange }: Props) {
  const settings = useImportSettings()
  const candidate = String(script.metadata?.import?.preserved?.server_import_attestation_v1 || '')
  const scope = `${candidate}:${projectId}`
  const currentScope = useRef(scope)
  currentScope.current = scope
  const inspectionRef = useRef<{ scope: string; promise: Promise<RepairResult> } | null>(null)
  const onReviewRef = useRef(onReview); onReviewRef.current = onReview
  useEffect(() => { currentScope.current = scope; return () => { currentScope.current = '' } }, [scope])
  const [review, setReview] = useState<RepairResult | null>(null)
  const [allocationPreview, setAllocationPreview] = useState<RepairResult | null>(null)
  const [allocationUser, setAllocationUser] = useState(1)
  const [preview, setPreview] = useState<RepairResult | null>(null)
  const [selection, setSelection] = useState('')
  const [occurrence, setOccurrence] = useState('')
  const [kind, setKind] = useState('')
  const [value, setValue] = useState('')
  const [responsePath, setResponsePath] = useState('')
  const [extractorType, setExtractorType] = useState('jsonpath')
  const [users, setUsers] = useState(2)
  const [runMode, setRunMode] = useState('duration')
  const [iterations, setIterations] = useState(1)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(false)
  const notifyBusy = useRef(onBusyChange); notifyBusy.current = onBusyChange
  useEffect(() => { notifyBusy.current?.(busy || loading) }, [busy, loading])
  useEffect(() => () => notifyBusy.current?.(false), [])
  const [pending, setPending] = useState<{ operation: Operation; finding: ImportFinding } | null>(null)
  const [filter, setFilter] = useState('all')
  const [search, setSearch] = useState('')
  const [resolved, setResolved] = useState<ImportIssue[]>([])
  const [announcement, setAnnouncement] = useState('')
  const [now, setNow] = useState(Date.now())
  const detailRef = useRef<HTMLHeadingElement>(null)
  const entries = useMemo(() => importActionEntries(script), [script])
  const issues = useMemo(() => groupImportFindings([...(review?.findings || draftImportFindings(script)), ...additionalFindings], script), [review, script, additionalFindings])
  const selected = issues.find(i => i.id === selection) || resolved.find(i => i.id === selection)
  const finding = selected?.findings.find(f => f.action_path === occurrence) || selected?.findings[0]
  const options = finding ? issueRepairOptions(finding.code) : []
  const selectedResolved = !!selected && !issues.some(i => i.id === selected.id)
  const blocked = issues.filter(i => i.blocking).length
  const reviewCount = issues.length - blocked
  const shown = (filter === 'resolved' ? resolved : issues).filter(i => (filter !== 'blockers' || i.blocking) && (filter !== 'review' || !i.blocking) && `${i.title} ${i.findings.map(f => `${f.source_ref} ${f.action_path}`).join(' ')}`.toLowerCase().includes(search.toLowerCase()))
  const expiry = review?.expires_at ? new Date(review.expires_at).getTime() : NaN
  const expired = Number.isFinite(expiry) && expiry <= now
  const locked = !!disabled || busy || loading || expired
  const canRepair = candidate.startsWith('protected:') && !!review && !selectedResolved && !finding?.code.startsWith('diagnostic:')
  const preceding = entries.slice(0, entries.findIndex(e => e.path === finding?.action_path)).filter(e => !!e.action.target && e.action.type !== 'http_sequence')
  const requestPayload = (operation: Operation, target = finding) => ({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: target?.code || '', action_path: target?.action_path || '', operation })
  const resetPreview = () => { setPreview(null); setPending(null) }
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(timer)
  }, [])
  useEffect(() => {
    setAllocationPreview(null); setReview(null); setPreview(null); setValue(''); setError(''); setPending(null); setBusy(false)
    setLoading(false)
    if (!candidate.startsWith('protected:')) return
    let active = true
    setLoading(true)
    // StrictMode replays effects; duplicate inspections compete for the protected candidate lease.
    const inspectionScope = `${candidate}:${projectId}`
    if (inspectionRef.current?.scope !== inspectionScope) inspectionRef.current = { scope: inspectionScope, promise: request<RepairResult>('/scripts/import/repairs/preview', { method: 'POST', body: JSON.stringify({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: '', action_path: '', operation: { type: 'inspect' } }) }) }
    inspectionRef.current.promise
      .then(result => { if (active) { setReview(result); onReviewRef.current?.(result) } }).catch(e => { if (active) setError(String(e.message || e)) }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [candidate, projectId])
  const lastFocus = useRef<number>()
  useEffect(() => {
    if (!focusPath || lastFocus.current === focusRequest) return
    const issue = issues.find(i => i.findings.some(f => f.action_path === focusPath))
    if (!issue) return
    lastFocus.current = focusRequest
    setSelection(issue.id); setOccurrence(focusPath)
    setKind(issueRepairOptions(issue.findings.find(f => f.action_path === focusPath)?.code || '')[0]?.value || '')
    setValue(''); setResponsePath(''); setPreview(null); setPending(null)
  }, [focusPath, focusRequest, issues])
  function choose(issue: ImportIssue) {
    setSelection(issue.id); setOccurrence(issue.findings[0]?.action_path || ''); setKind(issueRepairOptions(issue.findings[0]?.code || '')[0]?.value || '')
    setValue(''); setResponsePath(''); setError(''); resetPreview()
    requestAnimationFrame(() => detailRef.current?.focus())
  }
  async function run(operation: Operation, apply = false) {
    if (locked) return
    const requestScope = currentScope.current
    const target = apply ? pending?.finding : finding
    setBusy(true); setError('')
    try {
      const result = await request<RepairResult>(`/scripts/import/repairs/${apply ? 'apply' : 'preview'}`, { method: 'POST', body: JSON.stringify(requestPayload(operation, target)) })
      if (requestScope !== currentScope.current) return
      if (apply) {
        const remaining = groupImportFindings(result.findings, result.script)
        setResolved(previous => [...previous.filter(i => !remaining.some(next => next.id === i.id)), ...issues.filter(i => !i.findings[0]?.code.startsWith('diagnostic:') && !remaining.some(next => next.id === i.id) && !previous.some(p => p.id === i.id))])
        setValue(''); resetPreview(); setReview(result); setAnnouncement(`Repair applied. ${remaining.filter(i => i.blocking).length} grouped issues block replay.`); onApplied(result)
      } else if (operation.type === 'preview_allocation') { setAllocationPreview(result) } else { setPreview(result); setPending(target ? { operation, finding: target } : null) }
    } catch (e) { if (requestScope === currentScope.current) setError(e instanceof Error ? e.message : 'Repair failed') }
    finally { if (requestScope === currentScope.current) setBusy(false) }
  }
  function operation(): Operation {
    const variable = finding?.code.split(':').slice(1).join(':') || ''
    switch (kind) {
      case 'restore_body': return { type: kind, body: value }
      case 'replace_proxy_secret': return { type: kind, secret_name: value }
      case 'bind_variable': return { type: kind, producer: value }
      case 'add_parameter': return { type: kind, parameter: { name: variable, type: 'list', values: value.split('\n'), update: 'once', selection: 'sequential' } }
      case 'add_extractor': return { type: kind, response_path: responsePath, extractor: { name: variable, type: extractorType, source: 'body', expression: value, on_miss: 'fail' } }
      default: return { type: 'select_tls', tls_min: value || '1.2', tls_max: '1.3' }
    }
  }
  const previewCounts = preview ? groupImportFindings(preview.findings, preview.script) : []
  const previewContent = preview && <section className="lr-repair-preview" aria-label="Redacted repair preview">
    <h4>Review this change</h4>
    <p>{previewCounts.filter(i => i.blocking).length} grouped issues block replay after this change. {preview.readiness.replay_ready ? 'Static replay checks passed; target replay is not verified.' : 'Execution remains blocked.'}</p>
    {!!preview.readiness.unresolved_bindings && <p>{preview.readiness.unresolved_bindings} unresolved credential requirements remain.</p>}
    {preview.allocation && <div className="lr-issue-table"><table><thead><tr><th>Parameter</th><th>Usable rows</th><th>Block per VUser</th><th>VUsers</th></tr></thead><tbody>{preview.allocation.map(p => <tr key={p.name}><td>{p.name}</td><td>{p.rows}</td><td>{p.block_size}</td><td>{p.population}</td></tr>)}</tbody></table></div>}
    <details><summary>Redacted before / after</summary><pre>{JSON.stringify({ before: preview.before, after: preview.script }, null, 2)}</pre></details>
    {pending && pending.operation.type !== 'preview_allocation' && <button className="btn btn-primary" type="button" disabled={locked} onClick={() => run(pending.operation, true)}>Apply reviewed repair</button>}
  </section>
  return <section className="lr-issue-workspace" aria-label={view === 'data' ? 'Unique parameter allocation' : 'Guided import repair'} hidden={view === 'hidden' || (view === 'data' && !!settings && settings.dataView !== 'allocation')}>
    <p className="lr-repair-announcement" role="status">{announcement || (loading ? 'Checking current repair requirements…' : '')}</p>
    {view !== 'data' ? <>
      <div className="lr-issue-workspace-heading"><div><h3>Resolve import issues</h3><p>{blocked} grouped issues block replay · {reviewCount} need review · {resolved.length} resolved in this session</p></div>
        <button type="button" className="btn btn-primary" disabled={locked || !issues.length} onClick={() => { setFilter('all'); setSearch(''); choose(issues.find(i => i.id !== selection) || issues[0]) }}>Resolve next issue</button>
      </div>
      <p className="lr-subtitle">Repeated findings are grouped by value and execution scope. Request locations remain available inside each issue.</p>
      {!candidate.startsWith('protected:') && <p className="lr-warnings">Guided repair requires a protected import candidate. Re-import with encrypted import storage configured. You can still inspect the findings and source.</p>}
      {expired && <p className="lr-warnings">This protected draft has expired. Re-import the archive to make changes; the findings remain available for inspection.</p>}
      <div className="lr-issue-toolbar"><label>Find an issue<input type="search" value={search} placeholder="Search values or source locations" onChange={e => setSearch(e.target.value)} /></label>
        <label>Show<select value={filter} onChange={e => setFilter(e.target.value)}><option value="all">All unresolved ({issues.length})</option><option value="blockers">Needs repair ({blocked})</option><option value="review">Needs review ({reviewCount})</option><option value="resolved">Resolved ({resolved.length})</option></select></label>
      </div>
      <div className="lr-issue-layout"><div className="lr-issue-queue" aria-label="Import issue queue">
        {(Object.keys(issueCategories) as IssueCategory[]).map(category => {
          const rows = shown.filter(i => i.category === category)
          if (!rows.length) return null
          return <details key={category} open><summary>{issueCategories[category]} <span>{rows.length} issues</span></summary><ul>{rows.map(issue => <li key={issue.id}><button type="button" className="lr-issue-row" aria-pressed={selection === issue.id} onClick={() => choose(issue)} disabled={busy}>
            <span className="lr-issue-row-title">{issue.title}</span><span className="lr-issue-row-meta">{filter === 'resolved' ? 'Resolved' : issue.blocking ? 'Blocks replay' : 'Needs review'} · {issue.findings.length} affected {issue.findings.length === 1 ? 'location' : 'locations'}</span>
          </button></li>)}</ul></details>
        })}
        {!shown.length && <p>{search ? 'No issues match this search.' : filter === 'resolved' ? 'Applied repairs will appear here when their issues are resolved.' : 'No issues in this view.'}</p>}
      </div><section className="lr-issue-detail" aria-label="Selected issue">
        {!selected || !finding ? <><h4>Select an issue to get started</h4><p>See its impact, inspect the source, and preview the repair before applying it.</p></> : <>
          <h4 tabIndex={-1} ref={detailRef}>{selected.title}</h4>
          <p><strong>{selectedResolved ? 'Resolved' : selected.blocking ? 'Blocks replay' : 'Review decision required'}</strong> · {selected.findings.length} affected locations</p>
          <p>{selected.category === 'values' ? finding.code.startsWith('conditional_') ? 'A possible producer exists, but its order or execution path needs review. Check that it runs before the selected consumer.' : 'This value has no reachable producer in the imported draft. Bind a value, add a parameter, or extract it from a preceding response.' : selected.category === 'bodies' ? 'The original payload could not be restored during import. Supply the restored body; previews will keep its contents protected.' : selected.category === 'runtime' ? 'The source behavior needs a supported replacement. Review the migration before applying it.' : finding.detail || 'Inspect the source evidence and resolve this requirement in the relevant review section.'}</p>
          {finding.detail && !finding.detail.includes('imported behavior has an unresolved') && <details><summary>Diagnostic evidence</summary><p>{finding.detail}</p></details>}
          <details className="lr-issue-occurrences"><summary>Affected requests and source evidence ({selected.findings.length})</summary><ul>{selected.findings.map(f => <li key={`${f.action_path}:${f.code}`}><strong>{entries.find(e => e.path === f.action_path)?.action.name || f.action_path || 'Script requirement'}</strong><span>{f.source_ref || 'Source location unavailable'}</span>{f.action_path && onViewSource && <button className="btn btn-secondary btn-sm" type="button" onClick={() => onViewSource(f.action_path!)}>View source</button>}<code>{f.action_path}</code></li>)}</ul></details>
          {selectedResolved ? <p>This issue was removed by an applied repair. Remaining requirements are evaluated independently.</p> : canRepair && options.length ? <div className="lr-repair-form">
            <label>Repair operation<select aria-label="Repair operation" value={kind} disabled={locked} onChange={e => { setKind(e.target.value); setValue(''); resetPreview() }}>{options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}</select></label>
            {selected.findings.length > 1 && <label>Consumer to repair<select aria-label="Repair finding" value={finding.action_path || ''} disabled={locked} onChange={e => { setOccurrence(e.target.value); setResponsePath(''); resetPreview() }}>{selected.findings.map(f => <option key={f.action_path} value={f.action_path}>{f.source_ref || f.action_path} — {entries.find(e => e.path === f.action_path)?.action.name || f.action_path}</option>)}</select></label>}
            <p className="lr-subtitle">{kind === 'add_parameter' ? 'Adds a script-wide parameter. Preview checks which occurrences it satisfies.' : 'Repairs the selected consumer or setting. Other occurrences stay unresolved until the updated checks prove they are satisfied.'}</p>
            {kind === 'add_extractor' && <><label>Preceding response<select aria-label="Preceding response path" value={responsePath} disabled={locked} onChange={e => { setResponsePath(e.target.value); resetPreview() }}><option value="">Choose a preceding response</option>{preceding.map(e => <option key={e.path} value={e.path}>{e.action.name || e.path} · {e.path}</option>)}</select></label><label>Extractor type<select value={extractorType} disabled={locked} onChange={e => { setExtractorType(e.target.value); resetPreview() }}><option value="jsonpath">JSONPath</option><option value="regex">Regular expression</option></select></label></>}
            {kind === 'select_tls' ? <label>Minimum TLS<select value={value || '1.2'} disabled={locked} onChange={e => { setValue(e.target.value); resetPreview() }}><option value="1.2">TLS 1.2 (up to 1.3)</option><option value="1.3">TLS 1.3 only</option></select><span>This records an intentional migration change, not exact SSL 2/3 parity.</span></label> : <label>{labels[kind]}<textarea aria-label="Repair value" autoComplete="off" value={value} onChange={e => { setValue(e.target.value); resetPreview() }} disabled={locked} rows={kind === 'restore_body' ? 6 : 3} /></label>}
            <button className="btn btn-primary" type="button" disabled={locked || (kind !== 'restore_body' && kind !== 'select_tls' && !value.trim()) || (kind === 'add_extractor' && !responsePath)} onClick={() => run(operation())}>{busy ? 'Checking repair…' : 'Preview repair'}</button>
            {previewContent}
          </div> : <div className="lr-repair-actions"><p>{options.length ? 'Inspect the source or re-import to obtain a current protected repair candidate.' : 'This finding needs a source or configuration change. Acknowledging it alone cannot remove an execution blocker.'}</p>{onSection && <button className="btn btn-secondary" type="button" onClick={() => onSection(selected.category === 'credentials' ? 'credentials' : selected.category === 'values' ? 'data' : selected.category === 'runtime' ? 'preserved' : 'source')}>Open related review</button>}{finding.action_path && onViewSource && <button className="btn btn-secondary" type="button" onClick={() => onViewSource(finding.action_path!)}>Open source location</button>}</div>}
        </>}
      </section></div>
    </> : <>
      <h3>Unique parameter allocation</h3><p>Preview reserved rows for your planned VUsers and run mode. This does not change or launch a run.</p>
      <fieldset className="lr-repair-form lr-allocation-form" disabled={locked || !candidate.startsWith('protected:')}><legend>Planned run</legend><label>Planned VUsers<input type="number" min={1} value={users} onChange={e => { setUsers(Number(e.target.value)); setAllocationPreview(null); setAllocationUser(1) }} /></label><label>Run mode<select value={runMode} onChange={e => { setRunMode(e.target.value); setAllocationPreview(null) }}><option value="duration">Duration</option><option value="iterations">Iterations</option></select></label>{runMode === 'iterations' && <label>Iterations per VUser<input type="number" min={1} value={iterations} onChange={e => { setIterations(Number(e.target.value)); setAllocationPreview(null) }} /></label>}<button className="btn btn-secondary" type="button" disabled={!Number.isInteger(users) || users < 1 || (runMode === 'iterations' && (!Number.isInteger(iterations) || iterations < 1))} onClick={() => run({ type: 'preview_allocation', vusers: users, iterations: runMode === 'duration' ? 0 : iterations })}>{busy ? 'Calculating allocation…' : 'Preview allocation'}</button></fieldset>
      {allocationPreview && <section aria-label="Allocation result" className="lr-repair-preview"><h4>Capacity preview: {users} VUsers · {runMode === 'duration' ? 'duration run' : `${iterations} iterations per VUser`}</h4>
        <p role="status">{allocationPreview.allocation?.length ? 'Capacity available for the planned population. This does not clear other replay blockers.' : 'No uniquely allocated parameters in this draft.'}</p>
        {!!allocationPreview.allocation?.length && <><label>Inspect VUser ordinal<input type="number" min={1} max={users} value={allocationUser} onChange={e => setAllocationUser(Math.max(1, Math.min(users, Number(e.target.value) || 1)))} /></label><div className="lr-table-wrap"><table className="lr-parameter-table"><thead><tr><th>Parameter</th><th>Usable rows</th><th>Rows per VUser</th><th>Unused rows</th><th>VUser {allocationUser} row range</th></tr></thead><tbody>{allocationPreview.allocation.map(p => { const start = Math.max(1, script.parameters?.find(x => x.name === p.name)?.start_row || 1) + (allocationUser - 1) * p.block_size; return <tr key={p.name}><th scope="row">{p.name}</th><td>{p.rows}</td><td>{p.block_size}</td><td>{p.rows - p.block_size * p.population}</td><td>{start}–{start + p.block_size - 1}</td></tr> })}</tbody></table></div><p>Row numbers refer to data rows, excluding a header. Allocation is reserved across the whole planned population, including ramps.</p></>}
      </section>}
    </>}
    {error && <p className="lr-warnings" role="alert">{error} Re-import if the protected candidate has expired. If an apply request was interrupted, retry the same reviewed repair.</p>}
  </section>
}
