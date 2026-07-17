import { lazy, type ReactNode } from 'react'
import { type ScriptDefinition } from '../types/scripts'
import { Project, type StoredScript } from '../types'
import { type ImportKind } from './ProtocolAssetImport'
import { type AuthoringReturnTarget, type DraftEditorFocus, type ImportedSaveResult, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'
import LazyLoadBoundary from './LazyLoadBoundary'

const ScriptEditor = lazy(() => import('./ScriptEditor'))
const EnterpriseArchiveImport = lazy(() => import('./EnterpriseArchiveImport'))
const BrunoImport = lazy(() => import('./BrunoImport'))
const JMeterImport = lazy(() => import('./JMeterImport'))
const RecordingStudio = lazy(() => import('./RecordingStudio'))
const SampleScriptImport = lazy(() => import('./SampleScriptImport'))
const ProtocolAssetImport = lazy(() => import('./ProtocolAssetImport'))
const OracleEBusinessReviewImport = lazy(() => import('./OracleEBusinessReviewImport'))
const YAMLImportChooser = lazy(() => import('./YAMLImportChooser'))

export interface ScriptsAuthoringSurfaceProps {
  subView: ScriptsAuthoringSubView
  importLauncher?: string | null
  importMode?: string | null
  draftScript: ScriptDefinition | null
  draftInitiallyDirty: boolean
  draftEditorFocus: DraftEditorFocus | null
  draftReturnTarget: AuthoringReturnTarget | null
  authoringNotice: string | null
  importFiles?: File[] | null
  editingId: string | null
  editingScript: ScriptDefinition | null
  projectId?: string
  activeProject?: Project | null
  onSaved: (script: StoredScript) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: (result: ImportedSaveResult) => void
  onConfigureSecrets?: (projectId: string) => void
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

function ScriptsAuthoringContent({
  subView,
  importLauncher,
  importMode,
  draftScript,
  draftEditorFocus,
  draftReturnTarget,
  authoringNotice,
  importFiles,
  editingId,
  editingScript,
  draftInitiallyDirty,
  projectId,
  activeProject,
  onSaved,
  onOpenLibrary,
  onOpenCreateOptions,
  onImported,
  onConfigureSecrets,
  onEditDraft,
  onReturnToDraftSource,
}: ScriptsAuthoringSurfaceProps) {
  const enterpriseArchiveEditReturnTarget: AuthoringReturnTarget = {
    subView: 'import',
    label: 'Back to Converted Script',
    description: 'Return to the converted enterprise script archive review without losing the current import results.',
  }
  const openEnterpriseArchiveDraftInEditor = (script: ScriptDefinition, focus?: DraftEditorFocus) => {
    onEditDraft(script, focus, enterpriseArchiveEditReturnTarget)
  }
  const renderEnterpriseArchiveImportSurface = (hidden = false) => renderCreateOptionsSurface(
    <EnterpriseArchiveImport initialFiles={importFiles || undefined} projectId={projectId} onImported={onImported} onEdit={openEnterpriseArchiveDraftInEditor} onClose={onOpenCreateOptions} />,
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
              <ScriptEditor initialScript={draftScript} initiallyDirty={draftInitiallyDirty} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} onSave={onSaved} onClose={closeCreateEditor} />
            </div>
          </div>
        )
      }
      return <div className="page">{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor initialScript={draftScript} initiallyDirty={draftInitiallyDirty} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} onSave={onSaved} onClose={closeCreateEditor} /></div>
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
        <JMeterImport initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'bruno-import':
      return renderCreateOptionsSurface(
        <BrunoImport initialFiles={importFiles || undefined} projectId={projectId} onImported={onImported} onConfigureSecrets={onConfigureSecrets} onClose={onOpenCreateOptions} />,
      )
    case 'recorder':
      return renderCreateOptionsSurface(
        <RecordingStudio activeProject={activeProject} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'sample':
      return renderCreateOptionsSurface(
        <SampleScriptImport initialFiles={importFiles || undefined} preset={importMode === 'java_http' ? 'java-http' : 'default'} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'protocol-import':
      if (importLauncher === 'yaml_chooser') {
        return renderCreateOptionsSurface(
          <YAMLImportChooser initialFiles={importFiles || undefined} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
        )
      }
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind={protocolKindForLauncher(importLauncher)} initialFile={importFiles?.[0] || null} initialMode={importMode} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'oracle-review':
      return renderCreateOptionsSurface(
        <OracleEBusinessReviewImport projectId={projectId} onClose={onOpenCreateOptions} />,
      )
    case 'devweb-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="devweb" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'har-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="har" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'recorder-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="recorder-export" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'openapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="openapi" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'raml-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="raml" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'postman-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="postman" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'wsdl-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="wsdl" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    case 'asyncapi-import':
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind="asyncapi" initialFile={importFiles?.[0] || null} projectId={projectId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} />,
      )
    default:
      return null
  }
}

export default function ScriptsAuthoringSurface(props: ScriptsAuthoringSurfaceProps) {
  return (
    <LazyLoadBoundary title="Authoring tools could not be loaded.">
      <ScriptsAuthoringContent {...props} />
    </LazyLoadBoundary>
  )
}
