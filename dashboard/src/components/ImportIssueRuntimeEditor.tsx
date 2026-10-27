import { useEffect, useMemo, useState } from 'react'
import type { ScriptDefinition } from '../types/scripts'
import { SettingValue, formatSettingValue, useImportSettings } from './ImportSettingsContext'
import { importRuntimeFields, readSetting, runtimeSettingRows } from './importRuntimeSettingsModel'

/** Shares pending changes and the settings endpoint with the Runtime tab. */
export default function ImportIssueRuntimeEditor({ script, migrationPending, tlsOnly = false }: { script: ScriptDefinition; migrationPending: boolean; tlsOnly?: boolean }) {
  const settings = useImportSettings()
  const [previewCategory, setPreviewCategory] = useState('')
  const groups = useMemo(() => runtimeSettingRows(script), [script])
  const rows = tlsOnly ? importRuntimeFields.filter(field => ['config.http.tls.min_version', 'config.http.tls.max_version'].includes(field.path)).map(field => ({ id: field.path, path: field.path, category: field.category, label: field.label, field, imported: 'Source SSL/TLS selection', note: 'An explicit supported range records a migration and resolves the script’s TLS requirements.' })) : [...(groups['Requires decision'] || []), ...(groups['Preserved only'] || [])]
  useEffect(() => setPreviewCategory(''), [settings?.editors, script])
  const categories = [...new Set(rows.filter(row => row.field && row.path).map(row => row.category))]
  return <section className="lr-issue-runtime-editor" aria-label={tlsOnly ? "TLS settings" : "Runtime differences"}>
    <h5>{tlsOnly ? "Choose supported TLS settings" : "Settings that differ"}</h5>
    <div className="lr-issue-edit-controls">{settings && categories.map(category => {
      const editor = settings.editors[category]
      const changes = Object.values(editor?.changes || {})
      const previewing = previewCategory === category
      return <div className="lr-issue-setting-actions" key={category}>
        {editor ? <>
          {previewing && <section aria-label={`${category} change preview`}><h5>Review {category} changes</h5><ul>{changes.map(change => <li key={change.path}><strong>{change.path}</strong>: {formatSettingValue(readSetting(script, change.path))} → {formatSettingValue(change.value)}</li>)}</ul><p>The server validates these changes when applied.</p></section>}
          <div className="lr-issue-action-row"><button type="button" className="btn btn-primary btn-sm" disabled={settings.disabled || settings.busy || !changes.length} onClick={() => previewing ? void settings.apply(category) : setPreviewCategory(category)}>{settings.busy ? 'Applying…' : previewing ? `Apply ${category} changes` : `Preview ${category} changes`}</button><button type="button" className="btn btn-secondary btn-sm" disabled={settings.busy} onClick={() => settings.cancel(category)}>Cancel {category} edits</button></div>
          {editor.errors.category && <p role="alert">{editor.errors.category}</p>}
        </> : <button type="button" className="btn btn-secondary btn-sm" disabled={settings.disabled || settings.busy} onClick={() => settings.begin(category)}>Edit {category} here</button>}
      </div>
    })}</div>
    <p>Compare the imported behavior with the current draft. Applying settings refreshes checks; it does not establish exact source compatibility.</p>
    {!rows.length ? <p>No individual runtime differences were supplied for this draft. Inspect the evidence below or open Runtime settings.</p> : <div className="lr-table-wrap"><table className="import-settings-table" data-table-pagination="off"><thead><tr><th>Setting / source</th><th>Current draft</th><th>Replay consequence</th></tr></thead><tbody>{rows.map(row => <tr key={row.id}>
      <th scope="row">{row.label}<small>{row.imported}</small></th>
      <td>{row.path && row.field ? <SettingValue category={row.category} path={row.path} label={`${row.category}: ${row.label}`} value={readSetting(script, row.path) ?? (tlsOnly ? row.path.endsWith('min_version') ? '1.2' : '1.3' : undefined)} type={row.field.type} options={row.field.options} readOnly={migrationPending && row.path.startsWith('config.http.tls.')} /> : 'No editable equivalent'}</td>
      <td>{row.note}</td>
    </tr>)}</tbody></table></div>}
  </section>
}
