import PercentileQualityMark from '../PercentileQualityMark'
import type { ReportKpi } from './runReportModel'

interface Props {
  kpis: ReportKpi[]
}

function sparklinePath(values: number[]): string {
  if (values.length < 2) return ''
  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  return values.map((value, index) => {
    const x = values.length === 1 ? 0 : (index / (values.length - 1)) * 96
    const y = 28 - ((value - min) / span) * 24
    return `${index === 0 ? 'M' : 'L'} ${x.toFixed(2)} ${y.toFixed(2)}`
  }).join(' ')
}

function Sparkline({ values }: { values: number[] }) {
  const path = sparklinePath(values)
  if (!path) return null
  return (
    <svg className="run-report-sparkline" viewBox="0 0 96 32" role="img" aria-label="Recent trend">
      <path d={path} fill="none" stroke="currentColor" strokeWidth="2" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

export default function RunReportKpiScorecard({ kpis }: Props) {
  return (
    <section className="run-report-kpis" aria-label="Run KPI scorecard">
      {kpis.map(kpi => (
        <article key={kpi.id} className={`run-report-kpi run-report-tone-${kpi.tone}`}>
          <div>
            <p>{kpi.label}</p>
            <strong><PercentileQualityMark quality={kpi.percentileQuality} />{kpi.value}</strong>
            {kpi.delta && <span className={`run-report-delta run-report-delta-${kpi.deltaTone || 'neutral'}`}>{kpi.delta}</span>}
          </div>
          <Sparkline values={kpi.sparkline} />
        </article>
      ))}
    </section>
  )
}
