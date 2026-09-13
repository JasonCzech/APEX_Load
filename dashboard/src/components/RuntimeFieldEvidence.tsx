import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import { readRuntimePath, formatRuntimeEvidenceValue } from './runtimeFieldEvidenceModel'
import { readImportedRuntimeSettingDispositions } from './scriptEditorRuntimeSettingsModel'

interface SupportEntry { id: string; fields?: string[]; protocols: string[]; status: string; notes?: string; execution_scope?: string }

export function RuntimeFieldEvidence({ script, baseline, fields }: { script: ScriptDefinition; baseline?: ScriptConfig; fields: (keyof ScriptConfig)[] }) {
  const [catalog, setCatalog] = useState<{ support: SupportEntry[]; defaults: ScriptConfig } | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let active = true
    void Promise.all([
      request<{ support_matrix: SupportEntry[] }>('/runtime-settings/support-matrix'),
      request<{ config: ScriptConfig }>('/dsl/config-defaults'),
    ]).then(([support, defaults]) => { if (active) setCatalog({ support: support.support_matrix, defaults: defaults.config }) }).catch(() => { if (active) setFailed(true) })
    return () => { active = false }
  }, [])
  const importedFields = new Set(Object.values(readImportedRuntimeSettingDispositions(script.metadata?.import)).flatMap(sections => Object.values(sections).flatMap(keys => Object.values(keys).flatMap(item => item.fields || []))))
  const entries = catalog?.support.filter(entry => entry.fields?.some(path => fields.some(field => path === `config.${field}` || path.startsWith(`config.${field}.`)))) || []
  return <details className="runtime-field-evidence"><summary>Value origin &amp; execution support</summary>
    <p>Script context. Scenario and run overrides are resolved in their runtime editors; action-level values may override request defaults.</p>
    {failed ? <p role="status">Execution support is unavailable. No support status has been assumed.</p> : !catalog ? <p>Loading engine defaults and support…</p> : entries.length === 0 ? <p>No field-level support entries for this category.</p> : <table data-table-pagination="off"><thead><tr><th>Setting</th><th>Value</th><th>Origin</th><th>Support</th></tr></thead><tbody>{entries.flatMap(entry => (entry.fields || []).filter(path => fields.some(field => path === `config.${field}` || path.startsWith(`config.${field}.`))).map(path => {
      const configured = readRuntimePath(script.config, path)
      const changed = baseline && JSON.stringify(configured) !== JSON.stringify(readRuntimePath(baseline, path))
      const origin = changed ? 'Edited' : importedFields.has(path) ? 'Imported' : configured === undefined ? 'Engine default' : 'Script value'
      const applicable = entry.protocols.includes(script.protocol) || entry.protocols.includes('*') || entry.protocols.includes('all')
      return <tr key={`${entry.id}:${path}`}><th><code>{path.replace('config.', '')}</code></th><td>{formatRuntimeEvidenceValue(configured ?? readRuntimePath(catalog.defaults, path), path)}</td><td>{origin}</td><td>{applicable ? entry.status.replace(/_/g, ' ') : 'Check protocol applicability'}{entry.notes && <details><summary>Details</summary><p>{entry.notes}</p><p>{entry.execution_scope}</p></details>}</td></tr>
    }))}</tbody></table>}
  </details>
}
