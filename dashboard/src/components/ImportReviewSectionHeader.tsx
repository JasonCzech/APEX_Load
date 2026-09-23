import type { ReactNode } from 'react'
import './ImportReviewWorkspace.css'

export default function ImportReviewSectionHeader({ title, description, actions }: {
  title: string
  description?: ReactNode
  actions?: ReactNode
}) {
  return <div className="import-review-section-heading">
    <div><h3>{title}</h3>{description && <p>{description}</p>}</div>
    {actions && <div className="import-review-section-actions">{actions}</div>}
  </div>
}
