import type { ScriptSaveOptions } from '../types/storedContent'
import LibraryAssignmentFilters from './LibraryAssignmentFilters'
import { matchesAssignmentFilter } from './libraryAssignmentModel'
import LibraryAssignmentDialog from './LibraryAssignmentDialog'
import { useLibraryFilter } from './useLibraryFilter'
import BulkActions from './ux/BulkActions'
import MoveScriptsDialog from './MoveScriptsDialog'
import { confirmAction } from './ux/confirmAction'
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ApplicationRepository, AuthoringCorrelationPreviewRequest, AuthoringCorrelationPreviewResponse, AuthoringSession, DashboardAuthEffectiveAccessResource, Folder, GitHubConfigResponse, GitHubPushResponse, GitHubScriptStatus, StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import { useGitHubApi } from '../hooks/api/useGitHubApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import AuthoringSessionsPanel from './AuthoringSessionsPanel'
import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import ScriptsLibraryTable, { type ScriptLibraryFederation } from './ScriptsLibraryTable'
import { isAddressableScriptId } from './scriptIdentity'
import ObjectRepositoryPanel from './ObjectRepositoryPanel'
import type { ScriptOption } from './ScriptPicker'
import CorrelationRulesetPanel from './CorrelationRulesetPanel'
import PageViewToggle from './PageViewToggle'
import LibraryFederationControls from './LibraryFederationControls'
import { useLibraryFederation } from './useLibraryFederation'
import { type NavPage } from './Sidebar'
import { type ScriptSecretStatus } from '../types/secrets'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ImportedSaveResult, type ScriptsAuthoringEntryView, type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'
import type { ScriptImportLauncherDescriptor } from '../scriptImportLaunchers'
import { getErrorMessage } from '../utils/errorUtils'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { openProjectSecretsSettings } from './projectSecretsNavigation'
import StoredScriptSecretBindingsDialog from './StoredScriptSecretBindingsDialog'
import { requestSettingsFocus } from './settingsSectionLayout'
import {
  formatSourceSecretSessionFailure,
  sourceSecretSessionIsExpired,
} from './sourceSecretSession'
import {
  beginScriptSecretStatusRequest,
  isLatestScriptSecretStatusRequest,
} from './scriptSecretStatusRequest'
import { requiredScriptSecrets } from './scriptSecretReferenceModel'
import { loadGitHubScriptStatuses } from './scriptGitHubStatusModel'
import { boundedAllSettled } from '../utils/boundedAllSettled'

const GitHubSyncModal = lazy(() => import('./GitHubSyncModal'))

interface Props {
  onScriptSaved?: (script: StoredScript) => void
  initialCreateWorkflow?: 'start-new' | 'source'

  applicationRepository?: ApplicationRepository | null
  initialMode?: ScriptsAuthoringEntryView
  /** The importer the route names; it takes precedence over initialMode. */
  initialImport?: ScriptImportLauncherDescriptor | null
  initialEditId?: string
  nativeImportRequest?: {
    id: number
    launcher: string
    mode?: string | null
    files: File[]
  } | null
  onNativeImportRequestHandled?: () => void
  onSelectScenario?: (id: string) => void
  onNavigate?: (page: NavPage) => void
  onAuthoringStateChange?: (state: { subView: ScriptsAuthoringSubView; editingId: string | null; importLauncher?: string | null; importMode?: string | null }) => void
  featurePolicySignal?: number
}

type AuthoringHealthFilter = 'all' | 'blocked' | 'needs_replay' | 'missing_data' | 'runtime_warning' | 'ai_guardrail'

type ScriptSecretStatusPlan = {
  scriptId: string
  projectId?: string
  applicationId?: string
  required: string[]
  signature: string
}

function buildScriptSecretStatusPlans(
  scripts: readonly StoredScript[],
  activeProjectId?: string,
): ScriptSecretStatusPlan[] {
  return scripts.flatMap(script => {
    if (!isAddressableScriptId(script.id)) return []
    const required = requiredScriptSecrets(script)
    if (required.length === 0) return []
    const projectId = script.project_id || activeProjectId
    const applicationId = script.library_assignment?.application?.id
    return [{
      scriptId: script.id,
      projectId,
      applicationId,
      required,
      signature: `${projectId || ''}\u0000${applicationId || ''}\u0000${required.join('\u0000')}`,
    }]
  })
}

const healthFilters: Array<{ key: AuthoringHealthFilter; label: string }> = [
  { key: 'all', label: 'All' },
  { key: 'blocked', label: 'Blocked' },
  { key: 'needs_replay', label: 'Needs Replay' },
  { key: 'missing_data', label: 'Missing Data' },
  { key: 'runtime_warning', label: 'Runtime Warning' },
  { key: 'ai_guardrail', label: 'AI Guardrail' },
]

function buildImportedSaveNotice(result: ImportedSaveResult): string | null {
  const savedCount = result.scripts.length
  if (savedCount === 0) return null

  const scenarioName = result.scenario?.name?.trim()
  if (savedCount === 1 && !scenarioName) return null

  const scriptText = `${savedCount} script${savedCount === 1 ? '' : 's'}`
  if (scenarioName) {
    return `Saved ${scriptText} and scenario draft "${scenarioName}". Reviewing the first saved script.`
  }
  return `Saved ${scriptText}. Reviewing the first saved script.`
}

function scriptHasHealthText(script: StoredScript, pattern: RegExp) {
  return (script.authoring_health?.inputs || []).some(input =>
    input.status !== 'pass'
    && input.status !== 'info'
    && (pattern.test(input.key || '') || pattern.test(input.summary || '') || pattern.test(input.status || '')),
  )
}

function scriptMatchesHealthFilter(script: StoredScript, filter: AuthoringHealthFilter) {
  if (filter === 'all') return true
  if (filter === 'blocked') {
    return script.authoring_health?.status === 'blocked' || (script.authoring_health?.inputs || []).some(input => input.status === 'blocker')
  }
  if (filter === 'needs_replay') {
    return scriptHasHealthText(script, /replay|evidence/i)
  }
  if (filter === 'missing_data') {
    return scriptHasHealthText(script, /missing[_\s-]?data|data[_\s-]?source|data fit|parameter data/i)
  }
  if (filter === 'runtime_warning') {
    return scriptHasHealthText(script, /runtime|rts|parity/i)
  }
  if (filter === 'ai_guardrail') {
    return scriptHasHealthText(script, /ai|guardrail|provenance/i)
  }
  return true
}

function scriptSearchText(script: StoredScript) {
  return [
    script.name,
    script.protocol,
    script.version,
    script.authoring_health?.status,
    ...(script.associated_releases || []).map(release => release.name),
    ...(script.scenario_run_releases || []).map(release => release.name),
  ].filter(Boolean).join(' ').toLowerCase()
}

const healthFilterKeys = healthFilters.map(filter => filter.key)

