import type { ScriptActionDefinition as Action, ScriptDefinition } from '../types/scripts'
import type { IssueDetail } from './IssueDetailsDialog'
import { rawHttpSequenceRequest } from './scriptActionHelpers'
import { jsonPathForActionStepPath, phaseActionsForScript, resolveNestedAction, sequenceStepPaths, updateNestedAction, type AuthorCodeFocus } from './scriptEditorJsonModel'

export function humanActionType(type: string) {
  return type.replace(/^control_/, '').replace(/_/g, ' ').replace(/\b\w/g, letter => letter.toUpperCase())
}

export function actionBreadcrumb(script: ScriptDefinition, focus: AuthorCodeFocus) {
  const root = phaseActionsForScript(script, focus.phase)[focus.actionIndex]
  const names = [focus.phase === 'action' ? 'Actions' : humanActionType(focus.phase), root?.name || `Action ${focus.actionIndex + 1}`]
  for (let i = 0; root && i < (focus.stepPath?.length || 0); i++) {
    updateNestedAction(root, focus.stepPath!.slice(0, i + 1), action => { names.push(action.name || humanActionType(action.type)); return action })
  }
  return names.join(' › ')
}

export type InsertPosition = 'end' | 'before' | 'after' | 'inside'
export function insertAuthorAction(script: ScriptDefinition, action: Action, phase: 'init' | 'action' | 'teardown', position: InsertPosition, anchor?: AuthorCodeFocus | null) {
  const next = structuredClone(script)
  const collection = phase === 'action' ? 'actions' : phase
  const list = next[collection] || (next[collection] = [])
  let index = list.length
  let stepPath: string[] | undefined
  let containerPath = ''
  if (position !== 'end') {
    if (!anchor || anchor.phase !== phase || !list[anchor.actionIndex]) throw new Error('Select an action in this phase first.')
    index = anchor.actionIndex
    if (position === 'inside' || anchor.stepPath?.length) {
      const root = list[index]
      const parentSteps = position === 'inside' ? anchor.stepPath || [] : anchor.stepPath!.slice(0, -1)
      let inserted = false
      list[index] = updateNestedAction(root, parentSteps, parent => {
        const parentPath = `${collection}[${index}]${jsonPathForActionStepPath(parentSteps)}`
        // All nested branch kinds share the same serialized array contract.
        let segment = position === 'inside' ? (parent.type === 'control_loop' ? 'loop' : 'then') : anchor.stepPath![anchor.stepPath!.length - 1]
        let items: Action[] | undefined
        if (position === 'inside') {
          if (!['control_loop', 'control_if'].includes(parent.type)) throw new Error('Select a loop or an if block to insert inside; select a branch child for other containers.')
          parent.control = { ...parent.control, steps: parent.control?.steps || [] }
          items = parent.control.steps
          segment += `[${items!.length}]`
        } else {
          const suffix = jsonPathForActionStepPath([segment])
          const tokens = suffix.match(/[^.[\]]+/g) || []
          let node: unknown = parent
          for (const token of tokens.slice(0, -1)) node = (node as Record<string, unknown>)[token]
          if (Array.isArray(node)) items = node as Action[]
        }
        if (!items) throw new Error('The selected insertion point no longer exists.')
        const match = /\[(\d+)\]$/.exec(segment)!
        const at = Number(match[1]) + (position === 'after' ? 1 : 0)
        items.splice(at, 0, structuredClone(action))
        stepPath = [...parentSteps, segment.replace(/\[\d+\]$/, `[${at}]`)]
        containerPath = parentPath + jsonPathForActionStepPath([segment]).replace(/\[\d+\]$/, '')
        inserted = true
        return parent
      })
      if (!inserted) throw new Error('The selected insertion point no longer exists.')
    } else {
      index += position === 'after' ? 1 : 0
      list.splice(index, 0, structuredClone(action))
    }
  } else list.push(structuredClone(action))
  return { script: next, focus: { phase, actionIndex: index, stepPath } as AuthorCodeFocus, containerPath }
}

export function dedupeAuthorIssues(issues: IssueDetail[]) {
  const seen = new Set<string>()
  return issues.filter(issue => {
    const normalized = issue.title.toLowerCase().replace(/^(validation failed:\s*)/, '').replace(/\s+/g, ' ').trim()
    const key = `${issue.severity}:${issue.source || ''}:${normalized}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

export function changedDocumentLines(before: string, after: string) {
  const a = before.split('\n'), b = after.split('\n')
  let prefix = 0
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix]) prefix++
  let suffix = 0
  while (suffix < a.length - prefix && suffix < b.length - prefix && a[a.length - 1 - suffix] === b[b.length - 1 - suffix]) suffix++
  return { line: prefix + 1, removed: a.slice(prefix, a.length - suffix).join('\n'), added: b.slice(prefix, b.length - suffix).join('\n') }
}

/**
 * Keep each existing breakpoint attached to its action when a sibling is
 * inserted. `script` is the script after the insert: with it, a request
 * breakpoint on another sequence of the same step stays where it is.
 */
export function remapInsertedBreakpoints(breakpoints: import('../types').ScriptDebugBreakpoint[], focus: AuthorCodeFocus, script?: ScriptDefinition) {
  const root = script ? phaseActionsForScript(script, focus.phase)[focus.actionIndex] : undefined
  return breakpoints.map(bp => {
    if ((bp.phase || 'action') !== focus.phase) return bp
    if (!focus.stepPath?.length) return bp.action_index >= focus.actionIndex ? { ...bp, action_index: bp.action_index + 1 } : bp
    if (bp.action_index !== focus.actionIndex) return bp
    const inserted = focus.stepPath
    // A sequence request, top-level or nested, is keyed by its request_index
    // (as F9 and the outline set it): keep that form and shift the index.
    if (!bp.step_path?.length && bp.request_index != null) {
      const at = /^requests\[(\d+)\]$/.exec(inserted[inserted.length - 1])
      if (!at || bp.request_index < Number(at[1])) return bp
      // The index pauses on that request of whichever sequence has one, as
      // a removal relocates it: it follows the edited sequence when that
      // sequence had the request, and stays on another sequence's otherwise.
      if (root) {
        const edited = inserted.slice(0, -1).join('/')
        const holders = sequenceStepPaths(root).filter(path => {
          const sequence = resolveNestedAction(root, path)
          // The edited sequence had its request N before the insert at N+1.
          return sequence && rawHttpSequenceRequest(sequence, bp.request_index! + (path.join('/') === edited ? 1 : 0))
        })
        if (holders.length && !holders.some(path => path.join('/') === edited)) return bp
      }
      return { ...bp, request_index: bp.request_index + 1 }
    }
    const path = bp.step_path || []
    const depth = inserted.length - 1
    if (path.length <= depth || inserted.slice(0, depth).some((segment, index) => path[index] !== segment)) return bp
    const a = /^(.*)\[(\d+)\]$/.exec(inserted[depth]), b = /^(.*)\[(\d+)\]$/.exec(path[depth])
    if (!a || !b || a[1] !== b[1] || Number(b[2]) < Number(a[2])) return bp
    const moved = [...path]; moved[depth] = `${b[1]}[${Number(b[2]) + 1}]`
    return { ...bp, step_path: moved }
  })
}
