import { useEffect, useState } from 'react'

export interface ExactStringListRowsProps {
  ariaLabel: string
  values?: string[]
  placeholder?: string
  addLabel?: string
  /** When true, show ▲/▼ controls so order-significant lists (classpath) can be reordered. */
  reorderable?: boolean
  onChange: (values: string[]) => void
}

/**
 * ExactStringListRows edits an ordered list of exact string entries as one
 * input per row with add/remove (and optional reorder) controls. Unlike a
 * newline textarea it never splits or trims a value, so paths and JVM argument
 * tokens that contain spaces stay intact. Empty rows are dropped on commit.
 */
export function ExactStringListRows({
  ariaLabel,
  values,
  placeholder,
  addLabel = 'Add entry',
  reorderable = false,
  onChange,
}: ExactStringListRowsProps) {
  // Keep a local editable copy so a row can be blanked mid-edit without the
  // parent immediately dropping it; commit prunes empties.
  const [rows, setRows] = useState<string[]>(() => values ?? [])

  // Only re-sync from the parent when the committed content actually differs
  // from what we're showing. A parent re-render that passes a fresh-identity but
  // equal array (e.g. `values ?? []`) must NOT wipe an in-progress empty/edited
  // row the user just added. Comparing pruned content keeps local edits intact
  // while still absorbing genuine external changes (undo, reset, apply-to-group).
  useEffect(() => {
    const incoming = values ?? []
    const localPruned = rows.filter(entry => entry.trim() !== '')
    const sameContent =
      incoming.length === localPruned.length && incoming.every((entry, i) => entry === localPruned[i])
    if (!sameContent) setRows(incoming)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values])

  const commit = (next: string[]) => {
    setRows(next)
    onChange(next.map(entry => entry).filter(entry => entry.trim() !== ''))
  }

  const updateRow = (index: number, value: string) => {
    const next = rows.slice()
    next[index] = value
    setRows(next)
  }

  const removeRow = (index: number) => {
    commit(rows.filter((_, position) => position !== index))
  }

  const moveRow = (index: number, delta: number) => {
    const target = index + delta
    if (target < 0 || target >= rows.length) return
    const next = rows.slice()
    const [entry] = next.splice(index, 1)
    next.splice(target, 0, entry)
    commit(next)
  }

  const addRow = () => setRows([...rows, ''])

  // On blur, collapse only the row that just lost focus if the user blanked it,
  // so a cleared middle/leading entry doesn't linger as a ghost row. Other rows
  // (including a freshly-added trailing empty the user hasn't typed into yet)
  // are left untouched, then `commit` prunes empties for the parent value.
  const handleRowBlur = (index: number) => {
    if ((rows[index] ?? '').trim() === '') {
      commit(rows.filter((_, position) => position !== index))
    } else {
      commit(rows)
    }
  }

  return (
    <div className="exact-string-list-rows" role="group" aria-label={ariaLabel}>
      {rows.length === 0 && <div className="cell-muted">No entries.</div>}
      {rows.map((entry, index) => (
        <div className="exact-string-list-row" key={index}>
          <input
            aria-label={`${ariaLabel} entry ${index + 1}`}
            value={entry}
            placeholder={placeholder}
            onChange={event => updateRow(index, event.target.value)}
            onBlur={() => handleRowBlur(index)}
          />
          {reorderable && (
            <>
              <button
                type="button"
                className="row-action"
                aria-label={`Move ${ariaLabel} entry ${index + 1} up`}
                disabled={index === 0}
                onClick={() => moveRow(index, -1)}
              >
                ▲
              </button>
              <button
                type="button"
                className="row-action"
                aria-label={`Move ${ariaLabel} entry ${index + 1} down`}
                disabled={index === rows.length - 1}
                onClick={() => moveRow(index, 1)}
              >
                ▼
              </button>
            </>
          )}
          <button
            type="button"
            className="row-action row-remove"
            aria-label={`Remove ${ariaLabel} entry ${index + 1}`}
            onClick={() => removeRow(index)}
          >
            ✕
          </button>
        </div>
      ))}
      <button type="button" className="row-add" onClick={addRow}>
        + {addLabel}
      </button>
    </div>
  )
}
