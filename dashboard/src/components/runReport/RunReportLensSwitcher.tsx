import { REPORT_LENSES, type ReportLens } from '../../appRouting'
import { REPORT_LENS_LABELS } from './reportLensLabels'

interface Props {
  activeLens: ReportLens
  onLensChange: (lens: ReportLens) => void
}

// The audience picks which report profile exports and commentary use. It does
// not filter the page, so it is a labelled toggle group, not a set of tabs.
export default function RunReportLensSwitcher({ activeLens, onLensChange }: Props) {
  return (
    <div className="run-report-audience">
      <span id="run-report-audience-label" className="run-report-audience-label">Report audience</span>
      <div className="run-report-lens" role="group" aria-labelledby="run-report-audience-label" aria-describedby="run-report-audience-hint">
        {REPORT_LENSES.map(lens => (
          <button
            key={lens}
            type="button"
            aria-pressed={activeLens === lens}
            className={`run-report-lens-tab ${activeLens === lens ? 'active' : ''}`}
            onClick={() => onLensChange(lens)}
          >
            {REPORT_LENS_LABELS[lens]}
          </button>
        ))}
      </div>
      <span id="run-report-audience-hint" className="run-report-audience-hint">Affects exports and report commentary</span>
    </div>
  )
}