export default function ScriptsPage({ onScriptSaved, initialCreateWorkflow, applicationRepository, initialMode = 'list', initialImport, initialEditId, nativeImportRequest, onNativeImportRequestHandled, onSelectScenario, onNavigate, onAuthoringStateChange, featurePolicySignal = 0 }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const [archiveState, setArchiveState] = useState<'active' | 'archived' | 'all'>('active')
  const archiveStateRef = useRef(archiveState)
  archiveStateRef.current = archiveState
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [scriptsLoading, setScriptsLoading] = useState(true)
  const [scriptsLoadFailed, setScriptsLoadFailed] = useState(false)
  const [scriptFolders, setScriptFolders] = useState<Folder[]>([])
  const [authoringSessions, setAuthoringSessions] = useState<AuthoringSession[]>([])
  const [correlationPreviews, setCorrelationPreviews] = useState<Record<string, AuthoringCorrelationPreviewResponse>>({})
  const [correlationErrors, setCorrelationErrors] = useState<Record<string, string>>({})
  const [secretStatusByScript, setSecretStatusByScript] = useState<Record<string, ScriptSecretStatus>>({})
  const [secretStatusStateByScript, setSecretStatusStateByScript] = useState<Record<string, 'loading' | 'ready' | 'error'>>({})
  const filterScope = `scripts:${activeProject?.domain_id || ''}:${activeProjectId || ''}`
  const [libraryQuery, setLibraryQuery] = useLibraryFilter<string>(filterScope, 'query', '')
  const [healthFilter, setHealthFilter] = useLibraryFilter<AuthoringHealthFilter>(filterScope, 'health', 'all', healthFilterKeys)
  const [assignedApplicationFilter, setAssignedApplicationFilter] = useLibraryFilter<string>(filterScope, 'assignedApplication', 'all')
  const [assignedReleaseFilter, setAssignedReleaseFilter] = useLibraryFilter<string>(filterScope, 'assignedRelease', 'all')
  const [releaseFilter, setReleaseFilter] = useLibraryFilter<string>(filterScope, 'release', 'all')
  const [folderFilter, setFolderFilter] = useLibraryFilter<string>(filterScope, 'folder', 'all')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false)
  const [libraryNotice, setLibraryNotice] = useState('')
  // Why a create/edit link opened the library instead (role without edit rights).
  const [accessNotice, setAccessNotice] = useState('')
  const [scriptsLoadError, setScriptsLoadError] = useState('')
  // Which list `scripts` holds, so the Shared assets pickers reuse it only when it is the project's active scripts.
  const [loadedScriptsScope, setLoadedScriptsScope] = useState<{ projectId: string; archiveState: string } | null>(null)
  const [secretDialogScript, setSecretDialogScript] = useState<StoredScript | null>(null)
  // Saved scripts that asked to set up their secrets, waiting for the
  // library's secrets dialog one after another.
  const [secretDialogQueue, setSecretDialogQueue] = useState<StoredScript[]>([])
  const [selectedScriptIds, setSelectedScriptIds] = useState<Set<string>>(new Set())
  const [assignmentScripts, setAssignmentScripts] = useState<StoredScript[] | null>(null)
  const [moveScripts, setMoveScripts] = useState<StoredScript[] | null>(null)
  const [movingScriptIds, setMovingScriptIds] = useState<Set<string>>(new Set())
  const [deletingScriptIds, setDeletingScriptIds] = useState<Set<string>>(new Set())
  const projectScopeRef = useRef({ id: activeProjectId, generation: 0 })
  const scriptsRequestGenerationRef = useRef(0)
  const authoringSessionsRequestGenerationRef = useRef(0)
  const modelExistingRequestGenerationRef = useRef(0)
  const secretStatusSignatureByScriptRef = useRef(new Map<string, string>())
  const secretStatusRequestTokenByScriptRef = useRef(new Map<string, number>())

  if (projectScopeRef.current.id !== activeProjectId) {
    projectScopeRef.current = {
      id: activeProjectId,
      generation: projectScopeRef.current.generation + 1,
    }
    scriptsRequestGenerationRef.current += 1
    authoringSessionsRequestGenerationRef.current += 1
    modelExistingRequestGenerationRef.current += 1
  }

  const isProjectScopeCurrent = useCallback((scope: { id: string | undefined; generation: number }) => (
    projectScopeRef.current.id === scope.id
    && projectScopeRef.current.generation === scope.generation
  ), [])

  const appendLibraryNotice = useCallback((message: string) => {
    setLibraryNotice(prev => {
      if (!prev) return message
      if (prev.split('\n').includes(message)) return prev
      return `${prev}\n${message}`
    })
  }, [])

  const openRemoteSettings = useCallback(() => {
    requestSettingsFocus('integrations')
    onNavigate?.('settings')
  }, [onNavigate])

  useEffect(() => { setSecretDialogScript(null); setSecretDialogQueue([]) }, [activeProjectId])
  useEffect(() => {
    if (secretDialogScript || secretDialogQueue.length === 0) return
    setSecretDialogScript(secretDialogQueue[0])
    setSecretDialogQueue(queue => queue.slice(1))
  }, [secretDialogQueue, secretDialogScript])
  const queueSecretDialog = useCallback((script: StoredScript) => {
    // The dialog needs the script's definition; one without it would never
    // open, or close, and would hold up the rest.
    if (script.script) setSecretDialogQueue(queue => [...queue, script])
  }, [])

  const openProjectSecrets = useCallback((script?: StoredScript) => {
    if (script?.id && script.script) {
      setSecretDialogScript(script)
      return
    }
    openProjectSecretsSettings(script?.project_id || activeProjectId)
  }, [activeProjectId])

  const secretStatusPlans = useMemo(
    () => buildScriptSecretStatusPlans(scripts, activeProjectId),
    [activeProjectId, scripts],
  )
  const secretStatusPlanKey = useMemo(
    () => secretStatusPlans
      .map(plan => `${plan.scriptId}\u0000${plan.signature}`)
      .sort()
      .join('\u0001'),
    [secretStatusPlans],
  )
  const secretStatusPlansRef = useRef(secretStatusPlans)
  secretStatusPlansRef.current = secretStatusPlans

  const fetchSecretStatusesForPlans = useCallback(async (plans: readonly ScriptSecretStatusPlan[]) => {
    const projectId = plans[0]?.projectId
    const applicationId = plans[0]?.applicationId
    if (!projectId || plans.length === 0) return {} as Record<string, ScriptSecretStatus>
    const required = Array.from(new Set(plans.flatMap(plan => plan.required))).sort()
    const responses = await Promise.all(Array.from({ length: Math.ceil(required.length / 256) }, (_, index) => (
      applicationId
        ? api.getApplicationSecretStatuses(applicationId, required.slice(index * 256, (index + 1) * 256))
        : api.getProjectSecretStatuses(projectId, required.slice(index * 256, (index + 1) * 256))
    )))
    const secrets = responses.flatMap(response => response.secrets)
    const configured = new Set(secrets.filter(secret => secret.configured).map(secret => secret.name))
    return Object.fromEntries(plans.map(plan => [plan.scriptId, {
      script_id: plan.scriptId,
      project_id: projectId,
      application_id: applicationId,
      required: plan.required,
      configured: plan.required.filter(name => configured.has(name)),
      missing: plan.required.filter(name => !configured.has(name)),
      secrets: secrets.filter(secret => plan.required.includes(secret.name)),
      capabilities: responses[0].capabilities,
    } satisfies ScriptSecretStatus]))
  }, [api])

  useEffect(() => {
    const plans = secretStatusPlansRef.current
    const knownScriptIds = new Set(plans.map(plan => plan.scriptId))
    secretStatusSignatureByScriptRef.current.forEach((_, scriptId) => {
      if (!knownScriptIds.has(scriptId)) secretStatusSignatureByScriptRef.current.delete(scriptId)
    })
    secretStatusRequestTokenByScriptRef.current.forEach((_, scriptId) => {
      if (!knownScriptIds.has(scriptId)) secretStatusRequestTokenByScriptRef.current.delete(scriptId)
    })
    setSecretStatusByScript(current => Object.fromEntries(
      Object.entries(current).filter(([scriptId]) => knownScriptIds.has(scriptId)),
    ))

    if (plans.length === 0) {
      setSecretStatusStateByScript({})
      return
    }
    if (typeof api.getProjectSecretStatuses !== 'function' || (plans.some(plan => plan.applicationId) && typeof api.getApplicationSecretStatuses !== 'function')) {
      setSecretStatusStateByScript(Object.fromEntries(plans.map(plan => [plan.scriptId, 'error'])))
      return
    }

    let cancelled = false
    const pendingPlans = plans.filter(plan => (
      plan.projectId
      && secretStatusSignatureByScriptRef.current.get(plan.scriptId) !== plan.signature
    ))
    const requestTokens = new Map<string, number>()
    pendingPlans.forEach(plan => {
      const token = beginScriptSecretStatusRequest(secretStatusRequestTokenByScriptRef.current, plan.scriptId)
      requestTokens.set(plan.scriptId, token)
    })
    const missingProjectPlans = plans.filter(plan => !plan.projectId)
    setSecretStatusStateByScript(current => {
      const next = Object.fromEntries(Object.entries(current).filter(([scriptId]) => knownScriptIds.has(scriptId))) as Record<string, 'loading' | 'ready' | 'error'>
      pendingPlans.forEach(plan => { next[plan.scriptId] = 'loading' })
      missingProjectPlans.forEach(plan => { next[plan.scriptId] = 'error' })
      return next
    })

    if (pendingPlans.length === 0) {
      return () => { cancelled = true }
    }

    const byScope = new Map<string, ScriptSecretStatusPlan[]>()
    pendingPlans.forEach(plan => {
      if (!plan.projectId) return
      const key = `${plan.projectId}\u0000${plan.applicationId || ''}`
      byScope.set(key, [...(byScope.get(key) || []), plan])
    })
    void Promise.all(Array.from(byScope.values()).map(async projectPlans => {
      try {
        const statuses = await fetchSecretStatusesForPlans(projectPlans)
        if (cancelled) return
        const currentPlans = new Map(secretStatusPlansRef.current.map(plan => [plan.scriptId, plan.signature]))
        const acceptedPlans = projectPlans.filter(plan => (
          currentPlans.get(plan.scriptId) === plan.signature
          && isLatestScriptSecretStatusRequest(secretStatusRequestTokenByScriptRef.current, plan.scriptId, requestTokens.get(plan.scriptId) || 0)
        ))
        if (acceptedPlans.length === 0) return
        acceptedPlans.forEach(plan => secretStatusSignatureByScriptRef.current.set(plan.scriptId, plan.signature))
        setSecretStatusByScript(current => ({
          ...current,
          ...Object.fromEntries(acceptedPlans.map(plan => [plan.scriptId, statuses[plan.scriptId]])),
        }))
        setSecretStatusStateByScript(current => ({ ...current, ...Object.fromEntries(acceptedPlans.map(plan => [plan.scriptId, 'ready' as const])) }))
      } catch {
        if (cancelled) return
        const currentPlans = new Map(secretStatusPlansRef.current.map(plan => [plan.scriptId, plan.signature]))
        const failedPlans = projectPlans.filter(plan => (
          currentPlans.get(plan.scriptId) === plan.signature
          && isLatestScriptSecretStatusRequest(secretStatusRequestTokenByScriptRef.current, plan.scriptId, requestTokens.get(plan.scriptId) || 0)
        ))
        setSecretStatusStateByScript(current => ({
          ...current,
          ...Object.fromEntries(failedPlans.map(plan => [plan.scriptId, 'error' as const])),
        }))
      }
    }))
    return () => {
      cancelled = true
    }
  }, [api, fetchSecretStatusesForPlans, secretStatusPlanKey])

  const retryScriptSecretStatus = useCallback((script: StoredScript) => {
    const plan = secretStatusPlansRef.current.find(candidate => candidate.scriptId === script.id)
    if (!plan?.projectId || typeof api.getProjectSecretStatuses !== 'function' || (plan.applicationId && typeof api.getApplicationSecretStatuses !== 'function')) {
      if (isAddressableScriptId(script.id)) {
        setSecretStatusStateByScript(current => ({ ...current, [script.id]: 'error' }))
      }
      return
    }
    const token = beginScriptSecretStatusRequest(secretStatusRequestTokenByScriptRef.current, plan.scriptId)
    setSecretStatusStateByScript(current => ({ ...current, [plan.scriptId]: 'loading' }))
    void fetchSecretStatusesForPlans([plan]).then(statuses => {
      const currentPlan = secretStatusPlansRef.current.find(candidate => candidate.scriptId === plan.scriptId)
      if (!isLatestScriptSecretStatusRequest(secretStatusRequestTokenByScriptRef.current, plan.scriptId, token) || currentPlan?.signature !== plan.signature) return
      secretStatusSignatureByScriptRef.current.set(plan.scriptId, plan.signature)
      setSecretStatusByScript(current => ({ ...current, [plan.scriptId]: statuses[plan.scriptId] }))
      setSecretStatusStateByScript(current => ({ ...current, [plan.scriptId]: 'ready' }))
    }).catch(() => {
      const currentPlan = secretStatusPlansRef.current.find(candidate => candidate.scriptId === plan.scriptId)
      if (!isLatestScriptSecretStatusRequest(secretStatusRequestTokenByScriptRef.current, plan.scriptId, token) || currentPlan?.signature !== plan.signature) return
      setSecretStatusStateByScript(current => ({ ...current, [plan.scriptId]: 'error' }))
    })
  }, [api, fetchSecretStatusesForPlans])

  const refresh = useCallback(async () => {
    const requestedArchiveState = archiveStateRef.current
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++scriptsRequestGenerationRef.current
    if (!projectId) {
      setScripts([])
      setScriptsLoading(false)
      return
    }
    setScriptsLoading(true)
    setScriptsLoadFailed(false)
    try {
      const res = await (requestedArchiveState === 'active' ? api.listScripts(projectId) : api.listScripts(projectId, undefined, { archiveState: requestedArchiveState }))
      if (!isProjectScopeCurrent(scope) || scriptsRequestGenerationRef.current !== requestGeneration) return
      setScripts(res.scripts || [])
      setLoadedScriptsScope({ projectId, archiveState: requestedArchiveState })
      setScriptsLoadError('')
    } catch (error) {
      if (!isProjectScopeCurrent(scope) || scriptsRequestGenerationRef.current !== requestGeneration) return
      setScriptsLoadFailed(true)
      setScriptsLoadError(getErrorMessage(error, 'Unable to load scripts.'))
    } finally {
      if (isProjectScopeCurrent(scope) && scriptsRequestGenerationRef.current === requestGeneration) setScriptsLoading(false)
    }
  }, [activeProjectId, api, isProjectScopeCurrent])

  useEffect(() => {
    let cancelled = false
    if (!activeProjectId) {
      setScriptFolders([])
      return () => { cancelled = true }
    }
    void api.listFolders(activeProjectId, 'scripts').then(response => {
      if (!cancelled) setScriptFolders(response.folders || [])
    }).catch(() => {
      if (!cancelled) setScriptFolders([])
    })
    return () => { cancelled = true }
  }, [activeProjectId, api])

  const refreshAuthoringSessions = useCallback(async () => {
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++authoringSessionsRequestGenerationRef.current
    if (!projectId) {
      setAuthoringSessions([])
      return
    }
    try {
      const res = await api.listAuthoringSessions(projectId)
      if (!isProjectScopeCurrent(scope) || authoringSessionsRequestGenerationRef.current !== requestGeneration) return
      setAuthoringSessions(res.sessions || [])
    } catch (error) {
      if (!isProjectScopeCurrent(scope) || authoringSessionsRequestGenerationRef.current !== requestGeneration) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to load authoring sessions.'))
    }
  }, [activeProjectId, api, appendLibraryNotice, isProjectScopeCurrent])

  useLayoutEffect(() => {
    scriptsRequestGenerationRef.current += 1
    authoringSessionsRequestGenerationRef.current += 1
    setScripts([])
    setLoadedScriptsScope(null)
    setAuthoringSessions([])
    setCorrelationPreviews({})
    setCorrelationErrors({})
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    setLibraryNotice('')
    setScriptsLoadError('')
    setSelectedScriptIds(new Set())
    setDeletingScriptIds(new Set())
    setAssignmentScripts(null)
    setMoveScripts(null)
    setMovingScriptIds(new Set())
  }, [activeProjectId])

  const {
    authoringState,
    openLibrary,
    openCreateOptions,
    openScriptAssets,
    openCreateOptionsWithNotice,
    handleEdit,
    handleSavedScript: acceptSavedScript,
    handleDraftEdit,
    returnToDraftSource,
    createOptionActions,
  } = useScriptsAuthoringState({
    api,
    initialMode,
    initialImport,
    initialEditId,
    projectId: activeProjectId,
  })
  const handleSavedScript = useCallback((...args: Parameters<typeof acceptSavedScript>) => {
    acceptSavedScript(...args)
    onScriptSaved?.(args[0])
  }, [acceptSavedScript, onScriptSaved])


  useEffect(() => {
    const projectId = activeProjectId
    if (!projectId) return
    const importerActive = Boolean(authoringState.importLauncher)
    const recorderActive = authoringState.subView === 'recorder'
    if (!importerActive && !recorderActive) return
    let cancelled = false
    void api.listAuthoringCapabilities(projectId).then(capabilities => {
      if (cancelled) return
      const enabled = recorderActive
        ? !capabilities.feature_policy || capabilities.feature_policy.features.some(feature => feature.id === 'script_builder.browser_recording' && feature.effective_enabled)
        : capabilities.import_surfaces == null || capabilities.import_surfaces.some(surface => surface.launcher === authoringState.importLauncher)
      if (!enabled) {
        openCreateOptionsWithNotice('That authoring feature was disabled by an administrator. Choose another script builder workflow.')
      }
    }).catch(() => {
      if (!cancelled) {
        openCreateOptionsWithNotice('Unable to verify domain feature access. Retry from the script builder source selection when the service is available.')
      }
    })
    return () => { cancelled = true }
  }, [activeProjectId, api, authoringState.importLauncher, authoringState.subView, featurePolicySignal, openCreateOptionsWithNotice])
  const authoringProjectIdRef = useRef(activeProjectId)
  useLayoutEffect(() => {
    if (authoringProjectIdRef.current === activeProjectId) return
    authoringProjectIdRef.current = activeProjectId
    openLibrary()
  }, [activeProjectId, openLibrary])
  useLayoutEffect(() => {
    // Any authoring navigation invalidates an in-flight Model Existing result.
    // This prevents a late clone from reopening the editor after the operator
    // deliberately moved elsewhere.
    modelExistingRequestGenerationRef.current += 1
  }, [authoringState.editingId, authoringState.subView])
  const openImportFiles = createOptionActions.openImportFiles

  useEffect(() => {
    if (!nativeImportRequest || nativeImportRequest.files.length === 0) return
    openImportFiles(nativeImportRequest.launcher, nativeImportRequest.files, nativeImportRequest.mode)
    onNativeImportRequestHandled?.()
  }, [nativeImportRequest, onNativeImportRequestHandled, openImportFiles])

  const canManageScripts = useMemo(
    () => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_scripts'),
    [effectiveAuthAccessResources],
  )
  const canMigrateSecrets = useMemo(
    () => (
      isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_projects', false)
      || isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_system', false)
    ),
    [effectiveAuthAccessResources],
  )

  const releaseOptions = useMemo(() => {
    const names = new Set<string>()
    scripts.forEach(script => (script.associated_releases || []).forEach(release => release.name && names.add(release.name)))
    return Array.from(names).sort((a, b) => a.localeCompare(b))
  }, [scripts])

  const folderOptions = useMemo(
    () => [...scriptFolders].sort((left, right) => left.name.localeCompare(right.name)),
    [scriptFolders],
  )
  const folderNameById = useMemo(
    () => Object.fromEntries(scriptFolders.map(folder => [folder.id, folder.name])),
    [scriptFolders],
  )

  const scriptsMatchingLibraryFilters = useMemo(() => {
    const query = libraryQuery.trim().toLowerCase()
    return scripts.filter(script => {
      if (!matchesAssignmentFilter(script.library_assignment, assignedApplicationFilter, assignedReleaseFilter)) return false
      if (releaseFilter !== 'all' && !(script.associated_releases || []).some(release => release.name === releaseFilter)) return false
      if (folderFilter === 'unfiled' && script.folder_id) return false
      if (folderFilter !== 'all' && folderFilter !== 'unfiled' && script.folder_id !== folderFilter) return false
      if (!query) return true
      return scriptSearchText(script).includes(query)
    })
  }, [folderFilter, libraryQuery, releaseFilter, scripts, assignedApplicationFilter, assignedReleaseFilter])
  const filteredScripts = useMemo(() => scriptsMatchingLibraryFilters.filter(script => scriptMatchesHealthFilter(script, healthFilter)), [scriptsMatchingLibraryFilters, healthFilter])

  const healthFilterCounts = useMemo(() => {
    return healthFilters.reduce<Record<AuthoringHealthFilter, number>>((acc, filter) => {
      acc[filter.key] = scriptsMatchingLibraryFilters.filter(script => scriptMatchesHealthFilter(script, filter.key)).length
      return acc
    }, {
      all: 0,
      blocked: 0,
      needs_replay: 0,
      missing_data: 0,
      runtime_warning: 0,
      ai_guardrail: 0,
    })
  }, [scriptsMatchingLibraryFilters])

  const {
    sortField,
    sortDirection,
    sortedScripts,
    onSort,
    expandedScriptId,
    scriptRuns,
    scriptRunErrors,
    loadingRunsFor,
    onToggleScriptRuns,
    onRetryScriptRuns,
    innerSortField,
    innerSortDirection,
    onInnerSort,
    getSortedInnerRuns,
  } = useScriptsLibraryModel({
    api,
    scripts: filteredScripts,
    projectId: activeProjectId,
    folderNameById,
  })

  const federation = useLibraryFederation({ kind: 'script', projectId: activeProjectId, onRefreshLocal: refresh })
  const visibleSelectableScriptIds = useMemo(
    () => federation.sourceFilter === 'host'
      ? []
      : sortedScripts.flatMap(script => isAddressableScriptId(script.id) ? [script.id] : []),
    [federation.sourceFilter, sortedScripts],
  )
  const selectedVisibleScriptIds = useMemo(
    () => visibleSelectableScriptIds.filter(id => selectedScriptIds.has(id)),
    [selectedScriptIds, visibleSelectableScriptIds],
  )

  useEffect(() => {
    const visible = new Set(visibleSelectableScriptIds)
    setSelectedScriptIds(current => {
      const next = new Set(Array.from(current).filter(id => visible.has(id)))
      return next.size === current.size ? current : next
    })
  }, [visibleSelectableScriptIds])

  // --- GitHub script sync (config + connection gate + push/import modals) ---
  const githubApi = useGitHubApi()
  const [githubConfig, setGithubConfig] = useState<GitHubConfigResponse | null>(null)
  const [githubConnected, setGithubConnected] = useState(false)
  const [githubPushScript, setGithubPushScript] = useState<StoredScript | null>(null)
  const [githubImportOpen, setGithubImportOpen] = useState(false)
  const [githubNotice, setGithubNotice] = useState('')
  const [githubBulkRunning, setGithubBulkRunning] = useState(false)
  const [githubStatusByScript, setGithubStatusByScript] = useState<Record<string, GitHubScriptStatus>>({})

  useLayoutEffect(() => {
    setGithubNotice('')
    setGithubBulkRunning(false)
    setGithubStatusByScript({})
  }, [activeProjectId])

  useEffect(() => {
    let cancelled = false
    void (async () => {
      try {
        const config = await githubApi.getConfig()
        if (cancelled) return
        setGithubConfig(config)
        if (config.enabled) {
          const connection = await githubApi.getConnection()
          if (!cancelled) setGithubConnected(Boolean(connection.connected))
        }
      } catch {
        if (!cancelled) setGithubConfig(null)
      }
    })()
    return () => { cancelled = true }
  }, [githubApi])

  const githubReady = Boolean(githubConfig?.enabled && githubConnected)
  const githubAutomationReady = Boolean(githubConfig?.enabled && (githubConnected || githubConfig.app_configured || githubConfig.pat_configured))

  const handleGithubPushed = useCallback((response: GitHubPushResponse) => {
    setGithubNotice(
      response.pull_request_url
        ? `Opened pull request for ${response.owner}/${response.repo}.`
        : `Pushed to ${response.owner}/${response.repo}@${response.branch}.`,
    )
  }, [])

  const handleGithubImported = useCallback((result: ImportedSaveResult) => {
    const saved = result.scripts[0]
    if (saved) setGithubNotice(`Imported "${saved.name}" from GitHub.`)
    void refresh()
    void refreshAuthoringSessions()
    if (result.stayInImportReview || !saved) return
    handleSavedScript(saved)
  }, [handleSavedScript, refresh, refreshAuthoringSessions])

  const handleGithubBulkImport = useCallback(async () => {
    if (!activeProjectId) {
      setGithubNotice('Select a project before starting GitHub bulk import.')
      return
    }
    const scope = { ...projectScopeRef.current }
    setGithubBulkRunning(true)
    try {
      const job = await githubApi.createSyncJob({ project_id: activeProjectId, kind: 'import' })
      if (!isProjectScopeCurrent(scope)) return
      setGithubNotice(`GitHub bulk import ${job.status || 'queued'}: ${job.processed || 0}/${job.total || 0} processed, ${job.created || 0} imported.`)
      void refresh()
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      setGithubNotice(getErrorMessage(error, 'Unable to start GitHub bulk import.'))
    } finally {
      if (isProjectScopeCurrent(scope)) setGithubBulkRunning(false)
    }
  }, [activeProjectId, githubApi, isProjectScopeCurrent, refresh])

  const assetScripts = useMemo<ScriptOption[] | undefined>(() => (
    loadedScriptsScope && loadedScriptsScope.projectId === activeProjectId && loadedScriptsScope.archiveState === 'active'
      ? scripts.map(script => ({ id: script.id, name: script.name, protocol: script.protocol, refKey: script.ref_key }))
      : undefined
  ), [activeProjectId, loadedScriptsScope, scripts])

  // The Scripts page tabs. A role that cannot create scripts has no Builder.
  const scriptPageTab = authoringState.subView === 'create-options' ? 'create-options' : authoringState.subView === 'assets' ? 'assets' : 'library'
  const showScriptPageToggle = authoringState.subView === 'list' || authoringState.subView === 'assets' || (canManageScripts && authoringState.subView === 'create-options')
  const scriptPageControls = useMemo(() => (
    showScriptPageToggle ? (
      <PageViewToggle
        value={scriptPageTab}
        ariaLabel="Script page view"
        className="script-page-toggle topbar-page-section-toggle"
        onChange={view => {
          if (view === 'library') openLibrary()
          else if (view === 'assets') openScriptAssets()
          else openCreateOptions()
        }}
        options={[
          { value: 'library', label: 'Script library' },
          ...(canManageScripts ? [{ value: 'create-options' as const, label: 'Script builder' }] : []),
          { value: 'assets', label: 'Shared assets' },
        ]}
      />
    ) : null
  ), [canManageScripts, openCreateOptions, openLibrary, openScriptAssets, scriptPageTab, showScriptPageToggle])

  const scriptsTopbarContribution = useMemo(() => (
    showScriptPageToggle ? {
      id: 'scripts-page',
      title: scriptPageTab === 'create-options' ? 'Script builder' : scriptPageTab === 'assets' ? 'Shared assets' : 'Script library',
      controls: scriptPageControls,
      secondaryActions: [
        {
          id: 'github-import',
          label: 'Import from GitHub',
          compactLabel: 'GitHub',
          onClick: () => setGithubImportOpen(true),
          hidden: authoringState.subView !== 'list' || !canManageScripts || !githubReady,
          tone: 'secondary' as const,
        },
        {
          id: 'github-bulk-import',
          label: githubBulkRunning ? 'Bulk importing…' : 'Bulk Import',
          compactLabel: 'Bulk',
          onClick: () => { void handleGithubBulkImport() },
          disabled: githubBulkRunning,
          hidden: authoringState.subView !== 'list' || !canManageScripts || !githubAutomationReady,
          tone: 'secondary' as const,
        },
      ],
      priority: 10,
    } : null
  ), [authoringState.subView, canManageScripts, githubAutomationReady, githubBulkRunning, githubReady, handleGithubBulkImport, scriptPageControls, scriptPageTab, showScriptPageToggle])

  useTopbarContribution(scriptsTopbarContribution)

  useEffect(() => {
    let cancelled = false
    if (!githubReady || scripts.length === 0) {
      setGithubStatusByScript({})
      return () => { cancelled = true }
    }
    void (async () => {
      const statuses = await loadGitHubScriptStatuses(scripts, githubApi.scriptStatus)
      if (cancelled) return
      setGithubStatusByScript(statuses)
    })()
    return () => { cancelled = true }
  }, [githubApi, githubReady, scripts])

  const knownScriptIds = useMemo(
    () => new Set(scripts.filter(script => isAddressableScriptId(script.id)).map(script => script.id)),
    [scripts],
  )

  const federationRemoteRows = useMemo(() => {
    if (assignedApplicationFilter !== 'all' || assignedReleaseFilter !== 'all' || federation.sourceFilter === 'local' || releaseFilter !== 'all' || folderFilter !== 'all' || healthFilter !== 'all') return []
    const query = libraryQuery.trim().toLowerCase()
    return federation.remoteItems.filter(item => {
      // On "all", local scripts already cover assets that exist on both sides; only
      // surface remote-only entries so each asset appears once. "host" shows everything.
      if (federation.sourceFilter === 'all' && item.local_item_id && knownScriptIds.has(item.local_item_id)) return false
      if (!query) return true
      return [item.name, item.protocol, item.project_id].filter(Boolean).join(' ').toLowerCase().includes(query)
    })
  }, [federation.remoteItems, federation.sourceFilter, knownScriptIds, libraryQuery, releaseFilter, folderFilter, healthFilter, assignedApplicationFilter, assignedReleaseFilter])

  const matchingScriptCount = (federation.sourceFilter === 'host' ? 0 : sortedScripts.length) + federationRemoteRows.length

  const scriptFederation = useMemo<ScriptLibraryFederation>(() => ({
    sourceFilter: federation.sourceFilter,
    remoteRows: federationRemoteRows,
    actionBlocked: federation.actionBlocked,
    actionBlockedReason: federation.actionBlockedReason,
    onPushScript: (script) => void federation.pushLocalItem({ kind: 'script', id: script.id, name: script.name }),
    onPullRemote: (item) => void federation.pullRemoteItem(item),
    onCompare: (localId, remoteItem) => void federation.compareItems(localId, remoteItem),
    hostItemForLocal: federation.hostItemForLocal,
    syncStatusForLocal: federation.syncStatusForLocal,
    remoteItemHref: federation.remoteItemHref,
  }), [federation, federationRemoteRows])

  useEffect(() => { refresh() }, [refresh, archiveState])
  useEffect(() => { refreshAuthoringSessions() }, [refreshAuthoringSessions])
  useEffect(() => {
    let cancelled = false
    const scope = { ...projectScopeRef.current }
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    const loadEffectiveAccess = async () => {
      try {
        const response = await api.getAuthEffectiveAccess(activeProjectId, {
          domain_id: activeProject?.domain_id,
          application_repository_id: applicationRepository?.id,
        })
        if (!cancelled && isProjectScopeCurrent(scope)) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled && isProjectScopeCurrent(scope)) {
          setEffectiveAuthAccessResources([])
        }
      } finally {
        if (!cancelled && isProjectScopeCurrent(scope)) {
          setEffectiveAccessLoaded(true)
        }
      }
    }
    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProjectId, activeProject?.domain_id, api, applicationRepository?.id, isProjectScopeCurrent])
  useEffect(() => {
    // Only redirect away from a manage-only sub-view once effective access has
    // loaded; otherwise the brief default-DENY window before the access fetch
    // resolves would bounce a deep-linked create/edit view back to the library.
    if (effectiveAccessLoaded && !canManageScripts && authoringState.subView !== 'list' && authoringState.subView !== 'assets') {
      openLibrary()
      setAccessNotice('Your role in this project can view scripts but not create or edit them, so the script library is shown instead.')
    }
  }, [effectiveAccessLoaded, authoringState.subView, canManageScripts, openLibrary])

  useEffect(() => {
    onAuthoringStateChange?.({
      subView: authoringState.subView,
      editingId: authoringState.editingId,
      importLauncher: authoringState.importLauncher,
      importMode: authoringState.importMode,
    })
  }, [authoringState.editingId, authoringState.importLauncher, authoringState.importMode, authoringState.subView, onAuthoringStateChange])

  const handleDelete = async (id: string) => {
    if (!canManageScripts || movingScriptIds.has(id)) return
    const scriptName = scripts.find(script => script.id === id)?.name
    if (!(await confirmAction(scriptName ? `Delete the script “${scriptName}”? This cannot be undone; Archive keeps it restorable.` : 'Delete this script? This cannot be undone; Archive keeps it restorable.', { title: scriptName ? `Delete “${scriptName}”` : 'Delete script', confirmLabel: 'Delete', destructive: true }))) return
    const scope = { ...projectScopeRef.current }
    setDeletingScriptIds(current => new Set(current).add(id))
    try {
      await api.deleteScript(id)
      if (!isProjectScopeCurrent(scope)) return
      secretStatusSignatureByScriptRef.current.delete(id)
      setScripts(current => current.filter(script => script.id !== id))
      setSelectedScriptIds(current => {
        const next = new Set(current)
        next.delete(id)
        return next
      })
      await refresh()
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to delete script.'))
    } finally {
      if (isProjectScopeCurrent(scope)) {
        setDeletingScriptIds(current => {
          const next = new Set(current)
          next.delete(id)
          return next
        })
      }
    }
  }

  const toggleScriptSelection = (id: string) => {
    setSelectedScriptIds(current => {
      const next = new Set(current)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const toggleAllVisibleScripts = (checked: boolean) => {
    setSelectedScriptIds(current => {
      const next = new Set(current)
      visibleSelectableScriptIds.forEach(id => checked ? next.add(id) : next.delete(id))
      return next
    })
  }

  const deleteSelectedScripts = async () => {
    const ids = selectedVisibleScriptIds.filter(id => !deletingScriptIds.has(id) && !movingScriptIds.has(id))
    if (!canManageScripts || ids.length === 0 || !(await confirmAction(`Delete ${ids.length} selected script${ids.length === 1 ? '' : 's'}? This cannot be undone; Archive keeps scripts restorable.`, { title: 'Delete scripts', confirmLabel: 'Delete', destructive: true }))) return
    const scope = { ...projectScopeRef.current }
    setDeletingScriptIds(current => new Set([...current, ...ids]))
    const outcomes = await boundedAllSettled(ids, id => api.deleteScript(id))
    const deletedIds = ids.filter((_, index) => outcomes[index]?.status === 'fulfilled')
    const failures = outcomes.flatMap((outcome, index) => outcome.status === 'rejected'
      ? [{ id: ids[index], reason: getErrorMessage(outcome.reason, 'delete failed') }]
      : [])
    if (isProjectScopeCurrent(scope)) {
      const deleted = new Set(deletedIds)
      setScripts(current => current.filter(script => !script.id || !deleted.has(script.id)))
      setSelectedScriptIds(current => {
        const next = new Set(current)
        deletedIds.forEach(id => next.delete(id))
        return next
      })
      setDeletingScriptIds(current => {
        const next = new Set(current)
        ids.forEach(id => next.delete(id))
        return next
      })
      if (failures.length > 0) {
        const details = failures.slice(0, 3).map(item => `${item.id}: ${item.reason}`).join('; ')
        const suffix = failures.length > 3 ? `; ${failures.length - 3} more failed` : ''
        appendLibraryNotice(`Deleted ${deletedIds.length} of ${ids.length} selected scripts. ${details}${suffix}`)
      } else {
        appendLibraryNotice(`Deleted ${deletedIds.length} selected script${deletedIds.length === 1 ? '' : 's'}.`)
      }
      await refresh()
    }
  }

  const handleSaved = useCallback((script: StoredScript, options?: ScriptSaveOptions) => {
    void refresh()
    void refreshAuthoringSessions()
    if (options?.returnedToSource) {
      // The editor went back to the archive review that opened it, which took
      // the save; a page embedding this one still hears of it.
      if (options.configureSecrets) queueSecretDialog(script)
      onScriptSaved?.(script)
      return
    }
    handleSavedScript(script, undefined, options?.retainedDraft, options)
    if (options?.configureSecrets) queueSecretDialog(script)
  }, [handleSavedScript, onScriptSaved, queueSecretDialog, refresh, refreshAuthoringSessions])

  const handleImported = useCallback((result: ImportedSaveResult) => {
    void refresh()
    void refreshAuthoringSessions()
    if (result.stayInImportReview) return
    if (result.scripts[0]) {
      handleSavedScript(result.scripts[0], buildImportedSaveNotice(result))
      return
    }
    openLibrary()
  }, [handleSavedScript, openLibrary, refresh, refreshAuthoringSessions])

  const handleResumeAuthoringSession = useCallback((session: AuthoringSession) => {
    if (session.draft) {
      handleDraftEdit(session.draft)
    }
  }, [handleDraftEdit])

  const handleStartExistingScript = useCallback(async (script: StoredScript, options?: { migrateSecrets?: boolean }) => {
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++modelExistingRequestGenerationRef.current
    const requestIsCurrent = () => (
      modelExistingRequestGenerationRef.current === requestGeneration
      && isProjectScopeCurrent(scope)
    )
    try {
      const response = await api.cloneScriptDraft(script.id, {
        import_detected_credential_values: options?.migrateSecrets === true,
      })
      let migrationNotice = ''
      let sourceSessionConsumed = false
      try {
        if (!requestIsCurrent()) return
        if (options?.migrateSecrets && response.source_secret_session_id && activeProjectId) {
          if (sourceSecretSessionIsExpired(response.source_secret_session_expires_at)) {
            migrationNotice = 'The safe draft was opened without importing detected credential values because the protected source session expired. Configure its project secrets manually.'
          } else try {
            const sourceBacked = (response.secret_binding_requirements || [])
              .filter(requirement => requirement.source_value_available)
            const names = Array.from(new Set(sourceBacked.map(requirement => requirement.suggested_name).filter(Boolean)))
            const status = names.length > 0
              ? await api.getProjectSecretStatuses(activeProjectId, names)
              : null
            if (!requestIsCurrent()) return
            const configured = new Set((status?.secrets || [])
              .filter(secret => secret.configured)
              .map(secret => secret.name))
            const migrationBindings = sourceBacked
              .filter(requirement => !configured.has(requirement.suggested_name))
              .map(requirement => ({
                requirement_id: requirement.id,
                secret_name: requirement.suggested_name,
              }))
            if (migrationBindings.length > 0) {
              if (!requestIsCurrent()) return
              await api.migrateImportSecrets({
                project_id: activeProjectId,
                source_secret_session_id: response.source_secret_session_id,
                secrets: migrationBindings,
              })
              sourceSessionConsumed = true
            }
          } catch (error) {
            migrationNotice = `The safe draft was opened without importing detected credential values. ${formatSourceSecretSessionFailure(error)}`
          }
        }
      } finally {
        if (response.source_secret_session_id && activeProjectId && !sourceSessionConsumed) {
          try {
            await api.discardImportSecretSession({
              project_id: activeProjectId,
              source_secret_session_id: response.source_secret_session_id,
            })
          } catch {
            if (requestIsCurrent() && !migrationNotice) {
              migrationNotice = 'The protected source credential session could not be discarded and will expire automatically.'
            }
          }
        }
      }
      if (!requestIsCurrent()) return
      handleDraftEdit(response.script, undefined, undefined, { initiallyDirty: true })
      if (migrationNotice) appendLibraryNotice(migrationNotice)
    } catch (error) {
      if (!requestIsCurrent()) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to create a safe draft from this script.'))
    }
  }, [activeProjectId, api, appendLibraryNotice, handleDraftEdit, isProjectScopeCurrent])

  const handleApproveAuthoringSession = useCallback(async (session: AuthoringSession) => {
    const scope = { ...projectScopeRef.current }
    try {
      const res = await api.approveAuthoringSession(session.id)
      if (!isProjectScopeCurrent(scope)) return
      void refresh()
      void refreshAuthoringSessions()
      handleSavedScript(res.script, `Approved authoring session "${res.session.name}". Reviewing the promoted script.`)
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to approve authoring session.'))
    }
  }, [api, appendLibraryNotice, handleSavedScript, isProjectScopeCurrent, refresh, refreshAuthoringSessions])

  const handleDiscardAuthoringSession = useCallback(async (session: AuthoringSession) => {
    const scope = { ...projectScopeRef.current }
    try {
      await api.deleteAuthoringSession(session.id)
      if (!isProjectScopeCurrent(scope)) return
      await refreshAuthoringSessions()
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to discard authoring session.'))
    }
  }, [api, appendLibraryNotice, isProjectScopeCurrent, refreshAuthoringSessions])

  const handlePreviewAuthoringCorrelation = useCallback(async (session: AuthoringSession, payload: AuthoringCorrelationPreviewRequest) => {
    const scope = { ...projectScopeRef.current }
    try {
      const res = await api.previewAuthoringCorrelation(session.id, { ...payload, apply: false })
      if (!isProjectScopeCurrent(scope)) return
      setCorrelationPreviews(prev => ({ ...prev, [session.id]: res }))
      setCorrelationErrors(prev => ({ ...prev, [session.id]: '' }))
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      setCorrelationErrors(prev => ({ ...prev, [session.id]: error instanceof Error ? error.message : 'Unable to preview repair.' }))
    }
  }, [api, isProjectScopeCurrent])

  const handleApplyAuthoringCorrelation = useCallback(async (session: AuthoringSession, payload: AuthoringCorrelationPreviewRequest) => {
    const scope = { ...projectScopeRef.current }
    try {
      const res = await api.previewAuthoringCorrelation(session.id, { ...payload, apply: true })
      if (!isProjectScopeCurrent(scope)) return
      setCorrelationPreviews(prev => ({ ...prev, [session.id]: res }))
      setCorrelationErrors(prev => ({ ...prev, [session.id]: '' }))
      await refreshAuthoringSessions()
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      setCorrelationErrors(prev => ({ ...prev, [session.id]: error instanceof Error ? error.message : 'Unable to apply repair.' }))
    }
  }, [api, isProjectScopeCurrent, refreshAuthoringSessions])

  return (
    <>
      {scriptsLoadError && <p className="settings-notice form-error" role="alert">{scriptsLoadError} <button type="button" className="btn btn-secondary btn-sm" disabled={scriptsLoading} onClick={() => void refresh()}>Retry loading scripts</button></p>}
      {accessNotice && authoringState.subView === 'list' && (
        <p className="settings-notice" role="status">
          {accessNotice}{' '}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setAccessNotice('')}>Dismiss</button>
        </p>
      )}
      {libraryNotice && (
        <p className="settings-notice form-error" role="alert">
          {libraryNotice}{' '}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setLibraryNotice('')}>Dismiss</button>
        </p>
      )}
      {(authoringState.subView === 'list' || authoringState.subView === 'create-options') && authoringState.authoringNotice && (
        <p className="settings-notice form-error" role="alert">
          {authoringState.authoringNotice}
        </p>
      )}
      <ScriptsAuthoringShell
        initialCreateWorkflow={initialCreateWorkflow}
        authoringState={authoringState}
        projectId={activeProject?.id}
        policyRefreshSignal={featurePolicySignal}
        activeProject={activeProject}
        scripts={scripts}
        createOptionActions={createOptionActions}
        onSaved={handleSaved}
        onOpenLibrary={openLibrary}
        onOpenCreateOptions={openCreateOptions}
        onOpenAssets={openScriptAssets}
        assetsView={(
          <div className="page script-assets-page">
            <div className="page-header">
              <div>
                <h2>Shared assets</h2>
                <p className="page-subtitle">Shared by the project&apos;s scripts, so a change made here reaches every script that uses it.</p>
              </div>
              {!hasTopbarContributionProvider && scriptPageControls}
            </div>
            <div className="page-section">
              <ObjectRepositoryPanel projectId={activeProject?.id} canManage={canManageScripts} scripts={assetScripts} />
            </div>
            <div className="page-section">
              <CorrelationRulesetPanel projectId={activeProject?.id} canManage={canManageScripts} scripts={assetScripts} />
            </div>
          </div>
        )}
        onImported={handleImported}
        onConfigureSecrets={() => openProjectSecrets()}
        onEditDraft={handleDraftEdit}
        onReturnToDraftSource={returnToDraftSource}
        onStartExistingScript={handleStartExistingScript}
        canManageScripts={canManageScripts}
        canMigrateSecrets={canMigrateSecrets}
      >
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Script library</h2>
            <p className="page-subtitle">Reusable scripts. Release associations are inferred from actual test usage.</p>
          </div>
          {!hasTopbarContributionProvider && (
            <div className="settings-inline-actions" style={{ alignItems: 'center' }}>
              {canManageScripts && githubReady && (
                <button className="btn btn-secondary" type="button" onClick={() => setGithubImportOpen(true)}>
                  Import from GitHub
                </button>
              )}
              {canManageScripts && githubAutomationReady && (
                <button className="btn btn-secondary" type="button" onClick={() => { void handleGithubBulkImport() }} disabled={githubBulkRunning}>
                  Bulk Import
                </button>
              )}
              {scriptPageControls}
            </div>
          )}
        </div>
        {githubNotice && (
          <p className="settings-notice" role="status">
            {githubNotice}{' '}
            <button className="btn btn-ghost btn-sm" type="button" onClick={() => setGithubNotice('')}>Dismiss</button>
          </p>
        )}

        <div className="page-section">
          {canManageScripts && authoringSessions.length > 0 && (
            <details className="ux-review-queue"><summary>Authoring review · {authoringSessions.length} session{authoringSessions.length === 1 ? "" : "s"}</summary><AuthoringSessionsPanel
              sessions={authoringSessions}
              onResume={handleResumeAuthoringSession}
              onApprove={(session) => { void handleApproveAuthoringSession(session) }}
              onDiscard={(session) => { void handleDiscardAuthoringSession(session) }}
              correlationPreviews={correlationPreviews}
              correlationErrors={correlationErrors}
              onPreviewCorrelation={(session, payload) => { void handlePreviewAuthoringCorrelation(session, payload) }}
              onApplyCorrelation={(session, payload) => { void handleApplyAuthoringCorrelation(session, payload) }}
            /></details>
          )}
          <LibraryFederationControls
            kind="script"
            filtersActive={archiveState !== 'active' || assignedApplicationFilter !== 'all' || assignedReleaseFilter !== 'all' || Boolean(libraryQuery.trim()) || healthFilter !== 'all' || releaseFilter !== 'all' || folderFilter !== 'all' || federation.sourceFilter !== 'all'}
            onResetFilters={() => { setArchiveState('active'); setAssignedApplicationFilter('all'); setAssignedReleaseFilter('all'); setLibraryQuery(''); setHealthFilter('all'); setReleaseFilter('all'); setFolderFilter('all'); federation.setSourceFilter('all') }}
            filterCount={[archiveState !== 'active', assignedApplicationFilter !== 'all', assignedReleaseFilter !== 'all', releaseFilter !== 'all', folderFilter !== 'all'].filter(Boolean).length}
            primaryAction={canManageScripts && <button className="btn btn-primary" type="button" onClick={openCreateOptions}>Create script</button>}
            federation={federation}
            projectName={activeProject?.name}
            onConfigureRemotes={openRemoteSettings}
            summaryText={`${matchingScriptCount} matching script${matchingScriptCount === 1 ? '' : 's'} · Project library across applications and releases.`}
            searchControl={(<label className="project-redaction-rule-field">
                  <span>Search scripts</span>
                  <input
                    className="settings-input"
                    type="search"
                    value={libraryQuery}
                    onChange={(event) => setLibraryQuery(event.target.value)}
                    placeholder="Find scripts by name, protocol, or release..."
                  />
                </label>)}
            filterControls={(
              <>
            <label>Script archive state <select className="settings-input" value={archiveState} onChange={event => setArchiveState(event.target.value as typeof archiveState)}><option value="active">Active</option><option value="archived">Archived</option><option value="all">All</option></select></label>
              <LibraryAssignmentFilters items={scripts} application={assignedApplicationFilter} release={assignedReleaseFilter} onApplication={setAssignedApplicationFilter} onRelease={setAssignedReleaseFilter} />
                <label className="project-redaction-rule-field">
                  <span>Run release</span>
                  <select
                    className="settings-input library-source-select library-release-filter"
                    value={releaseFilter}
                    onChange={(event) => setReleaseFilter(event.target.value)}
                  >
                    <option value="all">All releases</option>
                    {releaseFilter !== 'all' && !releaseOptions.includes(releaseFilter) && (
                      <option value={releaseFilter}>{releaseFilter}</option>
                    )}
                    {releaseOptions.map(name => <option key={name} value={name}>{name}</option>)}
                  </select>
                </label>
                <label className="project-redaction-rule-field">
                  <span>Folder</span>
                  <select
                    className="settings-input library-source-select library-folder-filter"
                    value={folderFilter}
                    onChange={(event) => setFolderFilter(event.target.value)}
                  >
                    <option value="all">All folders</option>
                    <option value="unfiled">Unfiled</option>
                    {folderFilter !== 'all' && folderFilter !== 'unfiled' && !folderOptions.some(folder => folder.id === folderFilter) && (
                      <option value={folderFilter}>{folderNameById[folderFilter] || folderFilter}</option>
                    )}
                    {folderOptions.map(folder => <option key={folder.id} value={folder.id}>{folder.name}</option>)}
                  </select>
                </label>

              </>
            )}
          />
          <div className="library-health-filters" aria-label="Local authoring health filters">
            <span className="cell-muted">Local authoring health</span>
            {healthFilters.map(filter => (
              <button
                key={filter.key}
                type="button"
                aria-pressed={healthFilter === filter.key}
                className={`script-health-filter ${healthFilter === filter.key ? 'active' : ''}`}
                onClick={() => setHealthFilter(filter.key)}
              >
                <span>{filter.label}</span>
                <strong>{healthFilterCounts[filter.key] || 0}</strong>
              </button>
            ))}
          </div>
          {canManageScripts && (
            <BulkActions count={selectedVisibleScriptIds.length}>
                <button type="button" className="btn btn-secondary" disabled={selectedVisibleScriptIds.length === 0 || selectedVisibleScriptIds.some(id => deletingScriptIds.has(id) || movingScriptIds.has(id))} onClick={() => setMoveScripts(scripts.filter(script => selectedVisibleScriptIds.includes(script.id)))}>Move Selected…</button>
                <button className="btn btn-secondary" disabled={selectedVisibleScriptIds.some(id => deletingScriptIds.has(id) || movingScriptIds.has(id))} onClick={() => setAssignmentScripts(scripts.filter(script => selectedVisibleScriptIds.includes(script.id)))}>Change application/releases…</button>
                {selectedVisibleScriptIds.length < visibleSelectableScriptIds.length && <button type="button" className="btn btn-secondary" onClick={() => toggleAllVisibleScripts(true)}>Select all {visibleSelectableScriptIds.length} matching scripts</button>}
                <button
                  type="button"
                  className="btn btn-ghost"
                  onClick={() => setSelectedScriptIds(new Set())}
                  disabled={selectedVisibleScriptIds.length === 0}
                >
                  Clear
                </button>
                <button
                  type="button"
                  className="btn btn-danger"
                  onClick={() => { void deleteSelectedScripts() }}
                  disabled={selectedVisibleScriptIds.length === 0 || selectedVisibleScriptIds.some(id => deletingScriptIds.has(id) || movingScriptIds.has(id))}
                >
                  Delete Selected ({selectedVisibleScriptIds.length})
                </button>
              </BulkActions>
          )}
          <ScriptsLibraryTable
            paginationScope={filterScope}
            onArchiveChanged={refresh}
            scripts={sortedScripts}
            sortField={sortField}
            sortDirection={sortDirection}
            onSort={onSort}
            expandedScriptId={expandedScriptId}
            onToggleScriptRuns={(script) => { void onToggleScriptRuns(script) }}
            loadingRunsFor={loadingRunsFor}
            scriptRuns={scriptRuns}
            scriptRunErrors={scriptRunErrors}
            onRetryScriptRuns={(script) => { void onRetryScriptRuns(script) }}
            innerSortField={innerSortField}
            innerSortDirection={innerSortDirection}
            onInnerSort={onInnerSort}
            getSortedInnerRuns={getSortedInnerRuns}
            onSelectScenario={onSelectScenario}
            onEdit={(id) => { void handleEdit(id) }}
            onChangeAssignment={script => setAssignmentScripts([script])}
            onMove={script => setMoveScripts([script])}
            movingScriptIds={movingScriptIds}
            onDelete={(id) => { void handleDelete(id) }}
            selectedScriptIds={selectedScriptIds}
            onToggleScriptSelection={toggleScriptSelection}
            onToggleAllVisibleScripts={toggleAllVisibleScripts}
            deletingScriptIds={deletingScriptIds}
            canManageScripts={canManageScripts}
            emptyMessage={scriptsLoading ? 'Loading scripts…' : scriptsLoadFailed ? 'Scripts unavailable. Use Refresh to retry.' : assignedApplicationFilter !== 'all' || assignedReleaseFilter !== 'all' || libraryQuery.trim() || healthFilter !== 'all' || releaseFilter !== 'all' || folderFilter !== 'all' ? 'No scripts match these filters.' : 'No scripts yet.'}
            onCreate={!scriptsLoading && !scriptsLoadFailed && scripts.length === 0 && !libraryQuery.trim() && healthFilter === 'all' && releaseFilter === 'all' && folderFilter === 'all' ? openCreateOptions : undefined}
            folders={scriptFolders}
            federation={scriptFederation}
            github={canManageScripts && githubReady ? { enabled: true, onPush: setGithubPushScript, statusByScript: githubStatusByScript } : undefined}
            secretStatusByScript={secretStatusByScript}
            secretStatusStateByScript={secretStatusStateByScript}
            onConfigureSecrets={openProjectSecrets}
            onRetrySecretStatus={retryScriptSecretStatus}
            onValidateScriptDetails={api.validateScript}
          />
        </div>
      </div>
      {assignmentScripts && activeProjectId && canManageScripts && <LibraryAssignmentDialog key={activeProjectId} projectId={activeProjectId} kind="scripts" items={assignmentScripts} onClose={() => setAssignmentScripts(null)} onBusy={ids => setMovingScriptIds(new Set(ids))} onResult={(values, notice) => {
        scriptsRequestGenerationRef.current += 1
        setScripts(current => current.map(script => values.has(script.id) ? { ...script, library_assignment: values.get(script.id) } : script))
        setSelectedScriptIds(current => new Set([...current].filter(id => !values.has(id))))
        appendLibraryNotice(notice)
        void refresh()
      }} />}
      {moveScripts && activeProjectId && canManageScripts && (
        <MoveScriptsDialog
          key={activeProjectId}
          projectId={activeProjectId}
          scripts={moveScripts}
          onFoldersLoaded={setScriptFolders}
          onClose={() => setMoveScripts(null)}
          onBusy={ids => setMovingScriptIds(new Set(ids))}
          onResult={(movedIds, unchangedIds, folderId, notice) => {
            scriptsRequestGenerationRef.current += 1
            setScripts(current => current.map(script => movedIds.includes(script.id)
              ? { ...script, folder_id: folderId, script: script.script ? { ...script.script, folder_id: folderId } : script.script }
              : script))
            setSelectedScriptIds(current => new Set([...current].filter(id => !movedIds.includes(id) && !unchangedIds.includes(id))))
            appendLibraryNotice(notice)
            void refresh()
          }}
        />
      )}
      {githubPushScript && githubConfig && (
        <Suspense fallback={null}>
          <GitHubSyncModal
            mode="push"
            config={githubConfig}
            script={githubPushScript}
            projectId={activeProject?.id}
            onClose={() => setGithubPushScript(null)}
            onPushed={handleGithubPushed}
          />
        </Suspense>
      )}
      {secretDialogScript?.script && activeProjectId && (
        <StoredScriptSecretBindingsDialog key={`${activeProjectId}:${secretDialogScript.id}`}
          stored={secretDialogScript} projectId={activeProjectId}
          onClose={() => setSecretDialogScript(null)}
          onConfigured={async () => { retryScriptSecretStatus(secretDialogScript); await refresh() }}
          onSaved={async (saved, changedLocations) => {
            const scope = { ...projectScopeRef.current }
            if (saved.project_id !== scope.id || saved.id !== secretDialogScript.id) return
            appendLibraryNotice(`Rebound ${changedLocations.length} secret reference location${changedLocations.length === 1 ? '' : 's'} in ${saved.name}.`)
            await refresh()
          }} />
      )}
      {githubImportOpen && githubConfig && (
        <Suspense fallback={null}>
          <GitHubSyncModal
            mode="import"
            config={githubConfig}
            projectId={activeProject?.id}
            onClose={() => setGithubImportOpen(false)}
            onImported={handleGithubImported}
          />
        </Suspense>
      )}
      </ScriptsAuthoringShell>
    </>
  )
}
