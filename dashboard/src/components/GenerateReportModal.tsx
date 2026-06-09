import { useEffect, useState } from 'react'
import './GenerateReportModal.css'

export type ReportProfileId = 'executive' | 'engineer'

interface ReportTypeOption {
  id: ReportProfileId
  label: string
  blurb: string
}

const REPORT_TYPES: ReportTypeOption[] = [
  {
    id: 'executive',
    label: 'Executive Summary',
    blurb: 'High-level verdict, key metrics, supporting graphs, and a narrative for stakeholders and sign-off.',
  },
  {
    id: 'engineer',
    label: 'Detailed Technical',
    blurb: 'Full drilldown — per-action stats, timelines, errors, resource peaks, and graphs for engineers.',
  },
]

interface Props {
  onClose: () => void
  onGenerate: (profile: ReportProfileId) => void
}

export default function GenerateReportModal({ onClose, onGenerate }: Props) {
  const [selected, setSelected] = useState<ReportProfileId>('executive')

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  return (
    <div className="generate-report-backdrop" role="presentation" onMouseDown={onClose}>
      <div className="generate-report-modal" role="dialog" aria-modal="true" aria-label="Generate report" onMouseDown={(event) => event.stopPropagation()}>
        <div className="generate-report-header">
          <h3>Generate Report</h3>
          <button type="button" className="generate-report-close" onClick={onClose} aria-label="Close">×</button>
        </div>
        <p className="generate-report-intro">Choose the report type. You can edit the commentary and export to PDF or HTML in the next step.</p>
        <div className="generate-report-options">
          {REPORT_TYPES.map((option) => (
            <button
              key={option.id}
              type="button"
              className={`generate-report-option${selected === option.id ? ' selected' : ''}`}
              onClick={() => setSelected(option.id)}
              aria-pressed={selected === option.id}
            >
              <span className="generate-report-option-label">{option.label}</span>
              <span className="generate-report-option-blurb">{option.blurb}</span>
            </button>
          ))}
        </div>
        <div className="generate-report-actions">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Cancel</button>
          <button type="button" className="btn btn-primary" onClick={() => onGenerate(selected)}>Open Report</button>
        </div>
      </div>
    </div>
  )
}
