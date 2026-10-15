import type { BrowserVersionSkew } from '../types'

interface BrowserVersionSkewTableProps {
  rows: BrowserVersionSkew[]
}

function sourceLabel(source: string): string {
  return source === 'recording' ? 'The recording’s browser' : 'The Playwright driver’s tested browser'
}

/**
 * Browser sessions whose browser's major version differs from the one the
 * Playwright driver was tested with, or from the one the script was recorded
 * with.
 */
export default function BrowserVersionSkewTable({ rows }: BrowserVersionSkewTableProps) {
  if (rows.length === 0) return null
  return (
    <div className="archive-browser-version-skew" data-testid="archive-browser-version-skew">
      <h4>Browser version mismatches</h4>
      <p className="scenario-script-status">
        Behavior and timings can differ between browser major versions.
      </p>
      <table className="data-table">
        <thead>
          <tr><th>Compared with</th><th>Launched</th><th>Expected</th><th className="num">Sessions</th></tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={`${row.source}\u0000${row.actual}\u0000${row.expected}`}>
              <td>{sourceLabel(row.source)}</td>
              <td className="cell-mono">{row.actual}</td>
              <td className="cell-mono">{row.expected}</td>
              <td className="num">{row.sessions}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
