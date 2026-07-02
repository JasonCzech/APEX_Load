import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApplicationRepository, AuthoringCorrelationPreviewRequest, AuthoringCorrelationPreviewResponse, AuthoringSession, DashboardAuthEffectiveAccessResource, GitHubConfigResponse, GitHubPushResponse, GitHubScriptStatus, StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import { useGitHubApi } from '../hooks/api/useGitHubApi'
import GitHubSyncModal from './GitHubSyncModal'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import AuthoringSessionsPanel from './AuthoringSessionsPanel'
import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import ScriptsLibraryTable, { type ScriptLibraryFederation } from './ScriptsLibraryTable'
import ObjectRepositoryPanel from './ObjectRepositoryPanel'
import CorrelationRulesetPanel from './CorrelationRulesetPanel'
import PageViewToggle from './PageViewToggle'
import LibraryFederationControls from './LibraryFederationControls'
import { useLibraryFederation } from './useLibraryFederation'
import { type NavPage } from './Sidebar'
import { type ScriptDefinition } from '../types/scripts'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ImportedSaveResult, type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'
import { getErrorMessage } from '../utils/errorUtils'
import { useHasTopbarContributionProvider, useTopbarContribution } from '../hooks/TopbarContributionProvider'

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

function cloneScriptDraft(source: ScriptDefinition, fallbackName: string, projectId?: string): ScriptDefinition {
  const cloned = JSON.parse(JSON.stringify(source)) as ScriptDefinition
  delete cloned.id
  delete cloned.created_at
  delete cloned.updated_at
  delete cloned.folder_id
  cloned.name = `${(source.name || fallbackName || 'Script').trim()} (Copy)`
  cloned.version = cloned.version || '1.0.0'
  cloned.config = cloned.config || { base_url: '' }
  cloned.actions = cloned.actions || []
  if (projectId) {
    cloned.project_id = projectId
  } else {
    delete cloned.project_id
  }
  return cloned
}

