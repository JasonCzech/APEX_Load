import ImportIssueRuntimeEditor from './ImportIssueRuntimeEditor'
import ImportIssueSeverityTag from './ImportIssueSeverityTag'
import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { GuidedRepairForm, RepairPreview } from './GuidedRepairForm'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useImportSettings } from './ImportSettingsContext'
import { request } from '../hooks/apiClient'
import type { EnterpriseArchiveFidelityReport, SecretBindingRequirement } from '../types/scriptImports'
import type { ScriptDefinition } from '../types/scripts'
import { countLabel } from '../utils/numberFormat'
import { importActionEntries, isConfigReferenceIssue, issueCategories, issueRepairOptions, issueScope, readinessCounts, readinessSummaryText, repairImpactText, unresolvedIssueCount, type ImportFinding, type ImportIssue, type IssueCategory, type RepairFields, type RepairOperation } from './importReviewIssues'

export type RepairResult = { secret_binding_requirements: SecretBindingRequirement[]; fidelity_report: EnterpriseArchiveFidelityReport; candidate_id: string; revision: string; script: ScriptDefinition; before: ScriptDefinition; findings: ImportFinding[]; readiness: { replay_ready: boolean; promotion_ready: boolean; blocking_codes?: string[]; validation_issues?: string[]; unresolved_bindings?: number }; allocation?: { name: string; rows: number; block_size: number; population: number }[]; expires_at: string }
type Props = {
  script: ScriptDefinition; projectId: string; disabled?: boolean; disabledReason?: string; onApplied: (result: RepairResult) => void; onViewSource?: (path: string, finding?: ImportFinding) => void
  onCredentialTarget?: (target: HTMLDivElement | null) => void; onReimport?: () => void
  // The archive's own Validate again, for a draft kept with applied repairs:
  // validating from archive setup leaves it alone, so once it expires only
  // this renews it (discarding those repairs).
  validateAgain?: { disabled: boolean; onValidate: () => void }
  // The draft is in a save APEX has not confirmed: Save resends that save,
  // and validating again would drop it, so no Validate again is offered.
  unconfirmedSave?: boolean
  onSection?: (section: 'credentials' | 'data' | 'preserved' | 'source') => void
  categoryFocus?: { category: IssueCategory | 'all'; revision: number };
  focusPath?: string; focusRequest?: number; onBusyChange?: (busy: boolean) => void
  view?: 'overview' | 'data' | 'hidden'; onReview?: (result: RepairResult) => void
  onReviewedChange?: (issueIds: string[]) => void
  // The importer is saving, converting, or otherwise consuming this draft.
  inspectionPaused?: boolean
  // The importer's grouped issues for this draft: the queue lists exactly
  // these, so its counts are the importer's counts.
  issues: ImportIssue[]
  // The importer's inspection of the current candidate, an apply response
  // included. One that matches the candidate is not inspected again.
  inspection?: RepairResult | null
  // Groups a repair response as the importer would list it, for the apply
  // announcement and the preview outcome.
  regroup: (result: RepairResult) => ImportIssue[]
  // The importer still waits for this candidate's inspection: counts read "Checking…".
  countsPending?: boolean
}

