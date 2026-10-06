import { describe, expect, it } from 'vitest'
import { scenarioScriptReadiness } from './scenarioScriptReadiness'
import type { StoredScript } from '../types'

const base: StoredScript = { id: 's', name: 'Script', protocol: 'http', version: '1', created_at: '' }
describe('scenarioScriptReadiness', () => {
  it('does not equate an absence of known blockers with validated readiness', () => {
    expect(scenarioScriptReadiness(base)).toMatchObject({ blocked: false, label: 'Not validated for this scenario' })
  })
  it('explains nested action blockers and authoring health', () => {
    const script = { ...base, authoring_health: { score: 0, status: 'blocked', inputs: [{ key: 'asset', status: 'blocker', summary: 'Upload the required asset.' }] }, script: { config: { base_url: 'https://example.invalid' }, name: 'Script', version: '1', protocol: 'http', actions: [{ name: 'branch', type: 'if', target: '', control: { else: [{ name: 'import', type: 'request', target: '/', metadata: { execution_blockers: ['bruno_missing_variables', 'import_review_required'] } }] } }] } } satisfies StoredScript
    const result = scenarioScriptReadiness(script)
    expect(result.blocked).toBe(true)
    expect(result.summary).toContain('Supply the required Bruno variables')
    expect(result.summary).toContain('Resolve the imported behavior')
    expect(result.summary).toContain('Upload the required asset')
  })
})
