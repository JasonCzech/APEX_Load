import { type ComponentProps, type ReactNode, useState } from 'react'

import { type Project, type StoredScript } from '../types'
import ScriptCreateOptions, { type CreateWorkflow } from './ScriptCreateOptions'
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
  const [createOptionsWorkflow, setCreateOptionsWorkflow] = useState<CreateWorkflow>('existing')

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
        onStartWebScript={createOptionActions.startWebScript}
        onOpenRecorder={createOptionActions.openRecorder}
        onStartJmsScript={createOptionActions.startJmsScript}
        onStartKafkaScript={createOptionActions.startKafkaScript}
        onStartTemplateScript={createOptionActions.startTemplateScript}
        onComposeComponent={createOptionActions.composeComponentScript}
        onOpenImportSurface={createOptionActions.openImportSurface}
        scripts={scripts}
        onStartExistingScript={onStartExistingScript}
        workflow={createOptionsWorkflow}
        onWorkflowChange={setCreateOptionsWorkflow}
      />
    )
  }

  if (authoringState.subView !== 'list') {
    return (
      <ScriptsAuthoringSurface
        subView={authoringState.subView}
        importLauncher={authoringState.importLauncher}
        importMode={authoringState.importMode}
        importFiles={authoringState.importFiles}
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
