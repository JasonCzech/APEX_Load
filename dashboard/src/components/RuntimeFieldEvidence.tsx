import { useEffect, useState } from 'react'
import { request } from '../hooks/apiClient'
import type { ScriptConfig, ScriptDefinition } from '../types/scripts'
import { readRuntimePath, formatRuntimeEvidenceValue } from './runtimeFieldEvidenceModel'
import { readImportedRuntimeSettingDispositions } from './scriptEditorRuntimeSettingsModel'
import { importRuntimeFields } from './importRuntimeSettingsModel'
import { RuntimeSectionIcon } from './RuntimeSectionIcon'
import type { RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'

interface SupportEntry { id: string; fields?: string[]; protocols: string[]; status: string; concept?: string; notes?: string; execution_scope?: string }
interface EvidenceRow { entry: SupportEntry; path: string; applicable: boolean }

const supportLabels: Record<string, string> = {
  executed: 'Executed',
  mapped: 'Mapped for replay',
  partial: 'Partial',
  compatibility_only: 'Preserved only',
  blocked: 'Blocked',
  non_applicable: 'Not applicable',
}

const fieldLabels = new Map(importRuntimeFields.map(field => [field.path, field.label]))

function settingLabel(path: string): string {
  return fieldLabels.get(path) || path.replace(/^config\./, '').split('.').slice(-1)[0].replace(/_/g, ' ').replace(/\b\w/g, (letter: string) => letter.toUpperCase())
}

function supportTone(status: string): string {
  if (status === 'executed' || status === 'mapped') return 'healthy'
  if (status === 'partial' || status === 'compatibility_only') return 'warning'
  if (status === 'blocked') return 'danger'
  return ''
}

export function RuntimeFieldEvidence({ script, baseline, fields, sectionId, sectionLabel }: { script: ScriptDefinition; baseline?: ScriptConfig; fields: (keyof ScriptConfig)[]; sectionId?: RuntimeSettingsSectionId; sectionLabel?: string }) {
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
  const matchesSection = (path: string) => fields.some(field => path === `config.${field}` || path.startsWith(`config.${field}.`))
  const rows: EvidenceRow[] = (catalog?.support || []).flatMap(entry => (entry.fields || []).filter(matchesSection).map(path => ({
    entry,
    path,
    applicable: entry.protocols.includes(script.protocol) || entry.protocols.includes('*') || entry.protocols.includes('all'),
  })))
  const applicableRows = rows.filter(row => row.applicable)
  const otherRows = rows.filter(row => !row.applicable)

  const renderTable = (tableRows: EvidenceRow[], defaults: ScriptConfig) => <div className="script-parameter-table-wrap imported-runtime-table-wrap runtime-evidence-table-wrap">
    <table className="script-parameter-table imported-runtime-table runtime-evidence-table" data-table-pagination="off">
      <thead><tr><th>Setting</th><th>Current value</th><th>Value origin</th><th>Execution support</th></tr></thead>
      <tbody>{tableRows.map(({ entry, path, applicable }) => {
        const configured = readRuntimePath(script.config, path)
        const changed = baseline && JSON.stringify(configured) !== JSON.stringify(readRuntimePath(baseline, path))
        const origin = changed ? 'Edited' : importedFields.has(path) ? 'Imported' : configured === undefined ? 'Engine default' : 'Script value'
        const support = supportLabels[entry.status] || entry.status.replace(/_/g, ' ')
        const hasDetails = Boolean(entry.concept || entry.notes || entry.execution_scope)
        return <tr key={`${entry.id}:${path}`}>
          <th scope="row"><span className="runtime-evidence-setting-name">{sectionLabel && fields.length === 1 && path === `config.${fields[0]}` ? sectionLabel : settingLabel(path)}</span><code className="cell-mono runtime-evidence-setting-path">{path.replace(/^config\./, '')}</code></th>
          <td>{formatRuntimeEvidenceValue(configured ?? readRuntimePath(defaults, path), path)}</td>
          <td><span className="repository-status-pill runtime-evidence-origin">{origin}</span></td>
          <td>{applicable && <span className={`repository-status-pill imported-runtime-disposition runtime-evidence-support ${supportTone(entry.status)}`}>{support}</span>}
            {hasDetails ? <div className="runtime-evidence-row-details">{entry.concept && <p>{entry.concept}</p>}{entry.notes && <p>{entry.notes}</p>}{entry.execution_scope && <p><strong>Execution scope:</strong> {entry.execution_scope}</p>}</div> : !applicable && <span className="runtime-evidence-no-details">—</span>}
          </td>
        </tr>
      })}</tbody>
    </table>
  </div>

  return <details className="runtime-field-evidence settings-section"><summary className="runtime-evidence-toggle"><span className="runtime-evidence-heading">{sectionId && <RuntimeSectionIcon section={sectionId} />}<strong>Value origin &amp; execution support</strong><span className="repository-status-pill imported-runtime-file-tag">{sectionLabel || 'Runtime settings'}</span></span></summary>
    <div className="runtime-evidence-body">
      <p className="new-test-inline-note runtime-evidence-context">Values shown are script defaults. Scenario, run, and action overrides are resolved in their own editors. Setting names and paths connect these rows to the controls above.</p>
      {sectionLabel && <h5 className="panel-title cell-mono imported-runtime-section-title">[{sectionLabel}]</h5>}
      {failed ? <p role="status">Execution support is unavailable. No support status has been assumed.</p> : !catalog ? <p role="status">Loading engine defaults and support…</p> : rows.length === 0 ? <p role="status">No field-level support entries for this category.</p> : <>
        {applicableRows.length ? renderTable(applicableRows, catalog.defaults) : <p role="status">No support entries target this script protocol.</p>}
        {otherRows.length > 0 && <details className="runtime-evidence-other"><summary>Other protocol contexts ({otherRows.length})</summary><p>These support entries target different protocols.</p>{renderTable(otherRows, catalog.defaults)}</details>}
      </>}
    </div>
  </details>
}
