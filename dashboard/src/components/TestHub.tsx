import { Fragment, memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Area, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { DashboardAuthEffectiveAccessResource, DraftCapacityEstimate, GoalConfig, GoalSizingReport, LoadModelSupportEntry, LoadModelTemplate, LoadModelValidationReport, LoadStageDraft, ScenarioDataPoolBinding, ScenarioMode, SharedDataPool, StoredScenario, StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { formatTimeCompact, isValidDurationInput, parseDurationToSeconds } from '../utils/timeUtils'
import './TestHub.css'
import './CreateTestModal.css'
import { buildCreateTestPayload, buildScenarioUpsertPayload } from './scenarioPayloadUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'
import { buildScenarioModelPreview, buildScenarioShapeCatalog, formatModelTimeLabel, ScenarioModelPreview, ScenarioSourceMode, ScenarioWizardStep, validateStageTimeline } from './scenarioWizardUtils'
import { BaselineGroupOption, baselineGroupOptions, formatGoalSizingSummary, goalSizedGoalFields, goalSizedGroups, littlesLawVUsers } from './goalSizingUtils'
import DurationField from './DurationField'
import PageViewToggle from './PageViewToggle'
import { type NavPage } from './Sidebar'
import LibraryFederationControls from './LibraryFederationControls'
import { useLibraryFederation } from './useLibraryFederation'
import { shortLibraryHash } from './libraryFederationUtils'
import ReferenceKeyBadge from './ReferenceKeyBadge'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
  onOpenCommandCenter?: (id: string) => void
  onCreateTestFromScenario?: (scenarioId: string) => void
  onNavigate?: (page: NavPage) => void
  onScenarioViewChange?: (view: 'library' | 'builder') => void
  initialView?: 'library' | 'builder'
}

interface ScenarioFormState {
  name: string
  duration: string
  groups: ScenarioFormGroup[]
  scenario_mode: ScenarioMode
  script_shakeout: boolean
  resource_baseline: boolean
  goal_target_average_tps?: number
  goal_min_vusers?: number
  goal_max_vusers?: number
  goal_p95_latency_ms?: number
  goal_max_error_pct?: number
  continue_on_error: boolean
  snapshot_on_error: boolean
  browser_device_profile?: string
  browser_orientation?: string
  browser_touch_mode?: '' | 'true' | 'false'
  browser_headless?: '' | 'true' | 'false'
  assignment_mode: 'auto' | 'specific_hosts'
  assignment_targets: string[]
  data_pool_id?: string
  data_pool_alias?: string
  data_pool_access_mode?: ScenarioDataPoolBinding['access_mode']
  data_pool_selection_strategy?: ScenarioDataPoolBinding['selection_strategy']
}

interface ScenarioFormGroup {
  name: string
  script_id: string
  role?: 'load' | 'observer'
  vusers: number
  ramp_up: string
  ramp_down: string
  stages: LoadStageDraft[]
  initialization: 'at_runtime' | 'at_once'
}

type ScenarioSortField = 'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_test_id' | 'last_run_at' | 'last_run_ended_at' | 'result_count'
type ScriptLibrarySortField = 'name' | 'created_at' | 'last_run_at' | 'associated_scenarios' | 'associated_releases' | 'protocol'
type ScenarioStartChoice = ScenarioSourceMode | 'goal' | ''

const scenarioWizardSteps: Array<{ id: ScenarioWizardStep, label: string }> = [
  { id: 'start', label: 'Start' },
  { id: 'source', label: 'Shape / Source' },
  { id: 'scripts', label: 'Scripts' },
  { id: 'run', label: 'Run Settings' },
  { id: 'data', label: 'Data' },
  { id: 'review', label: 'Review' },
]


function emptyScenarioGroup(scriptId = '', index = 0): ScenarioFormGroup {
  return {
    name: index === 0 ? '' : `Group ${index + 1}`,
    script_id: scriptId,
    vusers: 10,
    ramp_up: '',
    ramp_down: '',
    stages: [],
    initialization: 'at_runtime',
  }
}

function emptyScenarioForm(scriptId = ''): ScenarioFormState {
  return {
    name: '',
    duration: '1m',
    groups: [emptyScenarioGroup(scriptId)],
    scenario_mode: 'vuser_driven',
    script_shakeout: false,
    resource_baseline: false,
    continue_on_error: false,
    snapshot_on_error: false,
    browser_device_profile: '',
    browser_orientation: '',
    browser_touch_mode: '',
    browser_headless: '',
    assignment_mode: 'auto',
    assignment_targets: [],
    data_pool_id: '',
    data_pool_alias: '',
    data_pool_access_mode: 'read_only',
    data_pool_selection_strategy: 'sequential',
  }
}

type LoadStageType = 'ramp_up' | 'hold' | 'ramp_down'

function goalConfigFromForm(form: ScenarioFormState): GoalConfig {
  return {
    ...(form.goal_target_average_tps ? { target_average_tps: Number(form.goal_target_average_tps) } : {}),
    ...(form.goal_min_vusers ? { min_vusers: Number(form.goal_min_vusers) } : {}),
    ...(form.goal_max_vusers ? { max_vusers: Number(form.goal_max_vusers) } : {}),
    ...(form.goal_p95_latency_ms ? { p95_latency_ms: Number(form.goal_p95_latency_ms) } : {}),
    ...(form.goal_max_error_pct ? { max_error_pct: Number(form.goal_max_error_pct) } : {}),
  }
}

function maxStageVUsers(stages: LoadStageDraft[]) {
  return stages.reduce((max, stage) => Math.max(max, Number(stage.target_vusers) || 0), 0)
}

function normalizeStageForPayload(stage: LoadStageDraft) {
  return {
    ...(stage.name ? { name: stage.name } : {}),
    type: stage.type,
    duration: stage.duration,
    ...(stage.target_vusers !== undefined ? { target_vusers: Number(stage.target_vusers) || 0 } : {}),
    ...(stage.target_tps !== undefined ? { target_tps: Number(stage.target_tps) || 0 } : {}),
  }
}

function normalizeGroupForPayload(group: ScenarioFormGroup) {
  return {
    name: group.name,
    scriptId: group.script_id,
    role: group.role,
    vusers: maxStageVUsers(group.stages) || Number(group.vusers) || 10,
    rampUp: group.ramp_up,
    rampDown: group.ramp_down,
    stages: group.stages.map(normalizeStageForPayload),
    initialization: group.initialization,
  }
}

function scenarioFormGroupFromStored(group: StoredScenario['scripts'][number], fallbackScriptId: string): ScenarioFormGroup {
  return {
    name: group.name || '',
    script_id: group.script_id || fallbackScriptId,
    role: group.role,
    vusers: group.vusers || 10,
    ramp_up: group.ramp_up || '',
    ramp_down: group.ramp_down || '',
    stages: group.stages ? [...group.stages] : [],
    initialization: group.initialization || 'at_runtime',
  }
}

function ensureScenarioGroups(groups: ScenarioFormGroup[]): ScenarioFormGroup[] {
  const nextGroups = groups.length > 0 ? groups : [emptyScenarioGroup()]
  return nextGroups.map((group, index) => ({
    ...group,
    name: group.name || (index === 0 ? '' : `Group ${index + 1}`),
    script_id: group.script_id || '',
    vusers: Number(group.vusers) || 10,
    ramp_up: group.ramp_up || '',
    ramp_down: group.ramp_down || '',
    stages: group.stages || [],
    initialization: group.initialization || 'at_runtime',
  }))
}

function findScriptById(scripts: StoredScript[], scriptId?: string) {
  if (!scriptId) return undefined
  return scripts.find(script => script.id === scriptId)
}

function createDefaultStages(vusers: number, rampUp = '2m', totalDuration = '5m', rampDown = '1m'): LoadStageDraft[] {
  const target = Number(vusers) || 10
  const totalSeconds = parseDurationToSeconds(totalDuration) || 300
  let rampUpSeconds = parseDurationToSeconds(rampUp) || Math.max(1, Math.round(totalSeconds * 0.3))
  let rampDownSeconds = parseDurationToSeconds(rampDown) || Math.max(1, Math.round(totalSeconds * 0.15))
  if (rampUpSeconds + rampDownSeconds >= totalSeconds) {
    rampUpSeconds = Math.max(1, Math.round(totalSeconds * 0.4))
    rampDownSeconds = Math.max(1, Math.round(totalSeconds * 0.2))
  }
  const holdSeconds = Math.max(1, totalSeconds - rampUpSeconds - rampDownSeconds)
  return [
    { name: 'Warmup', type: 'ramp_up', duration: formatTimeCompact(rampUpSeconds), target_vusers: target },
    { name: 'Steady', type: 'hold', duration: formatTimeCompact(holdSeconds), target_vusers: target },
    { name: 'Cooldown', type: 'ramp_down', duration: formatTimeCompact(rampDownSeconds), target_vusers: 1 },
  ]
}

function simpleRampFromStages(group: ScenarioFormGroup): Pick<ScenarioFormGroup, 'ramp_up' | 'ramp_down' | 'vusers'> {
  const firstRampUp = group.stages.find(stage => stage.type === 'ramp_up')?.duration || group.ramp_up
  const lastRampDown = [...group.stages].reverse().find(stage => stage.type === 'ramp_down')?.duration || group.ramp_down
  return {
    ramp_up: firstRampUp,
    ramp_down: lastRampDown,
    vusers: maxStageVUsers(group.stages) || group.vusers,
  }
}

function getLoadModelStatusClass(status?: string) {
  if (status === 'ready' || status === 'executed') return 'ready'
  if (status === 'blocked' || status === 'planned') return 'blocked'
  return 'needs-review'
}

function formatLoadModelDimension(value: string) {
  return value.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
}

function WorkloadShapeVisual({ entry }: { entry: LoadModelSupportEntry }) {
  const title = `${entry.name} workload shape`

  if (entry.id === 'open-arrival-rate') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 46L12 37L24 37L24 46L36 46L36 34L48 34L48 46L60 46L60 31L72 31L72 46L84 46L84 28L96 28L96 46Z" />
        <path className="scenario-shape-line" d="M12 40L108 28" />
        {[18, 34, 50, 66, 82, 98].map(x => <circle key={x} className="scenario-shape-marker" cx={x} cy={34 - (x - 18) * 0.12} r="2.8" />)}
      </svg>
    )
  }

  if (entry.id === 'stepped-ramp') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50H30V40H48V30H66V20H84V38H108V50Z" />
        <path className="scenario-shape-line" d="M12 50H30V40H48V30H66V20H84V38H108" />
      </svg>
    )
  }

  if (entry.id === 'spike-rendezvous') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-event" d="M60 14V51" />
        <path className="scenario-shape-fill" d="M12 50H45L60 16L75 50H108V52H12Z" />
        <path className="scenario-shape-line" d="M12 50H45L60 16L75 50H108" />
      </svg>
    )
  }

  if (entry.id === 'soak') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50L28 30H94L108 50V52H12Z" />
        <path className="scenario-shape-line" d="M12 50L28 30H94L108 50" />
      </svg>
    )
  }

  if (entry.id === 'stress') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 52L28 46L43 39L58 31L72 23L86 18L100 15L108 18V52Z" />
        <path className="scenario-shape-line" d="M12 52L28 46L43 39L58 31L72 23L86 18L100 15L108 18" />
      </svg>
    )
  }

  if (entry.id === 'goal-based') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-event" d="M12 24H108M12 43H108" />
        <path className="scenario-shape-line" d="M12 42C24 20 34 21 45 34S68 48 78 31S96 19 108 27" />
      </svg>
    )
  }

  if (entry.id === 'chaos-resilience') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50L26 34H48L56 44L66 24L78 39L88 30L108 46V52H12Z" />
        <path className="scenario-shape-event" d="M55 15V51M78 15V51" />
        <path className="scenario-shape-line" d="M12 50L26 34H48L56 44L66 24L78 39L88 30L108 46" />
        <path className="scenario-shape-line-secondary" d="M16 40L34 40L42 30L50 45L62 31L72 45L84 33L104 33" />
      </svg>
    )
  }

  if (entry.id === 'mixed-protocol') {
    return (
      <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
        <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
        <path className="scenario-shape-fill" d="M12 50L24 38H96L108 50V52H12Z" />
        <path className="scenario-shape-line" d="M12 50L24 38H96L108 50" />
        <path className="scenario-shape-line-secondary" d="M12 52L32 44H86L108 52" />
      </svg>
    )
  }

  return (
    <svg className="scenario-shape-visual" viewBox="0 0 120 62" role="img" aria-label={title}>
      <path className="scenario-shape-grid" d="M10 48H110M10 34H110M10 20H110" />
      <path className="scenario-shape-fill" d="M12 50L30 28H92L108 46V52H12Z" />
      <path className="scenario-shape-line" d="M12 50L30 28H92L108 46" />
    </svg>
  )
}

function browserRuntimeFromForm(form: ScenarioFormState) {
  const browser = {
    ...(form.browser_device_profile ? { device_profile: form.browser_device_profile } : {}),
    ...(form.browser_orientation ? { orientation: form.browser_orientation } : {}),
    ...(form.browser_touch_mode ? { touch_mode: form.browser_touch_mode === 'true' } : {}),
    ...(form.browser_headless ? { headless: form.browser_headless === 'true' } : {}),
  }
  return Object.keys(browser).length > 0 ? browser : undefined
}

function booleanSelectValue(value?: boolean): '' | 'true' | 'false' {
  if (value === undefined) return ''
  return value ? 'true' : 'false'
}

