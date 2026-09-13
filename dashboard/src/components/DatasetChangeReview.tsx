import { useRef } from 'react'
import type { ScriptDataSource } from '../types/scripts'
import { useModalDialog } from '../hooks/useModalDialog'
import DialogLayer from './ux/DialogLayer'
import { parseInlineData, validateInlineData } from './scriptEditorUtils'

export interface DatasetChange { index: number; before: ScriptDataSource; after: ScriptDataSource; title: string }

export function DatasetChangeReview({ change, affected, error, onApply, onCancel }: { change: DatasetChange; affected: string[]; error: string; onApply: () => void; onCancel: () => void }) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose: onCancel })
  const before = parseInlineData(change.before)
  const after = parseInlineData(change.after)
  const warnings = validateInlineData(change.after)
  const shortened = before.rows.filter((row, index) => row.length > (after.rows[index]?.length ?? 0)).length
  return <DialogLayer><div className="editor-dialog-backdrop"><div ref={dialogRef} className="editor-dialog" role="dialog" aria-modal="true" aria-label="Review dataset change">
    <div className="editor-dialog-header"><h3>{change.title}</h3></div>
    <div className="editor-dialog-body">
      <p>Dataset: <strong>{change.before.name}</strong>. Used by {affected.length} parameters: {affected.join(', ') || 'none'}.</p>
      <table data-table-pagination="off"><thead><tr><th>Property</th><th>Before</th><th>After</th></tr></thead><tbody>
        <tr><th>Rows</th><td>{before.rows.length}</td><td>{after.rows.length}</td></tr>
        <tr><th>Columns</th><td>{before.columns.join(', ')}</td><td>{after.columns.join(', ')}</td></tr>
      </tbody></table>
      {shortened > 0 && <p role="alert">{shortened} existing rows will lose cells or be replaced with shorter rows. Review the source before applying.</p>}
      {warnings.length > 0 && <ul>{warnings.map(warning => <li key={warning}>{warning}</li>)}</ul>}
      {error && <p role="alert">{error}</p>}
      <p>This changes the draft. Save Script persists it; Undo structural edit restores the previous dataset.</p>
    </div>
    <div className="parameter-workspace-actions" style={{ padding: 16 }}><button type="button" className="btn btn-secondary" onClick={onCancel}>Cancel</button><button type="button" className="btn btn-primary" disabled={Boolean(error)} onClick={onApply}>Apply dataset change</button></div>
  </div></div></DialogLayer>
}