export default function ScriptsPage({ applicationRepository, initialMode = 'list', initialEditId, nativeImportRequest, onNativeImportRequestHandled, onSelectScenario, onNavigate, onAuthoringStateChange }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const hasTopbarContributionProvider = useHasTopbarContributionProvider()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [authoringSessions, setAuthoringSessions] = useState<AuthoringSession[]>([])
  const [correlationPreviews, setCorrelationPreviews] = useState<Record<string, AuthoringCorrelationPreviewResponse>>({})
  const [correlationErrors, setCorrelationErrors] = useState<Record<string, string>>({})
  const [libraryQuery, setLibraryQuery] = useState('')
  const [healthFilter, setHealthFilter] = useState<AuthoringHealthFilter>('all')
  const [releaseFilter, setReleaseFilter] = useState('all')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])
  const [effectiveAccessLoaded, setEffectiveAccessLoaded] = useState(false)
  const [libraryNotice, setLibraryNotice] = useState('')
  const appendLibraryNotice = useCallback((message: string) => {
    setLibraryNotice(prev => {
      if (!prev) return message
      if (prev.split('\n').includes(message)) return prev
      return `${prev}\n${message}`
    })
  }, [])

  const openRemoteSettings = useCallback(() => {
    try { window.localStorage.setItem('apexload.settings.focusSection', 'integrations') } catch { /* storage unavailable */ }
    onNavigate?.('settings')
  }, [onNavigate])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts(activeProject?.id)
      setScripts(res.scripts || [])
    } catch (error) {
      appendLibraryNotice(getErrorMessage(error, 'Unable to load scripts.'))
    }
  }, [api, activeProject?.id, appendLibraryNotice])

  const refreshAuthoringSessions = useCallback(async () => {
    try {
      const res = await api.listAuthoringSessions(activeProject?.id)
      setAuthoringSessions(res.sessions || [])
    } catch (error) {
      appendLibraryNotice(getErrorMessage(error, 'Unable to load authoring sessions.'))
    }
  }, [api, activeProject?.id, appendLibraryNotice])

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
    projectId: activeProject?.id,
  })

  const federation = useLibraryFederation({ kind: 'script', projectId: activeProject?.id, onRefreshLocal: refresh })

  // --- GitHub script sync (config + connection gate + push/import modals) ---
  const githubApi = useGitHubApi()
  const [githubConfig, setGithubConfig] = useState<GitHubConfigResponse | null>(null)
  const [githubConnected, setGithubConnected] = useState(false)
  const [githubPushScript, setGithubPushScript] = useState<StoredScript | null>(null)
  const [githubImportOpen, setGithubImportOpen] = useState(false)
  const [githubNotice, setGithubNotice] = useState('')
  const [githubBulkRunning, setGithubBulkRunning] = useState(false)
  const [githubStatusByScript, setGithubStatusByScript] = useState<Record<string, GitHubScriptStatus>>({})

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

  const handleGithubImported = useCallback((saved: StoredScript) => {
    setGithubNotice(`Imported "${saved.name}" from GitHub.`)
    void refresh()
    handleSavedScript(saved)
  }, [handleSavedScript, refresh])

  const handleGithubBulkImport = useCallback(async () => {
    if (!activeProject?.id) {
      setGithubNotice('Select a project before starting GitHub bulk import.')
      return
    }
    setGithubBulkRunning(true)
    try {
      const job = await githubApi.createSyncJob({ project_id: activeProject.id, kind: 'import' })
      setGithubNotice(`GitHub bulk import ${job.status || 'queued'}: ${job.processed || 0}/${job.total || 0} processed, ${job.created || 0} imported.`)
      void refresh()
    } catch (error) {
      setGithubNotice(getErrorMessage(error, 'Unable to start GitHub bulk import.'))
    } finally {
      setGithubBulkRunning(false)
    }
  }, [activeProject?.id, githubApi, refresh])

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
      const statuses = await Promise.all(scripts.slice(0, 100).map(async script => {
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

  const knownScriptIds = useMemo(() => new Set(scripts.map(script => script.id)), [scripts])

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
    const loadEffectiveAccess = async () => {
      try {
        const response = await api.getAuthEffectiveAccess(activeProject?.id, {
          domain_id: activeProject?.domain_id,
          application_repository_id: applicationRepository?.id,
        })
        if (!cancelled) {
          setEffectiveAuthAccessResources(response.resources || [])
        }
      } catch {
        if (!cancelled) {
          setEffectiveAuthAccessResources([])
        }
      } finally {
        if (!cancelled) {
          setEffectiveAccessLoaded(true)
        }
      }
    }
    loadEffectiveAccess()
    return () => {
      cancelled = true
    }
  }, [activeProject?.id, activeProject?.domain_id, api, applicationRepository?.id])
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
    try {
      await api.deleteScript(id)
      void refresh()
    } catch (error) {
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

  const handleStartExistingScript = useCallback(async (script: StoredScript) => {
    let source = script.script
    if (!source) {
      try {
        const response = await api.getScript(script.id)
        source = response.script ?? undefined
      } catch (error) {
        appendLibraryNotice(getErrorMessage(error, 'Unable to load script before cloning.'))
        return
      }
    }
    if (!source) {
      appendLibraryNotice(`Unable to clone "${script.name}": the script body is unavailable.`)
      return
    }
    handleDraftEdit(cloneScriptDraft(source, script.name, activeProject?.id))
  }, [activeProject?.id, api, appendLibraryNotice, handleDraftEdit])

  const handleApproveAuthoringSession = useCallback(async (session: AuthoringSession) => {
    try {
      const res = await api.approveAuthoringSession(session.id)
      void refresh()
      void refreshAuthoringSessions()
      handleSavedScript(res.script, `Approved authoring session "${res.session.name}". Reviewing the promoted script.`)
    } catch (error) {
      appendLibraryNotice(getErrorMessage(error, 'Unable to approve authoring session.'))
    }
  }, [api, appendLibraryNotice, handleSavedScript, refresh, refreshAuthoringSessions])

  const handleDiscardAuthoringSession = useCallback(async (session: AuthoringSession) => {
    try {
      await api.deleteAuthoringSession(session.id)
      await refreshAuthoringSessions()
    } catch (error) {
      appendLibraryNotice(getErrorMessage(error, 'Unable to discard authoring session.'))
    }
  }, [api, appendLibraryNotice, refreshAuthoringSessions])

  const handlePreviewAuthoringCorrelation = useCallback(async (session: AuthoringSession, payload: AuthoringCorrelationPreviewRequest) => {
    try {
      const res = await api.previewAuthoringCorrelation(session.id, { ...payload, apply: false })
      setCorrelationPreviews(prev => ({ ...prev, [session.id]: res }))
      setCorrelationErrors(prev => ({ ...prev, [session.id]: '' }))
    } catch (error) {
      setCorrelationErrors(prev => ({ ...prev, [session.id]: error instanceof Error ? error.message : 'Unable to preview repair.' }))
    }
  }, [api])

  const handleApplyAuthoringCorrelation = useCallback(async (session: AuthoringSession, payload: AuthoringCorrelationPreviewRequest) => {
    try {
      const res = await api.previewAuthoringCorrelation(session.id, { ...payload, apply: true })
      setCorrelationPreviews(prev => ({ ...prev, [session.id]: res }))
      setCorrelationErrors(prev => ({ ...prev, [session.id]: '' }))
      await refreshAuthoringSessions()
    } catch (error) {
      setCorrelationErrors(prev => ({ ...prev, [session.id]: error instanceof Error ? error.message : 'Unable to apply repair.' }))
    }
  }, [api, refreshAuthoringSessions])

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
        onEditDraft={handleDraftEdit}
        onReturnToDraftSource={returnToDraftSource}
        onStartExistingScript={handleStartExistingScript}
        canManageScripts={canManageScripts}
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
            federation={scriptFederation}
            github={canManageScripts && githubReady ? { enabled: true, onPush: setGithubPushScript, statusByScript: githubStatusByScript } : undefined}
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
