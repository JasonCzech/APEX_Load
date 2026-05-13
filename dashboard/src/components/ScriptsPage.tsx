import { useCallback, useEffect, useState } from 'react'
import { StoredScript, TestSummary } from '../types'
import { useApi } from '../hooks/useApi'
import { type ScriptDefinition } from '../types/scripts'
import ScriptCreateOptions from './ScriptCreateOptions'
import ScriptsAuthoringSurface from './ScriptsAuthoringSurface'
import ScriptsLibraryTable from './ScriptsLibraryTable'
import { createApiDraft, createJavaHttpDraft, createJmsDraft, createSoapDraft, createWebDraft } from './scriptDraftFactories'
import { useScriptsLibraryModel } from './useScriptsLibraryModel'
import { type ScriptsAuthoringSubView, useScriptsAuthoringState } from './useScriptsAuthoringState'

interface Props {
  initialMode?: ScriptsAuthoringSubView
  initialEditId?: string
  onSelectScenario?: (id: string) => void
}

export default function ScriptsPage({ initialMode = 'list', initialEditId, onSelectScenario }: Props) {
  const api = useApi()
  const [scripts, setScripts] = useState<StoredScript[]>([])

  const refresh = useCallback(async () => {
    try {
      const res = await api.listScripts()
      setScripts(res.scripts || [])
    } catch { /* API not available */ }
  }, [api])

  const {
    authoringState,
    setSubview,
    openLibrary,
    openCreateOptions,
    handleEdit,
    handleDraftEdit,
    startDraft,
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

  const startApiScript = useCallback(() => { startDraft(createApiDraft) }, [startDraft])
  const startSoapScript = useCallback(() => { startDraft(createSoapDraft) }, [startDraft])
  const startJavaHttpScript = useCallback(() => { startDraft(createJavaHttpDraft) }, [startDraft])
  const startWebScript = useCallback(() => { startDraft(createWebDraft) }, [startDraft])
  const startJmsScript = useCallback(() => { startDraft(createJmsDraft) }, [startDraft])

  const hasAuthoringSubview = authoringState.subView !== 'list' && authoringState.subView !== 'create-options'

  const subviewContent = hasAuthoringSubview
    ? (
      <ScriptsAuthoringSurface
        subView={authoringState.subView}
        draftScript={authoringState.draftScript}
        editingId={authoringState.editingId}
        editingScript={authoringState.editingScript}
        onSaved={handleSaved}
        onOpenLibrary={openLibrary}
        onOpenCreateOptions={openCreateOptions}
        onImported={handleImported}
        onEditDraft={handleDraftEdit}
      />
    )
    : null

  if (subviewContent) {
    return subviewContent
  }

  if (authoringState.subView === 'create-options') {
    return (
      <ScriptCreateOptions
        onBack={openLibrary}
        onStartApiScript={startApiScript}
        onStartSoapScript={startSoapScript}
        onStartJavaHttpScript={startJavaHttpScript}
        onOpenSampleImport={() => setSubview('sample')}
        onStartWebScript={startWebScript}
        onOpenRecorder={() => setSubview('recorder')}
        onStartJmsScript={startJmsScript}
        onOpenLoadRunnerImport={() => setSubview('import')}
        onOpenJMeterImport={() => setSubview('jmeter-import')}
      />
    )
  }

  return (
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
  )
}
