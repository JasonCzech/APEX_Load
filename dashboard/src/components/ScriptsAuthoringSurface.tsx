import { type ReactNode } from 'react'
import { type ScriptDefinition } from '../types/scripts'
import { Project, type StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import EnterpriseArchiveImport from './EnterpriseArchiveImport'
import JMeterImport from './JMeterImport'
import JavaHttpImport from './JavaHttpImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'
import ProtocolAssetImport from './ProtocolAssetImport'
import { type AuthoringReturnTarget, type DraftEditorFocus, type ImportedSaveResult, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'

interface Props {
  subView: ScriptsAuthoringSubView
  draftScript: ScriptDefinition | null
  draftEditorFocus: DraftEditorFocus | null
  draftReturnTarget: AuthoringReturnTarget | null
  authoringNotice: string | null
  editingId: string | null
  editingScript: ScriptDefinition | null
  projectId?: string
  activeProject?: Project | null
  onSaved: (script: StoredScript) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: (result: ImportedSaveResult) => void
  onEditDraft: (script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null) => void
  onReturnToDraftSource: () => void
}

function renderCreateOptionsSurface(
  content: ReactNode,
  onBack: () => void,
  hidden = false,
) {
  return (
    <div className="page" hidden={hidden}>
      <button className="btn btn-secondary" onClick={onBack} style={{ alignSelf: 'flex-start' }}>Back to Create Script</button>
      {content}
    </div>
  )
}

export default function ScriptsAuthoringSurface({
  subView,
  draftScript,
  draftEditorFocus,
  draftReturnTarget,
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
  onReturnToDraftSource,
}: Props) {
  const enterpriseArchiveEditReturnTarget: AuthoringReturnTarget = {
    subView: 'import',
    label: 'Back to Converted Script',
    description: 'Return to the converted enterprise script archive review without losing the current import results.',
  }
  const openEnterpriseArchiveDraftInEditor = (script: ScriptDefinition, focus?: DraftEditorFocus) => {
    onEditDraft(script, focus, enterpriseArchiveEditReturnTarget)
  }
  const renderEnterpriseArchiveImportSurface = (hidden = false) => renderCreateOptionsSurface(
    <EnterpriseArchiveImport projectId={projectId} onImported={onImported} onEdit={openEnterpriseArchiveDraftInEditor} onClose={onOpenCreateOptions} />,
    onOpenCreateOptions,
    hidden,
  )
  const closeCreateEditor = draftReturnTarget ? onReturnToDraftSource : onOpenLibrary

  switch (subView) {
    case 'create':
      if (draftReturnTarget?.subView === 'import') {
        return (
          <div className="scripts-authoring-stack">
            {renderEnterpriseArchiveImportSurface(true)}
            <div className="page">
              <div className="authoring-return-bar">
                <button className="btn btn-secondary" onClick={onReturnToDraftSource}>{draftReturnTarget.label}</button>
                {draftReturnTarget.description && <span>{draftReturnTarget.description}</span>}
              </div>
              {authoringNotice && <div className="settings-notice">{authoringNotice}</div>}
              <ScriptEditor initialScript={draftScript} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} onSave={onSaved} onClose={closeCreateEditor} />
            </div>
          </div>
        )
      }
      return <div className="page">{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor initialScript={draftScript} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} onSave={onSaved} onClose={closeCreateEditor} /></div>
    case 'edit':
      return <div className="page">{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor scriptId={editingId} initialScript={editingScript} projectId={projectId} onSave={onSaved} onClose={onOpenLibrary} /></div>
    case 'import':
      return (
        <div className="scripts-authoring-stack">
          {renderEnterpriseArchiveImportSurface(false)}
        </div>
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
    case 'salesforce-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="salesforce" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'recorder-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="recorder-export" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'openapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="openapi" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'raml-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="raml" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'postman-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="postman" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'wsdl-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="wsdl" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    case 'asyncapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="asyncapi" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        onOpenCreateOptions,
      )
    default:
      return null
  }
}
