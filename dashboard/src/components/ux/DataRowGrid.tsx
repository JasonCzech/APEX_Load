import { useEffect, useId, useMemo, useRef, useState, type ClipboardEvent, type KeyboardEvent, type ReactNode } from 'react'
import { isTabularText, newColumnError, parseTabularPaste } from './dataRowGridModel'
import './DataRowGrid.css'

export interface DataGridRow {
  /** Stable key for the row. */
  id: string
  /** Cell values by column; a column the row doesn't set is absent, not empty. */
  values: Record<string, string>
  /** Shown beside the row number when the row is not in its normal state. */
  status?: string
  /** Row metadata shown when "Show row details" is on. */
  details?: ReactNode
}

interface DataRowGridProps {
  /** Accessible name of the table. */
  label: string
  columns: readonly string[]
  rows: readonly DataGridRow[]
  /** Number of the first row, for paged tables. */
  firstRowNumber?: number
  /** Heading for the details column. */
  detailsLabel?: string
  /** Disables every edit control, e.g. while a save is running. */
  disabled?: boolean
  emptyMessage?: ReactNode
  /** Name empty and absent cells in words; live tables tell the two apart. */
  markEmpty?: boolean
  maxColumns?: number
  /**
   * Saves one changed cell. Resolve false (or reject) to keep the typed value
   * in the cell so it can be corrected or retried; resolve a string to say why
   * it failed, which the grid shows as the cell's description.
   */
  onCellCommit?: (row: DataGridRow, column: string, value: string) => Promise<boolean | string | void> | boolean | string | void
  /** Adds rows typed into the new-row line or pasted from a spreadsheet. */
  onAddRows?: (rows: Record<string, string>[]) => Promise<boolean | void> | boolean | void
  onAddColumn?: (name: string) => Promise<boolean | void> | boolean | void
  /** Offered on columns no row sets, to take a column out of the table. */
  onRemoveColumn?: (name: string) => Promise<boolean | void> | boolean | void
  rowActions?: (row: DataGridRow, rowNumber: number) => ReactNode
}

const draftKey = (rowId: string, column: string) => `${rowId}\u0000${column}`

function without<T>(record: Record<string, T>, key: string): Record<string, T> {
  if (!(key in record)) return record
  const next = { ...record }
  delete next[key]
  return next
}

/**
 * One table for parameter data: a column per parameter in the source's order,
 * inline cell editing, a new-row line, add column, and paste from a
 * spreadsheet. Read-only when no edit callbacks are given.
 */
