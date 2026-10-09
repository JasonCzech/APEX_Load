import { useEffect, useState } from 'react'

import type { ReportAnnotations, RunArchiveReport } from '../../types'
import { effectiveReportVerdict } from './runReportModel'
import { REPORT_LENS_LABELS } from './reportLensLabels'
import type { ReportLens } from '../../appRouting'

interface Props {
  report: RunArchiveReport | null
  annotations: ReportAnnotations | null
  status: string
  explanation: string
  saving: boolean
  disabled?: boolean
  onStatusChange: (status: string) => void
  onExplanationChange: (explanation: string) => void
  onSaveOverride: (status: string, explanation: string) => Promise<boolean>
}

function verdictProvenance(annotations: ReportAnnotations | null): string {
  const shared = 'One assessment per run: it appears in every report audience and export.'
  if (!annotations?.verdict_override) return shared
  const author = annotations.verdict_updated_by || annotations.updated_by
  const audience = annotations.verdict_profile ? REPORT_LENS_LABELS[annotations.verdict_profile as ReportLens] ?? annotations.verdict_profile : ''
  const when = annotations.verdict_updated_at ? new Date(annotations.verdict_updated_at).toLocaleString() : ''
  const setBy = [author && `by ${author}`, audience && `from the ${audience} report`, when && `on ${when}`].filter(Boolean).join(' ')
  return setBy ? `Set ${setBy}. ${shared}` : shared
}

function verdictTone(verdict: ReturnType<typeof effectiveReportVerdict>) {
  if (verdict.failed) return 'fail'
  if (verdict.status !== 'Pass') return 'conditional'
  return 'pass'
}

export default function RunReportVerdictBanner({
  report,
  annotations,
  status,
  explanation,
  saving,
  disabled = false,
  onStatusChange,
  onExplanationChange,
  onSaveOverride,
}: Props) {
  const verdict = effectiveReportVerdict(report, annotations)
  const [message, setMessage] = useState('')
  const controlsDisabled = saving || disabled

  useEffect(() => {
    setMessage('')
  }, [explanation, status])

  const save = async () => {
    setMessage('')
    // Only confirm "Saved" when the override actually persisted; the parent
    // surfaces the failure message on error.
    if (await onSaveOverride(status, explanation)) {
      setMessage('Saved')
    }
  }

  return (
    <section className={`run-report-verdict run-report-verdict-${verdictTone(verdict)}`}>
      <div className="run-report-verdict-main">
        <div>
          <p className="run-report-kicker">Computed acceptance</p>
          <h1>{verdict.status}</h1>
          <p>{verdict.explanation}</p>
        </div>
        <div className="run-report-verdict-badge" aria-label={`Verdict ${verdict.status}`}>
          {verdict.status}
        </div>
      </div>
      <details className="run-report-override">
        <summary>Analyst assessment — separate from computed acceptance</summary>
        <div className="run-report-override-grid">
          <label>
            <span>Status</span>
            <select value={status} onChange={event => onStatusChange(event.currentTarget.value)} disabled={controlsDisabled}>
              <option value="">No analyst assessment</option>
              <option value="Pass">Pass</option>
              <option value="Conditional Pass">Conditional Pass</option>
              <option value="Fail">Fail</option>
            </select>
          </label>
          <label>
            <span>Explanation</span>
            <textarea value={explanation} onChange={event => onExplanationChange(event.currentTarget.value)} rows={3} disabled={controlsDisabled} />
          </label>
          <p className="run-report-muted run-report-override-provenance">{verdictProvenance(annotations)}</p>
          <div className="run-report-override-actions">
            <button className="btn btn-sm btn-primary" type="button" onClick={() => void save()} disabled={controlsDisabled}>
              {saving ? 'Saving…' : 'Save'}
            </button>
            {disabled && !saving && <span role="status">Annotations unavailable</span>}
            {message && <span role="status">{message}</span>}
          </div>
        </div>
      </details>
    </section>
  )
}
