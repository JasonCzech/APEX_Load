import { useMemo } from 'react'
import { summarizeInlineData } from './scriptEditorUtils'
import type { ScriptDefinition } from '../types/scripts'

export default function ImportParameterInventory({ script }: { script: ScriptDefinition }) {
  const sources = useMemo(() => new Map((script.data_sources || []).map(source => [source.name, summarizeInlineData(source)])), [script.data_sources])
  const parameters = script.parameters || []
  return <section className="lr-parameter-inventory" aria-label="Imported parameters and data">
    <h3>Imported parameters & data</h3>
    <p>Check selection and exhaustion behavior before planning capacity. Values remain protected; the allocation preview calculates usable rows on the server.</p>
    {!parameters.length ? <p>No parameter definitions were imported.</p> : <div className="lr-table-wrap"><table className="lr-parameter-table"><thead><tr><th>Parameter</th><th>Source</th><th>Available rows</th><th>Selection / update</th><th>Start row</th><th>Allocation</th><th>On exhaustion</th></tr></thead><tbody>{parameters.map(p => <tr key={p.name}><th scope="row">{p.name}</th><td>{p.data_source || p.type}{p.column ? ` · ${p.column}` : ''}</td><td>{p.values?.length ?? sources.get(p.data_source || '')?.rows ?? 'Server preview required'}</td><td>{p.selection || 'Default'} / {p.update || 'Default'}</td><td>{p.start_row ?? 1}</td><td>{p.block_size ? `${p.block_size} rows per VUser` : p.auto_allocate ? 'Automatic — preview below' : 'Default'}</td><td>{p.on_exhaustion || 'Default'}</td></tr>)}</tbody></table></div>}
    <details><summary>Data sources ({script.data_sources?.length || 0})</summary><ul>{script.data_sources?.map(source => <li key={source.name}><strong>{source.name}</strong> · {source.file_name || source.path || source.type} · {source.columns?.length || 0} declared columns</li>)}</ul></details>
  </section>
}
