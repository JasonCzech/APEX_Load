import { useState } from 'react'
import type { ScriptDefinition } from '../types/scripts'

/** Definition provenance is separate from captured values, which can come from an older replay. */
export function ParameterDefinitionGuide({ script }: { script: ScriptDefinition }) {
  const [query, setQuery] = useState('')
  if (!script.parameters?.length) return null
  const matches = script.parameters.filter(parameter => parameter.name.toLowerCase().includes(query.toLowerCase()))
  return <details className="runtime-field-evidence"><summary>Parameter sources &amp; refresh cadence</summary>
    <p>Definitions from the current draft. Captured request and variable evidence belongs to the selected replay and may reflect an earlier draft. Each-occurrence values can differ within one action.</p>
    <label>Find definition<input type="search" autoComplete="off" value={query} onChange={event => setQuery(event.target.value)} /></label>
    <table data-table-pagination="off"><thead><tr><th>Parameter</th><th>Value source</th><th>Refresh</th><th>Allocation</th></tr></thead><tbody>{matches.slice(0, 100).map(parameter => <tr key={parameter.name}>
      <th><code>{parameter.name}</code></th><td>{parameter.values?.length ? 'Inline values' : parameter.data_source ? <code>{parameter.data_source}.{parameter.column}</code> : `${parameter.type} generator`}</td>
      <td>{parameter.update === 'once' ? 'Once per virtual user' : parameter.update === 'each_occurrence' ? 'Every template reference' : 'Each iteration'}</td>
      <td>{parameter.same_line_as ? `Same row as ${parameter.same_line_as}` : parameter.block_size ? `${parameter.block_size} rows per virtual user` : parameter.value_for_each_vuser ? 'One row per virtual user' : parameter.selection || 'Sequential'}{parameter.start_row ? ` · starts at row ${parameter.start_row}` : ''}</td>
    </tr>)}</tbody></table>
    {matches.length > 100 && <p>Showing the first 100 definitions. Refine the search.</p>}
    {!matches.length && <p>No matching definitions.</p>}
  </details>
}
