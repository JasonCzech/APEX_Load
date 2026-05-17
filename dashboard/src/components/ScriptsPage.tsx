import { useCallback, useEffect, useState } from 'react'
import { StoredScript } from '../types'
import { useApi } from '../hooks/useApi'
import { useProject } from '../hooks/useProject'
import ScriptsAuthoringShell from './ScriptsAuthoringShell'
import ScriptsLibraryTable from './ScriptsLibraryTable'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'

interface Props {
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
  onSelectScenario?: (id: string) => void
}

export default function ScriptsPage({ initialMode = 'list', initialEditId, onSelectScenario }: Props) {
  const api = useApi()
  const { activeProject } = useProject()
  const [scripts, setScripts] = useState<StoredScript[]>([])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts(activeProject?.id)
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api, activeProject?.id])

  const {
    authoringState,
    openLibrary,
    openCreateOptions,
    handleEdit,
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

  const handleDelete = async (id: string) => {
    if (!confirm('Delete this script?')) return
    try { await api.deleteScript(id); refresh() } catch { /* ignore */ }
  }

  const handleSaved = useCallback(() => {
    void refresh()
    openLibrary()
  }, [openLibrary, refresh])

  const handleImported = useCallback(() => {
    void refresh()
    openLibrary()
  }, [openLibrary, refresh])

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
