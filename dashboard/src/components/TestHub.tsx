import { Fragment, memo, useCallback, useEffect, useMemo, useState } from 'react'
import { DashboardAuthEffectiveAccessResource, DraftCapacityEstimate, GoalConfig, LoadModelSupportEntry, LoadModelTemplate, LoadModelValidationReport, LoadStageDraft, ScenarioDataPoolBinding, ScenarioMode, SharedDataPool, StoredScenario, StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { getErrorMessage } from '../utils/errorUtils'
import './TestHub.css'
import './CreateTestModal.css'
import { buildCreateTestPayload, buildScenarioUpsertPayload } from './scenarioPayloadUtils'
import { formatBrowserRuntimeSummary } from './browserRuntimeSummary'

interface Props {
  testStatuses: Record<string, string>
  onSelectTest: (id: string) => void
}

interface ScenarioFormState {
  name: string
  duration: string
  script_id: string
  vusers: number
  ramp_up: string
  ramp_down: string
  stages: LoadStageDraft[]
  initialization: 'at_runtime' | 'at_once'
  scenario_mode: ScenarioMode
  goal_target_average_tps?: number
  goal_min_vusers?: number
  goal_max_vusers?: number
  goal_p95_latency_ms?: number
  goal_max_error_pct?: number
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

function emptyScenarioForm(scriptId = ''): ScenarioFormState {
  return {
    name: '',
    duration: '1m',
    script_id: scriptId,
    vusers: 10,
    ramp_up: '',
    ramp_down: '',
    stages: [],
    initialization: 'at_runtime',
    scenario_mode: 'vuser_driven',
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

function createDefaultStages(vusers: number, rampUp = '2m', hold = '5m', rampDown = '1m'): LoadStageDraft[] {
  const target = Number(vusers) || 10
  return [
    { name: 'Warmup', type: 'ramp_up', duration: rampUp || '2m', target_vusers: target },
    { name: 'Steady', type: 'hold', duration: hold, target_vusers: target },
    { name: 'Cooldown', type: 'ramp_down', duration: rampDown || '1m', target_vusers: 1 },
  ]
}

function getLoadModelStatusClass(status?: string) {
  if (status === 'ready' || status === 'executed') return 'ready'
  if (status === 'blocked' || status === 'planned') return 'blocked'
  return 'needs-review'
}

function formatLoadModelDimension(value: string) {
  return value.replace(/_/g, ' ').replace(/\b\w/g, char => char.toUpperCase())
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

function getScenarioSortValue(scenario: StoredScenario, field: 'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_at' | 'result_count'): string | number {
  switch (field) {
    case 'scripts':
      return (scenario.scripts || []).length
    case 'data_pools':
      return (scenario.data_pools || []).length
    case 'created_at':
      return new Date(scenario.created_at || 0).getTime()
    case 'last_run_at':
      return new Date(scenario.last_run_at || 0).getTime()
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

function scenarioSearchText(scenario: StoredScenario) {
  return [
    scenario.name,
    scenario.duration,
    scenario.assignment?.mode,
    ...(scenario.assignment?.selected_targets || []),
    ...(scenario.scripts || []).map(script => script.script_name || script.name || script.script_id),
    ...(scenario.data_pools || []).map(pool => `${pool.alias} ${pool.pool_name || pool.pool_id}`),
    ...(scenario.associated_releases || []).map(release => release.name),
    formatBrowserRuntimeSummary(scenario.runtime_settings?.browser),
  ].filter(Boolean).join(' ').toLowerCase()
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
  return {
    badge: 'Auto Assignment',
    detail: 'Uses controller recommendations',
  }
}

function SortIndicator({ active, direction }: { active: boolean, direction: 'asc' | 'desc' }) {
  return (
    <span className={`sort-icon ${active ? 'active' : ''}`}>
      {active ? (direction === 'asc' ? '↑' : '↓') : '↕'}
    </span>
  )
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

function TestHub({ onSelectTest }: Props) {
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
  const [showForm, setShowForm] = useState(false)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [form, setForm] = useState<ScenarioFormState>(emptyScenarioForm())
  const [error, setError] = useState('')
  const [scenarioQuery, setScenarioQuery] = useState('')
  const [sortField, setSortField] = useState<'name' | 'scripts' | 'data_pools' | 'created_at' | 'associated_releases' | 'last_run_at' | 'result_count'>('last_run_at')
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
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const selectedScript = scripts.find(script => script.id === form.script_id)
  const showBrowserRuntimeSettings = selectedScript?.protocol === 'browser'
  const canManageTests = useMemo(
    () => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_tests'),
    [effectiveAuthAccessResources],
  )

  const buildScenarioDraftPayload = useCallback((draftForm: ScenarioFormState = form) => {
    const stagedPeak = maxStageVUsers(draftForm.stages)
    const resolvedVUsers = stagedPeak || Number(draftForm.vusers) || 10
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
      scriptShakeout: false,
      resourceBaseline: false,
      assignment: draftForm.assignment_mode === 'specific_hosts'
        ? { mode: 'specific_hosts', selected_targets: draftForm.assignment_targets }
        : { mode: 'auto', selected_targets: [] },
      groups: [{
        name: '',
        scriptId: draftForm.script_id,
        vusers: resolvedVUsers,
        rampUp: draftForm.ramp_up,
        rampDown: draftForm.ramp_down,
        stages: draftForm.stages.map(normalizeStageForPayload),
        initialization: draftForm.initialization,
      }],
      goalConfig: goalConfigFromForm(draftForm),
      runtimeSettings: {
        iterations: undefined,
        pacing: undefined,
        think_time: undefined,
        browser: showBrowserRuntimeSettings ? browserRuntimeFromForm(draftForm) : undefined,
        log_level: '',
        auto_transactions: '',
        continue_on_error: false,
        snapshot_on_error: false,
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
        script_id: nextScripts.some(script => script.id === prev.script_id) ? prev.script_id : nextScripts[0]?.id || '',
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
    if (!showForm || !form.script_id) {
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
      groups: [{
        name: '',
        scriptId: form.script_id,
        vusers: maxStageVUsers(form.stages) || Number(form.vusers) || 10,
        rampUp: form.ramp_up,
        rampDown: form.ramp_down,
        stages: form.stages.map(normalizeStageForPayload),
        initialization: form.initialization,
      }],
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

  const sorted = useMemo(() => {
    const query = scenarioQuery.trim().toLowerCase()
    const filteredScenarios = query
      ? scenarios.filter(scenario => scenarioSearchText(scenario).includes(query))
      : scenarios
    return [...filteredScenarios].sort((a, b) => {
      const aValue = getScenarioSortValue(a, sortField)
      const bValue = getScenarioSortValue(b, sortField)
      if (aValue < bValue) return sortDirection === 'asc' ? -1 : 1
      if (aValue > bValue) return sortDirection === 'asc' ? 1 : -1
      return 0
    })
  }, [scenarios, scenarioQuery, sortField, sortDirection])

  const handleSort = (field: typeof sortField) => {
    if (sortField === field) setSortDirection(prev => prev === 'asc' ? 'desc' : 'asc')
    else {
      setSortField(field)
      setSortDirection(field === 'last_run_at' || field === 'result_count' ? 'desc' : 'asc')
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

  const toggleScenarioRuns = async (scenario: StoredScenario) => {
    if (expandedScenarioId === scenario.id) {
      setExpandedScenarioId(null)
      return
    }
    setExpandedScenarioId(scenario.id)
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

  const openCreate = () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    setEditingId(null)
    setForm(emptyScenarioForm(scripts[0]?.id || ''))
    setSelectedLoadModelTemplateId('')
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    setShowForm(true)
  }

  const openEdit = (scenario: StoredScenario) => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    const first = scenario.scripts[0]
    const firstPool = scenario.data_pools?.[0]
    setEditingId(scenario.id)
    setForm({
      name: scenario.name,
      duration: scenario.duration,
      script_id: first?.script_id || scripts[0]?.id || '',
      vusers: first?.vusers || 10,
      ramp_up: first?.ramp_up || '',
      ramp_down: first?.ramp_down || '',
      stages: first?.stages ? [...first.stages] : [],
      initialization: first?.initialization || 'at_runtime',
      scenario_mode: scenario.scenario_mode || 'vuser_driven',
      goal_target_average_tps: scenario.goal_config?.target_average_tps,
      goal_min_vusers: scenario.goal_config?.min_vusers,
      goal_max_vusers: scenario.goal_config?.max_vusers,
      goal_p95_latency_ms: scenario.goal_config?.p95_latency_ms,
      goal_max_error_pct: scenario.goal_config?.max_error_pct,
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
    })
    setSelectedLoadModelTemplateId('')
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

  const applyLoadModelTemplate = (templateId: string) => {
    setSelectedLoadModelTemplateId(templateId)
    setLoadModelValidation(null)
    setLoadModelValidationError('')
    const template = loadModelTemplates.find(item => item.id === templateId)
    if (!template) return
    const firstGroup = template.scenario.groups[0]
    setForm(prev => ({
      ...prev,
      duration: template.scenario.duration || prev.duration,
      scenario_mode: template.scenario.scenario_mode || 'vuser_driven',
      vusers: firstGroup?.vusers || prev.vusers,
      ramp_up: firstGroup?.ramp_up || '',
      ramp_down: firstGroup?.ramp_down || '',
      stages: [],
      goal_target_average_tps: template.scenario.goal_config?.target_average_tps,
      goal_min_vusers: template.scenario.goal_config?.min_vusers,
      goal_max_vusers: template.scenario.goal_config?.max_vusers,
      goal_p95_latency_ms: template.scenario.goal_config?.p95_latency_ms,
      goal_max_error_pct: template.scenario.goal_config?.max_error_pct,
    }))
  }

  const enableDefaultStages = () => {
    const nextStages = createDefaultStages(form.vusers, form.ramp_up || '2m', form.duration || '5m', form.ramp_down || '1m')
    setForm({ ...form, stages: nextStages, vusers: maxStageVUsers(nextStages), ramp_up: '', ramp_down: '' })
    setLoadModelValidation(null)
  }

  const updateStage = (index: number, patch: Partial<LoadStageDraft>) => {
    const stages = form.stages.map((stage, stageIndex) => stageIndex === index ? { ...stage, ...patch } : stage)
    const peak = maxStageVUsers(stages)
    setForm({ ...form, stages, vusers: peak || form.vusers })
    setLoadModelValidation(null)
  }

  const addStage = () => {
    const stages = [...form.stages, { name: `Stage ${form.stages.length + 1}`, type: 'hold' as LoadStageType, duration: '1m', target_vusers: Number(form.vusers) || 10 }]
    setForm({ ...form, stages, vusers: maxStageVUsers(stages) || form.vusers })
    setLoadModelValidation(null)
  }

  const removeStage = (index: number) => {
    const stages = form.stages.filter((_, stageIndex) => stageIndex !== index)
    setForm({ ...form, stages, vusers: maxStageVUsers(stages) || form.vusers })
    setLoadModelValidation(null)
  }

  const validateLoadModel = async () => {
    if (!form.script_id) {
      setLoadModelValidationError('Select a script before validating the load model.')
      return
    }
    setLoadModelValidationLoading(true)
    setLoadModelValidationError('')
    try {
      const report = await api.validateScenarioLoadModel(buildScenarioDraftPayload())
      setLoadModelValidation(report)
    } catch (err: unknown) {
      setLoadModelValidation(null)
      setLoadModelValidationError(getErrorMessage(err, 'Unable to validate load model'))
    } finally {
      setLoadModelValidationLoading(false)
    }
  }

  const saveScenario = async () => {
    if (!canManageTests) {
      setError('Current session can view scenarios but cannot create or edit them.')
      return
    }
    if (!form.name.trim() || !form.script_id) {
      setError('Scenario name and script are required.')
      return
    }
    const payload = buildScenarioDraftPayload()
    try {
      if (editingId) await api.updateScenario(editingId, payload)
      else await api.createScenario(payload)
      setShowForm(false)
      setEditingId(null)
      refresh()
    } catch (err: unknown) {
      setError(getErrorMessage(err, 'Unable to save scenario'))
    }
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

  return (
    <div className="page">
      <div className="page-header">
        <div>
          <h2>Scenarios</h2>
          <p className="page-subtitle">Reusable scenario definitions linked to scripts, releases, and results.</p>
        </div>
        {canManageTests && <button className="btn btn-primary" onClick={openCreate}>+ New Scenario</button>}
      </div>

      {error && <div className="settings-error-inline">{error}</div>}

      {showForm && (
        <div className="page-section scenario-builder">
          <div className="scenario-builder-header">
            <div>
              <h3 className="section-title">{editingId ? 'Edit Scenario' : 'New Scenario'}</h3>
              <p className="page-subtitle">Complete the core setup first; open advanced sections only when the workload needs them.</p>
            </div>
            <div className="scenario-builder-progress">
              <span className={form.name.trim() ? 'ready' : ''}>Basics</span>
              <span className={form.script_id ? 'ready' : ''}>Script</span>
              <span className={loadModelValidation?.status === 'blocked' ? 'blocked' : loadModelValidation ? 'ready' : ''}>Validation</span>
            </div>
          </div>

          <section className="scenario-builder-card">
            <div className="scenario-builder-card-header">
              <div>
                <h4>Basics</h4>
                <p>Name the reusable workload and choose the primary script group.</p>
              </div>
            </div>
            <div className="settings-grid scenario-core-grid">
              <label className="scenario-field">
                <span>Scenario Name</span>
                <input className="settings-input" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} placeholder="Scenario name" />
              </label>
              <label className="scenario-field">
                <span>Duration</span>
                <input className="settings-input" value={form.duration} onChange={event => setForm({ ...form, duration: event.target.value })} placeholder="Duration, e.g. 5m" />
              </label>
              <label className="scenario-field">
                <span>Script</span>
                <select className="settings-input" value={form.script_id} onChange={event => setForm({ ...form, script_id: event.target.value })}>
                  {scripts.map(script => <option key={script.id} value={script.id}>{script.name}</option>)}
                </select>
              </label>
              <label className="scenario-field">
                <span>VUsers</span>
                <input className="settings-input" type="number" min={1} value={form.vusers} onChange={event => setForm({ ...form, vusers: Number(event.target.value) })} />
              </label>
              <label className="scenario-field">
                <span>Ramp Up</span>
                <input className="settings-input" value={form.ramp_up} onChange={event => setForm({ ...form, ramp_up: event.target.value })} placeholder="Ramp up, e.g. 2m" />
              </label>
              <label className="scenario-field">
                <span>Ramp Down</span>
                <input className="settings-input" value={form.ramp_down} onChange={event => setForm({ ...form, ramp_down: event.target.value })} placeholder="Ramp down, e.g. 1m" />
              </label>
              <label className="scenario-field">
                <span>Scenario Mode</span>
                <select className="settings-input" value={form.scenario_mode} onChange={event => setForm({ ...form, scenario_mode: event.target.value as ScenarioMode })}>
                  <option value="vuser_driven">VUser driven</option>
                  <option value="goal_oriented">Goal oriented</option>
                </select>
              </label>
              <label className="scenario-field">
                <span>Initialization Scope</span>
                <select className="settings-input" value={form.initialization} onChange={event => setForm({ ...form, initialization: event.target.value as ScenarioFormState['initialization'] })}>
                  <option value="at_runtime">Init/teardown per VUser</option>
                  <option value="at_once">Init/teardown once per group</option>
                </select>
              </label>
            </div>
          </section>

          <details className="scenario-builder-disclosure">
            <summary>Data Pool Options</summary>
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
                <input
                  className="settings-input"
                  value={form.data_pool_alias || ''}
                  onChange={event => setForm({ ...form, data_pool_alias: event.target.value })}
                  placeholder="Pool alias, e.g. customers"
                  disabled={!form.data_pool_id}
                />
              </label>
              <label className="scenario-field">
                <span>Access Mode</span>
                <select
                  className="settings-input"
                  value={form.data_pool_access_mode || 'read_only'}
                  onChange={event => setForm({ ...form, data_pool_access_mode: event.target.value as 'read_only' | 'read_write' | 'consume' })}
                  disabled={!form.data_pool_id}
                >
                  <option value="read_only">Read only</option>
                  <option value="read_write">Read/write</option>
                  <option value="consume">Consume rows</option>
                </select>
              </label>
              <label className="scenario-field">
                <span>Selection Strategy</span>
                <select
                  className="settings-input"
                  value={form.data_pool_selection_strategy || 'sequential'}
                  onChange={event => setForm({ ...form, data_pool_selection_strategy: event.target.value as 'sequential' | 'random' | 'unique' })}
                  disabled={!form.data_pool_id}
                >
                  <option value="sequential">Sequential rows</option>
                  <option value="random">Random rows</option>
                  <option value="unique">Unique rows</option>
                </select>
              </label>
            </div>
          </details>
          {form.scenario_mode === 'goal_oriented' && (
            <div className="scenario-browser-panel">
              <div className="scenario-assignment-header">
                <div>
                  <h4>Goal Controller</h4>
                  <p>Persist goal-oriented load settings with the scenario draft so validation and execution use the same target envelope.</p>
                </div>
              </div>
              <div className="scenario-browser-grid">
                <label>
                  <span>Target average TPS</span>
                  <input className="settings-input" type="number" min={0} value={form.goal_target_average_tps || ''} onChange={event => setForm({ ...form, goal_target_average_tps: Number(event.target.value) || undefined })} />
                </label>
                <label>
                  <span>Min VUsers</span>
                  <input className="settings-input" type="number" min={0} value={form.goal_min_vusers || ''} onChange={event => setForm({ ...form, goal_min_vusers: Number(event.target.value) || undefined })} />
                </label>
                <label>
                  <span>Max VUsers</span>
                  <input className="settings-input" type="number" min={0} value={form.goal_max_vusers || ''} onChange={event => setForm({ ...form, goal_max_vusers: Number(event.target.value) || undefined })} />
                </label>
                <label>
                  <span>P95 latency ms</span>
                  <input className="settings-input" type="number" min={0} value={form.goal_p95_latency_ms || ''} onChange={event => setForm({ ...form, goal_p95_latency_ms: Number(event.target.value) || undefined })} />
                </label>
                <label>
                  <span>Max error %</span>
                  <input className="settings-input" type="number" min={0} value={form.goal_max_error_pct || ''} onChange={event => setForm({ ...form, goal_max_error_pct: Number(event.target.value) || undefined })} />
                </label>
              </div>
            </div>
          )}
          <div className="scenario-init-panel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Initialization Scope</h4>
                <p>{form.initialization === 'at_once'
                  ? 'Script init and teardown run once for this group, useful for shared seed or setup flows.'
                  : 'Each VUser runs script init and teardown independently, matching the default closed-model behavior.'}</p>
              </div>
              <span className="scenario-assignment-status">{form.initialization === 'at_once' ? 'Once per group' : 'Per VUser'}</span>
            </div>
          </div>
          <div className="scenario-stage-panel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Staged Load Segments</h4>
                <p>Model ramp-up, hold, and ramp-down segments explicitly. When stages are enabled, the peak target VUsers becomes the group VUser count.</p>
              </div>
              <button type="button" className="btn btn-secondary" onClick={form.stages.length > 0 ? addStage : enableDefaultStages}>
                {form.stages.length > 0 ? '+ Add Stage' : 'Enable Stages'}
              </button>
            </div>
            {form.stages.length > 0 ? (
              <div className="scenario-stage-list">
                {form.stages.map((stage, index) => (
                  <div key={`${stage.name || 'stage'}-${index}`} className="scenario-stage-row">
                    <input className="settings-input" value={stage.name || ''} onChange={event => updateStage(index, { name: event.target.value })} placeholder="Stage name" />
                    <select className="settings-input" value={stage.type} onChange={event => updateStage(index, { type: event.target.value as LoadStageType })}>
                      <option value="ramp_up">Ramp up</option>
                      <option value="hold">Hold</option>
                      <option value="ramp_down">Ramp down</option>
                    </select>
                    <input className="settings-input" value={stage.duration} onChange={event => updateStage(index, { duration: event.target.value })} placeholder="Duration" />
                    <input className="settings-input" type="number" min={0} value={stage.target_vusers ?? ''} onChange={event => updateStage(index, { target_vusers: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Target VUsers" />
                    <input className="settings-input" type="number" min={0} value={stage.target_tps ?? ''} onChange={event => updateStage(index, { target_tps: event.target.value === '' ? undefined : Number(event.target.value) })} placeholder="Target TPS" />
                    <button type="button" className="btn btn-sm btn-ghost" onClick={() => removeStage(index)}>Remove</button>
                  </div>
                ))}
                <p className="scenario-assignment-copy secondary">Peak staged VUsers: {maxStageVUsers(form.stages) || 0}. Validation will reject stages missing duration plus either VUsers or TPS.</p>
              </div>
            ) : (
              <p className="scenario-assignment-copy secondary">Use simple ramp fields above, or enable stages for stepped ramps and mixed target constraints.</p>
            )}
          </div>
          <div className="scenario-load-model-panel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Workload Shape Templates</h4>
                <p>Apply a starter shape, then validate modeled demand, capacity, data fit, and generator placement before saving.</p>
              </div>
              <button type="button" className="btn btn-secondary" onClick={validateLoadModel} disabled={loadModelValidationLoading}>
                {loadModelValidationLoading ? 'Validating...' : 'Validate Load Model'}
              </button>
            </div>
            <div className="scenario-template-grid">
              {loadModelTemplates.map(template => (
                <button
                  key={template.id}
                  type="button"
                  className={`scenario-template-card ${selectedLoadModelTemplateId === template.id ? 'selected' : ''}`}
                  onClick={() => applyLoadModelTemplate(template.id)}
                >
                  <span>{template.name}</span>
                  <strong>{template.shape_id}</strong>
                  <small>{template.description}</small>
                </button>
              ))}
              {loadModelTemplates.length === 0 && <p className="scenario-assignment-copy secondary">No workload templates are available from this controller.</p>}
            </div>
            {selectedLoadModelTemplateId && (
              <p className="scenario-assignment-copy secondary">
                Applied {loadModelTemplates.find(template => template.id === selectedLoadModelTemplateId)?.name}. Review script assignment and capacity validation before saving.
              </p>
            )}
            {loadModelValidationError && <div className="settings-error-inline">{loadModelValidationError}</div>}
            {loadModelValidation && (
              <div className="scenario-load-validation">
                <div className={`scenario-load-validation-summary ${getLoadModelStatusClass(loadModelValidation.status)}`}>
                  <strong>{formatLoadModelDimension(loadModelValidation.status)}</strong>
                  <span>{loadModelValidation.total_vusers} VUsers{loadModelValidation.peak_target_tps ? ` · ${loadModelValidation.peak_target_tps} TPS peak` : ''}</span>
                </div>
                <div className="scenario-load-validation-items">
                  {loadModelValidation.items.map((item, index) => (
                    <div key={`${item.dimension}-${item.group || 'all'}-${index}`} className="scenario-load-validation-item">
                      <span className={`scenario-load-status ${getLoadModelStatusClass(item.status)}`}>{formatLoadModelDimension(item.status)}</span>
                      <div>
                        <strong>{formatLoadModelDimension(item.dimension)}{item.group ? ` · ${item.group}` : ''}</strong>
                        <p>{item.summary}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
          <details className="scenario-builder-disclosure">
            <summary>Workload Shape Support</summary>
          <div className="scenario-support-panel compact">
            <div className="scenario-assignment-header">
              <div>
                <h4>Workload Shape Support</h4>
                <p>Code-owned support matrix for enterprise workload models and the Stormrunner fields that implement them.</p>
              </div>
            </div>
            {loadModelSupportMatrix.length > 0 ? (
              <div className="scenario-support-grid">
                {loadModelSupportMatrix.map(entry => (
                  <div key={entry.id} className="scenario-support-card">
                    <div className="scenario-support-card-header">
                      <div>
                        <strong>{entry.name}</strong>
                        <span>{entry.category}</span>
                      </div>
                      <span className={`scenario-load-status ${getLoadModelStatusClass(entry.status)}`}>{formatLoadModelDimension(entry.status)}</span>
                    </div>
                    <p>{entry.stormrunner_shape}</p>
                    <div className="scenario-support-fields">
                      {entry.fields.map(field => <code key={field}>{field}</code>)}
                    </div>
                    <small>{entry.notes}</small>
                  </div>
                ))}
              </div>
            ) : (
              <p className="scenario-assignment-copy secondary">No workload support matrix is available from this controller.</p>
            )}
          </div>
          </details>
          {showBrowserRuntimeSettings && (
            <div className="scenario-browser-panel">
              <div className="scenario-assignment-header">
                <div>
                  <h4>Browser Runtime</h4>
                  <p>Store device defaults with this scenario so queued runs and comparisons carry the same mobile profile.</p>
                </div>
              </div>
              <div className="scenario-browser-grid">
                <label>
                  <span>Device profile</span>
                  <select className="settings-input" value={form.browser_device_profile || ''} onChange={event => setForm({ ...form, browser_device_profile: event.target.value })}>
                    <option value="">Script default</option>
                    <option value="desktop_1080p">desktop_1080p</option>
                    <option value="galaxy_s24">galaxy_s24</option>
                    <option value="pixel_8">pixel_8</option>
                    <option value="ipad_air">ipad_air (WebKit)</option>
                    <option value="iphone_15_pro">iphone_15_pro (WebKit)</option>
                    <option value="iphone_se">iphone_se (WebKit)</option>
                  </select>
                </label>
                <label>
                  <span>Orientation</span>
                  <select className="settings-input" value={form.browser_orientation || ''} onChange={event => setForm({ ...form, browser_orientation: event.target.value })}>
                    <option value="">Script default</option>
                    <option value="portrait">Portrait</option>
                    <option value="landscape">Landscape</option>
                  </select>
                </label>
                <label>
                  <span>Touch mode</span>
                  <select className="settings-input" value={form.browser_touch_mode || ''} onChange={event => setForm({ ...form, browser_touch_mode: event.target.value as ScenarioFormState['browser_touch_mode'] })}>
                    <option value="">Script default</option>
                    <option value="true">Enabled</option>
                    <option value="false">Disabled</option>
                  </select>
                </label>
                <label>
                  <span>Headless</span>
                  <select className="settings-input" value={form.browser_headless || ''} onChange={event => setForm({ ...form, browser_headless: event.target.value as ScenarioFormState['browser_headless'] })}>
                    <option value="">Script default</option>
                    <option value="true">Enabled</option>
                    <option value="false">Disabled</option>
                  </select>
                </label>
              </div>
            </div>
          )}
          <div className="scenario-assignment-panel">
            <div className="scenario-assignment-header">
              <div>
                <h4>Controller / Generator Assignment</h4>
                <p>Use scenario size to decide whether the controller should auto-place the run or restrict it to specific generator hostnames.</p>
              </div>
              {estimateLoading && <span className="scenario-assignment-status">Estimating...</span>}
            </div>

            <div className="scenario-assignment-switcher">
              <button
                type="button"
                className={`mode-chip ${form.assignment_mode === 'auto' ? 'active' : ''}`}
                onClick={() => setForm({ ...form, assignment_mode: 'auto', assignment_targets: [] })}
              >
                Auto Assignment
              </button>
              <button
                type="button"
                className={`mode-chip ${form.assignment_mode === 'specific_hosts' ? 'active' : ''}`}
                onClick={() => setForm({ ...form, assignment_mode: 'specific_hosts' })}
              >
                Specific Hostnames
              </button>
            </div>

            {draftEstimate?.footprint && (
              <div className="scenario-assignment-footprint">
                <div className="scenario-assignment-metric">
                  <span>Footprint</span>
                  <strong>{draftEstimate.footprint.total_vusers} VUsers</strong>
                  <small>{draftEstimate.footprint.duration}</small>
                </div>
                <div className="scenario-assignment-metric">
                  <span>Complexity</span>
                  <strong>{draftEstimate.footprint.complexity_score.toFixed(2)}</strong>
                  <small>derived from current script mix</small>
                </div>
                <div className="scenario-assignment-metric">
                  <span>Estimated Load</span>
                  <strong>{draftEstimate.footprint.estimated_cpu_cores.toFixed(2)} CPU</strong>
                  <small>{Math.round(draftEstimate.footprint.estimated_memory_mb)} MB / {Math.round(draftEstimate.footprint.estimated_network_mbps)} Mbps</small>
                </div>
              </div>
            )}

            {assignmentRecommendation?.summary && (
              <p className="scenario-assignment-copy">{assignmentRecommendation.summary}</p>
            )}

            {form.assignment_mode === 'specific_hosts' ? (
              availableTargets.length > 0 ? (
                <>
                  {form.assignment_targets.length === 0 && (
                    <p className="scenario-assignment-copy secondary">Select at least one hostname to store an explicit scenario default.</p>
                  )}
                  <div className="scenario-assignment-targets">
                    {availableTargets.map((target) => {
                      const checked = form.assignment_targets.includes(target)
                      const recommended = recommendedTargets.includes(target)
                      return (
                        <label key={target} className={`scenario-assignment-target ${checked ? 'selected' : ''}`}>
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={(event) => toggleAssignmentTarget(target, event.target.checked)}
                          />
                          <div>
                            <strong>{target}</strong>
                            <span>{recommended ? 'Recommended for this scenario footprint' : 'Configured on the controller'}</span>
                          </div>
                        </label>
                      )
                    })}
                  </div>
                </>
              ) : (
                <p className="scenario-assignment-copy secondary">No distributed generator hostnames are currently configured on this controller.</p>
              )
            ) : recommendedTargets.length > 0 ? (
              <div className="scenario-assignment-chip-row">
                {recommendedTargets.map((target) => <span key={target} className="scenario-assignment-chip">{target}</span>)}
              </div>
            ) : (
              <p className="scenario-assignment-copy secondary">Auto-assignment will use the controller&apos;s configured generator inventory when distributed execution is enabled.</p>
            )}

            {estimateError && <div className="settings-error-inline">{estimateError}</div>}
            {assignmentRecommendation?.warnings && assignmentRecommendation.warnings.length > 0 && (
              <div className="scenario-assignment-warnings">
                {assignmentRecommendation.warnings.map((warning) => <p key={warning}>{warning}</p>)}
              </div>
            )}
          </div>
          <div className="scenario-builder-actions">
            <button className="btn btn-secondary" onClick={() => setShowForm(false)}>Cancel</button>
            <button className="btn btn-secondary" onClick={validateLoadModel} disabled={loadModelValidationLoading}>{loadModelValidationLoading ? 'Validating...' : 'Validate Load Model'}</button>
            <button className="btn btn-primary" onClick={saveScenario}>Save Scenario</button>
          </div>
        </div>
      )}

      <div className="page-section">
        <div className="library-toolbar">
          <div>
            <h3 className="section-title">Scenario Library ({scenarios.length})</h3>
            <p className="library-toolbar-subtitle">
              {scenarioQuery.trim() ? `${sorted.length} matching scenario${sorted.length === 1 ? '' : 's'}` : 'Search by scenario, script, data pool, release, host, or browser profile.'}
            </p>
          </div>
          <label className="library-search">
            <span>Search scenarios</span>
            <input
              type="search"
              value={scenarioQuery}
              onChange={(event) => setScenarioQuery(event.target.value)}
              placeholder="Find scenarios..."
            />
          </label>
        </div>
        <div className="data-table">
          <table>
            <thead>
              <tr>
                <th onClick={() => handleSort('name')} className="sortable">Scenario <SortIndicator active={sortField === 'name'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('scripts')} className="sortable">Workload <SortIndicator active={sortField === 'scripts'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('associated_releases')} className="sortable">Releases <SortIndicator active={sortField === 'associated_releases'} direction={sortDirection} /></th>
                <th onClick={() => handleSort('last_run_at')} className="sortable">Last Run <SortIndicator active={sortField === 'last_run_at'} direction={sortDirection} /></th>
                <th style={{ textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map(scenario => (
                <Fragment key={scenario.id}>
                  <tr className="clickable-row" onClick={() => toggleScenarioRuns(scenario)}>
                    <td className="cell-name">
                      <div>{scenario.name}</div>
                      <div className="scenario-table-assignment">
                        <span className={`scenario-table-assignment-badge ${scenario.assignment?.mode === 'specific_hosts' ? 'manual' : 'auto'}`}>
                          {scenarioAssignmentSummary(scenario).badge}
                        </span>
                        <span className="scenario-table-assignment-detail">{scenarioAssignmentSummary(scenario).detail}</span>
                      </div>
                      {formatBrowserRuntimeSummary(scenario.runtime_settings?.browser) && (
                        <div className="scenario-table-browser">
                          <span className="scenario-table-browser-badge">Browser</span>
                          <span className="scenario-table-assignment-detail">{formatBrowserRuntimeSummary(scenario.runtime_settings?.browser)}</span>
                        </div>
                      )}
                    </td>
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
                        <span>Created {formatDate(scenario.created_at)}</span>
                      </div>
                    </td>
                    <td className="cell-muted">
                      <div className="scenario-table-workload">
                        <strong>{formatDate(scenario.last_run_at)}</strong>
                        <span>{formatScenarioResultCount(scenario)}</span>
                      </div>
                    </td>
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
                        </>
                      ) : (
                        <span className="cell-muted">Read-only</span>
                      )}
                    </td>
                  </tr>
                  {expandedScenarioId === scenario.id && (
                    <tr className="no-hover">
                      <td colSpan={5} style={{ background: 'var(--bg-primary)' }}>
                        <div style={{ padding: '10px 4px' }}>
                          <div className="cell-muted" style={{ marginBottom: 8 }}>
                            Linked Scenario Runs {loadingRunsFor === scenario.id ? '(loading...)' : `(${(scenarioRuns[scenario.id] || []).length})`}
                          </div>
                          {(scenarioRuns[scenario.id] || []).length === 0 ? (
                            <div className="cell-muted">No runs for this scenario yet.</div>
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
                                  {getSortedInnerRuns(scenarioRuns[scenario.id] || []).map(run => (
                                    <tr key={run.id} className="clickable-row" onClick={(e) => { e.stopPropagation(); onSelectTest(run.id) }}>
                                      <td className="cell-name">{run.name}</td>
                                      <td>{run.status}</td>
                                      <td className="cell-mono">{run.release_id || '—'}</td>
                                      <td className="cell-muted">{formatDateTime(run.start_time || run.created_at)}</td>
                                      <td className="cell-muted">{run.duration}</td>
                                      <td style={{ textAlign: 'right' }}>
                                        <button className="btn btn-sm btn-secondary" onClick={(e) => { e.stopPropagation(); onSelectTest(run.id) }}>Open</button>
                                      </td>
                                    </tr>
                                  ))}
                                </tbody>
                              </table>
                            </div>
                          )}
                        </div>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
              {sorted.length === 0 && (
                <tr>
                  <td colSpan={5} className="cell-muted">
                    {scenarioQuery.trim() ? 'No scenarios match this search.' : 'No scenarios yet. Create a scenario from an existing script.'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

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
