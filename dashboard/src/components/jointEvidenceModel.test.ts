import { describe, expect, it } from 'vitest'
import type { JointEvidenceProjection } from '../types/jointEvidence'
import { describeJointEvidence, JOINT_EVIDENCE_DIMENSIONS, jointEvidenceIssue, promotedPropertyCategories, toggleJointEvidenceDimension } from './jointEvidenceModel'

const rule = (name: string) => ({ name, extraction: `${name}_value`, values: ['a', 'b'] })
const group = (scriptId: string, results?: { property_analytics: ReturnType<typeof rule>[] }) => ({
  name: scriptId, script_id: scriptId, vusers: 1, ramp_up: '', ramp_down: '', stages: [], initialization: 'at_runtime' as const,
  ...(results ? { runtimeSettings: { results, log_level: '', auto_transactions: '' } } : {}),
})

describe('jointEvidenceModel', () => {
  it('lists the controller dimensions in its order', () => {
    expect(JOINT_EVIDENCE_DIMENSIONS.map(item => item.id)).toEqual(['name', 'protocol', 'group', 'region', 'dependency', 'page', 'hierarchy', 'phase', 'network_profile', 'browser_profile', 'browser_engine', 'browser_platform'])
  })

  it("reads each group's promoted categories as the controller resolves them: group, then scenario, then script", () => {
    const scripts = [
      { id: 'a', script: { config: { results: { property_analytics: [rule('operation')] } } } },
      { id: 'b', script: { config: { results: { property_analytics: [rule('tier')] } } } },
    ] as never
    expect(promotedPropertyCategories({ groups: [group('a'), group('b')] }, scripts)).toEqual(['operation', 'tier'])
    // Scenario runtime settings replace every script's results.
    expect(promotedPropertyCategories({ groups: [group('a'), group('b')], runtime_settings: { results: { property_analytics: [rule('region_kind')] } } }, scripts)).toEqual(['region_kind'])
    // A group's own results replace the scenario's for that group only.
    expect(promotedPropertyCategories({ groups: [group('a', { property_analytics: [rule('channel')] }), group('b')], runtime_settings: { results: { property_analytics: [rule('region_kind')] } } }, scripts)).toEqual(['channel', 'region_kind'])
    // An empty override promotes nothing for its groups.
    expect(promotedPropertyCategories({ groups: [group('a', { property_analytics: [] })] }, scripts)).toEqual([])
    // A browser group adds the engine's own cold/warm category.
    expect(promotedPropertyCategories({ groups: [group('web')] }, [{ id: 'web', protocol: 'browser' }] as never)).toEqual(['browser_iteration'])
  })

  it('keeps at most three dimensions and one property category', () => {
    let projection: JointEvidenceProjection = { population: 'action', dimensions: ['name'] }
    projection = toggleJointEvidenceDimension(projection, 'group', true)
    projection = toggleJointEvidenceDimension(projection, 'property:operation', true)
    expect(projection.dimensions).toEqual(['name', 'group', 'property:operation'])
    expect(toggleJointEvidenceDimension(projection, 'region', true)).toBe(projection)
    // Another category replaces the chosen one, even with three chosen.
    expect(toggleJointEvidenceDimension(projection, 'property:tier', true).dimensions).toEqual(['name', 'group', 'property:tier'])
    expect(toggleJointEvidenceDimension(projection, 'group', false).dimensions).toEqual(['name', 'property:operation'])
  })

  it('refuses an empty projection and an unpromoted category', () => {
    expect(jointEvidenceIssue(undefined, [])).toBe('')
    expect(jointEvidenceIssue({ population: 'action', dimensions: [] }, [])).toContain('at least one')
    expect(jointEvidenceIssue({ population: 'action', dimensions: ['name', 'property:tier'] }, ['operation'])).toContain('"tier" is not promoted')
    expect(jointEvidenceIssue({ population: 'transaction', dimensions: ['group', 'property:operation'] }, ['operation'])).toBe('')
    // The controller always admits the browser iteration category.
    expect(jointEvidenceIssue({ population: 'transaction', dimensions: ['name', 'property:browser_iteration'] }, [])).toBe('')
  })

  it('describes a projection, naming the browser iteration category for what it splits', () => {
    expect(describeJointEvidence({ population: 'action', dimensions: ['name', 'group', 'property:tier'] })).toBe('Actions by Name, Group and Property: tier')
    expect(describeJointEvidence({ population: 'transaction', dimensions: ['property:browser_iteration'] })).toBe('Business transactions by Browser iteration (cold/warm)')
  })
})
