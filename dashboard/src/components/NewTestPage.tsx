import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { Application, DashboardAuthEffectiveAccessResource, Release, ScenarioSchedule, ScenarioScheduleOccurrence, StoredScenario } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { getErrorMessage } from '../utils/errorUtils'

interface Props {
  activeApplication: Application | null
  activeRelease: Release | null
  releases: Release[]
  onSelectTest: (id: string) => void
  onCreateRelease: (name: string) => Promise<Release | null>
}

type ScheduleDraft = {
  name: string
  cron_expr: string
  timezone: string
  release_id: string
  run_name_template: string
  enabled: boolean
  approval_policy: string
  missed_run_policy: string
  blackout_start_time: string
  blackout_end_time: string
  availability_start_time: string
  availability_end_time: string
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatScheduleTimestamp(value?: string) {
  return value ? new Date(value).toLocaleString(undefined, { month: 'short', day: 'numeric', year: 'numeric', hour: 'numeric', minute: '2-digit' }) : 'Not scheduled'
}

function formatScenarioMode(value?: StoredScenario['scenario_mode']) {
  return value === 'goal_oriented' ? 'Goal Oriented' : 'VUser Driven'
}

function formatScheduleToken(value?: string) {
  return value ? value.replace(/_/g, ' ') : ''
}

function formatScheduleWindow(window: { name?: string, start_time: string, end_time: string, timezone?: string }) {
  const label = window.name ? `${window.name}: ` : ''
  return `${label}${window.start_time}-${window.end_time}${window.timezone ? ` ${window.timezone}` : ''}`
}

function scheduleStatusTone(value?: string) {
  if (!value) return ''
  if (['blocked', 'failed', 'rejected'].includes(value)) return 'danger'
  if (['approval_required', 'operator_decision_required', 'pending', 'skipped'].includes(value)) return 'warning'
  if (['materialized', 'queued', 'approved', 'completed', 'enabled'].includes(value)) return 'healthy'
  return ''
}

function formatRuntimePacing(scenario?: StoredScenario | null) {
  const pacing = scenario?.runtime_settings?.pacing
  if (!pacing?.type) return 'Script default'
  if (pacing.type === 'none') return 'None'
  if (pacing.type === 'fixed_delay') return `Fixed delay · ${pacing.delay_ms || 0}ms`
  if (pacing.type === 'fixed_interval') return `Fixed interval · ${pacing.interval_ms || 0}ms`
  if (pacing.type === 'tps') return `Target TPS · ${pacing.tps || 0}`
  return pacing.type.replace(/_/g, ' ')
}

function formatRuntimeThinkTime(scenario?: StoredScenario | null) {
  const thinkTime = scenario?.runtime_settings?.think_time
  if (!thinkTime) return 'Script default'
  if (thinkTime.ignore) return 'Disabled'
  return `${thinkTime.type || 'fixed'} · ${thinkTime.mean_ms || 0}ms`
}

function formatRuntimeContinueOnError(scenario?: StoredScenario | null) {
  const value = scenario?.runtime_settings?.error_handling?.continue_on_error
  if (value === undefined) return 'Script default'
  return value ? 'Enabled' : 'Disabled'
}

function buildDefaultScheduleDraft(scenario?: StoredScenario | null, releaseId?: string) {
  return {
    name: scenario ? `${scenario.name} Schedule` : '',
    cron_expr: '0 9 * * 1-5',
    timezone: 'UTC',
    release_id: releaseId || '',
    run_name_template: scenario?.name || '',
    enabled: true,
    approval_policy: 'auto',
    missed_run_policy: 'skip',
    blackout_start_time: '',
    blackout_end_time: '',
    availability_start_time: '',
    availability_end_time: '',
  }
}

function isEffectiveOperationAllowed(resources: DashboardAuthEffectiveAccessResource[], operationID: string): boolean {
  for (const resource of resources) {
    const operation = resource.operations.find(item => item.id === operationID)
    if (operation) {
      return operation.allowed
    }
  }
  return true
}

export default function NewTestPage({ activeApplication, activeRelease, releases, onSelectTest, onCreateRelease }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [pageMode, setPageMode] = useState<'launch' | 'schedules'>('launch')
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scenarioId, setScenarioId] = useState('')
  const [releaseId, setReleaseId] = useState(activeRelease?.id || '')
  const [schedules, setSchedules] = useState<ScenarioSchedule[]>([])
  const [scheduleLoading, setScheduleLoading] = useState(false)
  const [scheduleSubmitting, setScheduleSubmitting] = useState(false)
  const [editingScheduleId, setEditingScheduleId] = useState<string | null>(null)
  const [expandedScheduleId, setExpandedScheduleId] = useState<string | null>(null)
  const [scheduleOccurrenceLoading, setScheduleOccurrenceLoading] = useState<string | null>(null)
  const [scheduleOccurrences, setScheduleOccurrences] = useState<Record<string, ScenarioScheduleOccurrence[]>>({})
  const [scheduleDraft, setScheduleDraft] = useState<ScheduleDraft>(buildDefaultScheduleDraft())
  const [runName, setRunName] = useState('')
  const [newReleaseName, setNewReleaseName] = useState('')
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScenarios(activeProject?.id)
      const next = res.scenarios || []
      setScenarios(next)
      setScenarioId(prev => (next.some(item => item.id === prev) ? prev : next[0]?.id || ''))
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load scenarios'))
    }
  }, [activeProject?.id, api])

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => { setReleaseId(activeRelease?.id || releases[0]?.id || '') }, [activeRelease?.id, releases])
  useEffect(() => {
    let cancelled = false
    const loadEffectiveAccess = async () => {
      try {
        const response = await api.getAuthEffectiveAccess(activeProject?.id)
        if (!cancelled) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled) {
          setEffectiveAuthAccessResources([])
        }
      }
    }
    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, api])

  const scenario = useMemo(() => scenarios.find(item => item.id === scenarioId), [scenarios, scenarioId])
  const release = useMemo(() => releases.find(item => item.id === releaseId), [releases, releaseId])
  const refreshSchedules = useCallback(async (currentScenarioId: string) => {
    if (!currentScenarioId) {
      setSchedules([])
      return
    }
    setScheduleLoading(true)
    try {
      const res = await api.listScenarioSchedules(currentScenarioId)
      setSchedules(res.schedules || [])
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load schedules'))
    } finally {
      setScheduleLoading(false)
    }
  }, [api])
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
  const canManageTests = useMemo(() => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_tests'), [effectiveAuthAccessResources])
  const canLaunch = readiness.every(item => item.ready) && !creating && canManageTests
  const nextScheduledRun = useMemo(() => schedules.filter(item => item.enabled && item.next_run_at).sort((left, right) => (left.next_run_at || '').localeCompare(right.next_run_at || ''))[0], [schedules])
  const schedulesNeedingAction = useMemo(() => schedules.filter(item => item.next_required_action || item.pending_approval_id || item.conflict_summary || item.last_error).length, [schedules])
  const scheduleConflictCount = useMemo(() => schedules.filter(item => item.conflict_summary).length, [schedules])

  useEffect(() => {
    if (scenario && !runName) setRunName(scenario.name)
  }, [scenario, runName])
  useEffect(() => {
    void refreshSchedules(scenarioId)
  }, [refreshSchedules, scenarioId])
  useEffect(() => {
    if (!editingScheduleId) {
      setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId))
    }
  }, [editingScheduleId, releaseId, scenario])

  const createRelease = async () => {
    const name = newReleaseName.trim()
    if (!name) return
    const created = await onCreateRelease(name)
    if (created) setReleaseId(created.id)
    setNewReleaseName('')
  }

  const createRun = async (start: boolean) => {
    if (!canManageTests) {
      setError('Current session cannot create or start test runs for this project.')
      return
    }
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
        project_id: activeProject?.id,
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

  const submitSchedule = async () => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) {
	  setError('Choose a scenario before managing schedules.')
	  return
	}
	setScheduleSubmitting(true)
	setError('')
	const payload = {
	  name: scheduleDraft.name.trim(),
	  cron_expr: scheduleDraft.cron_expr.trim(),
	  timezone: scheduleDraft.timezone.trim() || 'UTC',
	  release_id: scheduleDraft.release_id || undefined,
	  run_name_template: scheduleDraft.run_name_template.trim() || undefined,
	  enabled: scheduleDraft.enabled,
	  approval_policy: scheduleDraft.approval_policy,
	  missed_run_policy: scheduleDraft.missed_run_policy,
	  blackout_windows: scheduleDraft.blackout_start_time && scheduleDraft.blackout_end_time ? [{
	    name: 'dashboard-blackout',
	    start_time: scheduleDraft.blackout_start_time,
	    end_time: scheduleDraft.blackout_end_time,
	    timezone: scheduleDraft.timezone.trim() || 'UTC',
	  }] : undefined,
	  availability_windows: scheduleDraft.availability_start_time && scheduleDraft.availability_end_time ? [{
	    name: 'dashboard-availability',
	    days_of_week: [1, 2, 3, 4, 5],
	    start_time: scheduleDraft.availability_start_time,
	    end_time: scheduleDraft.availability_end_time,
	    timezone: scheduleDraft.timezone.trim() || 'UTC',
	  }] : undefined,
	}
	try {
	  if (editingScheduleId) {
	    await api.updateScenarioSchedule(scenario.id, editingScheduleId, payload)
	  } else {
	    await api.createScenarioSchedule(scenario.id, payload)
	  }
	  setEditingScheduleId(null)
	  setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId))
	  await refreshSchedules(scenario.id)
	} catch (err: unknown) {
	  setError(getErrorMessage(err, 'Unable to save schedule'))
	} finally {
	  setScheduleSubmitting(false)
	}
  }

  const editSchedule = (schedule: ScenarioSchedule) => {
	setEditingScheduleId(schedule.id)
	setScheduleDraft({
	  name: schedule.name,
	  cron_expr: schedule.cron_expr,
	  timezone: schedule.timezone || 'UTC',
	  release_id: schedule.release_id || '',
	  run_name_template: schedule.run_name_template || '',
	  enabled: schedule.enabled,
	  approval_policy: schedule.approval_policy || 'auto',
	  missed_run_policy: schedule.missed_run_policy || 'skip',
	  blackout_start_time: schedule.blackout_windows?.[0]?.start_time || '',
	  blackout_end_time: schedule.blackout_windows?.[0]?.end_time || '',
	  availability_start_time: schedule.availability_windows?.[0]?.start_time || '',
	  availability_end_time: schedule.availability_windows?.[0]?.end_time || '',
	})
  }

  const toggleSchedule = async (schedule: ScenarioSchedule) => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	setError('')
	try {
	  await api.updateScenarioSchedule(scenario.id, schedule.id, {
	    name: schedule.name,
	    cron_expr: schedule.cron_expr,
	    timezone: schedule.timezone || 'UTC',
	    release_id: schedule.release_id || undefined,
	    run_name_template: schedule.run_name_template || undefined,
	    enabled: !schedule.enabled,
	    approval_policy: schedule.approval_policy || 'auto',
	    missed_run_policy: schedule.missed_run_policy || 'skip',
	    blackout_windows: schedule.blackout_windows,
	    availability_windows: schedule.availability_windows,
	  })
	  await refreshSchedules(scenario.id)
	} catch (err: unknown) {
	  setError(getErrorMessage(err, 'Unable to update schedule'))
	}
  }

  const deleteSchedule = async (scheduleId: string) => {
	if (!canManageTests) {
	  setError('Current session cannot manage scenario schedules for this project.')
	  return
	}
	if (!scenario) return
	setError('')
	try {
	  await api.deleteScenarioSchedule(scenario.id, scheduleId)
	  if (editingScheduleId === scheduleId) {
	    setEditingScheduleId(null)
	    setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId))
	  }
	  await refreshSchedules(scenario.id)
	} catch (err: unknown) {
	  setError(getErrorMessage(err, 'Unable to delete schedule'))
	}
  }

  const resolveScheduleApproval = async (schedule: ScenarioSchedule, approved: boolean) => {
    if (!canManageTests) {
      setError('Current session cannot manage scenario schedules for this project.')
      return
    }
    if (!scenario || !schedule.pending_approval_id) return
    setError('')
    try {
      if (approved) {
        await api.approveScenarioScheduleApproval(scenario.id, schedule.id, schedule.pending_approval_id, 'Approved from schedule dashboard.')
      } else {
        await api.rejectScenarioScheduleApproval(scenario.id, schedule.id, schedule.pending_approval_id, 'Rejected from schedule dashboard.')
      }
      await refreshSchedules(scenario.id)
    } catch (err: unknown) {
      setError(getErrorMessage(err, approved ? 'Unable to approve schedule' : 'Unable to reject schedule'))
    }
  }

  const toggleScheduleOccurrences = async (schedule: ScenarioSchedule) => {
    if (!scenario) return
    if (expandedScheduleId === schedule.id) {
      setExpandedScheduleId(null)
      return
    }
    setExpandedScheduleId(schedule.id)
    if (scheduleOccurrences[schedule.id]) {
      return
    }
    setScheduleOccurrenceLoading(schedule.id)
    setError('')
    try {
      const response = await api.listScenarioScheduleOccurrences(scenario.id, schedule.id)
      setScheduleOccurrences(prev => ({ ...prev, [schedule.id]: response.occurrences || [] }))
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load schedule occurrences'))
    } finally {
      setScheduleOccurrenceLoading(null)
    }
  }

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>New Test</h2>
          <p className="page-subtitle">Choose the release context, scenario, and run setup.</p>
        </div>
        <div className="inline-create-row">
          <button className={`btn ${pageMode === 'launch' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => setPageMode('launch')}>Launch Runs</button>
          <button className={`btn ${pageMode === 'schedules' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => setPageMode('schedules')}>Schedules</button>
        </div>
      </div>

      {error && <div className="settings-error-inline">{error}</div>}
      {!canManageTests && <div className="settings-error-inline">Current session can view scenarios but cannot create runs or schedules for this project.</div>}

      {pageMode === 'schedules' ? (
        <div className="new-test-layout">
          <div className="new-test-main">
            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className="new-test-step-index">1</span>
                <div>
                  <h3>Scenario Schedule</h3>
                  <p>Bind a recurring cron expression to a reusable scenario.</p>
                </div>
              </div>
              <div className="settings-group">
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Scenario</span>
                    <span className="setting-desc">Recurring runs always execute one stored scenario</span>
                  </div>
                  <select className="settings-input" value={scenarioId} onChange={(event) => { setScenarioId(event.target.value); setEditingScheduleId(null) }}>
                    <option value="">Select scenario</option>
                    {scenarios.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Schedule Name</span>
                    <span className="setting-desc">Human-friendly label for the recurring trigger</span>
                  </div>
                  <input className="settings-input" value={scheduleDraft.name} onChange={(event) => setScheduleDraft(prev => ({ ...prev, name: event.target.value }))} placeholder="Nightly smoke" />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Cron Expression</span>
                    <span className="setting-desc">Five-field cron format, for example `0 9 * * 1-5`</span>
                  </div>
                  <input className="settings-input" value={scheduleDraft.cron_expr} onChange={(event) => setScheduleDraft(prev => ({ ...prev, cron_expr: event.target.value }))} placeholder="0 9 * * 1-5" />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Timezone</span>
                    <span className="setting-desc">IANA timezone used for cron and calendar windows</span>
                  </div>
                  <input className="settings-input" value={scheduleDraft.timezone} onChange={(event) => setScheduleDraft(prev => ({ ...prev, timezone: event.target.value }))} placeholder="UTC" />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Release Context</span>
                    <span className="setting-desc">Optional release to attach to scheduled runs</span>
                  </div>
                  <select className="settings-input" value={scheduleDraft.release_id} onChange={(event) => setScheduleDraft(prev => ({ ...prev, release_id: event.target.value }))}>
                    <option value="">No release</option>
                    {releases.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Run Name Template</span>
                    <span className="setting-desc">Default run name used when the schedule fires</span>
                  </div>
                  <input className="settings-input" value={scheduleDraft.run_name_template} onChange={(event) => setScheduleDraft(prev => ({ ...prev, run_name_template: event.target.value }))} placeholder={scenario?.name || 'Scheduled run'} />
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Approval Policy</span>
                    <span className="setting-desc">Require approval before materialization for governed schedules.</span>
                  </div>
                  <select className="settings-input" value={scheduleDraft.approval_policy} onChange={(event) => setScheduleDraft(prev => ({ ...prev, approval_policy: event.target.value }))}>
                    <option value="auto">Auto approve</option>
                    <option value="manual_review">Manual review</option>
                    <option value="high_cost">High cost</option>
                    <option value="off_hours">Off hours</option>
                    <option value="production">Production</option>
                    <option value="quota_burst">Quota burst</option>
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Missed Run Policy</span>
                    <span className="setting-desc">Choose how restart reconciliation handles missed cron windows.</span>
                  </div>
                  <select className="settings-input" value={scheduleDraft.missed_run_policy} onChange={(event) => setScheduleDraft(prev => ({ ...prev, missed_run_policy: event.target.value }))}>
                    <option value="skip">Skip missed runs</option>
                    <option value="catch_up_once">Catch up once</option>
                    <option value="catch_up_all">Catch up all</option>
                    <option value="require_operator_decision">Require operator decision</option>
                  </select>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Blackout Window</span>
                    <span className="setting-desc">Optional daily no-run range in the schedule timezone</span>
                  </div>
                  <div className="inline-create-row">
                    <input className="settings-input" value={scheduleDraft.blackout_start_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, blackout_start_time: event.target.value }))} placeholder="00:00" />
                    <input className="settings-input" value={scheduleDraft.blackout_end_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, blackout_end_time: event.target.value }))} placeholder="06:00" />
                  </div>
                </div>
                <div className="setting-row">
                  <div className="setting-info">
                    <span className="setting-label">Availability Window</span>
                    <span className="setting-desc">Optional weekday allowed-run range in the schedule timezone</span>
                  </div>
                  <div className="inline-create-row">
                    <input className="settings-input" value={scheduleDraft.availability_start_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, availability_start_time: event.target.value }))} placeholder="09:00" />
                    <input className="settings-input" value={scheduleDraft.availability_end_time} onChange={(event) => setScheduleDraft(prev => ({ ...prev, availability_end_time: event.target.value }))} placeholder="17:00" />
                  </div>
                </div>
                <label className="setting-row" style={{ alignItems: 'center', gap: 12 }}>
                  <input type="checkbox" checked={scheduleDraft.enabled} onChange={(event) => setScheduleDraft(prev => ({ ...prev, enabled: event.target.checked }))} />
                  <div className="setting-info">
                    <span className="setting-label">Enabled</span>
                    <span className="setting-desc">Disabled schedules stay saved but do not trigger</span>
                  </div>
                </label>
                <div className="inline-create-row">
                  <button className="btn btn-primary" onClick={submitSchedule} disabled={!scenario || scheduleSubmitting || !canManageTests}>{editingScheduleId ? 'Update Schedule' : 'Save Schedule'}</button>
                  {editingScheduleId && <button className="btn btn-secondary" onClick={() => { setEditingScheduleId(null); setScheduleDraft(buildDefaultScheduleDraft(scenario, releaseId)) }}>Cancel Edit</button>}
                </div>
              </div>
            </section>

            <section className="new-test-step">
              <div className="new-test-step-header">
                <span className="new-test-step-index">2</span>
                <div>
                  <h3>Recurring Triggers</h3>
                  <p>Review next run times, recent executions, and disable or remove stale schedules.</p>
                </div>
              </div>
              {scheduleLoading ? (
                <div className="new-test-empty">Loading schedules…</div>
              ) : schedules.length > 0 ? (
                <div className="data-table">
                  <table>
                    <thead>
                      <tr>
                        <th>Name</th>
                        <th>Cron</th>
                        <th>Next Run</th>
                        <th>Last Run</th>
                        <th>Release</th>
                        <th>Policy</th>
                        <th>Status</th>
                        <th>Actions</th>
                      </tr>
                    </thead>
                    <tbody>
                      {schedules.map(item => {
                        const occurrences = scheduleOccurrences[item.id] || []
                        const lastOccurrence = item.last_occurrence
                        return (
                          <Fragment key={item.id}>
                            <tr>
                              <td className="cell-name">
                                {item.name}
                                <div className="schedule-governance-chips">
                                  <span className="script-health-token">rev {item.revision || 1}</span>
                                  {item.owner_display_name && <span className="script-health-token">owner {item.owner_display_name}</span>}
                                  {item.pending_approval_id && <span className="script-health-token warning">approval pending</span>}
                                  {item.conflict_summary && <span className="script-health-token danger">conflict</span>}
                                </div>
                              </td>
                              <td className="cell-mono">
                                {item.cron_expr}
                                <div className="schedule-governance-chips">
                                  <span className="script-health-token">{item.timezone || 'UTC'}</span>
                                  {!!item.blackout_windows?.length && <span className="script-health-token warning">{item.blackout_windows.length} blackout</span>}
                                  {!!item.maintenance_windows?.length && <span className="script-health-token warning">{item.maintenance_windows.length} maintenance</span>}
                                  {!!item.availability_windows?.length && <span className="script-health-token healthy">{item.availability_windows.length} availability</span>}
                                </div>
                              </td>
                              <td>{formatScheduleTimestamp(item.next_run_at)}</td>
                              <td>{formatScheduleTimestamp(item.last_run_at)}</td>
                              <td>{releases.find(releaseItem => releaseItem.id === item.release_id)?.name || 'None'}</td>
                              <td>
                                <div className="schedule-governance-chips">
                                  <span className="script-health-token">{formatScheduleToken(item.approval_policy || 'auto')}</span>
                                  <span className="script-health-token">{formatScheduleToken(item.missed_run_policy || 'skip')}</span>
                                </div>
                              </td>
                              <td>
                                <div className="schedule-status-stack">
                                  <span className={`script-health-token ${item.enabled ? 'healthy' : 'warning'}`}>{item.enabled ? 'Enabled' : 'Paused'}</span>
                                  {item.next_required_action && <span className={`script-health-token ${scheduleStatusTone(item.next_required_action)}`}>{formatScheduleToken(item.next_required_action)}</span>}
                                  {lastOccurrence?.status && <span className={`script-health-token ${scheduleStatusTone(lastOccurrence.status)}`}>last {formatScheduleToken(lastOccurrence.status)}</span>}
                                  {lastOccurrence?.reason_code && <span className="script-health-token">{formatScheduleToken(lastOccurrence.reason_code)}</span>}
                                </div>
                                {item.conflict_summary && <div className="schedule-governance-detail text-danger">{item.conflict_summary}</div>}
                                {item.last_error && <div className="schedule-governance-detail text-danger">{item.last_error}</div>}
                                {item.governance?.rbac_review?.decision && (
                                  <div className="schedule-governance-detail">
                                    RBAC {formatScheduleToken(item.governance.rbac_review.decision)}
                                    {item.governance.rbac_review.review_source ? ` via ${formatScheduleToken(item.governance.rbac_review.review_source)}` : ''}
                                  </div>
                                )}
                                {item.governance?.quota_review?.decision && (
                                  <div className="schedule-governance-detail">
                                    Quota {formatScheduleToken(item.governance.quota_review.decision)}
                                    {item.governance.quota_review.estimated_peak_vusers ? ` · peak ${item.governance.quota_review.estimated_peak_vusers} VUsers` : ''}
                                  </div>
                                )}
                                {item.governance?.quota_review?.findings?.slice(0, 1).map(finding => (
                                  <div key={finding} className="schedule-governance-detail text-danger">{finding}</div>
                                ))}
                              </td>
                              <td>
                                <div className="inline-create-row">
                                  {item.next_required_action === 'approval_required' && item.pending_approval_id && (
                                    <>
                                      <button className="btn btn-secondary" onClick={() => resolveScheduleApproval(item, true)} disabled={!canManageTests}>Approve</button>
                                      <button className="btn btn-secondary" onClick={() => resolveScheduleApproval(item, false)} disabled={!canManageTests}>Reject</button>
                                    </>
                                  )}
                                  <button className="btn btn-secondary" onClick={() => toggleScheduleOccurrences(item)}>{expandedScheduleId === item.id ? 'Hide Audit' : 'Audit Trail'}</button>
                                  <button className="btn btn-secondary" onClick={() => editSchedule(item)} disabled={!canManageTests}>Edit</button>
                                  <button className="btn btn-secondary" onClick={() => toggleSchedule(item)} disabled={!canManageTests}>{item.enabled ? 'Pause' : 'Enable'}</button>
                                  <button className="btn btn-secondary" onClick={() => deleteSchedule(item.id)} disabled={!canManageTests}>Delete</button>
                                </div>
                              </td>
                            </tr>
                            {expandedScheduleId === item.id && (
                              <tr className="no-hover" key={`${item.id}-occurrences`}>
                                <td colSpan={8} style={{ background: 'var(--bg-primary)' }}>
                                  <div className="schedule-occurrence-panel">
                                    <div className="schedule-occurrence-header">
                                      <strong>Occurrence Audit Trail</strong>
                                      <span>{scheduleOccurrenceLoading === item.id ? 'Loading...' : `${occurrences.length} records`}</span>
                                    </div>
                                    {scheduleOccurrenceLoading === item.id ? (
                                      <div className="cell-muted">Loading schedule occurrence history...</div>
                                    ) : occurrences.length === 0 ? (
                                      <div className="cell-muted">No materialized occurrences recorded yet.</div>
                                    ) : (
                                      <div className="data-table">
                                        <table>
                                          <thead>
                                            <tr>
                                              <th>Planned</th>
                                              <th>Materialized</th>
                                              <th>Status</th>
                                              <th>Reason</th>
                                              <th>Test</th>
                                              <th>Approval</th>
                                              <th>Conflict</th>
                                            </tr>
                                          </thead>
                                          <tbody>
                                            {occurrences.map(occurrence => (
                                              <tr key={occurrence.id}>
                                                <td>{formatScheduleTimestamp(occurrence.planned_at)}</td>
                                                <td>{formatScheduleTimestamp(occurrence.materialized_at)}</td>
                                                <td><span className={`script-health-token ${scheduleStatusTone(occurrence.status)}`}>{formatScheduleToken(occurrence.status)}</span></td>
                                                <td>{formatScheduleToken(occurrence.reason_code) || 'None'}</td>
                                                <td className="cell-mono">{occurrence.test_id || 'None'}</td>
                                                <td className="cell-mono">{occurrence.approval_id || 'None'}</td>
                                                <td>{occurrence.conflict_summary || 'None'}</td>
                                              </tr>
                                            ))}
                                          </tbody>
                                        </table>
                                      </div>
                                    )}
                                    {(item.blackout_windows?.length || item.maintenance_windows?.length || item.availability_windows?.length) ? (
                                      <div className="schedule-window-summary">
                                        {item.blackout_windows?.map(window => (
                                          <span key={`blackout-${window.name || window.start_time}`} className="script-health-token warning">Blackout {formatScheduleWindow(window)}</span>
                                        ))}
                                        {item.maintenance_windows?.map(window => (
                                          <span key={`maintenance-${window.name || window.start_time}`} className="script-health-token warning">Maintenance {formatScheduleWindow(window)}</span>
                                        ))}
                                        {item.availability_windows?.map(window => (
                                          <span key={`availability-${window.name || window.start_time}`} className="script-health-token healthy">Availability {formatScheduleWindow(window)}</span>
                                        ))}
                                      </div>
                                    ) : null}
                                  </div>
                                </td>
                              </tr>
                            )}
                          </Fragment>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="new-test-empty">No schedules exist for this scenario yet.</div>
              )}
            </section>
          </div>

          <aside className="new-test-summary">
            <h3>Schedule Summary</h3>
            <div className="new-test-summary-grid">
              <div><span>Scenario</span><strong>{scenario?.name || 'None'}</strong></div>
              <div><span>Schedules</span><strong>{schedules.length}</strong></div>
              <div><span>Enabled</span><strong>{schedules.filter(item => item.enabled).length}</strong></div>
              <div><span>Needs Action</span><strong>{schedulesNeedingAction}</strong></div>
              <div><span>Conflicts</span><strong>{scheduleConflictCount}</strong></div>
              <div><span>Next Run</span><strong>{nextScheduledRun ? formatScheduleTimestamp(nextScheduledRun.next_run_at) : 'Not scheduled'}</strong></div>
            </div>
            <div className="readiness-list">
              <div className={`readiness-item ${scenario ? 'ready' : 'blocked'}`}>
                <span>{scenario ? 'Ready' : 'Needed'}</span>
                <p>{scenario ? `${scenario.name} selected` : 'Select a scenario to manage schedules'}</p>
              </div>
              <div className={`readiness-item ${scheduleDraft.name.trim() && scheduleDraft.cron_expr.trim() ? 'ready' : 'blocked'}`}>
                <span>{scheduleDraft.name.trim() && scheduleDraft.cron_expr.trim() ? 'Ready' : 'Needed'}</span>
                <p>{scheduleDraft.name.trim() && scheduleDraft.cron_expr.trim() ? 'Schedule form is complete' : 'Enter a name and cron expression'}</p>
              </div>
            </div>
          </aside>
        </div>
      ) : (
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
                  <span>Pacing</span>
                  <strong>{formatRuntimePacing(scenario)}</strong>
                </div>
                <div>
                  <span>Think Time</span>
                  <strong>{formatRuntimeThinkTime(scenario)}</strong>
                </div>
                <div>
                  <span>Continue On Error</span>
                  <strong>{formatRuntimeContinueOnError(scenario)}</strong>
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
      )}
    </div>
  )
}
