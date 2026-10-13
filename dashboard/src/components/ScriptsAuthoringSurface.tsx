import type { ScriptAuthoringBaseline, ScriptSaveOptions } from '../types/storedContent'
import ObjectHistory from './ObjectHistory'
import type { ObjectActivitySummary } from '../types/objectActivity'
import { lazy, useEffect, useRef, useState, type ReactNode } from 'react'
import { type ScriptDefinition } from '../types/scripts'
import { Project, type StoredScript } from '../types'
import { type ImportKind } from './ProtocolAssetImport'
import { RECORDING_STUDIO_RETURN_TARGET, type AuthoringReturnTarget, type DraftEditorFocus, type ImportedSaveResult, type ScriptsAuthoringSubView } from './useScriptsAuthoringState'
import LazyLoadBoundary from './LazyLoadBoundary'
import { ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID } from './enterpriseArchiveReviewOutcome'
import ImportDestinationFolder from './ImportDestinationFolder'

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
  editingAuthoring?: ScriptAuthoringBaseline | null
  editingRetainedDraft?: ScriptDefinition | null
  editingRetainedNeedsReview?: boolean
  editingRetainedJsonText?: string
  editingRetainedCreatedSecretNames?: string[]
  editingScript: ScriptDefinition | null
  editingActivity?: ObjectActivitySummary
  projectId?: string
  // folderId is the destination folder for scripts saved from the authoring
  // surface. The server binds it on create and re-validates it when a review
  // session is approved; omitting it saves to project root.
  folderId?: string | null
  activeProject?: Project | null
  /** The project's library, used to warn when a name repeats within its folder. */
  libraryScripts?: Array<Pick<StoredScript, 'id' | 'name' | 'folder_id'>>
  onSaved: (script: StoredScript, options?: ScriptSaveOptions) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: (result: ImportedSaveResult) => void
  onEditDraft: (script: ScriptDefinition, focus?: DraftEditorFocus, returnTarget?: AuthoringReturnTarget | null, options?: { initiallyDirty?: boolean }) => void
  onReturnToDraftSource: () => void
}

