import type { ScriptParameterDefinition } from '../types/scripts'

type AllocationFields = Pick<ScriptParameterDefinition, 'type' | 'selection' | 'auto_allocate' | 'block_size' | 'value_for_each_vuser'>

/**
 * Mirrors the engine's rule (dsl.AutoAllocatesUniqueRows): a unique file or
 * list parameter without a block size gets an automatically sized block of rows
 * per virtual user, as VuGen's default does, exactly as auto_allocate asks.
 */
export function autoAllocatesUniqueRows(parameter: AllocationFields): boolean {
  if (parameter.auto_allocate) return true
  if (parameter.selection !== 'unique' || (parameter.block_size ?? 0) > 0 || parameter.value_for_each_vuser) return false
  return parameter.type === 'file' || parameter.type === 'list'
}
