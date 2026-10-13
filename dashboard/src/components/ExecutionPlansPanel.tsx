import DialogLayer from './ux/DialogLayer'
import { useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import { useApplicationApi } from '../hooks/api/useApplicationApi'
import { useCapacityApi } from '../hooks/api/useCapacityApi'
import { useScriptApi } from '../hooks/api/useScriptApi'
import { ApplicationContext } from '../hooks/applicationContext'
import { isLegacyApplicationId, isLegacyReleaseId } from '../utils/legacyPlacement'
import type { CapacityTimeslot } from '../types/capacity'
import { formatDateTime } from '../utils/dateTime'
import { ApiError } from '../hooks/apiClient'
import { useNavigationGuard, useNavigationGuardRunner, type NavigationGuard } from '../hooks/NavigationGuardProvider'
import { useModalDialog } from '../hooks/useModalDialog'
import {
  ExecutionPlan,
  ExecutionPlanEntry,
  ExecutionPlanRun,
  ExecutionPlanRunResponse,
  ScenarioDataPoolBinding,
} from '../types'
import type { ExecutionPlanListItem, ExecutionPlanOverlap, ExecutionPlanOverlapPolicy, ExecutionPlanRunEntry, ExecutionPlanRunEntryView, ExecutionPlanRunView } from '../types/executionPlans'
import {
  OVERLAP_POLICY_OPTIONS,
  effectiveOverlapPolicy,
  planCardState,
  planEntryStatusLabel,
  planHeldState,
  type PlanCardState,
  planLastRunSentence,
  planReleaseName,
  planReleaseSentence,
  planRunOutcomeLabel,
  planRunOutcomeTone,
  planRunStopping,
  planRunTimingSentence,
} from './executionPlanActivityModel'
import ConfirmDestructiveModal from './ConfirmDestructiveModal'
import { confirmAction } from './ux/confirmAction'
import ExecutionPlanApprovalsModal from './ExecutionPlanApprovalsModal'
import ExecutionPlanSchedulesModal from './ExecutionPlanSchedulesModal'
import { dialogBackdropProps } from './dialogBackdropProps'
import { listAllExecutionPlans } from './schedulingCatalogModel'
import './parityPanels.css'
import LoadingState from './ux/LoadingState'
import EmptyState from './ux/EmptyState'
import PlanEntryDataNote from './PlanEntryDataNote'
import { liveBindingSummary } from './scenarioDataBindingModel'
import RowActions from './ux/RowActions'
import { scenarioNameCounts, scenarioNameKey } from './scenarioNames'

interface Props {
  projectId?: string
  canManage?: boolean
  canAdmin?: boolean
  scenarioOptions?: { id: string; name: string; ref_key?: string; data_pools?: ScenarioDataPoolBinding[] }[]
  onOpenTest?: (testId: string) => void
  // When set, opens a new-plan draft pre-seeded with this scenario as the first entry.
  seedScenarioId?: string
  onSeedConsumed?: () => void
}

// An override is a read-only snapshot chosen here, or a live binding saved
// through the API, which the editor keeps as it was saved (binding).
type DataPoolDraft = { pool_id: string; alias: string; binding?: ScenarioDataPoolBinding }

type EntryDraft = {
  scenario_id: string
  scenario_name: string
  enabled: boolean
  failure_policy: string
  timeslot_id: string
  data_pools: DataPoolDraft[]
}

type PlanDraft = {
  id?: string
  revision?: number
  name: string
  run_mode: string
  application_id: string
  release_id: string
  overlap_policy: ExecutionPlanOverlapPolicy
  entries: EntryDraft[]
	baseline: string
}

type PlanDraftFields = Omit<PlanDraft, 'baseline'>

function planDraftSnapshot(draft: PlanDraftFields | PlanDraft): string {
	const fields = { ...draft } as Partial<PlanDraft>
	delete fields.baseline
	return JSON.stringify(fields)
}

function withPlanDraftBaseline(fields: PlanDraftFields, baseline: PlanDraftFields | PlanDraft = fields): PlanDraft {
	return { ...fields, baseline: planDraftSnapshot(baseline) }
}

const emptyEntry: EntryDraft = {
  scenario_id: '',
  scenario_name: '',
  enabled: true,
  failure_policy: 'halt',
  timeslot_id: '',
  data_pools: [],
}

// A new plan files its runs where the workspace is, as New run does.
function emptyDraft(workspace: { applicationId?: string; releaseId?: string } = {}): PlanDraft {
	return withPlanDraftBaseline({
		name: '',
		run_mode: 'parallel',
		application_id: workspace.releaseId ? workspace.applicationId || '' : '',
		release_id: workspace.releaseId || '',
		overlap_policy: 'skip',
		entries: [{ ...emptyEntry }],
	})
}

const RUN_TERMINAL = ['completed', 'halted', 'failed']

const RUN_MODE_OPTIONS = [
  { value: 'parallel', label: 'In parallel' },
  { value: 'sequential', label: 'One after another' },
]

function runModeLabel(mode: string): string {
  return RUN_MODE_OPTIONS.find(option => option.value === mode)?.label || mode
}

// What an entry's failure policy does, as the editor offers it.
const FAILURE_POLICY_OPTIONS = [
  { value: 'halt', label: 'Stop the plan', summary: 'stops the plan if it fails' },
  { value: 'skip', label: 'Continue with the next', summary: 'continues if it fails' },
]

// A card lists this many entries, then says how many more there are.
const CARD_ENTRY_LIMIT = 5

// A run view from a current controller says whether its load is still running;
// an older run record only has the plan run's own status.
function runIsLive(run: ExecutionPlanRun | ExecutionPlanRunView): boolean {
  if ('active' in run && typeof run.active === 'boolean') return run.active
  return !RUN_TERMINAL.includes(run.status)
}

function isRunView(run: ExecutionPlanRun | ExecutionPlanRunView): run is ExecutionPlanRunView {
  return 'outcome' in run && typeof (run as ExecutionPlanRunView).outcome === 'string'
}

// How often the card and progress view refresh while a plan run is in progress.
const PLAN_ACTIVITY_POLL_MS = 5000
const PLAN_WAITING_POLL_MS = 30000
const PLAN_RUN_POLL_MS = 1500

function runResultPill(status: string): string {
  switch (status) {
    case 'created':
      return 'pp-pill pp-pill-success'
    case 'failed':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

function runStatusPill(status: string): string {
  switch (status) {
    case 'completed':
      return 'pp-pill pp-pill-success'
    case 'running':
      return 'pp-pill pp-pill-info'
    case 'halted':
    case 'failed':
      return 'pp-pill pp-pill-danger'
    default:
      return 'pp-pill pp-pill-muted'
  }
}

// Created and failed entry counts; older controllers may omit the totals.
function planRunCounts(res: ExecutionPlanRunResponse) {
  return {
    created: res.created_count ?? res.results.filter(result => result.status === 'created').length,
    failed: res.failed_count ?? res.results.filter(result => result.status === 'failed').length,
  }
}

// A parallel plan run "completes" once every entry has been started, not when
// the load finishes; name that state instead of showing "completed".
function planRunStatusLabel(run: ExecutionPlanRun | ExecutionPlanRunView): string {
  if (isRunView(run)) return planRunOutcomeLabel(run)
  const status = (run.status || '').toLowerCase()
  if (run.run_mode === 'parallel' && (status === 'completed' || status === 'complete')) return 'All runs started'
  return run.status
}

function planRunPillClass(run: ExecutionPlanRun | ExecutionPlanRunView): string {
  return isRunView(run) ? `pp-pill pp-pill-label pp-pill-${planRunOutcomeTone(run)}` : runStatusPill(run.status)
}

// An entry's state in plain words, from its run's live state when known.
function entryPill(entry: ExecutionPlanRunEntry | ExecutionPlanRunEntryView): { label: string; className: string } {
  const status = planEntryStatusLabel(entry)
  return { label: status.label, className: `pp-pill pp-pill-label pp-pill-${status.tone}` }
}

export default function ExecutionPlansPanel({ projectId, canManage = false, canAdmin = false, scenarioOptions, onOpenTest, seedScenarioId, onSeedConsumed }: Props) {
  const api = useExecutionPlansApi()
  const applicationApi = useApplicationApi()
  const { runGuarded } = useNavigationGuardRunner()
  // The workspace's application and release are where a new plan files its runs.
  const workspace = useContext(ApplicationContext)
  const workspaceApplication = workspace?.activeApplication
  const workspaceApplicationId = workspaceApplication && !isLegacyApplicationId(workspaceApplication.id) && (!projectId || !workspaceApplication.project_id || workspaceApplication.project_id === projectId) ? workspaceApplication.id : undefined
  const workspaceReleaseId = workspaceApplicationId && workspace?.activeRelease?.application_id === workspaceApplicationId ? workspace.activeRelease.id : undefined
  const newDraft = useCallback(() => emptyDraft({ applicationId: workspaceApplicationId, releaseId: workspaceReleaseId }), [workspaceApplicationId, workspaceReleaseId])
  const [applications, setApplications] = useState<{ id: string; name: string }[] | null>(null)
  // Bumped to re-read applications without blanking the ones shown meanwhile.
  const [applicationsEpoch, setApplicationsEpoch] = useState(0)
  const fetchedApplicationsEpochRef = useRef(-1)
  const [releasesByApplication, setReleasesByApplication] = useState<Record<string, { id: string; name: string }[]>>({})
  const requestedReleaseListsRef = useRef(new Set<string>())
  const [plans, setPlans] = useState<ExecutionPlanListItem[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [draft, setDraft] = useState<PlanDraft | null>(null)
  // Timeslots and data pools are chosen by name, not typed as IDs. When they
  // cannot be listed, the editor falls back to ID fields.
  const { listTimeslots } = useCapacityApi()
  const { listDataPools } = useScriptApi()
  const [planReferences, setPlanReferences] = useState<{ timeslots: CapacityTimeslot[] | null; pools: { id: string; name: string }[] | null }>({ timeslots: null, pools: null })
  const draftOpen = draft !== null
  useEffect(() => {
    if (!draftOpen || !projectId) return undefined
    let cancelled = false
    void Promise.allSettled([listTimeslots({ project_id: projectId, limit: 200 }), listDataPools(projectId)]).then(([timeslots, pools]) => {
      if (cancelled) return
      setPlanReferences({
        timeslots: timeslots.status === 'fulfilled' && timeslots.value ? timeslots.value.timeslots || [] : null,
        pools: pools.status === 'fulfilled' && pools.value ? (pools.value.data_pools || []).filter(pool => !pool.archived_at).map(pool => ({ id: pool.id, name: pool.name })) : null,
      })
    })
    return () => { cancelled = true }
  }, [draftOpen, listDataPools, listTimeslots, projectId])
  const [deleteTarget, setDeleteTarget] = useState<ExecutionPlan | null>(null)
  const [runResult, setRunResult] = useState<{ plan: string; res: ExecutionPlanRunResponse; names: Record<number, string> } | null>(null)
  const [planRun, setPlanRun] = useState<{ planId: string; planName: string; run: ExecutionPlanRun | ExecutionPlanRunView } | null>(null)
  const [planRunUpdatedAt, setPlanRunUpdatedAt] = useState<number | null>(null)
  const [stoppingRunId, setStoppingRunId] = useState<string | null>(null)
  const [clock, setClock] = useState(() => Date.now())
  const [schedulesTarget, setSchedulesTarget] = useState<ExecutionPlan | null>(null)
  const [approvalsTarget, setApprovalsTarget] = useState<ExecutionPlanListItem | null>(null)
  // Why the approvals dialog opened, e.g. a run just held for approval.
  const [approvalsNotice, setApprovalsNotice] = useState<string | null>(null)
  const openApprovals = useCallback((plan: ExecutionPlanListItem, notice: string | null = null) => {
    setApprovalsNotice(notice)
    setApprovalsTarget(plan)
  }, [])
  const closeApprovals = useCallback(() => {
    setApprovalsTarget(null)
    setApprovalsNotice(null)
  }, [])
  const [busy, setBusy] = useState(false)
  const scopeRef = useRef({ projectId, generation: 0 })
  const loadGenerationRef = useRef(0)
  if (scopeRef.current.projectId !== projectId) {
    scopeRef.current = { projectId, generation: scopeRef.current.generation + 1 }
  }
  const isCurrentScope = useCallback((scope: { projectId?: string; generation: number }) => (
    scopeRef.current.projectId === scope.projectId && scopeRef.current.generation === scope.generation
  ), [])
  const draftDialogRef = useRef<HTMLDivElement>(null)
  const resultDialogRef = useRef<HTMLDivElement>(null)
  const progressDialogRef = useRef<HTMLDivElement>(null)
  // The result and progress dialogs open on their one way out, Done.
  const resultDoneRef = useRef<HTMLButtonElement>(null)
  const progressDoneRef = useRef<HTMLButtonElement>(null)
	const discardDraft = useCallback(() => {
		setDraft(null)
		setError(null)
	}, [])
	const closeDraft = useCallback(() => runGuarded(discardDraft), [discardDraft, runGuarded])
  const closeRunResult = useCallback(() => setRunResult(null), [])
  const runResultCounts = runResult ? planRunCounts(runResult.res) : { created: 0, failed: 0 }
  const closePlanRun = useCallback(() => setPlanRun(null), [])

  // Entries are named by their scenario as the project has it now; two
  // scenarios with one name are told apart by their reference, as in the library.
  const scenarioNames = useMemo(() => {
    const counts = scenarioNameCounts(scenarioOptions || [])
    return new Map((scenarioOptions || []).map(option => {
      const duplicate = (counts.get(scenarioNameKey(option.name)) || 0) > 1
      return [option.id, { name: duplicate ? `${option.name} (${option.ref_key || option.id})` : option.name, duplicate }]
    }))
  }, [scenarioOptions])
  const entryScenarioName = useCallback((entry: { scenario_id: string; scenario_name?: string }) => (
    scenarioNames.get(entry.scenario_id)?.name || entry.scenario_name || entry.scenario_id
  ), [scenarioNames])

	useModalDialog({ dialogRef: draftDialogRef, enabled: Boolean(draft), onClose: busy ? undefined : closeDraft })
  useModalDialog({ dialogRef: resultDialogRef, enabled: Boolean(runResult), initialFocusRef: resultDoneRef, onClose: closeRunResult })
  useModalDialog({ dialogRef: progressDialogRef, enabled: Boolean(planRun), initialFocusRef: progressDoneRef, onClose: closePlanRun })

  useLayoutEffect(() => {
    loadGenerationRef.current += 1
    requestedReleaseListsRef.current = new Set()
    setApplications(null)
    setReleasesByApplication({})
    setPlans([])
    setLoading(false)
    setError(null)
    setDraft(null)
    setDeleteTarget(null)
    setRunResult(null)
    setPlanRun(null)
    setSchedulesTarget(null)
    setApprovalsTarget(null)
    setApprovalsNotice(null)
    setBusy(false)
  }, [projectId])

  // A quiet load refreshes the cards' running and waiting state in place.
  const load = useCallback(async (quiet = false) => {
    const scope = { ...scopeRef.current }
    const requestGeneration = ++loadGenerationRef.current
    if (!projectId) {
      if (isCurrentScope(scope)) setPlans([])
      return
    }
    if (!quiet) {
      setLoading(true)
      setError(null)
    }
    try {
      const nextPlans = await listAllExecutionPlans(params => api.listExecutionPlans({ ...params, include_activity: 'true' }), projectId)
      if (!isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current) return
      setPlans(nextPlans)
    } catch (err) {
      if (!isCurrentScope(scope) || requestGeneration !== loadGenerationRef.current || quiet) return
      setError(err instanceof ApiError ? err.message : 'Failed to load execution plans.')
    } finally {
      // The latest load clears loading, quiet or not: a poll can supersede a
      // load the user started, which then leaves its spinner to the poll.
      if (isCurrentScope(scope) && requestGeneration === loadGenerationRef.current) setLoading(false)
    }
  }, [api, isCurrentScope, projectId])

  useEffect(() => {
    void load()
  }, [load])

  // Keep the running and waiting states current while any plan has one: often
  // while load runs, rarely while a run only waits for someone to act.
  const anyPlanRunning = plans.some(plan => Boolean(plan.activity?.active_run))
  const anyPlanWaiting = plans.some(plan => Boolean(plan.activity?.pending_approval || plan.activity?.waiting?.length))
  const activityPollMs = anyPlanRunning ? PLAN_ACTIVITY_POLL_MS : anyPlanWaiting ? PLAN_WAITING_POLL_MS : 0
  useEffect(() => {
    if (!activityPollMs) return undefined
    const timer = setInterval(() => {
      setClock(Date.now())
      void load(true)
    }, activityPollMs)
    return () => clearInterval(timer)
  }, [activityPollMs, load])

  // Application and release names for the editor and for "Runs are filed under".
  const loadReleases = useCallback((applicationId: string) => {
    if (!applicationId || requestedReleaseListsRef.current.has(applicationId)) return
    requestedReleaseListsRef.current.add(applicationId)
    const scope = { ...scopeRef.current }
    void applicationApi.listReleases(applicationId).then(response => {
      if (!isCurrentScope(scope)) return
      setReleasesByApplication(current => ({ ...current, [applicationId]: (response.releases || []).filter(release => !release.archived_at && !isLegacyReleaseId(release.id)).map(release => ({ id: release.id, name: release.name })) }))
    }).catch(() => {
      requestedReleaseListsRef.current.delete(applicationId)
    })
  }, [applicationApi, isCurrentScope])
  const planApplicationIds = useMemo(() => [...new Set(plans.map(plan => plan.application_id || '').filter(Boolean))].join('\n'), [plans])
  useEffect(() => {
    planApplicationIds.split('\n').filter(Boolean).forEach(loadReleases)
  }, [loadReleases, planApplicationIds])
  useEffect(() => {
    if (!projectId || (!draftOpen && !planApplicationIds)) return undefined
    if (applications !== null && fetchedApplicationsEpochRef.current === applicationsEpoch) return undefined
    fetchedApplicationsEpochRef.current = applicationsEpoch
    let cancelled = false
    let settled = false
    void applicationApi.listApplications(projectId).then(response => {
      settled = true
      // The legacy bucket is where a plan without a release files; it is not a choice.
      if (!cancelled) setApplications((response.applications || []).filter(application => !isLegacyApplicationId(application.id)).map(application => ({ id: application.id, name: application.name })))
    }).catch(() => {
      settled = true
      if (!cancelled) setApplications(current => current ?? [])
    })
    return () => {
      cancelled = true
      // A read cut short is read again on the next run of this effect.
      if (!settled) fetchedApplicationsEpochRef.current = -1
    }
  }, [applicationApi, applications, applicationsEpoch, draftOpen, planApplicationIds, projectId])
  // Each time an editor opens, re-read the lists so a release created since
  // the page loaded can be chosen.
  const schedulesApplicationId = schedulesTarget?.application_id || ''
  useEffect(() => {
    if (!draftOpen && !schedulesApplicationId) return
    requestedReleaseListsRef.current.clear()
    if (draftOpen) setApplicationsEpoch(epoch => epoch + 1)
    if (schedulesApplicationId) loadReleases(schedulesApplicationId)
  }, [draftOpen, loadReleases, schedulesApplicationId])
  const draftApplicationId = draft?.application_id || ''
  useEffect(() => {
    if (draftApplicationId) loadReleases(draftApplicationId)
  }, [draftApplicationId, loadReleases])
  const releaseNames = useMemo(() => ({
    applications: Object.fromEntries((applications || []).map(application => [application.id, application.name])),
    releases: Object.fromEntries(Object.values(releasesByApplication).flat().map(release => [release.id, release.name])),
  }), [applications, releasesByApplication])

  // When a scenario is handed in from the library, open a fresh plan draft seeded with it.
  useEffect(() => {
    if (!seedScenarioId || !canManage) return
    const opt = scenarioOptions?.find(s => s.id === seedScenarioId)
		const blank = newDraft()
		const seeded: PlanDraftFields = {
      name: opt?.name ? `${opt.name} plan` : '',
      run_mode: 'parallel',
      application_id: blank.application_id,
      release_id: blank.release_id,
      overlap_policy: blank.overlap_policy,
      entries: [{ ...emptyEntry, scenario_id: seedScenarioId, scenario_name: opt?.name || '' }],
		}
		setDraft(withPlanDraftBaseline(seeded, blank))
    onSeedConsumed?.()
  }, [seedScenarioId, canManage, scenarioOptions, onSeedConsumed, projectId, newDraft])

  // Poll an open plan run until its load has finished, sequential or parallel.
  const planRunLive = Boolean(planRun && runIsLive(planRun.run))
  const planRunKey = planRun ? `${planRun.planId}/${planRun.run.id}` : ''
  useEffect(() => {
    if (!planRunLive || !planRunKey) return undefined
    const [planId, runId] = planRunKey.split('/')
    const scope = { ...scopeRef.current }
    const timer = setInterval(async () => {
      try {
        const run = await api.getExecutionPlanRun(planId, runId)
        if (!isCurrentScope(scope)) return
        setPlanRun(prev => (prev && prev.run.id === run.id ? { ...prev, run } : prev))
        setPlanRunUpdatedAt(Date.now())
        setClock(Date.now())
      } catch {
        /* transient errors are fine — keep polling */
      }
    }, PLAN_RUN_POLL_MS)
    return () => clearInterval(timer)
  }, [api, isCurrentScope, planRunKey, planRunLive])

  const openPlanRun = useCallback(async (plan: ExecutionPlan, run: ExecutionPlanRunView) => {
    const scope = { ...scopeRef.current }
    setPlanRun({ planId: plan.id, planName: plan.name, run })
    setPlanRunUpdatedAt(Date.now())
    try {
      const latest = await api.getExecutionPlanRun(plan.id, run.id)
      if (isCurrentScope(scope)) {
        setPlanRun(prev => (prev && prev.run.id === latest.id ? { ...prev, run: latest } : prev))
        setPlanRunUpdatedAt(Date.now())
      }
    } catch {
      /* the card's snapshot stays until the next poll */
    }
  }, [api, isCurrentScope])

  // Opened from a card, the dialog starts without an earlier action's error.
  const viewPlanRun = (plan: ExecutionPlan, run: ExecutionPlanRunView) => {
    setError(null)
    void openPlanRun(plan, run)
  }

  const stopPlanRun = useCallback(async (planId: string, planName: string, runId: string) => {
    if (!await confirmAction(`Stop “${planName}”? Runs it started are stopped, and entries that have not started yet are skipped.`, { title: 'Stop plan', confirmLabel: 'Stop plan', destructive: true })) return
    const scope = { ...scopeRef.current }
    setStoppingRunId(runId)
    setError(null)
    try {
      const response = await api.stopExecutionPlanRun(planId, runId)
      if (!isCurrentScope(scope)) return
      if (response.run) {
        const stopped = response.run
        setPlanRun(prev => (prev && prev.run.id === stopped.id ? { ...prev, run: stopped } : prev))
        setPlanRunUpdatedAt(Date.now())
      }
      if (response.error) setError(`Some runs could not be stopped: ${response.error}`)
      await load(true)
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to stop the plan run.')
    } finally {
      if (isCurrentScope(scope)) setStoppingRunId(null)
    }
  }, [api, isCurrentScope, load])

  const draftToEntries = (entries: EntryDraft[]): ExecutionPlanEntry[] =>
    entries
      .filter(e => e.scenario_id.trim())
      .map((e, index) => {
        const pools: ScenarioDataPoolBinding[] = e.data_pools
          .filter(dp => dp.binding || (dp.pool_id.trim() && dp.alias.trim()))
          .map(dp => dp.binding ? { ...dp.binding } : {
            pool_id: dp.pool_id.trim(),
            alias: dp.alias.trim(),
            access_mode: 'read_only',
            selection_strategy: 'sequential',
          })
        return {
          order: index,
          scenario_id: e.scenario_id.trim(),
          scenario_name: e.scenario_name.trim() || undefined,
          enabled: e.enabled,
          failure_policy: e.failure_policy,
          timeslot_id: e.timeslot_id.trim() || undefined,
          data_pools: pools.length > 0 ? pools : undefined,
        }
      })

	const submitDraft = useCallback(async (): Promise<boolean> => {
		if (!draft || !projectId) return false
    const scope = { ...scopeRef.current }
    const entries = draftToEntries(draft.entries)
    setBusy(true)
    setError(null)
    try {
			let saved: ExecutionPlan
      const placement = { application_id: draft.release_id ? draft.application_id : '', release_id: draft.release_id, overlap_policy: draft.overlap_policy }
      if (draft.id) {
				saved = await api.updateExecutionPlan(draft.id, { revision: draft.revision, name: draft.name.trim(), run_mode: draft.run_mode, entries, ...placement })
      } else {
				saved = await api.createExecutionPlan({ project_id: projectId, name: draft.name.trim(), run_mode: draft.run_mode, entries, ...placement })
      }
			if (!isCurrentScope(scope)) return false
			setPlans(current => [...current.filter(plan => plan.id !== saved.id), saved])
      setDraft(null)
      await load()
			return true
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to save execution plan.')
			return false
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
  }, [api, draft, isCurrentScope, load, projectId])

  const runPlan = useCallback(
    async (plan: ExecutionPlanListItem, options: { approvalReady?: boolean } = {}) => {
      // Running a plan starts real load immediately; say what will start first.
      const enabled = plan.entries.filter(entry => entry.enabled !== false)
      const names = enabled.map(entryScenarioName).slice(0, 5).join(', ')
      const how = plan.run_mode === 'parallel' ? 'all at the same time' : 'one after another, each after the previous run finishes'
      const alongside = plan.activity?.active_run && effectiveOverlapPolicy(plan.overlap_policy) === 'allow'
        ? ' A run of this plan is still in progress; this one runs alongside it, and quota approval counts both.'
        : ''
      // A plan over the project's VUser limit runs only with a project admin's
      // approval: say so before asking, and show a request already waiting
      // rather than asking again.
      const quota = plan.activity?.quota
      const approvalReady = Boolean(options.approvalReady || quota?.ready_approval_id)
      const overLimit = !quota
        ? ''
        : quota.active_vusers
          ? `Its peak of ${quota.peak_vusers} VUsers, with the ${quota.active_vusers} VUsers of its runs already in progress, is over the project limit of ${quota.max_vusers}`
          : `Its peak of ${quota.peak_vusers} VUsers is over the project limit of ${quota.max_vusers}`
      // The card's request may have been decided since it was listed: the
      // dialog shows where it stands.
      if (quota && !approvalReady && plan.activity?.pending_approval) {
        setError(null)
        openApprovals(plan, `${overLimit}, so its run needs a project admin's approval. Nothing has started.`)
        return
      }
      const runs = `${enabled.length} scenario run${enabled.length === 1 ? '' : 's'} ${how}`
      const targets = `${names}${enabled.length > 5 ? ', …' : ''}`
      const confirmed = quota && !approvalReady
        ? await confirmAction(`“${plan.name}” starts ${runs}: ${targets}. ${overLimit}, so this asks a project admin to approve it first. Nothing starts until it is approved.${canAdmin ? ' You can approve it next.' : ''}${alongside}`, { title: 'Request approval to run', confirmLabel: 'Request approval', destructive: false })
        : await confirmAction(`Start “${plan.name}”? This starts ${runs} and sends real load to their targets: ${targets}.${quota ? ` ${overLimit}; the approval granted for it lets this run start.` : ''}${alongside}`, { title: 'Start execution plan', confirmLabel: 'Start runs', destructive: false })
      if (!confirmed) return
      const scope = { ...scopeRef.current }
      setBusy(true)
      setError(null)
      try {
        const res = await api.runExecutionPlan(plan.id)
        if (!isCurrentScope(scope)) return
        if (res.run_id) {
          // Sequential: dependency-gated run — show live per-entry progress.
          const seeded: ExecutionPlanRun = {
            id: res.run_id,
            plan_id: plan.id,
            project_id: plan.project_id,
            run_mode: res.run_mode,
            status: res.status || 'running',
            entries: res.results.map(r => ({
              order: r.order,
              scenario_id: r.scenario_id,
              test_id: r.test_id,
              status: r.status,
              detail: r.detail,
            })),
            created_at: res.materialized_at,
            updated_at: res.materialized_at,
          }
          setPlanRun({ planId: plan.id, planName: plan.name, run: seeded })
          try {
            const run = await api.getExecutionPlanRun(plan.id, res.run_id)
            if (isCurrentScope(scope)) setPlanRun({ planId: plan.id, planName: plan.name, run })
          } catch {
            /* keep the seeded snapshot if the run can't be re-read yet */
          }
        } else {
          setRunResult({ plan: plan.name, res, names: Object.fromEntries(plan.entries.map(entry => [entry.order, entryScenarioName(entry)])) })
        }
        await load()
      } catch (err) {
        if (!isCurrentScope(scope)) return
        // A run that exceeds the project VUser quota is blocked (HTTP 409) until
        // a project admin approves it. Open the approvals dialog, which says
        // why, instead of showing a bare error so the gate is actionable.
        const conflict = err instanceof ApiError && err.status === 409 ? err.payload as { status?: string; overlap?: ExecutionPlanOverlap } | undefined : undefined
        if (conflict?.status === 'blocked') {
          openApprovals(plan, canAdmin
            ? 'Nothing has started: this run is over the project\'s VUser limit and needs a project admin\'s approval. Approve the request below, then run it.'
            : 'Nothing has started: this run is over the project\'s VUser limit and waits for a project admin\'s approval.')
          // The card shows the request now waiting (and keeps it current).
          await load(true)
        } else if (conflict?.status === 'plan_running') {
          // The overlap policy kept a second run from starting: show the one in progress.
          setError(conflict.overlap?.message || (err instanceof ApiError ? err.message : 'This plan is already running.'))
          const active = plan.activity?.active_run
          if (active && (!conflict.overlap?.active_run_id || active.id === conflict.overlap.active_run_id)) void openPlanRun(plan, active)
          await load(true)
        } else {
          setError(err instanceof ApiError ? err.message : 'Failed to run execution plan.')
        }
      } finally {
        if (isCurrentScope(scope)) setBusy(false)
      }
    },
    [api, canAdmin, entryScenarioName, isCurrentScope, load, openApprovals, openPlanRun],
  )

  const confirmDelete = useCallback(async () => {
    if (!deleteTarget) return
    const scope = { ...scopeRef.current }
    setBusy(true)
    setError(null)
    try {
      await api.deleteExecutionPlan(deleteTarget.id)
      if (!isCurrentScope(scope)) return
			setPlans(current => current.filter(plan => plan.id !== deleteTarget.id))
      setDeleteTarget(null)
      await load()
    } catch (err) {
      if (isCurrentScope(scope)) setError(err instanceof ApiError ? err.message : 'Failed to delete execution plan.')
    } finally {
      if (isCurrentScope(scope)) setBusy(false)
    }
  }, [api, deleteTarget, isCurrentScope, load])

  const startEdit = (plan: ExecutionPlan) => {
		const fields: PlanDraftFields = {
      id: plan.id,
      revision: plan.revision,
      name: plan.name,
      run_mode: plan.run_mode,
      application_id: plan.release_id ? plan.application_id || '' : '',
      release_id: plan.release_id || '',
      overlap_policy: effectiveOverlapPolicy(plan.overlap_policy),
      entries:
        plan.entries.length > 0
          ? plan.entries.map(e => ({
              scenario_id: e.scenario_id,
              scenario_name: e.scenario_name || '',
              enabled: e.enabled !== false,
              failure_policy: e.failure_policy || 'halt',
              timeslot_id: e.timeslot_id || '',
              data_pools: (e.data_pools || []).map(dp => ({ pool_id: dp.pool_id, alias: dp.alias, ...(dp.access_mode === 'live' ? { binding: dp } : {}) })),
            }))
          : [{ ...emptyEntry }],
		}
		setError(null)
		setDraft(withPlanDraftBaseline(fields))
  }

	const draftDirty = Boolean(draft && planDraftSnapshot(draft) !== draft.baseline)
	const draftDirtyRef = useRef(draftDirty)
	draftDirtyRef.current = draftDirty
	const submitDraftRef = useRef(submitDraft)
	submitDraftRef.current = submitDraft
	const draftGuard = useMemo<NavigationGuard>(() => ({
		id: 'execution-plan-draft',
		label: 'Execution plan editor',
		isDirty: () => draftDirtyRef.current,
		save: () => submitDraftRef.current(),
		discard: discardDraft,
	}), [discardDraft])
	useNavigationGuard(draftGuard, draftDirty)

  const updateEntry = (index: number, patch: Partial<EntryDraft>) =>
    setDraft(d => (d ? { ...d, entries: d.entries.map((e, i) => (i === index ? { ...e, ...patch } : e)) } : d))

  const updatePool = (entryIdx: number, poolIdx: number, patch: Partial<DataPoolDraft>) =>
    setDraft(d =>
      d
        ? {
            ...d,
            entries: d.entries.map((e, i) =>
              i === entryIdx ? { ...e, data_pools: e.data_pools.map((p, j) => (j === poolIdx ? { ...p, ...patch } : p)) } : e,
            ),
          }
        : d,
    )

  const addPool = (entryIdx: number) =>
    setDraft(d =>
      d
        ? { ...d, entries: d.entries.map((e, i) => (i === entryIdx ? { ...e, data_pools: [...e.data_pools, { pool_id: '', alias: '' }] } : e)) }
        : d,
    )

  const removePool = (entryIdx: number, poolIdx: number) =>
    setDraft(d =>
      d
        ? { ...d, entries: d.entries.map((e, i) => (i === entryIdx ? { ...e, data_pools: e.data_pools.filter((_, j) => j !== poolIdx) } : e)) }
        : d,
    )

  const moveEntry = (index: number, dir: -1 | 1) =>
    setDraft(d => {
      if (!d) return d
      const next = [...d.entries]
      const target = index + dir
      if (target < 0 || target >= next.length) return d
      ;[next[index], next[target]] = [next[target], next[index]]
      return { ...d, entries: next }
    })

  function renderPlanState(plan: ExecutionPlanListItem) {
    const state = planCardState(plan.activity, clock)
    if (state.kind === 'running') {
      return (
        <>
          {renderRunningState(plan, state)}
          {renderHeldState(plan, planHeldState(plan.activity, clock))}
        </>
      )
    }
    return renderHeldState(plan, state)
  }

  function renderRunningState(plan: ExecutionPlanListItem, state: Extract<PlanCardState, { kind: 'running' }>) {
    return (
      <div className="pp-row-meta pp-plan-state">
        <span className="pp-pill pp-pill-label pp-pill-info">{state.stopping ? 'Stopping' : 'Running'}</span>
        <span>{state.summary}{state.extraRuns > 0 ? ` · ${state.extraRuns} more run${state.extraRuns === 1 ? '' : 's'} of this plan in progress` : ''}</span>
        {canManage && effectiveOverlapPolicy(plan.overlap_policy) !== 'allow' ? (
          <span id={`plan-run-blocked-${plan.id}`}>Run is unavailable until this run ends.</span>
        ) : null}
        <button type="button" className="btn btn-ghost btn-sm" onClick={() => viewPlanRun(plan, state.run)}>View progress</button>
        {canManage && !state.stopping ? (
          <button type="button" className="btn btn-ghost btn-sm" disabled={stoppingRunId === state.run.id} onClick={() => void stopPlanRun(plan.id, plan.name, state.run.id)}>
            {stoppingRunId === state.run.id ? 'Stopping…' : 'Stop plan'}
          </button>
        ) : null}
      </div>
    )
  }

  function renderHeldState(plan: ExecutionPlanListItem, state: PlanCardState) {
    const lastRun = plan.activity?.last_run
    const lastRunLine = planLastRunSentence(plan.activity)
    if (state.kind === 'running') return null
    if (state.kind === 'idle') {
      return lastRunLine && lastRun ? (
        <div className="pp-row-meta pp-plan-state">
          <span>{lastRunLine}</span>
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => viewPlanRun(plan, lastRun)}>View last run</button>
        </div>
      ) : null
    }
    const pill = state.kind === 'waiting_approval'
      ? { label: 'Waiting for approval', className: 'pp-pill pp-pill-label pp-pill-warning' }
      : state.kind === 'queued'
        ? { label: 'Queued', className: 'pp-pill pp-pill-label pp-pill-info' }
        : { label: 'Needs a decision', className: 'pp-pill pp-pill-label pp-pill-warning' }
    return (
      <>
        <div className="pp-row-meta pp-plan-state">
          <span className={pill.className}>{pill.label}</span>
          <span>{state.summary}</span>
          {state.kind === 'waiting_approval' ? (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => openApprovals(plan)}>
              {canAdmin ? 'Review approval' : 'View approval'}
            </button>
          ) : null}
          {state.kind === 'decision' && canManage ? (
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => setSchedulesTarget(plan)}>Open schedules</button>
          ) : null}
        </div>
        {lastRunLine && lastRun && !plan.activity?.active_run ? (
          <div className="pp-row-meta">
            <span>{lastRunLine}</span>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => viewPlanRun(plan, lastRun)}>View last run</button>
          </div>
        ) : null}
      </>
    )
  }

  const draftValid = useMemo(
    () => Boolean(draft && draft.name.trim() && draft.entries.some(e => e.scenario_id.trim())),
    [draft],
  )

  // Approvals reads the plan as last listed, so a granted approval offers Run
  // now once the list says the next run uses it; Run now waits where Run does.
  const approvalsPlan = approvalsTarget ? plans.find(candidate => candidate.id === approvalsTarget.id) || approvalsTarget : null
  const runNowBlocked = busy
    ? 'Another plan action is still in progress.'
    : approvalsPlan?.activity?.active_run && effectiveOverlapPolicy(approvalsPlan.overlap_policy) !== 'allow'
      ? 'A run of this plan is in progress; Run now is available when it ends.'
      : undefined

  return (
    <div className="pp-panel">
      <div className="pp-toolbar">
        <span className="pp-toolbar-title">
          {plans.length} execution plan{plans.length === 1 ? '' : 's'}
        </span>
        {canManage && (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={!projectId}
            title={projectId ? undefined : 'Select a project to create an execution plan'}
            onClick={() => {
              setError(null)
              setDraft(newDraft())
            }}
          >
            New execution plan
          </button>
        )}
      </div>

      {/* An open editor or progress dialog shows the error itself; behind its overlay it would go unseen. */}
      {error && !draft && !planRun && <div className="pp-error" role="alert">{error}</div>}

      {!projectId ? (
        <EmptyState title="No project selected." hint="Select a project to view its execution plans." />
      ) : loading ? (
        <LoadingState what="execution plans" className="pp-loading" />
      ) : plans.length === 0 ? (
        <EmptyState title="No execution plans yet." hint="An execution plan runs several scenarios, in parallel or one after another, and says for each what happens if it fails." />
      ) : (
        <div className="pp-list">
          {plans.map(plan => (
            <div className="pp-row" key={plan.id}>
              <div className="pp-row-main">
                <div className="pp-row-title">
                  {plan.name}
                  <span className="pp-pill pp-pill-label pp-pill-info">{runModeLabel(plan.run_mode)}</span>
                </div>
                <div className="pp-row-meta">
                  <span>{plan.entries.length} entr{plan.entries.length === 1 ? 'y' : 'ies'}</span>
                  <span>{plan.entries.filter(e => e.enabled !== false).length} enabled</span>
                  <span>{planReleaseSentence(plan, releaseNames)}</span>
                </div>
                {/* What the plan runs, in order, and what each entry does if it fails. */}
                {plan.entries.length > 0 ? (
                  <ol className="pp-plan-entries" aria-label={`Entries of ${plan.name}`}>
                    {[...plan.entries].sort((a, b) => a.order - b.order).slice(0, CARD_ENTRY_LIMIT).map(entry => (
                      <li key={entry.order} className={entry.enabled === false ? 'is-off' : undefined}>
                        <span>{entryScenarioName(entry)}</span>
                        {' · '}
                        <span>
                          {entry.enabled === false ? 'turned off' : FAILURE_POLICY_OPTIONS.find(option => option.value === (entry.failure_policy || 'halt'))?.summary}
                        </span>
                      </li>
                    ))}
                    {plan.entries.length > CARD_ENTRY_LIMIT ? <li className="pp-plan-entries-more">and {plan.entries.length - CARD_ENTRY_LIMIT} more</li> : null}
                  </ol>
                ) : null}
                {renderPlanState(plan)}
              </div>
              {canManage && (
                <div className="pp-row-actions">
                  <button
                    type="button"
                    className="btn btn-primary btn-sm"
                    disabled={busy || (Boolean(plan.activity?.active_run) && effectiveOverlapPolicy(plan.overlap_policy) !== 'allow')}
                    title={plan.activity?.active_run && effectiveOverlapPolicy(plan.overlap_policy) !== 'allow' ? 'A run of this plan is in progress. Its overlap policy does not start another alongside it.' : undefined}
                    aria-describedby={plan.activity?.active_run && effectiveOverlapPolicy(plan.overlap_policy) !== 'allow' ? `plan-run-blocked-${plan.id}` : undefined}
                    onClick={() => void runPlan(plan)}
                  >
                    Run
                  </button>
                  {/* One action on the card; the rest wait in More, as in the libraries. */}
                  <RowActions label={plan.name}>
                    <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => startEdit(plan)}>
                      Edit
                    </button>
                    <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => setSchedulesTarget(plan)}>
                      Schedules
                    </button>
                    <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={() => openApprovals(plan)}>
                      Approvals
                    </button>
                    <button type="button" className="btn btn-ghost btn-sm pp-menu-danger" disabled={busy} onClick={() => setDeleteTarget(plan)}>
                      Delete
                    </button>
                  </RowActions>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Create / edit modal */}
      {draft && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeDraft)}>
          <div ref={draftDialogRef} className="pp-modal pp-modal-wide" role="dialog" aria-modal="true" aria-labelledby="execution-plan-editor-title">
            {/* One way out: Cancel (or Escape), beside Save. */}
            <div className="pp-modal-header">
              <h3 id="execution-plan-editor-title">{draft.id ? 'Edit execution plan' : 'New execution plan'}</h3>
            </div>
            <div className="pp-modal-form">
              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Name</span>
                  <input value={draft.name} onChange={e => setDraft(d => (d ? { ...d, name: e.target.value } : d))} placeholder="e.g. nightly regression" />
                </label>
                <label className="pp-field">
                  <span>Run mode</span>
                  <select value={draft.run_mode} onChange={e => setDraft(d => (d ? { ...d, run_mode: e.target.value } : d))}>
                    {RUN_MODE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                  </select>
                </label>
              </div>

              <div className="pp-field-row">
                <label className="pp-field">
                  <span>Application</span>
                  <select
                    aria-label="Application the plan's runs are filed under"
                    value={draft.application_id}
                    onChange={e => setDraft(d => (d ? { ...d, application_id: e.target.value, release_id: '' } : d))}
                  >
                    <option value="">None (Imported Legacy)</option>
                    {draft.application_id && applications && !applications.some(application => application.id === draft.application_id) && <option value={draft.application_id}>Application {draft.application_id}</option>}
                    {(applications || []).map(application => <option key={application.id} value={application.id}>{application.name}</option>)}
                  </select>
                </label>
                <label className="pp-field">
                  <span>Release</span>
                  <select
                    aria-label="Release the plan's runs are filed under"
                    value={draft.release_id}
                    disabled={!draft.application_id}
                    onChange={e => setDraft(d => (d ? { ...d, release_id: e.target.value } : d))}
                  >
                    <option value="">{draft.application_id ? 'Choose a release' : 'Choose an application first'}</option>
                    {draft.release_id && !(releasesByApplication[draft.application_id] || []).some(release => release.id === draft.release_id) && <option value={draft.release_id}>{releaseNames.releases[draft.release_id] || `Release ${draft.release_id}`}</option>}
                    {(releasesByApplication[draft.application_id] || []).map(release => <option key={release.id} value={release.id}>{release.name}</option>)}
                  </select>
                </label>
              </div>
              <p className="setting-desc pp-field-help">
                {draft.release_id
                  ? 'Every run this plan starts, by hand or on a schedule, is filed under this release and shows in its workspace.'
                  : draft.application_id
                    ? 'Choose a release, or the plan\'s runs are filed under Imported Legacy and stay out of this workspace.'
                    : 'Without a release, the plan\'s runs are filed under Imported Legacy and stay out of this workspace.'}
              </p>

              <label className="pp-field">
                <span>If it is started while a run of it is still running</span>
                <select
                  aria-label="If the plan is started while a run of it is still running"
                  value={draft.overlap_policy}
                  onChange={e => setDraft(d => (d ? { ...d, overlap_policy: e.target.value as ExecutionPlanOverlapPolicy } : d))}
                >
                  {OVERLAP_POLICY_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                </select>
                <span className="setting-desc">{OVERLAP_POLICY_OPTIONS.find(option => option.value === draft.overlap_policy)?.description}</span>
              </label>

              <div className="pp-field">
                <span>Entries (run order)</span>
                <div className="pp-entry-list">
                  {/* One grid row per entry; the column names are for sight, each control is named for its entry. */}
                  <div className="pp-entry-head" aria-hidden="true">
                    <span>#</span>
                    <span>Scenario</span>
                    <span>If it fails</span>
                    <span>Enabled</span>
                    <span />
                  </div>
                  {draft.entries.map((entry, index) => {
                    // Named for its scenario, and by number: one scenario can run twice in a plan.
                    const entryName = entry.scenario_id ? `entry ${index + 1}, ${entryScenarioName(entry)}` : `entry ${index + 1}`
                    return (
                    <div className="pp-entry-block" key={index}>
                      <div className="pp-entry">
                      <div className="pp-entry-index">{index + 1}</div>
                      {scenarioOptions && scenarioOptions.length > 0 ? (
                        <select
                          aria-label={`Entry ${index + 1} scenario`}
                          value={entry.scenario_id}
                          onChange={e => {
                            const opt = scenarioOptions.find(s => s.id === e.target.value)
                            updateEntry(index, { scenario_id: e.target.value, scenario_name: opt?.name || '' })
                          }}
                        >
                          <option value="">Choose a scenario</option>
                          {entry.scenario_id && !scenarioNames.has(entry.scenario_id) && <option value={entry.scenario_id}>{entry.scenario_name || `Scenario ${entry.scenario_id}`} (not in this project&apos;s list)</option>}
                          {scenarioOptions.map(s => (
                            <option key={s.id} value={s.id}>
                              {scenarioNames.get(s.id)?.duplicate ? `${scenarioNames.get(s.id)?.name} · duplicate name` : s.name}
                            </option>
                          ))}
                        </select>
                      ) : (
                        <input aria-label={`Entry ${index + 1} scenario ID`} value={entry.scenario_id} placeholder="scenario id" onChange={e => updateEntry(index, { scenario_id: e.target.value })} />
                      )}
                      <select aria-label={`Entry ${index + 1}: if this run fails`} value={entry.failure_policy} onChange={e => updateEntry(index, { failure_policy: e.target.value })}>
                        {FAILURE_POLICY_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                      </select>
                      <label className="pp-entry-enabled">
                        <input type="checkbox" checked={entry.enabled} aria-label={`Enabled: ${entryName}`} onChange={e => updateEntry(index, { enabled: e.target.checked })} />
                        <span className="pp-entry-enabled-text">Enabled</span>
                      </label>
                      <div className="pp-entry-order">
                      <button type="button" className="btn btn-ghost btn-sm" disabled={index === 0} onClick={() => moveEntry(index, -1)} title="Move up" aria-label={`Move entry ${index + 1} up`}>
                        ↑
                      </button>
                      <button type="button" className="btn btn-ghost btn-sm" disabled={index === draft.entries.length - 1} onClick={() => moveEntry(index, 1)} title="Move down" aria-label={`Move entry ${index + 1} down`}>
                        ↓
                      </button>
                      <button
                        type="button"
                        className="btn btn-ghost btn-sm"
                        onClick={() => setDraft(d => (d ? { ...d, entries: d.entries.filter((_, i) => i !== index) } : d))}
                        title="Remove entry"
                        aria-label={`Remove entry ${index + 1}`}
                      >
                        ✕
                      </button>
                      </div>
                      </div>
                      <div className="pp-entry-advanced">
                        <label className="pp-entry-advanced-field">
                          <span>Timeslot</span>
                          {planReferences.timeslots ? (
                            <select aria-label={`Entry ${index + 1} timeslot`} value={entry.timeslot_id} onChange={e => updateEntry(index, { timeslot_id: e.target.value })}>
                              <option value="">None (use the plan’s timeslot)</option>
                              {entry.timeslot_id && !planReferences.timeslots.some(slot => slot.id === entry.timeslot_id) && <option value={entry.timeslot_id}>Timeslot {entry.timeslot_id} (not in the loaded list)</option>}
                              {planReferences.timeslots.map(slot => <option key={slot.id} value={slot.id}>{formatDateTime(slot.start_at, { timeZone: slot.timezone })} – {formatDateTime(slot.end_at, { timeZone: slot.timezone })} · {slot.approval_state.replace(/_/g, ' ')}</option>)}
                            </select>
                          ) : (
                            <input
                              aria-label={`Entry ${index + 1} timeslot ID`}
                              value={entry.timeslot_id}
                              placeholder="optional timeslot id"
                              onChange={e => updateEntry(index, { timeslot_id: e.target.value })}
                            />
                          )}
                        </label>
                        <div className="pp-entry-pools">
                          <span className="pp-entry-advanced-label">Data pool overrides</span>
                          <PlanEntryDataNote bindings={scenarioOptions?.find(option => option.id === entry.scenario_id)?.data_pools} sequential={draft.run_mode === 'sequential'} />
                          {entry.data_pools.map((dp, di) => dp.binding ? (
                            <div className="pp-entry-pool" key={di}>
                              <span>{liveBindingSummary(dp.binding)}</span>
                              <button type="button" className="btn btn-ghost btn-sm" onClick={() => removePool(index, di)} title="Remove data pool" aria-label={`Remove data pool ${di + 1} from entry ${index + 1}`}>
                                ✕
                              </button>
                            </div>
                          ) : (
                            <div className="pp-entry-pool" key={di}>
                              {planReferences.pools ? (
                                <select aria-label={`Entry ${index + 1} data pool ${di + 1}`} value={dp.pool_id} onChange={e => updatePool(index, di, { pool_id: e.target.value })}>
                                  <option value="">Choose a data pool</option>
                                  {dp.pool_id && !planReferences.pools.some(pool => pool.id === dp.pool_id) && <option value={dp.pool_id}>Unknown pool ({dp.pool_id})</option>}
                                  {planReferences.pools.map(pool => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
                                </select>
                              ) : (
                                <input aria-label={`Entry ${index + 1} data pool ${di + 1} ID`} value={dp.pool_id} placeholder="pool id" onChange={e => updatePool(index, di, { pool_id: e.target.value })} />
                              )}
                              <input aria-label={`Entry ${index + 1} data pool ${di + 1} alias`} value={dp.alias} placeholder="alias" onChange={e => updatePool(index, di, { alias: e.target.value })} />
                              <button type="button" className="btn btn-ghost btn-sm" onClick={() => removePool(index, di)} title="Remove data pool" aria-label={`Remove data pool ${di + 1} from entry ${index + 1}`}>
                                ✕
                              </button>
                            </div>
                          ))}
                          <button type="button" className="btn btn-ghost btn-sm" onClick={() => addPool(index)}>
                            + Add data pool
                          </button>
                        </div>
                      </div>
                    </div>
                    )
                  })}
                </div>
                <button type="button" className="btn btn-secondary btn-sm" onClick={() => setDraft(d => (d ? { ...d, entries: [...d.entries, { ...emptyEntry }] } : d))}>
                  + Add entry
                </button>
              </div>
            </div>
            {error && <div className="pp-error" role="alert">{error}</div>}
            <div className="pp-modal-actions pp-modal-actions-sticky">
								<button type="button" className="btn btn-secondary" onClick={closeDraft} disabled={busy}>
                Cancel
              </button>
              <button type="button" className="btn btn-primary" disabled={!draftValid || busy} onClick={() => void submitDraft()}>
                {busy ? 'Saving…' : draft.id ? 'Save' : 'Create'}
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {/* Run result modal */}
      {runResult && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closeRunResult)}>
          <div ref={resultDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="execution-plan-result-title">
            <div className="pp-modal-header">
              <h3 id="execution-plan-result-title">{runResultCounts.created === 0 ? 'Plan run did not start' : runResultCounts.failed > 0 ? 'Plan run partly started' : 'Plan run started'}: “{runResult.plan}”</h3>
            </div>
            <div className="pp-modal-form">
              <div className="pp-row-meta">
                <span>{runModeLabel(runResult.res.run_mode)}</span>
                <span>{runResultCounts.created} run{runResultCounts.created === 1 ? '' : 's'} created</span>
                <span>{runResultCounts.failed} failed to start</span>
              </div>
              <div className="pp-list">
                {runResult.res.results.map(result => (
                  <div className="pp-row" key={result.order}>
                    <div className="pp-row-main">
                      <div className="pp-row-title">
                        #{result.order + 1} {runResult.names[result.order] || result.scenario_id}
                        <span className={runResultPill(result.status)}>{result.status}</span>
                      </div>
                      {result.detail ? <div className="pp-row-meta"><span>{result.detail}</span></div> : null}
                    </div>
                    {result.test_id && onOpenTest ? (
                      <div className="pp-row-actions">
                        <button type="button" className="btn btn-ghost btn-sm" onClick={() => onOpenTest(result.test_id!)}>
                          Open run
                        </button>
                      </div>
                    ) : null}
                  </div>
                ))}
              </div>
            </div>
            <div className="pp-modal-actions pp-modal-actions-sticky">
              <button ref={resultDoneRef} type="button" className="btn btn-primary" onClick={closeRunResult}>
                Done
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {/* Sequential plan-run progress modal (dependency-gated, polled live) */}
      {planRun && (
        <DialogLayer><div className="pp-modal-overlay" {...dialogBackdropProps(closePlanRun)}>
          <div ref={progressDialogRef} className="pp-modal" role="dialog" aria-modal="true" aria-labelledby="execution-plan-progress-title">
            <div className="pp-modal-header">
              <h3 id="execution-plan-progress-title">
                Run “{planRun.planName}”
                <span className={planRunPillClass(planRun.run)} style={{ marginLeft: 8 }} role="status" aria-live="polite">
                  {planRunStatusLabel(planRun.run)}
                </span>
              </h3>
            </div>
            <div className="pp-modal-form">
              <div className="pp-row-meta">
                <span>{planRun.run.run_mode === 'parallel' ? 'In parallel — every enabled entry starts at once' : 'One after another — each entry starts only after the previous run finishes'}</span>
                {isRunView(planRun.run) ? <span>{planRunTimingSentence(planRun.run, clock)}</span> : null}
                {planRunLive && planRunUpdatedAt ? <span>Updated {formatDateTime(planRunUpdatedAt, { seconds: true, zone: false })}</span> : null}
              </div>
              <div className="pp-list">
                {planRun.run.entries.map(entry => {
                  const pill = entryPill(entry)
                  return (
                    <div className="pp-row" key={entry.order}>
                      <div className="pp-row-main">
                        <div className="pp-row-title">
                          #{entry.order + 1} {entryScenarioName(entry)}
                          <span className={pill.className}>{pill.label}</span>
                        </div>
                        {entry.detail ? <div className="pp-row-meta"><span>{entry.detail}</span></div> : null}
                      </div>
                      {entry.test_id && onOpenTest && (('test_status' in entry && entry.test_status) || entry.status !== 'failed') ? (
                        <div className="pp-row-actions">
                          <button type="button" className="btn btn-ghost btn-sm" onClick={() => onOpenTest(entry.test_id!)} aria-label={`Open run for ${entryScenarioName(entry)}`}>
                            Open run
                          </button>
                        </div>
                      ) : null}
                    </div>
                  )
                })}
              </div>
            </div>
            {error && <div className="pp-error" role="alert">{error}</div>}
            <div className="pp-modal-actions pp-modal-actions-sticky">
              {canManage && planRunLive && !(isRunView(planRun.run) && planRunStopping(planRun.run)) ? (
                <button
                  type="button"
                  className="btn btn-danger"
                  disabled={stoppingRunId === planRun.run.id}
                  onClick={() => void stopPlanRun(planRun.planId, planRun.planName, planRun.run.id)}
                >
                  {stoppingRunId === planRun.run.id ? 'Stopping…' : 'Stop plan'}
                </button>
              ) : null}
              <button ref={progressDoneRef} type="button" className="btn btn-primary" onClick={closePlanRun}>
                Done
              </button>
            </div>
          </div>
        </div></DialogLayer>
      )}

      {schedulesTarget && (
        <ExecutionPlanSchedulesModal
          plan={schedulesTarget}
          canManage={canManage}
          canAdmin={canAdmin}
          onClose={() => setSchedulesTarget(null)}
          planReleaseLabel={planReleaseName(schedulesTarget, releaseNames)}
          releaseOptions={schedulesTarget.application_id ? releasesByApplication[schedulesTarget.application_id] || [] : []}
          onReviewApproval={() => {
            const target = schedulesTarget
            // A schedule being edited asks before it is left.
            runGuarded(() => {
              setSchedulesTarget(null)
              openApprovals(target)
            })
          }}
        />
      )}

      {approvalsTarget && (
        <ExecutionPlanApprovalsModal
          plan={approvalsTarget}
          canAdmin={canAdmin}
          notice={approvalsNotice || undefined}
          onClose={closeApprovals}
          onResolved={() => {
            // The notice described the request just decided.
            setApprovalsNotice(null)
            void load()
          }}
          readyApprovalId={approvalsPlan?.activity?.quota?.ready_approval_id}
          runNowBlocked={runNowBlocked}
          onRunNow={canManage && approvalsPlan ? () => {
            closeApprovals()
            void runPlan(approvalsPlan, { approvalReady: true })
          } : undefined}
        />
      )}

      {deleteTarget && (
        <ConfirmDestructiveModal
          title="Delete execution plan"
          description={`This permanently deletes the execution plan “${deleteTarget.name}”.`}
          target={deleteTarget.name}
          targetLabel="Execution plan"
          confirmLabel="Delete plan"
          loading={busy}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => void confirmDelete()}
        />
      )}
    </div>
  )
}
