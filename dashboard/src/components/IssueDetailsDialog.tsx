import DialogLayer from './ux/DialogLayer'
import { useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
import ImportIssueSeverityTag from './ImportIssueSeverityTag'
import './IssueDetailsDialog.css'

export type IssueDetailSeverity = 'blocker' | 'error' | 'warning' | 'info'

export interface IssueDetail {
  id: string
  severity: IssueDetailSeverity
  title: string
  description?: string
  count?: number
  source?: string
  onSelect?: () => void
  ownsDestinationFocus?: boolean
}

interface Props {
  title: string
  issues: IssueDetail[]
  onClose: () => void
}

const ISSUE_PAGE_SIZE = 100

export default function IssueDetailsDialog({ title, issues, onClose }: Props) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const destinationOwnsFocusRef = useRef(false)
  const titleId = useId()
  const descriptionId = useId()
  const pageStatusId = useId()
  const [page, setPage] = useState(0)
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose, shouldRestoreFocus: () => !destinationOwnsFocusRef.current })
  useEffect(() => setPage(0), [issues, title])
  const pageCount = Math.max(1, Math.ceil(issues.length / ISSUE_PAGE_SIZE))
  const safePage = Math.min(page, pageCount - 1)
  const pageStart = safePage * ISSUE_PAGE_SIZE
  const visibleIssues = issues.slice(pageStart, pageStart + ISSUE_PAGE_SIZE)

  return createPortal((
    <DialogLayer><div className="modal-overlay issue-details-overlay" {...dialogBackdropProps(onClose)}>
      <div
        ref={dialogRef}
        className="modal issue-details-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <div className="modal-header issue-details-header">
          <div>
            <h3 id={titleId}>{title}</h3>
            <p id={descriptionId} className="page-subtitle">
              {issues.length} specific issue{issues.length === 1 ? '' : 's'} require attention.
            </p>
          </div>
          <button ref={closeRef} className="btn btn-sm btn-ghost" type="button" onClick={onClose}>
            Close
          </button>
        </div>
        <div
          className="issue-details-list"
          role="region"
          aria-label={`${title}, items ${issues.length === 0 ? 0 : pageStart + 1} through ${pageStart + visibleIssues.length} of ${issues.length}`}
          aria-describedby={pageStatusId}
          tabIndex={0}
        >
          {visibleIssues.map(issue => {
            const content = (
              <>
                <ImportIssueSeverityTag severity={issue.severity} />
                <span className="issue-details-copy">
                  <strong>{issue.title}</strong>
                  {issue.description && <span>{issue.description}</span>}
                  {issue.source && <small>{issue.source}</small>}
                </span>
                {issue.count !== undefined && <span className="issue-details-count">{issue.count}</span>}
              </>
            )
            return issue.onSelect ? (
              <button
                key={issue.id}
                type="button"
                className="issue-details-item selectable"
                onClick={() => {
                  destinationOwnsFocusRef.current = issue.ownsDestinationFocus === true
                  onClose()
                  issue.onSelect?.()
                }}
              >
                {content}
              </button>
            ) : (
              <div key={issue.id} className="issue-details-item">{content}</div>
            )
          })}
        </div>
        <nav className="issue-details-pagination" aria-label={`${title} pages`}>
          <span id={pageStatusId} aria-live="polite">
            {issues.length === 0
              ? 'No issues to display.'
              : `Showing ${pageStart + 1}–${pageStart + visibleIssues.length} of ${issues.length}. Page ${safePage + 1} of ${pageCount}.`}
          </span>
          {pageCount > 1 && (
            <div role="group" aria-label="Issue page controls">
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={safePage === 0}
                onClick={() => setPage(Math.max(0, safePage - 1))}
              >
                Previous {ISSUE_PAGE_SIZE}
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                disabled={safePage >= pageCount - 1}
                onClick={() => setPage(Math.min(pageCount - 1, safePage + 1))}
              >
                Next {ISSUE_PAGE_SIZE}
              </button>
            </div>
          )}
        </nav>
      </div>
    </div></DialogLayer>
  ), document.body)
}
