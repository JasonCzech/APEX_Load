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
 const [hiddenStatuses, setHiddenStatuses] = useState<string[]>([])
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
  const names: Record<string, string> = { browseremulation: 'Browser emulation', thinktime: 'Think time', web: 'HTTP' }
  for (const rows of Object.values(result)) for (const row of rows) {
   const key = row.category.replace(/\s/g, '').toLowerCase()
   const category = key.startsWith('runlogic') || key === 'profileactions' ? 'Run logic' : names[key] || row.category
   if (category === 'Run logic' && row.category !== category) row.note = `Source section: ${row.category}. ${row.note}`
   row.category = category
  }
  return result
 }, [script, context?.editors])
 const orderedGroups = ['Requires decision', 'Mapped for replay', 'Additional APEX settings', 'Preserved only', 'Not applicable'].filter(name => groups[name]?.length).map(name => [name, groups[name]] as const)
 const categories = [...new Set(orderedGroups.flatMap(([, rows]) => rows.map(row => row.category)))].sort()
 const preferred = script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP'
 const active = selected === 'All categories' || categories.includes(selected) ? selected : categories.includes(preferred) ? preferred : categories[0]
 const query = search.trim().toLowerCase()
 const matches = (row: RuntimeSettingRow) => !query || `${row.category} ${row.label} ${row.imported} ${row.note}`.toLowerCase().includes(query)
 const visible = (rows: RuntimeSettingRow[]) => rows.filter(row => matches(row) && (query || active === 'All categories' || active === row.category))
 const enabledGroups = orderedGroups.filter(([name]) => !hiddenStatuses.includes(name))
 const enabledRows = enabledGroups.flatMap(([, rows]) => rows)
 const shownGroups = enabledGroups.map(([name, rows]) => [name, visible(rows)] as const).filter(([, rows]) => rows.length)
 const pending = Object.keys(context?.editors || {}).filter(category => !category.startsWith('parameters:'))
 function table(rows: RuntimeSettingRow[], group: string) {
  return <div className="lr-table-wrap"><table className="import-settings-table import-runtime-table" data-table-pagination="off"><thead><tr><th>Setting</th><th>Imported value</th><th>Current draft value</th><th>Details</th></tr></thead>{[...new Set(rows.map(row => row.category))].map(category => <tbody key={category}>
   <tr className="import-category"><th colSpan={4} scope="rowgroup"><ImportCategoryHeading label={category} details={<p>{group === 'Additional APEX settings' ? 'Draft settings without a reported source mapping.' : `${group}. Original source evidence remains unchanged when you edit the draft.`}</p>} /></th></tr>
   {rows.filter(row => row.category === category).map(row => <tr key={row.id}><th scope="row">{row.label}</th><td className="import-setting-value">{row.importedSummary || row.imported}</td><td>{row.path ? row.field ? <SettingValue category={category} path={row.path} label={`${category}: ${row.label}`} value={readSetting(script, row.path)} type={row.field.type} options={row.field.options} /> : formatSettingValue(readSetting(script, row.path)) : group === 'Not applicable' ? 'Not applicable' : group === 'Mapped for replay' ? 'Used by importer' : group === 'Preserved only' ? 'Preserved only' : 'No exact mapping'}</td><td><details className="import-row-details"><summary>Details</summary><div><p>{row.note}</p><p className="import-setting-value">{row.imported}</p>{group === 'Requires decision' && <p>Review this source behavior before replay.</p>}</div></details></td></tr>)}
  </tbody>)}</table></div>
 }
 return <div className="import-runtime-workspace"><h3>Runtime settings</h3><p>Compare source and draft values. Applying changes refreshes checks; replay still needs verification.</p>
  <div className="import-runtime-summary" role="group" aria-label="Overall runtime setting counts">{orderedGroups.map(([name, rows]) => <label key={name} className={hiddenStatuses.includes(name) ? 'is-hidden' : undefined}><input type="checkbox" aria-label={name === 'Mapped for replay' ? 'Mapped' : name} checked={!hiddenStatuses.includes(name)} onChange={() => setHiddenStatuses(previous => previous.includes(name) ? previous.filter(value => value !== name) : [...previous, name])} /><span>{name === 'Mapped for replay' ? 'Mapped' : name}</span><strong>{rows.length}</strong></label>)}</div>
  <div className="import-settings-toolbar"><label>Find a setting<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search all categories" /></label></div>
  <div className="import-runtime-layout"><nav className="import-category-nav" aria-label="Runtime categories">{['All categories', ...categories].map(category => <button type="button" key={category} aria-pressed={active === category && !query} onClick={() => { setSelected(category); setSearch('') }}>{category}<small>{category === 'All categories' ? enabledRows.length : enabledRows.filter(row => row.category === category).length}{context?.editors[category] ? ' · Editing' : ''}</small></button>)}</nav>
   <div className="import-runtime-content">
    <div className="import-category-editbar">{[...new Set([...shownGroups.flatMap(([, rows]) => rows).filter(row => row.field).map(row => row.category), ...pending])].map(category => <span key={category}>{pending.includes(category) && <button type="button" className="btn btn-secondary btn-sm" onClick={() => { setSelected(category); setSearch(''); setHiddenStatuses([]) }}>Review {category} edits{Object.keys(context?.editors[category]?.errors || {}).length ? ' · Errors' : ''}</button>}<CategoryActions category={category} /></span>)}</div>
    {!shownGroups.length && <p className="import-review-empty" role="status">No settings match these filters. Enable a mapping status, choose another category, or change your search.</p>}
    {shownGroups.map(([name, rows]) => <section className="import-settings-section" aria-label={name} key={name}><h3>{name} <small>({rows.length})</small></h3>{name === 'Additional APEX settings' && <p className="import-settings-hint">Draft settings without a reported source mapping.</p>}{table(rows, name)}</section>)}
   </div>
  </div>
 </div>
}
