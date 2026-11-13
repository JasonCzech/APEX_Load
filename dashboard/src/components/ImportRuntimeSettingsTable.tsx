import ImportReviewSectionHeader from './ImportReviewSectionHeader'
import { useId, useMemo, useState } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import { CategoryActions, SettingValue, formatSettingValue, isSettingModified, useImportSettings } from './ImportSettingsContext'
import {
 CANONICAL_RUNTIME_GROUPS,
 PROVENANCE_FILTERS,
 readSetting,
 resolveCanonicalRuntimeGroup,
 resolveRuntimeSubgroup,
 runtimeSettingRows,
 useImportRuntimeFields,
 type CanonicalRuntimeGroup,
 type ProvenanceFilterOption,
 type RemoteImportRuntimeField,
 type RuntimeSettingRow,
} from './importRuntimeSettingsModel'

export default function ImportRuntimeSettingsTable({
 script,
 onReviewTLSMigration,
 remoteFields,
}: {
 script: ScriptDefinition
 onReviewTLSMigration?: () => void
 remoteFields?: RemoteImportRuntimeField[]
}) {
 const context = useImportSettings()
 // The server's field list for this protocol (catalog-widened, with the
 // options it validates). The offline catalog renders until it arrives.
 const servedFields = useImportRuntimeFields(remoteFields ? undefined : script.protocol)
 return <RuntimeSettingsWorkspace key={context?.viewGeneration} script={script} onReviewTLSMigration={onReviewTLSMigration} remoteFields={remoteFields ?? servedFields} />
}

