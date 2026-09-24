import { useEffect, useMemo, useRef, useState } from 'react'

import type { IssueDetailSeverity } from './IssueDetailsDialog'
import type { ImportReviewTab } from './ImportReviewTabs'
import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { groupImportWorkspaceIssues, type ImportWorkspaceIssue } from './importIssueWorkspaceModel'
import './IssueDetailsDialog.css'
const evidencePageSize = 100

export default function ImportIssueWorkspace({ issues, onNavigate, normalizedEvidence = false }: {
  normalizedEvidence?: boolean
  issues: ImportWorkspaceIssue[]
  onNavigate?: (tab: ImportReviewTab) => void
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
  const selected = visible.find(issue => issue.id === selectedId) || visible[0]
  const blockers = grouped.filter(issue => issue.severity === 'blocker' || issue.severity === 'error').length
  const warnings = grouped.filter(issue => issue.severity === 'warning').length
  const reviewedCount = grouped.filter(issue => reviewed.has(issue.id)).length
  const nextIssue = visible.find(issue => !reviewed.has(issue.id) && issue.id !== selected?.id)

  const selectedVersion = selected ? `${selected.id}:${selected.occurrences.length}:${selected.occurrences[0]?.description || ''}` : ''
  useEffect(() => {
    setEvidencePage(0)
    if (selectedVersion) detailHeadingRef.current?.focus()
  }, [selectedVersion])

  if (grouped.length === 0) return null
  return <section className="shared-import-issue-workspace" aria-label="Resolve import issues">
    <ImportReviewSectionHeader
      title="Resolve import issues"
      description={normalizedEvidence ? `${grouped.length} findings · ${blockers} blocking · ${warnings} warnings. Informational findings do not block replay.` : `${grouped.length} grouped issue${grouped.length === 1 ? '' : 's'} · ${blockers} blocking · ${warnings} warning${warnings === 1 ? '' : 's'}. Repeated findings are grouped by the operator decision they require.`}
      actions={nextIssue && <button type="button" className="btn btn-primary" onClick={() => setSelectedId(nextIssue.id)}>Next unresolved issue</button>}
    />
    <div className="import-review-toolbar">
      <label>Find an issue<input type="search" value={search} placeholder="Search evidence or source locations" onChange={event => setSearch(event.target.value)} /></label>
      <label>Show<select value={filter} onChange={event => setFilter(event.target.value as typeof filter)}><option value="all">{normalizedEvidence ? 'All findings' : 'All issues'} ({grouped.length})</option><option value="blocker">Blockers</option><option value="error">Errors</option><option value="warning">Warnings</option><option value="info">Review notes</option><option value="reviewed">Reviewed with limitation ({reviewedCount})</option></select></label>
      <span role="status">{visible.length} matching {normalizedEvidence ? 'findings' : 'issues'}</span>
    </div>
    <div className="shared-import-issue-layout">
      <div className="shared-import-issue-list" aria-label="Import issue queue">
        {visible.length === 0 && <p className="import-review-empty">No issues match this filter.</p>}
        {visible.map((issue, index) => <button
          key={issue.id}
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
          <span className={`issue-details-severity ${issue.severity}`}>{issue.severity}</span>
          <span><strong>{issue.title}</strong><small>{issue.occurrences.length} occurrence{issue.occurrences.length === 1 ? '' : 's'}{reviewed.has(issue.id) ? ' · reviewed' : ''}</small></span>
        </button>)}
      </div>
      <article className="shared-import-issue-detail" aria-label="Selected issue">
        {selected ? <>
          <div className="shared-import-issue-detail-heading">
            <span className={`issue-details-severity ${selected.severity}`}>{selected.severity}</span>
            <div><h4 ref={detailHeadingRef} tabIndex={-1}>{selected.title}</h4><p>{selected.occurrences.length} affected location{selected.occurrences.length === 1 ? '' : 's'}</p></div>
          </div>
          <div className="shared-import-issue-actions">
            {selected.targetTab && onNavigate && <button type="button" className="btn btn-primary" onClick={() => onNavigate(selected.targetTab!)}>{selected.actionLabel || 'Open related settings'}</button>}
            {selected.onSelect && <button type="button" className="btn btn-secondary" onClick={selected.onSelect}>Open related source</button>}
            <button type="button" className="btn btn-secondary" onClick={() => setReviewed(current => { const next = new Set(current); if (next.has(selected.id)) next.delete(selected.id); else next.add(selected.id); return next })}>{reviewed.has(selected.id) ? 'Remove reviewed mark' : 'Mark reviewed with limitation'}</button>
          </div>
          <div className="shared-import-issue-evidence">
            {selected.occurrences.slice(evidencePage * evidencePageSize, (evidencePage + 1) * evidencePageSize).map((occurrence, index) => <section key={occurrence.id}>
              <strong>{occurrence.source || `Occurrence ${evidencePage * evidencePageSize + index + 1}`}</strong>
              {occurrence.description && <p>{occurrence.description}</p>}
            </section>)}
          </div>
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
