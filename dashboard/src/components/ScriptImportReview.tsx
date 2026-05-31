import { type ReactNode } from 'react'

import { type ScriptDefinition } from '../types/scripts'
import './ScriptImportReview.css'

interface Props {
  title: string
  subtitle?: ReactNode
  script: ScriptDefinition
  previewData?: unknown
  warnings?: string[]
  warningsTitle?: string
  saving?: boolean
  saveDisabled?: boolean
  saveLabel?: string
  editLabel?: string
  previewTitle?: string
  previewMode?: 'panel' | 'details'
  previewAside?: ReactNode
  previewAsideLayout?: 'side' | 'stack'
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
  saving = false,
  saveDisabled = false,
  saveLabel = 'Save To Script Library',
  editLabel = 'Open In Visual Editor',
  previewTitle = 'Generated Script Preview',
  previewMode = 'panel',
  previewAside,
  previewAsideLayout = 'side',
  onSave,
  onEdit,
  children,
}: Props) {
  const previewContent = <pre>{JSON.stringify(previewData ?? { script }, null, 2)}</pre>
  const previewPane = previewMode === 'details' ? (
    <details className="script-import-review-preview">
      <summary>{previewTitle}</summary>
      {previewContent}
    </details>
  ) : (
    <div className="script-import-review-preview">
      <h4>{previewTitle}</h4>
      {previewContent}
    </div>
  )

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

      {warnings.length > 0 && (
        <div className="script-import-review-warnings">
          <h4>{warningsTitle}</h4>
          <ul>
            {warnings.map((warning, index) => <li key={index}>{warning}</li>)}
          </ul>
        </div>
      )}

      {previewAside && previewAsideLayout === 'side' ? (
        <div className="script-import-review-preview-row">
          {previewPane}
          <div className="script-import-review-preview-aside">
            {previewAside}
          </div>
        </div>
      ) : (
        <>
          {previewPane}
          {previewAside}
        </>
      )}
    </div>
  )
}