function createOptionsSurface(
  content: ReactNode,
  hidden: boolean,
  destination: ReactNode,
  destinationOperationBusy = false,
) {
  return (
    <div className="page" hidden={hidden}>
      {destination && <details className="import-destination-disclosure"><summary>Save destination — review folder before saving</summary>{destination}</details>}
      <fieldset
        className="import-operation-gate"
        disabled={destinationOperationBusy}
        aria-label="Script import controls"
        aria-busy={destinationOperationBusy}
      >
        {content}
      </fieldset>
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
    case 'cloudtest':
      return 'cloudtest'
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
  editingAuthoring,
  editingRetainedDraft,
  editingRetainedNeedsReview,
  editingRetainedJsonText,
  editingRetainedCreatedSecretNames,
  editingActivity,
  draftInitiallyDirty,
  projectId,
  folderId,
  libraryScripts = [],
  activeProject,
  onSaved,
  onOpenLibrary,
  onOpenCreateOptions,
  onImported,
  onEditDraft,
  onReturnToDraftSource,
}: ScriptsAuthoringSurfaceProps) {
  // Nothing supplies the folderId prop today: ScriptsAuthoringShell, the only
  // caller does not pass one, and the current authoring surface has no folder
  // hierarchy selector. Every import
  // and recorder surface already accepted a destination folder, so the plumbing
  // existed while silently always resolving to the project root. This state, fed
  // by the control below, is what makes that plumbing do anything. The prop is
  // kept as the initial value so a caller can seed it later.
  const [destinationFolderId, setDestinationFolderId] = useState(folderId ?? '')
  const [importOperationBusy, setImportOperationBusy] = useState(false)
  const [destinationOperationBusy, setDestinationOperationBusy] = useState(false)
  useEffect(() => {
    setDestinationFolderId(folderId ?? '')
  }, [folderId, projectId])
  const destinationFolderControl = (
    <ImportDestinationFolder
      projectId={projectId}
      value={destinationFolderId}
      onChange={setDestinationFolderId}
      disabled={importOperationBusy}
      onOperationBusyChange={setDestinationOperationBusy}
    />
  )
  // Empty string is "project root"; the save APIs expect null rather than "".
  const destinationFolderValue = destinationFolderId || null
  // An edited script's folder comes from the library list; when it is not
  // listed the folder is unknown, so no namesake is reported rather than a wrong one.
  const editedEntry = subView === 'edit' ? libraryScripts.find(script => script.id === editingId) : undefined
  const nameFolder = subView === 'edit' ? editedEntry?.folder_id ?? null : destinationFolderValue
  const takenScriptNames = subView === 'edit' && !editedEntry
    ? []
    : libraryScripts.filter(script => script.id !== editingId && (script.folder_id || null) === (nameFolder || null)).map(script => script.name)
  const renderCreateOptionsSurface = (content: ReactNode, hidden = false) =>
    createOptionsSurface(content, hidden, destinationFolderControl, destinationOperationBusy)
  const enterpriseArchiveEditReturnTarget: AuthoringReturnTarget = {
    subView: 'import',
    importLauncher: 'enterprise_archive',
    label: 'Back to Converted Script',
    description: 'Return to the converted enterprise script archive review without losing the current import results.',
    retainedGuardIds: [ENTERPRISE_ARCHIVE_REVIEW_GUARD_ID],
  }
  // The archive review's record of a draft it opened in the editor.
  const archiveDraftSavedRef = useRef<((saved: StoredScript) => void) | null>(null)
  const openEnterpriseArchiveDraftInEditor = (script: ScriptDefinition, focus?: DraftEditorFocus, onDraftSaved?: (saved: StoredScript) => void) => {
    archiveDraftSavedRef.current = onDraftSaved || null
    onEditDraft(script, focus, enterpriseArchiveEditReturnTarget)
  }
  // A converted draft saved from the editor goes back to the archive review,
  // which counts it as saved and moves on to the next draft. A request to set
  // up the script's secrets is kept: the library's secrets dialog opens when
  // the user is back in the library, as for any new script. Edits made while
  // the save was in flight open the saved script instead, as any other save
  // does; the review still counts the draft as saved.
  const saveArchiveDraft = (saved: StoredScript, options?: ScriptSaveOptions) => {
    const recordSave = archiveDraftSavedRef.current
    archiveDraftSavedRef.current = null
    recordSave?.(saved)
    if (!recordSave || options?.laterEdits) {
      onSaved(saved, options)
      return
    }
    onReturnToDraftSource()
    onSaved(saved, { returnedToSource: true, ...(options?.configureSecrets ? { configureSecrets: true } : {}) })
  }
  const renderEnterpriseArchiveImportSurface = (hidden = false) => createOptionsSurface(
    <EnterpriseArchiveImport destinationControl={destinationFolderControl} initialFiles={importFiles || undefined} projectId={projectId} folderId={destinationFolderValue} onImported={onImported} onEdit={openEnterpriseArchiveDraftInEditor} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
    hidden,
    null,
    destinationOperationBusy,
  )
  const closeCreateEditor = draftReturnTarget ? onReturnToDraftSource : onOpenLibrary

  switch (subView) {
    case 'create':
      if (draftReturnTarget?.subView === 'import') {
        return (
          <div className="scripts-authoring-stack">
            {renderEnterpriseArchiveImportSurface(true)}
            <div className="page">
              {authoringNotice && <div className="settings-notice">{authoringNotice}</div>}
              <ScriptEditor returnTarget={{ label: draftReturnTarget.label, description: draftReturnTarget.description, retainedGuardIds: draftReturnTarget.retainedGuardIds, onReturn: onReturnToDraftSource }} initialScript={draftScript} initiallyDirty={draftInitiallyDirty} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} folderId={destinationFolderValue} takenScriptNames={takenScriptNames} onSave={saveArchiveDraft} onClose={closeCreateEditor} />
            </div>
          </div>
        )
      }
      return <div className="page">{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor initialScript={draftScript} initiallyDirty={draftInitiallyDirty} initialFocusedActionName={draftEditorFocus?.targetAction || null} initialFocusedSection={draftEditorFocus?.targetSection || null} initialFocusedDataSourceName={draftEditorFocus?.targetDataSource || null} initialFocusedRuntimeArea={draftEditorFocus?.targetRuntimeArea || null} projectId={projectId} folderId={destinationFolderValue} takenScriptNames={takenScriptNames} onSave={onSaved} onClose={closeCreateEditor} /></div>
    case 'edit':
      // History opens from the editor's More menu; an archived script has no editor, so it keeps the panel.
      return <div className="page">{editingId && editingScript?.archived_at && <ObjectHistory key={editingId} projectId={projectId} resourceType="script" resourceId={editingId} activity={editingActivity} />}{editingScript?.archived_at ? <section aria-label="Archived script"><h2>{editingScript.name} — Archived</h2><p>Restore this script from the library before editing or executing it.</p><button className="btn btn-secondary" onClick={onOpenLibrary}>Back to library</button><pre className="object-archive-content">{JSON.stringify(editingScript, null, 2)}</pre></section> : <>{authoringNotice && <div className="settings-notice">{authoringNotice}</div>}<ScriptEditor scriptId={editingId} initialScript={editingScript} initialAuthoring={editingAuthoring} initialRetainedDraft={editingRetainedDraft} initialRetainedNeedsReview={editingRetainedNeedsReview} initialRetainedJsonText={editingRetainedJsonText} initialRetainedCreatedSecretNames={editingRetainedCreatedSecretNames} projectId={projectId} folderId={destinationFolderValue} takenScriptNames={takenScriptNames} onSave={onSaved} onClose={draftReturnTarget ? onReturnToDraftSource : onOpenLibrary} activity={editingActivity} /></>}</div>
    case 'import':
      return (
        <div className="scripts-authoring-stack">
          {renderEnterpriseArchiveImportSurface(false)}
        </div>
      )
    case 'jmeter-import':
      return renderCreateOptionsSurface(
        <JMeterImport initialFile={importFiles?.[0] || null} projectId={projectId} folderId={destinationFolderValue} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
      )
    case 'bruno-import':
      return createOptionsSurface(
        <BrunoImport initialFiles={importFiles || undefined} initialOutputMode={importMode === 'native_requests' ? 'native_requests' : null} projectId={projectId} folderId={destinationFolderValue} onFolderChange={setDestinationFolderId} onImported={onImported} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
        false,
        null,
      )
    case 'recorder':
      return createOptionsSurface(
        // A recording opened in the editor, saved or not, closes back to the
        // studio; one not in the library yet asks before it is discarded.
        <RecordingStudio destinationControl={destinationFolderControl} activeProject={activeProject} folderId={destinationFolderValue} onImported={result => onImported({ ...result, returnTarget: RECORDING_STUDIO_RETURN_TARGET })} onEdit={script => onEditDraft(script, undefined, RECORDING_STUDIO_RETURN_TARGET, { initiallyDirty: true })} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
        false, null, destinationOperationBusy,
      )
    case 'sample':
      return renderCreateOptionsSurface(
        <SampleScriptImport initialFiles={importFiles || undefined} preset={importMode === 'java_http' ? 'java-http' : 'default'} projectId={projectId} folderId={destinationFolderValue} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
      )
    case 'protocol-import':
      if (importLauncher === 'yaml_chooser') {
        return renderCreateOptionsSurface(
          <YAMLImportChooser initialFiles={importFiles || undefined} projectId={projectId} folderId={destinationFolderValue} onFolderChange={setDestinationFolderId} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
        )
      }
      return renderCreateOptionsSurface(
        <ProtocolAssetImport kind={protocolKindForLauncher(importLauncher)} initialFile={importFiles?.[0] || null} initialMode={importMode} projectId={projectId} folderId={destinationFolderValue} onImported={onImported} onEdit={onEditDraft} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
      )
    case 'oracle-review':
      return createOptionsSurface(
        <OracleEBusinessReviewImport projectId={projectId} onClose={onOpenCreateOptions} onOperationBusyChange={setImportOperationBusy} />,
        false,
        null,
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
