import { useState } from 'react'

import type { ReportLens } from '../../appRouting'
import { downloadBlob } from '../../utils/fileUtils'

interface Props {
  testId: string
  lens: ReportLens
  testName?: string
  downloadHTML: (id: string, lens: string) => Promise<Blob>
  downloadFormat: (id: string, lens: string, format: 'pdf' | 'csv') => Promise<Blob>
}

function safeName(value: string | undefined, fallback: string) {
  return (value || fallback).trim().replace(/[^A-Za-z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || fallback
}

export default function RunReportExportActions({ testId, lens, testName, downloadHTML, downloadFormat }: Props) {
  const [busy, setBusy] = useState<string>('')
  const [error, setError] = useState('')

  const exportReport = async (format: 'html' | 'pdf' | 'csv') => {
    setBusy(format)
    setError('')
    try {
      const blob = format === 'html'
        ? await downloadHTML(testId, lens)
        : await downloadFormat(testId, lens, format)
      await downloadBlob(blob, `${safeName(testName, testId)}-${lens}.${format}`)
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
          <p>Use the same profile lens for rendered report files.</p>
        </div>
      </div>
      <div className="run-report-export-actions">
        {(['html', 'pdf', 'csv'] as const).map(format => (
          <button
            key={format}
            className="btn btn-sm btn-secondary"
            type="button"
            onClick={() => void exportReport(format)}
            disabled={Boolean(busy)}
          >
            {busy === format ? 'Exporting...' : format.toUpperCase()}
          </button>
        ))}
      </div>
      {error && <p className="run-report-error" role="alert">{error}</p>}
    </section>
  )
}
