import { useState } from 'react'

import type { ReportLens } from '../../appRouting'
import type { RunArchiveTailAttribution } from '../../types/timeAnatomy'
import { bandEstimateCopy, formatAnatomyMs, formatSignedMs, tailBars, tailStatusCopy } from './timeAnatomyModel'

interface Props {
  tail: RunArchiveTailAttribution
  lens: ReportLens
  onSelectRequest?: (key: string) => void
}

/**
 * R2: what the slowest 5% of instances spend extra time on, as differences
 * of band means (never P95 − P50). Bars diverge from a zero centre line.
 */
export default function TailAttributionChart({ tail, lens, onSelectRequest }: Props) {
  const [asTable, setAsTable] = useState(false)
  const allBars = tailBars(tail)
  const bars = lens === 'executive' ? allBars.slice(0, 3) : allBars
  const maxAbs = Math.max(1, ...bars.map(bar => Math.abs(bar.deltaMs)))
  const statusCopy = tailStatusCopy(tail)
  const headline = tail.status === 'ok' ? tail.headline : ''
  const contributors = (tail.requests || []).slice(0, 3)
  const showBars = bars.length > 0 && tail.status !== 'insufficient_samples' && tail.status !== 'no_tail'
  return (
    <div className="rr-anatomy-tail">
      {/* The panel's persistent live region announces a change of transaction; this paragraph is not live. */}
      <p className="rr-anatomy-headline">
        {headline || statusCopy}
      </p>
      {showBars && <>
        <div className="rr-anatomy-tail-toolbar">
          <h3>Tail vs median, per component</h3>
          <button type="button" className="btn btn-ghost btn-sm" aria-pressed={asTable} onClick={() => setAsTable(value => !value)}>{asTable ? 'View as chart' : 'View as table'}</button>
        </div>
        {asTable
          ? <table className="tx-table rr-anatomy-tail-table">
            <caption className="sr-only">Mean time per component in the tail band minus the median band</caption>
            <thead><tr><th scope="col">Component</th><th scope="col">Tail − median</th><th scope="col">Share of difference</th></tr></thead>
            <tbody>{bars.map(bar => <tr key={bar.component}>
              <th scope="row">{bar.label}</th>
              <td>{formatSignedMs(bar.deltaMs)}</td>
              <td>{bar.sharePct === null ? '—' : `${Math.round(bar.sharePct)}%`}</td>
            </tr>)}</tbody>
          </table>
          : <ul className="rr-anatomy-diverging" aria-label="Tail vs median, per component">
            {bars.map(bar => {
              const width = (Math.abs(bar.deltaMs) / maxAbs) * 50
              const value = `${formatSignedMs(bar.deltaMs)}${bar.sharePct === null ? '' : ` (${Math.round(bar.sharePct)}%)`}`
              return <li key={bar.component} className={`rr-anatomy-diverging-row rr-anatomy-${bar.sign}`}>
                <span className="rr-anatomy-diverging-label">{bar.label}</span>
                <span className="rr-anatomy-diverging-track" aria-hidden="true">
                  <span className="rr-anatomy-diverging-zero" />
                  <span className="rr-anatomy-diverging-bar" style={bar.sign === 'negative' ? { right: '50%', width: `${width}%` } : { left: '50%', width: `${width}%` }} />
                </span>
                <span className="rr-anatomy-diverging-value">{value}</span>
              </li>
            })}
          </ul>}
      </>}
      {contributors.length > 0 && tail.status !== 'insufficient_samples' && <div className="rr-anatomy-contributors">
        <h3>Biggest contributors</h3>
        <ul>{contributors.map(request => <li key={request.key}>
          <button type="button" className="btn btn-secondary btn-sm rr-anatomy-contributor" onClick={() => onSelectRequest?.(request.key)}
            aria-label={`${request.action_name}: ${formatSignedMs(request.delta_ms)} in the tail, mostly ${request.dominant_phase}. Show its row`}>
            {request.action_name} <strong>{formatSignedMs(request.delta_ms)}</strong> <small>mostly {request.dominant_phase}</small>
          </button>
        </li>)}</ul>
      </div>}
      <p className="rr-anatomy-badges">
        <span className="rr-anatomy-badge">{bandEstimateCopy(tail)}</span>
        {tail.threshold_quality === 'approximate' && <span className="rr-anatomy-badge">Thresholds approximate (within 0.5%)</span>}
        <span className="rr-anatomy-badge">Tail: above P95 {formatAnatomyMs(tail.threshold_p95_ms)} · Median: at or below P50 {formatAnatomyMs(tail.threshold_p50_ms)}</span>
      </p>
    </div>
  )
}
