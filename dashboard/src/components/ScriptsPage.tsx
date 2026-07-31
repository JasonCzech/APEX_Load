import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { ApplicationRepository, AuthoringCorrelationPreviewRequest, AuthoringCorrelationPreviewResponse, AuthoringSession, DashboardAuthEffectiveAccessResource, GitHubConfigResponse, GitHubPushResponse, GitHubScriptStatus, StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import { useGitHubApi } from '../hooks/api/useGitHubApi'
import GitHubSyncModal from './GitHubSyncModal'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import AuthoringSessionsPanel from './AuthoringSessionsPanel'
import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import ScriptsLibraryTable, { type ScriptLibraryFederation } from './ScriptsLibraryTable'
import { isAddressableScriptId } from './scriptIdentity'
import ObjectRepositoryPanel from './ObjectRepositoryPanel'
import CorrelationRulesetPanel from './CorrelationRulesetPanel'
import PageViewToggle from './PageViewToggle'
import LibraryFederationControls from './LibraryFederationControls'
import { useLibraryFederation } from './useLibraryFederation'
import { type NavPage } from './Sidebar'
import { type ScriptSecretStatus } from '../types/secrets'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ImportedSaveResult, type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'
import { getErrorMessage } from '../utils/errorUtils'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { prepareProjectSecretsSettingsNavigation } from './projectSecretsNavigation'
import { requestSettingsFocus } from './settingsSectionLayout'
import {
  formatSourceSecretSessionFailure,
  sourceSecretSessionIsExpired,
} from './sourceSecretSession'

interface Props {
  applicationRepository?: ApplicationRepository | null
  initialMode?: ScriptsAuthoringSubView
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
  onAuthoringStateChange?: (state: { subView: ScriptsAuthoringSubView; editingId: string | null }) => void
}

type AuthoringHealthFilter = 'all' | 'blocked' | 'needs_replay' | 'missing_data' | 'runtime_warning' | 'ai_guardrail'

const healthFilters: Array<{ key: AuthoringHealthFilter; label: string }> = [
  { key: 'all', label: 'All' },
  { key: 'blocked', label: 'Blocked' },
  { key: 'needs_replay', label: 'Needs Replay' },
  { key: 'missing_data', label: 'Missing Data' },
  { key: 'runtime_warning', label: 'Runtime Warning' },
  { key: 'ai_guardrail', label: 'AI Guardrail' },
]

const SECRET_STATUS_CONCURRENCY = 8

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

export default function ScriptsPage({ applicationRepository, initialMode = 'list', initialEditId, nativeImportRequest, onNativeImportRequestHandled, onSelectScenario, onNavigate, onAuthoringStateChange }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const activeProjectId = activeProject?.id
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [authoringSessions, setAuthoringSessions] = useState<AuthoringSession[]>([])
  const [correlationPreviews, setCorrelationPreviews] = useState<Record<string, AuthoringCorrelationPreviewResponse>>({})
  const [correlationErrors, setCorrelationErrors] = useState<Record<string, string>>({})
  const [secretStatusByScript, setSecretStatusByScript] = useState<Record<string, ScriptSecretStatus>>({})
  const [secretStatusStateByScript, setSecretStatusStateByScript] = useState<Record<string, 'loading' | 'ready' | 'error'>>({})
  const [secretStatusRetryGeneration, setSecretStatusRetryGeneration] = useState(0)
  const [libraryQuery, setLibraryQuery] = useState('')
  const [healthFilter, setHealthFilter] = useState<AuthoringHealthFilter>('all')
  const [releaseFilter, setReleaseFilter] = useState('all')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false)
  const [libraryNotice, setLibraryNotice] = useState('')
  const projectScopeRef = useRef({ id: activeProjectId, generation: 0 })
  const scriptsRequestGenerationRef = useRef(0)
  const authoringSessionsRequestGenerationRef = useRef(0)
  const modelExistingRequestGenerationRef = useRef(0)

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

  const openProjectSecrets = useCallback((script?: StoredScript) => {
    prepareProjectSecretsSettingsNavigation(script?.project_id || activeProjectId)
    if (onNavigate) onNavigate('settings')
    else window.location.hash = '#/settings'
  }, [activeProjectId, onNavigate])

  useEffect(() => {
    if (scripts.length === 0) {
      setSecretStatusByScript({})
      setSecretStatusStateByScript({})
      return
    }
    const addressableScripts = scripts.filter(script => isAddressableScriptId(script.id))
    if (typeof api.getScriptSecretStatus !== 'function') {
      setSecretStatusByScript({})
      setSecretStatusStateByScript(Object.fromEntries(addressableScripts.map(script => [script.id, 'error'])))
      return
    }
    if (addressableScripts.length === 0) {
      setSecretStatusByScript({})
      setSecretStatusStateByScript({})
      return
    }
    let cancelled = false
    setSecretStatusByScript({})
    setSecretStatusStateByScript(Object.fromEntries(addressableScripts.map(script => [script.id, 'loading'])))
    let nextIndex = 0
    const worker = async () => {
      while (!cancelled) {
        const index = nextIndex
        nextIndex += 1
        const script = addressableScripts[index]
        if (!script) return
        try {
          const status = await api.getScriptSecretStatus(script.id)
          if (cancelled) return
          setSecretStatusByScript(current => ({ ...current, [script.id]: status }))
          setSecretStatusStateByScript(current => ({ ...current, [script.id]: 'ready' }))
        } catch {
          if (cancelled) return
          setSecretStatusStateByScript(current => ({ ...current, [script.id]: 'error' }))
        }
      }
    }
    void Promise.all(Array.from(
      { length: Math.min(SECRET_STATUS_CONCURRENCY, addressableScripts.length) },
      () => worker(),
    ))
    return () => {
      cancelled = true
    }
  }, [api, scripts, secretStatusRetryGeneration])

  const refresh = useCallback(async () => {
    const projectId = activeProjectId
    const scope = { ...projectScopeRef.current }
    const requestGeneration = ++scriptsRequestGenerationRef.current
    if (!projectId) {
      setScripts([])
      return
    }
    try {
      const res = await api.listScripts(projectId)
      if (!isProjectScopeCurrent(scope) || scriptsRequestGenerationRef.current !== requestGeneration) return
      setScripts(res.scripts || [])
    } catch (error) {
      if (!isProjectScopeCurrent(scope) || scriptsRequestGenerationRef.current !== requestGeneration) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to load scripts.'))
    }
  }, [activeProjectId, api, appendLibraryNotice, isProjectScopeCurrent])

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
    setAuthoringSessions([])
    setCorrelationPreviews({})
    setCorrelationErrors({})
    setEffectiveAuthAccessResources([])
    setEffectiveAccessLoaded(false)
    setLibraryNotice('')
    setLibraryQuery('')
    setHealthFilter('all')
    setReleaseFilter('all')
  }, [activeProjectId])

  const {
    authoringState,
    openLibrary,
    openCreateOptions,
    handleEdit,
    handleSavedScript,
    handleDraftEdit,
    returnToDraftSource,
    createOptionActions,
  } = useScriptsAuthoringState({
    api,
    initialMode,
    initialEditId,
  })
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

  const filteredScripts = useMemo(() => {
    const query = libraryQuery.trim().toLowerCase()
    return scripts.filter(script => {
      if (!scriptMatchesHealthFilter(script, healthFilter)) return false
      if (releaseFilter !== 'all' && !(script.associated_releases || []).some(release => release.name === releaseFilter)) return false
      if (!query) return true
      return scriptSearchText(script).includes(query)
    })
  }, [healthFilter, libraryQuery, releaseFilter, scripts])

  const healthFilterCounts = useMemo(() => {
    return healthFilters.reduce<Record<AuthoringHealthFilter, number>>((acc, filter) => {
      acc[filter.key] = scripts.filter(script => scriptMatchesHealthFilter(script, filter.key)).length
      return acc
    }, {
      all: 0,
      blocked: 0,
      needs_replay: 0,
      missing_data: 0,
      runtime_warning: 0,
      ai_guardrail: 0,
    })
  }, [scripts])

  const {
    sortField,
    sortDirection,
    sortedScripts,
    onSort,
    expandedScriptId,
    scriptRuns,
    loadingRunsFor,
    onToggleScriptRuns,
    innerSortField,
    innerSortDirection,
    onInnerSort,
    getSortedInnerRuns,
  } = useScriptsLibraryModel({
    api,
    scripts: filteredScripts,
    projectId: activeProjectId,
  })

  const federation = useLibraryFederation({ kind: 'script', projectId: activeProjectId, onRefreshLocal: refresh })

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

  const showScriptPageToggle = canManageScripts && (authoringState.subView === 'list' || authoringState.subView === 'create-options')
  const scriptPageControls = useMemo(() => (
    showScriptPageToggle ? (
      <PageViewToggle
        value={authoringState.subView === 'create-options' ? 'create-options' : 'library'}
        ariaLabel="Script page view"
        className="script-page-toggle topbar-page-section-toggle"
        onChange={view => {
          if (view === 'library') openLibrary()
          else openCreateOptions()
        }}
        options={[
          { value: 'library', label: 'Script Library' },
          { value: 'create-options', label: 'Script Builder' },
        ]}
      />
    ) : null
  ), [authoringState.subView, openCreateOptions, openLibrary, showScriptPageToggle])

  const scriptsTopbarContribution = useMemo(() => (
    showScriptPageToggle ? {
      id: 'scripts-page',
      title: authoringState.subView === 'create-options' ? 'Script Builder' : 'Script Library',
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
          label: githubBulkRunning ? 'Bulk Importing...' : 'Bulk Import',
          compactLabel: 'Bulk',
          onClick: () => { void handleGithubBulkImport() },
          disabled: githubBulkRunning,
          hidden: authoringState.subView !== 'list' || !canManageScripts || !githubAutomationReady,
          tone: 'secondary' as const,
        },
      ],
      priority: 10,
    } : null
  ), [authoringState.subView, canManageScripts, githubAutomationReady, githubBulkRunning, githubReady, handleGithubBulkImport, scriptPageControls, showScriptPageToggle])

  useTopbarContribution(scriptsTopbarContribution)

  useEffect(() => {
    let cancelled = false
    if (!githubReady || scripts.length === 0) {
      setGithubStatusByScript({})
      return () => { cancelled = true }
    }
    void (async () => {
      const statuses = await Promise.all(scripts
        .filter(script => isAddressableScriptId(script.id))
        .slice(0, 100)
        .map(async script => {
        try {
          return [script.id, await githubApi.scriptStatus(script.id)] as const
        } catch {
          return [script.id, null] as const
        }
      }))
      if (cancelled) return
      const next: Record<string, GitHubScriptStatus> = {}
      statuses.forEach(([id, status]) => {
        if (status?.linked) next[id] = status
      })
      setGithubStatusByScript(next)
    })()
    return () => { cancelled = true }
  }, [githubApi, githubReady, scripts])

  const knownScriptIds = useMemo(
    () => new Set(scripts.filter(script => isAddressableScriptId(script.id)).map(script => script.id)),
    [scripts],
  )

  const federationRemoteRows = useMemo(() => {
    if (releaseFilter !== 'all') return []
    const query = libraryQuery.trim().toLowerCase()
    return federation.remoteItems.filter(item => {
      // On "all", local scripts already cover assets that exist on both sides; only
      // surface remote-only entries so each asset appears once. "host" shows everything.
      if (federation.sourceFilter === 'all' && item.local_item_id && knownScriptIds.has(item.local_item_id)) return false
      if (!query) return true
      return [item.name, item.protocol, item.project_id].filter(Boolean).join(' ').toLowerCase().includes(query)
    })
  }, [federation.remoteItems, federation.sourceFilter, knownScriptIds, libraryQuery, releaseFilter])

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

  useEffect(() => { refresh() }, [refresh])
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
    if (effectiveAccessLoaded && !canManageScripts && authoringState.subView !== 'list') {
      openLibrary()
    }
  }, [effectiveAccessLoaded, authoringState.subView, canManageScripts, openLibrary])

  useEffect(() => {
    onAuthoringStateChange?.({
      subView: authoringState.subView,
      editingId: authoringState.editingId,
    })
  }, [authoringState.editingId, authoringState.subView, onAuthoringStateChange])

  const handleDelete = async (id: string) => {
    if (!canManageScripts) return
    if (!confirm('Delete this script?')) return
    const scope = { ...projectScopeRef.current }
    try {
      await api.deleteScript(id)
      if (!isProjectScopeCurrent(scope)) return
      void refresh()
    } catch (error) {
      if (!isProjectScopeCurrent(scope)) return
      appendLibraryNotice(getErrorMessage(error, 'Unable to delete script.'))
    }
  }

  const handleSaved = useCallback((script: StoredScript) => {
    void refresh()
    void refreshAuthoringSessions()
    handleSavedScript(script)
  }, [handleSavedScript, refresh, refreshAuthoringSessions])

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
      {libraryNotice && (
        <p className="settings-notice form-error" role="alert">
          {libraryNotice}{' '}
          <button className="btn btn-ghost btn-sm" type="button" onClick={() => setLibraryNotice('')}>Dismiss</button>
        </p>
      )}
      {authoringState.subView === 'list' && authoringState.authoringNotice && (
        <p className="settings-notice form-error" role="alert">
          {authoringState.authoringNotice}
        </p>
      )}
      <ScriptsAuthoringShell
        authoringState={authoringState}
        projectId={activeProject?.id}
        activeProject={activeProject}
        scripts={scripts}
        createOptionActions={createOptionActions}
        onSaved={handleSaved}
        onOpenLibrary={openLibrary}
        onOpenCreateOptions={openCreateOptions}
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
            <h2>Script Library</h2>
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
          {canManageScripts && (
            <AuthoringSessionsPanel
              sessions={authoringSessions}
              onResume={handleResumeAuthoringSession}
              onApprove={(session) => { void handleApproveAuthoringSession(session) }}
              onDiscard={(session) => { void handleDiscardAuthoringSession(session) }}
              correlationPreviews={correlationPreviews}
              correlationErrors={correlationErrors}
              onPreviewCorrelation={(session, payload) => { void handlePreviewAuthoringCorrelation(session, payload) }}
              onApplyCorrelation={(session, payload) => { void handleApplyAuthoringCorrelation(session, payload) }}
            />
          )}
          <LibraryFederationControls
            kind="script"
            federation={federation}
            onConfigureRemotes={openRemoteSettings}
            summaryText={libraryQuery.trim() || releaseFilter !== 'all' ? `${sortedScripts.length} matching script${sortedScripts.length === 1 ? '' : 's'}` : 'Search by script, protocol, health, or release.'}
            filterControls={(
              <>
                <label className="project-redaction-rule-field">
                  <span>Associated Release</span>
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
                  <span>Search Release</span>
                  <input
                    className="settings-input"
                    type="search"
                    value={libraryQuery}
                    onChange={(event) => setLibraryQuery(event.target.value)}
                    placeholder="Find releases or scripts..."
                  />
                </label>
              </>
            )}
          />
          <div className="library-health-filters" aria-label="Authoring health filters">
            {healthFilters.map(filter => (
              <button
                key={filter.key}
                type="button"
                className={`script-health-filter ${healthFilter === filter.key ? 'active' : ''}`}
                onClick={() => setHealthFilter(filter.key)}
              >
                <span>{filter.label}</span>
                <strong>{healthFilterCounts[filter.key] || 0}</strong>
              </button>
            ))}
          </div>
          <ScriptsLibraryTable
            scripts={sortedScripts}
            sortField={sortField}
            sortDirection={sortDirection}
            onSort={onSort}
            expandedScriptId={expandedScriptId}
            onToggleScriptRuns={(script) => { void onToggleScriptRuns(script) }}
            loadingRunsFor={loadingRunsFor}
            scriptRuns={scriptRuns}
            innerSortField={innerSortField}
            innerSortDirection={innerSortDirection}
            onInnerSort={onInnerSort}
            getSortedInnerRuns={getSortedInnerRuns}
            onSelectScenario={onSelectScenario}
            onEdit={(id) => { void handleEdit(id) }}
            onDelete={(id) => { void handleDelete(id) }}
            canManageScripts={canManageScripts}
            emptyMessage={libraryQuery.trim() || healthFilter !== 'all' || releaseFilter !== 'all' ? 'No scripts match these filters.' : 'No scripts yet.'}
            onCreate={scripts.length === 0 && !libraryQuery.trim() && healthFilter === 'all' && releaseFilter === 'all' ? openCreateOptions : undefined}
            federation={scriptFederation}
            github={canManageScripts && githubReady ? { enabled: true, onPush: setGithubPushScript, statusByScript: githubStatusByScript } : undefined}
            secretStatusByScript={secretStatusByScript}
            secretStatusStateByScript={secretStatusStateByScript}
            onConfigureSecrets={onNavigate ? openProjectSecrets : undefined}
            onRetrySecretStatus={() => setSecretStatusRetryGeneration(generation => generation + 1)}
            onValidateScriptDetails={api.validateScript}
          />
        </div>
        <div className="page-section">
          <ObjectRepositoryPanel projectId={activeProject?.id} canManage={canManageScripts} />
        </div>
        <div className="page-section">
          <CorrelationRulesetPanel projectId={activeProject?.id} canManage={canManageScripts} />
        </div>
      </div>
      {githubPushScript && githubConfig && (
        <GitHubSyncModal
          mode="push"
          config={githubConfig}
          script={githubPushScript}
          projectId={activeProject?.id}
          onClose={() => setGithubPushScript(null)}
          onPushed={handleGithubPushed}
        />
      )}
      {githubImportOpen && githubConfig && (
        <GitHubSyncModal
          mode="import"
          config={githubConfig}
          projectId={activeProject?.id}
          onClose={() => setGithubImportOpen(false)}
          onImported={handleGithubImported}
        />
      )}
      </ScriptsAuthoringShell>
    </>
  )
}
