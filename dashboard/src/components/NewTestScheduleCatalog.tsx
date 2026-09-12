import { useEffect, useMemo, useState } from 'react'
import type { ScenarioSchedule, StoredScenario } from '../types'
import { useApi } from '../hooks/useApi'
import { scenarioIdentity } from './newTestPresentation'
import { formatScheduleTimestampInZone, formatScheduleToken } from './newTestScheduleFormat'

export default function NewTestScheduleCatalog({ scenarios, projectId, revision, onSelectScenario }: {
  scenarios: StoredScenario[]; projectId: string; revision: number; onSelectScenario: (id: string) => void
}) {
  const api = useApi()
  const [query, setQuery] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [result, setResult] = useState<{ scope: string; schedules: ScenarioSchedule[]; failures: string[] } | null>(null)
  const scope = `${projectId}:${revision}:${refresh}:${scenarios.map(item => item.id).join(',')}`
  useEffect(() => {
    let cancelled = false
    void (async () => {
      const schedules: ScenarioSchedule[] = []
      const failures: string[] = []
      for (const scenario of scenarios) {
        if (cancelled) return
        try {
          const response = await api.listScenarioSchedules(scenario.id)
          schedules.push(...(response.schedules || []))
        } catch { failures.push(scenarioIdentity(scenario)) }
      }
      if (!cancelled) setResult({ scope, schedules, failures })
    })()
    return () => { cancelled = true }
  }, [api, scenarios, scope])
  const current = result?.scope === scope ? result : null
  const rows = useMemo(() => (current?.schedules || []).filter(schedule => {
    const scenario = scenarios.find(item => item.id === schedule.scenario_id)
    return `${schedule.name} ${scenario ? scenarioIdentity(scenario) : schedule.scenario_id} ${schedule.next_required_action || ''}`.toLowerCase().includes(query.toLowerCase())
  }).sort((a, b) => (a.next_run_at || '9999').localeCompare(b.next_run_at || '9999') || a.name.localeCompare(b.name)), [current, query, scenarios])
  return <section className="new-test-step new-test-schedule-catalog" aria-label="All scenario schedules">
    <div className="new-test-step-header"><div><h3>All scenario schedules</h3><p>Upcoming runs and required actions across this project.</p></div><button type="button" className="btn btn-secondary" onClick={() => setRefresh(value => value + 1)}>Refresh schedules</button></div>
    <label className="new-test-catalog-search">Find a schedule<input type="search" className="settings-input" value={query} onChange={event => setQuery(event.target.value)} /></label>
    {!current ? <p role="status">Loading project schedules…</p> : <>
      {current.failures.length > 0 && <p role="alert">Some schedules could not be loaded: {current.failures.join('; ')}. Refresh to retry.</p>}
      {rows.length === 0 ? <p>{current.failures.length ? 'No schedules found in the scenarios that could be loaded.' : query ? 'No schedules match your search.' : 'No recurring schedules in this project.'}</p> : <div className="data-table"><table><thead><tr><th>Schedule / scenario</th><th>Next run</th><th>Status / required action</th><th>Actions</th></tr></thead><tbody>{rows.map(schedule => {
        const scenario = scenarios.find(item => item.id === schedule.scenario_id)
        return <tr key={schedule.id}><td><strong>{schedule.name}</strong><div>{scenario ? scenarioIdentity(scenario) : schedule.scenario_id}</div></td><td>{schedule.next_run_at ? formatScheduleTimestampInZone(schedule.next_run_at, schedule.timezone) : 'Not scheduled'}<div>{schedule.timezone || 'UTC'}</div></td><td>{schedule.enabled ? 'Enabled' : 'Paused'}<div>{formatScheduleToken(schedule.next_required_action) || 'No action required'}</div>{schedule.conflict_summary && <p>{schedule.conflict_summary}</p>}</td><td><button type="button" className="btn btn-secondary" onClick={() => onSelectScenario(schedule.scenario_id)} aria-label={`Manage ${schedule.name} for ${scenario?.name || schedule.scenario_id}`}>Manage</button></td></tr>
      })}</tbody></table></div>}
    </>}
  </section>
}
