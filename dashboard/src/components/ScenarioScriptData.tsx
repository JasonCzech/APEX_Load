import type { ScriptDataSource, ScriptParameterDefinition } from '../types/scripts'
import type { StoredScript } from '../types'
import './ScenarioScriptData.css'

function label(value: string) {
  const text = value.replace(/_/g, ' ')
  return text.charAt(0).toUpperCase() + text.slice(1)
}

function SourcePreview({ source }: { source: ScriptDataSource }) {
  const inline = source.inline_data || ''
  const isInline = !source.staged_asset && Boolean(inline.trim())
  const location = source.staged_asset || (isInline ? 'Stored in script' : source.path || source.storage_ref || 'Not configured')
  // Show the original text so quoted fields and multiline CSV values stay intact.
  const preview = isInline && source.encoding?.trim().toLowerCase() !== 'base64' ? inline.slice(0, 4000).split(/\r?\n/).slice(0, 10).join('\n') : ''
  return <details className="scenario-script-source">
    <summary><strong>{source.name}</strong><span>{source.type.toUpperCase()} · {source.type === 'file' ? 'File asset' : label(source.strategy || 'sequential')} · {isInline ? 'Inline data' : 'External data'}</span></summary>
    <dl className="scenario-script-data-settings">
      <div><dt>Location</dt><dd>{location}</dd></div>
      {source.type !== 'file' && <><div><dt>Header row</dt><dd>{source.first_row_header ? 'First row contains column names' : 'No header row'}</dd></div>
      <div><dt>Delimiter</dt><dd>{source.delimiter === '\t' || source.delimiter === 'tab' ? 'Tab' : source.delimiter === ',' || source.delimiter === 'comma' ? 'Comma' : source.delimiter || 'Auto-detect'}</dd></div></>}
      {source.type === 'file' && <div><dt>Encoding</dt><dd>{source.encoding || 'Text (default)'}</dd></div>}
      {!!source.columns?.length && <div><dt>Columns</dt><dd>{source.columns.join(', ')}</dd></div>}
    </dl>
    {preview ? <><p>Data preview · first 10 lines, up to 4,000 characters. Values may contain sensitive test data.</p><pre tabIndex={0} aria-label={`Data preview for ${source.name}`}>{preview}</pre></>
      : source.staged_asset ? <p>The staged asset takes precedence over the script’s inline copy. Its contents are not loaded here.</p>
        : isInline ? <p>Encoded file contents are not previewed here.</p>
          : <p>External data contents are not loaded here. Availability is checked before execution.</p>}
  </details>
}

function ParameterSettings({ parameter }: { parameter: ScriptParameterDefinition }) {
  const settings: string[] = []
  const rowBased = parameter.type === 'file' || parameter.type === 'list'
  if (parameter.same_line_as) settings.push(`Same row as ${parameter.same_line_as}`)
  else if (rowBased && (parameter.data_source || parameter.values?.length)) settings.push(`${label(parameter.selection || 'sequential')} selection`)
  if ((rowBased && (parameter.data_source || parameter.values?.length)) || parameter.type === 'counter') settings.push(`On exhaustion: ${label(parameter.on_exhaustion || 'wrap')}`)
  if (parameter.start_row !== undefined) settings.push(`Start row: ${parameter.start_row}`)
  if (parameter.auto_allocate) settings.push('Auto-allocate rows')
  if (parameter.block_size !== undefined) settings.push(`Block size: ${parameter.block_size}`)
  if (parameter.value_for_each_vuser) settings.push('Value per VUser')
  for (const field of ['min', 'max', 'start', 'end', 'step', 'length', 'format', 'timezone', 'offset_seconds', 'charset', 'pad_width', 'pad_char', 'seed'] as const) {
    if (parameter[field] !== undefined) settings.push(`${label(field)}: ${parameter[field]}`)
  }
  return <>{settings.length ? settings.join(' · ') : 'Type defaults'}</>
}

