import { expect } from '@playwright/test'
import type { ScriptDefinition } from '../../src/types/scripts'

// Inspect the negotiated wire contract explicitly. Keep workflow.json() and
// direct API tests unchanged so legacy response compatibility remains visible.
export function canonicalImportDraft(payload: Record<string, unknown>): ScriptDefinition {
  expect(payload.response_format).toBe('canonical-v1')
  expect(payload).not.toHaveProperty('script')
  expect(Array.isArray(payload.scripts)).toBe(true)
  const scripts = payload.scripts as unknown[]
  const index = payload.primary_script_index
  expect(typeof index).toBe('number')
  expect(Number.isInteger(index)).toBe(true)
  expect(index).toBeGreaterThanOrEqual(0)
  expect(index).toBeLessThan(scripts.length)
  if (payload.result !== null && typeof payload.result === 'object' && !Array.isArray(payload.result)) {
    expect(payload.result).not.toHaveProperty('script')
    expect(payload.result).not.toHaveProperty('scripts')
  }
  const draft = scripts[index as number] as ScriptDefinition
  expect(typeof draft?.name).toBe('string')
  expect(Array.isArray(draft?.actions)).toBe(true)
  return draft
}
