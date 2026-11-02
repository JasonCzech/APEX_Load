import { createContext, useCallback, useRef, useState } from 'react'
import type { ScriptDebugBreakpoint } from '../types/replay'
import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import {
  applyAuraEnvelopeDrafts,
  formatAuraActionTitle,
  hasAuraEnvelopeDrafts,
  parseAuraFormEnvelope,
  type AuraEnvelopeDrafts,
} from './auraEnvelopeModel'
import { authorFocusFromKey, authorFocusKey, resolveAuthorJsonScopeByKey, updateNestedAction } from './scriptEditorJsonModel'

export interface AuraEnvelopeDraftOwner {
  drafts: AuraEnvelopeDrafts
  /** The step's body changed after these drafts were typed (a raw edit, undo, Script JSON). */
  stale: boolean
  /** Why the drafts are stale and how to keep them, when they are. */
  staleMessage: string
  /**
   * Replace the step's drafts. `baseBody` is the body they now apply to: the
   * editor passes the body it just wrote when Apply or a field edit changes it.
   */
  change: (drafts: AuraEnvelopeDrafts, baseBody?: string) => void
}

// A standalone editor keeps its own drafts. The script editor supplies a
// registry so Save script, replay and the unsaved-changes prompt see edits
// that were typed but not applied, and selecting another step or switching
// to Raw form body keeps them.
export const AuraEnvelopeDraftContext = createContext<AuraEnvelopeDraftOwner | null>(null)

interface Entry {
  /** Stays with the drafts when a structural edit or undo moves them. */
  id: number
  actionKey: string
  actionName: string
  /** The body the drafts were typed against. */
  baseBody: string
  drafts: AuraEnvelopeDrafts
  /**
   * Steps were added or removed by an edit the drafts could not follow (an
   * applied Script JSON draft), so their place may now hold another step.
   */
  orphaned?: boolean
}

/** Unapplied Aura edits as an undo/redo history entry records them. */
export type AuraEnvelopeDraftSnapshot = Record<string, Entry>

let nextEntryId = 1

const STALE_BODY = 'The request body changed while Aura envelope edits were pending. Discard Aura edits and enter them again.'
const STALE_LAYOUT = 'Steps were added or removed in Script JSON while Aura envelope edits were pending, so they may now point at another step. Undo that change to keep them, or discard Aura edits and enter them again.'
// An unapplied Script JSON draft lays steps out differently: the drafts are
// intact, and resetting the draft makes them apply again.
const STALE_DRAFT_LAYOUT = 'The unapplied Script JSON draft adds or removes steps, so Aura envelope edits may point at another step in it. Reset the Script JSON draft to keep them, or discard Aura edits.'
const STALE_BODY_VIEW = 'The request body changed after these Aura envelope edits were typed, so they no longer apply to it.'

/** Removed drafts the undo history should not bring back, as `restore` reports them. */
export interface AuraEnvelopeDraftRestore {
  /** Steps whose drafts the restored document state has no place for. */
  discarded: string[]
  /** Steps whose drafts went back to the text that state recorded. */
  reverted: string[]
}

// Reset and Apply work on one sub-action's parameters (or the context) at a
// time, so what the user removed is kept per draft slot, not per entry.
const ALL_SLOTS = '*'

function draftSlots(drafts: AuraEnvelopeDrafts): string[] {
  return [...Object.keys(drafts.params).map(index => `params:${index}`), ...(drafts.context !== null ? ['context'] : [])]
}

/** `entry` without the slots the user removed, or null when none remain. */
function withoutRemoved(entry: Entry, removed: Set<string> | undefined): Entry | null {
  if (!removed) return structuredClone(entry)
  if (removed.has(ALL_SLOTS)) return null
  const params = Object.fromEntries(Object.entries(entry.drafts.params).filter(([index]) => !removed.has(`params:${index}`)))
  const drafts = { params, context: removed.has('context') ? null : entry.drafts.context }
  return hasAuraEnvelopeDrafts(drafts) ? structuredClone({ ...entry, drafts }) : null
}

