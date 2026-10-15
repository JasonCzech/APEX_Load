import type { SalesforceEPTCalibration } from '../types'

interface SalesforceEPTTableProps {
  rows: SalesforceEPTCalibration[]
}

function formatMs(value: number, signed = false): string {
  if (!Number.isFinite(value)) return '—'
  return `${signed && value > 0 ? '+' : ''}${value.toFixed(1)} ms`
}

/**
 * Each step's Salesforce EPT readings (eptVisible=1, read when the counter
 * changed) beside the same steps' own times, from an EPT calibration run.
 */
export default function SalesforceEPTTable({ rows }: SalesforceEPTTableProps) {
  if (rows.length === 0) return null
  return (
    <div className="archive-salesforce-ept" data-testid="archive-salesforce-ept">
      <h4>Salesforce EPT calibration</h4>
      <p className="scenario-script-status">
        Salesforce's Experienced Page Time counter, read after each step whose counter changed, beside the same steps' own
        times.
      </p>
      <table className="data-table">
        <thead>
          <tr>
            <th>Step</th>
            <th className="num">Readings</th>
            <th className="num">EPT avg</th>
            <th className="num">Step avg</th>
            <th className="num">Difference avg</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={row.step}>
              <td>{row.step}</td>
              <td className="num">{row.samples}</td>
              <td className="num">{formatMs(row.avg_ept_ms)}</td>
              <td className="num">{formatMs(row.avg_step_ms)}</td>
              <td className="num">{formatMs(row.avg_difference_ms, true)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
