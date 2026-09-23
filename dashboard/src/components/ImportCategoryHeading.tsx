import { useId, useState, type ReactNode } from 'react'
import './ImportSettingsTables.css'

/** Keep category actions available independently of its optional source explanation. */
export default function ImportCategoryHeading({ label, details, actions }: {
  label: string
  details?: ReactNode
  actions?: ReactNode
}) {
  const [expanded, setExpanded] = useState(false)
  const detailsId = useId()
  return <div className="import-category-heading">
    <div className="import-category-heading-bar">
      {details ? <button type="button" className="import-category-toggle" aria-label={`${label} details`} aria-expanded={expanded} aria-controls={detailsId} onClick={() => setExpanded(value => !value)}>
        <span aria-hidden="true">{expanded ? '▾' : '▸'}</span> {label}
      </button> : <span>{label}</span>}
      {actions}
    </div>
    {details && <div id={detailsId} className="import-category-details" hidden={!expanded}>{details}</div>}
  </div>
}
