import { useState, useEffect, useCallback, type MouseEvent as ReactMouseEvent } from 'react'
import type { ScriptParameterDefinition as ParameterConfig } from '../types/scripts'
import type { ActionEditorParameterDraft } from './scriptEditorState'
import type { ActionEditorSelectionState } from './scriptEditorJsonModel'
import { sanitizeVarName } from './scriptEditorUtils'

export function useActionParameterMenu({
  index,
  onBeginParameterEdit,
  parameters,
}: {
  index: number
  onBeginParameterEdit: (draft: ActionEditorParameterDraft) => void
  parameters: ParameterConfig[]
}) {
  const [selectionState, setSelectionState] = useState<ActionEditorSelectionState | null>(null)

  useEffect(() => {
    if (!selectionState) return
    const closeMenu = () => setSelectionState(null)
    window.addEventListener('click', closeMenu)
    return () => window.removeEventListener('click', closeMenu)
  }, [selectionState])

  const detectSelectedParameter = useCallback((selectedText: string): ParameterConfig | null => {
    const match = selectedText.trim().match(/^{{\s*param\.([a-zA-Z0-9_]+)\s*}}$/)
    if (!match) return null
    const normalizedName = sanitizeVarName(match[1])
    return parameters.find(parameter => sanitizeVarName(parameter.name || '') === normalizedName) || null
  }, [parameters])

  const openSelectionMenu = useCallback((event: ReactMouseEvent<HTMLTextAreaElement>) => {
    const textarea = event.currentTarget
    const start = textarea.selectionStart ?? 0
    const end = textarea.selectionEnd ?? 0
    const selectedText = (textarea.value || '').slice(start, end)
    if (!selectedText.trim()) {
      setSelectionState(null)
      return
    }

    event.preventDefault()
    const existingParameter = detectSelectedParameter(selectedText)
    setSelectionState({
      start,
      end,
      selectedText,
      x: event.clientX,
      y: event.clientY,
      existingName: existingParameter?.name || null,
    })
  }, [detectSelectedParameter])

  const openParameterEditor = useCallback((nextExistingName?: string | null) => {
    if (!selectionState) return
    const existingParameter = nextExistingName
      ? parameters.find(parameter => sanitizeVarName(parameter.name || '') === sanitizeVarName(nextExistingName))
      : detectSelectedParameter(selectionState.selectedText)

    const requestedName = existingParameter?.name
      || sanitizeVarName(selectionState.selectedText.replace(/^{{\s*param\.|\s*}}$/g, ''))
    const seedValue = existingParameter?.values?.[0] || selectionState.selectedText

    setSelectionState(null)
    onBeginParameterEdit({
      source: 'action',
      actionIndex: index,
      start: selectionState.start,
      end: selectionState.end,
      selectedText: selectionState.selectedText,
      existingName: existingParameter?.name || null,
      requestedName,
      seedValue,
    })
  }, [detectSelectedParameter, index, onBeginParameterEdit, parameters, selectionState])

  return { openParameterEditor, openSelectionMenu, selectionState }
}