function stepListLayout(steps: unknown): string {
  return Array.isArray(steps) ? `[${steps.map(stepLayout).join(',')}]` : '-'
}

function stepLayout(step: unknown): string {
  if (!step || typeof step !== 'object') return '.'
  const action = step as { params?: { requests?: unknown }; control?: Record<string, unknown> }
  const parts: string[] = []
  if (Array.isArray(action.params?.requests)) parts.push(`r${stepListLayout(action.params.requests)}`)
  const control = action.control
  if (control && typeof control === 'object') {
    for (const field of ['steps', 'else', 'default'] as const) {
      if (Array.isArray(control[field])) parts.push(`${field}${stepListLayout(control[field])}`)
    }
    for (const field of ['cases', 'branches'] as const) {
      const groups = control[field]
      if (Array.isArray(groups)) parts.push(`${field}[${groups.map(group => stepListLayout((group as { steps?: unknown } | null)?.steps)).join(',')}]`)
    }
  }
  return parts.length ? `{${parts.join(';')}}` : '.'
}

/**
 * Where every step sits (phases, nesting and counts, not content). Drafts
 * are keyed by a step's place, so two documents with different layouts may
 * hold different steps under the same key.
 */
export function scriptStepLayout(script: ScriptDefinition): string {
  return [script.init, script.actions, script.teardown].map(stepListLayout).join('|')
}

function entryOutcome(entry: Entry, script: ScriptDefinition, layoutChanged = false): { body: string } | { problem: string } {
  if (entry.orphaned) return { problem: STALE_LAYOUT }
  if (layoutChanged) return { problem: STALE_DRAFT_LAYOUT }
  const action = resolveAuthorJsonScopeByKey(script, entry.actionKey)?.action
  // Drafts index the sub-actions of the body they were typed on: on any other
  // body they could land on a different sub-action, so they never transfer.
  if (!action || (action.body ?? '') !== entry.baseBody) return { problem: STALE_BODY }
  const envelope = parseAuraFormEnvelope(entry.baseBody)
  if (!envelope) return { problem: STALE_BODY }
  const result = applyAuraEnvelopeDrafts(envelope, entry.drafts)
  if ('body' in result) return result
  if (result.context) return { problem: `Aura context: ${result.error}` }
  const subAction = result.actionIndex !== undefined ? envelope.actions[result.actionIndex] : undefined
  const label = subAction ? formatAuraActionTitle(subAction, result.actionIndex) : 'Aura action'
  return { problem: `${label} parameters: ${result.error}` }
}