function RuntimeSettingsWorkspace({
 script,
 onReviewTLSMigration,
 remoteFields,
}: {
 script: ScriptDefinition
 onReviewTLSMigration?: () => void
 remoteFields?: RemoteImportRuntimeField[]
}) {
 const context = useImportSettings()
 const countsId = useId()
 // Open on what needs a decision, across every category, when anything does;
 // otherwise on the protocol's settings with the not-applicable ones folded away.
 const [decisionsFirst] = useState(() => (runtimeSettingRows(script)['Requires decision'] || []).length > 0)
 const [selected, setSelected] = useState(() => decisionsFirst ? 'All categories' : script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP')
 const [selectedCanonicalGroup, setSelectedCanonicalGroup] = useState<'All groups' | CanonicalRuntimeGroup>('All groups')
 const [provenanceFilter, setProvenanceFilter] = useState<'custom' | ProvenanceFilterOption>('custom')
 const [hiddenStatuses, setHiddenStatuses] = useState<string[]>(() => decisionsFirst ? ['Mapped for replay', 'Additional APEX settings', 'Preserved only', 'Not applicable'] : ['Not applicable'])
 const [showUnconfigured, setShowUnconfigured] = useState(false)
 const [search, setSearch] = useState('')
 const groups = useMemo(() => {
  const working = structuredClone(script)
  for (const editor of Object.values(context?.editors || {})) for (const change of Object.values(editor.changes)) {
   if (change.parameter) continue
   const parts = change.path.split('.')
   let object = working as unknown as Record<string, unknown>
   parts.slice(0, -1).forEach(key => {
    if (!object[key] || typeof object[key] !== 'object' || Array.isArray(object[key])) object[key] = {}
    object = object[key] as Record<string, unknown>
   })
   object[parts[parts.length - 1]] = change.value
  }
  const result = runtimeSettingRows(working, remoteFields)
  const names: Record<string, string> = { browseremulation: 'Browser emulation', thinktime: 'Think time', web: 'HTTP', log: 'Logging', filters: 'Filters', modemspeed: 'Network speed simulation', networkspeedsimulation: 'Network speed simulation', proxy: 'HTTP' }
  for (const rows of Object.values(result)) for (const row of rows) {
   const key = row.category.replace(/\s/g, '').toLowerCase()
   const category = key.startsWith('runlogic') || key === 'profileactions' ? 'Run logic' : names[key] || row.category
   if (row.category !== category) row.note = `Source section: ${row.category}. ${row.note}`
   row.category = category
   row.group = row.field?.group || resolveCanonicalRuntimeGroup(category, row.path)
   row.subgroup = row.field?.subgroup || resolveRuntimeSubgroup(row.path, category)
  }
  return result
 }, [script, context?.editors, remoteFields])
 const orderedGroups = ['Requires decision', 'Mapped for replay', 'Additional APEX settings', 'Preserved only', 'Not applicable'].filter(name => groups[name]?.length).map(name => [name, groups[name]] as const)
 const unconfiguredCount = (groups['Additional APEX settings'] || []).filter(row => row.path && readSetting(script, row.path) === undefined).length
 const mappedCount = (groups['Mapped for replay'] || []).length
 const allRows = orderedGroups.flatMap(([, rows]) => rows)
 const isModified = (row: RuntimeSettingRow) => Boolean(row.path && isSettingModified(context, row.path))
 const usesDefault = (row: RuntimeSettingRow) => Boolean(row.path && row.field && readSetting(script, row.path) === undefined && !isModified(row))
 // Each count is what its filter shows: the filter reveals every mapping
 // status it needs and the unconfigured inventory.
 const provenanceCounts: Record<ProvenanceFilterOption, number> = {
  'All settings': allRows.length,
  Modified: allRows.filter(isModified).length,
  Imported: orderedGroups.filter(([name]) => name !== 'Additional APEX settings').reduce((sum, [, rows]) => sum + rows.length, 0),
  Default: allRows.filter(usesDefault).length,
 }
 const isVisibleByDefault = (name: string, row: RuntimeSettingRow) => name !== 'Additional APEX settings' || !row.path || readSetting(script, row.path) !== undefined || Boolean(context?.editors[row.category] || context?.editors[row.group])
 const categories = [...new Set(orderedGroups.flatMap(([name, rows]) => rows.filter(row => showUnconfigured || isVisibleByDefault(name, row)).map(row => row.category)))].sort()
 const enabledGroups = orderedGroups.filter(([name]) => !hiddenStatuses.includes(name))
 const enabledAllCategories = [...new Set(enabledGroups.flatMap(([, rows]) => rows.map(row => row.category)))].sort()
 const preferred = script.protocol?.startsWith('jms') ? 'JMS' : script.protocol === 'kafka' ? 'Kafka' : 'HTTP'
 const active = selected === 'All categories' || categories.includes(selected) || enabledAllCategories.includes(selected)
  ? selected
  : categories.includes(preferred) || enabledAllCategories.includes(preferred)
    ? preferred
    : categories[0] || 'All categories'
 const query = search.trim().toLowerCase()
 const matches = (row: RuntimeSettingRow) => !query || `${row.category} ${row.group} ${row.subgroup || ''} ${row.label} ${row.imported} ${row.note}`.toLowerCase().includes(query)
 const matchesProvenanceRow = (name: string, row: RuntimeSettingRow) => {
  if (provenanceFilter === 'Modified') return isModified(row)
  if (provenanceFilter === 'Imported') return name !== 'Additional APEX settings'
  if (provenanceFilter === 'Default') return usesDefault(row)
  return true
 }
 const visible = (name: string, rows: RuntimeSettingRow[]) =>
  rows.filter(
   row =>
    matches(row) &&
    matchesProvenanceRow(name, row) &&
    (query || showUnconfigured || isVisibleByDefault(name, row)) &&
    (query || selectedCanonicalGroup !== 'All groups' ? (query || row.group === selectedCanonicalGroup) : (active === 'All categories' || active === row.category)),
  )
 const enabledRows = enabledGroups.flatMap(([name, rows]) => rows.filter(row => matchesProvenanceRow(name, row) && (showUnconfigured || isVisibleByDefault(name, row))))
 // A search finds a setting by name whatever its status, like it ignores the category.
 const shownGroups = (query ? orderedGroups : enabledGroups).map(([name, rows]) => [name, visible(name, rows)] as const).filter(([, rows]) => rows.length)
 const pending = Object.keys(context?.editors || {}).filter(category => !category.startsWith('parameters:'))

 function handleProvenanceFilter(option: ProvenanceFilterOption) {
  setProvenanceFilter(option)
  setSearch('')
  setSelected('All categories')
  setSelectedCanonicalGroup('All groups')
  setHiddenStatuses(option === 'Imported' ? ['Additional APEX settings'] : [])
  setShowUnconfigured(true)
 }

 function handleSelectCanonicalGroup(group: 'All groups' | CanonicalRuntimeGroup) {
  setSelectedCanonicalGroup(group)
  setSearch('')
  if (group === 'All groups') setSelected('All categories')
  else {
   setProvenanceFilter('custom')
   setSelected('All categories')
   setHiddenStatuses(previous => previous.filter(status => !['Requires decision', 'Mapped for replay', 'Additional APEX settings'].includes(status)))
   setShowUnconfigured(true)
  }
 }

 // A group shows every row of the statuses it reveals (see handleSelectCanonicalGroup).
 const hiddenForGroup = hiddenStatuses.filter(status => !['Requires decision', 'Mapped for replay', 'Additional APEX settings'].includes(status))
 const canonicalGroupRows = orderedGroups.filter(([name]) => !hiddenForGroup.includes(name)).flatMap(([, rows]) => rows)

 function table(rows: RuntimeSettingRow[], group: string) {
  return <div className="lr-table-wrap"><table className="import-settings-table import-runtime-table" data-table-pagination="off"><thead><tr><th>Setting</th><th>Imported value</th><th>Current draft value</th><th>Details</th></tr></thead>{[...new Set(rows.map(row => row.category))].map(category => <tbody key={category}>
   <tr className="import-category"><th colSpan={4} scope="rowgroup"><strong>{category}</strong><small>{group === 'Additional APEX settings' ? 'Draft settings without a reported source mapping.' : `${group}. Original source evidence remains unchanged when you edit the draft.`}</small></th></tr>
   {rows.filter(row => row.category === category).map(row => <tr key={row.id} data-canonical-group={row.group} data-canonical-subgroup={row.subgroup}><th scope="row"><span className="import-runtime-setting-name">{row.label}</span>{row.path && <code className="import-runtime-setting-path" aria-hidden="true">{row.path.replace(/^config\./, '')}</code>}</th><td className="import-setting-value">{row.importedSummary || row.imported}</td><td>{row.path ? row.field ? <SettingValue category={category} path={row.path} label={`${category}: ${row.label}`} value={readSetting(script, row.path)} readOnly={!!onReviewTLSMigration && ['config.http.tls.min_version', 'config.http.tls.max_version'].includes(row.path)} type={row.field.type} options={row.field.options} credentialReference={row.field.credentialReference} /> : formatSettingValue(readSetting(script, row.path)) : group === 'Not applicable' ? 'Not applicable' : group === 'Mapped for replay' ? 'Used by importer' : group === 'Preserved only' ? 'Preserved only' : 'No exact mapping'}</td><td><div className="import-runtime-inline-details"><p>{row.note || '—'}</p>{row.importedSummary && row.importedSummary !== row.imported && <p className="import-setting-value">Source detail: {row.imported}</p>}</div></td></tr>)}
  </tbody>)}</table></div>
 }
 return <div className="import-runtime-workspace"><ImportReviewSectionHeader title="Runtime settings" description="Compare source and draft values. Applying changes refreshes checks; replay still needs verification." />
  {onReviewTLSMigration && <div className="import-runtime-repair-notice"><p>Legacy TLS requires an intentional migration. Use the guided repair to preview, acknowledge, and apply the replacement.</p><button type="button" className="btn btn-secondary" onClick={onReviewTLSMigration}>Review TLS migration</button></div>}
  <div className="import-provenance-filter-bar" role="group" aria-label="Import provenance filter">
   {PROVENANCE_FILTERS.map(option => {
    const count = provenanceCounts[option]
    return (
     <button
      type="button"
      key={option}
      className="btn btn-secondary btn-sm"
      aria-pressed={provenanceFilter === option}
      onClick={() => handleProvenanceFilter(option)}
     >
      {option} <small>({count})</small>
     </button>
    )
   })}
  </div>
  <p className="import-settings-hint">Mapping status totals across all categories</p>
  <div className="import-runtime-summary" role="group" aria-label="Overall runtime setting counts">{orderedGroups.map(([name, rows]) => <label key={name} className={hiddenStatuses.includes(name) ? 'is-hidden' : undefined}><input type="checkbox" aria-label={name === 'Mapped for replay' ? 'Mapped' : name} aria-describedby={`${countsId}-runtime-count-${name.replace(/ /g, '-')}`} checked={!hiddenStatuses.includes(name)} onChange={() => { setProvenanceFilter('custom'); setHiddenStatuses(previous => previous.includes(name) ? previous.filter(value => value !== name) : [...previous, name]) }} /><span>{name === 'Mapped for replay' ? 'Mapped' : name}</span><strong aria-hidden="true">{rows.length}</strong><span className="import-review-count-description" id={`${countsId}-runtime-count-${name.replace(/ /g, '-')}`}>{rows.length} settings across all categories</span></label>)}</div>
  {mappedCount > 0 && hiddenStatuses.includes('Mapped for replay') && (
   <button
    type="button"
    className="btn btn-secondary btn-sm"
    onClick={() => {
     setProvenanceFilter('custom')
     setHiddenStatuses(previous => previous.filter(value => value !== 'Mapped for replay'))
    }}
   >
    Show {mappedCount} mapped setting{mappedCount === 1 ? '' : 's'}
   </button>
  )}
  {unconfiguredCount > 0 && <label className="import-settings-hint"><input type="checkbox" checked={showUnconfigured} onChange={event => { setProvenanceFilter('custom'); setShowUnconfigured(event.target.checked) }} /> Show {unconfiguredCount} unconfigured APEX setting{unconfiguredCount === 1 ? '' : 's'}</label>}
  <nav className="import-canonical-group-nav" aria-label="Canonical runtime groups">
   <button
    type="button"
    aria-pressed={selectedCanonicalGroup === 'All groups' && !query}
    onClick={() => handleSelectCanonicalGroup('All groups')}
   >
    All groups<small>{enabledRows.length}</small>
   </button>
   {CANONICAL_RUNTIME_GROUPS.map(group => {
    const groupCount = canonicalGroupRows.filter(row => row.group === group).length
    return (
     <button
      type="button"
      key={group}
      aria-pressed={selectedCanonicalGroup === group && !query}
      onClick={() => handleSelectCanonicalGroup(group)}
     >
      {group}<small>{groupCount}{context?.editors[group] ? ' · Editing' : ''}</small>
     </button>
    )
   })}
  </nav>
  <div className="import-review-toolbar"><label>Find a setting<input type="search" autoComplete="off" value={search} onChange={event => setSearch(event.target.value)} placeholder="Search all categories" /></label></div>
  <div className="import-runtime-layout"><nav className="import-category-nav" aria-label="Runtime categories">{['All categories', ...categories].map(category => <button type="button" key={category} aria-pressed={selectedCanonicalGroup === 'All groups' && active === category && !query} onClick={() => { setSelectedCanonicalGroup('All groups'); setSelected(category); setSearch('') }}>{category}<small>{category === 'All categories' ? enabledRows.length : enabledRows.filter(row => row.category === category).length}{context?.editors[category] ? ' · Editing' : ''}</small></button>)}</nav>
   <div className="import-runtime-content">
    <p className="import-settings-hint" role="status">{query ? 'Search across all categories and statuses' : selectedCanonicalGroup !== 'All groups' ? selectedCanonicalGroup : active} · {shownGroups.reduce((count, [, rows]) => count + rows.length, 0)} settings shown</p>
    <div className="import-category-editbar">{[...new Set([...shownGroups.flatMap(([, rows]) => rows).filter(row => row.field).map(row => row.category), ...pending])].map(category => <span key={category}>{pending.includes(category) && <button type="button" className="btn btn-secondary btn-sm" onClick={() => { setSelectedCanonicalGroup('All groups'); setSelected(category); setSearch(''); setHiddenStatuses([]) }}>Review {category} edits{Object.keys(context?.editors[category]?.errors || {}).length ? ' · Errors' : ''}</button>}<CategoryActions category={category} /></span>)}</div>
    {!shownGroups.length && <p className="import-review-empty" role="status">{enabledGroups.length > 0 && unconfiguredCount > 0 && !showUnconfigured && !query ? 'No configured settings in this category. Show unconfigured APEX settings to inspect the full inventory.' : 'No settings match these filters. Enable a mapping status, choose another category, or change your search.'}</p>}
    {shownGroups.map(([name, rows]) => <section className="import-settings-section" aria-label={name} key={name}><h3>{name} <small>({rows.length})</small></h3>{name === 'Additional APEX settings' && <p className="import-settings-hint">Draft settings without a reported source mapping.</p>}{table(rows, name)}</section>)}
   </div>
  </div>
 </div>
}
