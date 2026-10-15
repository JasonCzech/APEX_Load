import type { BrowserIterationTimes, BrowserIterationTransaction } from '../types'

interface BrowserIterationTableProps {
  rows: BrowserIterationTransaction[]
}

function formatMs(times: BrowserIterationTimes | undefined, key: 'avg_ms' | 'p90_ms'): string {
  if (!times || times.count === 0 || !Number.isFinite(times[key])) return '—'
  return `${times[key].toFixed(1)} ms`
}

/**
 * Each transaction's passed times in cold browser iterations (the first in a
 * browser context) next to warm ones, which SLAs judge by default.
 */
export default function BrowserIterationTable({ rows }: BrowserIterationTableProps) {
  if (rows.length === 0) return null
  return (
    <div className="archive-browser-iterations" data-testid="archive-browser-iterations">
      <h4>Cold and warm browser iterations</h4>
      <p className="scenario-script-status">
        A browser session that keeps its context runs its first iteration cold and the rest warm. Times are of passed
        transactions; SLAs judge the warm iterations unless a rule says otherwise.
      </p>
      <table className="data-table">
        <thead>
          <tr>
            <th>Transaction</th>
            <th className="num">Cold passed</th><th className="num">Cold avg</th><th className="num">Cold p90</th>
            <th className="num">Warm passed</th><th className="num">Warm avg</th><th className="num">Warm p90</th>
            <th className="num">Failed (cold / warm)</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.transaction}>
              <td>{row.transaction}</td>
              <td className="num">{row.cold?.count ?? 0}</td>
              <td className="num">{formatMs(row.cold, 'avg_ms')}</td>
              <td className="num">{formatMs(row.cold, 'p90_ms')}</td>
              <td className="num">{row.warm?.count ?? 0}</td>
              <td className="num">{formatMs(row.warm, 'avg_ms')}</td>
              <td className="num">{formatMs(row.warm, 'p90_ms')}</td>
              <td className="num">{row.cold?.errors ?? 0} / {row.warm?.errors ?? 0}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
