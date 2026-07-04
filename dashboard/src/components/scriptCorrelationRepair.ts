import type { ScriptActionDefinition } from '../types/scripts'

export interface CorrelationExtractorRepair {
  failingActionIndex: number
  recordedValue: string
  replacement: string
}

function replaceRecordedValueInField(value: unknown, recordedValue: string, replacement: string): { value: unknown; changed: boolean } {
  if (!recordedValue) return { value, changed: false }
  if (typeof value === 'string') {
    const replaced = value.split(recordedValue).join(replacement)
    return { value: replaced, changed: replaced !== value }
  }
  if (Array.isArray(value)) {
    let changed = false
    const replaced = value.map(item => {
      const next = replaceRecordedValueInField(item, recordedValue, replacement)
      changed ||= next.changed
      return next.value
    })
    return { value: changed ? replaced : value, changed }
  }
  if (value && typeof value === 'object') {
    let changed = false
    const replaced = Object.fromEntries(Object.entries(value).map(([key, item]) => {
      const next = replaceRecordedValueInField(item, recordedValue, replacement)
      changed ||= next.changed
      return [key, next.value]
    }))
    return { value: changed ? replaced : value, changed }
  }
  return { value, changed: false }
}

export function applyCorrelationRepairToAction(action: ScriptActionDefinition, repair: CorrelationExtractorRepair): ScriptActionDefinition {
  const fields: Array<keyof ScriptActionDefinition> = ['target', 'body', 'value', 'headers', 'params']
  let next = action
  for (const field of fields) {
    if (next[field] === undefined) continue
    const replaced = replaceRecordedValueInField(next[field], repair.recordedValue, repair.replacement)
    if (replaced.changed) {
      next = { ...next, [field]: replaced.value }
    }
  }
  return next
}
