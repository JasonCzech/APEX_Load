import { type ReactNode } from 'react'
import { type ScriptDefinition } from '../types/scripts'
import { Project, type StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import LoadRunnerImport from './LoadRunnerImport'
import JMeterImport from './JMeterImport'
import JavaHttpImport from './JavaHttpImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'
import ProtocolAssetImport from './ProtocolAssetImport'
import { type DraftEditorFocus, type ImportedSaveResult, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'

interface Props {
  subView: ScriptsAuthoringSubView
  draftScript: ScriptDefinition | null
  draftEditorFocus: DraftEditorFocus | null
  authoringNotice: string | null
  editingId: string | null
  editingScript: ScriptDefinition | null
  projectId?: string
  activeProject?: Project | null
  onSaved: (script: StoredScript) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: (result: ImportedSaveResult) => void
  onEditDraft: (script: ScriptDefinition, focus?: DraftEditorFocus) => void
}

function renderCreateOptionsSurface(
  content: ReactNode,
  onBack: () => void,
) {
  return (
    <div className="page">
      <button className="btn btn-secondary" onClick={onBack} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
      {content}
    </div>
  )
}

export default function ScriptsAuthoringSurface({
  subView,
  draftScript,
  draftEditorFocus,
  authoringNotice,
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
      return <div className="page">{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor initialScript={draftScript} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} onSave={onSaved} onClose={onOpenLibrary} /></div>
    case 'edit':
      return <div className="page">{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor scriptId={editingId} initialScript={editingScript} projectId={projectId} onSave={onSaved} onClose={onOpenLibrary} /></div>
    case 'import':
      return renderCreateOptionsSurface(
        <LoadRunnerImport projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'jmeter-import':
      return renderCreateOptionsSurface(
        <JMeterImport projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'recorder':
      return renderCreateOptionsSurface(
        <RecordingStudio activeProject={activeProject} onImported={onImported} onEdit={onEditDraft} />,
        onOpenCreateOptions,
      )
    case 'sample':
      return renderCreateOptionsSurface(
        <SampleScriptImport projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'java-http-import':
      return renderCreateOptionsSurface(
        <JavaHttpImport onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'devweb-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="devweb" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'har-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="har" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'openapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="openapi" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'wsdl-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="wsdl" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    default:
      return null
  }
}
