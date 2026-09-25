import { useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { CartesianGrid, Legend, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useModalDialog } from '../../hooks/useModalDialog'
import { comparisonColors, difference, measurementValue, number, type ComparisonRow, type ComparisonSnapshot, type Measurement } from './model'


export function ComparisonDetail({ row, snapshot, onClose }: { row: ComparisonRow; snapshot: ComparisonSnapshot; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement>(null)
  const [chart, setChart] = useState<'load' | 'elapsed'>('load')
  useModalDialog({ dialogRef, onClose })
  const resource = row.id.startsWith('resource/')
  const series = [{ id: snapshot.current.id, name: 'Current', measurement: row.current, window: row.window }, ...snapshot.baselines.map(run => ({ id: run.id, name: run.name, measurement: row.baselines[run.id]?.measurement, window: row.baselines[run.id]?.window }))]
  const metrics: Array<[keyof Measurement, string]> = [['samples', 'Samples'], ['successes', 'Successful samples'], ['errors', 'Errors'], ['mean', resource ? 'Time-weighted mean' : 'Successful mean'], ['p50', 'P50'], ['p90', 'P90'], ['p95', 'P95'], ['p99', 'P99'], ['min', 'Minimum'], ['max', 'Maximum'], ['coverage_pct', 'Coverage %'], ['instances', 'Instances']]
  if (resource) metrics.splice(1, 2)
  if (!resource) metrics.push(['throughput', 'Throughput / s'], ['successful_throughput', 'Successful throughput / s'], ['error_pct', 'Error %'])
  return createPortal(<div className="bc-overlay"><div ref={dialogRef} className="bc-drawer" role="dialog" aria-modal="true" aria-labelledby="bc-detail-title">
    <header><div><p className="bc-eyebrow">{row.source} · {row.group || 'Whole workload'}</p><h2 id="bc-detail-title">{row.name}</h2></div><button type="button" className="btn btn-secondary" onClick={onClose}>Close details</button></header>
    <p>{snapshot.rule_version} · {row.unit || 'Unit unverified'} · Recorded window {number(row.window?.start_seconds)}–{number(row.window?.end_seconds)} seconds</p>
    <label>Chart alignment <select value={chart} onChange={e => setChart(e.target.value as 'load' | 'elapsed')}><option value="load">Matched observed load</option><option value="elapsed">Elapsed time within run</option></select></label>
    <div className="bc-chart"><ResponsiveContainer width="100%" height={260}><LineChart><CartesianGrid strokeDasharray="3 3" /><XAxis dataKey={chart === 'load' ? 'load' : 'seconds'} type="number" domain={['dataMin', 'dataMax']} label={{ value: chart === 'load' ? 'Observed VUsers' : 'Seconds since run start', position: 'insideBottom', offset: -3 }} /><YAxis /><Tooltip /><Legend />{series.map((run, i) => <Line key={run.id} name={run.name} data={chart === 'load' ? (run.measurement && run.window ? [{ load: run.window.load, value: measurementValue(run.measurement, resource ? 'mean' : 'p95', resource) }] : []) : run.measurement?.points || []} dataKey="value" stroke={comparisonColors[i]} dot={{ r: 3, fill: comparisonColors[i], stroke: comparisonColors[i], strokeOpacity: 1 }} strokeOpacity={chart === 'elapsed' ? 0 : 1} connectNulls={false} isAnimationActive={false} />)}</LineChart></ResponsiveContainer></div>
    <p>{chart === 'load' ? 'Each point is the selected stable window: successful P95 for transactions, time-weighted mean for resources.' : resource ? 'Captured resource measurements shown as points; gaps are excluded from coverage calculations.' : 'Each elapsed-time point shows maximum successful latency in its time bucket.'}</p>
    <div className="bc-table-scroll"><table><thead><tr><th>Metric</th>{series.map(run => <th key={run.id}>{run.name}</th>)}</tr></thead><tbody>{metrics.map(([key, label]) => <tr key={key}><th scope="row">{label}</th>{series.map(run => <td key={run.id}>{number(measurementValue(run.measurement, key, resource))}</td>)}</tr>)}</tbody></table></div>
    <h3>Comparison evidence</h3>{snapshot.baselines.map((run, i) => { const cell = row.baselines[run.id]; return <section key={run.id} className="bc-detail-evidence"><h4 style={{ borderLeft: `4px solid ${comparisonColors[i + 1]}`, paddingLeft: 8 }}>{run.release_name} · {run.name}</h4><p>{cell?.approximate ? 'Approximate' : 'Exact or unavailable'} load match: {number(row.window?.load)} vs {number(cell?.window?.load)} VUsers. {resource ? 'Mean' : 'P95'} change: {resource && cell?.delta === undefined ? 'Change unavailable' : difference(measurementValue(row.current, resource ? 'mean' : 'p95', resource), measurementValue(cell?.measurement, resource ? 'mean' : 'p95', resource))} {row.unit}</p><p>Reference window {number(cell?.window?.start_seconds)}–{number(cell?.window?.end_seconds)} seconds · {cell?.window?.direction || 'unavailable'} load</p><ul>{cell?.qualification?.length ? cell.qualification.map((reason, index) => <li key={index}>{reason}</li>) : <li>Recorded comparison checks passed. Advisory only.</li>}</ul><a href={`#/report/${encodeURIComponent(run.id)}?section=evidence`}>Open baseline evidence</a></section> })}
    {!resource && series.some(run => run.measurement?.sla?.length) && <section><h3>Recorded whole-run SLA results</h3>{series.map(run => <div key={run.id}><h4>{run.name}</h4>{run.measurement?.sla?.map((sla, i) => <p key={i}>{sla.transaction}: {sla.status} · {number(sla.met_pct)}% met · {number(sla.threshold_ms)} ms threshold</p>)}</div>)}</section>}
    <details><summary>Evaluation policy</summary><pre>{JSON.stringify(snapshot.policy, null, 2)}</pre></details>
    <p>Resource changes are contemporaneous evidence. They do not establish which transaction caused a system change.</p>
  </div></div>, document.body)
}
