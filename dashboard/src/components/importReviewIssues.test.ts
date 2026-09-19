import { describe, expect, it } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import { draftImportFindings, groupImportFindings, issueRepairOptions } from './importReviewIssues'
const script = { name: 'Review', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [] } as ScriptDefinition

describe('import issue queue', () => {
  it('groups repeated consumers without conflating namespaces or control branches', () => {
    const s = { ...script, actions: [
      { name: 'one', type: 'POST', target: '/', body: '{{var.token}}' },
      { name: 'two', type: 'POST', target: '/', body: '{{var.token}}' },
      { name: 'parameter', type: 'POST', target: '/', body: '{{param.token}}' },
      { name: 'branch', type: 'control_if', target: '', control: { steps: [{ name: 'nested', type: 'POST', target: '/', body: '{{var.token}}' }] } },
    ] } as ScriptDefinition
    const findings = ['actions[0]', 'actions[1]', 'actions[2]', 'actions[3].control.steps[0]'].map(action_path => ({ code: 'missing_producer:token', action_path, detail: '', gate: 'execution' }))
    const groups = groupImportFindings([...findings, findings[0]], s)
    expect(groups).toHaveLength(3)
    expect(groups.map(g => g.findings.length).sort()).toEqual([1, 1, 2])
    expect(groups[0].title).toBe('Missing value: token')
  })
  it('merges generic body evidence but retains independent bodies and blockers', () => {
    const findings = ['actions[0]', 'actions[1]'].flatMap(action_path => [
      { code: 'restored_body_required', action_path, detail: '', gate: 'execution' },
      { code: 'import_review_required', action_path, detail: '', gate: 'promotion' },
    ])
    const groups = groupImportFindings(findings, script)
    expect(groups).toHaveLength(2)
    expect(groups.every(g => g.blocking)).toBe(true)
  })
  it('reads nested request metadata while preserving review-only severity', () => {
    const s = { ...script, actions: [{ name: 'sequence', type: 'http_sequence', target: '', params: { requests: [{ name: 'request', metadata: { execution_blockers: ['missing_producer:token'] } }] } }, { name: 'review', type: 'source_review', target: '', metadata: { review_reasons: ['conditional_producer:later'] } }] } as ScriptDefinition
    const findings = draftImportFindings(s)
    expect(findings[0].action_path).toBe('actions[0].params.requests[0]')
    expect(groupImportFindings(findings, s).find(g => g.title.includes('later'))?.blocking).toBe(false)
  })
  it('keeps findings inside parallel branches discoverable', () => {
    const s = { ...script, actions: [{ name: 'parallel', type: 'control_parallel', target: '', control: { branches: [{ name: 'one', steps: [{ name: 'nested', type: 'request', target: '/', metadata: { execution_blockers: ['missing_producer:branchValue'] } }] }] } }] } as ScriptDefinition
    expect(draftImportFindings(s)[0].action_path).toBe('actions[0].control.branches[0].steps[0]')
  })
  it('only offers repairs that match the finding', () => {
    expect(issueRepairOptions('missing_producer:tstamp').map(o => o.value)).toEqual(['bind_variable', 'add_parameter', 'add_extractor'])
    expect(issueRepairOptions('restored_body_required').map(o => o.value)).toEqual(['restore_body'])
    expect(issueRepairOptions('legacy_tls_replacement').map(o => o.value)).toEqual(['select_tls'])
    expect(issueRepairOptions('unknown_unsupported')).toEqual([])
  })
})
