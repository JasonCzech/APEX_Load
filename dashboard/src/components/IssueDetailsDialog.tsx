import { useId, useRef } from 'react'
import { createPortal } from 'react-dom'

import { useModalDialog } from '../hooks/useModalDialog'
import { dialogBackdropProps } from './dialogBackdropProps'
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
}

interface Props {
  title: string
  issues: IssueDetail[]
  onClose: () => void
}

export default function IssueDetailsDialog({ title, issues, onClose }: Props) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const closeRef = useRef<HTMLButtonElement | null>(null)
  const titleId = useId()
  const descriptionId = useId()
  useModalDialog({ dialogRef, initialFocusRef: closeRef, onClose })

  return createPortal((
    <div className="modal-overlay issue-details-overlay" {...dialogBackdropProps(onClose, `Close ${title}`)}>
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
        <div className="issue-details-list">
          {issues.map(issue => {
            const content = (
              <>
                <span className={`issue-details-severity ${issue.severity}`}>{issue.severity}</span>
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
      </div>
    </div>
  ), document.body)
}
