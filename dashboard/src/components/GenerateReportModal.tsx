import { useEffect, useMemo, useRef, useState } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'
import type { RunArchiveReportProfile } from '../types'
import { buildReportTypeOptions, type ReportProfileId } from './reportTypeOptions'
import { dialogBackdropProps } from './dialogBackdropProps'
import './GenerateReportModal.css'

// Known built-in profiles, while still accepting any backend-supplied id. The
// `(string & {})` intersection keeps literal autocomplete without collapsing the
// union to plain `string`.

interface Props {
  profiles?: RunArchiveReportProfile[]
  onClose: () => void
  onGenerate: (profile: ReportProfileId) => void
}


export default function GenerateReportModal({ profiles, onClose, onGenerate }: Props) {
  const reportTypes = useMemo(() => buildReportTypeOptions(profiles), [profiles])
  const [selected, setSelected] = useState<ReportProfileId>(reportTypes[0]?.id || 'executive')
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose })

  useEffect(() => {
    if (!reportTypes.some((option) => option.id === selected)) {
      setSelected(reportTypes[0]?.id || 'executive')
    }
  }, [reportTypes, selected])

  return (
    <div className="generate-report-backdrop" {...dialogBackdropProps(onClose, 'Close report generator')}>
      <div ref={dialogRef} className="generate-report-modal" role="dialog" aria-modal="true" aria-label="Generate report">
        <div className="generate-report-header">
          <h3>Generate Report</h3>
          <button type="button" className="generate-report-close" onClick={onClose} aria-label="Close">×</button>
        </div>
        <p className="generate-report-intro">Choose the report type. You can edit the commentary and export to PDF or HTML in the next step.</p>
        <div className="generate-report-options">
          {reportTypes.map((option) => (
            <button
              key={option.id}
              type="button"
              className={`generate-report-option${selected === option.id ? ' selected' : ''}`}
              onClick={() => setSelected(option.id)}
              aria-pressed={selected === option.id}
            >
              <span className="generate-report-option-label">{option.label}</span>
              <span className="generate-report-option-blurb">{option.blurb}</span>
              {option.bestFor && <span className="generate-report-option-blurb">{option.bestFor}</span>}
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