export default function ScenarioScriptData({ scripts, details, loading, unavailable, onRetry }: {
  scripts: StoredScript[]; details: StoredScript[]; loading: boolean; unavailable: number; onRetry: () => void
}) {
  const sources = details.reduce((count, script) => count + (script.script?.data_sources?.length || 0), 0)
  const parameters = details.reduce((count, script) => count + (script.script?.parameters?.length || 0), 0)
  return <section className="scenario-script-data" aria-label="Script data & parameters">
    <div className="scenario-wizard-panel-header">
      <h4>Script data &amp; parameters</h4>
      <p>No shared data pools are attached. Each script uses its own data sources and parameter settings. This is a read-only view; edit these settings in Scripts.</p>
    </div>
    {!scripts.length ? <p>Select scripts in the Start step to review their data and parameters.</p>
      : loading ? <p role="status">Loading script data and parameters…</p> : <>
        <dl className="scenario-script-data-summary" aria-label="Data settings summary">
          <div><dt>Data scope</dt><dd>Script-level</dd></div>
          <div><dt>Scripts inspected</dt><dd>{details.length} of {scripts.length}</dd></div>
          <div><dt>Data sources</dt><dd>{sources}</dd></div>
          <div><dt>Parameters</dt><dd>{parameters}</dd></div>
        </dl>
        {unavailable > 0 && <p role="status">Some script data could not be loaded. Counts include inspected scripts only. <button type="button" className="btn btn-sm btn-secondary" onClick={onRetry}>Retry script data</button></p>}
        {scripts.map(script => {
          const definition = details.find(detail => detail.id === script.id)?.script
          return <details className="scenario-script-data-item" key={script.id} open={scripts.length === 1}>
            <summary><strong>{script.name}</strong><span>{definition ? `${definition.data_sources?.length || 0} sources · ${definition.parameters?.length || 0} parameters` : 'Data unavailable'}</span></summary>
            {!definition ? <p>Unable to inspect this script. Retry or review it in Scripts before running.</p> : <>
              {!definition.data_sources?.length && !definition.parameters?.length && <p>No script-level data sources or parameters are configured.</p>}
              {!!definition.data_sources?.length && <div className="scenario-script-sources"><h5>Data sources</h5>{definition.data_sources.map((source, index) => <SourcePreview key={index} source={source} />)}</div>}
              {!!definition.parameters?.length && <div className="scenario-script-parameters"><h5>Parameters</h5><div className="scenario-script-data-table" role="region" aria-label={`Parameters for ${script.name}`} tabIndex={0}><table>
                <thead><tr><th scope="col">Parameter</th><th scope="col">Source / value</th><th scope="col">Update</th><th scope="col">Settings</th></tr></thead>
                <tbody>{definition.parameters.map((parameter, index) => <tr key={index}>
                  <th scope="row"><code>{parameter.name}</code><small>{label(parameter.type)}</small></th>
                  <td>{parameter.type === 'host' && parameter.values?.some(value => value.trim()) ? 'Host override configured'
                    : (parameter.type === 'file' || parameter.type === 'list') && parameter.values?.length ? <details><summary>{parameter.values.length} configured values</summary><p>Values may contain sensitive test data.</p><pre>{parameter.values.slice(0, 5).map(value => value.slice(0, 500)).join('\n')}</pre><small>First 5 values, up to 500 characters each.</small></details>
                      : (parameter.type === 'file' || parameter.type === 'list') && parameter.data_source ? <><code>{parameter.data_source}</code><br />{parameter.column || 'No column selected'}</>
                      : parameter.type === 'file' ? 'No data source configured' : parameter.type === 'list' ? 'No values configured' : 'Generated at runtime'}</td>
                  <td>{label(parameter.update || 'each_iteration')}</td><td><ParameterSettings parameter={parameter} /></td>
                </tr>)}</tbody>
              </table></div></div>}
            </>}
          </details>
        })}
      </>}
  </section>
}
