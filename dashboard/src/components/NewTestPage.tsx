import { useCallback, useEffect, useMemo, useState } from 'react'
import { Application, Release, StoredScenario } from '../types'
import { useApi } from '../hooks/useApi'

interface Props {
  activeApplication: Application | null
  activeRelease: Release | null
  releases: Release[]
  onSelectTest: (id: string) => void
  onCreateRelease: (name: string) => Promise<Release | null>
}

export default function NewTestPage({ activeApplication, activeRelease, releases, onSelectTest, onCreateRelease }: Props) {
  const api = useApi()
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scenarioId, setScenarioId] = useState('')
  const [releaseId, setReleaseId] = useState(activeRelease?.id || '')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScenarios()
      const next = res.scenarios || []
      setScenarios(next)
      if (!scenarioId && next[0]) setScenarioId(next[0].id)
    } catch (err: any) {
      setError(err.message || 'Unable to load scenarios')
    }
  }, [api, scenarioId])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => { setReleaseId(activeRelease?.id || releases[0]?.id || '') }, [activeRelease?.id, releases])

  const scenario = useMemo(() => scenarios.find(item => item.id === scenarioId), [scenarios, scenarioId])

  const createRelease = async () => {
    const name = window.prompt('Release name')
    if (!name?.trim()) return
    const created = await onCreateRelease(name.trim())
    if (created) setReleaseId(created.id)
  }

  const createRun = async (start: boolean) => {
    if (!scenario || !releaseId) {
      setError('Choose a release and scenario before creating a test.')
      return
    }
    setCreating(true)
    setError('')
    try {
      const test = await api.createTest({
        name: scenario.name,
        release_id: releaseId,
        scenario_id: scenario.id,
      })
      if (start) {
        await api.startTest(test.id)
      }
      onSelectTest(test.id)
    } catch (err: any) {
      setError(err.message || 'Unable to create test')
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
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn btn-secondary" onClick={() => createRun(false)} disabled={creating || !scenario || !releaseId}>Create Queued Run</button>
          <button className="btn btn-primary" onClick={() => createRun(true)} disabled={creating || !scenario || !releaseId}>Create & Start</button>
        </div>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      <div className="settings-grid">
        <div className="settings-card">
          <h3>Release Context</h3>
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
              <div style={{ display: 'flex', gap: 8 }}>
                <select className="settings-input" value={releaseId} onChange={(event) => setReleaseId(event.target.value)}>
                  <option value="">Select release</option>
                  {releases.map(release => <option key={release.id} value={release.id}>{release.name}</option>)}
                </select>
                <button className="btn btn-secondary" onClick={createRelease}>New</button>
              </div>
            </div>
          </div>
        </div>

        <div className="settings-card">
          <h3>Scenario</h3>
          <div className="settings-group">
            <div className="setting-row">
              <div className="setting-info">
                <span className="setting-label">Reusable Scenario</span>
                <span className="setting-desc">Scenarios define scripts and runtime shape</span>
              </div>
              <select className="settings-input" value={scenarioId} onChange={(event) => setScenarioId(event.target.value)}>
                <option value="">Select scenario</option>
                {scenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
              </select>
            </div>
          </div>
        </div>
      </div>

      {scenario && (
        <div className="page-section">
          <h3 className="section-title">Run Review</h3>
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
        </div>
      )}
    </div>
  )
}
