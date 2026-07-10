import type { ScriptActionDefinition } from '../types/scripts'
import { escapeRegExp } from './scriptEditorHelpers'

export interface CorrelationExtractorRepair {
  failingActionIndex: number
  recordedValue: string
  replacement: string
}

// Short recorded values collide with incidental data (a page number, a version
// segment), and an unanchored substring replace also rewrites the value inside a
// larger token. Replace only when the value stands alone as a token (not flanked
// by alphanumeric/underscore characters) — so a recorded "2" never rewrites "v2"
// — and skip trivially short (1–2 char) values, which are too ambiguous to
// blanket-replace safely.
const MIN_CORRELATION_REPAIR_LENGTH = 3

function replaceRecordedValueInString(value: string, recordedValue: string, replacement: string): string {
  const pattern = new RegExp(`(^|[^A-Za-z0-9_])${escapeRegExp(recordedValue)}(?=[^A-Za-z0-9_]|$)`, 'g')
  return value.replace(pattern, (_match, prefix: string) => prefix + replacement)
}

function replaceRecordedValueInField(value: unknown, recordedValue: string, replacement: string): { value: unknown; changed: boolean } {
  if (!recordedValue || recordedValue.length < MIN_CORRELATION_REPAIR_LENGTH) return { value, changed: false }
  if (typeof value === 'string') {
    const replaced = replaceRecordedValueInString(value, recordedValue, replacement)
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
