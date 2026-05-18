import { useCallback, useEffect, useState } from 'react'
import { AuthoringSession, StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import AuthoringSessionsPanel from './AuthoringSessionsPanel'
import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import ScriptsLibraryTable from './ScriptsLibraryTable'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ImportedSaveResult, type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'

interface Props {
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
  onSelectScenario?: (id: string) => void
}

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

export default function ScriptsPage({ initialMode = 'list', initialEditId, onSelectScenario }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [scripts, setScripts] = useState<StoredScript[]>([])
  const [authoringSessions, setAuthoringSessions] = useState<AuthoringSession[]>([])

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
    createOptionActions,
  } = useScriptsAuthoringState({
    api,
    initialMode,
    initialEditId,
  })

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
    scripts,
    projectId: activeProject?.id,
  })

  useEffect(() => { refresh() }, [refresh])
  useEffect(() => { refreshAuthoringSessions() }, [refreshAuthoringSessions])

  const handleDelete = async (id: string) => {
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

  return (
    <ScriptsAuthoringShell
      authoringState={authoringState}
      projectId={activeProject?.id}
      activeProject={activeProject}
      createOptionActions={createOptionActions}
      onSaved={handleSaved}
      onOpenLibrary={openLibrary}
      onOpenCreateOptions={openCreateOptions}
      onImported={handleImported}
      onEditDraft={handleDraftEdit}
    >
      <div className="page">
        <div className="page-header">
          <div>
            <h2>Scripts</h2>
            <p className="page-subtitle">Reusable scripts. Release associations are inferred from actual test usage.</p>
          </div>
          <button className="btn btn-primary" onClick={openCreateOptions}>+ Create Script</button>
        </div>

        <div className="page-section">
          <AuthoringSessionsPanel
            sessions={authoringSessions}
            onResume={handleResumeAuthoringSession}
            onApprove={(session) => { void handleApproveAuthoringSession(session) }}
            onDiscard={(session) => { void handleDiscardAuthoringSession(session) }}
          />
          <h3 className="section-title">Script Library ({scripts.length})</h3>
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
          />
        </div>
      </div>
    </ScriptsAuthoringShell>
  )
}
