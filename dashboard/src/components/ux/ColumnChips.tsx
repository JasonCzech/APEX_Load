import { useId, useState, type ReactNode } from 'react'
import './ColumnChips.css'

/**
 * An ordered list of column names as chips that move and remove, with a field
 * that adds one. `problem` says why a name cannot be added ('' when it can);
 * `known` columns are offered as suggestions.
 */
export default function ColumnChips({ label, caption, hint, columns, known = [], count, problem: problemFor, onChange, className = '' }: {
  label: string
  caption: ReactNode
  hint?: ReactNode
  columns: string[]
  known?: string[]
  /** How many columns the field takes, when fixed. */
  count?: number
  problem: (name: string, columns: string[]) => string
  onChange: (columns: string[]) => void
  className?: string
}) {
  const id = useId()
  const [draft, setDraft] = useState('')
  const problem = draft.trim() ? problemFor(draft.trim(), columns) : ''
  const full = count !== undefined && columns.length >= count
  const add = () => {
    const name = draft.trim()
    if (!name || problem || full) return
    onChange([...columns, name])
    setDraft('')
  }
  const move = (at: number, by: number) => {
    const next = [...columns]
    ;[next[at], next[at + by]] = [next[at + by], next[at]]
    onChange(next)
  }
  const suggestions = known.filter(column => !columns.includes(column))
  return <div className={`column-chips-field ${className}`.trim()} role="group" aria-labelledby={`${id}-label`}>
    <span id={`${id}-label`}>{caption}</span>
    <ol className="column-chips" aria-label={label}>
      {columns.map((column, at) => <li key={`${at}:${column}`} className="column-chip">
        <code>{column}</code>
        {columns.length > 1 && <>
          <button type="button" className="btn btn-ghost btn-sm" disabled={at === 0} aria-label={`Move ${column} earlier`} onClick={() => move(at, -1)}>←</button>
          <button type="button" className="btn btn-ghost btn-sm" disabled={at === columns.length - 1} aria-label={`Move ${column} later`} onClick={() => move(at, 1)}>→</button>
        </>}
        <button type="button" className="btn btn-ghost btn-sm" aria-label={`Remove ${column}`} onClick={() => onChange(columns.filter((_item, index) => index !== at))}>×</button>
      </li>)}
    </ol>
    {!full && <div className="column-chips-add">
      <input type="text" value={draft} list={suggestions.length ? `${id}-known` : undefined} aria-label={`Add a column to ${label}`} aria-invalid={Boolean(problem)} aria-describedby={problem ? `${id}-problem` : undefined} placeholder="column"
        onChange={event => setDraft(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); add() } }} />
      <button type="button" className="btn btn-secondary btn-sm" disabled={!draft.trim() || Boolean(problem)} onClick={add}>Add</button>
      {suggestions.length > 0 && <datalist id={`${id}-known`}>{suggestions.map(column => <option key={column} value={column}>{column}</option>)}</datalist>}
    </div>}
    {count !== undefined && <small>Takes {count === 1 ? 'one column' : `${count} columns`}.</small>}
    {problem && <small id={`${id}-problem`} role="alert">{problem}</small>}
    {hint}
  </div>
}
