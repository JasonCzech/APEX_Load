import { type ReactNode } from 'react'

import { type ImportReviewSection } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import './ScriptImportReview.css'

const severityRank: Record<string, number> = {
  blocker: 0,
  warning: 1,
  info: 2,
}

interface Props {
  title: string
  subtitle?: ReactNode
  script: ScriptDefinition
  previewData?: unknown
  warnings?: string[]
  warningsTitle?: string
  unsupportedElements?: string[]
  reviewSections?: ImportReviewSection[]
  saving?: boolean
  saveDisabled?: boolean
  saveLabel?: string
  editLabel?: string
  previewTitle?: string
  previewAside?: ReactNode
  onSave?: () => void
  onEdit?: () => void
  children?: ReactNode
}

export default function ScriptImportReview({
  title,
  subtitle,
  script,
  previewData,
  warnings = [],
  warningsTitle = 'Warnings',
  unsupportedElements = [],
  reviewSections = [],
  saving = false,
  saveDisabled = false,
  saveLabel = 'Save To Script Library',
  editLabel = 'Open In Visual Editor',
  previewTitle = 'Generated Script Preview',
  previewAside,
  onSave,
  onEdit,
  children,
}: Props) {
  const previewPane = (
    <details className="script-import-review-preview" open>
      <summary>
        <h4>{previewTitle}</h4>
      </summary>
      <pre>{JSON.stringify(previewData ?? { script }, null, 2)}</pre>
    </details>
  )
  const sortedReviewSections = [...reviewSections].sort((a, b) => {
    const left = severityRank[String(a.severity || '').toLowerCase()] ?? 3
    const right = severityRank[String(b.severity || '').toLowerCase()] ?? 3
    if (left !== right) return left - right
    return a.title.localeCompare(b.title)
  })

  return (
    <div className="script-import-review">
      <div className="script-import-review-header">
        <div>
          <h3>{title}</h3>
          {subtitle && <p>{subtitle}</p>}
        </div>
        <div className="script-import-review-actions">
          {onEdit && <button className="btn btn-secondary" onClick={onEdit}>{editLabel}</button>}
          {onSave && <button className="btn btn-primary" onClick={onSave} disabled={saving || saveDisabled}>{saving ? 'Saving...' : saveLabel}</button>}
        </div>
      </div>

      {children}

      {unsupportedElements.length > 0 && (
        <div className="script-import-review-unsupported">
          <h4>Unsupported Elements ({unsupportedElements.length})</h4>
          <ul>
            {unsupportedElements.map((item, index) => <li key={`${item}-${index}`}>{item}</li>)}
          </ul>
        </div>
      )}

      {sortedReviewSections.length > 0 && (
        <div className="script-import-review-sections">
          <h4>Import Review ({sortedReviewSections.length})</h4>
          <div className="script-import-review-section-list">
            {sortedReviewSections.map(section => (
              <details key={section.key} className={`script-import-review-section severity-${section.severity || 'info'}`} open={section.severity === 'blocker' || section.severity === 'warning'}>
                <summary>
                  <span>{section.title}</span>
                  <small>{section.severity}</small>
                </summary>
                {section.summary && <p>{section.summary}</p>}
                {section.items.length > 0 && (
                  <ul>
                    {section.items.map((item, index) => <li key={`${section.key}-${index}`}>{item}</li>)}
                  </ul>
                )}
              </details>
            ))}
          </div>
        </div>
      )}

      {warnings.length > 0 && (
        <div className="script-import-review-warnings">
          <h4>{warningsTitle}</h4>
          <ul>
            {warnings.map((warning, index) => <li key={index}>{warning}</li>)}
          </ul>
        </div>
      )}

      {previewPane}
      {previewAside && (
        <div className="script-import-review-preview-aside">
          {previewAside}
        </div>
      )}
    </div>
  )
}
