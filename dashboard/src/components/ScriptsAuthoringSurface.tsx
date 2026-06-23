import { type ReactNode } from 'react'
import { type ScriptDefinition } from '../types/scripts'
import { Project, type StoredScript } from '../types'
import ScriptEditor from './ScriptEditor'
import EnterpriseArchiveImport from './EnterpriseArchiveImport'
import JMeterImport from './JMeterImport'
import RecordingStudio from './RecordingStudio'
import SampleScriptImport from './SampleScriptImport'
import ProtocolAssetImport, { type ImportKind } from './ProtocolAssetImport'
import OracleEBusinessReviewImport from './OracleEBusinessReviewImport'
import { type AuthoringReturnTarget, type DraftEditorFocus, type ImportedSaveResult, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'

interface Props {
  subView: ScriptsAuthoringSubView
  importLauncher?: string | null
  importMode?: string | null
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
  hidden = false,
) {
  return (
    <div className="page" hidden={hidden}>
      {content}
    </div>
  )
}

function protocolKindForLauncher(launcher?: string | null): ImportKind {
  switch (launcher) {
    case 'devweb':
      return 'devweb'
    case 'recorder_export':
      return 'recorder-export'
    case 'openapi':
      return 'openapi'
    case 'raml':
      return 'raml'
    case 'postman':
      return 'postman'
    case 'wsdl':
      return 'wsdl'
    case 'asyncapi':
      return 'asyncapi'
    case 'k6':
      return 'k6'
    case 'eventhubs_capture':
      return 'eventhubs-capture'
    case 'broker_config':
      return 'broker-config'
    case 'traffic_har':
    default:
      return 'har'
  }
}

export default function ScriptsAuthoringSurface({
  subView,
  importLauncher,
  importMode,
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
      )
    case 'recorder':
      return renderCreateOptionsSurface(
        <RecordingStudio activeProject={activeProject} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'sample':
      return renderCreateOptionsSurface(
        <SampleScriptImport preset={importMode === 'java_http' ? 'java-http' : 'default'} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'protocol-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind={protocolKindForLauncher(importLauncher)} initialMode={importMode} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'oracle-review':
      return renderCreateOptionsSurface(
        <OracleEBusinessReviewImport projectId={projectId} onClose={onOpenCreateOptions} />,
      )
    case 'devweb-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="devweb" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'har-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="har" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'recorder-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="recorder-export" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'openapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="openapi" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'raml-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="raml" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'postman-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="postman" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'wsdl-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="wsdl" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'asyncapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="asyncapi" projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    default:
      return null
  }
}
