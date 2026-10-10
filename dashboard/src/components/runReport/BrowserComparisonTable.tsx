import { useMemo } from 'react'

import type { BrowserDocumentSummaryMode } from '../../types/browserDocumentEvidence'
import { formatBrowserDocumentValue } from '../browserDocumentEvidenceModel'
import { BROWSER_COMPARISON_METRICS, buildBrowserComparison } from './browserComparisonModel'

const METRIC_LABELS: Record<string, string> = { lcp: 'LCP', fcp: 'FCP', inp: 'INP', cls: 'CLS', ttfb: 'TTFB' }

// BrowserComparisonTable compares a run's browser documents by engine, device
// profile and scenario group: each metric's p75 over final qualified
// documents, with how many documents it counts.
export default function BrowserComparisonTable({ summary, mode }: { summary: unknown; mode: BrowserDocumentSummaryMode }) {
  const comparison = useMemo(() => buildBrowserComparison(summary, mode), [summary, mode])
  return <section className="run-report-panel browser-comparison" aria-label="Device, engine and group comparison">
    <h2>Device, engine and group comparison</h2>
    {comparison.state === 'absent' && <p>{mode === 'live-history' ? 'No browser document evidence has been published for this run yet.' : 'This run kept no browser document evidence.'}</p>}
    {(comparison.state === 'invalid' || comparison.state === 'unsupported') && <p role="note">{comparison.issue}</p>}
    {comparison.state === 'ready' && <>
      <p>
        p75 over final qualified main-frame navigation documents, by engine, device profile and scenario group (documents record no page, so the group stands in for one).
        {mode === 'live-history' ? ' Live values cover the documents published so far.' : ''}
        {comparison.incomplete ? ' Some document evidence was dropped, so these values are incomplete.' : ''}
        {comparison.otherScopes > 0 ? ` ${comparison.otherScopes} child-frame or back-forward cache metric ${comparison.otherScopes === 1 ? 'tuple is' : 'tuples are'} left out.` : ''}
      </p>
      {comparison.rows.length === 0
        ? <p>No main-frame navigation documents were measured.</p>
        : <div className="run-archive-coverage-table"><table className="tx-table" aria-label="Browser document comparison">
          <thead><tr>
            <th scope="col">Engine</th><th scope="col">Device profile</th><th scope="col">Group</th>
            {BROWSER_COMPARISON_METRICS.map(metric => <th scope="col" key={metric}>{METRIC_LABELS[metric]} p75</th>)}
          </tr></thead>
          <tbody>{comparison.rows.map(row => <tr key={row.key}>
            <td>{row.engine}</td>
            <td>{row.deviceProfile || 'Desktop (no profile)'}{row.cohortLabel ? ` · ${row.cohortLabel}` : ''}</td>
            <td>{row.group || '—'}{row.region ? ` · ${row.region}` : ''}</td>
            {BROWSER_COMPARISON_METRICS.map(metric => {
              const cell = row.metrics[metric]
              if (!cell) return <td key={metric}>—</td>
              return <td key={metric}>{cell.p75 === null ? 'No final documents' : formatBrowserDocumentValue(cell.p75, cell.unit)}<span className="run-report-muted"> ({cell.final} {cell.final === 1 ? 'document' : 'documents'})</span></td>
            })}
          </tr>)}</tbody>
        </table></div>}
    </>}
  </section>
}
