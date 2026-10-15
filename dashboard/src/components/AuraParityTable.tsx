import type { AuraParityTransaction } from '../types'

interface AuraParityTableProps {
  rows: AuraParityTransaction[]
}

function formatCount(value: number): string {
  return Number.isFinite(value) ? value.toFixed(1) : '—'
}

/**
 * Per transaction, the Aura calls real browsers sent beside those the HTTP
 * scripts send (median calls per transaction instance). Differences above one
 * call are flagged: Lightning caches and re-batches some actions.
 */
export default function AuraParityTable({ rows }: AuraParityTableProps) {
  if (rows.length === 0) return null
  return (
    <div className="archive-aura-parity" data-testid="archive-aura-parity">
      <h4>Aura parity</h4>
      <p className="scenario-script-status">
        The Aura calls real browsers sent beside those the HTTP scripts send, as median calls per transaction instance.
        Lightning caches some actions in the browser and re-batches calls, so a difference of more than one call is flagged.
      </p>
      {rows.map(row => (
        <div key={row.transaction} className="archive-aura-parity-transaction">
          <h5>{row.transaction}</h5>
          <p className="scenario-script-status">
            Browsers: {formatCount(row.browser_requests)} requests per instance ({row.browser_instances} instances),{' '}
            {formatCount(row.browser_actions_per_request)} actions each. HTTP scripts: {formatCount(row.http_requests)} requests per instance
            ({row.http_instances} instances), {formatCount(row.http_actions_per_request)} actions each.
          </p>
          <table className="data-table">
            <thead>
              <tr>
                <th>Action</th>
                <th className="num">Browser calls</th>
                <th className="num">HTTP calls</th>
                <th>Differs</th>
              </tr>
            </thead>
            <tbody>
              {row.actions.map(action => (
                <tr key={action.name} className={action.flagged ? 'is-flagged' : undefined}>
                  <td>{action.name}</td>
                  <td className="num">{formatCount(action.browser)}</td>
                  <td className="num">{formatCount(action.http)}</td>
                  <td>{action.flagged ? 'Yes' : ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </div>
  )
}
