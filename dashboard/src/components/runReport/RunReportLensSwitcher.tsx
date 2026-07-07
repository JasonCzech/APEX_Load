import { REPORT_LENSES, type ReportLens } from '../../appRouting'

const LENS_LABELS: Record<ReportLens, string> = {
  executive: 'Executive',
  engineer: 'Engineer',
  audit: 'Audit',
}

interface Props {
  activeLens: ReportLens
  onLensChange: (lens: ReportLens) => void
}

export default function RunReportLensSwitcher({ activeLens, onLensChange }: Props) {
  return (
    <div className="run-report-lens" role="tablist" aria-label="Report audience lens">
      {REPORT_LENSES.map(lens => (
        <button
          key={lens}
          type="button"
          role="tab"
          aria-selected={activeLens === lens}
          className={`run-report-lens-tab ${activeLens === lens ? 'active' : ''}`}
          onClick={() => onLensChange(lens)}
        >
          {LENS_LABELS[lens]}
        </button>
      ))}
    </div>
  )
}
