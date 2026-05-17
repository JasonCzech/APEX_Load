import { type ComponentProps, type ReactNode } from 'react'

import { type Project, type StoredScript } from '../types'
import ScriptCreateOptions from './ScriptCreateOptions'
import ScriptsAuthoringSurface from './ScriptsAuthoringSurface'
import { type AuthoringState, type ScriptsCreateOptionActions } from './useScriptsAuthoringState'

interface Props {
  authoringState: AuthoringState
  projectId?: string
  activeProject?: Project | null
  createOptionActions: ScriptsCreateOptionActions
  onSaved: (script: StoredScript) => void
  onOpenLibrary: () => void
  onOpenCreateOptions: () => void
  onImported: () => void
  onEditDraft: NonNullable<ComponentProps<typeof ScriptsAuthoringSurface>['onEditDraft']>
  children: ReactNode
}

export default function ScriptsAuthoringShell({
  authoringState,
  projectId,
  activeProject,
  createOptionActions,
  onSaved,
  onOpenLibrary,
  onOpenCreateOptions,
  onImported,
  onEditDraft,
  children,
}: Props) {
  if (authoringState.subView === 'create-options') {
    return (
      <ScriptCreateOptions
        onBack={onOpenLibrary}
        onStartApiScript={createOptionActions.startApiScript}
        onStartSoapScript={createOptionActions.startSoapScript}
        onStartJavaHttpScript={createOptionActions.startJavaHttpScript}
        onOpenJavaHttpImport={createOptionActions.openJavaHttpImport}
        onOpenSampleImport={createOptionActions.openSampleImport}
        onStartWebScript={createOptionActions.startWebScript}
        onOpenRecorder={createOptionActions.openRecorder}
        onStartJmsScript={createOptionActions.startJmsScript}
        onOpenLoadRunnerImport={createOptionActions.openLoadRunnerImport}
        onOpenJMeterImport={createOptionActions.openJMeterImport}
      />
    )
  }

  if (authoringState.subView !== 'list') {
    return (
      <ScriptsAuthoringSurface
        subView={authoringState.subView}
        draftScript={authoringState.draftScript}
        draftEditorFocus={authoringState.draftEditorFocus}
        editingId={authoringState.editingId}
        editingScript={authoringState.editingScript}
        projectId={projectId}
        activeProject={activeProject}
        onSaved={onSaved}
        onOpenLibrary={onOpenLibrary}
        onOpenCreateOptions={onOpenCreateOptions}
        onImported={onImported}
        onEditDraft={onEditDraft}
      />
    )
  }

  return <>{children}</>
}
