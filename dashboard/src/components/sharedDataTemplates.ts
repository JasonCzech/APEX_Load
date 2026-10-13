import type { ScriptActionDefinition as Action } from '../types/scripts'
import type { DslCatalogVariant } from './dslActionCatalog'
import { uniqueVariableName } from './scriptDataUsageModel'

/** A result prefix that reads as what the step holds. */
const PREFIX_BASES: Record<string, string> = {
  read: 'row', next: 'next_row', read_column: 'column', push: 'added', update: 'updated', increment: 'counted', clear: 'cleared',
  pop: 'taken', lease: 'leased', renew: 'renewed', ack: 'acknowledged', release: 'released', create_column: 'new_column', rotate: 'rotated',
}

/** What a new shared-data step can be wired to. */
export interface SharedDataContext {
  alias: string
  /** The alias's known columns, in order. */
  columns: string[]
  /** Variable names in use; each prefix made here is added. */
  taken: Set<string>
  /** Action names in use (lower case); each name made here is added. */
  names: Set<string>
  /** Outputs of steps before the insertion point, nearest last. */
  earlier: { record_id: string[]; record_version: string[]; lease: string[] }
  /** Variables set before the insertion point, without `var.`. */
  variables: ReadonlySet<string>
}

function uniqueName(base: string, names: Set<string>): string {
  let name = base
  for (let suffix = 2; names.has(name.toLowerCase()); suffix++) name = `${base} (${suffix})`
  names.add(name.toLowerCase())
  return name
}

/** A result prefix for a new step of this operation that no step or data source uses. */
export function suggestedResultPrefix(operation: string, taken: ReadonlySet<string>): string {
  return uniqueVariableName(PREFIX_BASES[operation] || 'result', taken)
}

function newPrefix(operation: string, context: SharedDataContext): string {
  const prefix = suggestedResultPrefix(operation, context.taken)
  context.taken.add(prefix)
  return prefix
}

const allows = (variant: DslCatalogVariant, field: string) => variant.allowed.includes(`params.${field}`)
const last = (values: string[]) => values[values.length - 1]

/**
 * One table step as the insert dialog makes it: the operation, a result prefix
 * no other step uses, and safe defaults. A read takes the first row without
 * changing it, an empty table fails the step (set explicitly, so the choice is
 * visible), and a change to a row read earlier, or a lease taken earlier,
 * points at that step's outputs.
 */
export function sharedDataAction(variant: DslCatalogVariant, name: string, context: SharedDataContext, prefix?: string): Action {
  const operation = variant.value
  const params: Record<string, unknown> = { alias: context.alias, operation, result_prefix: prefix || newPrefix(operation, context), ...structuredClone(variant.defaults || {}) }
  if (allows(variant, 'on_empty')) params.on_empty = 'error'
  if (allows(variant, 'on_condition')) params.on_condition = 'error'
  const byRecord = variant.choices?.find(choice => choice.includes('params.record_id'))
  if (variant.mutates && byRecord && last(context.earlier.record_id)) {
    params.record_id = last(context.earlier.record_id)
    if (byRecord.includes('params.expected_version') && last(context.earlier.record_version)) params.expected_version = last(context.earlier.record_version)
  } else if (variant.choices?.some(choice => choice.includes('params.position')) || variant.required?.includes('params.position')) {
    params.position ??= 1
  }
  if (allows(variant, 'lease_ref') && last(context.earlier.lease)) params.lease_ref = last(context.earlier.lease)
  if (operation === 'push') {
    // A new row's values come from earlier steps where a variable has the column's name.
    params.values = Object.fromEntries(context.columns.map(column => [column, context.variables.has(column) ? `{{var.${column}}}` : '']))
  }
  return { name: uniqueName(name, context.names), type: 'live_table', target: '', params } as Action
}

export type LinkedTemplateKey = 'publish' | 'read_update' | 'take_process' | 'lease_ack'