// One owner for unapplied Aura envelope text: dirty state, save/replay
// admission and exact application to the resolved document consult it.
// `script` is the document as authored now (an unapplied Script JSON draft
// included); `placedOn` is the structured script the drafts' keys were
// placed on. While the two lay steps out differently, the keys may name
// other steps, so every draft is reported stale and never applied.
export function useAuraEnvelopeDrafts(script: ScriptDefinition, onEdit: () => void, placedOn: ScriptDefinition = script) {
  const [entries, setEntries] = useState<Record<string, Entry>>({})
  const entriesRef = useRef(entries)
  const placedOnRef = useRef(placedOn)
  placedOnRef.current = placedOn
  // Draft slots the user removed (Reset, Discard), by entry id: undo never
  // brings them back.
  const removedSlots = useRef(new Map<number, Set<string>>())
  const markRemoved = (id: number, slots: string[]) => {
    const removed = removedSlots.current.get(id) ?? new Set<string>()
    for (const slot of slots) removed.add(slot)
    removedSlots.current.set(id, removed)
  }
  const replace = useCallback((next: Record<string, Entry>) => {
    entriesRef.current = next
    setEntries(next)
  }, [])
  const clear = useCallback(() => replace({}), [replace])
  const layoutChangedFor = (target: ScriptDefinition) => target !== placedOnRef.current && Object.keys(entriesRef.current).length > 0
    && scriptStepLayout(target) !== scriptStepLayout(placedOnRef.current)
  /** Discard one step's drafts, leaving every other step's. */
  const discard = useCallback((actionKey: string) => {
    if (!(actionKey in entriesRef.current)) return
    const next = { ...entriesRef.current }
    markRemoved(next[actionKey].id, [ALL_SLOTS])
    delete next[actionKey]
    replace(next)
  }, [replace])
  /**
   * An edit added or removed steps without saying where they moved (an
   * applied Script JSON draft): every draft may now be keyed to another
   * step, so all are held as stale until discarded or the edit is undone.
   */
  const detach = useCallback(() => {
    const existing = entriesRef.current
    if (!Object.keys(existing).length) return
    replace(Object.fromEntries(Object.entries(existing).map(([key, entry]) => [key, { ...entry, orphaned: true }])))
  }, [replace])
  const snapshot = useCallback((): AuraEnvelopeDraftSnapshot => structuredClone(entriesRef.current), [])
  /**
   * Undo/redo to `restored`: put the drafts back where they were in that
   * document state (`saved`, its record). A draft still pending on the same
   * body keeps its newer text; one whose body was since changed (Apply, a
   * field edit) returns to its recorded text, which matches the restored
   * body, and is reported as reverted. A draft the record lacks stays when
   * the restored document lays steps out as this one does and its step there
   * still has the body it was typed on; otherwise it has no certain place and
   * is discarded. A draft the user removed is never brought back.
   */
  const restore = useCallback((saved: AuraEnvelopeDraftSnapshot | undefined, restored: ScriptDefinition): AuraEnvelopeDraftRestore => {
    const recorded = new Map(Object.entries(saved ?? {}).map(([key, entry]) => [entry.id, { key, entry }]))
    const sameLayout = scriptStepLayout(restored) === scriptStepLayout(placedOnRef.current)
    const next: Record<string, Entry> = {}
    const unrecorded: Entry[] = []
    const reverted: string[] = []
    for (const current of Object.values(entriesRef.current)) {
      const record = recorded.get(current.id)
      if (!record) { unrecorded.push(current); continue }
      recorded.delete(current.id)
      const { key, entry } = record
      if (current.baseBody === entry.baseBody && !current.orphaned) next[key] = { ...current, actionKey: key, orphaned: entry.orphaned }
      else {
        const kept = withoutRemoved(entry, removedSlots.current.get(entry.id))
        if (kept) next[key] = kept
        if (!current.orphaned && JSON.stringify(current.drafts) !== JSON.stringify(kept?.drafts)) reverted.push(current.actionName || 'Action')
      }
    }
    // Recorded drafts since applied come back with the body they were typed
    // on, less any sub-action or context the user reset since.
    for (const { key, entry } of recorded.values()) {
      if (key in next) continue
      const kept = withoutRemoved(entry, removedSlots.current.get(entry.id))
      if (kept) next[key] = kept
    }
    const discarded: string[] = []
    for (const current of unrecorded) {
      const action = !current.orphaned && sameLayout ? resolveAuthorJsonScopeByKey(restored, current.actionKey)?.action : undefined
      if (action && (action.body ?? '') === current.baseBody) next[current.actionKey] = current
      else discarded.push(current.actionName || 'Action')
    }
    replace(next)
    return { discarded, reverted }
  }, [replace])
  // Drafts are kept under their step's place in the script (phase, index and
  // step path), so a structural edit that moves steps (insert, duplicate,
  // move, delete, a nested removal) moves them as it moves breakpoints:
  // `remap` is that edit's breakpoint remapping. Drafts on a removed step go
  // with it; they never pass to whichever step takes its place.
  const follow = useCallback((remap: (locations: ScriptDebugBreakpoint[]) => ScriptDebugBreakpoint[]) => {
    const existing = entriesRef.current
    const locations = Object.keys(existing).flatMap(key => {
      const focus = authorFocusFromKey(key)
      if (!focus) return []
      return [{ phase: focus.phase, action_index: focus.actionIndex, ...(focus.stepPath?.length ? { step_path: [...focus.stepPath] } : {}), draft_key: key }]
    })
    if (!locations.length) return
    const next: Record<string, Entry> = {}
    for (const moved of remap(locations) as Array<ScriptDebugBreakpoint & { draft_key?: string }>) {
      const entry = moved.draft_key ? existing[moved.draft_key] : undefined
      if (!entry) continue
      const stepPath = moved.step_path?.length ? moved.step_path : moved.request_index != null ? [`requests[${moved.request_index}]`] : undefined
      const actionKey = authorFocusKey({ phase: moved.phase as 'init' | 'action' | 'teardown', actionIndex: moved.action_index, stepPath })
      next[actionKey] = { ...entry, actionKey }
    }
    replace(next)
  }, [replace])
  const current = Object.values(entries)
  const layoutChanged = current.length > 0 && script !== placedOn && scriptStepLayout(script) !== scriptStepLayout(placedOn)
  let problem = ''
  let problemKey = ''
  for (const entry of current) {
    const outcome = entryOutcome(entry, script, layoutChanged)
    if ('problem' in outcome) {
      problem = `${entry.actionName || 'Action'} · ${outcome.problem}`
      problemKey = entry.actionKey
      break
    }
  }
  const forAction = (actionKey: string, action: ScriptActionDefinition): AuraEnvelopeDraftOwner => {
    const entry = entries[actionKey]
    const staleMessage = !entry ? ''
      : entry.orphaned ? STALE_LAYOUT
        : layoutChanged ? STALE_DRAFT_LAYOUT
          : entry.baseBody !== (action.body ?? '') ? STALE_BODY_VIEW : ''
    return {
      drafts: entry?.drafts ?? { params: {}, context: null },
      stale: staleMessage !== '',
      staleMessage,
      change: (drafts, baseBody) => {
        const next = { ...entriesRef.current }
        const previous = next[actionKey]
        if (previous) {
          // Without a new body nothing was applied: a slot that went away was
          // reset. One typed again is the user's once more.
          const present = new Set(draftSlots(drafts))
          if (baseBody === undefined) markRemoved(previous.id, draftSlots(previous.drafts).filter(slot => !present.has(slot)))
          const removed = removedSlots.current.get(previous.id)
          for (const slot of present) removed?.delete(slot)
        }
        if (hasAuraEnvelopeDrafts(drafts)) {
          next[actionKey] = { id: previous?.id ?? nextEntryId++, actionKey, actionName: action.name, baseBody: baseBody ?? action.body ?? '', drafts }
          onEdit()
        } else {
          delete next[actionKey]
        }
        replace(next)
      },
    }
  }
  const resolve = <T extends ScriptDefinition>(base: T): T => {
    let result = base
    const relaid = layoutChangedFor(base)
    for (const entry of Object.values(entriesRef.current)) {
      const outcome = entryOutcome(entry, result, relaid)
      if ('problem' in outcome) throw new Error(`Fix ${entry.actionName || 'action'} · ${outcome.problem}`)
      const focus = authorFocusFromKey(entry.actionKey)!
      const collection = focus.phase === 'action' ? 'actions' : focus.phase
      result = { ...result, [collection]: (result[collection] || []).map((action, index) => index !== focus.actionIndex ? action : updateNestedAction(action, focus.stepPath, leaf => ({ ...leaf, body: outcome.body }))) }
    }
    return result
  }
  return { count: current.length, problem, problemKey, clear, detach, discard, follow, forAction, resolve, restore, snapshot, hasPending: () => Object.keys(entriesRef.current).length > 0 }
}