export default function DataRowGrid({
  label, columns, rows, firstRowNumber = 1, detailsLabel = 'Row details', disabled = false, emptyMessage, markEmpty = false,
  maxColumns, onCellCommit, onAddRows, onAddColumn, onRemoveColumn, rowActions,
}: DataRowGridProps) {
  const tableId = useId()
  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [failed, setFailed] = useState<Record<string, true>>({})
  const [newRow, setNewRow] = useState<Record<string, string>>({})
  const [showDetails, setShowDetails] = useState(false)
  const [addingColumn, setAddingColumn] = useState(false)
  const [columnName, setColumnName] = useState('')
  const [columnError, setColumnError] = useState('')
  const [notice, setNotice] = useState('')
  // The latest failed save, kept apart from notice so a failed cell is never
  // described by other text.
  const [failure, setFailure] = useState<{ key: string; message: string } | null>(null)
  const [announcement, setAnnouncement] = useState('')
  const [busy, setBusy] = useState(false)
  const tableRef = useRef<HTMLTableElement | null>(null)
  const columnInputRef = useRef<HTMLInputElement | null>(null)
  // Opening the add-column form moves focus into it.
  useEffect(() => { if (addingColumn) columnInputRef.current?.focus() }, [addingColumn])
  // Saves run after awaits, so they read the latest drafts and rows from refs.
  const draftsRef = useRef(drafts)
  draftsRef.current = drafts
  const rowsRef = useRef(rows)
  rowsRef.current = rows
  const committing = useRef(new Set<string>())
  const recommit = useRef(new Set<string>())
  const busyRef = useRef(false)
  const hasDetails = rows.some(row => row.details !== undefined)
  // Computed per data change, not per keystroke.
  const unusedColumns = useMemo(() => {
    const used = new Set(rows.flatMap(row => Object.keys(row.values)))
    return columns.filter(column => !used.has(column))
  }, [columns, rows])
  // A failure is shown only while its row is still in the table.
  const shownFailure = failure && rows.some(row => failure.key.startsWith(`${row.id}\u0000`)) ? failure : null
  const canAddRows = Boolean(onAddRows) && columns.length > 0
  // Inputs stay enabled while rows are added, so focus stays where it is;
  // only the buttons wait for the request.
  const locked = disabled || busy
  const failureId = `${tableId}-cell-failure`
  const stageCell = (key: string, value: string) => {
    const next = { ...draftsRef.current, [key]: value }
    draftsRef.current = next
    setDrafts(current => ({ ...current, [key]: value }))
  }

  // A live region only speaks when its text changes, so a repeat of the same
  // message alternates a trailing no-break space.
  const announce = (text: string) => setAnnouncement(current => current === text ? `${text}\u00a0` : text)

  const focusCell = (rowIndex: number, columnIndex: number) => {
    const cell = tableRef.current?.querySelector<HTMLInputElement>(`[data-grid-cell="${rowIndex}:${columnIndex}"]`)
    if (cell) { cell.focus(); cell.select() }
  }

  // force skips the unchanged check: right after a save the parent may not
  // have re-rendered the saved value yet, so the rows here can be stale.
  const commitCell = async (rowId: string, column: string, force = false) => {
    const key = draftKey(rowId, column)
    if (!onCellCommit) return
    // A newer edit of a cell that is still saving goes out once that save ends.
    if (committing.current.has(key)) { recommit.current.add(key); return }
    const row = rowsRef.current.find(candidate => candidate.id === rowId)
    if (!row || !(key in draftsRef.current)) return
    const value = draftsRef.current[key]
    const unchanged = Object.prototype.hasOwnProperty.call(row.values, column) && row.values[column] === value
    if (unchanged && !force) {
      setDrafts(current => without(current, key))
      setFailed(current => without(current, key))
      return
    }
    const rowNumber = firstRowNumber + rowsRef.current.indexOf(row)
    committing.current.add(key)
    let outcome: boolean | string | void = false
    try {
      outcome = await onCellCommit(row, column, value)
    } catch {
      outcome = false
    } finally {
      committing.current.delete(key)
    }
    if (outcome !== false && typeof outcome !== 'string') {
      setDrafts(current => current[key] === value ? without(current, key) : current)
      setFailed(current => without(current, key))
      setFailure(current => current?.key === key ? null : current)
      announce(`Saved ${column} in row ${rowNumber}.`)
    } else if (key in draftsRef.current) {
      setFailed(current => ({ ...current, [key]: true }))
      setFailure({ key, message: `${column} in row ${rowNumber} was not saved. ${typeof outcome === 'string' ? `${outcome} ` : ''}Press Enter in the cell to try again, or Escape to undo the change.` })
    }
    // Only an edit made while this save ran goes out again; the blur that
    // follows Enter lands here too and must not repeat the same value.
    if (recommit.current.delete(key) && key in draftsRef.current && draftsRef.current[key] !== value) void commitCell(rowId, column, true)
  }

  const addRows = async (added: Record<string, string>[]): Promise<boolean> => {
    if (!onAddRows || added.length === 0 || busyRef.current) return false
    busyRef.current = true
    setBusy(true)
    try {
      return (await onAddRows(added)) !== false
    } catch {
      return false
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const submitNewRow = async (refocus: boolean) => {
    // The draft contains only touched columns. An explicitly cleared cell is
    // present with an empty string; untouched columns remain absent.
    const values = { ...newRow }
    if (Object.keys(values).length === 0) {
      setNotice('Type at least one value before adding the row.')
      return
    }
    if (busyRef.current) {
      setNotice('The previous row is still being added. Press Enter again once it is saved.')
      return
    }
    setNotice('')
    if (await addRows([values])) {
      // Keep anything typed for the next row while this one was being added.
      setNewRow(current => Object.fromEntries(Object.entries(current).filter(([column, value]) => value !== values[column])))
      if (refocus) focusCell(rowsRef.current.length, 0)
    }
  }

  const submitColumn = async () => {
    const problem = newColumnError(columnName, columns, maxColumns)
    if (problem) { setColumnError(problem); return }
    if (!onAddColumn || busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      if ((await onAddColumn(columnName.trim())) !== false) {
        setAddingColumn(false)
        setColumnName('')
        setColumnError('')
      }
    } catch {
      // The caller reports why the column was not added.
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const removeColumn = async (name: string) => {
    if (!onRemoveColumn || busyRef.current) return
    busyRef.current = true
    setBusy(true)
    try {
      await onRemoveColumn(name)
    } catch {
      // The caller reports why the column was not removed.
    } finally {
      busyRef.current = false
      setBusy(false)
    }
  }

  const onNewRowPaste = (event: ClipboardEvent<HTMLInputElement>, columnIndex: number) => {
    const text = event.clipboardData.getData('text/plain')
    if (!isTabularText(text)) return
    event.preventDefault()
    if (busyRef.current) {
      setNotice('Nothing was pasted: rows are still being added. Paste again once they are saved.')
      return
    }
    const parsed = parseTabularPaste(text, columns, columnIndex)
    // Nothing is added when values would be lost, so pasting again after
    // adding the columns can't duplicate rows. The caller reports success.
    if (parsed.unknownColumns.length > 0) {
      setNotice(`Nothing was pasted: the pasted header names columns this table doesn't have (${parsed.unknownColumns.join(', ')}). Add them, then paste again.`)
      return
    }
    if (parsed.droppedValues > 0) {
      setNotice(`Nothing was pasted: ${parsed.droppedValues} value(s) fall past the last column. Add those columns, then paste again.`)
      return
    }
    if (parsed.rows.length === 0) return
    setNotice('')
    void addRows(parsed.rows)
  }

  // A block pasted into an existing cell would land as one tab-joined value.
  const onCellPaste = (event: ClipboardEvent<HTMLInputElement>) => {
    if (!isTabularText(event.clipboardData.getData('text/plain'))) return
    event.preventDefault()
    setNotice(canAddRows
      ? 'Several cells can\'t be pasted into one cell. To add rows, paste them into the last line; to change existing cells, edit them one at a time.'
      : 'Several cells can\'t be pasted into one cell; edit them one at a time.')
  }

  const onCellKeyDown = (event: KeyboardEvent<HTMLInputElement>, rowIndex: number, columnIndex: number, row?: DataGridRow) => {
    if (event.key === 'Escape' && row) {
      const key = draftKey(row.id, columns[columnIndex])
      setDrafts(current => without(current, key))
      setFailed(current => without(current, key))
      setFailure(current => current?.key === key ? null : current)
      return
    }
    if (event.key === 'Enter' && !row) {
      event.preventDefault()
      void submitNewRow(false)
      return
    }
    const nextRow = event.key === 'Enter' || event.key === 'ArrowDown' ? rowIndex + 1 : event.key === 'ArrowUp' ? rowIndex - 1 : -2
    if (nextRow === -2) return
    event.preventDefault()
    if (event.key === 'Enter' && row) void commitCell(row.id, columns[columnIndex])
    if (nextRow >= 0) focusCell(nextRow, columnIndex)
  }

  const editable = Boolean(onCellCommit)
  const showActions = Boolean(rowActions) || canAddRows
  const visibleDetails = hasDetails && showDetails

  return <div className="data-grid">
    {(hasDetails || onAddColumn || (onRemoveColumn && unusedColumns.length > 0)) && <div className="data-grid-toolbar">
      {hasDetails && <label className="data-grid-toggle">
        <input type="checkbox" checked={showDetails} onChange={event => setShowDetails(event.target.checked)} />
        Show row details
      </label>}
      {onRemoveColumn && unusedColumns.length > 0 && <div className="data-grid-empty-columns" role="group" aria-labelledby={`${tableId}-empty-columns`}>
        <span id={`${tableId}-empty-columns`}>Columns no row uses:</span>
        {unusedColumns.map(column => <button key={column} type="button" className="btn btn-sm btn-ghost" disabled={locked} onClick={() => void removeColumn(column)}>Remove {column}</button>)}
      </div>}
      {onAddColumn && !addingColumn && <button type="button" className="btn btn-sm btn-secondary" disabled={locked} onClick={() => { setAddingColumn(true); setColumnError('') }}>Add column</button>}
      {onAddColumn && addingColumn && <form className="data-grid-add-column" onSubmit={event => { event.preventDefault(); void submitColumn() }}>
        <label>
          <span>Column name</span>
          <input
            ref={columnInputRef}
            className="settings-input"
            value={columnName}
            disabled={disabled}
            aria-invalid={Boolean(columnError)}
            aria-describedby={columnError ? `${tableId}-column-error` : undefined}
            onChange={event => { setColumnName(event.target.value); setColumnError('') }}
            onKeyDown={event => { if (event.key === 'Escape') { setAddingColumn(false); setColumnName(''); setColumnError('') } }}
          />
        </label>
        <button type="submit" className="btn btn-sm btn-primary" disabled={locked}>Add</button>
        <button type="button" className="btn btn-sm btn-ghost" onClick={() => { setAddingColumn(false); setColumnName(''); setColumnError('') }}>Cancel</button>
        {columnError && <span id={`${tableId}-column-error`} className="data-grid-error" role="alert">{columnError}</span>}
      </form>}
    </div>}
    {columns.length === 0 && rows.length === 0 ? <div className="data-grid-empty">{emptyMessage ?? 'No rows yet.'}</div> : <div className="data-grid-scroll">
      <table ref={tableRef} className="data-grid-table" aria-label={label}>
        <thead>
          <tr>
            <th scope="col" className="data-grid-number"><span className="sr-only">Row</span>#</th>
            {columns.map(column => <th scope="col" key={column} title={column}>{column}</th>)}
            {visibleDetails && <th scope="col">{detailsLabel}</th>}
            {showActions && <th scope="col"><span className="sr-only">Actions</span></th>}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => {
            const number = firstRowNumber + rowIndex
            return <tr key={row.id} data-status={row.status || undefined}>
              <th scope="row" className="data-grid-number">
                {number}
                {row.status && <span className="data-grid-status">{row.status}</span>}
              </th>
              {columns.map((column, columnIndex) => {
                const present = Object.prototype.hasOwnProperty.call(row.values, column)
                const value = present ? row.values[column] : ''
                if (!editable) {
                  return <td key={column} className="data-grid-value" title={present ? value : undefined}>
                    {!present
                      ? markEmpty ? <span className="data-grid-empty-value">absent</span> : <><span className="data-grid-absent" aria-hidden="true">—</span><span className="sr-only">Not set</span></>
                      : value !== '' ? value : markEmpty ? <span className="data-grid-empty-value">empty</span> : <span className="sr-only">Empty</span>}
                  </td>
                }
                const key = draftKey(row.id, column)
                return <td key={column} className={failed[key] ? 'data-grid-cell-failed' : undefined}>
                  <input
                    className="data-grid-input"
                    data-grid-cell={`${rowIndex}:${columnIndex}`}
                    aria-label={`${column}, row ${number}`}
                    aria-invalid={failed[key] || undefined}
                    aria-describedby={failed[key] && shownFailure?.key === key ? failureId : undefined}
                    placeholder={!present ? (markEmpty ? 'absent' : '—') : markEmpty && value === '' ? 'empty' : undefined}
                    value={key in drafts ? drafts[key] : value}
                    disabled={disabled}
                    onChange={event => {
                      const next = event.target.value
                      stageCell(key, next)
                    }}
                    onPaste={onCellPaste}
                    onBlur={() => void commitCell(row.id, column)}
                    onKeyDown={event => onCellKeyDown(event, rowIndex, columnIndex, row)}
                  />
                  {markEmpty && !present && !(key in drafts) && <button type="button" className="data-grid-set-empty" disabled={disabled} aria-label={`Set ${column} empty, row ${number}`} onClick={() => { stageCell(key, ''); focusCell(rowIndex, columnIndex); void commitCell(row.id, column) }}>Set empty</button>}
                </td>
              })}
              {visibleDetails && <td className="data-grid-details">{row.details}</td>}
              {showActions && <td className="data-grid-actions">{rowActions?.(row, number)}</td>}
            </tr>
          })}
          {canAddRows && <tr className="data-grid-new-row">
            <th scope="row" className="data-grid-number"><span className="sr-only">New row</span>+</th>
            {columns.map((column, columnIndex) => <td key={column}>
              <input
                className="data-grid-input"
                data-grid-cell={`${rows.length}:${columnIndex}`}
                aria-label={`${column}, new row`}
                value={newRow[column] ?? ''}
                disabled={disabled}
                onChange={event => {
                  const next = event.target.value
                  setNewRow(current => ({ ...current, [column]: next }))
                }}
                onPaste={event => onNewRowPaste(event, columnIndex)}
                onKeyDown={event => onCellKeyDown(event, rows.length, columnIndex)}
              />
              {markEmpty && !(column in newRow) && <button type="button" className="data-grid-set-empty" disabled={disabled} aria-label={`Set ${column} empty, new row`} onClick={() => { setNewRow(current => ({ ...current, [column]: '' })); focusCell(rows.length, columnIndex) }}>Set empty</button>}
            </td>)}
            {visibleDetails && <td><span className="sr-only">No details until the row is added</span></td>}
            <td className="data-grid-actions">
              <button type="button" className="btn btn-sm btn-secondary" disabled={locked} onClick={() => void submitNewRow(true)}>Add row</button>
            </td>
          </tr>}
        </tbody>
      </table>
    </div>}
    {canAddRows && <p className="data-grid-hint">Type in the last line and press Enter to add a row, or paste rows copied from a spreadsheet there. A pasted header row that names the columns sets which column each value goes to.</p>}
    {shownFailure && <p id={failureId} className="data-grid-notice" role="status">{shownFailure.message}</p>}
    {notice && <p className="data-grid-notice" role="status">{notice}</p>}
    <p className="sr-only" role="status" aria-live="polite">{announcement}</p>
  </div>
}
