import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { StoredScenario, StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import './TestHub.css'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function releaseList(scenario: StoredScenario) {
  const releases = scenario.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return releases.map(release => release.name).join(', ')
}

function TestHub({ onSelectTest }: Props) {
  const api = useApi()
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<any>({ name: '', duration: '1m', script_id: '', vusers: 10 })
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const [scenarioRes, scriptRes] = await Promise.all([api.listScenarios(), api.listScripts()])
      const nextScenarios = scenarioRes.scenarios || []
      const nextScripts = scriptRes.scripts || []
      setScenarios(nextScenarios)
      setScripts(nextScripts)
      setForm((prev: any) => ({ ...prev, script_id: prev.script_id || nextScripts[0]?.id || '' }))
    } catch (err: any) {
      setError(err.message || 'Unable to load scenarios')
    }
  }, [api])

  useEffect(() => { refresh() }, [refresh])

  const sorted = useMemo(
    () => [...scenarios].sort((a, b) => new Date(b.updated_at || b.created_at).getTime() - new Date(a.updated_at || a.created_at).getTime()),
    [scenarios],
  )

  const openCreate = () => {
    setEditingId(null)
    setForm({ name: '', duration: '1m', script_id: scripts[0]?.id || '', vusers: 10 })
    setShowForm(true)
  }

  const openEdit = (scenario: StoredScenario) => {
    const first = scenario.scripts[0]
    setEditingId(scenario.id)
    setForm({
      name: scenario.name,
      duration: scenario.duration,
      script_id: first?.script_id || scripts[0]?.id || '',
      vusers: first?.vusers || 10,
    })
    setShowForm(true)
  }

  const saveScenario = async () => {
    if (!form.name.trim() || !form.script_id) {
      setError('Scenario name and script are required.')
      return
    }
    const payload = {
      name: form.name.trim(),
      duration: form.duration || '1m',
      scenario_mode: 'vuser_driven',
      groups: [{
        script_id: form.script_id,
        vusers: Number(form.vusers) || 10,
        initialization: 'at_runtime',
      }],
    }
    try {
      if (editingId) await api.updateScenario(editingId, payload)
      else await api.createScenario(payload)
      setShowForm(false)
      setEditingId(null)
      refresh()
    } catch (err: any) {
      setError(err.message || 'Unable to save scenario')
    }
  }

  const deleteScenario = async (id: string) => {
    if (!confirm('Delete this scenario? Scenarios with results cannot be deleted.')) return
    try {
      await api.deleteScenario(id)
      refresh()
    } catch (err: any) {
      setError(err.message || 'Unable to delete scenario')
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scenarios</h2>
          <p className="page-subtitle">Reusable scenario definitions linked to scripts, releases, and results.</p>
        </div>
        <button className="btn btn-primary" onClick={openCreate}>+ New Scenario</button>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      {showForm && (
        <div className="page-section">
          <h3 className="section-title">{editingId ? 'Edit Scenario' : 'New Scenario'}</h3>
          <div className="settings-grid">
            <input className="settings-input" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} placeholder="Scenario name" />
            <input className="settings-input" value={form.duration} onChange={event => setForm({ ...form, duration: event.target.value })} placeholder="Duration, e.g. 5m" />
            <select className="settings-input" value={form.script_id} onChange={event => setForm({ ...form, script_id: event.target.value })}>
              {scripts.map(script => <option key={script.id} value={script.id}>{script.name}</option>)}
            </select>
            <input className="settings-input" type="number" min={1} value={form.vusers} onChange={event => setForm({ ...form, vusers: Number(event.target.value) })} />
          </div>
          <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginTop: 12 }}>
            <button className="btn btn-secondary" onClick={() => setShowForm(false)}>Cancel</button>
            <button className="btn btn-primary" onClick={saveScenario}>Save Scenario</button>
          </div>
        </div>
      )}

      <div className="page-section">
        <h3 className="section-title">Scenario Library ({scenarios.length})</h3>
        <div className="data-table">
          <table>
            <thead>
              <tr>
                <th>Name</th>
                <th>Scripts</th>
                <th>Created</th>
                <th>Associated Releases</th>
                <th>Last Run</th>
                <th>Results</th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map(scenario => (
                <tr key={scenario.id}>
                  <td className="cell-name">{scenario.name}</td>
                  <td>{scenario.scripts.map(script => script.script_name || script.name || script.script_id).join(', ')}</td>
                  <td className="cell-muted">{formatDate(scenario.created_at)}</td>
                  <td className="cell-muted">{releaseList(scenario)}</td>
                  <td className="cell-muted">{formatDate(scenario.last_run_at)}</td>
                  <td>{scenario.result_count || 0}</td>
                  <td style={{ textAlign: 'right' }}>
                    <button className="btn btn-sm btn-secondary" onClick={() => {
                      api.listScenarioResults(scenario.id).then((res: any) => {
                        const first = res.tests?.[0]
                        if (first) onSelectTest(first.id)
                      })
                    }}>Results</button>
                    <button className="btn btn-sm btn-ghost" onClick={() => openEdit(scenario)}>Edit</button>
                    <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={() => deleteScenario(scenario.id)}>Delete</button>
                  </td>
                </tr>
              ))}
              {sorted.length === 0 && (
                <tr><td colSpan={7} className="cell-muted">No scenarios yet. Create a scenario from an existing script.</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export default memo(TestHub)
