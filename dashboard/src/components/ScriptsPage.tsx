import { useCallback, useEffect, useMemo, useState } from 'react'
import { AuthoringCorrelationPreviewRequest, AuthoringCorrelationPreviewResponse, AuthoringSession, DashboardAuthEffectiveAccessResource, StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import { isEffectiveOperationAllowed } from '../utils/authAccessUtils'
import AuthoringSessionsPanel from './AuthoringSessionsPanel'
import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import ScriptsLibraryTable from './ScriptsLibraryTable'
import PageViewToggle from './PageViewToggle'
import LibraryAssetSourcePanel from './LibraryAssetSourcePanel'
import { type ScriptDefinition } from '../types/scripts'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ImportedSaveResult, type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'

interface Props {
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
  onSelectScenario?: (id: string) => void
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

export default function ScriptsPage({ initialMode = 'list', initialEditId, onSelectScenario }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [authoringSessions, setAuthoringSessions] = useState<AuthoringSession[]>([])
  const [correlationPreviews, setCorrelationPreviews] = useState<Record<string, AuthoringCorrelationPreviewResponse>>({})
  const [correlationErrors, setCorrelationErrors] = useState<Record<string, string>>({})
  const [libraryQuery, setLibraryQuery] = useState('')
  const [healthFilter, setHealthFilter] = useState<AuthoringHealthFilter>('all')
  const [effectiveAuthAccessResources, setEffectiveAuthAccessResources] = useState<DashboardAuthEffectiveAccessResource[]>([])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts(activeProject?.id)
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api, activeProject?.id])

  const refreshAuthoringSessions = useCallback(async () => {
    try {
      const res = await api.listAuthoringSessions(activeProject?.id)
      setAuthoringSessions(res.sessions || [])
    } catch { /* API not available */ }
  }, [api, activeProject?.id])

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

  const canManageScripts = useMemo(
    () => isEffectiveOperationAllowed(effectiveAuthAccessResources, 'manage_scripts'),
    [effectiveAuthAccessResources],
  )

  const filteredScripts = useMemo(() => {
    const query = libraryQuery.trim().toLowerCase()
    return scripts.filter(script => {
      if (!scriptMatchesHealthFilter(script, healthFilter)) return false
      if (!query) return true
      return scriptSearchText(script).includes(query)
    })
  }, [healthFilter, libraryQuery, scripts])

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

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => { refreshAuthoringSessions() }, [refreshAuthoringSessions])
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
    if (!canManageScripts && authoringState.subView !== 'list') {
      openLibrary()
    }
  }, [authoringState.subView, canManageScripts, openLibrary])

  const handleDelete = async (id: string) => {
    if (!canManageScripts) return
    if (!confirm('Delete this script?')) return
    try { await api.deleteScript(id); refresh() } catch { /* ignore */ }
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
      } catch {
        source = undefined
      }
    }
    if (!source) return
    handleDraftEdit(cloneScriptDraft(source, script.name, activeProject?.id))
  }, [activeProject?.id, api, handleDraftEdit])

  const handleApproveAuthoringSession = useCallback(async (session: AuthoringSession) => {
    try {
      const res = await api.approveAuthoringSession(session.id)
      void refresh()
      void refreshAuthoringSessions()
      handleSavedScript(res.script, `Approved authoring session "${res.session.name}". Reviewing the promoted script.`)
    } catch { /* approval errors are surfaced by backend validation in a future detail panel */ }
  }, [api, handleSavedScript, refresh, refreshAuthoringSessions])

  const handleDiscardAuthoringSession = useCallback(async (session: AuthoringSession) => {
    try {
      await api.deleteAuthoringSession(session.id)
      await refreshAuthoringSessions()
    } catch { /* ignore */ }
  }, [api, refreshAuthoringSessions])

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
            <h2>Scripts</h2>
            <p className="page-subtitle">Reusable scripts. Release associations are inferred from actual test usage.</p>
          </div>
          {canManageScripts && (
            <PageViewToggle
              value="library"
              ariaLabel="Script page view"
              className="script-page-toggle"
              onChange={view => {
                if (view === 'library') openLibrary()
                else openCreateOptions()
              }}
              options={[
                { value: 'library', label: 'Script Library' },
                { value: 'create-options', label: 'Create Script Options' },
              ]}
            />
          )}
        </div>

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
          <div className="library-toolbar">
            <div>
              <h3 className="section-title">Script Library ({scripts.length})</h3>
              <p className="library-toolbar-subtitle">
                {libraryQuery.trim() ? `${sortedScripts.length} matching script${sortedScripts.length === 1 ? '' : 's'}` : 'Search by script, protocol, health, or release.'}
              </p>
            </div>
            <label className="library-search">
              <span>Search scripts</span>
              <input
                type="search"
                value={libraryQuery}
                onChange={(event) => setLibraryQuery(event.target.value)}
                placeholder="Find scripts..."
              />
            </label>
          </div>
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
            emptyMessage={libraryQuery.trim() || healthFilter !== 'all' ? 'No scripts match these filters.' : 'No scripts yet.'}
          />
          <LibraryAssetSourcePanel kind="script" projectId={activeProject?.id} onRefreshLocal={refresh} />
        </div>
      </div>
    </ScriptsAuthoringShell>
  )
}
