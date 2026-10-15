import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import TabList from './ux/TabList'
import { Fragment, useId, useMemo, useState } from 'react'
import { isSensitiveCredentialPreviewName, isSensitiveCredentialPreviewKey } from './credentialPreviewPolicy'
import type { ScriptDefinition, ScriptParameterDefinition } from '../types/scripts'
import { getImportSummary, findImportedParameterMappingDataSourceIndex, type ImportParameterFileSection } from './scriptEditorImportModel'
import { inferDelimiter } from './scriptEditorUtils'
import { CategoryActions, SettingValue, formatSettingValue, useImportSettings, type ImportDataView } from './ImportSettingsContext'

const generatorFields: Record<string, string[]> = {
 random_number: ['min', 'max', 'seed', 'format'], counter: ['start', 'end', 'step', 'pad_width', 'pad_char'],
 date_time: ['format', 'timezone', 'offset_seconds'], time: ['format', 'timezone', 'offset_seconds'], timestamp: ['format'],
 random_string: ['length', 'charset', 'seed'], current_iteration: ['pad_width', 'pad_char'], vuser_id: ['pad_width', 'pad_char'],
}
const textFields = new Set(['format', 'timezone', 'charset', 'pad_char', 'same_line_as'])
function parameterValueProtected(parameter: ScriptParameterDefinition) {
 return isSensitiveCredentialPreviewName(parameter.name) || isSensitiveCredentialPreviewName(parameter.column || '') || /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(JSON.stringify(parameter))
}
function parameterSourceSections(sections: ImportParameterFileSection[], parameter: ScriptParameterDefinition) {
 return sections.filter(section => section.name.replace(/^parameter[:\s]+/i, '').replace(/["']/g, '').toLowerCase() === parameter.name.toLowerCase())
}
function parameterEvidence(section: ImportParameterFileSection): string {
 const protectedSection = isSensitiveCredentialPreviewName(section.name.replace(/^parameter[:\s]+/i, '').replace(/["']/g, ''))
 return Object.entries(section.values).map(([key, value]) => `${key}: ${protectedSection || isSensitiveCredentialPreviewKey(key) ? 'Protected — manage in Credentials' : value}`).join(' · ')
}
export default function ImportParameterInventory({ script }: { script: ScriptDefinition }) {
 const context = useImportSettings()
 return <ParameterInventoryWorkspace key={context?.viewGeneration} script={script} />
}

function ParameterInventoryWorkspace({ script }: { script: ScriptDefinition }) {
 const viewId = useId()
 const context = useImportSettings()
 const [localView, setLocalView] = useState<ImportDataView>('parameters')
 const view = context?.dataView || localView
 const setView = context?.setDataView || setLocalView
 const [search, setSearch] = useState('')
 const [typeFilter, setTypeFilter] = useState('')
 const [sourceFilter, setSourceFilter] = useState('')
 const [attention, setAttention] = useState(false)
 const [editing, setEditing] = useState('')
 const sources = useMemo(() => script.data_sources || [], [script.data_sources])
 const summaries = useMemo(() => new Map(sources.map(source => [source.name, sourceRowCount(source)])), [sources])
 const parameters = (script.parameters || []).map(parameter => {
  const changes = context?.editors[`parameters:${parameter.type}`]?.changes || {}
  return Object.values(changes).filter(change => change.parameter === parameter.name).reduce((result, change) => ({ ...result, [change.path]: change.value ?? undefined }), parameter)
 })
 const summary = useMemo(() => getImportSummary(script.metadata?.import), [script.metadata?.import])
 const sections = summary.parameterFileSections
 const unmatched = sections.filter(section => !parameters.some(parameter => parameterSourceSections(sections, parameter).some(s => s.file === section.file && s.name === section.name)))
 const needsAttention = (p: ScriptParameterDefinition) => (p.auto_allocate && p.selection !== 'unique') || (p.type === 'file' && (!p.column || !sources.some(source => source.name === p.data_source) || (columnsForSource(sources.find(source => source.name === p.data_source)).length > 0 && !columnsForSource(sources.find(source => source.name === p.data_source)).includes(p.column)))) || Object.keys(context?.editors[`parameters:${p.type}`]?.errors || {}).some(key => key.startsWith(`${p.name}.`))
 const filtered = parameters.filter(p => (!typeFilter || p.type === typeFilter) && (!sourceFilter || p.data_source === sourceFilter) && (!attention || needsAttention(p)) && `${p.name} ${p.type} ${p.data_source || ''} ${p.column || ''}`.toLowerCase().includes(search.trim().toLowerCase()))
 const types = [...new Set(parameters.map(p => p.type))]
 const pendingTypes = types.filter(type => context?.editors[`parameters:${type}`])
 function field(p: ScriptParameterDefinition, key: string, options?: string[], type?: string) {
  const label = key.replace(/_/g, ' ')
  if (key === 'values' && parameterValueProtected(script.parameters?.find(parameter => parameter.name === p.name) || p)) return <span key={key}>Protected — manage in Credentials</span>
  return <div key={key}><small>{label}</small><SettingValue category={`parameters:${p.type}`} parameter={p.name} path={key} label={`${p.name}: ${label}`} value={(script.parameters?.find(parameter => parameter.name === p.name) || p)[key as keyof ScriptParameterDefinition] ?? (key === 'start_row' ? 1 : undefined)} type={options ? 'select' : type || (textFields.has(key) ? 'text' : 'signed')} options={options} /></div>
 }
 function valueSummary(p: ScriptParameterDefinition) {
  if (parameterValueProtected(p)) return 'Protected — manage in Credentials'
  if (p.type === 'file') return `Sample: ${sampleForParameter(script, p)}`
  if (p.type === 'list') return `${(p.values || []).slice(0, 2).join(', ') || '(empty list)'}${(p.values?.length || 0) > 2 ? ` · +${p.values!.length - 2} values` : ''}`
  return (generatorFields[p.type] || []).filter(key => p[key as keyof ScriptParameterDefinition] != null).map(key => `${key.replace(/_/g, ' ')}: ${formatSettingValue(p[key as keyof ScriptParameterDefinition])}`).join(' · ') || `Generated at replay (${p.type.replace(/_/g, ' ')})`
 }
 const allocation = (p: ScriptParameterDefinition) => `${['file', 'list'].includes(p.type) ? `Row ${p.start_row ?? 1}${p.selection === 'unique' ? p.auto_allocate ? ' · Auto allocation' : ` · Block ${p.block_size ?? 'default'}` : ''}${p.same_line_as ? ` · Same row as ${p.same_line_as}` : ''} · ` : ''}${p.on_exhaustion || 'wrap'} on exhaustion${p.on_exhaustion ? '' : ' (default)'}`
 return <section className="import-settings-section lr-parameter-inventory" aria-label="Imported parameters and data">
  <ImportReviewSectionHeader title="Imported parameters & data" description="Edit imported definitions, inspect source files, or preview row allocation. Replay issues are tracked separately from definition checks." />
  <TabList className="import-data-nav" aria-label="Data views">{(['parameters', 'sources', 'allocation'] as const).map(name => <button type="button" key={name} id={`${viewId}-${name}`} role="tab" aria-selected={view === name} aria-controls={`${viewId}-panel`} tabIndex={view === name ? 0 : -1} onClick={() => setView(name)}>{name === 'parameters' ? `Parameters (${parameters.length})` : name === 'sources' ? `Data sources (${sources.length})` : 'Allocation preview'}</button>)}</TabList>
  <div role="tabpanel" id={`${viewId}-panel`} aria-labelledby={`${viewId}-${view}`}>
  {pendingTypes.length > 0 && <div className="import-category-editbar" aria-label="Pending parameter changes">{pendingTypes.map(type => <span key={type}><button className="btn btn-secondary btn-sm" type="button" onClick={() => { setView('parameters'); setTypeFilter(type); setSourceFilter(''); setSearch(''); setAttention(false); const errors = Object.keys(context?.editors[`parameters:${type}`]?.errors || {}); setEditing(parameters.find(p => p.type === type && errors.some(key => key.startsWith(`${p.name}.`)))?.name || parameters.find(p => p.type === type)?.name || '') }}>Review {type.replace(/_/g, ' ')} edits</button><CategoryActions category={`parameters:${type}`} /></span>)}</div>}
  {view === 'parameters' && <>
   <div className="import-review-toolbar"><label>Find a parameter<input type="search" autoComplete="off" value={search} onChange={event => setSearch(event.target.value)} placeholder="Name or source column" /></label><label>Type<select value={typeFilter} onChange={event => setTypeFilter(event.target.value)}><option value="">All types</option>{types.map(type => <option key={type} value={type}>{type.replace(/_/g, ' ')}</option>)}</select></label><label>Source<select value={sourceFilter} onChange={event => setSourceFilter(event.target.value)}><option value="">All sources</option>{sources.map(source => <option key={source.name}>{source.name}</option>)}</select></label><label className="import-attention-filter"><input type="checkbox" checked={attention} onChange={event => setAttention(event.target.checked)} />Definition issues ({parameters.filter(needsAttention).length})</label></div>
   <p className="import-settings-hint">{filtered.length} of {parameters.length} parameters. Samples are examples, not the next replay value.</p>
   {!filtered.length ? <p>{parameters.length ? 'No parameters match these filters.' : 'No parameter definitions were imported.'}</p> : <div className="lr-table-wrap"><table className="import-settings-table import-parameters-table" data-table-pagination="off"><thead><tr><th>Parameter</th><th>Current value / rule</th><th>Source mapping</th><th>Selection / update</th><th>Allocation</th><th>Actions</th></tr></thead>{types.filter(type => filtered.some(p => p.type === type)).map(type => <tbody key={type}>
    <tr className="import-category"><th colSpan={6} scope="rowgroup">{type.replace(/_/g, ' ')} parameters <small>({filtered.filter(p => p.type === type).length})</small></th></tr>
    {filtered.filter(p => p.type === type).map(p => {
     const editor = context?.editors[`parameters:${type}`]
     const open = !!editor && editing === p.name
     const changed = Object.values(editor?.changes || {}).some(change => change.parameter === p.name)
     const evidence = parameterSourceSections(sections, p)
     return <Fragment key={p.name}><tr><th scope="row">{p.name}{changed && <small>Unsaved changes</small>}{needsAttention(p) && <small>Needs attention</small>}</th><td><span className="import-compact-value" title={valueSummary(p)}>{valueSummary(p)}</span>{p.type === 'list' && (p.values?.length || 0) > 2 && !parameterValueProtected(p) && <details className="import-row-details"><summary>All values</summary><p className="import-setting-value">{p.values?.join('\n')}</p></details>}</td><td>{p.type === 'file' ? <>{p.data_source || 'Unmapped'} · {p.column || 'No column'}</> : '—'}{evidence.length > 0 && <details className="import-row-details"><summary>Source evidence</summary>{evidence.map(section => <p key={`${section.file}:${section.name}`}>{section.file} · {section.name}<br />{parameterEvidence(section)}</p>)}</details>}</td><td>{p.selection || 'sequential'}{!p.selection && ' (default)'}<small>{(p.update || 'each_iteration').replace(/_/g, ' ')}{!p.update && ' (default)'}</small></td><td>{allocation(p)}</td><td><button className="btn btn-secondary btn-sm" type="button" id={`${viewId}-edit-${p.name}`} aria-label={`Edit ${p.name}`} aria-expanded={open} disabled={!context || context.disabled || context.busy} onClick={() => { context?.begin(`parameters:${type}`); setEditing(open ? '' : p.name) }}>{open ? 'Close' : 'Edit'}</button></td></tr>
      {open && <tr className="import-parameter-editor"><td colSpan={6}><div className="import-parameter-editor-heading"><strong>Edit {p.name}</strong><span>Apply or cancel {type.replace(/_/g, ' ')} changes above.</span></div><div className="import-parameter-edit-fields">
       {type === 'list' ? field(p, 'values', undefined, 'lines') : type === 'file' ? <>{field(p, 'data_source', sources.map(source => source.name))}{field(p, 'column', columnsForSource(sources.find(source => source.name === p.data_source)))}<p>{summaries.get(p.data_source || '') ?? 'Unknown'} available rows<br />Sample: {sampleForParameter(script, p)}</p></> : generatorFields[type]?.length ? generatorFields[type].map(key => field(p, key)) : <span>Generated at replay ({type.replace(/_/g, ' ')})</span>}
       {field(p, 'selection', ['sequential', 'random', 'unique'])}{field(p, 'update', ['once', 'each_iteration', 'each_occurrence'])}
       {['file', 'list'].includes(type) && <>{field(p, 'start_row', undefined, 'number')}{(p.selection === 'unique' || p.auto_allocate || editor?.errors[`${p.name}.block_size`] || editor?.errors[`${p.name}.auto_allocate`]) && <>{p.auto_allocate && p.selection !== 'unique' && <p>Automatic allocation requires unique selection. Disable it to use this selection rule.</p>}{field(p, 'block_size', undefined, 'number')}{field(p, 'auto_allocate', undefined, 'boolean')}</>}{field(p, 'same_line_as', parameters.filter(other => other.name !== p.name).map(other => other.name))}</>}{field(p, 'on_exhaustion', ['wrap', 'empty', 'error', 'last'])}
      </div></td></tr>}
     </Fragment>
    })}</tbody>)}</table></div>}
   {unmatched.length > 0 && <details className="import-settings-disclosure"><summary>Unmapped parameter source sections ({unmatched.length})</summary><section aria-label="Unmapped parameter source sections"><div className="lr-table-wrap"><table className="import-settings-table" data-table-pagination="off"><thead><tr><th>Source section</th><th>Source evidence</th><th>Data source</th></tr></thead><tbody>{unmatched.map(section => { const index = findImportedParameterMappingDataSourceIndex(sources, section.values); return <tr key={`${section.file}:${section.name}`}><th scope="row">{section.file} · {section.name}<small>Unmapped parameter definition</small></th><td>{parameterEvidence(section)}</td><td>{index == null ? 'No matching source' : sources[index]?.name}</td></tr> })}</tbody></table></div></section></details>}
   {summary.correlationSuggestions.length > 0 && <details className="import-settings-disclosure"><summary>Correlation suggestions ({summary.correlationSuggestions.length})</summary><div className="lr-table-wrap"><table className="import-settings-table" data-table-pagination="off"><thead><tr><th>Name</th><th>Expression</th><th>Source / use</th></tr></thead><tbody>{summary.correlationSuggestions.map((c, index) => <tr key={`${c.name}:${index}`}><th scope="row">{c.name}</th><td>{c.expression}</td><td>{c.source}<small>Found in {c.found_at}</small><small>Used in {c.used_at.join(', ')}</small></td></tr>)}</tbody></table></div></details>}
  </>}
  {view === 'sources' && <><h3>Data sources</h3><p className="import-settings-hint">Source files are read-only. Edit column mappings in Parameters.</p>{!sources.length ? <p>No data sources were imported.</p> : <div className="lr-table-wrap"><table className="import-settings-table" data-table-pagination="off"><thead><tr><th>Source</th><th>Format</th><th>Available rows</th><th>Columns</th><th>Mapped parameters</th></tr></thead><tbody>{sources.map(source => <tr key={source.name}><th scope="row">{source.name}<small>{source.file_name || source.path}</small></th><td>{source.type}</td><td>{source.inline_data != null ? summaries.get(source.name) ?? 'Unavailable' : 'Unavailable until loaded'}</td><td>{columnsForSource(source).join(', ') || 'Not declared'}</td><td>{parameters.filter(p => p.data_source === source.name).map(p => p.name).join(', ') || 'None'}</td></tr>)}</tbody></table></div>}</>}
  {view === 'allocation' && <details className="import-settings-disclosure"><summary>Allocation rules ({parameters.length})</summary><p className="import-settings-hint">Configured rules, including pending edits. Apply changes before previewing allocation.</p>{parameters.length ? <div className="lr-table-wrap"><table className="import-settings-table" data-table-pagination="off"><thead><tr><th>Parameter</th><th>Selection / update</th><th>Allocation / exhaustion</th><th>Actions</th></tr></thead><tbody>{parameters.map(p => <tr key={p.name}><th scope="row">{p.name}</th><td>{p.selection || 'sequential'} · {(p.update || 'each_iteration').replace(/_/g, ' ')}</td><td>{allocation(p)}</td><td><button type="button" className="btn btn-secondary btn-sm" disabled={!context || context.disabled || context.busy} onClick={() => {
     setView('parameters'); setTypeFilter(''); setSourceFilter(''); setSearch(''); setAttention(false)
     context?.begin(`parameters:${p.type}`); setEditing(p.name)
     requestAnimationFrame(() => document.getElementById(`${viewId}-edit-${p.name}`)?.focus())
    }}>Edit {p.name} rules</button></td></tr>)}</tbody></table></div> : <p>No parameter allocation rules.</p>}</details>}
  </div>
 </section>
}
// Bounded, quote-aware samples; never open external files from the browser.
function sourceSampleRows(source?: NonNullable<ScriptDefinition['data_sources']>[number], limit = 4): string[][] {
 if (!source?.inline_data || !['csv', 'dat'].includes(source.type) || source.encoding === 'base64' || source.inline_data.length > 1024 * 1024) return []
 const delimiter = inferDelimiter(source)
 const rows: string[][] = []; let row: string[] = []; let cell = ''; let quoted = false; let started = false
 for (let i = 0; i < source.inline_data.length; i++) {
  const ch = source.inline_data[i]
  if (ch === '"') { started = true; if (quoted && source.inline_data[i + 1] === '"') { cell += '"'; i++ } else quoted = !quoted }
  else if (ch === delimiter && !quoted) { row.push(cell); cell = '' }
  else if ((ch === '\n' || ch === '\r') && !quoted) { if (ch === '\r' && source.inline_data[i + 1] === '\n') i++; if (started || cell || row.length) { row.push(cell); rows.push(row) }; row = []; cell = ''; started = false; if (rows.length >= limit) break }
  else cell += ch
 }
 if (started || cell || row.length) rows.push([...row, cell])
 return rows
}
function sourceRowCount(source: NonNullable<ScriptDefinition['data_sources']>[number]): number | undefined {
 if (source.inline_data == null || !['csv', 'dat'].includes(source.type) || source.encoding === 'base64' || source.inline_data.length > 1024 * 1024) return undefined
 const rows = sourceSampleRows(source, Infinity)
 return Math.max(0, rows.length - (source.first_row_header ? 1 : 0))
}
function columnsForSource(source?: NonNullable<ScriptDefinition['data_sources']>[number]): string[] {
 if (!source) return []
 const first = sourceSampleRows(source)[0]
 const columns = source.first_row_header ? first || source.columns || [] : source.columns?.length ? source.columns : first?.map((_, index) => `col_${index + 1}`) || []
 return columns.map(column => column.trim())
}
function sampleForParameter(script: ScriptDefinition, parameter: ScriptParameterDefinition): string {
 if (isSensitiveCredentialPreviewName(parameter.name) || isSensitiveCredentialPreviewName(parameter.column || '')) return 'Protected — manage in Credentials'
 const source = script.data_sources?.find(s => s.name === parameter.data_source)
 if (!source) return 'Unavailable'
 const columns = columnsForSource(source)
 const index = columns.indexOf(parameter.column || '')
 if (index < 0) return 'Select a declared column'
 const rows = sourceSampleRows(source).slice(source.first_row_header ? 1 : 0)
 return rows.slice(0, 2).map(row => /\[REDACTED\]|\[MASKED\]|<redacted>/i.test(row[index] || '') ? 'Protected' : row[index]?.trim() || '(empty)').join(', ') || 'Unavailable until loaded'
}
