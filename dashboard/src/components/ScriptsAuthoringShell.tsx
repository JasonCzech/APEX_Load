import { type ComponentProps, type ReactNode } from 'react'

import { type Project, type StoredScript } from '../types'
import ScriptCreateOptions from './ScriptCreateOptions'
import ScriptsAuthoringSurface from './ScriptsAuthoringSurface'
import { type AuthoringState, type ImportedSaveResult, type ScriptsCreateOptionActions } from './useScriptsAuthoringState'

interface Props {
  authoringState: AuthoringState
  projectId?: string
  activeProject?: Project | null
  scripts?: StoredScript[]
  createOptionActions: ScriptsCreateOptionActions
  onSaved: (script: StoredScript) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: (result: ImportedSaveResult) => void
  onEditDraft: NonNullable<ComponentProps<typeof ScriptsAuthoringSurface>['onEditDraft']>
  onReturnToDraftSource: () => void
  onStartExistingScript?: (script: StoredScript) => void
  canManageScripts?: boolean
  children: ReactNode
}

export default function ScriptsAuthoringShell({
  authoringState,
  projectId,
  activeProject,
  scripts = [],
  createOptionActions,
  onSaved,
  onOpenLibrary,
  onOpenCreateOptions,
  onImported,
  onEditDraft,
  onReturnToDraftSource,
  onStartExistingScript,
  canManageScripts = true,
  children,
}: Props) {
  if (!canManageScripts) {
    return <>{children}</>
  }

  if (authoringState.subView === 'create-options') {
    return (
      <ScriptCreateOptions
        onBack={onOpenLibrary}
        onStartApiScript={createOptionActions.startApiScript}
        onStartSoapScript={createOptionActions.startSoapScript}
        onStartJavaHttpScript={createOptionActions.startJavaHttpScript}
        onStartWebSocketScript={createOptionActions.startWebSocketScript}
        onStartGrpcScript={createOptionActions.startGrpcScript}
        onStartDatabaseScript={createOptionActions.startDatabaseScript}
        onStartNetworkScript={createOptionActions.startNetworkScript}
        onOpenJavaHttpImport={createOptionActions.openJavaHttpImport}
        onOpenSampleImport={createOptionActions.openSampleImport}
        onStartWebScript={createOptionActions.startWebScript}
        onOpenRecorder={createOptionActions.openRecorder}
        onStartJmsScript={createOptionActions.startJmsScript}
        onStartKafkaScript={createOptionActions.startKafkaScript}
        onStartTemplateScript={createOptionActions.startTemplateScript}
        onComposeComponent={createOptionActions.composeComponentScript}
        onOpenEnterpriseArchiveImport={createOptionActions.openEnterpriseArchiveImport}
        onOpenJMeterImport={createOptionActions.openJMeterImport}
        onOpenDevWebImport={createOptionActions.openDevWebImport}
        onOpenHARImport={createOptionActions.openHARImport}
        onOpenOpenAPIImport={createOptionActions.openOpenAPIImport}
        onOpenPostmanImport={createOptionActions.openPostmanImport}
        onOpenWSDLImport={createOptionActions.openWSDLImport}
        onOpenAsyncAPIImport={createOptionActions.openAsyncAPIImport}
        scripts={scripts}
        onStartExistingScript={onStartExistingScript}
      />
    )
  }

  if (authoringState.subView !== 'list') {
    return (
      <ScriptsAuthoringSurface
        subView={authoringState.subView}
        draftScript={authoringState.draftScript}
        draftEditorFocus={authoringState.draftEditorFocus}
        draftReturnTarget={authoringState.draftReturnTarget}
        authoringNotice={authoringState.authoringNotice}
        editingId={authoringState.editingId}
        editingScript={authoringState.editingScript}
        projectId={projectId}
        activeProject={activeProject}
        onSaved={onSaved}
        onOpenLibrary={onOpenLibrary}
        onOpenCreateOptions={onOpenCreateOptions}
        onImported={onImported}
        onEditDraft={onEditDraft}
        onReturnToDraftSource={onReturnToDraftSource}
      />
    )
  }

  return <>{children}</>
}
