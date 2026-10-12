import { liveTableStateLabel, useLiveTablesSnapshot } from '../hooks/useLiveTablesSnapshot'
import LoadingState from './ux/LoadingState'

/**
 * The run's live tables at a glance, so a consumer group idling on an empty
 * queue reads as that rather than as a slow target. Row values stay on the
 * run page.
 */
export default function CommandCenterLiveTables({ testId, onOpen }: { testId: string; onOpen?: () => void }) {
  const { current, error } = useLiveTablesSnapshot(testId)
  return (
    <section className="command-panel command-live-tables" aria-label="Live tables">
      <div className="command-panel-header">
        <h3>Live tables</h3>
        {onOpen && <button type="button" className="btn btn-ghost btn-sm" onClick={onOpen}>Open live tables</button>}
      </div>
      {error && <p role="alert">{error}</p>}
      {!current && !error && <LoadingState what="live tables" inline />}
      {current && current.tables.length === 0 && current.projectTables.length === 0 && <p>No live tables yet; they appear when the run starts.</p>}
      {current && (current.tables.length > 0 || current.projectTables.length > 0) && (
        <ul className="command-live-table-list">
          {current.tables.map(table => (
            <li key={table.id}>
              <strong>{table.alias}</strong> {liveTableStateLabel(table)}
              {' · '}{table.counts?.available ?? 'Unknown'} available
              {' · '}{table.counts?.leased ?? 0} leased
              {table.observations?.wait_count ? <>{' · '}{table.observations.wait_count} waits so far</> : null}
              {table.op_unknown ? <>{' · '}<span className="danger">{table.op_unknown} unknown outcome{table.op_unknown === 1 ? '' : 's'}</span></> : null}
            </li>
          ))}
          {current.projectTables.map(table => (
            <li key={table.attachment_id || table.table_id}><strong>{table.alias}</strong> Project live table</li>
          ))}
        </ul>
      )}
    </section>
  )
}
