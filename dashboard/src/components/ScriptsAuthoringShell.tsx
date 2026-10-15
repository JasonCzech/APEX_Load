import { lazy, Suspense, type ReactNode, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigationGuard, type NavigationGuard } from '../hooks/NavigationGuardProvider'

import { type Project, type StoredScript } from '../types'
import ScriptCreateOptions, { type CreateWorkflow, type StartNewCategory } from './ScriptCreateOptions'
import type { ScriptsAuthoringSurfaceProps } from './ScriptsAuthoringSurface'
import { type AuthoringState, type ImportedSaveResult, type ScriptsCreateOptionActions } from './useScriptsAuthoringState'
import LazyLoadBoundary from './LazyLoadBoundary'
import LoadingState from './ux/LoadingState'

const ScriptsAuthoringSurface = lazy(() => import('./ScriptsAuthoringSurface'))

/**
 * Chosen content (a template, a block, a recording) is unsaved from the first
 * render, but the editor that guards it may still be loading: until it does,
 * leaving asks here (it cannot be saved yet, only kept or discarded).
 */
function LoadingDraftGuard({ name }: { name: string }) {
  const guard = useMemo<NavigationGuard>(() => ({
    id: 'scripts-loading-draft',
    label: name || 'New script',
    isDirty: () => true,
    save: () => false,
    discard: () => {},
    canSave: false,
  }), [name])
  useNavigationGuard(guard, true)
  return null
}

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
  onEditDraft: ScriptsAuthoringSurfaceProps['onEditDraft']
  onReturnToDraftSource: () => void
  onStartExistingScript?: (script: StoredScript, options?: { migrateSecrets?: boolean }) => void
  canManageScripts?: boolean
  canMigrateSecrets?: boolean
  /** The builder's workflow and Start new tab, and the recorder's method, when the page holds them (to route them). */
  createWorkflow?: CreateWorkflow
  onCreateWorkflowChange?: (workflow: CreateWorkflow) => void
  createCategory?: StartNewCategory
  onCreateCategoryChange?: (category: StartNewCategory) => void
  recordMethod?: ScriptsAuthoringSurfaceProps['recordMethod']
  onRecordMethodChange?: ScriptsAuthoringSurfaceProps['onRecordMethodChange']
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
  onEditDraft,
  onReturnToDraftSource,
  onStartExistingScript,
  canManageScripts = true,
  canMigrateSecrets = false,
  createWorkflow,
  onCreateWorkflowChange,
  createCategory,
  onCreateCategoryChange,
  recordMethod,
  onRecordMethodChange,
  children,
}: Props) {
  const [createOptionsWorkflow, setCreateOptionsWorkflow] = useState<CreateWorkflow>(initialCreateWorkflow || 'start-new')
  // Kept here, outside the builder, so closing a draft returns to the tab it came from.
  const [createOptionsCategory, setCreateOptionsCategory] = useState<StartNewCategory>('editors')
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
  // The page holds them when it routes them; this shell's own state otherwise.
  const activeWorkflow = createWorkflow ?? createOptionsWorkflow
  const changeWorkflow = onCreateWorkflowChange ?? changeCreateOptionsWorkflow
  const activeCategory = createCategory ?? createOptionsCategory
  const changeCategory = onCreateCategoryChange ?? setCreateOptionsCategory

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
        workflow={activeWorkflow}
        onWorkflowChange={changeWorkflow}
        category={activeCategory}
        onCategoryChange={changeCategory}
      />
    )
  }

  if (authoringState.subView !== 'list') {
    return (
      <LazyLoadBoundary title="Authoring tools could not be loaded.">
        <Suspense fallback={<>
          {authoringState.subView === 'create' && authoringState.draftInitiallyDirty && <LoadingDraftGuard name={authoringState.draftScript?.name || ''} />}
          <LoadingState what="authoring tools" />
        </>}>
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
            editingWorkspace={authoringState.editingWorkspace}
            projectId={projectId}
            activeProject={activeProject}
            libraryScripts={scripts}
            onSaved={onSaved}
            onOpenLibrary={onOpenLibrary}
            onOpenCreateOptions={onOpenCreateOptions}
            onImported={onImported}
            onEditDraft={onEditDraft}
            onReturnToDraftSource={onReturnToDraftSource}
            recordMethod={recordMethod}
            onRecordMethodChange={onRecordMethodChange}
          />
        </Suspense>
      </LazyLoadBoundary>
    )
  }

  return <>{children}</>
}
