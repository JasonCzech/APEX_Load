import type { BrowserReplayEvent } from '../types/replay'
import type { ScriptActionDefinition } from '../types/scripts'
import { liveTableFailureNote, liveTableStepResult } from './liveTableSandboxModel'
import DataRowGrid from './ux/DataRowGrid'

const STATUS_NOTES: Record<string, string> = {
  empty: 'No row was found, and the step went on without one (When no row is found: Skip and continue).',
  condition_failed: 'The row did not match what the step expected, so nothing changed (When the row does not match: Skip and continue).',
}

/**
 * A replayed table step: its row as a table, with status, record ID, version,
 * revision and count; or why it failed, and what to do about it.
 */
export default function LiveTableStepResult({ event, action }: { event: BrowserReplayEvent; action?: ScriptActionDefinition }) {
  const params = (action?.params || {}) as Record<string, unknown>
  const prefix = typeof params.result_prefix === 'string' ? params.result_prefix : undefined
  const result = liveTableStepResult(event.extractions, prefix)
  const failure = event.success ? null : liveTableFailureNote(event.error_message)
  const rowColumns = result?.row ? [...new Set([...(result.columns || []), ...Object.keys(result.row)])] : []
  const rangeColumns = [...new Set((result?.rows || []).flatMap(row => Object.keys(row.values || {})))]
  // A count says how many rows or cells a range returned; beside one row, a zero only confuses.
  const showCount = Boolean(result?.rows || result?.cells || (result?.count && result.count !== '0'))
  return <div className="replay-code-block live-table-step-result">
    <span className="replay-detail-label">Table result{result ? <> · saved as <code>{result.prefix}</code></> : null}</span>
    {failure && <p className="live-table-step-note" role="note">{failure}</p>}
    {result ? <>
      <dl className="live-table-step-meta">
        {([['Status', result.status], ['Record ID', result.recordId], ['Version', result.version], ['Table revision', result.revision], ['Count', showCount ? result.count : undefined], ['Value', result.value]] as const)
          .filter(([, value]) => value !== undefined)
          .map(([label, value]) => <div key={label}><dt>{label}</dt><dd><code>{value === '' ? '(empty)' : value}</code></dd></div>)}
      </dl>
      {result.status && STATUS_NOTES[result.status] && <p className="live-table-step-note">{STATUS_NOTES[result.status]}</p>}
      {result.row && <DataRowGrid label={`Row for ${event.action_name || 'this step'}`} columns={rowColumns} rows={[{ id: result.recordId || 'row', values: result.row }]} markEmpty emptyMessage="The row has no cells." />}
      {result.rows && result.rows.length > 0 && <DataRowGrid label={`Rows for ${event.action_name || 'this step'}`} columns={rangeColumns} rows={result.rows.map((row, index) => ({ id: row.id || String(index), values: row.values || {} }))} markEmpty />}
      {result.cells && result.cells.length > 0 && <DataRowGrid label={`Cells for ${event.action_name || 'this step'}`} columns={['position', 'value']} rows={result.cells.map((cell, index): { id: string; values: Record<string, string> } => ({ id: String(index), values: cell.present ? { position: String(index + 1), value: cell.value || '' } : { position: String(index + 1) } }))} markEmpty />}
    </> : <p className="live-table-step-note">{prefix === undefined && !action ? 'No result was saved.' : !prefix ? 'This step saves no results. Name where it saves them (Save results as) to see its row here.' : event.success ? 'No result was saved.' : 'A failed step clears its results.'}</p>}
  </div>
}
