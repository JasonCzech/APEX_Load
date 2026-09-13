import type { ScriptActionDefinition, ScriptParameterDefinition } from '../types/scripts'
import type { ActionEditorParameterDraft } from './scriptEditorState'
import { ActionParameterMenu } from './ScriptEditorActionEditor'
import { useActionParameterMenu } from './useActionParameterMenu'

export function ActionBodyWorkspace({ action, index, path, parameters, onUpdate, onParameterize }: {
  action: ScriptActionDefinition; index: number; path: string; parameters: ScriptParameterDefinition[]
  onUpdate: (field: keyof ScriptActionDefinition, value: unknown) => void
  onParameterize: (draft: ActionEditorParameterDraft) => void
}) {
  const { selectionState, openSelectionMenu, openParameterEditor } = useActionParameterMenu({ index, parameters, onBeginParameterEdit: onParameterize })
  return <section className="center-document" aria-label="Request body editor"><h3>{action.name} · Request body</h3><p>{path} · Changes are included in Save script.</p>
    <label>Body representation<select value={action.body_encoding || ''} onChange={event => onUpdate('body_encoding', event.target.value || undefined)}><option value="">Plain text</option><option value="base64">Base64-encoded wire bytes</option>{action.body_encoding && action.body_encoding !== 'base64' && <option value={action.body_encoding}>Preserved: {action.body_encoding}</option>}</select></label>
    <label>Request body<textarea className="body-textarea request-body-workspace" value={action.body || ''} onContextMenu={openSelectionMenu} onChange={event => onUpdate('body', event.target.value)} /></label>
    <ActionParameterMenu selectionState={selectionState} onOpenParameterEditor={openParameterEditor} />
  </section>
}
