import { type ReactNode } from 'react'
import './ImportConversionDetails.css'

export interface ImportEvidenceFact {
  label: string
  value: ReactNode
  detail?: ReactNode
}

/** Readable label/value pairs for source accounting, not readiness indicators. */
export function ImportEvidenceFacts({ facts }: { facts: ImportEvidenceFact[] }) {
  if (!facts.length) return null
  return (
    <dl className="import-evidence-facts">
      {facts.map(fact => (
        <div key={fact.label}>
          <dt>{fact.label}</dt>
          <dd>{fact.value}{fact.detail && <small>{fact.detail}</small>}</dd>
        </div>
      ))}
    </dl>
  )
}

export function ImportConversionDetails({
  facts,
  children,
  title = 'Conversion reference',
  description = 'Counts from the original conversion. These describe the imported source, not replay readiness.',
}: {
  facts: ImportEvidenceFact[]
  children?: ReactNode
  title?: string
  description?: string
}) {
  if (!facts.length && !children) return null
  return (
    <details className="import-conversion-details">
      <summary>{title}</summary>
      <div className="import-conversion-details-body">
        <p>{description}</p>
        <ImportEvidenceFacts facts={facts} />
        {children}
      </div>
    </details>
  )
}
