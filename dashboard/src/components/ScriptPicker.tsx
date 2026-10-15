import { useEffect, useId, useMemo, useState } from 'react'

import { useScriptApi } from '../hooks/api/useScriptApi'
import { getErrorMessage } from '../utils/errorUtils'

// Picks one of the project's scripts by name instead of asking for its id.
// When the page already holds the project's scripts the picker searches them
// in place; otherwise it asks the server, a page of names at a time. A chosen
// script stays shown when a later search, or a remount, no longer lists it.

const RESULT_LIMIT = 50
const SEARCH_DELAY_MS = 250
const REMEMBERED_LIMIT = 500

export interface ScriptOption { id: string; name: string; protocol?: string; refKey?: string }

// Scripts pickers have listed, by id, so a picker that opens again with a
// chosen script outside its first page still names it.
const rememberedScripts = new Map<string, ScriptOption>()
function remember(options: ScriptOption[]) {
  for (const option of options) {
    rememberedScripts.delete(option.id)
    rememberedScripts.set(option.id, option)
  }
  while (rememberedScripts.size > REMEMBERED_LIMIT) rememberedScripts.delete(rememberedScripts.keys().next().value as string)
}

function matches(option: ScriptOption, needle: string): boolean {
  return [option.name, option.id, option.refKey || ''].some(text => text.toLowerCase().includes(needle))
}

interface Props {
  projectId: string
  label: string
  value: string
  onChange: (scriptId: string) => void
  disabled?: boolean
  hint?: string
  /** The project's scripts, when the page already holds them. */
  scripts?: ScriptOption[]
}

export default function ScriptPicker({ projectId, label, value, onChange, disabled = false, hint, scripts }: Props) {
  const api = useScriptApi()
  const labelId = useId()
  const hintId = useId()
  const moreId = useId()
  const [query, setQuery] = useState('')
  const [fetched, setFetched] = useState<{ options: ScriptOption[]; total: number }>({ options: [], total: 0 })
  const [loading, setLoading] = useState(!scripts)
  const [error, setError] = useState('')
  const local = Boolean(scripts)

  useEffect(() => {
    if (local) return undefined
    let current = true
    setLoading(true)
    const timer = window.setTimeout(() => {
      api.listScripts(projectId, undefined, { search: query, limit: RESULT_LIMIT })
        .then(response => {
          if (!current) return
          const page = (response.scripts || []).map(script => ({ id: script.id, name: script.name, protocol: script.protocol, refKey: script.ref_key }))
          remember(page)
          setFetched({ options: page, total: Math.max(response.count || 0, page.length) })
          setError('')
        })
        .catch(cause => {
          if (!current) return
          setFetched({ options: [], total: 0 })
          setError(getErrorMessage(cause, 'Scripts could not be loaded.'))
        })
        .finally(() => { if (current) setLoading(false) })
    }, query.trim() ? SEARCH_DELAY_MS : 0)
    return () => { current = false; window.clearTimeout(timer) }
  }, [api, local, projectId, query])

  const localMatches = useMemo(() => {
    if (!scripts) return null
    const needle = query.trim().toLowerCase()
    return needle ? scripts.filter(script => matches(script, needle)) : scripts
  }, [query, scripts])
  const options = localMatches ? localMatches.slice(0, RESULT_LIMIT) : fetched.options
  const total = localMatches ? localMatches.length : fetched.total
  const busyLoading = !localMatches && loading

  // A chosen script the list does not show stays selectable as the first option.
  const chosen = value
    ? options.find(option => option.id === value) || scripts?.find(option => option.id === value) || rememberedScripts.get(value) || { id: value, name: 'The chosen script' }
    : null
  const listed = chosen && !options.some(option => option.id === chosen.id) ? [chosen, ...options] : options
  const emptyLabel = busyLoading && options.length === 0 ? 'Loading scripts…'
    : options.length > 0 ? 'Choose a script'
      : query.trim() ? 'No scripts match the search' : 'No scripts in this project'
  const describedBy = [hint ? hintId : '', total > options.length ? moreId : ''].filter(Boolean).join(' ') || undefined

  return (
    <div className="pp-field script-picker" role="group" aria-labelledby={labelId}>
      <span id={labelId}>{label}</span>
      <div className="script-picker-controls">
        {/* Find first, then choose: the search narrows the list that follows it. */}
        <input
          type="search" autoComplete="off"
          aria-label="Find a script by name"
          placeholder="Find by name"
          value={query}
          disabled={disabled}
          onChange={event => setQuery(event.target.value)}
        />
        <select
          aria-labelledby={labelId}
          aria-describedby={describedBy}
          value={value}
          disabled={disabled}
          onChange={event => {
            const option = listed.find(candidate => candidate.id === event.target.value)
            if (option) remember([option])
            onChange(event.target.value)
          }}
        >
          <option value="">{emptyLabel}</option>
          {listed.map(option => (
            <option key={option.id} value={option.id}>{option.protocol ? `${option.name} · ${option.protocol}` : option.name}</option>
          ))}
        </select>
      </div>
      {hint && <p id={hintId} className="script-picker-hint">{hint}</p>}
      {total > options.length && (
        <p id={moreId} className="script-picker-hint">
          Showing {options.length} of {total} scripts. Find by name to narrow the list.
        </p>
      )}
      {error && <p className="pp-field-error" role="alert">{error}</p>}
    </div>
  )
}
