import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { fieldUpdate } from './catalogActionFormModel'
import { authorFocusFromKey, resolveAuthorJsonScopeByKey, updateNestedAction } from './scriptEditorJsonModel'

export interface CatalogFieldDraft {
  text: string
  error: string
  value?: unknown
}

interface FieldDraftContext {
  drafts: Record<string, CatalogFieldDraft>
  disabled: boolean
  sourcePending: boolean
  change: (field: string, draft: CatalogFieldDraft | null) => void
}

export const CatalogFieldDraftContext = createContext<FieldDraftContext | null>(null)

// Standalone forms keep their own drafts. The script editor supplies a registry
// so changing the selected action cannot discard an unresolved field silently.
export function useCatalogFieldDraft(field: string, value: unknown) {
  const owner = useContext(CatalogFieldDraftContext)
  const [local, setLocal] = useState<CatalogFieldDraft | null>(null)
  const valueKey = JSON.stringify(value)
  useEffect(() => { setLocal(null) }, [valueKey])
  return {
    draft: owner ? owner.drafts[field] ?? null : local,
    disabled: owner?.disabled ?? false,
    change: (draft: CatalogFieldDraft | null) => {
      if (owner) owner.change(field, draft)
      else setLocal(draft)
    },
  }
}

interface Entry extends CatalogFieldDraft {
  actionKey: string
  actionName: string
  action: ScriptActionDefinition
  field: string
}

function entryProblem(entry: Entry, script: ScriptDefinition): string {
  // A path alone is not identity: a rename, replacement or reorder may put a
  // different action here. Keep the unresolved edit until deliberate discard;
  // never transfer its text to an action that merely has the same name/type.
  if (resolveAuthorJsonScopeByKey(script, entry.actionKey)?.action !== entry.action) {
    return 'The action changed while this field was being edited. Discard field edits and enter the value again.'
  }
  return entry.error
}

// One owner for pending catalog text: dirty state, save/validate/replay admission,
// and exact application to the resolved document all consult this registry.
export function useCatalogFieldDrafts(script: ScriptDefinition, onEdit: () => void) {
  const [entries, setEntries] = useState<Record<string, Entry>>({})
  const entriesRef = useRef(entries)
  const identities = useRef(new WeakMap<ScriptActionDefinition, number>())
  const nextIdentity = useRef(0)
  const replace = useCallback((next: Record<string, Entry>) => {
    entriesRef.current = next
    setEntries(next)
  }, [])
  const clear = useCallback(() => replace({}), [replace])
  const current = Object.values(entries)
  const error = current.find(entry => entryProblem(entry, script))
  const problem = error ? `${error.actionName || 'Action'} · ${error.field}: ${entryProblem(error, script)}` : ''
  const forAction = (actionKey: string, action: ScriptActionDefinition, { disabled = false, sourcePending = false }: { disabled?: boolean; sourcePending?: boolean } = {}): FieldDraftContext => ({
    drafts: Object.fromEntries(current.filter(entry => entry.actionKey === actionKey && entry.action === action).map(entry => [entry.field, entry])),
    disabled,
    sourcePending,
    change: (field, draft) => {
      if (disabled) return
      if (!identities.current.has(action)) identities.current.set(action, ++nextIdentity.current)
      const key = `${identities.current.get(action)}\u0000${field}`
      const next = { ...entriesRef.current }
      if (draft) {
        next[key] = { ...draft, actionKey, actionName: action.name, action, field }
        onEdit()
      } else delete next[key]
      replace(next)
    },
  })
  const resolve = (base: ScriptDefinition): ScriptDefinition => {
    const pending = Object.values(entriesRef.current)
    const invalid = pending.find(entry => entryProblem(entry, base))
    if (invalid) throw new Error(`Fix ${invalid.actionName || 'action'} · ${invalid.field}: ${entryProblem(invalid, base)}`)
    let result = base
    for (const entry of pending) {
      const focus = authorFocusFromKey(entry.actionKey)!
      const collection = focus.phase === 'action' ? 'actions' : focus.phase
      result = { ...result, [collection]: (result[collection] || []).map((action, index) => index !== focus.actionIndex ? action : updateNestedAction(action, focus.stepPath, leaf => {
        const change = fieldUpdate(leaf, entry.field, entry.value)
        const next = { ...leaf }
        if (change.value === undefined) delete next[change.key]
        else Object.assign(next, { [change.key]: change.value })
        return next
      })) }
    }
    return result
  }
  return { count: current.length, problem, clear, forAction, resolve, hasPending: () => Object.keys(entriesRef.current).length > 0 }
}
