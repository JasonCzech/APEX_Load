import { describe, expect, it } from 'vitest'
import type { BrunoNativeScriptCandidate } from '../types/scriptImports'
import { brunoGeneratedValueGroups, brunoGeneratedValueSelections } from './brunoGeneratedValueGroups'

const candidate = (id: string, suggestions: Array<{ id: string; rule: string; name: string; auto?: boolean; kind?: string }>) => ({
  candidate_id: id,
  parameterization: {
    suggestions: suggestions.map(item => ({
      id: item.id, kind: item.kind || 'generated', var_name: item.name, replacement: `{{var.${item.name}}}`,
      confidence: 0.85, rule_id: item.rule, rationale: `${item.name} rationale`, auto_apply: item.auto ?? true, high_risk: false,
    })),
    summary: { total: 0, by_kind: {}, auto_apply_count: 0, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
  },
}) as unknown as BrunoNativeScriptCandidate

describe('Bruno generated values', () => {
  it('groups a field across requests and selects only what every request suggests', () => {
    const groups = brunoGeneratedValueGroups([
      candidate('a', [{ id: 'a1', rule: 'generated.uuid', name: 'messageid' }, { id: 'a2', rule: 'generated.uuid', name: 'messageid_2' }, { id: 'a3', rule: 'generated.timestamp', name: 'sent' }]),
      candidate('b', [{ id: 'b1', rule: 'generated.uuid', name: 'messageid', auto: false }, { id: 'b2', rule: 'generated.uuid', name: 'data', kind: 'data_param' }]),
    ])
    expect(groups.map(group => [group.description, group.requests, group.suggested, group.members.length])).toEqual([
      ['sent rationale', 1, true, 1],
      ['messageid rationale', 2, false, 3],
    ])
    expect(brunoGeneratedValueSelections(groups, {})).toEqual({ a: ['a3'] })
    const messageGroup = groups[1].key
    expect(brunoGeneratedValueSelections(groups, { [messageGroup]: true })).toEqual({ a: ['a3', 'a1', 'a2'], b: ['b1'] })
    expect(brunoGeneratedValueSelections(groups, { [groups[0].key]: false })).toEqual({})
  })
})
