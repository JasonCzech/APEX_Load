import { type ReactNode } from 'react'

import { type ImportedScript } from '../types/scriptImports'

interface Props<TScript extends ImportedScript = ImportedScript> {
  title: string
  subtitle?: ReactNode
  script: TScript
  previewData?: unknown
  warnings?: string[]
  warningsTitle?: string
  saving?: boolean
  saveLabel?: string
  editLabel?: string
  previewTitle?: string
  onSave?: () => void
  onEdit?: () => void
  children?: ReactNode
}

export default function ScriptImportReview<TScript extends ImportedScript = ImportedScript>({
  title,
  subtitle,
  script,
  previewData,
  warnings = [],
  warningsTitle = 'Warnings',
  saving = false,
  saveLabel = 'Save To Script Library',
  editLabel = 'Open In Visual Editor',
  previewTitle = 'Generated Script Preview',
  onSave,
  onEdit,
  children,
}: Props<TScript>) {
  return (
    <div className="script-import-review">
      <div className="script-import-review-header">
        <div>
          <h3>{title}</h3>
          {subtitle && <p>{subtitle}</p>}
        </div>
        <div className="script-import-review-actions">
          {onEdit && <button className="btn btn-secondary" onClick={onEdit}>{editLabel}</button>}
          {onSave && <button className="btn btn-primary" onClick={onSave} disabled={saving}>{saving ? 'Saving...' : saveLabel}</button>}
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

      <div className="script-import-review-preview">
        <h4>{previewTitle}</h4>
        <pre>{JSON.stringify(previewData ?? { script }, null, 2)}</pre>
      </div>
    </div>
  )
}