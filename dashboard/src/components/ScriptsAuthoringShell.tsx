import { lazy, Suspense, type ReactNode, useEffect, useRef, useState } from 'react'

import { type Project, type StoredScript } from '../types'
import ScriptCreateOptions, { type CreateWorkflow } from './ScriptCreateOptions'
import type { ScriptsAuthoringSurfaceProps } from './ScriptsAuthoringSurface'
import { type AuthoringState, type ImportedSaveResult, type ScriptsCreateOptionActions } from './useScriptsAuthoringState'
import LazyLoadBoundary from './LazyLoadBoundary'

const ScriptsAuthoringSurface = lazy(() => import('./ScriptsAuthoringSurface'))

interface Props {
  initialCreateWorkflow?: CreateWorkflow
  authoringState: AuthoringState
  projectId?: string
  policyRefreshSignal?: number
  activeProject?: Project | null
  scripts?: StoredScript[]
  createOptionActions: ScriptsCreateOptionActions
  onSaved: ScriptsAuthoringSurfaceProps['onSaved']
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: (result: ImportedSaveResult) => void
  onConfigureSecrets?: (projectId: string) => void
  onEditDraft: ScriptsAuthoringSurfaceProps['onEditDraft']
  onReturnToDraftSource: () => void
  onStartExistingScript?: (script: StoredScript, options?: { migrateSecrets?: boolean }) => void
  canManageScripts?: boolean
  canMigrateSecrets?: boolean
  children: ReactNode
}

export default function ScriptsAuthoringShell({
  initialCreateWorkflow,
  authoringState,
  projectId,
  policyRefreshSignal,
  activeProject,
  scripts = [],
  createOptionActions,
  onSaved,
  onOpenLibrary,
  onOpenCreateOptions,
  onImported,
  onConfigureSecrets,
  onEditDraft,
  onReturnToDraftSource,
  onStartExistingScript,
  canManageScripts = true,
  canMigrateSecrets = false,
  children,
}: Props) {
  const [createOptionsWorkflow, setCreateOptionsWorkflow] = useState<CreateWorkflow>(initialCreateWorkflow || 'start-new')
  const workflowChosenByUserRef = useRef(false)

  useEffect(() => {
    if (authoringState.subView === 'create-options' && !workflowChosenByUserRef.current) {
      setCreateOptionsWorkflow(initialCreateWorkflow || 'start-new')
    }
  }, [authoringState.subView, scripts.length, initialCreateWorkflow])

  const changeCreateOptionsWorkflow = (workflow: CreateWorkflow) => {
    workflowChosenByUserRef.current = true
    setCreateOptionsWorkflow(workflow)
  }

  if (!canManageScripts) {
    return <>{children}</>
  }

  if (authoringState.subView === 'create-options') {
    return (
      <ScriptCreateOptions
        projectId={projectId}
        policyRefreshSignal={policyRefreshSignal}
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
        canMigrateSecrets={canMigrateSecrets}
        workflow={createOptionsWorkflow}
        onWorkflowChange={changeCreateOptionsWorkflow}
      />
    )
  }

  if (authoringState.subView !== 'list') {
    return (
      <LazyLoadBoundary title="Authoring tools could not be loaded.">
        <Suspense fallback={<div className="page-loading" role="status">Loading authoring tools…</div>}>
          <ScriptsAuthoringSurface
            subView={authoringState.subView}
            importLauncher={authoringState.importLauncher}
            importMode={authoringState.importMode}
            importFiles={authoringState.importFiles}
            draftScript={authoringState.draftScript}
            draftInitiallyDirty={authoringState.draftInitiallyDirty}
            draftEditorFocus={authoringState.draftEditorFocus}
            draftReturnTarget={authoringState.draftReturnTarget}
            authoringNotice={authoringState.authoringNotice}
            editingId={authoringState.editingId}
            editingScript={authoringState.editingScript}
            editingAuthoring={authoringState.editingAuthoring}
            editingRetainedDraft={authoringState.editingRetainedDraft}
            editingRetainedNeedsReview={authoringState.editingRetainedNeedsReview}
            editingRetainedJsonText={authoringState.editingRetainedJsonText}
            editingRetainedCreatedSecretNames={authoringState.editingRetainedCreatedSecretNames}
            editingActivity={authoringState.editingActivity}
            projectId={projectId}
            activeProject={activeProject}
            onSaved={onSaved}
            onOpenLibrary={onOpenLibrary}
            onOpenCreateOptions={onOpenCreateOptions}
            onImported={onImported}
            onConfigureSecrets={onConfigureSecrets}
            onEditDraft={onEditDraft}
            onReturnToDraftSource={onReturnToDraftSource}
          />
        </Suspense>
      </LazyLoadBoundary>
    )
  }

  return <>{children}</>
}