export function ImportGuidedRepair({ script, projectId, disabled, disabledReason, onApplied, onViewSource, onSection, onCredentialTarget, onReimport, validateAgain, unconfirmedSave = false, view = 'overview', onReview, onReviewedChange, categoryFocus, focusPath, focusRequest, onBusyChange, inspectionPaused = false, issues, inspection, regroup, countsPending = false }: Props) {
  const settings = useImportSettings()
  const candidate = String(script.metadata?.import?.preserved?.server_import_attestation_v1 || '')
  const scope = `${candidate}:${projectId}`
  const currentScope = useRef(scope)
  currentScope.current = scope
  const inspectionRef = useRef<{ scope: string; promise: Promise<RepairResult>; delivered?: boolean } | null>(null)
  const onReviewRef = useRef(onReview); onReviewRef.current = onReview
  useEffect(() => { currentScope.current = scope; return () => { currentScope.current = '' } }, [scope])
  const [inspected, setInspected] = useState<RepairResult | null>(null)
  const importerInspection = candidate && inspection?.candidate_id === candidate ? inspection : null
  const importerInspectionRef = useRef(importerInspection); importerInspectionRef.current = importerInspection
  const review = importerInspection || inspected
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
  const [pending, setPending] = useState<{ operation: RepairOperation; finding: ImportFinding } | null>(null)
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
  const selected = issues.find(i => i.id === selection) || resolved.find(i => i.id === selection)
  // A configuration reference reads as its configuration finding, whichever
  // step's finding for the value led here: no step repair supplies it.
  const finding = selected && isConfigReferenceIssue(selected) ? selected.findings[0] : selected?.findings.find(f => f.action_path === occurrence) || selected?.findings[0]
  const options = finding ? issueRepairOptions(finding.code) : []
  const selectedResolved = !!selected && !issues.some(i => i.id === selected.id)
  // A review mark settles a warning or note for this session, so it leaves
  // the unresolved counts and moves to the reviewed filter.
  const counts = readinessCounts(issues, reviewed)
  const unresolvedCount = unresolvedIssueCount(counts)
  const inView = (i: ImportIssue) => (category === 'all' || i.category === category) && (filter !== 'blockers' || i.blocking) && (filter !== 'warnings' || i.severity === 'warning') && (filter !== 'review' || i.severity === 'note') && `${i.title} ${i.findings.map(f => `${f.source_ref} ${f.action_path} ${f.detail}`).join(' ')}`.toLowerCase().includes(search.toLowerCase())
  const shown = (filter === 'resolved' ? resolved : issues).filter(i => inView(i) && (filter === 'reviewed' ? reviewed.has(i.id) : filter === 'resolved' || i.blocking || !reviewed.has(i.id)))
  // Notes this view would list if they were not marked reviewed.
  const hiddenAsReviewed = filter === 'all' || filter === 'warnings' || filter === 'review' ? issues.filter(i => inView(i) && !i.blocking && reviewed.has(i.id)).length : 0
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
  // Until the first inspection answers, the issues are the draft's provisional
  // ones, so counts read "checking" here exactly when they do in the importer.
  const checking = countsPending || (loading && !review)
  const counted = (label: string, count: number) => checking ? label : `${label} (${count})`
  const requestPayload = (operation: RepairOperation, target = finding) => ({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: target?.code || '', action_path: target?.action_path || '', operation })
  function resetPreview() { previewVersion.current++; setPreview(null); setPending(null) }
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000)
    return () => clearInterval(timer)
  }, [])
  const resetScopeRef = useRef<string | null>(null)
  useEffect(() => {
    const inspectionScope = `${candidate}:${projectId}`
    // An apply response already describes the candidate it created, and the
    // importer passes it back as this candidate's inspection: keep the
    // repair's own state and do not inspect that candidate again.
    const answered = importerInspectionRef.current?.candidate_id === candidate
    if (resetScopeRef.current !== inspectionScope) {
      resetScopeRef.current = inspectionScope
      if (!answered) {
        setAllocationPreview(null); setInspected(null); setPreview(null); setValue(''); setError(''); setPending(null); setBusy(false)
        setLoading(false)
      }
    }
    if (!candidate.startsWith('protected:') || answered) return
    // A save or draft mutation consumes this candidate. Inspecting it at the same
    // time only races that request and reports a stale candidate, so wait until
    // the importer is idle; an inspection that already started is still shown.
    const started = inspectionRef.current?.scope === inspectionScope ? inspectionRef.current : null
    if (started?.delivered || (inspectionPaused && !started)) return
    let active = true
    setLoading(true)
    // StrictMode replays effects; reuse one inspection per candidate instead of issuing duplicates.
    const pendingInspection = started
      ?? (inspectionRef.current = { scope: inspectionScope, promise: request<RepairResult>('/scripts/import/repairs/preview', { method: 'POST', body: JSON.stringify({ candidate_id: candidate, revision: candidate, project_id: projectId, finding: '', action_path: '', operation: { type: 'inspect' } }) }) })
    pendingInspection.promise
      .then(result => { if (active) { pendingInspection.delivered = true; setInspected(result); onReviewRef.current?.(result) } }).catch(e => { if (active) { pendingInspection.delivered = true; setError(String(e.message || e)) } }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [candidate, projectId, inspectionPaused])
  const lastFocus = useRef<number | undefined>(undefined)
  useEffect(() => {
    if (!focusPath || lastFocus.current === focusRequest) return
    const issue = issues.find(i => i.findings.some(f => f.action_path === focusPath))
    if (!issue) return
    lastFocus.current = focusRequest
    setCategory('all'); setFilter('all'); setSearch('')
    const saved = formDrafts.current.get(issue.id)
    // A configuration reference keeps no step occurrence, so no step form opens for it now or later.
    const config = isConfigReferenceIssue(issue)
    setSelection(issue.id); setOccurrence(config ? '' : focusPath)
    setKind(saved?.kind || issueRepairOptions((config ? issue.findings[0] : issue.findings.find(f => f.action_path === focusPath))?.code || '')[0]?.value || '')
    setValue(saved?.value || ''); setResponsePath(saved?.responsePath || ''); setExtractorType(saved?.extractorType || 'jsonpath'); resetPreview()
  }, [focusPath, focusRequest, issues])
  function choose(issue: ImportIssue) {
    const saved = formDrafts.current.get(issue.id)
    setSelection(issue.id); setOccurrence(isConfigReferenceIssue(issue) ? '' : saved?.occurrence || issue.findings[0]?.action_path || ''); setKind(saved?.kind || issueRepairOptions(issue.findings[0]?.code || '')[0]?.value || '')
    setValue(saved?.value || ''); setResponsePath(saved?.responsePath || ''); setExtractorType(saved?.extractorType || 'jsonpath'); setError(''); resetPreview()
  }
  async function run(operation: RepairOperation, apply = false) {
    if (locked || (operation.type === 'preview_allocation' && allocationUnavailable)) return
    const requestScope = currentScope.current
    const requestPreviewVersion = previewVersion.current
    const target = apply ? pending?.finding : finding
    setBusy(true); setError('')
    try {
      const result = await request<RepairResult>(`/scripts/import/repairs/${apply ? 'apply' : 'preview'}`, { method: 'POST', body: JSON.stringify(requestPayload(operation, target)) })
      if (requestScope !== currentScope.current) return
      if (apply) {
        // Grouped as the importer lists the repaired draft, its other findings included.
        const remaining = regroup(result)
        setResolved(previous => [...previous.filter(i => !remaining.some(next => next.id === i.id)), ...issues.filter(i => !i.findings[0]?.code.startsWith('diagnostic:') && !remaining.some(next => next.id === i.id) && !previous.some(p => p.id === i.id))])
        setValue(''); resetPreview(); setInspected(result); setAnnouncement(`Repair applied. ${readinessSummaryText(readinessCounts(remaining))}.`); onApplied(result)
      } else if (operation.type === 'preview_allocation') { setAllocationPreview(result) } else if (requestPreviewVersion === previewVersion.current) { setPreview(result); setPending(target ? { operation, finding: target } : null) }
    } catch (e) { if (requestScope === currentScope.current) setError(e instanceof Error ? e.message : 'Repair failed') }
    finally { if (requestScope === currentScope.current) setBusy(false) }
  }
  // The form's fields, which survive navigation through formDrafts; any
  // change drops the preview it no longer describes.
  const fields: RepairFields = { kind, value, responsePath, extractorType }
  function changeFields(change: Partial<RepairFields>) {
    if (change.kind !== undefined) setKind(change.kind)
    if (change.value !== undefined) setValue(change.value)
    if (change.responsePath !== undefined) setResponsePath(change.responsePath)
    if (change.extractorType !== undefined) setExtractorType(change.extractorType)
    resetPreview()
  }
  const nextIssues = shown.filter(issue => !reviewed.has(issue.id))
  const nextIssue = nextIssues[(nextIssues.findIndex(issue => issue.id === selection) + 1) % nextIssues.length]
  const issueLocation = finding?.source_ref || finding?.action_path
  const canViewSource = !!finding && !!onViewSource && !!(finding.action_path || finding.source_mapping_id || finding.source_file)
  const narrowerAssertion = /web_reg_find.*Search=ALL/i.test(finding?.detail || '')
  const runtimeDiagnostic = selected?.category === 'runtime' && !!finding?.code.startsWith('diagnostic:')
  const issueImpact = (finding && repairImpactText(finding.code, finding.detail)) || 'Inspect the evidence and choose an available resolution below.'
  const previewIssues = useMemo(() => preview ? regroup(preview) : [], [preview, regroup])
  const previewBlockers = readinessCounts(previewIssues).blockers
  const previewContent = preview && <RepairPreview preview={preview} blockers={previewBlockers} apply={pending && pending.operation.type !== 'preview_allocation' ? { disabled: locked, onApply: () => run(pending.operation, true) } : undefined} />
  return <section className="lr-issue-workspace" aria-label={view === 'data' ? 'Preview unique data allocation' : 'Guided import repair'} hidden={view === 'hidden' || (view === 'data' && (!hasUniqueParameters || (!!settings && settings.dataView !== 'allocation')))}>
    <p className="lr-repair-announcement" role="status">{announcement}</p>
    {view !== 'data' ? <>
      <ImportReviewSectionHeader title="Resolve import issues" description={`${resolved.length} resolved${counts.reviewed ? ` and ${counts.reviewed} reviewed` : ''} in this session. Repeated findings are grouped; request locations are available within each issue.`} actions={
        <button type="button" className="btn btn-secondary" disabled={busy || loading || filter === 'resolved' || !nextIssue || nextIssue.id === selection} onClick={() => nextIssue && choose(nextIssue)}>Next unresolved issue</button>
      } />
      {(expired || (!candidate.startsWith('protected:') && issues.some(issue => issue.findings.some(item => issueRepairOptions(item.code).length > 0)))) && <div className="lr-issue-unavailable" role="status"><strong>{expired ? 'This protected draft has expired' : 'Protected repairs unavailable for this draft'}</strong><p>{expired && unconfirmedSave ? 'APEX has not confirmed the earlier save of this draft. Save first: it resends the unconfirmed save, which APEX answers if it was saved.' : expired && validateAgain ? 'It can no longer be saved. Only Validate again on this archive renews it, and the repairs applied to it are discarded.' : expired ? 'Revalidate the archive to renew its repair session.' : 'Body, correlation, and TLS repairs require encrypted import storage. Existing settings and credential editors remain available when supported.'}</p>{expired && unconfirmedSave ? null : expired && validateAgain
        ? <button type="button" className="btn btn-secondary btn-sm" disabled={validateAgain.disabled} title="Validate this archive again. The repairs applied to its reviewed draft are discarded." onClick={validateAgain.onValidate}>Validate again</button>
        : onReimport && <button type="button" className="btn btn-secondary btn-sm" onClick={onReimport}>Open archive setup to revalidate</button>}</div>}
      <div className="import-review-toolbar"><label>Find an issue<input type="search" autoComplete="off" value={search} placeholder="Search values or source locations" onChange={e => setSearch(e.target.value)} /></label>
        <label>Show<select value={filter} onChange={e => setFilter(e.target.value)}><option value="all">{counted('All unresolved', unresolvedCount)}</option><option value="blockers">{counted('Blockers', counts.blockers)}</option><option value="warnings">{counted('Warnings', counts.warnings)}</option><option value="review">{counted('Review notes', counts.notes)}</option><option value="resolved">{counted('Resolved', resolved.length)}</option><option value="reviewed">{counted('Reviewed with limitation', counts.reviewed)}</option></select></label>
        <label>Issue category<select value={category} onChange={e => { setCategory(e.target.value as IssueCategory | 'all'); setSelection(''); resetPreview() }}><option value="all">All categories</option>{(Object.keys(issueCategories) as IssueCategory[]).map(key => <option value={key} key={key}>{issueCategories[key]}</option>)}</select></label>
        {/* The one place a running inspection is announced. */}
        <span role="status">{loading ? 'Checking repair requirements…' : `${shown.length} matching issues`}</span>
      </div>
      <div className="lr-issue-layout"><div className="lr-issue-queue" aria-label="Import issue queue">
        {(Object.keys(issueCategories) as IssueCategory[]).map(category => {
          const rows = shown.filter(i => i.category === category)
          if (!rows.length) return null
          return <details key={category} open><summary>{issueCategories[category]} <span>{checking ? 'Checking…' : countLabel(rows.length, 'issue')}</span></summary><ul>{rows.map(issue => <li key={issue.id}><button type="button" className="lr-issue-row" aria-pressed={selection === issue.id} onClick={() => choose(issue)} disabled={busy} onKeyDown={event => {
              if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
              event.preventDefault()
              const buttons = Array.from(event.currentTarget.closest('.lr-issue-queue')?.querySelectorAll<HTMLButtonElement>('.lr-issue-row:not(:disabled)') || []).filter(button => button.closest('details')?.open !== false)
              const index = buttons.indexOf(event.currentTarget)
              const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length
              buttons[next]?.focus()
            }}>
            <span className="lr-issue-row-title">{issue.title}</span><span className="lr-issue-row-meta"><ImportIssueSeverityTag severity={filter === 'resolved' ? 'resolved' : issue.severity === 'note' ? 'info' : issue.severity} /><span>{reviewed.has(issue.id) ? 'Reviewed with limitation · ' : ''}{issueScope(issue)}</span></span>
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
          {selectedResolved ? <p>This issue was removed by an applied repair. Remaining requirements are evaluated independently.</p> : canRepair && options.length ? <GuidedRepairForm finding={finding} occurrences={selected.findings} options={options} entries={entries} fields={fields} onChange={changeFields}
            onOccurrence={path => { setOccurrence(path); setResponsePath(''); resetPreview() }} locked={locked} busy={busy} onPreview={operation => run(operation)}>
            {previewContent}
          </GuidedRepairForm> : <div className="lr-repair-actions"><h5>{options.length ? 'Repair availability' : 'Available resolution'}</h5><p>{options.length ? (loading ? 'Checking whether a guided repair applies to this issue…' : 'Protected repairs are unavailable until this archive has a current protected draft. Use archive setup to revalidate after encrypted import storage is configured.') : selected.blocking ? 'This requires a supported source or configuration change. Reviewing the limitation cannot clear this blocker.' : 'Review the specific behavior above. A review decision records your assessment; it does not change replay behavior or prove compatibility.'}</p>
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