function scenarioFormStateFromStoredScenario(
  scenario: StoredScenario,
  fallbackScriptId: string,
  cloneDraft = false,
): ScenarioFormState {
  const groups = [...(scenario.scripts || [])]
    .sort((left, right) => (left.order ?? 0) - (right.order ?? 0))
    .map(group => scenarioFormGroupFromStored(group, fallbackScriptId))
  const firstPool = scenario.data_pools?.[0]

  return {
    name: cloneDraft ? `${scenario.name} (Copy)` : scenario.name,
    duration: scenario.duration,
    groups: ensureScenarioGroups(groups),
    scenario_mode: scenario.scenario_mode || 'vuser_driven',
    script_shakeout: Boolean(scenario.script_shakeout),
    resource_baseline: Boolean(scenario.resource_baseline),
    goal_target_average_tps: scenario.goal_config?.target_average_tps,
    goal_min_vusers: scenario.goal_config?.min_vusers,
    goal_max_vusers: scenario.goal_config?.max_vusers,
    goal_p95_latency_ms: scenario.goal_config?.p95_latency_ms,
    goal_max_error_pct: scenario.goal_config?.max_error_pct,
    continue_on_error: Boolean(scenario.runtime_settings?.error_handling?.continue_on_error),
    snapshot_on_error: Boolean(scenario.runtime_settings?.error_handling?.snapshot_on_error),
    browser_device_profile: scenario.runtime_settings?.browser?.device_profile || '',
    browser_orientation: scenario.runtime_settings?.browser?.orientation || '',
    browser_touch_mode: booleanSelectValue(scenario.runtime_settings?.browser?.touch_mode),
    browser_headless: booleanSelectValue(scenario.runtime_settings?.browser?.headless),
    assignment_mode: scenario.assignment?.mode === 'specific_hosts' ? 'specific_hosts' : 'auto',
    assignment_targets: scenario.assignment?.selected_targets ? [...scenario.assignment.selected_targets] : [],
    data_pool_id: firstPool?.pool_id || '',
    data_pool_alias: firstPool?.alias || '',
    data_pool_access_mode: firstPool?.access_mode || 'read_only',
    data_pool_selection_strategy: firstPool?.selection_strategy || 'sequential',
  }
}

function getScenarioSortValue(scenario: StoredScenario, field: ScenarioSortField): string | number {
  switch (field) {
    case 'scripts':
      return (scenario.scripts || []).length
    case 'data_pools':
      return (scenario.data_pools || []).length
    case 'created_at':
      return new Date(scenario.created_at || 0).getTime()
    case 'last_run_test_id':
      return (scenario.last_run_test_id || '').toLowerCase()
    case 'last_run_at':
      return new Date(scenario.last_run_at || 0).getTime()
    case 'last_run_ended_at':
      return new Date(scenario.last_run_ended_at || 0).getTime()
    case 'associated_releases':
      return (scenario.associated_releases || []).length
    case 'result_count':
      return scenario.result_count || 0
    case 'name':
    default:
      return (scenario.name || '').toLowerCase()
  }
}

function getRunSortValue(run: TestSummary, field: 'name' | 'status' | 'release_id' | 'start_time' | 'duration'): string | number {
  if (field === 'start_time') {
    return new Date(run.start_time || run.created_at || 0).getTime()
  }
  return String(run[field] || '').toLowerCase()
}

function formatDate(value?: string) {
  return value ? new Date(value).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' }) : 'Never'
}

function formatDateTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '—'
}

function releaseList(scenario: StoredScenario) {
  const releases = scenario.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return Array.from(new Set(releases.map(release => release.name))).join(', ')
}

function formatScenarioScripts(scenario: StoredScenario) {
  const scripts = scenario.scripts || []
  if (scripts.length === 0) return 'No scripts assigned'
  return scripts.map(script => script.script_name || script.name || script.script_id).join(', ')
}

function formatScenarioDataPools(scenario: StoredScenario) {
  const pools = scenario.data_pools || []
  if (pools.length === 0) return 'No data pools'
  return pools.map(pool => `${pool.alias} (${pool.pool_name || pool.pool_id})`).join(', ')
}

function formatScenarioResultCount(scenario: StoredScenario) {
  const count = scenario.result_count || 0
  return `${count} result${count === 1 ? '' : 's'}`
}

function formatScenarioRunTestID(scenario: StoredScenario) {
  return scenario.last_run_test_id || '—'
}

function scenarioSearchText(scenario: StoredScenario) {
  return [
    scenario.name,
    scenario.duration,
    scenario.last_run_test_id,
    scenario.assignment?.mode,
    ...(scenario.assignment?.selected_targets || []),
    ...(scenario.scripts || []).map(script => script.script_name || script.name || script.script_id),
    ...(scenario.data_pools || []).map(pool => `${pool.alias} ${pool.pool_name || pool.pool_id}`),
    ...(scenario.associated_releases || []).map(release => release.name),
    formatBrowserRuntimeSummary(scenario.runtime_settings?.browser),
  ].filter(Boolean).join(' ').toLowerCase()
}

function uniqueValues(values: Array<string | undefined>) {
  return Array.from(new Set(values.map(value => value?.trim()).filter(Boolean) as string[]))
}

function scriptProtocols(script: StoredScript) {
  return uniqueValues([script.protocol || 'http'])
}

function associatedScenarioList(script: StoredScript) {
  const scenarios = script.associated_scenarios || []
  if (scenarios.length === 0) return 'No scenarios'
  return uniqueValues(scenarios.map(scenario => scenario.name || scenario.id)).join(', ')
}

function scriptLibrarySearchText(script: StoredScript) {
  return [
    script.id,
    script.name,
    ...scriptProtocols(script),
    script.version,
    script.created_at,
    script.last_run_at,
    formatDate(script.created_at),
    formatDate(script.last_run_at),
    formatDateTime(script.last_run_at),
    script.authoring_health?.status,
    ...(script.associated_scenarios || []).map(scenario => `${scenario.id} ${scenario.name}`),
    ...(script.associated_releases || []).map(release => release.name),
    ...(script.associated_releases || []).map(release => release.id),
    ...(script.scenario_run_releases || []).map(release => release.name),
  ].filter(Boolean).join(' ').toLowerCase()
}

function scriptReleaseList(script: StoredScript) {
  const releases = script.associated_releases || []
  if (releases.length === 0) return 'No releases yet'
  return uniqueValues(releases.map(release => release.name || release.id)).join(', ')
}

function formatScriptScenarioCount(script: StoredScript) {
  const count = script.scenario_count || 0
  return `${count} scenario${count === 1 ? '' : 's'}`
}

function getScriptLibrarySortValue(script: StoredScript, field: ScriptLibrarySortField): string | number {
  switch (field) {
    case 'created_at':
      return new Date(script.created_at || 0).getTime()
    case 'last_run_at':
      return new Date(script.last_run_at || 0).getTime()
    case 'associated_scenarios':
      return associatedScenarioList(script).toLowerCase()
    case 'associated_releases':
      return scriptReleaseList(script).toLowerCase()
    case 'protocol':
      return scriptProtocols(script).join(', ').toLowerCase()
    case 'name':
    default:
      return `${script.name || ''} ${script.id || ''}`.toLowerCase()
  }
}

function scenarioAssignmentSummary(scenario: StoredScenario) {
  if (scenario.assignment?.mode === 'specific_hosts') {
    const targets = scenario.assignment.selected_targets || []
    if (targets.length === 0) return { badge: 'Specific Hosts', detail: 'No hosts selected' }
    return {
      badge: 'Specific Hosts',
      detail: targets.join(', '),
    }
  }
  return null
}

function SortIndicator({ active, direction }: { active: boolean, direction: 'asc' | 'desc' }) {
  return (
    <span className={`sort-icon ${active ? 'active' : ''}`}>
      {active ? (direction === 'asc' ? '↑' : '↓') : '↕'}
    </span>
  )
}

function ScenarioModelTooltip({ active, payload, label }: {
  active?: boolean
  payload?: Array<{ color?: string, name?: string, value?: number | string }>
  label?: string | number
}) {
  if (!active || !payload || payload.length === 0) return null
  return (
    <div className="scenario-model-tooltip">
      <strong>{formatModelTimeLabel(Number(label) || 0)}</strong>
      {payload
        .filter(item => item.value !== undefined && item.value !== null)
        .map(item => (
          <span key={`${item.name}-${item.color}`}>
            <i style={{ background: item.color || 'var(--accent)' }} />
            {item.name}: {typeof item.value === 'number' ? item.value.toFixed(item.value % 1 === 0 ? 0 : 1) : item.value}
          </span>
        ))}
    </div>
  )
}

function ScenarioModelPreviewPanel({
  model,
  shapeLabel,
  dataPoolLabel,
  validationStatus,
}: {
  model: ScenarioModelPreview
  shapeLabel: string
  dataPoolLabel: string
  validationStatus: string
}) {
  const groupSeries = model.series.filter(series => series.kind === 'group')
  const totalSeries = model.series.find(series => series.kind === 'total')
  const tpsSeries = model.series.find(series => series.kind === 'tps')

  return (
    <section className="scenario-model-preview-panel">
      <div className="scenario-model-preview-header">
        <div>
          <h4>Model Preview</h4>
          <p>Planned VUsers over time by script group, calculated from the current wizard draft.</p>
        </div>
        <div className="scenario-model-summary">
          <span><strong>{model.peakVusers}</strong> peak VUsers</span>
          <span><strong>{formatModelTimeLabel(model.durationSeconds)}</strong> duration</span>
          <span><strong>{groupSeries.length}</strong> groups</span>
          <span>{shapeLabel}</span>
          <span>{dataPoolLabel}</span>
          <span>{validationStatus}</span>
        </div>
      </div>
      <div className="scenario-model-chart">
        <ResponsiveContainer width="100%" height={340}>
          <ComposedChart data={model.points} margin={{ top: 16, right: tpsSeries ? 92 : 24, bottom: 18, left: 24 }}>
            <CartesianGrid stroke="var(--border-subtle, var(--border))" strokeDasharray="3 3" vertical={false} />
            <XAxis
              dataKey="elapsedSeconds"
              tickFormatter={(value) => formatModelTimeLabel(Number(value))}
              stroke="var(--text-muted)"
              fontSize={11}
              height={42}
              tickLine={false}
              axisLine={false}
              label={{ value: 'Elapsed time', position: 'insideBottom', offset: -4, fill: 'var(--text-muted)', fontSize: 11 }}
            />
            <YAxis
              yAxisId="vusers"
              stroke="var(--text-muted)"
              fontSize={11}
              width={80}
              tickLine={false}
              axisLine={false}
              label={{ value: 'VUsers', angle: -90, position: 'left', fill: 'var(--text-muted)', fontSize: 11 }}
            />
            {tpsSeries && (
              <YAxis
                yAxisId="tps"
                orientation="right"
                stroke="var(--warning)"
                fontSize={11}
                width={68}
                tickLine={false}
                axisLine={false}
                label={{ value: 'TPS', angle: 90, position: 'right', fill: 'var(--warning)', fontSize: 11 }}
              />
            )}
            <Tooltip content={<ScenarioModelTooltip />} />
            {groupSeries.map(series => (
              <Area
                key={series.key}
                yAxisId="vusers"
                type="linear"
                dataKey={series.key}
                name={series.label}
                stackId="vusers"
                stroke={series.color}
                fill={series.color}
                fillOpacity={0.16}
                strokeWidth={2}
                dot={false}
                isAnimationActive={false}
              />
            ))}
            {totalSeries && (
              <Line
                yAxisId="vusers"
                type="linear"
                dataKey={totalSeries.key}
                name={totalSeries.label}
                stroke={totalSeries.color}
                strokeWidth={3}
                dot={false}
                isAnimationActive={false}
              />
            )}
            {tpsSeries && (
              <Line
                yAxisId="tps"
                type="linear"
                dataKey={tpsSeries.key}
                name={tpsSeries.label}
                stroke={tpsSeries.color}
                strokeWidth={2}
                strokeDasharray="6 5"
                dot={false}
                isAnimationActive={false}
              />
            )}
          </ComposedChart>
        </ResponsiveContainer>
      </div>
      <div className="scenario-model-legend">
        {model.series.map(series => (
          <span key={series.key}>
            <i style={{ background: series.color }} />
            {series.label}
          </span>
        ))}
      </div>
    </section>
  )
}

