import { Fragment, useEffect, useMemo, useRef, useState } from 'react'

import type { IssueDetailSeverity } from './IssueDetailsDialog'
import ImportIssueSeverityTag from './ImportIssueSeverityTag'
import type { ImportReviewTab } from './ImportReviewTabs'
import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { groupImportWorkspaceIssues, type GroupedImportWorkspaceIssue, type ImportWorkspaceIssue } from './importIssueWorkspaceModel'
import './IssueDetailsDialog.css'
import { countLabel } from '../utils/numberFormat'
const evidencePageSize = 100

export default function ImportIssueWorkspace({ issues, onNavigate, onSourceSelect, normalizedEvidence = false }: {
  normalizedEvidence?: boolean
  issues: ImportWorkspaceIssue[]
  onNavigate?: (tab: ImportReviewTab, issue?: GroupedImportWorkspaceIssue) => void
  onSourceSelect?: (issue: GroupedImportWorkspaceIssue) => void
}) {
  const grouped = useMemo(() => groupImportWorkspaceIssues(issues), [issues])
  const [selectedId, setSelectedId] = useState('')
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<'all' | IssueDetailSeverity | 'reviewed'>('all')
  const [reviewed, setReviewed] = useState<Set<string>>(new Set())
  const [evidencePage, setEvidencePage] = useState(0)
  const detailHeadingRef = useRef<HTMLHeadingElement>(null)
  const issuesVersion = issues.map(issue => `${issue.id}:${issue.severity}:${issue.title}:${issue.description || ''}:${issue.source || ''}`).join('\u001f')
  useEffect(() => {
    setSelectedId('')
    setReviewed(new Set())
    setEvidencePage(0)
    setSearch('')
    setFilter('all')
  }, [issuesVersion])
  const visible = grouped.filter(issue => {
    if (filter === 'reviewed' && !reviewed.has(issue.id)) return false
    if (filter !== 'all' && filter !== 'reviewed' && issue.severity !== filter) return false
    const haystack = `${issue.title} ${issue.occurrences.map(item => `${item.description || ''} ${item.source || ''}`).join(' ')}`.toLowerCase()
    return haystack.includes(search.trim().toLowerCase())
  })
  const blockers = grouped.filter(issue => issue.severity === 'blocker' || issue.severity === 'error').length
  const warnings = grouped.filter(issue => issue.severity === 'warning').length
  const notes = grouped.filter(issue => issue.severity === 'info').length
  const reviewedCount = grouped.filter(issue => reviewed.has(issue.id)).length
  const categoryLabel = (issue: typeof grouped[number]) => issue.targetTab === 'credentials' ? 'Credentials'
    : issue.targetTab === 'data' ? 'Data and parameters'
    : issue.targetTab === 'runtime' ? 'Runtime settings'
    : issue.targetTab === 'source' ? 'Source mapping'
    : 'Conversion findings'
  const orderedVisible = ['Credentials', 'Data and parameters', 'Runtime settings', 'Source mapping', 'Conversion findings']
    .flatMap(category => visible.filter(issue => categoryLabel(issue) === category))
  const selected = orderedVisible.find(issue => issue.id === selectedId) || orderedVisible[0]
  const categoryCounts = new Map<string, number>()
  visible.forEach(issue => categoryCounts.set(categoryLabel(issue), (categoryCounts.get(categoryLabel(issue)) || 0) + 1))
  const nextIssue = orderedVisible.find(issue => !reviewed.has(issue.id) && issue.id !== selected?.id)

  const selectedVersion = selected ? `${selected.id}:${selected.occurrences.length}:${selected.occurrences[0]?.description || ''}` : ''
  useEffect(() => {
    setEvidencePage(0)
    if (selectedVersion) detailHeadingRef.current?.focus()
  }, [selectedVersion])

  if (grouped.length === 0) return null
  return <section className="shared-import-issue-workspace" aria-label="Resolve import issues">
    <ImportReviewSectionHeader
      title="Resolve import issues"
      description={`${normalizedEvidence ? `${countLabel(grouped.length, 'finding')} · ${blockers} blocking · ${countLabel(warnings, 'warning')} · ${countLabel(notes, 'note')}` : `${countLabel(grouped.length, 'finding')} · ${blockers} blocking · ${countLabel(warnings, 'warning')} · ${countLabel(notes, 'review note')}`}${reviewedCount ? ` · ${reviewedCount} reviewed` : ''}. ${normalizedEvidence ? 'Informational findings do not block replay.' : 'Repeated findings are grouped by the operator decision they require.'}`}
      actions={nextIssue && <button type="button" className="btn btn-secondary" onClick={() => setSelectedId(nextIssue.id)}>Next unresolved issue</button>}
    />
    <div className="import-review-toolbar">
      <label>Find an issue<input type="search" autoComplete="off" value={search} placeholder="Search evidence or source locations" onChange={event => setSearch(event.target.value)} /></label>
      <label>Show<select value={filter} onChange={event => setFilter(event.target.value as typeof filter)}><option value="all">All findings ({grouped.length})</option><option value="blocker">Blockers</option><option value="error">Errors</option><option value="warning">Warnings</option><option value="info">Review notes</option><option value="reviewed">Reviewed ({reviewedCount})</option></select></label>
      <span role="status">{countLabel(visible.length, 'matching finding')}</span>
    </div>
    <div className="shared-import-issue-layout">
      <div className="shared-import-issue-list" aria-label="Import issue queue">
        {visible.length === 0 && <p className="import-review-empty">No issues match this filter.</p>}
        {orderedVisible.map((issue, index) => <Fragment key={issue.id}>
          {(index === 0 || categoryLabel(orderedVisible[index - 1]) !== categoryLabel(issue)) && <h5 className="shared-import-issue-category">{categoryLabel(issue)} <span>{categoryCounts.get(categoryLabel(issue))} issue{categoryCounts.get(categoryLabel(issue)) === 1 ? '' : 's'}</span></h5>}
          <button
          type="button"
          className="shared-import-issue-row"
          aria-pressed={selected?.id === issue.id}
          onClick={() => setSelectedId(issue.id)}
          onKeyDown={event => {
            if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
            event.preventDefault()
            const buttons = Array.from(event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('.shared-import-issue-row') || [])
            const nextIndex = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length
            buttons[nextIndex]?.focus()
          }}
        >
          <ImportIssueSeverityTag severity={issue.severity} />
          <span><strong>{issue.title}</strong><small>{issue.occurrences.length} occurrence{issue.occurrences.length === 1 ? '' : 's'}{reviewed.has(issue.id) ? ' · reviewed' : ''}</small></span>
        </button></Fragment>)}
      </div>
      <article className="shared-import-issue-detail" aria-label="Selected issue">
        {selected ? <>
          <div className="shared-import-issue-detail-heading">
            <div><h4 ref={detailHeadingRef} tabIndex={-1}>{selected.title}</h4><p>{selected.occurrences.length} affected location{selected.occurrences.length === 1 ? '' : 's'}</p></div>
          </div>
          <div className="shared-import-issue-actions" aria-label="Issue actions">
            {selected.targetTab && onNavigate && <button type="button" className="btn btn-primary" onClick={() => onNavigate(selected.targetTab!, selected)}>{selected.actionLabel || 'Open related settings'}</button>}
            {selected.onSelect && <button type="button" className="btn btn-secondary" onClick={() => { onSourceSelect?.(selected); selected.onSelect?.() }}>Open related source</button>}
            {selected.severity !== 'blocker' && selected.severity !== 'error' && <button type="button" className="btn btn-secondary" onClick={() => setReviewed(current => { const next = new Set(current); if (next.has(selected.id)) next.delete(selected.id); else next.add(selected.id); return next })}>{reviewed.has(selected.id) ? 'Remove reviewed mark' : selected.severity === 'info' ? 'Mark reviewed' : 'Mark reviewed with limitation'}</button>}
          </div>
          <section className="shared-import-issue-impact" aria-label="Problem and impact">
            <h5>Why this matters</h5>
            <p>{selected.impact || selected.occurrences[0]?.description || 'The importer found a behavior that needs review before controlled replay.'}</p>
          </section>
          <section className="shared-import-issue-resolution" aria-label="Available resolution">
            <h5>Available resolution</h5>
            <p>{selected.resolution || (selected.targetTab && onNavigate
              ? selected.targetTab === 'credentials' ? 'Review credential handling in Credentials, then verify the affected request before replay.'
                : selected.targetTab === 'data' ? 'Review imported data producers and bindings in Data & parameters. Edit a value if the draft cannot produce what the source requires.'
                : selected.targetTab === 'runtime' ? 'Compare imported and draft runtime values, apply a supported edit, and recheck replay readiness.'
                : 'Inspect the source mapping and affected locations. Correct unsupported source behavior where possible, then inspect again.'
              : selected.severity === 'blocker' || selected.severity === 'error'
                ? 'Correct the source or configuration, then inspect it again. This blocker cannot be cleared by a review mark.'
                : 'Review the source evidence and verify the intended behavior before replay. A review mark records your assessment for this session only.')}</p>
          </section>
          <details className="shared-import-issue-evidence-details">
            <summary>Technical evidence and affected locations ({selected.occurrences.length})</summary>
          <div className="shared-import-issue-evidence">
            {selected.occurrences.slice(evidencePage * evidencePageSize, (evidencePage + 1) * evidencePageSize).map((occurrence, index) => <section key={occurrence.id}>
              <strong>{occurrence.source || `Occurrence ${evidencePage * evidencePageSize + index + 1}`}</strong>
              {occurrence.description && <p>{occurrence.description}</p>}
            </section>)}
          </div>
          </details>
          {selected.occurrences.length > evidencePageSize && <nav className="shared-import-issue-pagination" aria-label="Issue evidence pages">
            <span>Showing {evidencePage * evidencePageSize + 1}–{Math.min((evidencePage + 1) * evidencePageSize, selected.occurrences.length)} of {selected.occurrences.length}</span>
            <button type="button" className="btn btn-secondary" disabled={evidencePage === 0} onClick={() => setEvidencePage(page => Math.max(0, page - 1))}>Previous 100</button>
            <button type="button" className="btn btn-secondary" disabled={(evidencePage + 1) * evidencePageSize >= selected.occurrences.length} onClick={() => setEvidencePage(page => page + 1)}>Next 100</button>
          </nav>}
        </> : <p className="import-review-empty">Select an issue to inspect its evidence and next action.</p>}
      </article>
    </div>
  </section>
}
