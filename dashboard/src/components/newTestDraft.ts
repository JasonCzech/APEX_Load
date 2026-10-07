import type { MonitorBinding, MonitorRule } from '../types/runMonitoring'
import type { GroupRunOverride, RunDefaultOverrides } from './newTestRunModel'
import type { RunTraceContextSettings } from './runTraceContext'

export type ScheduleWindowDraft = {
  name: string
  days_of_week: number[]
  start_time: string
  end_time: string
  timezone?: string
}
export type ScheduleDraft = {
  revision?: number
  name: string
  cron_expr: string
  timezone: string
  release_id: string
  timeslot_id: string
  run_name_template: string
  enabled: boolean
  approval_policy: string
  missed_run_policy: string
  blackout_windows: ScheduleWindowDraft[]
  maintenance_windows: ScheduleWindowDraft[]
  availability_windows: ScheduleWindowDraft[]
  disabled_from: string
  disabled_until: string
}
export interface NewTestDraft {
  version: 1
  projectId: string
  applicationId: string
  scenarioId: string
  scenarioUpdatedAt: string
  pageMode: 'launch' | 'schedules'
  runName: string
  releaseId: string
  runDefaultOverrides: RunDefaultOverrides
  groupRunOverrides: Record<string, GroupRunOverride>
  scheduledStartLocal: string
  selectedTimeslotId: string
  elkConnectionId?: string
  dynatraceConnectionId?: string
  monitorBindings?: MonitorBinding[]
  traceContext?: RunTraceContextSettings
  monitorRules?: MonitorRule[]
  newReleaseName: string
  showNewRelease: boolean
  scheduleDraft: ScheduleDraft
  editingScheduleId: string | null
}
const key = (project: string, application: string) => `apexload.newTest.draft.v1.${encodeURIComponent(project)}.${encodeURIComponent(application)}`
const record = (value: unknown): value is Record<string, unknown> => Boolean(value && typeof value === 'object' && !Array.isArray(value))

function validDraft(value: unknown): value is NewTestDraft {
  if (!record(value) || value.version !== 1) return false
  if (!['projectId', 'applicationId', 'scenarioId', 'scenarioUpdatedAt', 'runName', 'releaseId', 'scheduledStartLocal', 'selectedTimeslotId', 'newReleaseName'].every(field => typeof value[field] === 'string')) return false
  if (value.elkConnectionId !== undefined && typeof value.elkConnectionId !== 'string') return false
  if (value.dynatraceConnectionId !== undefined && typeof value.dynatraceConnectionId !== 'string') return false
  if (value.monitorBindings !== undefined && (!Array.isArray(value.monitorBindings) || value.monitorBindings.length > 32 || !value.monitorBindings.every(binding => record(binding) && typeof binding.source_id === 'string' && typeof binding.required === 'boolean'))) return false
  if (value.traceContext !== undefined && (!record(value.traceContext) || typeof value.traceContext.enabled !== 'boolean'
    || (value.traceContext.transaction_marker !== undefined && typeof value.traceContext.transaction_marker !== 'boolean')
    || (value.traceContext.origins !== undefined && (!Array.isArray(value.traceContext.origins) || value.traceContext.origins.length > 64 || !value.traceContext.origins.every(origin => typeof origin === 'string'))))) return false
  if (value.monitorRules !== undefined && (!Array.isArray(value.monitorRules) || value.monitorRules.length > 32 || !value.monitorRules.every(rule => record(rule) && typeof rule.source_id === 'string' && typeof rule.metric === 'string' && typeof rule.threshold === 'number'))) return false
  if (typeof value.showNewRelease !== 'boolean') return false
  if (!['launch', 'schedules'].includes(String(value.pageMode))) return false
  if (value.editingScheduleId !== null && typeof value.editingScheduleId !== 'string') return false
  if (!record(value.runDefaultOverrides) || !record(value.groupRunOverrides) || !record(value.scheduleDraft)) return false
  const defaults = value.runDefaultOverrides
  if (!record(defaults.runtime_settings) || (defaults.duration !== undefined && typeof defaults.duration !== 'string')) return false
  if (!['script_shakeout', 'resource_baseline'].every(field => defaults[field] === undefined || typeof defaults[field] === 'boolean')) return false
  if (!Object.values(value.groupRunOverrides).every(group => record(group)
    && (group.vusers === undefined || (typeof group.vusers === 'number' && Number.isSafeInteger(group.vusers)))
    && ['ramp_up', 'ramp_down'].every(field => group[field] === undefined || typeof group[field] === 'string')
    && (group.runtime_settings === undefined || record(group.runtime_settings))
    && (group.assignment === undefined || record(group.assignment)))) return false
  const schedule = value.scheduleDraft
  if (!['name', 'cron_expr', 'timezone', 'release_id', 'timeslot_id', 'run_name_template', 'approval_policy', 'missed_run_policy', 'disabled_from', 'disabled_until'].every(field => typeof schedule[field] === 'string')) return false
  if (schedule.revision !== undefined && (!Number.isInteger(schedule.revision) || Number(schedule.revision) < 1)) return false
  if (typeof schedule.enabled !== 'boolean') return false
  return ['blackout_windows', 'maintenance_windows', 'availability_windows'].every(field => {
    const windows = schedule[field]
    return Array.isArray(windows) && windows.every(window => record(window)
      && ['name', 'start_time', 'end_time'].every(prop => typeof window[prop] === 'string')
      && Array.isArray(window.days_of_week) && window.days_of_week.every(day => Number.isInteger(day) && day >= 0 && day <= 6))
  })
}

export function saveNewTestDraft(draft: NewTestDraft): void {
  // Explicit draft saving is local to this tab; it never uses a test/schedule API.
  window.sessionStorage.setItem(key(draft.projectId, draft.applicationId), JSON.stringify(draft))
}
export function readNewTestDraft(project: string, application: string): NewTestDraft | null {
  if (!project || !application) return null
  try {
    const raw = window.sessionStorage.getItem(key(project, application))
    if (!raw || raw.length > 1_000_000) return null
    const draft: unknown = JSON.parse(raw)
    return validDraft(draft) && draft.projectId === project && draft.applicationId === application ? draft : null
  } catch { return null }
}
export function clearNewTestDraft(project: string, application: string): void {
  try { window.sessionStorage.removeItem(key(project, application)) } catch { /* Execution must not fail because local storage is unavailable. */ }
}
