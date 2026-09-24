import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { useId, useMemo, useState } from 'react'
import ImportCategoryHeading from './ImportCategoryHeading'
import type { ScriptDefinition } from '../types/scripts'
import { CategoryActions, SettingValue, formatSettingValue, useImportSettings } from './ImportSettingsContext'
import { runtimeSettingRows, readSetting, type RuntimeSettingRow } from './importRuntimeSettingsModel'

export default function ImportRuntimeSettingsTable({ script, onReviewTLSMigration }: { script: ScriptDefinition; onReviewTLSMigration?: () => void }) {
 const context = useImportSettings()
 return <RuntimeSettingsWorkspace key={context?.viewGeneration} script={script} onReviewTLSMigration={onReviewTLSMigration} />
}

function RuntimeSettingsWorkspace({ script, onReviewTLSMigration }: { script: ScriptDefinition; onReviewTLSMigration?: () => void }) {
 const context = useImportSettings()
 const countsId = useId()
 const [selected, setSelected] = useState(() => script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP')
 const [hiddenStatuses, setHiddenStatuses] = useState<string[]>([])
 const [showUnconfigured, setShowUnconfigured] = useState(false)
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
  const names: Record<string, string> = { browseremulation: 'Browser emulation', thinktime: 'Think time', web: 'HTTP', log: 'Logging', filters: 'Filters', modemspeed: 'Network speed simulation', networkspeedsimulation: 'Network speed simulation' }
  for (const rows of Object.values(result)) for (const row of rows) {
   const key = row.category.replace(/\s/g, '').toLowerCase()
   const category = key.startsWith('runlogic') || key === 'profileactions' ? 'Run logic' : names[key] || row.category
   if (row.category !== category) row.note = `Source section: ${row.category}. ${row.note}`
   row.category = category
  }
  return result
 }, [script, context?.editors])
 const orderedGroups = ['Requires decision', 'Mapped for replay', 'Additional APEX settings', 'Preserved only', 'Not applicable'].filter(name => groups[name]?.length).map(name => [name, groups[name]] as const)
 const unconfiguredCount = (groups['Additional APEX settings'] || []).filter(row => row.path && readSetting(script, row.path) === undefined).length
 const isVisibleByDefault = (name: string, row: RuntimeSettingRow) => name !== 'Additional APEX settings' || !row.path || readSetting(script, row.path) !== undefined || Boolean(context?.editors[row.category])
 const categories = [...new Set(orderedGroups.flatMap(([name, rows]) => rows.filter(row => showUnconfigured || isVisibleByDefault(name, row)).map(row => row.category)))].sort()
 const preferred = script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP'
 const active = selected === 'All categories' || categories.includes(selected) ? selected : categories.includes(preferred) ? preferred : categories[0] || 'All categories'
 const query = search.trim().toLowerCase()
 const matches = (row: RuntimeSettingRow) => !query || `${row.category} ${row.label} ${row.imported} ${row.note}`.toLowerCase().includes(query)
 const visible = (name: string, rows: RuntimeSettingRow[]) => rows.filter(row => matches(row) && (query || showUnconfigured || isVisibleByDefault(name, row)) && (query || active === 'All categories' || active === row.category))
 const enabledGroups = orderedGroups.filter(([name]) => !hiddenStatuses.includes(name))
 const enabledRows = enabledGroups.flatMap(([name, rows]) => rows.filter(row => showUnconfigured || isVisibleByDefault(name, row)))
 const shownGroups = enabledGroups.map(([name, rows]) => [name, visible(name, rows)] as const).filter(([, rows]) => rows.length)
 const pending = Object.keys(context?.editors || {}).filter(category => !category.startsWith('parameters:'))
 function table(rows: RuntimeSettingRow[], group: string) {
  return <div className="lr-table-wrap"><table className="import-settings-table import-runtime-table" data-table-pagination="off"><thead><tr><th>Setting</th><th>Imported value</th><th>Current draft value</th><th>Details</th></tr></thead>{[...new Set(rows.map(row => row.category))].map(category => <tbody key={category}>
   <tr className="import-category"><th colSpan={4} scope="rowgroup"><ImportCategoryHeading label={category} details={<p>{group === 'Additional APEX settings' ? 'Draft settings without a reported source mapping.' : `${group}. Original source evidence remains unchanged when you edit the draft.`}</p>} /></th></tr>
   {rows.filter(row => row.category === category).map(row => <tr key={row.id}><th scope="row">{row.label}</th><td className="import-setting-value">{row.importedSummary || row.imported}</td><td>{row.path ? row.field ? <SettingValue category={category} path={row.path} label={`${category}: ${row.label}`} value={readSetting(script, row.path)} readOnly={!!onReviewTLSMigration && ['config.http.tls.min_version', 'config.http.tls.max_version'].includes(row.path)} type={row.field.type} options={row.field.options} /> : formatSettingValue(readSetting(script, row.path)) : group === 'Not applicable' ? 'Not applicable' : group === 'Mapped for replay' ? 'Used by importer' : group === 'Preserved only' ? 'Preserved only' : 'No exact mapping'}</td><td><details className="import-row-details"><summary>Details</summary><div><p>{row.note}</p><p className="import-setting-value">{row.imported}</p></div></details></td></tr>)}
  </tbody>)}</table></div>
 }
 return <div className="import-runtime-workspace"><ImportReviewSectionHeader title="Runtime settings" description="Compare source and draft values. Applying changes refreshes checks; replay still needs verification." />
  {onReviewTLSMigration && <div className="import-runtime-repair-notice"><p>Legacy TLS requires an intentional migration. Use the guided repair to preview, acknowledge, and apply the replacement.</p><button type="button" className="btn btn-secondary" onClick={onReviewTLSMigration}>Review TLS migration</button></div>}
  <p className="import-settings-hint">Mapping status totals across all categories</p>
  <div className="import-runtime-summary" role="group" aria-label="Overall runtime setting counts">{orderedGroups.map(([name, rows]) => <label key={name} className={hiddenStatuses.includes(name) ? 'is-hidden' : undefined}><input type="checkbox" aria-label={name === 'Mapped for replay' ? 'Mapped' : name} aria-describedby={`${countsId}-runtime-count-${name.replace(/ /g, '-')}`} checked={!hiddenStatuses.includes(name)} onChange={() => setHiddenStatuses(previous => previous.includes(name) ? previous.filter(value => value !== name) : [...previous, name])} /><span>{name === 'Mapped for replay' ? 'Mapped' : name}</span><strong aria-hidden="true">{rows.length}</strong><span className="import-review-count-description" id={`${countsId}-runtime-count-${name.replace(/ /g, '-')}`}>{rows.length} settings across all categories</span></label>)}</div>
  {unconfiguredCount > 0 && <label className="import-settings-hint"><input type="checkbox" checked={showUnconfigured} onChange={event => setShowUnconfigured(event.target.checked)} /> Show {unconfiguredCount} unconfigured APEX setting{unconfiguredCount === 1 ? '' : 's'}</label>}
  <div className="import-review-toolbar"><label>Find a setting<input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search all categories" /></label></div>
  <div className="import-runtime-layout"><nav className="import-category-nav" aria-label="Runtime categories">{['All categories', ...categories].map(category => <button type="button" key={category} aria-pressed={active === category && !query} onClick={() => { setSelected(category); setSearch('') }}>{category}<small>{category === 'All categories' ? enabledRows.length : enabledRows.filter(row => row.category === category).length}{context?.editors[category] ? ' · Editing' : ''}</small></button>)}</nav>
   <div className="import-runtime-content">
    <p className="import-settings-hint" role="status">{query ? 'Search across all categories' : active} · {shownGroups.reduce((count, [, rows]) => count + rows.length, 0)} settings shown</p>
    <div className="import-category-editbar">{[...new Set([...shownGroups.flatMap(([, rows]) => rows).filter(row => row.field).map(row => row.category), ...pending])].map(category => <span key={category}>{pending.includes(category) && <button type="button" className="btn btn-secondary btn-sm" onClick={() => { setSelected(category); setSearch(''); setHiddenStatuses([]) }}>Review {category} edits{Object.keys(context?.editors[category]?.errors || {}).length ? ' · Errors' : ''}</button>}<CategoryActions category={category} /></span>)}</div>
    {!shownGroups.length && <p className="import-review-empty" role="status">{enabledGroups.length > 0 && unconfiguredCount > 0 && !showUnconfigured && !query ? 'No configured settings in this category. Show unconfigured APEX settings to inspect the full inventory.' : 'No settings match these filters. Enable a mapping status, choose another category, or change your search.'}</p>}
    {shownGroups.map(([name, rows]) => <section className="import-settings-section" aria-label={name} key={name}><h3>{name} <small>({rows.length})</small></h3>{name === 'Additional APEX settings' && <p className="import-settings-hint">Draft settings without a reported source mapping.</p>}{table(rows, name)}</section>)}
   </div>
  </div>
 </div>
}
