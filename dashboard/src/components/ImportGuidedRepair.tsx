import ImportIssueRuntimeEditor from './ImportIssueRuntimeEditor'
import ImportIssueSeverityTag from './ImportIssueSeverityTag'
import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import SourceCodeViewer from './SourceCodeViewer'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useImportSettings } from './ImportSettingsContext'
import { request } from '../hooks/apiClient'
import type { EnterpriseArchiveFidelityReport, SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
import { draftImportFindings, groupImportFindings, importActionEntries, issueCategories, issueRepairOptions, issueScope, changedRepairPaths, type ImportFinding, type ImportIssue, type IssueCategory } from './importReviewIssues'

export type RepairResult = { secret_binding_requirements: SecretBindingRequirement[]; fidelity_report: EnterpriseArchiveFidelityReport; candidate_id: string; revision: string; script: ScriptDefinition; before: ScriptDefinition; findings: ImportFinding[]; readiness: { replay_ready: boolean; promotion_ready: boolean; blocking_codes?: string[]; validation_issues?: string[]; unresolved_bindings?: number }; allocation?: { name: string; rows: number; block_size: number; population: number }[]; expires_at: string }
type Operation = { type: string; [key: string]: unknown }
type Props = {
  script: ScriptDefinition; projectId: string; disabled?: boolean; disabledReason?: string; onApplied: (result: RepairResult) => void; onViewSource?: (path: string, finding?: ImportFinding) => void
  onCredentialTarget?: (target: HTMLDivElement | null) => void; onReimport?: () => void
  onSection?: (section: 'credentials' | 'data' | 'preserved' | 'source') => void
  categoryFocus?: { category: IssueCategory | 'all'; revision: number };
  focusPath?: string; focusRequest?: number; onBusyChange?: (busy: boolean) => void
  view?: 'overview' | 'data' | 'hidden'; additionalFindings?: ImportFinding[]; onReview?: (result: RepairResult) => void
  onReviewedChange?: (issueIds: string[]) => void
  // The importer is saving, converting, or otherwise consuming this draft.
  inspectionPaused?: boolean
}
const labels: Record<string, string> = { restore_body: 'Restored body', add_parameter: 'Parameter values, one per line', add_extractor: 'Extractor expression', bind_variable: 'Existing producer or parameter name', replace_proxy_secret: 'Project secret name' }

export function ImportGuidedRepair({ script, projectId, disabled, disabledReason, onApplied, onViewSource, onSection, onCredentialTarget, onReimport, view = 'overview', additionalFindings = [], onReview, onReviewedChange, categoryFocus, focusPath, focusRequest, onBusyChange, inspectionPaused = false }: Props) {
  const settings = useImportSettings()
  const candidate = String(script.metadata?.import?.preserved?.server_import_attestation_v1 || '')
  const scope = `${candidate}:${projectId}`
  const currentScope = useRef(scope)
  currentScope.current = scope
  const inspectionRef = useRef<{ scope: string; promise: Promise<RepairResult>; delivered?: boolean } | null>(null)
  const onReviewRef = useRef(onReview); onReviewRef.current = onReview
  useEffect(() => { currentScope.current = scope; return () => { currentScope.current = '' } }, [scope])
  const [review, setReview] = useState<RepairResult | null>(null)
  const [allocationPreview, setAllocationPreview] = useState<RepairResult | null>(null)
  const [allocationUser, setAllocationUser] = useState(1)
  const [preview, setPreview] = useState<RepairResult | null>(null)
  const [showComparison, setShowComparison] = useState(false)
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
  const previewVersion = useRef(0)
  const [category, setCategory] = useState<IssueCategory | 'all'>('all')
  useEffect(() => {
    if (!categoryFocus) return
    setCategory(categoryFocus.category); setFilter('all'); setSearch(''); setSelection(''); resetPreview()
  }, [categoryFocus])
  const [resolved, setResolved] = useState<ImportIssue[]>([])
  const [announcement, setAnnouncement] = useState('')
  const [reviewed, setReviewed] = useState<Set<string>>(new Set())
  const onReviewedChangeRef = useRef(onReviewedChange); onReviewedChangeRef.current = onReviewedChange
  useEffect(() => { onReviewedChangeRef.current?.([...reviewed]) }, [reviewed])
  const formDrafts = useRef(new Map<string, { kind: string; value: string; responsePath: string; extractorType: string; occurrence: string }>())
  useEffect(() => { if (selection) formDrafts.current.set(selection, { kind, value, responsePath, extractorType, occurrence }) }, [selection, kind, value, responsePath, extractorType, occurrence])
  useEffect(() => { formDrafts.current.clear(); setReviewed(new Set()) }, [scope])
  const [now, setNow] = useState(Date.now())
  const detailRef = useRef<HTMLHeadingElement>(null)
  useEffect(() => { if (view === 'overview') detailRef.current?.focus() }, [selection, view])
  const entries = useMemo(() => importActionEntries(script), [script])
  const issues = useMemo(() => groupImportFindings([...(review?.findings || draftImportFindings(script)), ...additionalFindings], script), [review, script, additionalFindings])
  const selected = issues.find(i => i.id === selection) || resolved.find(i => i.id === selection)
  const finding = selected?.findings.find(f => f.action_path === occurrence) || selected?.findings[0]
  const options = finding ? issueRepairOptions(finding.code) : []
  const selectedResolved = !!selected && !issues.some(i => i.id === selected.id)
  const blocked = issues.filter(i => i.blocking).length
  // A review mark settles a non-blocking note for this session, so it leaves
  // the unresolved counts and moves to the reviewed filter.
  const reviewedCount = issues.filter(issue => !issue.blocking && reviewed.has(issue.id)).length
  const unresolvedCount = issues.length - reviewedCount
  const reviewCount = unresolvedCount - blocked
  const inView = (i: ImportIssue) => (category === 'all' || i.category === category) && (filter !== 'blockers' || i.blocking) && (filter !== 'review' || !i.blocking) && `${i.title} ${i.findings.map(f => `${f.source_ref} ${f.action_path} ${f.detail}`).join(' ')}`.toLowerCase().includes(search.toLowerCase())
  const shown = (filter === 'resolved' ? resolved : issues).filter(i => inView(i) && (filter === 'reviewed' ? reviewed.has(i.id) : filter === 'resolved' || i.blocking || !reviewed.has(i.id)))
  // Notes this view would list if they were not marked reviewed.
  const hiddenAsReviewed = filter === 'all' || filter === 'review' ? issues.filter(i => inView(i) && !i.blocking && reviewed.has(i.id)).length : 0
  const expiry = review?.expires_at ? new Date(review.expires_at).getTime() : NaN
  const expired = Number.isFinite(expiry) && expiry <= now
  // While the importer consumes this draft, no repair may race it either.
  const locked = !!disabled || busy || loading || expired || inspectionPaused
  const hasUniqueParameters = script.parameters?.some(parameter => parameter.selection === 'unique')
  const allocationUnavailable = !candidate.startsWith('protected:')
    ? 'Allocation preview requires a protected import draft. Re-import with encrypted import storage configured to enable it.'
    : expired ? 'This import draft has expired. Re-import the archive to preview allocation.'
    : Object.keys(settings?.editors || {}).length ? 'Apply or cancel pending edits before previewing allocation.'
    : loading ? 'Checking the import draft. Allocation preview will be available when the check finishes.'
    : busy ? 'An import operation is in progress. Wait for it to finish before previewing allocation.'
    : disabled ? disabledReason || 'Allocation preview is unavailable while this draft is locked. Finish pending operations or refresh the import review.'
    : inspectionPaused ? 'An import operation is in progress. Wait for it to finish before previewing allocation.'
    : ''
  const canRepair = candidate.startsWith('protected:') && !!review && !selectedResolved && !finding?.code.startsWith('diagnostic:')
  // The first inspection replaces the draft's provisional issues: until it
  // answers, counts would change under the reader, so they read "checking".
  const checking = loading && !review
  const counted = (label: string, count: number) => checking ? label : `${label} (${count})`
  const preceding = entries.slice(0, entries.findIndex(e => e.path === finding?.action_path)).filter(e => !!e.action.target && e.action.type !== 'http_sequence')
  const requestPayload = (operation: Operation, target = finding) => ({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: target?.code || '', action_path: target?.action_path || '', operation })
  function resetPreview() { previewVersion.current++; setPreview(null); setPending(null); setShowComparison(false) }
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(timer)
  }, [])
  const resetScopeRef = useRef<string | null>(null)
  useEffect(() => {
    const inspectionScope = `${candidate}:${projectId}`
    if (resetScopeRef.current !== inspectionScope) {
      resetScopeRef.current = inspectionScope
      setAllocationPreview(null); setReview(null); setPreview(null); setShowComparison(false); setValue(''); setError(''); setPending(null); setBusy(false)
      setLoading(false)
    }
    if (!candidate.startsWith('protected:')) return
    // A save or draft mutation consumes this candidate. Inspecting it at the same
    // time only races that request and reports a stale candidate, so wait until
    // the importer is idle; an inspection that already started is still shown.
    const started = inspectionRef.current?.scope === inspectionScope ? inspectionRef.current : null
    if (started?.delivered || (inspectionPaused && !started)) return
    let active = true
    setLoading(true)
    // StrictMode replays effects; reuse one inspection per candidate instead of issuing duplicates.
    const inspection = started
      ?? (inspectionRef.current = { scope: inspectionScope, promise: request<RepairResult>('/scripts/import/repairs/preview', { method: 'POST', body: JSON.stringify({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: '', action_path: '', operation: { type: 'inspect' } }) }) })
    inspection.promise
      .then(result => { if (active) { inspection.delivered = true; setReview(result); onReviewRef.current?.(result) } }).catch(e => { if (active) { inspection.delivered = true; setError(String(e.message || e)) } }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [candidate, projectId, inspectionPaused])
  const lastFocus = useRef<number>()
  useEffect(() => {
    if (!focusPath || lastFocus.current === focusRequest) return
    const issue = issues.find(i => i.findings.some(f => f.action_path === focusPath))
    if (!issue) return
    lastFocus.current = focusRequest
    setCategory('all'); setFilter('all'); setSearch('')
    const saved = formDrafts.current.get(issue.id)
    setSelection(issue.id); setOccurrence(focusPath)
    setKind(saved?.kind || issueRepairOptions(issue.findings.find(f => f.action_path === focusPath)?.code || '')[0]?.value || '')
    setValue(saved?.value || ''); setResponsePath(saved?.responsePath || ''); setExtractorType(saved?.extractorType || 'jsonpath'); resetPreview()
  }, [focusPath, focusRequest, issues])
  function choose(issue: ImportIssue) {
    const saved = formDrafts.current.get(issue.id)
    setSelection(issue.id); setOccurrence(saved?.occurrence || issue.findings[0]?.action_path || ''); setKind(saved?.kind || issueRepairOptions(issue.findings[0]?.code || '')[0]?.value || '')
    setValue(saved?.value || ''); setResponsePath(saved?.responsePath || ''); setExtractorType(saved?.extractorType || 'jsonpath'); setError(''); resetPreview()
  }
  async function run(operation: Operation, apply = false) {
    if (locked || (operation.type === 'preview_allocation' && allocationUnavailable)) return
    const requestScope = currentScope.current
    const requestPreviewVersion = previewVersion.current
    const target = apply ? pending?.finding : finding
    setBusy(true); setError('')
    try {
      const result = await request<RepairResult>(`/scripts/import/repairs/${apply ? 'apply' : 'preview'}`, { method: 'POST', body: JSON.stringify(requestPayload(operation, target)) })
      if (requestScope !== currentScope.current) return
      if (apply) {
        const remaining = groupImportFindings(result.findings, result.script)
        setResolved(previous => [...previous.filter(i => !remaining.some(next => next.id === i.id)), ...issues.filter(i => !i.findings[0]?.code.startsWith('diagnostic:') && !remaining.some(next => next.id === i.id) && !previous.some(p => p.id === i.id))])
        setValue(''); resetPreview(); setReview(result); setAnnouncement(`Repair applied. ${remaining.filter(i => i.blocking).length} grouped issues block replay.`); onApplied(result)
      } else if (operation.type === 'preview_allocation') { setAllocationPreview(result) } else if (requestPreviewVersion === previewVersion.current) { setPreview(result); setPending(target ? { operation, finding: target } : null) }
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
  const nextIssues = shown.filter(issue => !reviewed.has(issue.id))
  const nextIssue = nextIssues[(nextIssues.findIndex(issue => issue.id === selection) + 1) % nextIssues.length]
  const issueLocation = finding?.source_ref || finding?.action_path
  const canViewSource = !!finding && !!onViewSource && !!(finding.action_path || finding.source_mapping_id || finding.source_file)
  const narrowerAssertion = /web_reg_find.*Search=ALL/i.test(finding?.detail || '')
  const runtimeDiagnostic = selected?.category === 'runtime' && !!finding?.code.startsWith('diagnostic:')
  const issueImpact = selected?.category === 'values' ? finding?.code.startsWith('conditional_') ? 'A possible producer exists, but its order or execution path needs review. It must run before the selected request.' : 'This value has no reachable producer. Bind an existing value, add test data, or extract it from a preceding response.' : selected?.category === 'bodies' ? 'The original payload could not be restored. Supply the body below; previews keep its contents protected.' : finding?.code === 'legacy_tls_replacement' ? 'The source uses legacy TLS behavior. Choose a supported replacement and preview the migration.' : narrowerAssertion ? 'The converted check can miss text outside the main response body. Verify the intended assertion before replay.' : runtimeDiagnostic ? 'Some imported settings need an explicit choice or have no editable equivalent. Review their source values and replay consequences below.' : finding?.detail || 'Inspect the evidence and choose an available resolution below.'
  const previewCounts = preview ? groupImportFindings(preview.findings, preview.script) : []
  const previewBlockers = previewCounts.filter(issue => issue.blocking).length
  const changedPaths = preview ? changedRepairPaths(preview.before, preview.script) : []
  const previewContent = preview && <section className="lr-repair-preview" aria-label="Redacted repair preview">
    <header className="lr-repair-preview-heading"><div><span className="script-create-kicker">Repair preview</span><h4>Review this change</h4><p>Check the outcome and changed fields before applying this repair.</p></div><span className="repository-status-pill imported-runtime-file-tag">Preview only</span></header>
    <div className="lr-repair-preview-outcome" role="group" aria-label="Repair outcome">
      <div><span>Replay checks</span><strong className={preview.readiness.replay_ready ? 'healthy' : 'danger'}>{preview.readiness.replay_ready ? 'Passed' : 'Blocked'}</strong><small>{preview.readiness.replay_ready ? 'Target replay is not verified' : 'Execution remains blocked'}</small></div>
      <div><span>Blocking issues</span><strong>{previewBlockers}</strong><small>grouped issues after this change</small></div>
      <div><span>Credential requirements</span><strong>{preview.readiness.unresolved_bindings || 0}</strong><small>unresolved after this change</small></div>
    </div>
    <div className="lr-repair-preview-fields"><div className="lr-repair-preview-section-heading"><h5>Fields changed</h5><span>{changedPaths.length} shown</span></div>{changedPaths.length ? <ul>{changedPaths.map(path => <li key={path}><code>{path}</code></li>)}</ul> : <p>No field paths changed in this preview.</p>}<p>Only field paths are summarized here. Protected values stay hidden; up to 100 fields are listed.</p></div>
    {preview.allocation && <div className="lr-issue-table"><table><thead><tr><th>Parameter</th><th>Usable rows</th><th>Block per VUser</th><th>VUsers</th></tr></thead><tbody>{preview.allocation.map(p => <tr key={p.name}><td>{p.name}</td><td>{p.rows}</td><td>{p.block_size}</td><td>{p.population}</td></tr>)}</tbody></table></div>}
    <details className="lr-repair-preview-comparison" onToggle={event => setShowComparison(event.currentTarget.open)}><summary>Redacted before / after</summary>{showComparison && <div className="lr-repair-preview-code-grid"><div><h6>Before repair</h6><SourceCodeViewer ariaLabel="Before repair JSON" sourcePath="before.json" value={JSON.stringify(preview.before, null, 2)} /></div><div><h6>After repair</h6><SourceCodeViewer ariaLabel="After repair JSON" sourcePath="after.json" value={JSON.stringify(preview.script, null, 2)} /></div></div>}</details>
    {pending && pending.operation.type !== 'preview_allocation' && <div className="lr-repair-preview-footer"><p>Preview only. Nothing changes until you apply.</p><button className="btn btn-primary" type="button" disabled={locked} onClick={() => run(pending.operation, true)}>Apply reviewed repair</button></div>}
  </section>
  return <section className="lr-issue-workspace" aria-label={view === 'data' ? 'Preview unique data allocation' : 'Guided import repair'} hidden={view === 'hidden' || (view === 'data' && (!hasUniqueParameters || (!!settings && settings.dataView !== 'allocation')))}>
    <p className="lr-repair-announcement" role="status">{announcement}</p>
    {view !== 'data' ? <>
      <ImportReviewSectionHeader title="Resolve import issues" description={`${resolved.length} resolved${reviewedCount ? ` and ${reviewedCount} reviewed` : ''} in this session. Repeated findings are grouped; request locations are available within each issue.`} actions={
        <button type="button" className="btn btn-secondary" disabled={busy || loading || filter === 'resolved' || !nextIssue || nextIssue.id === selection} onClick={() => nextIssue && choose(nextIssue)}>Next unresolved issue</button>
      } />
      {(expired || (!candidate.startsWith('protected:') && issues.some(issue => issue.findings.some(item => issueRepairOptions(item.code).length > 0)))) && <div className="lr-issue-unavailable" role="status"><strong>{expired ? 'This protected draft has expired' : 'Protected repairs unavailable for this draft'}</strong><p>{expired ? 'Revalidate the archive to renew its repair session.' : 'Body, correlation, and TLS repairs require encrypted import storage. Existing settings and credential editors remain available when supported.'}</p>{onReimport && <button type="button" className="btn btn-secondary btn-sm" onClick={onReimport}>Open archive setup to revalidate</button>}</div>}
      <div className="import-review-toolbar"><label>Find an issue<input type="search" autoComplete="off" value={search} placeholder="Search values or source locations" onChange={e => setSearch(e.target.value)} /></label>
        <label>Show<select value={filter} onChange={e => setFilter(e.target.value)}><option value="all">{counted('All unresolved', unresolvedCount)}</option><option value="blockers">{counted('Blockers', blocked)}</option><option value="review">{counted('Review notes', reviewCount)}</option><option value="resolved">{counted('Resolved', resolved.length)}</option><option value="reviewed">{counted('Reviewed with limitation', reviewedCount)}</option></select></label>
        <label>Issue category<select value={category} onChange={e => { setCategory(e.target.value as IssueCategory | 'all'); setSelection(''); resetPreview() }}><option value="all">All categories</option>{(Object.keys(issueCategories) as IssueCategory[]).map(key => <option value={key} key={key}>{issueCategories[key]}</option>)}</select></label>
        {/* The one place a running inspection is announced. */}
        <span role="status">{loading ? 'Checking repair requirements…' : `${shown.length} matching issues`}</span>
      </div>
      <div className="lr-issue-layout"><div className="lr-issue-queue" aria-label="Import issue queue">
        {(Object.keys(issueCategories) as IssueCategory[]).map(category => {
          const rows = shown.filter(i => i.category === category)
          if (!rows.length) return null
          return <details key={category} open><summary>{issueCategories[category]} <span>{rows.length} issues</span></summary><ul>{rows.map(issue => <li key={issue.id}><button type="button" className="lr-issue-row" aria-pressed={selection === issue.id} onClick={() => choose(issue)} disabled={busy} onKeyDown={event => {
              if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
              event.preventDefault()
              const buttons = Array.from(event.currentTarget.closest('.lr-issue-queue')?.querySelectorAll<HTMLButtonElement>('.lr-issue-row:not(:disabled)') || []).filter(button => button.closest('details')?.open !== false)
              const index = buttons.indexOf(event.currentTarget)
              const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length
              buttons[next]?.focus()
            }}>
            <span className="lr-issue-row-title">{issue.title}</span><span className="lr-issue-row-meta"><ImportIssueSeverityTag severity={filter === 'resolved' ? 'resolved' : issue.blocking ? 'blocker' : 'info'} /><span>{reviewed.has(issue.id) ? 'Reviewed with limitation · ' : ''}{issueScope(issue)}</span></span>
          </button></li>)}</ul></details>
        })}
        {!shown.length && <p>{hiddenAsReviewed ? 'Every issue in this view is marked reviewed. Show "Reviewed with limitation" to reopen one.' : search ? 'No issues match this search.' : filter === 'resolved' ? 'Applied repairs will appear here when their issues are resolved.' : 'No issues in this view.'}</p>}
      </div><section className="lr-issue-detail" aria-label="Selected issue">
        {!selected || !finding ? <><h4>Select an issue to get started</h4><p>See its impact, inspect the source, and preview the repair before applying it.</p></> : <>
          <header className="lr-issue-detail-heading"><h4 tabIndex={-1} ref={detailRef} onKeyDown={event => { if (event.key === 'Escape') { event.preventDefault(); event.currentTarget.closest('.lr-issue-layout')?.querySelector<HTMLButtonElement>('.lr-issue-row[aria-pressed="true"]')?.focus() } }}>{selected.title}</h4>
          <p><span>{selectedResolved ? 'Resolved · ' : reviewed.has(selected.id) ? 'Reviewed with limitation · this session · ' : ''}{issueScope(selected)}</span></p></header>
          <section aria-label="Problem and impact"><h5>Why this matters</h5><p>{issueImpact}</p></section>
          {narrowerAssertion && <dl className="lr-issue-comparison"><div><dt>Source behavior</dt><dd>Search all response content</dd></div><div><dt>Current replay behavior</dt><dd>Search the main response body only</dd></div></dl>}
          {issueLocation && <p className="lr-issue-location"><strong>Location</strong> {issueLocation} {canViewSource && <button type="button" className="btn btn-secondary btn-sm" onClick={() => onViewSource?.(finding.action_path || '', finding)}>Inspect {finding.source_ref || 'selected request'}</button>}</p>}
          {runtimeDiagnostic && <ImportIssueRuntimeEditor script={script} migrationPending={issues.some(issue => issue.findings.some(item => item.code === 'legacy_tls_replacement'))} />}
          {selected.category === 'credentials' && onCredentialTarget && view === 'overview' && <section aria-label="Fix credential bindings here"><h5>Configure credentials</h5><div ref={onCredentialTarget} /></section>}
          {selectedResolved ? <p>This issue was removed by an applied repair. Remaining requirements are evaluated independently.</p> : canRepair && options.length ? <div className="lr-repair-form"><h5>Fix this issue</h5>
            <label>Repair operation<select aria-label="Repair operation" value={kind} disabled={locked} onChange={e => { setKind(e.target.value); setValue(''); resetPreview() }}>{options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}</select></label>
            {selected.findings.length > 1 && <label>Consumer to repair<select aria-label="Repair finding" value={finding.action_path || ''} disabled={locked} onChange={e => { setOccurrence(e.target.value); setResponsePath(''); resetPreview() }}>{selected.findings.map(f => <option key={f.action_path} value={f.action_path}>{f.source_ref || f.action_path} — {entries.find(e => e.path === f.action_path)?.action.name || f.action_path}</option>)}</select></label>}
            <p className="lr-subtitle">{kind === 'add_parameter' ? 'Adds a script-wide parameter. Preview checks which occurrences it satisfies.' : 'Repairs the selected consumer or setting. Other occurrences stay unresolved until the updated checks prove they are satisfied.'}</p>
            {kind === 'add_extractor' && <><label>Preceding response<select aria-label="Preceding response path" value={responsePath} disabled={locked} onChange={e => { setResponsePath(e.target.value); resetPreview() }}><option value="">Choose a preceding response</option>{preceding.map(e => <option key={e.path} value={e.path}>{e.action.name || e.path} · {e.path}</option>)}</select></label><label>Extractor type<select value={extractorType} disabled={locked} onChange={e => { setExtractorType(e.target.value); resetPreview() }}><option value="jsonpath">JSONPath</option><option value="regex">Regular expression</option></select></label></>}
            {kind === 'select_tls' ? <label>Minimum TLS<select value={value || '1.2'} disabled={locked} onChange={e => { setValue(e.target.value); resetPreview() }}><option value="1.2">TLS 1.2 (up to 1.3)</option><option value="1.3">TLS 1.3 only</option></select><span>This records an intentional migration change, not exact SSL 2/3 parity.</span></label> : <label>{labels[kind]}<textarea aria-label="Repair value" autoComplete="off" value={value} onChange={e => { setValue(e.target.value); resetPreview() }} disabled={locked} rows={kind === 'restore_body' ? 6 : 3} /></label>}
            <div className="lr-issue-action-row"><button className="btn btn-primary" type="button" disabled={locked || (kind !== 'restore_body' && kind !== 'select_tls' && !value.trim()) || (kind === 'add_extractor' && !responsePath)} onClick={() => run(operation())}>{busy ? 'Checking repair…' : 'Preview repair'}</button><span>Preview first. Nothing changes until you apply.</span></div>
            {previewContent}
          </div> : <div className="lr-repair-actions"><h5>{options.length ? 'Repair availability' : 'Available resolution'}</h5><p>{options.length ? (loading ? 'Checking whether a guided repair applies to this issue…' : 'Protected repairs are unavailable until this archive has a current protected draft. Use archive setup to revalidate after encrypted import storage is configured.') : selected.blocking ? 'This requires a supported source or configuration change. Reviewing the limitation cannot clear this blocker.' : 'Review the specific behavior above. A review decision records your assessment; it does not change replay behavior or prove compatibility.'}</p>
            {onSection && selected.category !== 'review' && !(selected.category === 'credentials' && onCredentialTarget) && <button className="btn btn-secondary btn-sm" type="button" onClick={() => onSection(selected.category === 'credentials' ? 'credentials' : selected.category === 'values' ? 'data' : selected.category === 'runtime' ? 'preserved' : 'source')}>{selected.category === 'credentials' ? 'Configure credential bindings' : selected.category === 'values' ? 'Edit data and parameters' : selected.category === 'runtime' ? 'Open all runtime settings' : 'Inspect source files'}</button>}
            {!options.length && selected.category === 'review' && !canViewSource && <p>No unique source mapping is available. Use the diagnostic location below to find the relevant source; no unrelated mapping will be selected automatically.</p>}
          </div>}
          {!selectedResolved && !selected.blocking && <div className="lr-issue-review-decision"><button type="button" className="btn btn-secondary btn-sm" disabled={busy} onClick={() => { setReviewed(previous => { const next = new Set(previous); if (next.has(selected.id)) next.delete(selected.id); else next.add(selected.id); return next }); setAnnouncement('Review decision updated for this session. Replay requirements are unchanged.') }}>{reviewed.has(selected.id) ? 'Reopen review decision' : 'Mark reviewed with limitation'}</button><small>Session note only. Does not clear replay requirements or modify the script.</small></div>}
          <details className="lr-issue-occurrences"><summary>Technical evidence and affected locations</summary>
            {finding.detail && <p>{finding.detail}</p>}{finding.evidence?.length ? <ul>{finding.evidence.map((item, index) => <li key={index}>{item}</li>)}</ul> : null}
            <ul>{selected.findings.map(f => <li key={`${f.action_path}:${f.code}`}><strong>{entries.find(e => e.path === f.action_path)?.action.name || f.action_path || 'Script-wide requirement'}</strong>{f.source_ref && <span>{f.source_ref}</span>}{f.action_path && onViewSource && <button className="btn btn-secondary btn-sm" type="button" onClick={() => onViewSource(f.action_path!, f)}>View source</button>}</li>)}</ul>
          </details>

        </>}
      </section></div>
    </> : <>
      <ImportReviewSectionHeader title="Preview unique data allocation" description="Check row capacity for the planned run without changing settings or launching a test. Edit rules in Parameters." />
      {allocationUnavailable && <p role="status">{allocationUnavailable}</p>}
      <fieldset className="lr-repair-form lr-allocation-form" disabled={!!allocationUnavailable}><legend>Planned run</legend><label>Planned VUsers<input type="number" min={1} value={users} onChange={e => { setUsers(Number(e.target.value)); setAllocationPreview(null); setAllocationUser(1) }} /></label><label>Run mode<select value={runMode} onChange={e => { setRunMode(e.target.value); setAllocationPreview(null) }}><option value="duration">Duration</option><option value="iterations">Iterations</option></select></label>{runMode === 'iterations' && <label>Iterations per VUser<input type="number" min={1} value={iterations} onChange={e => { setIterations(Number(e.target.value)); setAllocationPreview(null) }} /></label>}<button className="btn btn-secondary" type="button" disabled={!Number.isInteger(users) || users < 1 || (runMode === 'iterations' && (!Number.isInteger(iterations) || iterations < 1))} onClick={() => run({ type: 'preview_allocation', vusers: users, iterations: runMode === 'duration' ? 0 : iterations })}>{busy ? 'Calculating allocation…' : 'Preview allocation'}</button></fieldset>
      {allocationPreview && <section aria-label="Allocation result" className="lr-repair-preview"><h4>Capacity preview: {users} VUsers · {runMode === 'duration' ? 'duration run' : `${iterations} iterations per VUser`}</h4>
        <p role="status">{allocationPreview.allocation?.length ? 'Capacity available for the planned population. This does not clear other replay blockers.' : 'No uniquely allocated parameters in this draft.'}</p>
        {!!allocationPreview.allocation?.length && <><label>Inspect VUser ordinal<input type="number" min={1} max={users} value={allocationUser} onChange={e => setAllocationUser(Math.max(1, Math.min(users, Number(e.target.value) || 1)))} /></label><div className="lr-table-wrap"><table className="lr-parameter-table"><thead><tr><th>Parameter</th><th>Usable rows</th><th>Rows per VUser</th><th>Unused rows</th><th>VUser {allocationUser} row range</th></tr></thead><tbody>{allocationPreview.allocation.map(p => { const start = Math.max(1, script.parameters?.find(x => x.name === p.name)?.start_row || 1) + (allocationUser - 1) * p.block_size; return <tr key={p.name}><th scope="row">{p.name}</th><td>{p.rows}</td><td>{p.block_size}</td><td>{p.rows - p.block_size * p.population}</td><td>{start}–{start + p.block_size - 1}</td></tr> })}</tbody></table></div><p>Row numbers refer to data rows, excluding a header. Allocation is reserved across the whole planned population, including ramps.</p></>}
      </section>}
    </>}
    {error && <p className="lr-warnings" role="alert">{error} Re-import if the protected candidate has expired. If an apply request was interrupted, retry the same reviewed repair.</p>}
  </section>
}
