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
    <div className="script-import-review-preview">
      <h4>{previewTitle}</h4>
      <pre>{JSON.stringify(previewData ?? { script }, null, 2)}</pre>
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

      {previewAside ? (
        <div className="script-import-review-preview-row">
          {previewPane}
          <div className="script-import-review-preview-aside">
            {previewAside}
          </div>
        </div>
      ) : previewPane}
    </div>
  )
}
