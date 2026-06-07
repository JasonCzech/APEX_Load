import { useMemo, useState } from 'react'

import {
  GeneratorShardSummary,
  TestRunManifest,
  TestRunManifestConfiguration,
  TestRunManifestRuntimeChange,
  TestRunManifestScript,
} from '../types'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import { downloadBlob } from '../utils/fileUtils'

interface Props {
  manifest: TestRunManifest | null
  error: string
  loading: boolean
  onReload: () => Promise<void>
}

type DefinitionEntry = [string, string | number | boolean | null | undefined]

const COPY_TIMEOUT_MS = 1500

function formatDate(value?: string | null): string {
  if (!value) return 'Not set'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function formatBoolean(value?: boolean): string {
  if (value === undefined) return 'Not set'
  return value ? 'Enabled' : 'Disabled'
}

function formatValue(value: string | number | boolean | null | undefined): string {
  if (value === null || value === undefined || value === '') return 'Not set'
  if (typeof value === 'boolean') return value ? 'Yes' : 'No'
  return String(value)
}

function safeFilename(value?: string | null): string {
  return value?.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') || 'test-run'
}

function stringifyJSON(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

function copyWithTimeout(value: string): Promise<boolean> {
  return Promise.race([
    copyTextToClipboard(value),
    new Promise<boolean>(resolve => {
      setTimeout(() => resolve(false), COPY_TIMEOUT_MS)
    }),
  ])
}

function hasObjectValue(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false
  if (Array.isArray(value)) return value.length > 0
  return Object.keys(value as Record<string, unknown>).length > 0
}

function renderDefinitions(entries: DefinitionEntry[]) {
  return (
    <dl className="manifest-definition-grid">
      {entries.map(([label, value]) => (
        <div className="manifest-definition" key={label}>
          <dt>{label}</dt>
          <dd>{formatValue(value)}</dd>
        </div>
      ))}
    </dl>
  )
}

function renderJSONDetails(label: string, value: unknown) {
  if (!hasObjectValue(value)) return null
  return (
    <details className="manifest-json-details">
      <summary>{label}</summary>
      <pre>{stringifyJSON(value)}</pre>
    </details>
  )
}

function configurationEntries(config: TestRunManifestConfiguration): DefinitionEntry[] {
  return [
    ['Status', config.status],
    ['Duration', config.duration],
    ['Total VUsers', config.total_vusers],
    ['Active VUsers', config.active_vusers],
    ['Start Delay', config.start_delay],
    ['Scenario Mode', config.scenario_mode],
    ['Script Shakeout', formatBoolean(config.script_shakeout)],
    ['Resource Baseline', formatBoolean(config.resource_baseline)],
    ['Baseline Pinned', formatBoolean(config.baseline_pinned)],
  ]
}

function scriptRamp(script: TestRunManifestScript): string {
  const values = [script.ramp_up || '', script.ramp_down || ''].filter(Boolean)
  return values.length > 0 ? values.join(' / ') : 'Not set'
}

function formatRuntimeDetails(change: TestRunManifestRuntimeChange): string {
  const details = change.details || {}
  const entries = Object.entries(details)
  if (entries.length === 0) return 'No details'
  return entries
    .map(([key, value]) => `${key.replace(/_/g, ' ')}: ${Array.isArray(value) ? value.join(', ') : String(value)}`)
    .join(' | ')
}

function shardSummary(shards?: GeneratorShardSummary[]): string {
  if (!shards || shards.length === 0) return 'No shards assigned'
  const vusers = shards.reduce((sum, shard) => sum + (shard.vusers || 0), 0)
  return `${shards.length} shard${shards.length === 1 ? '' : 's'} / ${vusers} VUsers`
}

export default function TestExecutionManifestCard({ manifest, error, loading, onReload }: Props) {
  const [copyStatus, setCopyStatus] = useState('')

  const jsonText = useMemo(() => (manifest ? stringifyJSON(manifest) : ''), [manifest])
  const fileBaseName = manifest ? safeFilename(manifest.ref_key || manifest.test_id || manifest.test_name) : 'test-run'

  const copyManifest = async () => {
    setCopyStatus('Copying...')
    const copied = await copyWithTimeout(jsonText)
    setCopyStatus(copied ? 'Copied' : 'Copy unavailable')
  }

  const downloadManifest = () => {
    if (!manifest) return
    downloadBlob(new Blob([jsonText], { type: 'application/json' }), `apexload-test-manifest-${fileBaseName}.json`)
  }

  if (loading && !manifest) {
    return (
      <section className="manifest-card" aria-label="Test manifest">
        <p className="loading">Loading test manifest...</p>
      </section>
    )
  }

  if (error && !manifest) {
    return (
      <section className="manifest-card" aria-label="Test manifest">
        <div className="manifest-header">
          <div>
            <h3>Test Manifest</h3>
            <p>{error}</p>
          </div>
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => void onReload()}>Retry</button>
        </div>
      </section>
    )
  }

  if (!manifest) {
    return (
      <section className="manifest-card" aria-label="Test manifest">
        <p className="empty-hint">Manifest unavailable.</p>
      </section>
    )
  }

  return (
    <section className="manifest-card" aria-label="Test manifest">
      <div className="manifest-header">
        <div>
          <h3>Test Manifest</h3>
          <p>{manifest.test_name} · {manifest.status} · {manifest.source}</p>
        </div>
        <div className="manifest-actions">
          {copyStatus && <span className="manifest-action-status">{copyStatus}</span>}
          <button type="button" className="btn btn-secondary btn-sm" onClick={() => void copyManifest()}>Copy JSON</button>
          <button type="button" className="btn btn-secondary btn-sm" onClick={downloadManifest}>Download JSON</button>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => void onReload()}>Refresh</button>
        </div>
      </div>

      {error && <p className="manifest-warning">{error}</p>}

      <div className="manifest-summary-grid">
        <div>
          <span>Generated</span>
          <strong>{formatDate(manifest.generated_at)}</strong>
        </div>
        <div>
          <span>Run ID</span>
          <strong>{manifest.ref_key || manifest.test_id}</strong>
        </div>
        <div>
          <span>Initial Load</span>
          <strong>{manifest.initial_configuration.total_vusers || 0} VUsers / {manifest.initial_configuration.duration || 'Not set'}</strong>
        </div>
        <div>
          <span>Current Load</span>
          <strong>{manifest.current_configuration.total_vusers || 0} VUsers / {manifest.current_configuration.duration || 'Not set'}</strong>
        </div>
        <div>
          <span>Scripts</span>
          <strong>{manifest.scripts?.length || 0}</strong>
        </div>
        <div>
          <span>Load Generators</span>
          <strong>{shardSummary(manifest.load_generators.generator_shards)}</strong>
        </div>
      </div>

      <div className="manifest-section">
        <h4>Run Identity</h4>
        {renderDefinitions([
          ['Application', manifest.identity.application_id],
          ['Release', manifest.identity.release_id],
          ['Scenario', manifest.identity.scenario_id],
          ['Project', manifest.identity.project_id],
          ['Folder', manifest.identity.folder_id],
          ['Created', formatDate(manifest.identity.created_at)],
          ['Started', formatDate(manifest.identity.started_at)],
          ['Ended', formatDate(manifest.identity.ended_at)],
          ['Created By', manifest.identity.created_by_display_name || manifest.identity.created_by_subject],
          ['Started By', manifest.identity.started_by_display_name || manifest.identity.started_by_subject],
        ])}
      </div>

      <div className="manifest-two-column">
        <div className="manifest-section">
          <h4>Initial Configuration</h4>
          {renderDefinitions(configurationEntries(manifest.initial_configuration))}
          {renderJSONDetails('Initial runtime settings', manifest.initial_configuration.runtime_settings)}
          {renderJSONDetails('Initial goal config', manifest.initial_configuration.goal_config)}
        </div>
        <div className="manifest-section">
          <h4>Current Configuration</h4>
          {renderDefinitions(configurationEntries(manifest.current_configuration))}
          {renderJSONDetails('Active VUser ramps', manifest.current_configuration.vuser_ramps)}
          {renderJSONDetails('Current runtime settings', manifest.current_configuration.runtime_settings)}
        </div>
      </div>

      <div className="manifest-section">
        <h4>Scripts & Groups</h4>
        <div className="manifest-table-wrap">
          <table className="manifest-table">
            <thead>
              <tr>
                <th>Group</th>
                <th>Script</th>
                <th>Protocol</th>
                <th>VUsers</th>
                <th>Ramp</th>
                <th>Iterations</th>
                <th>Initialization</th>
              </tr>
            </thead>
            <tbody>
              {(manifest.scripts || []).map(script => (
                <tr key={`${script.group_name}-${script.script_id}`}>
                  <td>{script.group_name}</td>
                  <td>
                    <strong>{script.script_name || script.script_id || 'Unnamed script'}</strong>
                    {script.script_id && <span>{script.script_id}</span>}
                  </td>
                  <td>{script.protocol || 'Not set'}</td>
                  <td>{script.vusers ?? 0}</td>
                  <td>{scriptRamp(script)}</td>
                  <td>{script.iterations ?? 'Run logic'}</td>
                  <td>{script.initialization || 'Not set'}</td>
                </tr>
              ))}
              {(!manifest.scripts || manifest.scripts.length === 0) && (
                <tr>
                  <td colSpan={7}>No scripts recorded.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="manifest-section">
        <h4>Load Generators</h4>
        {renderDefinitions([
          ['Assignment Mode', manifest.load_generators.assignment?.mode],
          ['Selected Targets', manifest.load_generators.assignment?.selected_targets?.join(', ')],
          ['Shard Strategy', manifest.load_generators.assignment?.shard_strategy],
          ['Generator Inventory', manifest.load_generators.generator_inventory?.length],
        ])}
        <div className="manifest-table-wrap">
          <table className="manifest-table">
            <thead>
              <tr>
                <th>Group</th>
                <th>Target</th>
                <th>VUsers</th>
                <th>Strategy</th>
              </tr>
            </thead>
            <tbody>
              {(manifest.load_generators.generator_shards || []).map((shard, index) => (
                <tr key={`${shard.group}-${shard.target}-${index}`}>
                  <td>{shard.group}</td>
                  <td>{shard.target}</td>
                  <td>{shard.vusers}</td>
                  <td>{shard.shard_strategy || 'Not set'}</td>
                </tr>
              ))}
              {(!manifest.load_generators.generator_shards || manifest.load_generators.generator_shards.length === 0) && (
                <tr>
                  <td colSpan={4}>No generator shards recorded.</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
        {renderJSONDetails('Generator inventory', manifest.load_generators.generator_inventory)}
        {renderJSONDetails('Execution settings', manifest.load_generators.execution_settings)}
        {renderJSONDetails('Controller environment', manifest.load_generators.controller_env)}
      </div>

      <div className="manifest-section">
        <h4>Runtime Changes</h4>
        <div className="manifest-timeline">
          {(manifest.runtime_changes || []).map(change => (
            <div className="manifest-timeline-entry" key={change.id || `${change.action}-${change.created_at}`}>
              <div>
                <strong>{change.label}</strong>
                <span>{formatDate(change.created_at)} · {change.status || 'recorded'} · {change.actor_display_name || change.actor_subject || change.actor_type || 'system'}</span>
              </div>
              <p>{formatRuntimeDetails(change)}</p>
            </div>
          ))}
          {(!manifest.runtime_changes || manifest.runtime_changes.length === 0) && (
            <p className="empty-hint">No runtime changes recorded.</p>
          )}
        </div>
      </div>

      {manifest.warnings && manifest.warnings.length > 0 && (
        <div className="manifest-section">
          <h4>Data Quality</h4>
          <ul className="manifest-warning-list">
            {manifest.warnings.map(warning => <li key={warning}>{warning}</li>)}
          </ul>
        </div>
      )}

      <div className="manifest-section">
        <h4>Raw Manifest</h4>
        <pre className="manifest-raw">{jsonText}</pre>
      </div>
    </section>
  )
}
