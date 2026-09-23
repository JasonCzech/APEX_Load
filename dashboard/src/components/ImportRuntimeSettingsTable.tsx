import { useMemo, useState } from 'react'
import ImportCategoryHeading from './ImportCategoryHeading'
import type { ScriptDefinition } from '../types/scripts'
import { CategoryActions, SettingValue, formatSettingValue, useImportSettings } from './ImportSettingsContext'
import { runtimeSettingRows, readSetting, type RuntimeSettingRow } from './importRuntimeSettingsModel'

export default function ImportRuntimeSettingsTable({ script }: { script: ScriptDefinition }) {
 const context = useImportSettings()
 return <RuntimeSettingsWorkspace key={context?.viewGeneration} script={script} />
}

function RuntimeSettingsWorkspace({ script }: { script: ScriptDefinition }) {
 const context = useImportSettings()
 const [selected, setSelected] = useState(() => script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP')
 const [status, setStatus] = useState('All statuses')
 const [search, setSearch] = useState('')
 const groups = useMemo(() => {
  const working = structuredClone(script)
  for (const editor of Object.values(context?.editors || {})) for (const change of Object.values(editor.changes)) {
   if (change.parameter) continue
   const parts = change.path.split('.')
   let object = working as unknown as Record<string, unknown>
   parts.slice(0, -1).forEach(key => { if (!object[key]) object[key] = {}; object = object[key] as Record<string, unknown> })
   object[parts[parts.length - 1]] = change.value
  }
  const result = runtimeSettingRows(working)
  const names: Record<string, string> = { browseremulation: 'Browser emulation', thinktime: 'Think time', runlogic: 'Run logic', runlogicrunroot: 'Run logic', web: 'HTTP' }
  for (const rows of Object.values(result)) for (const row of rows) row.category = names[row.category.replace(/\s/g, '').toLowerCase()] || row.category
  return result
 }, [script, context?.editors])
 const primary = Object.entries(groups).filter(([name]) => !['Preserved only', 'Not applicable'].includes(name))
 const categories = [...new Set(primary.flatMap(([, rows]) => rows.map(row => row.category)))].sort()
 const preferred = script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP'
 const active = selected === 'All categories' || categories.includes(selected) ? selected : categories.includes(preferred) ? preferred : categories[0]
 const query = search.trim().toLowerCase()
 const matches = (row: RuntimeSettingRow) => !query || `${row.category} ${row.label} ${row.imported} ${row.note}`.toLowerCase().includes(query)
 const visible = (rows: RuntimeSettingRow[]) => rows.filter(row => matches(row) && (query || active === 'All categories' || active === row.category))
 const shownGroups = primary.filter(([name]) => status === 'All statuses' || status === name)
 const pending = Object.keys(context?.editors || {}).filter(category => !category.startsWith('parameters:'))
 function table(rows: RuntimeSettingRow[], group: string) {
  if (!rows.length) return <p className="import-review-empty">None.</p>
  return <div className="lr-table-wrap"><table className="import-settings-table import-runtime-table" data-table-pagination="off"><thead><tr><th>Setting</th><th>Imported value</th><th>Current draft value</th></tr></thead>{[...new Set(rows.map(row => row.category))].map(category => <tbody key={category}>
   <tr className="import-category"><th colSpan={3} scope="rowgroup"><ImportCategoryHeading label={category} details={<p>{group === 'Current runtime settings' ? 'Current draft settings. This importer did not report source mappings.' : `${group}. Original source evidence remains unchanged when you edit the draft.`}</p>} /></th></tr>
   {rows.filter(row => row.category === category).map(row => <tr key={row.id}><th scope="row">{row.label}<details className="import-row-details"><summary>Details</summary><div><p>{row.note}</p><p className="import-setting-value">{row.imported}</p>{group === 'Requires decision' && <p>Review this source behavior before replay.</p>}</div></details></th><td className="import-setting-value">{row.importedSummary || row.imported}</td><td>{row.path ? row.field ? <SettingValue category={category} path={row.path} label={`${category}: ${row.label}`} value={readSetting(script, row.path)} type={row.field.type} options={row.field.options} /> : formatSettingValue(readSetting(script, row.path)) : group === 'Not applicable' ? 'Not applicable' : group === 'Mapped for replay' ? 'Used by importer' : group === 'Preserved only' ? 'Preserved only' : 'No exact mapping'}</td></tr>)}
  </tbody>)}</table></div>
 }
 return <div className="import-runtime-workspace"><h3>Runtime settings</h3><p>Compare source and draft values. Applying changes refreshes checks; replay still needs verification.</p>
  <div className="import-settings-toolbar"><label>Find a setting<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search all categories" /></label><label>Status<select value={status} onChange={event => { setStatus(event.target.value); setSelected('All categories') }}>{['All statuses', ...primary.map(([name]) => name)].map(name => <option key={name} value={name}>{name}{groups[name] ? ` (${groups[name].length})` : ''}</option>)}</select></label></div>
  <div className="import-runtime-layout"><nav className="import-category-nav" aria-label="Runtime categories">{['All categories', ...categories].map(category => <button type="button" key={category} aria-pressed={active === category && !query} onClick={() => { setSelected(category); setSearch(''); setStatus('All statuses') }}>{category}<small>{category === 'All categories' ? primary.reduce((count, [, rows]) => count + rows.length, 0) : primary.flatMap(([, rows]) => rows).filter(row => row.category === category).length}{context?.editors[category] ? ' · Editing' : ''}</small></button>)}</nav>
   <div className="import-runtime-content">
    <div className="import-category-editbar">{[...new Set([...shownGroups.flatMap(([, rows]) => visible(rows)).filter(row => row.field).map(row => row.category), ...pending])].map(category => <span key={category}>{pending.includes(category) && <button type="button" className="btn btn-secondary btn-sm" onClick={() => { setSelected(category); setSearch(''); setStatus('All statuses') }}>Review {category} edits{Object.keys(context?.editors[category]?.errors || {}).length ? ' · Errors' : ''}</button>}<CategoryActions category={category} /></span>)}</div>
    {shownGroups.map(([name, rows]) => <section className="import-settings-section" aria-label={name} key={name}><h3>{name} <small>({visible(rows).length})</small></h3>{table(visible(rows), name)}</section>)}
   </div>
  </div>
  {['Preserved only', 'Not applicable'].map(name => <section className="import-settings-section" aria-label={name} key={name}><details className="import-settings-disclosure"><summary><h3>{name} <small>({groups[name].filter(matches).length})</small></h3></summary>{table(groups[name].filter(matches), name)}</details></section>)}
 </div>
}