export interface LinkedTemplate {
  key: LinkedTemplateKey
  label: string
  summary: string
  /** The operations it inserts. */
  operations: string[]
}

export const LINKED_TEMPLATES: LinkedTemplate[] = [
  { key: 'publish', label: 'Publish work', summary: 'Adds a row whose values come from earlier steps.', operations: ['push'] },
  { key: 'read_update', label: 'Read and update', summary: 'Reads a row, then updates it only if no one changed it in between: the update checks the version the read saw.', operations: ['read', 'update'] },
  { key: 'take_process', label: 'Remove and process', summary: 'Takes the next row out of the table, then runs your steps with its values.', operations: ['pop'] },
  { key: 'lease_ack', label: 'Lease, process, acknowledge', summary: 'Leases a row, runs your steps, then acknowledges it. If the iteration fails first, the lease expires and the row is delivered again.', operations: ['lease', 'ack'] },
]

/** Where the user's own steps go: before or after a named step, which is left selected. */
export interface TemplateSpot { name: string; position: 'before' | 'after' }

/**
 * A block that runs `steps` only when the step saved under `prefix` got a row.
 * When a failed step lets the iteration go on (continue on error), the steps
 * that depend on its row do not run with a missing one.
 */
function whenItGotARow(name: string, prefix: string, steps: Action[], context: SharedDataContext): Action {
  return { name: uniqueName(name, context.names), type: 'control_if', target: '', control: { condition: { left: `{{var.${prefix}.status}}`, op: 'eq', right: 'ok' }, steps } } as Action
}

/**
 * A linked template's steps, references already wired, and where the user's
 * own steps go. A closing step (the update, the acknowledgment) runs only when
 * the first step got a row, and the user's steps go just before it. Null when
 * the catalog lacks one of its operations.
 */
export function linkedTemplateActions(key: LinkedTemplateKey, variants: DslCatalogVariant[], context: SharedDataContext): { actions: Action[]; spot?: TemplateSpot } | null {
  const variant = (operation: string) => variants.find(candidate => candidate.value === operation)
  const template = LINKED_TEMPLATES.find(candidate => candidate.key === key)
  if (!template || template.operations.some(operation => !variant(operation))) return null
  const alias = context.alias
  if (key === 'publish') return { actions: [sharedDataAction(variant('push')!, `Add a row to ${alias}`, context)] }
  if (key === 'take_process') {
    const take = sharedDataAction(variant('pop')!, `Take the next row from ${alias}`, context)
    return { actions: [take], spot: { name: take.name, position: 'after' } }
  }
  if (key === 'read_update') {
    const read = sharedDataAction(variant('read')!, `Read a row from ${alias}`, context)
    const prefix = String(read.params!.result_prefix)
    const wired = { ...context, earlier: { ...context.earlier, record_id: [`{{var.${prefix}.record_id}}`], record_version: [`{{var.${prefix}.record_version}}`] } }
    const update = sharedDataAction(variant('update')!, `Update the row in ${alias}`, wired)
    // Unchanged until edited: each known column is written back as the read saw it.
    update.params!.values = Object.fromEntries(context.columns.map(column => [column, `{{var.${prefix}.values.${column}}}`]))
    return { actions: [read, whenItGotARow('When the row was read', prefix, [update], context)], spot: { name: update.name, position: 'before' } }
  }
  const lease = sharedDataAction(variant('lease')!, `Lease a row from ${alias}`, context)
  const prefix = String(lease.params!.result_prefix)
  const ack = sharedDataAction(variant('ack')!, 'Acknowledge the leased row', { ...context, earlier: { ...context.earlier, lease: [prefix] } })
  return { actions: [lease, whenItGotARow('When a row was leased', prefix, [ack], context)], spot: { name: ack.name, position: 'before' } }
}

/** Every step's name in `actions`, blocks' steps included, in order. */
export function templateStepNames(actions: Action[]): string[] {
  return actions.flatMap(action => [action.name, ...templateStepNames((action.control?.steps || []) as Action[])])
}
