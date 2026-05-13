import { useCallback, useEffect, useMemo, useState } from 'react'
import { Application, Release, StoredScenario } from '../types'
import { useApi } from '../hooks/useApi'
import { getErrorMessage } from '../utils/errorUtils'

interface Props {
  activeApplication: Application | null
  activeRelease: Release | null
  releases: Release[]
  onSelectTest: (id: string) => void
  onCreateRelease: (name: string) => Promise<Release | null>
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatScenarioMode(value?: StoredScenario['scenario_mode']) {
  return value === 'goal_oriented' ? 'Goal Oriented' : 'VUser Driven'
}

export default function NewTestPage({ activeApplication, activeRelease, releases, onSelectTest, onCreateRelease }: Props) {
  const api = useApi()
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scenarioId, setScenarioId] = useState('')
  const [releaseId, setReleaseId] = useState(activeRelease?.id || '')
  const [runName, setRunName] = useState('')
  const [newReleaseName, setNewReleaseName] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScenarios()
      const next = res.scenarios || []
      setScenarios(next)
      if (!scenarioId && next[0]) setScenarioId(next[0].id)
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load scenarios'))
    }
  }, [api, scenarioId])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => { setReleaseId(activeRelease?.id || releases[0]?.id || '') }, [activeRelease?.id, releases])

  const scenario = useMemo(() => scenarios.find(item => item.id === scenarioId), [scenarios, scenarioId])
  const release = useMemo(() => releases.find(item => item.id === releaseId), [releases, releaseId])
  const totalVUsers = useMemo(() => scenario?.scripts.reduce((sum, script) => sum + (script.vusers || 0), 0) || 0, [scenario])
  const protocols = useMemo(() => {
    const values = new Set((scenario?.scripts || []).map(script => script.protocol || 'http'))
    return Array.from(values)
  }, [scenario])
  const associatedReleases = useMemo(() => scenario?.associated_releases || [], [scenario])
  const readiness = useMemo(() => {
    const checks = [
      { label: activeApplication ? `Application: ${activeApplication.name}` : 'Select an application', ready: !!activeApplication },
      { label: release ? `Release: ${release.name}` : 'Select or create a release', ready: !!release },
      { label: scenario ? `Scenario: ${scenario.name}` : 'Select a scenario', ready: !!scenario },
      { label: scenario?.scripts.length ? `${scenario.scripts.length} script group${scenario.scripts.length === 1 ? '' : 's'} ready` : 'Scenario needs at least one script group', ready: !!scenario?.scripts.length },
    ]
    return checks
  }, [activeApplication, release, scenario])
  const canLaunch = readiness.every(item => item.ready) && !creating

  useEffect(() => {
    if (scenario && !runName) setRunName(scenario.name)
  }, [scenario, runName])

  const createRelease = async () => {
    const name = newReleaseName.trim()
    if (!name) return
    const created = await onCreateRelease(name)
    if (created) setReleaseId(created.id)
    setNewReleaseName('')
  }

  const createRun = async (start: boolean) => {
    if (!scenario || !releaseId || !activeApplication) {
      setError('Choose a release and scenario before creating a test.')
      return
    }
    setCreating(true)
    setError('')
    try {
      const test = await api.createTest({
        name: runName.trim() || scenario.name,
        duration: scenario.duration,
        application_id: activeApplication.id,
        release_id: releaseId,
        scenario_id: scenario.id,
      })
      if (start) {
        await api.startTest(test.id)
      }
      onSelectTest(test.id)
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to create test'))
    } finally {
      setCreating(false)
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>New Test</h2>
          <p className="page-subtitle">Choose the release context, scenario, and run setup.</p>
        </div>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      <div className="new-test-layout">
        <div className="new-test-main">
          <section className="new-test-step">
            <div className="new-test-step-header">
              <span className="new-test-step-index">1</span>
              <div>
                <h3>Release Context</h3>
                <p>Results are always stored against one release.</p>
              </div>
            </div>
            <div className="settings-group">
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Application</span>
                  <span className="setting-desc">Top-level application under test</span>
                </div>
                <span className="setting-value">{activeApplication?.name || 'No application selected'}</span>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Release</span>
                  <span className="setting-desc">Required target for results</span>
                </div>
                <select className="settings-input" value={releaseId} onChange={(event) => setReleaseId(event.target.value)}>
                  <option value="">Select release</option>
                  {releases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                </select>
                <div className="inline-create-row">
                  <input className="settings-input" value={newReleaseName} onChange={event => setNewReleaseName(event.target.value)} placeholder="Create release, e.g. PI30" />
                  <button className="btn btn-secondary" onClick={createRelease} disabled={!newReleaseName.trim()}>Add Release</button>
                </div>
              </div>
            </div>
          </section>

          <section className="new-test-step">
            <div className="new-test-step-header">
              <span className="new-test-step-index">2</span>
              <div>
                <h3>Scenario</h3>
                <p>Choose the reusable workload definition to execute.</p>
              </div>
            </div>
            <div className="settings-group">
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Reusable Scenario</span>
                  <span className="setting-desc">Scenarios define scripts and runtime shape</span>
                </div>
                <select className="settings-input" value={scenarioId} onChange={(event) => { setScenarioId(event.target.value); setRunName('') }}>
                  <option value="">Select scenario</option>
                  {scenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                </select>
              </div>
              <div className="setting-row">
                <div className="setting-info">
                  <span className="setting-label">Run Name</span>
                  <span className="setting-desc">Shown in results and monitoring</span>
                </div>
                <input className="settings-input" value={runName} onChange={event => setRunName(event.target.value)} placeholder={scenario?.name || 'Run name'} />
              </div>
              {scenario && (
                <div className="new-test-detail-grid">
                  <div>
                    <span>Mode</span>
                    <strong>{formatScenarioMode(scenario.scenario_mode)}</strong>
                  </div>
                  <div>
                    <span>Created</span>
                    <strong>{formatDate(scenario.created_at)}</strong>
                  </div>
                  <div>
                    <span>Last Run</span>
                    <strong>{formatDate(scenario.last_run_at)}</strong>
                  </div>
                  <div>
                    <span>Results</span>
                    <strong>{scenario.result_count ?? 0}</strong>
                  </div>
                  <div>
                    <span>Shared Data Pools</span>
                    <strong>{scenario.data_pools?.length || 0}</strong>
                  </div>
                </div>
              )}
              {scenario && (
                <div className="new-test-associated-row">
                  <span className="setting-label">Associated Releases</span>
                  {associatedReleases.length > 0 ? (
                    <div className="new-test-tag-list">
                      {associatedReleases.map((item) => <span key={item.id} className="new-test-tag">{item.name}</span>)}
                    </div>
                  ) : (
                    <p className="new-test-inline-note">This scenario has not been run against a release yet.</p>
                  )}
                </div>
              )}
            </div>
          </section>

          <section className="new-test-step">
            <div className="new-test-step-header">
              <span className="new-test-step-index">3</span>
              <div>
                <h3>Run Review</h3>
                <p>Confirm the workload before creating the run.</p>
              </div>
            </div>
            {scenario ? (
              <div className="data-table">
                <table>
                  <thead>
                    <tr>
                      <th>Group</th>
                      <th>Script</th>
                      <th>Protocol</th>
                      <th>VUsers</th>
                      <th>Ramp Up</th>
                      <th>Ramp Down</th>
                    </tr>
                  </thead>
                  <tbody>
                    {scenario.scripts.map(script => (
                      <tr key={`${script.order}-${script.script_id}`}>
                        <td className="cell-name">{script.name || script.script_name}</td>
                        <td>{script.script_name || script.script_id}</td>
                        <td><span className="protocol-badge">{script.protocol || 'http'}</span></td>
                        <td>{script.vusers}</td>
                        <td className="cell-muted">{script.ramp_up || 'None'}</td>
                        <td className="cell-muted">{script.ramp_down || 'None'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className="new-test-empty">Select a scenario to preview scripts, VUsers, and ramp behavior.</div>
            )}
            {scenario && (
              <>
              {(scenario.data_pools || []).length > 0 && (
                <div className="new-test-associated-row">
                  <span className="setting-label">Shared Data Pools</span>
                  <div className="new-test-tag-list">
                    {(scenario.data_pools || []).map(pool => (
                      <span key={`${pool.pool_id}-${pool.alias}`} className="new-test-tag">
                        {pool.alias}: {pool.pool_name || pool.pool_id} / {pool.selection_strategy}
                      </span>
                    ))}
                  </div>
                </div>
              )}
              <div className="new-test-detail-grid review">
                <div>
                  <span>Duration</span>
                  <strong>{scenario.duration}</strong>
                </div>
                <div>
                  <span>Iterations</span>
                  <strong>{scenario.runtime_settings?.iterations ?? 'Script default'}</strong>
                </div>
                <div>
                  <span>Log Level</span>
                  <strong>{scenario.runtime_settings?.log_level || 'Script default'}</strong>
                </div>
                <div>
                  <span>Auto Transactions</span>
                  <strong>{scenario.runtime_settings?.auto_transactions || 'Script default'}</strong>
                </div>
                <div>
                  <span>Snapshot On Error</span>
                  <strong>{scenario.runtime_settings?.error_handling?.snapshot_on_error ? 'Enabled' : 'Script default'}</strong>
                </div>
                <div>
                  <span>Script Shakeout</span>
                  <strong>{scenario.script_shakeout ? 'Enabled' : 'Disabled'}</strong>
                </div>
                <div>
                  <span>Resource Baseline</span>
                  <strong>{scenario.resource_baseline ? '5m before and after' : 'Disabled'}</strong>
                </div>
              </div>
              </>
            )}
          </section>
        </div>

        <aside className="new-test-summary">
          <h3>Launch Readiness</h3>
          <div className="new-test-summary-grid">
            <div><span>Application</span><strong>{activeApplication?.name || 'None'}</strong></div>
            <div><span>Release</span><strong>{release?.name || 'None'}</strong></div>
            <div><span>Scenario</span><strong>{scenario?.name || 'None'}</strong></div>
            <div><span>Total VUsers</span><strong>{totalVUsers}</strong></div>
            <div><span>Duration</span><strong>{scenario?.duration || 'Not set'}</strong></div>
            <div><span>Protocols</span><strong>{protocols.length ? protocols.join(', ') : 'None'}</strong></div>
          </div>
          <div className="readiness-list">
            {readiness.map(item => (
              <div className={`readiness-item ${item.ready ? 'ready' : 'blocked'}`} key={item.label}>
                <span>{item.ready ? 'Ready' : 'Needed'}</span>
                <p>{item.label}</p>
              </div>
            ))}
          </div>
          <div className="new-test-launch-actions">
            <button className="btn btn-secondary" onClick={() => createRun(false)} disabled={!canLaunch}>Create Queued Run</button>
            <button className="btn btn-primary" onClick={() => createRun(true)} disabled={!canLaunch}>Create & Start</button>
          </div>
        </aside>
      </div>
    </div>
  )
}
