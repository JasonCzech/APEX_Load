import { useState } from 'react'

import type { ReportLens } from '../../appRouting'
import { downloadBlob } from '../../utils/fileUtils'
import { REPORT_LENS_LABELS } from './reportLensLabels'

interface Props {
  testId: string
  lens: ReportLens
  testName?: string
  downloadHTML: (id: string, lens: string) => Promise<Blob>
  downloadFormat: (id: string, lens: string, format: 'pdf' | 'csv' | 'docx' | 'analysis_zip') => Promise<Blob>
}

const formats = [
  { id: 'html', label: 'Navigable HTML', description: 'Browse sections and charts offline.' },
  { id: 'pdf', label: 'Print-ready PDF', description: 'Share a paged review copy.' },
  { id: 'docx', label: 'Editable Word', description: 'Add review notes and sign-off.' },
  { id: 'csv', label: 'Data CSV', description: 'Use section and metric values in other tools.' },
  { id: 'analysis_zip', label: 'Analysis tables (ZIP)', description: 'Download row-based CSV tables and a data guide.' },
] as const

function safeName(value: string | undefined, fallback: string) {
  return (value || fallback).trim().replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || fallback
}

export default function RunReportExportActions({ testId, lens, testName, downloadHTML, downloadFormat }: Props) {
  const [busy, setBusy] = useState<string>('')
  const [error, setError] = useState('')

  const exportReport = async (format: 'html' | 'pdf' | 'csv' | 'docx' | 'analysis_zip') => {
    setBusy(format)
    setError('')
    try {
      const blob = format === 'html'
        ? await downloadHTML(testId, lens)
        : await downloadFormat(testId, lens, format)
      await downloadBlob(blob, `${safeName(testName, testId)}-${lens}.${format === 'analysis_zip' ? 'zip' : format}`)
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Unable to export report')
    } finally {
      setBusy('')
    }
  }

  return (
    <section className="run-report-panel run-report-export">
      <div className="run-report-section-heading">
        <div>
          <h2>Export</h2>
          <p>Download the {REPORT_LENS_LABELS[lens]} report in the format your audience reviews it in.</p>
        </div>
      </div>
      <div className="run-report-export-actions">
        {formats.map(format => (
          <button
            key={format.id}
            className="run-report-export-option"
            type="button"
            onClick={() => void exportReport(format.id)}
            disabled={Boolean(busy)}
          >
            <strong>{busy === format.id ? 'Exporting…' : format.label}</strong>
            <span>{format.description}</span>
          </button>
        ))}
      </div>
      {error && <p className="run-report-error" role="alert">{error}</p>}
    </section>
  )
}
