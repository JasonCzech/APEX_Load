import { describe, expect, it, vi } from 'vitest'

import type { ImportExistingScript } from '../types/scriptImports'
import { buildImportSaveChoices, defaultImportSaveChoice, importUpdateTargets, previousImportInputs } from './importReimport'

const match = (overrides: Partial<ImportExistingScript> = {}): ImportExistingScript => ({
  script_id: 'script-1',
  name: 'Orders',
  updated_at: '2026-10-15T10:00:00Z',
  matches: 1,
  same_source: true,
  edited_since_import: false,
  ...overrides,
})

describe('re-import save choices', () => {
  it('updates a match from a declared source and copies one matched by file name', () => {
    expect(defaultImportSaveChoice(match())).toBe('update')
    expect(defaultImportSaveChoice(match({ same_source: false }))).toBe('copy')
    expect(defaultImportSaveChoice(null)).toBe('copy')
  })

  it('sends positional targets only when something updates', () => {
    const matches = [match(), null, match({ script_id: 'script-3', same_source: false })]
    const defaults = buildImportSaveChoices(matches, {}, vi.fn())
    expect(defaults.updateTargets(3)).toEqual([
      { script_id: 'script-1', expected_updated_at: '2026-10-15T10:00:00Z' },
      null,
      null,
    ])
    const copies = buildImportSaveChoices(matches, { 0: 'copy' }, vi.fn())
    expect(copies.updateTargets(3)).toBeUndefined()
    expect(importUpdateTargets(matches, () => 'update', 3)?.[2]).toEqual({ script_id: 'script-3', expected_updated_at: '2026-10-15T10:00:00Z' })
  })

  it('sends the reviewed content back with a target, and tells chosen from default', () => {
    const choices = buildImportSaveChoices([match({ content_digest: 'abc123' })], { 0: 'update' }, vi.fn())
    expect(choices.updateTargetFor(0)).toEqual({
      script_id: 'script-1',
      expected_updated_at: '2026-10-15T10:00:00Z',
      expected_content_digest: 'abc123',
    })
    expect(choices.chosen(0)).toBe(true)
    expect(buildImportSaveChoices([match()], {}, vi.fn()).chosen(0)).toBe(false)
  })

  it('offers the inputs of the scripts it updates, the first one deciding', () => {
    const matches = [
      match({ inputs: { region: { mode: 'value', value: 'us-east' } } }),
      match({ script_id: 'script-2', inputs: { region: { mode: 'value', value: 'eu-west' }, customer: { mode: 'data', alias: 'customers', column: 'id' } } }),
    ]
    expect(previousImportInputs(matches, () => 'update')).toEqual({
      region: { mode: 'value', value: 'us-east' },
      customer: { mode: 'data', alias: 'customers', column: 'id' },
    })
    expect(previousImportInputs(matches, index => index === 0 ? 'copy' : 'update')?.region).toEqual({ mode: 'value', value: 'eu-west' })
    expect(previousImportInputs(matches, () => 'copy')).toBeUndefined()
  })

})