function TestHub({ onSelectTest, onOpenCommandCenter, onCreateTestFromScenario, onNavigate, onScenarioViewChange, initialView = 'library' }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [scenarios, setScenarios] = useState<StoredScenario[]>([])
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [dataPools, setDataPools] = useState<SharedDataPool[]>([])
  const [loadModelTemplates, setLoadModelTemplates] = useState<LoadModelTemplate[]>([])
  const [loadModelSupportMatrix, setLoadModelSupportMatrix] = useState<LoadModelSupportEntry[]>([])
  const [selectedLoadModelTemplateId, setSelectedLoadModelTemplateId] = useState('')
  const [loadModelValidation, setLoadModelValidation] = useState<LoadModelValidationReport | null>(null)
  const [loadModelValidationLoading, setLoadModelValidationLoading] = useState(false)
  const [loadModelValidationError, setLoadModelValidationError] = useState('')
  const [showForm, setShowForm] = useState(initialView === 'builder')
  const [editingId, setEditingId] = useState<string | null>(null)
  const [wizardStep, setWizardStep] = useState<ScenarioWizardStep>('start')
  const [maxVisitedWizardIndex, setMaxVisitedWizardIndex] = useState(0)
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const [scenarioSourceMode, setScenarioSourceMode] = useState<ScenarioSourceMode>('shape')
  const [scenarioStartChoice, setScenarioStartChoice] = useState<ScenarioStartChoice>('')
  const [scenarioCloneQuery, setScenarioCloneQuery] = useState('')
  const [selectedCloneScenarioId, setSelectedCloneScenarioId] = useState('')
  const [selectedShapeId, setSelectedShapeId] = useState('')
  const [form, setForm] = useState<ScenarioFormState>(emptyScenarioForm())
  const [error, setError] = useState('')
  const [scenarioQuery, setScenarioQuery] = useState('')
  const [scenarioReleaseFilter, setScenarioReleaseFilter] = useState('all')
  const [scriptLibraryQuery, setScriptLibraryQuery] = useState('')
  const [scriptLibrarySortField, setScriptLibrarySortField] = useState<ScriptLibrarySortField>('name')
  const [scriptLibrarySortDirection, setScriptLibrarySortDirection] = useState<'asc' | 'desc'>('asc')
  const [scriptSelectionTargetIndex, setScriptSelectionTargetIndex] = useState<number | null>(null)
  const [sortField, setSortField] = useState<ScenarioSortField>('last_run_at')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [expandedScenarioId, setExpandedScenarioId] = useState<string | null>(null)
  const [scenarioRuns, setScenarioRuns] = useState<Record<string, TestSummary[]>>({})
  const [loadingRunsFor, setLoadingRunsFor] = useState<string | null>(null)
  const [innerSortField, setInnerSortField] = useState<'name' | 'status' | 'release_id' | 'start_time' | 'duration'>('start_time')
  const [innerSortDirection, setInnerSortDirection] = useState<'asc' | 'desc'>('desc')
  const [draftEstimate, setDraftEstimate] = useState<DraftCapacityEstimate | null>(null)
  const [estimateLoading, setEstimateLoading] = useState(false)
  const [estimateError, setEstimateError] = useState('')
  const [confirmDeleteScenarioId, setConfirmDeleteScenarioId] = useState<string | null>(null)
  // Build-from-goal (Little's Law) sizing panel state.
  const [goalSizingOpen, setGoalSizingOpen] = useState(false)
  const [goalBaselineTests, setGoalBaselineTests] = useState<TestSummary[]>([])
  const [goalBaselineId, setGoalBaselineId] = useState('')
  const [goalBaselineGroups, setGoalBaselineGroups] = useState<BaselineGroupOption[]>([])
  const [goalTargetTps, setGoalTargetTps] = useState<Record<string, string>>({})
  const [goalSizingResult, setGoalSizingResult] = useState<GoalSizingReport | null>(null)
  const [goalSizingLoading, setGoalSizingLoading] = useState(false)
  const [goalSizingError, setGoalSizingError] = useState('')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [stageGroupIndex, setStageGroupIndex] = useState(0)
  const scriptLibraryRef = useRef<HTMLDivElement | null>(null)
  const scriptLibrarySearchRef = useRef<HTMLInputElement | null>(null)
  const scriptGroupRefs = useRef<Array<HTMLDivElement | null>>([])
  const formGroups = useMemo(
    () => form.groups.length > 0 ? form.groups : [emptyScenarioGroup()],
    [form.groups],
  )
  const activeStageGroup = formGroups[Math.min(stageGroupIndex, formGroups.length - 1)] || formGroups[0]
  const activeStageTimeline = useMemo(
    () => validateStageTimeline(form.duration, activeStageGroup?.stages || []),
    [activeStageGroup?.stages, form.duration],
  )
  const blockingStageTimelineMessage = useMemo(() => {
    for (let index = 0; index < formGroups.length; index += 1) {
      const group = formGroups[index]
      if (!group.stages.length) continue
      const validation = validateStageTimeline(form.duration, group.stages)
      if (validation.status === 'error') {
        return `${group.name || `Group ${index + 1}`}: ${validation.message}`
      }
    }
    return ''
  }, [form.duration, formGroups])
  const showBrowserRuntimeSettings = formGroups.some(group => findScriptById(scripts, group.script_id)?.protocol === 'browser')
  const canManageTests = useMemo(
    () => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_tests'),
    [effectiveAuthAccessResources],
  )
  const shapeCatalog = useMemo(
    () => buildScenarioShapeCatalog(loadModelSupportMatrix, loadModelTemplates),
    [loadModelSupportMatrix, loadModelTemplates],
  )
  const selectedShape = shapeCatalog.find(option => option.id === selectedShapeId)
  const selectedTemplate = loadModelTemplates.find(template => template.id === selectedLoadModelTemplateId)
  const selectedCloneScenario = useMemo(
    () => scenarios.find(scenario => scenario.id === selectedCloneScenarioId) || null,
    [scenarios, selectedCloneScenarioId],
  )
  const scenarioModel = useMemo(
    () => buildScenarioModelPreview({
      duration: form.duration,
      groups: formGroups.map(group => ({
        name: group.name || findScriptById(scripts, group.script_id)?.name || undefined,
        vusers: Number(group.vusers) || 0,
        ramp_up: group.ramp_up,
        ramp_down: group.ramp_down,
        stages: group.stages,
      })),
      goalTargetTps: form.goal_target_average_tps,
    }),
    [form.duration, form.goal_target_average_tps, formGroups, scripts],
  )
  const filteredCloneScenarios = useMemo(() => {
    const query = scenarioCloneQuery.trim().toLowerCase()
    if (!query) return scenarios
    return scenarios.filter(scenario => scenarioSearchText(scenario).includes(query))
  }, [scenarioCloneQuery, scenarios])
  const visibleScriptLibrary = useMemo(() => {
    const query = scriptLibraryQuery.trim().toLowerCase()
    const filtered = query
      ? scripts.filter(script => scriptLibrarySearchText(script).includes(query))
      : scripts
    return [...filtered].sort((left, right) => {
      const leftValue = getScriptLibrarySortValue(left, scriptLibrarySortField)
      const rightValue = getScriptLibrarySortValue(right, scriptLibrarySortField)
      const direction = scriptLibrarySortDirection === 'asc' ? 1 : -1
      if (typeof leftValue === 'number' && typeof rightValue === 'number') {
        return (leftValue - rightValue) * direction
      }
      return String(leftValue).localeCompare(String(rightValue), undefined, { numeric: true, sensitivity: 'base' }) * direction
    })
  }, [scriptLibraryQuery, scriptLibrarySortDirection, scriptLibrarySortField, scripts])
  const showScenarioLibrary = !showForm
  const showScriptLibrary = showForm && wizardStep === 'scripts'
  const showModelPreview = showForm && wizardStep !== 'start' && wizardStep !== 'scripts'

  const buildScenarioDraftPayload = useCallback((draftForm: ScenarioFormState = form) => {
    const dataPoolsPayload = draftForm.data_pool_id ? [{
      pool_id: draftForm.data_pool_id,
      alias: (draftForm.data_pool_alias || dataPools.find(pool => pool.id === draftForm.data_pool_id)?.name || 'data').trim().toLowerCase().replace(/\s+/g, '_'),
      access_mode: draftForm.data_pool_access_mode || 'read_only',
      selection_strategy: draftForm.data_pool_selection_strategy || 'sequential',
    }] : []

    return buildScenarioUpsertPayload({
      name: draftForm.name || 'Draft scenario',
      duration: draftForm.duration || '1m',
      startDelay: '',
      scenarioMode: draftForm.scenario_mode,
      scriptShakeout: draftForm.script_shakeout,
      resourceBaseline: draftForm.resource_baseline,
      assignment: draftForm.assignment_mode === 'specific_hosts'
        ? { mode: 'specific_hosts', selected_targets: draftForm.assignment_targets }
        : { mode: 'auto', selected_targets: [] },
      groups: draftForm.groups.map(normalizeGroupForPayload),
      goalConfig: goalConfigFromForm(draftForm),
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        browser: showBrowserRuntimeSettings ? browserRuntimeFromForm(draftForm) : undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: draftForm.continue_on_error,
        snapshot_on_error: draftForm.snapshot_on_error,
      },
      dataPools: dataPoolsPayload,
    })
  }, [dataPools, form, showBrowserRuntimeSettings])

  const refresh = useCallback(async () => {
    try {
      const [scenarioRes, scriptRes, dataPoolRes, templateRes, supportRes] = await Promise.all([
        api.listScenarios(activeProject?.id),
        api.listScripts(activeProject?.id),
        api.listDataPools(activeProject?.id),
        api.listLoadModelTemplates(),
        api.getLoadModelSupportMatrix(),
      ])
      const nextScenarios = scenarioRes.scenarios || []
      const nextScripts = scriptRes.scripts || []
      setScenarios(nextScenarios)
      setScripts(nextScripts)
      setDataPools(dataPoolRes.data_pools || [])
      setLoadModelTemplates(templateRes.templates || [])
      setLoadModelSupportMatrix(supportRes.support_matrix || [])
      setForm(prev => ({
        ...prev,
        groups: ensureScenarioGroups(prev.groups.map(group => ({
          ...group,
          script_id: group.script_id && nextScripts.some(script => script.id === group.script_id) ? group.script_id : '',
        }))),
      }))
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to load scenarios'))
    }
  }, [activeProject?.id, api])

  useEffect(() => { refresh() }, [refresh])
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
  useEffect(() => {
    if (!canManageTests) {
      setShowForm(false)
      setEditingId(null)
      setConfirmDeleteScenarioId(null)
    }
  }, [canManageTests])

  useEffect(() => {
    setStageGroupIndex(current => Math.min(current, Math.max(0, form.groups.length - 1)))
  }, [form.groups.length])

  useEffect(() => {
    scriptGroupRefs.current = scriptGroupRefs.current.slice(0, form.groups.length)
    setScriptSelectionTargetIndex(current => (
      current !== null && current >= form.groups.length ? null : current
    ))
  }, [form.groups.length])

  useEffect(() => {
    if (!showForm || form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setDraftEstimate(null)
      setEstimateLoading(false)
      setEstimateError('')
      return
    }

    let cancelled = false
    setEstimateLoading(true)
    setEstimateError('')

    const draftPayload = buildScenarioDraftPayload()
    api.estimateTest(buildCreateTestPayload({
      name: form.name,
      duration: draftPayload.duration,
      startDelay: '',
      scenarioMode: draftPayload.scenario_mode,
      scriptShakeout: false,
      resourceBaseline: false,
      assignment: draftPayload.assignment,
      groups: form.groups.map(normalizeGroupForPayload),
      goalConfig: goalConfigFromForm(form),
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        browser: showBrowserRuntimeSettings ? browserRuntimeFromForm(form) : undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
      },
    }))
      .then((estimate) => {
        if (cancelled) return
        setDraftEstimate(estimate)
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setDraftEstimate(null)
        setEstimateError(getErrorMessage(err, 'Unable to estimate assignment guidance'))
      })
      .finally(() => {
        if (cancelled) return
        setEstimateLoading(false)
      })

    return () => { cancelled = true }
  }, [api, buildScenarioDraftPayload, form, showBrowserRuntimeSettings, showForm])

  const scenarioReleaseOptions = useMemo(() => {
    const names = new Set<string>()
    scenarios.forEach(scenario => (scenario.associated_releases || []).forEach(release => release.name && names.add(release.name)))
    return Array.from(names).sort((a, b) => a.localeCompare(b))
  }, [scenarios])

  const openRemoteSettings = useCallback(() => {
    try { window.localStorage.setItem('apexload.settings.focusSection', 'integrations') } catch { /* storage unavailable */ }
    onNavigate?.('settings')
  }, [onNavigate])

  const sorted = useMemo(() => {
    const query = scenarioQuery.trim().toLowerCase()
    const filteredScenarios = scenarios.filter(scenario => {
      if (scenarioReleaseFilter !== 'all' && !(scenario.associated_releases || []).some(release => release.name === scenarioReleaseFilter)) return false
      if (!query) return true
      return scenarioSearchText(scenario).includes(query)
    })
    return [...filteredScenarios].sort((a, b) => {
      const aValue = getScenarioSortValue(a, sortField)
      const bValue = getScenarioSortValue(b, sortField)
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scenarios, scenarioQuery, scenarioReleaseFilter, sortField, sortDirection])

  const scenarioFederation = useLibraryFederation({ kind: 'scenario', projectId: activeProject?.id, onRefreshLocal: refresh })

  const knownScenarioIds = useMemo(() => new Set(scenarios.map(scenario => scenario.id)), [scenarios])

  const federationRemoteScenarioRows = useMemo(() => {
    if (scenarioFederation.sourceFilter === 'local') return []
    if (scenarioReleaseFilter !== 'all') return []
    const query = scenarioQuery.trim().toLowerCase()
    return scenarioFederation.remoteItems.filter(item => {
      if (scenarioFederation.sourceFilter === 'all' && item.local_item_id && knownScenarioIds.has(item.local_item_id)) return false
      if (!query) return true
      return [item.name, item.protocol, item.project_id].filter(Boolean).join(' ').toLowerCase().includes(query)
    })
  }, [scenarioFederation.remoteItems, scenarioFederation.sourceFilter, knownScenarioIds, scenarioQuery, scenarioReleaseFilter])

  const showLocalScenarios = scenarioFederation.sourceFilter !== 'host'

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else {
      setSortField(field)
      setSortDirection(field === 'last_run_at' || field === 'last_run_ended_at' || field === 'result_count' ? 'desc' : 'asc')
    }
  }

  const handleScriptLibrarySort = (field: ScriptLibrarySortField) => {
    if (scriptLibrarySortField === field) {
      setScriptLibrarySortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    } else {
      setScriptLibrarySortField(field)
      setScriptLibrarySortDirection(field === 'created_at' || field === 'last_run_at' ? 'desc' : 'asc')
    }
  }

  const handleInnerSort = (field: typeof innerSortField) => {
    if (innerSortField === field) setInnerSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else { setInnerSortField(field); setInnerSortDirection('asc') }
  }

  const getSortedInnerRuns = (runs: TestSummary[]) => {
    return [...runs].sort((a, b) => {
      const aValue = getRunSortValue(a, innerSortField)
      const bValue = getRunSortValue(b, innerSortField)
      if (aValue < bValue) return innerSortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return innerSortDirection === 'asc' ? 1 : -1
      return 0
    })
  }

  const loadScenarioRuns = async (scenario: StoredScenario) => {
    if (scenarioRuns[scenario.id]) return
    setLoadingRunsFor(scenario.id)
    try {
      const res = await api.listScenarioResults(scenario.id)
      const runs = (res.tests || []).sort((a: TestSummary, b: TestSummary) => {
        const aTime = new Date(a.start_time || a.created_at || 0).getTime()
        const bTime = new Date(b.start_time || b.created_at || 0).getTime()
        return bTime - aTime
      })
      setScenarioRuns(prev => ({ ...prev, [scenario.id]: runs }))
    } catch {
      setScenarioRuns(prev => ({ ...prev, [scenario.id]: [] }))
    } finally {
      setLoadingRunsFor(null)
    }
  }

  const toggleScenarioRuns = async (scenario: StoredScenario) => {
    if (expandedScenarioId === scenario.id) {
      setExpandedScenarioId(null)
      return
    }
    setExpandedScenarioId(scenario.id)
    await loadScenarioRuns(scenario)
  }

  const openCreate = useCallback(() => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    setEditingId(null)
    setForm(emptyScenarioForm())
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setScenarioCloneQuery('')
    setScriptSelectionTargetIndex(null)
    setWizardStep('start')
    setMaxVisitedWizardIndex(0)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(true)
    onScenarioViewChange?.('builder')
  }, [canManageTests, onScenarioViewChange])

  const openScenarioLibrary = useCallback(() => {
    setShowForm(false)
    setEditingId(null)
    setConfirmDeleteScenarioId(null)
    setScriptSelectionTargetIndex(null)
    setScenarioStartChoice('')
    setMaxVisitedWizardIndex(0)
    onScenarioViewChange?.('library')
  }, [onScenarioViewChange])

  const scenarioPageControls = useMemo(() => (
    <PageViewToggle
      value={showForm ? 'builder' : 'library'}
      ariaLabel="Scenario page view"
      className="scenario-page-toggle topbar-page-section-toggle"
      onChange={view => {
        if (view === 'library') openScenarioLibrary()
        else openCreate()
      }}
      options={[
        { value: 'library', label: 'Scenario Library' },
        { value: 'builder', label: 'Scenario Builder', disabled: !canManageTests },
      ]}
    />
  ), [canManageTests, openCreate, openScenarioLibrary, showForm])

  const scenarioTopbarContribution = useMemo(() => ({
    id: 'scenario-page',
    title: showForm ? 'Scenario Builder' : 'Scenario Library',
    controls: scenarioPageControls,
    priority: 10,
  }), [scenarioPageControls, showForm])

  useTopbarContribution(scenarioTopbarContribution)

  const openEdit = (scenario: StoredScenario) => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    const fallbackScriptId = scripts[0]?.id || ''
    setEditingId(scenario.id)
    setForm(scenarioFormStateFromStoredScenario(scenario, fallbackScriptId))
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('existing')
    setScenarioStartChoice('existing')
    setScenarioCloneQuery('')
    setScriptSelectionTargetIndex(null)
    setWizardStep('scripts')
    setMaxVisitedWizardIndex(2)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(true)
  }

  const duplicateScenario = (scenario: StoredScenario) => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    openEdit(scenario)
    setEditingId(null)
    setForm(prev => ({ ...prev, name: `${scenario.name} (Copy)` }))
  }

  const selectScenarioSourceMode = (mode: ScenarioSourceMode) => {
    setScenarioSourceMode(mode)
    setScenarioStartChoice(mode)
    if (mode === 'shape') setSelectedCloneScenarioId('')
    setWizardStep('source')
  }

  const cloneScenarioDraft = (scenario: StoredScenario) => {
    const fallbackScriptId = scripts[0]?.id || ''
    setEditingId(null)
    setForm(scenarioFormStateFromStoredScenario(scenario, fallbackScriptId, true))
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setSelectedCloneScenarioId(scenario.id)
    setScenarioSourceMode('existing')
    setScenarioStartChoice('existing')
    setScriptSelectionTargetIndex(null)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    void loadScenarioRuns(scenario)
  }

  const selectShapeOption = (shapeId: string) => {
    setSelectedShapeId(shapeId)
    setSelectedCloneScenarioId('')
    setSelectedLoadModelTemplateId('')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('shape')
    setScriptSelectionTargetIndex(null)
    setWizardStep('scripts')
  }

  const applyLoadModelTemplate = (templateId: string) => {
    setSelectedLoadModelTemplateId(templateId)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    const template = loadModelTemplates.find(item => item.id === templateId)
    if (!template) return
    setSelectedShapeId(template.shape_id)
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('shape')
    setScriptSelectionTargetIndex(null)
    setWizardStep('scripts')
    setForm(prev => ({
      ...prev,
      duration: template.scenario.duration || prev.duration,
      scenario_mode: template.scenario.scenario_mode || 'vuser_driven',
      script_shakeout: template.scenario.script_shakeout ?? prev.script_shakeout,
      resource_baseline: template.scenario.resource_baseline ?? prev.resource_baseline,
      groups: ensureScenarioGroups(
        (template.scenario.groups.length > 0 ? template.scenario.groups : [{ name: '', vusers: 10 }]).map((group, index) => ({
          name: group.name || prev.groups[index]?.name || (index === 0 ? '' : `Group ${index + 1}`),
          script_id: prev.groups[index]?.script_id || prev.groups[0]?.script_id || '',
          vusers: group.vusers || prev.groups[index]?.vusers || 10,
          ramp_up: group.ramp_up || '',
          ramp_down: group.ramp_down || '',
          stages: group.stages || [],
          initialization: prev.groups[index]?.initialization || 'at_runtime',
        })),
      ),
      goal_target_average_tps: template.scenario.goal_config?.target_average_tps,
      goal_min_vusers: template.scenario.goal_config?.min_vusers,
      goal_max_vusers: template.scenario.goal_config?.max_vusers,
      goal_p95_latency_ms: template.scenario.goal_config?.p95_latency_ms,
      goal_max_error_pct: template.scenario.goal_config?.max_error_pct,
      continue_on_error: template.scenario.runtime_settings?.error_handling?.continue_on_error ?? prev.continue_on_error,
      snapshot_on_error: template.scenario.runtime_settings?.error_handling?.snapshot_on_error ?? prev.snapshot_on_error,
    }))
  }

  const openGoalSizing = async () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    setGoalSizingOpen(true)
    setScenarioStartChoice('goal')
    setGoalBaselineId('')
    setGoalBaselineGroups([])
    setGoalTargetTps({})
    setGoalSizingResult(null)
    setGoalSizingError('')
    setWizardStep('start')
    setShowForm(true)
    setEditingId(null)
    try {
      const response = await api.listTests({ project_id: activeProject?.id })
      setGoalBaselineTests((response.tests || []).filter(test => test.status === 'COMPLETE'))
    } catch (err: unknown) {
      setGoalSizingError(getErrorMessage(err, 'Unable to load completed runs'))
    }
  }

  const selectGoalBaseline = async (testId: string) => {
    setGoalBaselineId(testId)
    setGoalBaselineGroups([])
    setGoalTargetTps({})
    setGoalSizingResult(null)
    setGoalSizingError('')
    if (!testId) return
    try {
      const report = await api.getTestArchiveReport(testId)
      const options = baselineGroupOptions(report)
      if (options.length === 0) {
        setGoalSizingError('This run has no response-time data to size from.')
        return
      }
      setGoalBaselineGroups(options)
      setGoalTargetTps(Object.fromEntries(options.map(option => [option.group, ''])))
    } catch (err: unknown) {
      setGoalSizingError(getErrorMessage(err, 'Unable to load the baseline run report'))
    }
  }

  const runGoalSizing = async () => {
    setGoalSizingError('')
    const groups = goalBaselineGroups
      .map(option => ({ option, tps: Number(goalTargetTps[option.group]) }))
      .filter(entry => Number.isFinite(entry.tps) && entry.tps > 0)
    if (groups.length === 0) {
      setGoalSizingError('Enter a target TPS greater than zero for at least one group.')
      return
    }
    setGoalSizingLoading(true)
    try {
      const result = await api.sizeScenarioFromGoal({
        baseline_test_id: goalBaselineId,
        groups: groups.map(({ option, tps }) => ({
          script_id: option.script_id,
          group: option.basis === 'group' ? option.group : undefined,
          target_tps: tps,
        })),
      })
      setGoalSizingResult(result)
    } catch (err: unknown) {
      setGoalSizingResult(null)
      setGoalSizingError(getErrorMessage(err, 'Unable to size scenario from goal'))
    } finally {
      setGoalSizingLoading(false)
    }
  }

  const applyGoalSizing = () => {
    if (!goalSizingResult) return
    const draft = goalSizingResult.scenario_draft
    const sizedGroups = goalSizedGroups(draft)
    const goalFields = goalSizedGoalFields(draft)
    setEditingId(null)
    setForm(prev => ({
      ...prev,
      name: draft.name || prev.name,
      duration: draft.duration || prev.duration,
      scenario_mode: goalFields.scenario_mode,
      goal_target_average_tps: goalFields.goal_target_average_tps,
      goal_min_vusers: goalFields.goal_min_vusers,
      goal_max_vusers: goalFields.goal_max_vusers,
      groups: ensureScenarioGroups(
        sizedGroups.map((group, index) => ({
          name: group.name,
          script_id: group.script_id || prev.groups[index]?.script_id || '',
          vusers: group.vusers,
          ramp_up: group.ramp_up,
          ramp_down: '',
          stages: [],
          initialization: 'at_runtime' as const,
        })),
      ),
    }))
    setSelectedShapeId('')
    setSelectedLoadModelTemplateId('')
    setSelectedCloneScenarioId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('goal')
    setStageGroupIndex(0)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setGoalSizingOpen(false)
    setWizardStep('run')
  }

  const closeGoalSizing = () => {
    setGoalSizingOpen(false)
    setWizardStep('start')
  }

  const updateScenarioGroup = (index: number, patch: Partial<ScenarioFormGroup>) => {
    setForm(prev => ({
      ...prev,
      groups: ensureScenarioGroups(
        prev.groups.map((group, groupIndex) => groupIndex === index ? { ...group, ...patch } : group),
      ),
    }))
    setLoadModelValidation(null)
  }

  const addScenarioGroup = () => {
    setForm(prev => {
      const nextGroups = [...prev.groups, emptyScenarioGroup('', prev.groups.length)]
      return { ...prev, groups: nextGroups }
    })
    setStageGroupIndex(form.groups.length)
    setLoadModelValidation(null)
  }

  const addScenarioGroupFromScript = (script: StoredScript) => {
    setForm(prev => {
      const nextGroups = [
        ...prev.groups,
        {
          ...emptyScenarioGroup(script.id, prev.groups.length),
          name: script.name,
          script_id: script.id,
        },
      ]
      return { ...prev, groups: ensureScenarioGroups(nextGroups) }
    })
    setStageGroupIndex(form.groups.length)
    setLoadModelValidation(null)
  }

  const chooseScriptForGroup = (index: number) => {
    setScriptSelectionTargetIndex(index)
    window.setTimeout(() => {
      scriptLibraryRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' })
      scriptLibrarySearchRef.current?.focus()
    }, 0)
  }

  const selectScriptFromLibrary = (script: StoredScript) => {
    if (scriptSelectionTargetIndex !== null && formGroups[scriptSelectionTargetIndex]) {
      const targetIndex = scriptSelectionTargetIndex
      updateScenarioGroup(targetIndex, { script_id: script.id })
      setScriptSelectionTargetIndex(null)
      window.setTimeout(() => {
        scriptGroupRefs.current[targetIndex]?.scrollIntoView?.({ behavior: 'smooth', block: 'center' })
      }, 0)
      return
    }
    addScenarioGroupFromScript(script)
  }

  const removeScenarioGroup = (index: number) => {
    setForm(prev => {
      const nextGroups = prev.groups.filter((_, groupIndex) => groupIndex !== index)
      return { ...prev, groups: ensureScenarioGroups(nextGroups) }
    })
    setScriptSelectionTargetIndex(current => {
      if (current === null) return null
      if (current === index) return null
      return current > index ? current - 1 : current
    })
    setStageGroupIndex(current => Math.max(0, Math.min(current, form.groups.length - 2)))
    setLoadModelValidation(null)
  }

  const enableDefaultStages = () => {
    const nextStages = createDefaultStages(activeStageGroup.vusers, activeStageGroup.ramp_up || '2m', form.duration || '5m', activeStageGroup.ramp_down || '1m')
    updateScenarioGroup(stageGroupIndex, { stages: nextStages, vusers: maxStageVUsers(nextStages), ramp_up: '', ramp_down: '' })
    setLoadModelValidation(null)
  }

  const disableStages = () => {
    const simpleRamp = simpleRampFromStages(activeStageGroup)
    updateScenarioGroup(stageGroupIndex, { ...simpleRamp, stages: [] })
    setLoadModelValidation(null)
    setLoadModelValidationError('')
  }

  const updateStage = (index: number, patch: Partial<LoadStageDraft>) => {
    const stages = activeStageGroup.stages.map((stage, stageIndex) => stageIndex === index ? { ...stage, ...patch } : stage)
    const peak = maxStageVUsers(stages)
    updateScenarioGroup(stageGroupIndex, { stages, vusers: peak || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const addStage = () => {
    const stages = [...activeStageGroup.stages, { name: `Stage ${activeStageGroup.stages.length + 1}`, type: 'hold' as LoadStageType, duration: '1m', target_vusers: Number(activeStageGroup.vusers) || 10 }]
    updateScenarioGroup(stageGroupIndex, { stages, vusers: maxStageVUsers(stages) || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const duplicateStage = (index: number) => {
    const source = activeStageGroup.stages[index]
    if (!source) return
    const stages = [
      ...activeStageGroup.stages.slice(0, index + 1),
      { ...source, name: source.name ? `${source.name} Copy` : `Stage ${index + 2}` },
      ...activeStageGroup.stages.slice(index + 1),
    ]
    updateScenarioGroup(stageGroupIndex, { stages, vusers: maxStageVUsers(stages) || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const removeStage = (index: number) => {
    const stages = activeStageGroup.stages.filter((_, stageIndex) => stageIndex !== index)
    updateScenarioGroup(stageGroupIndex, { stages, vusers: maxStageVUsers(stages) || activeStageGroup.vusers })
    setLoadModelValidation(null)
  }

  const validateLoadModel = async () => {
    if (form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setLoadModelValidationError('Select a script for every group before validating the load model.')
      return
    }
    if (!isValidDurationInput(form.duration)) {
      setLoadModelValidationError('Set a positive scenario duration before validating the load model.')
      return
    }
    if (blockingStageTimelineMessage) {
      setLoadModelValidationError(blockingStageTimelineMessage)
      return
    }
    setLoadModelValidationLoading(true)
    setLoadModelValidationError('')
    try {
      const report = await api.validateScenarioLoadModel(buildScenarioDraftPayload())
      setLoadModelValidation(report)
      setWizardStep('review')
    } catch (err: unknown) {
      setLoadModelValidation(null)
      setLoadModelValidationError(getErrorMessage(err, 'Unable to validate load model'))
    } finally {
      setLoadModelValidationLoading(false)
    }
  }

  const persistScenario = async () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return null
    }
    if (!form.name.trim()) {
      setError('')
      setWizardStep('review')
      return null
    }
    if (form.groups.length === 0 || form.groups.some(group => !group.script_id)) {
      setError('At least one script group with a selected script is required.')
      return null
    }
    if (!isValidDurationInput(form.duration)) {
      setError('Scenario duration is required.')
      return null
    }
    if (blockingStageTimelineMessage) {
      setError(blockingStageTimelineMessage)
      return null
    }
    const payload = buildScenarioDraftPayload()
    try {
      const saved = editingId ? await api.updateScenario(editingId, payload) : await api.createScenario(payload)
      return saved
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to save scenario'))
      return null
    }
  }

  const resetScenarioBuilder = () => {
    setEditingId(null)
    setForm(emptyScenarioForm())
    setStageGroupIndex(0)
    setSelectedLoadModelTemplateId('')
    setSelectedShapeId('')
    setScenarioSourceMode('shape')
    setScenarioStartChoice('')
    setScenarioCloneQuery('')
    setWizardStep('start')
    setMaxVisitedWizardIndex(0)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(false)
  }

  const saveScenario = async () => {
    const saved = await persistScenario()
    if (saved) {
      resetScenarioBuilder()
      refresh()
    }
  }

  const saveScenarioAndCreateTest = async () => {
    const saved = await persistScenario()
    if (!saved?.id) {
      setError('Scenario was saved, but the response did not include a scenario id.')
      return
    }
    setEditingId(null)
    onCreateTestFromScenario?.(saved.id)
    void refresh()
  }

  const deleteScenario = async (id: string) => {
    setConfirmDeleteScenarioId(null)
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot delete them.')
      return
    }
    try {
      await api.deleteScenario(id)
      refresh()
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to delete scenario'))
    }
  }

  const assignmentRecommendation = draftEstimate?.assignment_recommendation
  const availableTargets = assignmentRecommendation?.available_targets || []
  const recommendedTargets = assignmentRecommendation?.recommended_targets || []

  const toggleAssignmentTarget = (target: string, checked: boolean) => {
    const current = new Set(form.assignment_targets)
    if (checked) current.add(target)
    else current.delete(target)
    setForm({ ...form, assignment_mode: 'specific_hosts', assignment_targets: Array.from(current) })
  }

  const currentWizardIndex = scenarioWizardSteps.findIndex(step => step.id === wizardStep)
  useEffect(() => {
    if (currentWizardIndex >= 0) {
      setMaxVisitedWizardIndex(current => Math.max(current, currentWizardIndex))
    }
  }, [currentWizardIndex])
  const dataPoolLabel = form.data_pool_id
    ? dataPools.find(pool => pool.id === form.data_pool_id)?.name || 'Shared data configured'
    : 'No shared data pool'
  const shapeLabel = selectedTemplate?.name || selectedShape?.support.name || (scenarioSourceMode === 'existing' ? 'Modeled from scenario' : 'Manual shape')
  const validationLabel = loadModelValidation ? formatLoadModelDimension(loadModelValidation.status) : 'Not validated'
  const requiredDraftReady = Boolean(form.name.trim() && isValidDurationInput(form.duration) && formGroups.every(group => group.script_id))
  const sourceStepComplete = scenarioStartChoice === 'goal'
    ? Boolean(goalSizingResult)
    : scenarioSourceMode === 'existing'
      ? Boolean(selectedCloneScenarioId)
      : Boolean(selectedShapeId || selectedLoadModelTemplateId)
  const wizardStepCompletion: Record<ScenarioWizardStep, boolean> = {
    start: Boolean(scenarioStartChoice),
    source: sourceStepComplete,
    scripts: Boolean(form.name.trim() && formGroups.length > 0 && formGroups.every(group => group.script_id)),
    run: Boolean(isValidDurationInput(form.duration) && !blockingStageTimelineMessage),
    data: true,
    review: Boolean(requiredDraftReady && !blockingStageTimelineMessage),
  }

  const renderScenarioRunsContent = (scenario: StoredScenario) => {
    const runs = scenarioRuns[scenario.id] || []
    const loading = loadingRunsFor === scenario.id
    return (
      <div className="scenario-linked-runs-content">
        <div className="scenario-linked-runs-header">
          <div>
            <strong>Past Scenario Runs</strong>
            <span>{loading ? 'Loading runs...' : `${runs.length} linked run${runs.length === 1 ? '' : 's'}`}</span>
          </div>
          <span className="scenario-table-assignment-detail">{scenario.name}</span>
        </div>
        {runs.length === 0 ? (
          <div className="cell-muted">{loading ? 'Loading runs...' : 'No runs for this scenario yet.'}</div>
        ) : (
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th onClick={() => handleInnerSort('name')} className="sortable">
                    Run Name <SortIndicator active={innerSortField === 'name'} direction={innerSortDirection} />
                  </th>
                  <th onClick={() => handleInnerSort('status')} className="sortable">
                    Status <SortIndicator active={innerSortField === 'status'} direction={innerSortDirection} />
                  </th>
                  <th onClick={() => handleInnerSort('release_id')} className="sortable">
                    Release ID <SortIndicator active={innerSortField === 'release_id'} direction={innerSortDirection} />
                  </th>
                  <th onClick={() => handleInnerSort('start_time')} className="sortable">
                    Started <SortIndicator active={innerSortField === 'start_time'} direction={innerSortDirection} />
                  </th>
                  <th onClick={() => handleInnerSort('duration')} className="sortable">
                    Duration <SortIndicator active={innerSortField === 'duration'} direction={innerSortDirection} />
                  </th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {getSortedInnerRuns(runs).map(run => (
                  <tr key={run.id} className="clickable-row" onClick={(event) => { event.stopPropagation(); onSelectTest(run.id) }}>
                    <td className="cell-name">
                      <div>{run.name}</div>
                      <div className="script-table-meta">
                        <ReferenceKeyBadge value={run.ref_key} />
                      </div>
                    </td>
                    <td>{run.status}</td>
                    <td className="cell-mono">{run.release_id || '—'}</td>
                    <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                    <td className="cell-muted">{run.duration}</td>
                    <td style={{ textAlign: 'right' }}>
                      <div className="scenario-linked-run-actions">
                      <button className="btn btn-sm btn-secondary" onClick={(event) => { event.stopPropagation(); onSelectTest(run.id) }}>Open</button>
                      {onOpenCommandCenter && (
                        <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); onOpenCommandCenter(run.id) }}>Run Dashboard</button>
                      )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    )
  }

  const renderSourceScenarioRunsPanel = () => {
    if (wizardStep !== 'source' || scenarioSourceMode !== 'existing' || !selectedCloneScenario) return null
    return (
      <section className="scenario-source-runs-panel" aria-live="polite">
        {renderScenarioRunsContent(selectedCloneScenario)}
      </section>
    )
  }

  const renderGoalSizingPanel = () => {
    const previewTotal = goalBaselineGroups.reduce((sum, option) => {
      const tps = Number(goalTargetTps[option.group])
      return sum + (Number.isFinite(tps) && tps > 0 ? littlesLawVUsers(tps, option.baseline_art_ms) : 0)
    }, 0)
    return (
      <section className="scenario-wizard-panel" data-testid="goal-sizing-panel">
        <div className="scenario-wizard-panel-header">
          <div>
            <h4>Build From Goal</h4>
            <p>Pick a completed baseline run and a target throughput per group. APEX Load sizes VUsers with Little&apos;s Law (VUsers = target TPS × response time), then the goal controller converges at run time.</p>
          </div>
          <button type="button" className="mode-chip" onClick={closeGoalSizing}>Back</button>
        </div>

        <label className="settings-field">
          <span>Baseline run</span>
          <select value={goalBaselineId} onChange={event => void selectGoalBaseline(event.target.value)} aria-label="Baseline run">
            <option value="">Select a completed run…</option>
            {goalBaselineTests.map(test => (
              <option key={test.id} value={test.id}>{test.name}</option>
            ))}
          </select>
        </label>

        {goalBaselineGroups.length > 0 && (
          <div className="goal-sizing-groups">
            <table className="settings-table">
              <thead>
                <tr><th>Group</th><th>Baseline ART</th><th>Target TPS</th><th>Est. VUsers</th></tr>
              </thead>
              <tbody>
                {goalBaselineGroups.map(option => {
                  const tps = Number(goalTargetTps[option.group])
                  const estimate = Number.isFinite(tps) && tps > 0 ? littlesLawVUsers(tps, option.baseline_art_ms) : 0
                  return (
                    <tr key={option.group}>
                      <td>{option.group}</td>
                      <td>{Math.round(option.baseline_art_ms)} ms</td>
                      <td>
                        <input
                          className="settings-input"
                          type="number"
                          min="0"
                          step="1"
                          aria-label={`Target TPS for ${option.group}`}
                          value={goalTargetTps[option.group] ?? ''}
                          onChange={event => setGoalTargetTps(prev => ({ ...prev, [option.group]: event.target.value }))}
                        />
                      </td>
                      <td>{estimate > 0 ? estimate : '—'}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
            {previewTotal > 0 && <p className="goal-sizing-preview">Estimated total: <strong>{previewTotal} VUsers</strong></p>}
            <button type="button" className="primary-button" onClick={() => void runGoalSizing()} disabled={goalSizingLoading}>
              {goalSizingLoading ? 'Sizing…' : 'Size scenario'}
            </button>
          </div>
        )}

        {goalSizingError && <p className="form-error" role="alert">{goalSizingError}</p>}

        {goalSizingResult && (
          <div className="goal-sizing-result" data-testid="goal-sizing-result">
            <p>{formatGoalSizingSummary(goalSizingResult)}</p>
            <ul>
              {goalSizingResult.groups.map(group => (
                <li key={group.group}>{group.group}: <strong>{group.computed_vusers} VUsers</strong> ({group.target_tps} TPS × {Math.round(group.baseline_art_ms)} ms)</li>
              ))}
            </ul>
            {goalSizingResult.load_model_validation?.capacity_advisory && (
              <p className="goal-sizing-capacity">Capacity: {goalSizingResult.load_model_validation.capacity_advisory.recommended_generators} generator(s) recommended.</p>
            )}
            <button type="button" className="primary-button" onClick={applyGoalSizing}>Apply to scenario</button>
          </div>
        )}
      </section>
    )
  }

  const renderWizardStep = () => {
    if (goalSizingOpen) {
      return renderGoalSizingPanel()
    }
    if (wizardStep === 'start') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Start With A Model</h4>
            <p>Choose whether this scenario should inherit a known workload or start from a supported load shape.</p>
          </div>
          <div className="scenario-source-grid">
            <button type="button" className={`scenario-source-card ${scenarioSourceMode === 'existing' ? 'selected' : ''}`} onClick={() => selectScenarioSourceMode('existing')}>
              <span>Model from Existing Scenario</span>
              <strong>Clone Draft</strong>
              <small>Search the scenario library, copy its scripts, run settings, data pool, and generator defaults, then save as a new scenario.</small>
            </button>
            <button type="button" className={`scenario-source-card ${scenarioSourceMode === 'shape' ? 'selected' : ''}`} onClick={() => selectScenarioSourceMode('shape')}>
              <span>Build from Workload Shape</span>
              <strong>Unified Catalog</strong>
              <small>Select a supported workload model and optionally apply one of its starter templates.</small>
            </button>
            <button type="button" className="scenario-source-card" onClick={() => void openGoalSizing()}>
              <span>Build from Goal</span>
              <strong>Little&apos;s Law Sizing</strong>
              <small>Pick a completed baseline run and a target TPS per group; VUsers are sized automatically and tuned by the goal controller.</small>
            </button>
          </div>
        </section>
      )
    }

    if (wizardStep === 'source') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <div>
              <h4>{scenarioSourceMode === 'existing' ? 'Choose Scenario To Model' : 'Choose Workload Shape'}</h4>
              <p>{scenarioSourceMode === 'existing' ? 'The selected scenario becomes an editable copy; the original remains unchanged.' : 'Supported workload models and starter templates are combined into one actionable catalog.'}</p>
            </div>
            <div className="scenario-source-toggle">
              <button type="button" className={`mode-chip ${scenarioSourceMode === 'shape' ? 'active' : ''}`} onClick={() => selectScenarioSourceMode('shape')}>Shapes</button>
              <button type="button" className={`mode-chip ${scenarioSourceMode === 'existing' ? 'active' : ''}`} onClick={() => selectScenarioSourceMode('existing')}>Existing</button>
            </div>
          </div>
          {scenarioSourceMode === 'existing' ? (
            <>
              <label className="library-search scenario-clone-search">
                <span>Search existing scenarios</span>
                <input type="search" value={scenarioCloneQuery} onChange={event => setScenarioCloneQuery(event.target.value)} placeholder="Scenario, script, data pool, release..." />
              </label>
              <div className="data-table scenario-clone-table">
                <table>
                  <thead>
                    <tr>
                      <th>Source</th>
                      <th>Scenario</th>
                      <th>Test ID</th>
                      <th>Workload</th>
                      <th>Associated Releases</th>
                      <th>Create Date</th>
                      <th>Start</th>
                      <th>Finish</th>
                      <th style={{ textAlign: 'right' }}>Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filteredCloneScenarios.map(scenario => {
                      const selected = selectedCloneScenarioId === scenario.id
                      const assignment = scenarioAssignmentSummary(scenario)
                      const showScenarioMeta = Boolean(scenario.ref_key || assignment)
                      const browserRuntimeSummary = formatBrowserRuntimeSummary(scenario.runtime_settings?.browser)
                      return (
                        <tr
                          key={scenario.id}
                          className={`clickable-row ${selected ? 'selected' : ''}`}
                          aria-selected={selected}
                          tabIndex={0}
                          onClick={() => cloneScenarioDraft(scenario)}
                          onKeyDown={(event) => {
                            if (event.key === 'Enter' || event.key === ' ') {
                              event.preventDefault()
                              cloneScenarioDraft(scenario)
                            }
                          }}
                        >
                          <td>
                            <span className="script-health-token">Local</span>
                          </td>
                          <td className="cell-name">
                            <div>{scenario.name}</div>
                            {showScenarioMeta && (
                              <div className="scenario-table-assignment">
                                <ReferenceKeyBadge value={scenario.ref_key} />
                                {assignment && (
                                  <>
                                    <span className="scenario-table-assignment-badge manual">{assignment.badge}</span>
                                    <span className="scenario-table-assignment-detail">{assignment.detail}</span>
                                  </>
                                )}
                              </div>
                            )}
                            {browserRuntimeSummary && (
                              <div className="scenario-table-browser">
                                <span className="scenario-table-browser-badge">Browser</span>
                                <span className="scenario-table-assignment-detail">{browserRuntimeSummary}</span>
                              </div>
                            )}
                          </td>
                          <td className="cell-mono">{formatScenarioRunTestID(scenario)}</td>
                          <td>
                            <div className="scenario-table-workload">
                              <strong>{formatScenarioScripts(scenario)}</strong>
                              <span>{formatScenarioDataPools(scenario)}</span>
                              <span>{formatScenarioResultCount(scenario)}</span>
                            </div>
                          </td>
                          <td>
                            <div className="scenario-table-workload">
                              <strong>{releaseList(scenario)}</strong>
                            </div>
                          </td>
                          <td className="cell-muted">{formatDate(scenario.created_at)}</td>
                          <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_at)}</td>
                          <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_ended_at)}</td>
                          <td style={{ textAlign: 'right' }}>
                            <button
                              className={`btn btn-sm ${selected ? 'btn-primary' : 'btn-secondary'}`}
                              type="button"
                              onClick={(event) => {
                                event.stopPropagation()
                                cloneScenarioDraft(scenario)
                              }}
                            >
                              {selected ? 'Selected' : 'Model'}
                            </button>
                          </td>
                        </tr>
                      )
                    })}
                    {filteredCloneScenarios.length === 0 && (
                      <tr>
                        <td colSpan={9} className="cell-muted">No scenarios match this source search.</td>
                      </tr>
                    )}
                  </tbody>
                </table>
              </div>
            </>
          ) : (
            <div className="scenario-shape-catalog-grid">
              {shapeCatalog.map(option => (
                <div key={option.id} className={`scenario-shape-catalog-card ${selectedShapeId === option.id ? 'selected' : ''}`}>
                  <div className="scenario-support-card-header">
                    <div>
                      <strong>{option.support.name}</strong>
                      <span>{option.support.category}</span>
                    </div>
                    <span className={`scenario-load-status ${getLoadModelStatusClass(option.support.status)}`}>{formatLoadModelDimension(option.support.status)}</span>
                  </div>
                  <p>{option.support.apexload_shape}</p>
                  <div className="scenario-support-fields">
                    {option.support.fields.slice(0, 3).map(field => <code key={field}>{field}</code>)}
                    {option.support.fields.length > 3 && <code>+{option.support.fields.length - 3} fields</code>}
                  </div>
                  <WorkloadShapeVisual entry={option.support} />
                  <div className="scenario-shape-actions">
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => selectShapeOption(option.id)}>Use Manual Shape</button>
                    {option.templates.map(template => (
                      <button key={template.id} type="button" className={`btn btn-sm ${selectedLoadModelTemplateId === template.id ? 'btn-primary' : 'btn-ghost'}`} onClick={() => applyLoadModelTemplate(template.id)}>
                        {template.name}
                      </button>
                    ))}
                  </div>
                </div>
              ))}
              {shapeCatalog.length === 0 && <p className="scenario-assignment-copy secondary">No workload shape catalog is available from this controller.</p>}
            </div>
          )}
        </section>
      )
    }

    if (wizardStep === 'scripts') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <div>
              <h4>Scripts And Groups</h4>
              <p>Add each script participating in the scenario and set its initialization scope. Tune VUsers and ramp on the Run Settings step.</p>
            </div>
          </div>
          <div className="settings-grid scenario-core-grid">
            <label className="scenario-field">
              <span>Scenario Name</span>
              <input className="settings-input" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} placeholder="Scenario name" />
            </label>
          </div>
          <div className="scenario-script-group-list scenario-script-group-list-spacious">
            {formGroups.map((group, index) => {
              const selectedScript = findScriptById(scripts, group.script_id)
              const isSelectionTarget = scriptSelectionTargetIndex === index
              return (
                <div
                  key={`${group.script_id || 'script'}-${index}`}
                  className={`scenario-script-group-card ${isSelectionTarget ? 'selecting-script' : ''}`}
                  ref={node => { scriptGroupRefs.current[index] = node }}
                >
                  <div className="scenario-script-group-title">
                    <strong>{group.name || `Group ${index + 1}`}</strong>
                    {formGroups.length > 1 && (
                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => removeScenarioGroup(index)}>Remove</button>
                    )}
                  </div>
                  <div className="settings-grid scenario-group-grid">
                    <label className="scenario-field">
                      <span>Group Name</span>
                      <input className="settings-input" value={group.name} onChange={event => updateScenarioGroup(index, { name: event.target.value })} placeholder={`Group ${index + 1}`} />
                    </label>
                    <div className="scenario-field scenario-script-picker-field">
                      <span>Script</span>
                      <button
                        type="button"
                        className={`scenario-script-select-button ${selectedScript ? 'has-script' : ''}`}
                        onClick={() => chooseScriptForGroup(index)}
                        aria-label={`Choose script for ${group.name || `Group ${index + 1}`}`}
                      >
                        <span className="scenario-script-select-copy">
                          <strong>{selectedScript?.name || 'Add Script'}</strong>
                          {selectedScript && <small>v{selectedScript.version || '—'}</small>}
                        </span>
                        {selectedScript && <span className="protocol-badge">{selectedScript.protocol || 'http'}</span>}
                      </button>
                    </div>
                    <label className="scenario-field">
                      <span>Initialization Scope</span>
                      <select className="settings-input" value={group.initialization} onChange={event => updateScenarioGroup(index, { initialization: event.target.value as ScenarioFormGroup['initialization'] })}>
                        <option value="at_runtime">Init/teardown per VUser</option>
                        <option value="at_once">Init/teardown once per group</option>
                      </select>
                    </label>
                    <label className="scenario-field">
                      <span>Role</span>
                      <select
                        className="settings-input"
                        value={group.role || 'load'}
                        onChange={event => updateScenarioGroup(index, { role: event.target.value as ScenarioFormGroup['role'] })}
                        title="Observer canaries are small browser groups that continuously report Web Vitals and stay pinned during goal-based scaling."
                      >
                        <option value="load">Load</option>
                        <option value="observer" disabled={selectedScript?.protocol !== 'browser'}>
                          Observer Canary{selectedScript?.protocol !== 'browser' ? ' (browser scripts only)' : ''}
                        </option>
                      </select>
                    </label>
                  </div>
                </div>
              )
            })}
            <button type="button" className="scenario-add-group-card" onClick={addScenarioGroup}>
              <span>+</span>
              <strong>Add Group</strong>
            </button>
          </div>
        </section>
      )
    }

    if (wizardStep === 'run') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Run Settings</h4>
            <p>Configure VUsers, ramp, duration, mode, goals, stage modeling, browser defaults, and generator assignment.</p>
          </div>
          <div className="settings-grid scenario-core-grid">
            <DurationField
              className="scenario-field"
              label="Duration"
              value={form.duration}
              required
              showPresets
              onChange={value => {
                setForm({ ...form, duration: value })
                setLoadModelValidation(null)
                setLoadModelValidationError('')
              }}
            />
            <label className="scenario-field">
              <span>Scenario Mode</span>
              <select className="settings-input" value={form.scenario_mode} onChange={event => setForm({ ...form, scenario_mode: event.target.value as ScenarioMode })}>
                <option value="vuser_driven">VUser driven</option>
                <option value="goal_oriented">Goal oriented</option>
              </select>
            </label>
            <label className="scenario-field scenario-stage-group-picker">
              <span>Configure stages for</span>
              <select className="settings-input" value={stageGroupIndex} onChange={event => setStageGroupIndex(Number(event.target.value))}>
                {formGroups.map((group, index) => (
                  <option key={`${group.script_id || 'group'}-${index}`} value={index}>
                    {group.name || `Group ${index + 1}`} · {findScriptById(scripts, group.script_id)?.name || 'Unassigned script'}
                  </option>
                ))}
              </select>
            </label>
          </div>
          <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Load Per Group</h4>
                <p>Set the VUser count and simple ramp for each script group participating in this scenario.</p>
              </div>
            </div>
            <div className="scenario-script-group-list">
              {formGroups.map((group, index) => (
                <div key={`${group.script_id || 'group'}-load-${index}`} className="scenario-script-group-card">
                  <div className="scenario-script-group-title">
                    <strong>{group.name || `Group ${index + 1}`}</strong>
                    <span className="scenario-assignment-copy secondary">{findScriptById(scripts, group.script_id)?.name || 'Unassigned script'}</span>
                  </div>
                  <div className="settings-grid scenario-group-grid">
                    <label className="scenario-field">
                      <span>VUsers</span>
                      <div className="scenario-vuser-field">
                        <input
                          className="scenario-vuser-slider"
                          type="range"
                          min={1}
                          max={Math.max(100, Number(group.vusers) * 2 || 100)}
                          value={group.vusers}
                          onChange={event => updateScenarioGroup(index, { vusers: Number(event.target.value) })}
                        />
                        <input className="settings-input" type="number" min={1} value={group.vusers} onChange={event => updateScenarioGroup(index, { vusers: Number(event.target.value) })} />
                      </div>
                    </label>
                    <DurationField
                      className="scenario-field"
                      label="Ramp Up"
                      value={group.ramp_up}
                      allowEmpty
                      compact
                      onChange={value => updateScenarioGroup(index, { ramp_up: value })}
                    />
                    <DurationField
                      className="scenario-field"
                      label="Ramp Down"
                      value={group.ramp_down}
                      allowEmpty
                      compact
                      onChange={value => updateScenarioGroup(index, { ramp_down: value })}
                    />
                  </div>
                </div>
              ))}
            </div>
          </div>
          <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Pre-Run Assurance</h4>
                <p>Run a one-pass canary/shakeout immediately before full load. Passing starts the run automatically; failures pause with evidence for continue/cancel.</p>
              </div>
            </div>
            <div className="scenario-option-grid">
              <label className={`scenario-option-card ${form.script_shakeout ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.script_shakeout} onChange={event => setForm({ ...form, script_shakeout: event.target.checked })} />
                <div><strong>Script shakeout gate</strong><span>Execute init/actions once for each script group before full load.</span></div>
              </label>
              <label className={`scenario-option-card ${form.resource_baseline ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.resource_baseline} onChange={event => setForm({ ...form, resource_baseline: event.target.checked })} />
                <div><strong>Resource baseline windows</strong><span>Capture 5m pre/post resource evidence around the run.</span></div>
              </label>
              <label className={`scenario-option-card ${form.continue_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.continue_on_error} onChange={event => setForm({ ...form, continue_on_error: event.target.checked })} />
                <div><strong>Continue on action errors</strong><span>Keep collecting resilience evidence after request-level failures.</span></div>
              </label>
              <label className={`scenario-option-card ${form.snapshot_on_error ? 'selected' : ''}`}>
                <input type="checkbox" checked={form.snapshot_on_error} onChange={event => setForm({ ...form, snapshot_on_error: event.target.checked })} />
                <div><strong>Snapshot on error</strong><span>Preserve request/response context for failed actions.</span></div>
              </label>
            </div>
          </div>
          {form.scenario_mode === 'goal_oriented' && (
            <div className="scenario-subpanel">
              <div className="scenario-assignment-header">
                <div>
                  <h4>Goal Controller</h4>
                  <p>Persist target envelope settings with this scenario draft.</p>
                </div>
              </div>
              <div className="scenario-browser-grid">
                <label><span>Target average TPS</span><input className="settings-input" type="number" min={0} value={form.goal_target_average_tps || ''} onChange={event => setForm({ ...form, goal_target_average_tps: Number(event.target.value) || undefined })} /></label>
                <label><span>Min VUsers</span><input className="settings-input" type="number" min={0} value={form.goal_min_vusers || ''} onChange={event => setForm({ ...form, goal_min_vusers: Number(event.target.value) || undefined })} /></label>
                <label><span>Max VUsers</span><input className="settings-input" type="number" min={0} value={form.goal_max_vusers || ''} onChange={event => setForm({ ...form, goal_max_vusers: Number(event.target.value) || undefined })} /></label>
                <label><span>P95 latency ms</span><input className="settings-input" type="number" min={0} value={form.goal_p95_latency_ms || ''} onChange={event => setForm({ ...form, goal_p95_latency_ms: Number(event.target.value) || undefined })} /></label>
                <label><span>Max error %</span><input className="settings-input" type="number" min={0} value={form.goal_max_error_pct || ''} onChange={event => setForm({ ...form, goal_max_error_pct: Number(event.target.value) || undefined })} /></label>
              </div>
            </div>
          )}
          <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Advanced Stair-Step Ramp</h4>
                <p>Keep simple ramp fields for common runs, or enable explicit stages when each interval needs its own VUser/TPS target.</p>
              </div>
              <div className="scenario-stage-actions">
                {activeStageGroup.stages.length > 0 && (
                  <button type="button" className="btn btn-secondary" onClick={disableStages}>
                    Use Simple Ramp
                  </button>
                )}
                <button type="button" className="btn btn-secondary" onClick={activeStageGroup.stages.length > 0 ? addStage : enableDefaultStages}>
                  {activeStageGroup.stages.length > 0 ? '+ Add Stage' : 'Enable Stages'}
                </button>
              </div>
            </div>
            {activeStageGroup.stages.length > 0 ? (
              <div className="scenario-stage-list">
                <div className="scenario-stage-toolbar">
                  <span className={`scenario-stage-status ${activeStageTimeline.status}`}>{formatLoadModelDimension(activeStageTimeline.status)}</span>
                  {activeStageTimeline.message && <span>{activeStageTimeline.message}</span>}
                  <button type="button" className="btn btn-sm btn-ghost" onClick={enableDefaultStages}>Regenerate From Simple Ramp</button>
                </div>
                {activeStageGroup.stages.map((stage, index) => (
                  <div key={`${stage.name || 'stage'}-${index}`} className="scenario-stage-row">
                    <input className="settings-input" value={stage.name || ''} onChange={event => updateStage(index, { name: event.target.value })} placeholder="Stage name" />
                    <select className="settings-input" value={stage.type} onChange={event => updateStage(index, { type: event.target.value as LoadStageType })}>
                      <option value="ramp_up">Ramp up</option>
                      <option value="hold">Hold</option>
                      <option value="ramp_down">Ramp down</option>
                    </select>
                    <DurationField
                      value={stage.duration}
                      compact
                      required
                      showValidation={false}
                      onChange={value => updateStage(index, { duration: value })}
                    />
                    <input className="settings-input" type="number" min={0} value={stage.target_vusers ?? ''} onChange={event => updateStage(index, { target_vusers: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Target VUsers" />
                    <input className="settings-input" type="number" min={0} value={stage.target_tps ?? ''} onChange={event => updateStage(index, { target_tps: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Target TPS" />
                    <div className="scenario-stage-row-actions">
                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => duplicateStage(index)}>Duplicate</button>
                      <button type="button" className="btn btn-sm btn-ghost" onClick={() => removeStage(index)}>Remove</button>
                    </div>
                  </div>
                ))}
                <p className="scenario-assignment-copy secondary">Peak staged VUsers: {maxStageVUsers(activeStageGroup.stages) || 0}. Validation will reject stages missing duration plus either VUsers or TPS.</p>
              </div>
            ) : (
              <p className="scenario-assignment-copy secondary">Simple ramp is active for this group. Enable advanced stages to define stair-step intervals and per-step VUser/TPS targets.</p>
            )}
          </div>
          {showBrowserRuntimeSettings && (
            <div className="scenario-subpanel">
              <div className="scenario-assignment-header">
                <div>
                  <h4>Browser Runtime</h4>
                  <p>Store device defaults with this scenario so queued runs and comparisons carry the same mobile profile.</p>
                </div>
              </div>
              <div className="scenario-browser-grid">
                <label><span>Device profile</span><select className="settings-input" value={form.browser_device_profile || ''} onChange={event => setForm({ ...form, browser_device_profile: event.target.value })}><option value="">Script default</option><option value="desktop_1080p">desktop_1080p</option><option value="galaxy_s24">galaxy_s24</option><option value="pixel_8">pixel_8</option><option value="ipad_air">ipad_air (WebKit)</option><option value="iphone_15_pro">iphone_15_pro (WebKit)</option><option value="iphone_se">iphone_se (WebKit)</option></select></label>
                <label><span>Orientation</span><select className="settings-input" value={form.browser_orientation || ''} onChange={event => setForm({ ...form, browser_orientation: event.target.value })}><option value="">Script default</option><option value="portrait">Portrait</option><option value="landscape">Landscape</option></select></label>
                <label><span>Touch mode</span><select className="settings-input" value={form.browser_touch_mode || ''} onChange={event => setForm({ ...form, browser_touch_mode: event.target.value as ScenarioFormState['browser_touch_mode'] })}><option value="">Script default</option><option value="true">Enabled</option><option value="false">Disabled</option></select></label>
                <label><span>Headless</span><select className="settings-input" value={form.browser_headless || ''} onChange={event => setForm({ ...form, browser_headless: event.target.value as ScenarioFormState['browser_headless'] })}><option value="">Script default</option><option value="true">Enabled</option><option value="false">Disabled</option></select></label>
              </div>
            </div>
          )}
          <div className="scenario-subpanel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Controller / Generator Assignment</h4>
                <p>Use scenario size to decide whether the controller should auto-place the run or restrict it to specific generator hostnames.</p>
              </div>
              {estimateLoading && <span className="scenario-assignment-status">Estimating...</span>}
            </div>
            <div className="scenario-assignment-switcher">
              <button type="button" className={`mode-chip ${form.assignment_mode === 'auto' ? 'active' : ''}`} onClick={() => setForm({ ...form, assignment_mode: 'auto', assignment_targets: [] })}>Auto Assignment</button>
              <button type="button" className={`mode-chip ${form.assignment_mode === 'specific_hosts' ? 'active' : ''}`} onClick={() => setForm({ ...form, assignment_mode: 'specific_hosts' })}>Specific Hostnames</button>
            </div>
            {draftEstimate?.footprint && (
              <div className="scenario-assignment-footprint">
                <div className="scenario-assignment-metric"><span>Footprint</span><strong>{draftEstimate.footprint.total_vusers} VUsers</strong><small>{draftEstimate.footprint.duration}</small></div>
                <div className="scenario-assignment-metric"><span>Complexity</span><strong>{draftEstimate.footprint.complexity_score.toFixed(2)}</strong><small>derived from current script mix</small></div>
                <div className="scenario-assignment-metric"><span>Estimated Load</span><strong>{draftEstimate.footprint.estimated_cpu_cores.toFixed(2)} CPU</strong><small>{Math.round(draftEstimate.footprint.estimated_memory_mb)} MB / {Math.round(draftEstimate.footprint.estimated_network_mbps)} Mbps</small></div>
              </div>
            )}
            {assignmentRecommendation?.summary && <p className="scenario-assignment-copy">{assignmentRecommendation.summary}</p>}
            {form.assignment_mode === 'specific_hosts' ? (
              availableTargets.length > 0 ? (
                <div className="scenario-assignment-targets">
                  {availableTargets.map((target) => {
                    const checked = form.assignment_targets.includes(target)
                    const recommended = recommendedTargets.includes(target)
                    return (
                      <label key={target} className={`scenario-assignment-target ${checked ? 'selected' : ''}`}>
                        <input type="checkbox" checked={checked} onChange={(event) => toggleAssignmentTarget(target, event.target.checked)} />
                        <div><strong>{target}</strong><span>{recommended ? 'Recommended for this scenario footprint' : 'Configured on the controller'}</span></div>
                      </label>
                    )
                  })}
                </div>
              ) : <p className="scenario-assignment-copy secondary">No distributed generator hostnames are currently configured on this controller.</p>
            ) : recommendedTargets.length > 0 ? (
              <div className="scenario-assignment-chip-row">{recommendedTargets.map(target => <span key={target} className="scenario-assignment-chip">{target}</span>)}</div>
            ) : <p className="scenario-assignment-copy secondary">Auto-assignment will use the controller&apos;s configured generator inventory when distributed execution is enabled.</p>}
            {estimateError && <div className="settings-error-inline">{estimateError}</div>}
            {assignmentRecommendation?.warnings && assignmentRecommendation.warnings.length > 0 && (
              <div className="scenario-assignment-warnings">{assignmentRecommendation.warnings.map(warning => <p key={warning}>{warning}</p>)}</div>
            )}
          </div>
        </section>
      )
    }

    if (wizardStep === 'data') {
      return (
        <section className="scenario-wizard-panel">
          <div className="scenario-wizard-panel-header">
            <h4>Shared Data Pool</h4>
            <p>Attach one shared data pool for scripts that need coordinated parameter data.</p>
          </div>
          <div className="settings-grid scenario-core-grid">
            <label className="scenario-field">
              <span>Shared Data Pool</span>
              <select className="settings-input" value={form.data_pool_id || ''} onChange={event => setForm({ ...form, data_pool_id: event.target.value })}>
                <option value="">No shared data pool</option>
                {dataPools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
              </select>
            </label>
            <label className="scenario-field">
              <span>Pool Alias</span>
              <input className="settings-input" value={form.data_pool_alias || ''} onChange={event => setForm({ ...form, data_pool_alias: event.target.value })} placeholder="Pool alias, e.g. customers" disabled={!form.data_pool_id} />
            </label>
            <label className="scenario-field">
              <span>Access Mode</span>
              <select className="settings-input" value={form.data_pool_access_mode || 'read_only'} onChange={event => setForm({ ...form, data_pool_access_mode: event.target.value as 'read_only' | 'read_write' | 'consume' })} disabled={!form.data_pool_id}>
                <option value="read_only">Read only</option>
                <option value="read_write">Read/write</option>
                <option value="consume">Consume rows</option>
              </select>
            </label>
            <label className="scenario-field">
              <span>Selection Strategy</span>
              <select className="settings-input" value={form.data_pool_selection_strategy || 'sequential'} onChange={event => setForm({ ...form, data_pool_selection_strategy: event.target.value as 'sequential' | 'random' | 'unique' })} disabled={!form.data_pool_id}>
                <option value="sequential">Sequential rows</option>
                <option value="random">Random rows</option>
                <option value="unique">Unique rows</option>
              </select>
            </label>
          </div>
        </section>
      )
    }

    return (
      <section className="scenario-wizard-panel">
        <div className="scenario-wizard-panel-header">
          <div>
            <h4>Review And Validate</h4>
            <p>Validate modeled demand, capacity, data fit, and generator placement before saving.</p>
          </div>
          <button type="button" className="btn btn-secondary" onClick={validateLoadModel} disabled={loadModelValidationLoading || Boolean(blockingStageTimelineMessage)}>
            {loadModelValidationLoading ? 'Validating...' : 'Validate Load Model'}
          </button>
        </div>
        {!form.name.trim() && (
          <label className="scenario-field scenario-name-prompt">
            <span>Name this scenario to save it</span>
            <input
              className="settings-input"
              value={form.name}
              autoFocus
              placeholder="Scenario name"
              aria-label="Scenario name"
              onChange={event => setForm({ ...form, name: event.target.value })}
            />
          </label>
        )}
        <div className="scenario-review-grid">
          <div className="scenario-review-card"><span>Name</span><strong>{form.name.trim() || 'Unnamed scenario'}</strong></div>
          <div className="scenario-review-card"><span>Shape</span><strong>{shapeLabel}</strong></div>
          <div className="scenario-review-card"><span>Scripts</span><strong>{formGroups.length} group{formGroups.length === 1 ? '' : 's'}</strong></div>
          <div className="scenario-review-card"><span>Data</span><strong>{dataPoolLabel}</strong></div>
          <div className="scenario-review-card"><span>Shakeout</span><strong>{form.script_shakeout ? 'Enabled' : 'Disabled'}</strong></div>
        </div>
        {loadModelValidationError && <div className="settings-error-inline">{loadModelValidationError}</div>}
        {loadModelValidation ? (
          <div className="scenario-load-validation">
            <div className={`scenario-load-validation-summary ${getLoadModelStatusClass(loadModelValidation.status)}`}>
              <strong>{formatLoadModelDimension(loadModelValidation.status)}</strong>
              <span>{loadModelValidation.total_vusers} VUsers{loadModelValidation.peak_target_tps ? ` · ${loadModelValidation.peak_target_tps} TPS peak` : ''}</span>
            </div>
            <div className="scenario-load-validation-items">
              {loadModelValidation.items.map((item, index) => (
                <div key={`${item.dimension}-${item.group || 'all'}-${index}`} className="scenario-load-validation-item">
                  <span className={`scenario-load-status ${getLoadModelStatusClass(item.status)}`}>{formatLoadModelDimension(item.status)}</span>
                  <div><strong>{formatLoadModelDimension(item.dimension)}{item.group ? ` · ${item.group}` : ''}</strong><p>{item.summary}</p></div>
                </div>
              ))}
            </div>
          </div>
        ) : (
          <p className="scenario-assignment-copy secondary">Run validation to check demand, capacity, data fit, and generator placement.</p>
        )}
      </section>
    )
  }

  return (
    <div className="page">
      {(!showForm || !hasTopbarContributionProvider) && (
        <div className="page-header">
          <div>
            <h2>{showForm ? 'Scenario Builder' : 'Scenario Library'}</h2>
            <p className="page-subtitle">Reusable scenario definitions linked to scripts, releases, and results.</p>
          </div>
          {!hasTopbarContributionProvider && scenarioPageControls}
        </div>
      )}

      {error && <div className="settings-error-inline">{error}</div>}

      {showForm && (
        <div className="page-section scenario-builder">
          <div className="scenario-builder-header">
            <div>
              {form.name.trim()
                ? <p className="scenario-builder-name" aria-label="Scenario name">{form.name.trim()}</p>
                : <p className="page-subtitle">Choose a source, compose scripts, tune run settings, attach data, and validate the planned model.</p>}
            </div>
            <div className="scenario-builder-progress">
              <span className={requiredDraftReady ? 'ready' : ''}>Draft</span>
              <span className={selectedShapeId || scenarioSourceMode === 'existing' ? 'ready' : ''}>Model</span>
              <span className={loadModelValidation?.status === 'blocked' ? 'blocked' : loadModelValidation ? 'ready' : ''}>Validation</span>
            </div>
          </div>

          <div className="scenario-wizard-stepper" aria-label="Scenario builder steps">
            {scenarioWizardSteps.map((step, index) => {
              const stepComplete = index <= maxVisitedWizardIndex && wizardStepCompletion[step.id]
              return (
                <button
                  key={step.id}
                  type="button"
                  className={`scenario-wizard-step ${wizardStep === step.id ? 'active' : ''} ${stepComplete ? 'complete' : ''}`}
                  onClick={() => setWizardStep(step.id)}
                  aria-label={`${step.label}${stepComplete ? ' complete' : ''}`}
                >
                  <span className="scenario-wizard-step-marker" aria-hidden="true">{stepComplete ? '✓' : index + 1}</span>
                  <span className="scenario-wizard-step-label">{step.label}</span>
                </button>
              )
            })}
          </div>

          {renderWizardStep()}

          {showModelPreview && (
            <ScenarioModelPreviewPanel
              model={scenarioModel}
              shapeLabel={shapeLabel}
              dataPoolLabel={dataPoolLabel}
              validationStatus={validationLabel}
            />
          )}

          {renderSourceScenarioRunsPanel()}

          {blockingStageTimelineMessage && <div className="settings-error-inline">{blockingStageTimelineMessage}</div>}

          <div className="scenario-builder-actions">
            <button className="btn btn-secondary" onClick={openScenarioLibrary}>Cancel</button>
            <button
              className="btn btn-secondary"
              onClick={() => {
                const nextIndex = Math.max(0, currentWizardIndex - 1)
                setWizardStep(scenarioWizardSteps[nextIndex].id)
              }}
              disabled={currentWizardIndex === 0}
            >
              Back
            </button>
            <button
              className="btn btn-secondary"
              onClick={() => {
                const nextIndex = Math.min(scenarioWizardSteps.length - 1, currentWizardIndex + 1)
                setWizardStep(scenarioWizardSteps[nextIndex].id)
              }}
              disabled={currentWizardIndex === scenarioWizardSteps.length - 1}
            >
              Next
            </button>
            <button className="btn btn-secondary" onClick={validateLoadModel} disabled={loadModelValidationLoading || Boolean(blockingStageTimelineMessage)}>{loadModelValidationLoading ? 'Validating...' : 'Validate Load Model'}</button>
            {wizardStep === 'review' && onCreateTestFromScenario && (
              <button className="btn btn-secondary" onClick={saveScenarioAndCreateTest} disabled={Boolean(blockingStageTimelineMessage)}>Save &amp; Create Test Run</button>
            )}
            <button className="btn btn-primary" onClick={saveScenario} disabled={Boolean(blockingStageTimelineMessage)}>Save Scenario</button>
          </div>

        </div>
      )}

      {showScriptLibrary && (
        <div className="page-section scenario-existing-script-library" ref={scriptLibraryRef}>
          <div className="library-toolbar">
            <div>
              <h3 className="section-title">Existing Scripts Library ({scripts.length})</h3>
              <p className="library-toolbar-subtitle">
                {scriptLibraryQuery.trim() ? `${visibleScriptLibrary.length} matching script${visibleScriptLibrary.length === 1 ? '' : 's'}` : 'Pick from existing scripts while assembling scenario groups.'}
              </p>
              {scriptSelectionTargetIndex !== null && formGroups[scriptSelectionTargetIndex] && (
                <p className="scenario-script-selection-status">
                  Choosing for {formGroups[scriptSelectionTargetIndex].name || `Group ${scriptSelectionTargetIndex + 1}`}
                </p>
              )}
            </div>
            <label className="library-search">
              <span>Search scripts</span>
              <input
                ref={scriptLibrarySearchRef}
                type="search"
                value={scriptLibraryQuery}
                onChange={(event) => setScriptLibraryQuery(event.target.value)}
                placeholder="Find scripts..."
              />
            </label>
          </div>
          <div className="data-table">
            <table>
              <thead>
                <tr>
                  <th onClick={() => handleScriptLibrarySort('name')} className="sortable">Script <SortIndicator active={scriptLibrarySortField === 'name'} direction={scriptLibrarySortDirection} /></th>
                  <th onClick={() => handleScriptLibrarySort('created_at')} className="sortable">Created <SortIndicator active={scriptLibrarySortField === 'created_at'} direction={scriptLibrarySortDirection} /></th>
                  <th onClick={() => handleScriptLibrarySort('last_run_at')} className="sortable">Last Run <SortIndicator active={scriptLibrarySortField === 'last_run_at'} direction={scriptLibrarySortDirection} /></th>
                  <th onClick={() => handleScriptLibrarySort('associated_scenarios')} className="sortable">Associated Scenarios <SortIndicator active={scriptLibrarySortField === 'associated_scenarios'} direction={scriptLibrarySortDirection} /></th>
                  <th onClick={() => handleScriptLibrarySort('associated_releases')} className="sortable">Associated Releases <SortIndicator active={scriptLibrarySortField === 'associated_releases'} direction={scriptLibrarySortDirection} /></th>
                  <th onClick={() => handleScriptLibrarySort('protocol')} className="sortable">Protocol(s) <SortIndicator active={scriptLibrarySortField === 'protocol'} direction={scriptLibrarySortDirection} /></th>
                  <th style={{ textAlign: 'right' }}>Actions</th>
                </tr>
              </thead>
              <tbody>
                {visibleScriptLibrary.map(script => {
                  const targetScriptId = scriptSelectionTargetIndex !== null ? formGroups[scriptSelectionTargetIndex]?.script_id : ''
                  const selectedForTarget = Boolean(targetScriptId) && targetScriptId === script.id
                  return (
                    <tr key={script.id} className={selectedForTarget ? 'selected' : ''}>
                      <td className="cell-name">
                        <div>{script.name}</div>
                        <div className="scenario-table-assignment-detail">v{script.version || '—'} · {script.id}</div>
                      </td>
                      <td className="cell-muted">{formatDate(script.created_at)}</td>
                      <td className="cell-muted">{formatDate(script.last_run_at)}</td>
                      <td>
                        <div className="scenario-table-workload">
                          <strong>{formatScriptScenarioCount(script)}</strong>
                          <span>{associatedScenarioList(script)}</span>
                        </div>
                      </td>
                      <td>
                        <div className="scenario-table-workload">
                          <strong>{scriptReleaseList(script)}</strong>
                          {!!script.scenario_run_releases?.length && <span>Run releases: {uniqueValues(script.scenario_run_releases.map(release => release.name || release.id)).join(', ')}</span>}
                        </div>
                      </td>
                      <td>
                        <div className="scenario-protocol-list">
                          {scriptProtocols(script).map(protocol => <span key={protocol} className="protocol-badge">{protocol}</span>)}
                        </div>
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <button className="btn btn-sm btn-secondary" type="button" onClick={() => selectScriptFromLibrary(script)}>
                          {scriptSelectionTargetIndex !== null ? `Use for Group ${scriptSelectionTargetIndex + 1}` : 'Add Group'}
                        </button>
                      </td>
                    </tr>
                  )
                })}
                {visibleScriptLibrary.length === 0 && (
                  <tr>
                    <td colSpan={7} className="cell-muted">
                      {scriptLibraryQuery.trim() ? 'No scripts match this search.' : 'No existing scripts are available for this project.'}
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {showScenarioLibrary && (
      <div className="page-section">
        <LibraryFederationControls
          kind="scenario"
          federation={scenarioFederation}
          onConfigureRemotes={openRemoteSettings}
          summaryText={scenarioQuery.trim() || scenarioReleaseFilter !== 'all' ? `${sorted.length} matching scenario${sorted.length === 1 ? '' : 's'}` : 'Search by scenario, script, data pool, release, host, or browser profile.'}
          filterControls={(
            <>
              <label className="project-redaction-rule-field">
                <span>Associated Release</span>
                <select
                  className="settings-input library-source-select library-release-filter"
                  value={scenarioReleaseFilter}
                  onChange={(event) => setScenarioReleaseFilter(event.target.value)}
                >
                  <option value="all">All releases</option>
                  {scenarioReleaseFilter !== 'all' && !scenarioReleaseOptions.includes(scenarioReleaseFilter) && (
                    <option value={scenarioReleaseFilter}>{scenarioReleaseFilter}</option>
                  )}
                  {scenarioReleaseOptions.map(name => <option key={name} value={name}>{name}</option>)}
                </select>
              </label>
              <label className="project-redaction-rule-field">
                <span>Search Release</span>
                <input
                  className="settings-input"
                  type="search"
                  value={scenarioQuery}
                  onChange={(event) => setScenarioQuery(event.target.value)}
                  placeholder="Find releases or scenarios..."
                />
              </label>
            </>
          )}
        />
        <div className="data-table scenario-library-table">
          <table>
            <thead>
              <tr>
                <th>Source</th>
                <th onClick={() => handleSort('name')} className="sortable">Scenario <SortIndicator active={sortField === 'name'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('last_run_test_id')} className="sortable">Test ID <SortIndicator active={sortField === 'last_run_test_id'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('scripts')} className="sortable">Workload <SortIndicator active={sortField === 'scripts'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('associated_releases')} className="sortable">Associated Releases <SortIndicator active={sortField === 'associated_releases'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('created_at')} className="sortable">Create Date <SortIndicator active={sortField === 'created_at'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('last_run_at')} className="sortable">Start <SortIndicator active={sortField === 'last_run_at'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('last_run_ended_at')} className="sortable">Finish <SortIndicator active={sortField === 'last_run_ended_at'} direction={sortDirection} /></th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {showLocalScenarios && sorted.map(scenario => {
                const syncStatus = scenarioFederation.syncStatusForLocal(scenario.id)
                const hostItem = scenarioFederation.hostItemForLocal(scenario.id)
                const conflict = syncStatus === 'conflict'
                const assignment = scenarioAssignmentSummary(scenario)
                const showScenarioMeta = Boolean(scenario.ref_key || assignment)
                return (
                <Fragment key={scenario.id}>
                  <tr className="clickable-row" onClick={() => toggleScenarioRuns(scenario)}>
                    <td>
                      <span className="script-health-token">Local</span>
                    </td>
                    <td className="cell-name">
                      <div>{scenario.name}</div>
                      {showScenarioMeta && (
                        <div className="scenario-table-assignment">
                          <ReferenceKeyBadge value={scenario.ref_key} />
                          {assignment && (
                            <>
                              <span className="scenario-table-assignment-badge manual">{assignment.badge}</span>
                              <span className="scenario-table-assignment-detail">{assignment.detail}</span>
                            </>
                          )}
                        </div>
                      )}
                      {formatBrowserRuntimeSummary(scenario.runtime_settings?.browser) && (
                        <div className="scenario-table-browser">
                          <span className="scenario-table-browser-badge">Browser</span>
                          <span className="scenario-table-assignment-detail">{formatBrowserRuntimeSummary(scenario.runtime_settings?.browser)}</span>
                        </div>
                      )}
                    </td>
                    <td className="cell-mono">{formatScenarioRunTestID(scenario)}</td>
                    <td>
                      <div className="scenario-table-workload">
                        <strong>{formatScenarioScripts(scenario)}</strong>
                        <span>{formatScenarioDataPools(scenario)}</span>
                        <span>{formatScenarioResultCount(scenario)}</span>
                      </div>
                    </td>
                    <td>
                      <div className="scenario-table-workload">
                        <strong>{releaseList(scenario)}</strong>
                      </div>
                    </td>
                    <td className="cell-muted">{formatDate(scenario.created_at)}</td>
                    <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_at)}</td>
                    <td className="cell-muted scenario-run-time-cell">{formatDateTime(scenario.last_run_ended_at)}</td>
                    <td style={{ textAlign: 'right' }}>
                      <button className="btn btn-sm btn-secondary" onClick={(event) => {
                        event.stopPropagation()
                        void toggleScenarioRuns(scenario)
                      }}>{expandedScenarioId === scenario.id ? 'Hide Runs' : 'View Runs'}</button>
                      {canManageTests ? (
                        <>
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); openEdit(scenario) }}>Edit</button>
                          <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); duplicateScenario(scenario) }}>Duplicate</button>
                          <button className="btn btn-sm btn-ghost" style={{ color: 'var(--danger)' }} onClick={(event) => { event.stopPropagation(); setConfirmDeleteScenarioId(scenario.id) }}>Delete</button>
                          <button
                            className="btn btn-sm btn-secondary"
                            onClick={(event) => { event.stopPropagation(); void scenarioFederation.pushLocalItem({ kind: 'scenario', id: scenario.id, name: scenario.name }) }}
                            disabled={scenarioFederation.actionBlocked || conflict}
                            title={conflict ? 'Compare this conflict before transferring.' : scenarioFederation.actionBlockedReason}
                          >Push</button>
                          {hostItem?.local_item_id && (
                            <button className="btn btn-sm btn-ghost" onClick={(event) => { event.stopPropagation(); void scenarioFederation.compareItems(scenario.id, hostItem) }}>Compare</button>
                          )}
                        </>
                      ) : (
                        <span className="cell-muted">Read-only</span>
                      )}
                    </td>
                  </tr>
                  {expandedScenarioId === scenario.id && (
                    <tr className="no-hover">
                      <td colSpan={9} style={{ background: 'var(--bg-primary)' }}>
                        {renderScenarioRunsContent(scenario)}
                      </td>
                    </tr>
                  )}
                </Fragment>
                )
              })}
              {federationRemoteScenarioRows.map(item => {
                const syncStatus = item.sync_status || 'host only'
                const conflict = syncStatus === 'conflict'
                const href = scenarioFederation.remoteItemHref(item)
                return (
                  <tr key={`remote:${item.kind}:${item.id}`} className="no-hover">
                    <td>
                      <span className="script-health-token">Remote</span>
                    </td>
                    <td className="cell-name">
                      <div>{item.name || item.id}</div>
                      <div className="scenario-table-assignment-detail">Project {item.project_id || 'unmapped'} · Hash {shortLibraryHash(item.content_hash)}</div>
                    </td>
                    <td className="cell-mono">—</td>
                    <td>
                      <div className="scenario-table-workload">
                        <span>{item.dependencies?.length ? `${item.dependencies.length} dependencies` : 'No referenced scripts'}</span>
                      </div>
                    </td>
                    <td className="cell-muted">—</td>
                    <td className="cell-muted">—</td>
                    <td className="cell-muted">—</td>
                    <td className="cell-muted">—</td>
                    <td style={{ textAlign: 'right' }}>
                      {canManageTests ? (
                        <>
                          <button
                            className="btn btn-sm btn-secondary"
                            onClick={() => void scenarioFederation.pullRemoteItem(item)}
                            disabled={scenarioFederation.actionBlocked || conflict}
                            title={conflict ? 'Compare this conflict before transferring.' : scenarioFederation.actionBlockedReason}
                          >Pull</button>
                          {item.local_item_id && (
                            <button className="btn btn-sm btn-ghost" onClick={() => void scenarioFederation.compareItems(item.local_item_id!, item)}>Compare</button>
                          )}
                          {href && <a className="btn btn-sm btn-ghost" href={href} target="_blank" rel="noreferrer">Open in Remote Library</a>}
                        </>
                      ) : (
                        <span className="cell-muted">Read-only</span>
                      )}
                    </td>
                  </tr>
                )
              })}
              {(!showLocalScenarios || sorted.length === 0) && federationRemoteScenarioRows.length === 0 && (
                <tr>
                  <td colSpan={9} className="cell-muted">
                    {scenarioQuery.trim() || scenarioReleaseFilter !== 'all' ? 'No scenarios match these filters.' : 'No scenarios yet. Create a scenario from an existing script.'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      )}

      {confirmDeleteScenarioId && (
        <div className="modal-overlay" onClick={() => setConfirmDeleteScenarioId(null)}>
          <div className="modal" role="dialog" aria-modal="true" onClick={event => event.stopPropagation()}>
            <div className="modal-header">
              <h3>Delete Scenario</h3>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setConfirmDeleteScenarioId(null)}>Cancel</button>
            </div>
            <div className="monitor-body">
              <p>Permanently delete <strong>{scenarios.find(s => s.id === confirmDeleteScenarioId)?.name || 'this scenario'}</strong>? Scenarios with linked results cannot be deleted.</p>
              <p style={{ color: 'var(--danger)', fontWeight: 600 }}>This action cannot be undone.</p>
              <div className="settings-inline-actions">
                <button className="btn btn-secondary" type="button" onClick={() => setConfirmDeleteScenarioId(null)}>Cancel</button>
                <button className="btn btn-danger" type="button" onClick={() => deleteScenario(confirmDeleteScenarioId)}>Delete Scenario</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default memo(TestHub)
