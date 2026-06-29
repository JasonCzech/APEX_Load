import { useEffect, useMemo, useRef, useState } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'
import type { RunArchiveReportProfile } from '../types'
import './GenerateReportModal.css'

// Known built-in profiles, while still accepting any backend-supplied id. The
// `(string & {})` intersection keeps literal autocomplete without collapsing the
// union to plain `string`.
export type ReportProfileId = 'executive' | 'engineer' | 'audit' | (string & {})

interface ReportTypeOption {
  id: ReportProfileId
  label: string
  blurb: string
  bestFor?: string
}

interface Props {
  profiles?: RunArchiveReportProfile[]
  onClose: () => void
  onGenerate: (profile: ReportProfileId) => void
}

const FALLBACK_REPORT_TYPES: ReportTypeOption[] = [
  {
    id: 'executive',
    label: 'Executive Summary',
    blurb: 'High-level verdict, key metrics, and release decision narrative for stakeholders.',
  },
  {
    id: 'engineer',
    label: 'Detailed Technical',
    blurb: 'Full drilldown with per-action stats, timelines, errors, resources, and diagnostics.',
  },
  {
    id: 'audit',
    label: 'Audit',
    blurb: 'Governance evidence, provenance, completeness, and artifact integrity.',
  },
]

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
    <div className="generate-report-backdrop" role="presentation" onMouseDown={onClose}>
      <div ref={dialogRef} className="generate-report-modal" role="dialog" aria-modal="true" aria-label="Generate report" onMouseDown={(event) => event.stopPropagation()}>
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

export function buildReportTypeOptions(profiles?: RunArchiveReportProfile[]): ReportTypeOption[] {
  const htmlProfiles = (profiles || []).filter((profile) => profile.export_formats?.includes('html'))
  if (htmlProfiles.length === 0) return FALLBACK_REPORT_TYPES
  return htmlProfiles.map((profile) => ({
    id: profile.id,
    label: profile.label,
    blurb: profile.description,
    bestFor: profile.best_for || profile.audience,
  }))
}
