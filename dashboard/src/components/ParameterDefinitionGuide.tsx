import { useId, useMemo, useState } from 'react'
import type { ScriptDefinition, ScriptParameterDefinition } from '../types/scripts'

type ParameterSortField = 'name' | 'source' | 'refresh' | 'allocation'
type ParameterSortDirection = 'asc' | 'desc'
type ParameterRefreshFilter = 'all' | 'each_iteration' | 'once' | 'each_occurrence'
type ParameterSourceFilter = 'all' | 'data_source' | 'inline' | 'generator'

function parameterSourceLabel(parameter: ScriptParameterDefinition): string {
  if (parameter.values?.length) return 'Inline values'
  if (parameter.data_source) return `${parameter.data_source}.${parameter.column || ''}`
  return `${parameter.type || 'list'} generator`
}

function parameterSourceKind(parameter: ScriptParameterDefinition): Exclude<ParameterSourceFilter, 'all'> {
  if (parameter.values?.length) return 'inline'
  if (parameter.data_source) return 'data_source'
  return 'generator'
}

function parameterRefreshLabel(parameter: ScriptParameterDefinition): string {
  if (parameter.update === 'once') return 'Once per virtual user'
  if (parameter.update === 'each_occurrence') return 'Every template reference'
  return 'Each iteration'
}

function parameterRefreshKey(parameter: ScriptParameterDefinition): Exclude<ParameterRefreshFilter, 'all'> {
  if (parameter.update === 'once') return 'once'
  if (parameter.update === 'each_occurrence') return 'each_occurrence'
  return 'each_iteration'
}

function parameterAllocationLabel(parameter: ScriptParameterDefinition): string {
  const base = parameter.same_line_as
    ? `Same row as ${parameter.same_line_as}`
    : parameter.block_size
      ? `${parameter.block_size} rows per virtual user`
      : parameter.value_for_each_vuser
        ? 'One row per virtual user'
        : parameter.selection || 'Sequential'
  return `${base}${parameter.start_row ? ` · starts at row ${parameter.start_row}` : ''}`
}

