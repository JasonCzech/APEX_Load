import { Fragment, useMemo, useState } from 'react'

import type { RunArchiveReport } from '../../types'
import PercentileQualityMark from '../PercentileQualityMark'
import { groupBreakdown, groupBreakdownLabel, type GroupBreakdownStats } from './groupBreakdownModel'
import { populationLabels, type MetricPopulation } from './reportPresentation'
import { formatMsValue, formatNumber, formatPct, type ReportKpi } from './runReportModel'

interface Props {
  report: RunArchiveReport
  population: MetricPopulation
  /** The run's KPIs: the table's whole-run row carries their baseline deltas; the rest are run facts. */
  kpis: ReportKpi[]
}

// Groups start expanded while the whole table stays short.
const EXPANDED_ROW_LIMIT = 60
const COLUMNS = 9

const TABLE_KPIS = new Set(['avgLatency', 'p95', 'errorRate'])

function StatCells({ stats, deltas }: { stats?: GroupBreakdownStats; deltas?: Partial<Record<'avgLatency' | 'p95' | 'errorRate', ReportKpi>> }) {
  const delta = (kpi?: ReportKpi) => kpi?.delta ? <span className={`run-report-delta run-report-delta-${kpi.deltaTone || 'neutral'}`}>{kpi.delta}</span> : null
  const rate = stats?.average_rate_per_second
  return <>
    <td>{formatNumber(stats?.transactions, 0)}</td>
    <td>{formatNumber(stats?.errors, 0)}</td>
    <td>{formatPct(stats?.error_pct)}{delta(deltas?.errorRate)}</td>
    <td>{formatMsValue(stats?.avg_response_ms)}{delta(deltas?.avgLatency)}</td>
    <td>{formatMsValue(stats?.p90_ms)}</td>
    <td><PercentileQualityMark quality={stats?.percentile_quality} />{formatMsValue(stats?.p95_ms)}{delta(deltas?.p95)}</td>
    <td>{formatMsValue(stats?.max_response_ms)}</td>
    <td>{rate === undefined ? formatNumber(undefined) : formatNumber(rate, 2)}</td>
  </>
}

export default function RunReportGroupBreakdown({ report, population, kpis }: Props) {
  const breakdown = useMemo(() => groupBreakdown(report, population), [report, population])
  const itemCount = breakdown.groups.reduce((total, group) => total + group.items.length, 0)
  const [collapsed, setCollapsed] = useState<Set<string>>(() => itemCount > EXPANDED_ROW_LIMIT ? new Set(breakdown.groups.map(group => group.name)) : new Set())
  const [filter, setFilter] = useState('')
  const query = filter.trim().toLocaleLowerCase()
  const itemNoun = population === 'action' ? 'Request' : population === 'transaction' ? 'Transaction' : 'Record'
  const deltas = Object.fromEntries(kpis.filter(kpi => TABLE_KPIS.has(kpi.id)).map(kpi => [kpi.id, kpi]))
  const facts = kpis.filter(kpi => !TABLE_KPIS.has(kpi.id))
  const toggle = (name: string) => setCollapsed(current => {
    const next = new Set(current)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })
  const groups = breakdown.groups.map(group => ({
    ...group,
    items: query ? group.items.filter(item => item.name.toLocaleLowerCase().includes(query) || groupBreakdownLabel(group.name).toLocaleLowerCase().includes(query)) : group.items,
  })).filter(group => !query || group.items.length > 0)

  return (
    <section className="run-report-panel run-report-group-breakdown" aria-label="Results by group and transaction">
      <div className="run-report-section-heading">
        <div>
          <h2>Results by group and {itemNoun.toLowerCase()}</h2>
          <p>{populationLabels[population]} · whole captured run.{breakdown.ungrouped && itemCount > 0 ? ' This report has no per-group breakdown, so each row covers every group.' : ''}</p>
        </div>
        {itemCount > 15 && <div className="run-report-table-controls">
          <input type="search" autoComplete="off" aria-label={`Filter groups and ${itemNoun.toLowerCase()}s`} value={filter} onChange={event => setFilter(event.currentTarget.value)} placeholder={`Filter group or ${itemNoun.toLowerCase()}`} />
        </div>}
      </div>
      {facts.length > 0 && <dl className="run-report-group-breakdown-facts" aria-label="Run facts">
        {facts.map(kpi => <div key={kpi.id} className={`run-report-tone-${kpi.tone}`}>
          <dt>{kpi.label}</dt>
          <dd><PercentileQualityMark quality={kpi.percentileQuality} />{kpi.value}{kpi.delta && <span className={`run-report-delta run-report-delta-${kpi.deltaTone || 'neutral'}`}>{kpi.delta}</span>}</dd>
        </div>)}
      </dl>}
      <div className="run-report-table-wrap">
        <table className="run-report-table run-report-group-breakdown-table">
          <thead>
            <tr>
              <th scope="col">Group / {itemNoun.toLowerCase()}</th>
              <th scope="col">Count</th>
              <th scope="col">Errors</th>
              <th scope="col">Error rate</th>
              <th scope="col">Avg (ms)</th>
              <th scope="col">P90 (ms)</th>
              <th scope="col">P95 (ms)</th>
              <th scope="col">Max (ms)</th>
              <th scope="col">Rate (/s)</th>
            </tr>
          </thead>
          <tbody>
            <tr className="run-report-group-breakdown-overall">
              <th scope="row">Whole run</th>
              <StatCells stats={breakdown.overall} deltas={deltas} />
            </tr>
            {groups.map(group => {
              const open = Boolean(query) || !collapsed.has(group.name)
              const label = breakdown.ungrouped ? 'All groups' : groupBreakdownLabel(group.name)
              return <Fragment key={group.name}>
                <tr className="run-report-group-breakdown-group">
                  <th scope="row">
                    <button type="button" className="btn btn-ghost btn-sm" aria-expanded={open} onClick={() => toggle(group.name)} disabled={Boolean(query)}>
                      <span aria-hidden="true">{open ? '▾' : '▸'}</span> {label}
                    </button>
                    <small>{group.items.length.toLocaleString()} {group.items.length === 1 ? itemNoun.toLowerCase() : `${itemNoun.toLowerCase()}s`}</small>
                  </th>
                  {group.totals ? <StatCells stats={group.totals} /> : <td colSpan={COLUMNS - 1} className="run-report-muted">Group totals are not in this report.</td>}
                </tr>
                {open && group.items.map(item => <tr key={item.name} className="run-report-group-breakdown-item">
                  <th scope="row">{item.name}</th>
                  <StatCells stats={item.stats} />
                </tr>)}
              </Fragment>
            })}
            {groups.length === 0 && <tr><td colSpan={COLUMNS} className="run-report-muted">{query ? 'No group or name matches the filter.' : `No ${populationLabels[population].toLowerCase()} were recorded.`}</td></tr>}
          </tbody>
        </table>
      </div>
      {breakdown.truncated && <p className="run-report-muted" role="note">This run recorded more group and name pairs than the report keeps; some rows are missing. Group totals and the whole-run row cover every record.</p>}
    </section>
  )
}
