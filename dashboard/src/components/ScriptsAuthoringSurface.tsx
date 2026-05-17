import { type ScriptDefinition } from '../types/scripts'
import { Project, type StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'
import JMeterImport from './JMeterImport'
import JavaHttpImport from './JavaHttpImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'
import { type DraftEditorFocus, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'

interface Props {
  subView: ScriptsAuthoringSubView
  draftScript: ScriptDefinition | null
  draftEditorFocus: DraftEditorFocus | null
  editingId: string | null
  editingScript: ScriptDefinition | null
  projectId?: string
  activeProject?: Project | null
  onSaved: (script: StoredScript) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: () => void
  onEditDraft: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
}

export default function ScriptsAuthoringSurface({
  subView,
  draftScript,
  draftEditorFocus,
  editingId,
  editingScript,
  projectId,
  activeProject,
  onSaved,
  onOpenLibrary,
  onOpenCreateOptions,
  onImported,
  onEditDraft,
}: Props) {
  switch (subView) {
    case 'create':
      return <div className="page"><ScriptEditor initialScript={draftScript} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} onSave={onSaved} onClose={onOpenLibrary} /></div>
    case 'edit':
      return <div className="page"><ScriptEditor scriptId={editingId} initialScript={editingScript} projectId={projectId} onSave={onSaved} onClose={onOpenLibrary} /></div>
    case 'import':
      return <div className="page"><LoadRunnerImport projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenLibrary} /></div>
    case 'jmeter-import':
      return <div className="page"><JMeterImport projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenLibrary} /></div>
    case 'recorder':
      return (
        <div className="page">
          <button className="btn btn-secondary" onClick={onOpenCreateOptions} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
          <RecordingStudio activeProject={activeProject} onImported={onImported} onEdit={onEditDraft} />
        </div>
      )
    case 'sample':
      return (
        <div className="page">
          <button className="btn btn-secondary" onClick={onOpenCreateOptions} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
          <SampleScriptImport projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />
        </div>
      )
    case 'java-http-import':
      return (
        <div className="page">
          <button className="btn btn-secondary" onClick={onOpenCreateOptions} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
          <JavaHttpImport onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />
        </div>
      )
    default:
      return null
  }
}