/** Definition provenance is separate from captured values, which can come from an older replay. */
export function ParameterDefinitionGuide({ script }: { script: ScriptDefinition }) {
  const findId = useId()
  const refreshId = useId()
  const sourceId = useId()
  const [query, setQuery] = useState('')
  const [refreshFilter, setRefreshFilter] = useState<ParameterRefreshFilter>('all')
  const [sourceFilter, setSourceFilter] = useState<ParameterSourceFilter>('all')
  const [sortField, setSortField] = useState<ParameterSortField | null>(null)
  const [sortDirection, setSortDirection] = useState<ParameterSortDirection>('asc')

  const parameters = useMemo(() => script.parameters || [], [script.parameters])

  const matches = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase()
    const filtered = parameters.filter(parameter => {
      if (refreshFilter !== 'all' && parameterRefreshKey(parameter) !== refreshFilter) return false
      if (sourceFilter !== 'all' && parameterSourceKind(parameter) !== sourceFilter) return false
      if (!normalizedQuery) return true
      const haystack = `${parameter.name || ''} ${parameterSourceLabel(parameter)} ${parameterRefreshLabel(parameter)} ${parameterAllocationLabel(parameter)}`.toLowerCase()
      return haystack.includes(normalizedQuery)
    })
    if (!sortField) return filtered
    return [...filtered].sort((a, b) => {
      let cmp = 0
      switch (sortField) {
        case 'name':
          cmp = (a.name || '').localeCompare(b.name || '', undefined, { numeric: true, sensitivity: 'base' })
          break
        case 'source':
          cmp = parameterSourceLabel(a).localeCompare(parameterSourceLabel(b), undefined, { numeric: true, sensitivity: 'base' })
          break
        case 'refresh':
          cmp = parameterRefreshLabel(a).localeCompare(parameterRefreshLabel(b), undefined, { numeric: true, sensitivity: 'base' })
          break
        case 'allocation':
          cmp = parameterAllocationLabel(a).localeCompare(parameterAllocationLabel(b), undefined, { numeric: true, sensitivity: 'base' })
          break
      }
      return sortDirection === 'asc' ? cmp : -cmp
    })
  }, [parameters, query, refreshFilter, sortDirection, sortField, sourceFilter])

  const toggleSort = (field: ParameterSortField) => {
    if (sortField === field) {
      setSortDirection(prev => (prev === 'asc' ? 'desc' : 'asc'))
    } else {
      setSortField(field)
      setSortDirection('asc')
    }
  }

  const ariaSortFor = (field: ParameterSortField): 'ascending' | 'descending' | 'none' => {
    if (sortField !== field) return 'none'
    return sortDirection === 'asc' ? 'ascending' : 'descending'
  }

  const sortIndicator = (field: ParameterSortField) => {
    if (sortField !== field) return '↕'
    return sortDirection === 'asc' ? '↑' : '↓'
  }

  return (
    <section className="runtime-field-evidence debug-parameter-sources-panel" aria-label="Parameter sources & refresh cadence">
      <p className="debug-parameter-sources-note">Definitions from the current draft. Captured request and variable evidence belongs to the selected replay and may reflect an earlier draft. Each-occurrence values can differ within one action.</p>
      <div className="debug-session-tools debug-parameter-sources-toolbar">
        <label htmlFor={findId}>
          Find definition
          <input
            id={findId}
            type="search"
            autoComplete="off"
            value={query}
            onChange={event => setQuery(event.target.value)}
            placeholder="Filter by parameter, source, refresh, or allocation"
          />
        </label>
        <label htmlFor={refreshId}>
          Refresh
          <select id={refreshId} aria-label="Refresh" value={refreshFilter} onChange={event => setRefreshFilter(event.target.value as ParameterRefreshFilter)}>
            <option value="all">All ({parameters.length})</option>
            <option value="each_iteration">Each iteration</option>
            <option value="once">Once per virtual user</option>
            <option value="each_occurrence">Every template reference</option>
          </select>
        </label>
        <label htmlFor={sourceId}>
          Source type
          <select id={sourceId} aria-label="Source type" value={sourceFilter} onChange={event => setSourceFilter(event.target.value as ParameterSourceFilter)}>
            <option value="all">All sources</option>
            <option value="data_source">Data source</option>
            <option value="inline">Inline values</option>
            <option value="generator">Generator</option>
          </select>
        </label>
      </div>
      <div className="debug-watch-table-scroll" role="region" aria-label="Parameter sources and refresh cadence" tabIndex={0}>
        <table className="debug-watch-table debug-parameter-sources-table" data-table-pagination="off">
          <caption className="sr-only">Parameter sources and refresh cadence</caption>
          <thead>
            <tr>
              {([['name', 'Parameter'], ['source', 'Value source'], ['refresh', 'Refresh'], ['allocation', 'Allocation']] as const).map(([field, label]) => (
                <th key={field} scope="col" aria-sort={ariaSortFor(field)}>
                  <button type="button" className="debug-table-sort-btn" onClick={() => toggleSort(field)}>
                    <span>{label}</span>
                    <span className="debug-table-sort-icon" aria-hidden="true">{sortIndicator(field)}</span>
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {matches.map((parameter, index) => (
              <tr key={parameter.name || `param-${index}`}>
                <th scope="row"><code>{parameter.name}</code></th>
                <td>{parameter.values?.length ? 'Inline values' : parameter.data_source ? <code>{parameter.data_source}.{parameter.column}</code> : `${parameter.type} generator`}</td>
                <td>{parameterRefreshLabel(parameter)}</td>
                <td>{parameterAllocationLabel(parameter)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!matches.length && <p>{parameters.length ? 'No matching definitions.' : 'No parameters are defined in this script.'}</p>}
    </section>
  )
}
