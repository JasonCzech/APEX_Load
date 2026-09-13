import { describe, expect, it } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { datasetColumnDependencies, parameterUsages, renameDatasetColumn, renameScriptParameter, validateDatasetReplacement } from './parameterIntegrity'

function fixture(): ScriptDefinition {
  return { name: 'Data flow', version: '1', protocol: 'http', config: { base_url: '{{param.USER}}' },
    parameters: [{ name: 'user', type: 'file', data_source: 'users', column: 'id' }, { name: 'paired', type: 'file', data_source: 'users', column: 'other', same_line_as: 'USER' }],
    data_sources: [{ name: 'users', type: 'csv', strategy: 'sequential', first_row_header: true, inline_data: 'id,other\na,b\n' }],
    init: [{ name: 'init', type: 'GET', target: '{{param.user}}' }],
    actions: [{ name: 'branch', type: 'control_if', target: '', control: { steps: [{ name: 'nested', type: 'GET', target: '{{param.user}}/{{param.user_id}}/{{data.users.id}}' }] } }],
    metadata: { import: { preserved: { original: '{{param.user}}' } } },
  }
}

describe('reference-safe parameter edits', () => {
  it('renames nested, init, config and same-row references without touching similar tokens or import evidence', () => {
    const script = fixture()
    const next = renameScriptParameter(script, 0, 'account')
    expect(next.config.base_url).toBe('{{param.account}}')
    expect(next.init?.[0].target).toBe('{{param.account}}')
    expect(next.actions[0].control?.steps?.[0].target).toBe('{{param.account}}/{{param.user_id}}/{{data.users.id}}')
    expect(next.parameters?.[1].same_line_as).toBe('account')
    expect(next.metadata).toEqual(script.metadata)
    expect(script.parameters?.[0].name).toBe('user')
    expect(parameterUsages(next, 'account')).toHaveLength(4)
  })
  it.each(['', '1name', 'bad.name', 'paired', 'PAIRED'])('rejects invalid or duplicate name %s', name => {
    expect(() => renameScriptParameter(fixture(), 0, name)).toThrow()
  })
  it('renames a source column and all mappings including direct data tokens', () => {
    const next = renameDatasetColumn(fixture(), 0, 0, 'account_id')
    expect(next.parameters?.[0].column).toBe('account_id')
    expect(next.data_sources?.[0].inline_data).toContain('account_id,other')
    expect(next.actions[0].control?.steps?.[0].target).toContain('{{data.users.account_id}}')
  })
  it('requires disambiguation of variable fallbacks and preserves direct dataset variable aliases', () => {
    const script = fixture()
    script.actions = [{ name: 'legacy', type: 'request', target: '{{var.user}}/{{var.users.id}}' }]
    expect(() => renameScriptParameter(script, 0, 'account')).toThrow('explicit param tokens')
    expect(renameDatasetColumn(script, 0, 0, 'account_id').actions[0].target).toContain('{{var.users.account_id}}')
    expect(() => validateDatasetReplacement(script, 0, { ...script.data_sources![0], inline_data: 'ID,other\na,b' })).toThrow('Remap')
  })
  it('blocks destructive replacement with dependencies and accepts compatible replacement', () => {
    const script = fixture()
    const source = script.data_sources![0]
    expect(datasetColumnDependencies(script, source, 'id')).toEqual(['Parameter user', 'actions[0].control.steps[0].target'])
    expect(() => validateDatasetReplacement(script, 0, { ...source, inline_data: 'other\nb\n' })).toThrow('Remap')
    expect(() => validateDatasetReplacement(script, 0, { ...source, inline_data: 'other,id\nb,c\n' })).not.toThrow()
    expect(() => renameDatasetColumn(script, 0, 0, 'OTHER')).toThrow('already exists')
  })
})
