import type { AuraActionRollup } from '../types'

interface AuraActionRollupTableProps {
  rows: AuraActionRollup[]
}

function formatMs(value: number): string {
  return Number.isFinite(value) ? `${value.toFixed(1)} ms` : '—'
}

/**
 * The Salesforce Aura actions each transaction's steps sent, with the most
 * time first: an Apex call is named by its class and method, any other action
 * by its descriptor.
 */
export default function AuraActionRollupTable({ rows }: AuraActionRollupTableProps) {
  if (rows.length === 0) return null
  return (
    <div className="archive-aura-actions" data-testid="archive-aura-actions">
      <h4>Salesforce Aura actions by transaction</h4>
      <p className="scenario-script-status">
        Time is that of the requests that carried each action: a batch's time counts for every action in it.
      </p>
      <table className="data-table">
        <thead>
          <tr><th>Transaction</th><th>Action</th><th className="num">Calls</th><th className="num">Total time</th><th className="num">Errors</th></tr>
        </thead>
        <tbody>
          {rows.map(row => (
            <tr key={`${row.transaction}\u0000${row.name}`}>
              <td>{row.transaction || 'Outside transactions'}</td>
              <td className="cell-mono">{row.name}{row.apex ? ' (Apex)' : ''}</td>
              <td className="num">{row.count}</td>
              <td className="num">{formatMs(row.total_ms)}</td>
              <td className="num">{row.errors ?? 0}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
