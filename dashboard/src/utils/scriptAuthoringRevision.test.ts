import { describe, expect, it } from 'vitest'
import { ApiError } from '../hooks/apiClient'
import { authoringFixture, savedAuthoringFixture } from '../test/scriptAuthoringFixtures'
import { admitScriptAuthoringBaseline, authoringChangedPaths, baselineFromSavedScript, reapplyScriptDraft, scriptSaveNeedsReconciliation, withoutScriptAuthoringReadContext } from './scriptAuthoringRevision'

describe('paired script authoring revisions', () => {
  it('admits one cloned document and exact strong tag', () => {
    const pair = authoringFixture()
    const admitted = admitScriptAuthoringBaseline(pair.projection, pair.etag, 'script-1', 'project-1')
    expect(admitted).toEqual(pair)
    pair.projection.script.name = 'Later mutation'
    expect(admitted.projection.script.name).toBe('Fixture')
  })
  it.each(['missing', 'weak', 'list', 'different', 'unsupported', 'old-controller', 'wrong-id', 'wrong-project', 'malformed-folder', 'malformed-placement'])('refuses %s authority', problem => {
    const pair = authoringFixture()
    const projection: unknown = problem === 'old-controller' ? savedAuthoringFixture() : pair.projection
    let tag: string | null = pair.etag
    if (problem === 'missing') tag = null
    if (problem === 'weak') tag = `W/${tag}`
    if (problem === 'list') tag = `${tag}, ${tag}`
    if (problem === 'different') tag = authoringFixture({}, 'script-1', 'project-1', 'b').etag
    if (problem === 'unsupported') Object.assign(pair.projection, { projection_version: 'v2' })
    if (problem === 'wrong-id') pair.projection.id = 'other'
    if (problem === 'wrong-project') pair.projection.project_id = 'other'
    if (problem === 'malformed-folder') Object.assign(pair.projection, { folder_id: 1 })
    if (problem === 'malformed-placement') Object.assign(pair.projection.library_placement, { release_ids: ['valid', {}] })
    expect(() => admitScriptAuthoringBaseline(projection, tag, 'script-1', 'project-1')).toThrow('supported paired authoring revision')
  })
  it('adopts only a matching final saved projection, never an enriched response tag', () => {
    const saved = savedAuthoringFixture()
    expect(baselineFromSavedScript(saved).projection.script).toEqual(saved.script)
    expect(() => baselineFromSavedScript({ ...saved, content_revision: authoringFixture().projection.content_revision })).toThrow('outcome could not be verified')
    expect(() => baselineFromSavedScript({ ...saved, authoring_projection: undefined })).toThrow('outcome could not be verified')
  })
  it('reapplies only local changes while preserving current server fields and placement', () => {
    const original = authoringFixture({ config: { base_url: '/old' }, name: 'Original', folder_id: 'old-folder' }).projection.script
    const local = { ...original, name: 'My draft' }
    const current = authoringFixture({ ...original, config: { base_url: '/new' }, folder_id: 'new-folder' }, 'script-1', 'project-1', 'b')
    const reapplied = reapplyScriptDraft(local, current, original)
    expect(reapplied.name).toBe('My draft')
    expect(reapplied.config.base_url).toBe('/new')
    expect(reapplied.folder_id).toBe('new-folder')
    expect(current.projection.script.name).toBe('Original')
  })
  it('preserves explicit local deletion and local action-array precedence', () => {
    const original = authoringFixture({ parameters: [{ name: 'remove', type: 'constant', values: ['old'] }] }).projection.script
    const local = { ...original, actions: [{ name: 'Local', type: 'http', target: '/' }] }
    delete local.parameters
    const current = authoringFixture({ ...original, actions: [{ name: 'Remote', type: 'http', target: '/remote' }] })
    const reapplied = reapplyScriptDraft(local, current, original)
    expect(reapplied.parameters).toBeUndefined()
    expect(reapplied.actions).toEqual(local.actions)
  })
  it.each(['__proto__', 'constructor', 'toString'])('preserves an own added JSON header %s while retaining independent remote changes', key => {
    const original = authoringFixture({ config: { base_url: '/old', http: { default_headers: { Accept: 'original' } } } }).projection.script
    const localHeaders = JSON.parse(JSON.stringify(Object.fromEntries([['Accept', 'original'], [key, 'local value']]))) as Record<string, string>
    const local = { ...original, config: { ...original.config, http: { default_headers: localHeaders } } }
    const current = authoringFixture({ ...original, config: { base_url: '/remote', http: { default_headers: { Accept: 'remote', 'X-Remote': 'retained' } } } })
    const originalBytes = JSON.stringify(original)
    const currentBytes = JSON.stringify(current)
    const reapplied = reapplyScriptDraft(local, current, original)
    const headers = reapplied.config.http!.default_headers!
    expect(Object.prototype.hasOwnProperty.call(headers, key)).toBe(true)
    expect(Object.getOwnPropertyDescriptor(headers, key)).toMatchObject({ value: 'local value', enumerable: true, writable: true, configurable: true })
    expect(Object.getPrototypeOf(headers)).toBe(Object.prototype)
    expect(JSON.parse(JSON.stringify(headers))).toEqual(Object.fromEntries([['Accept', 'remote'], ['X-Remote', 'retained'], [key, 'local value']]))
    expect(reapplied.config.base_url).toBe('/remote')
    expect(JSON.stringify(original)).toBe(originalBytes)
    expect(JSON.stringify(current)).toBe(currentBytes)
  })
  it.each(['__proto__', 'constructor', 'toString'])('preserves explicit deletion of an own JSON header %s without substituting an inherited value', key => {
    const originalHeaders = JSON.parse(JSON.stringify(Object.fromEntries([['Accept', 'original'], [key, 'original own value']]))) as Record<string, string>
    const original = authoringFixture({ config: { base_url: '/old', http: { default_headers: originalHeaders } } }).projection.script
    const local = { ...original, config: { ...original.config, http: { default_headers: { Accept: 'original' } } } }
    const current = authoringFixture({ ...original, config: { base_url: '/remote', http: { default_headers: Object.fromEntries([['Accept', 'remote'], ['X-Remote', 'retained'], [key, 'remote own value']]) } } })
    const headers = reapplyScriptDraft(local, current, original).config.http!.default_headers!
    expect(Object.prototype.hasOwnProperty.call(headers, key)).toBe(false)
    expect(Object.getPrototypeOf(headers)).toBe(Object.prototype)
    expect(JSON.parse(JSON.stringify(headers))).toEqual({ Accept: 'remote', 'X-Remote': 'retained' })
  })
  it.each(['__proto__', 'constructor', 'toString'])('retains a remote own JSON header %s when the local draft leaves it unchanged or absent', key => {
    for (const originallyPresent of [false, true]) {
      const originalHeaders = Object.fromEntries([['Accept', 'original'], ...(originallyPresent ? [[key, 'original own value']] : [])])
      const original = authoringFixture({ config: { base_url: '/old', http: { default_headers: originalHeaders } } }).projection.script
      const local = { ...original, name: 'Independent local edit' }
      const current = authoringFixture({ ...original, config: { base_url: '/remote', http: { default_headers: Object.fromEntries([['Accept', 'remote'], [key, 'remote own value']]) } } })
      const reapplied = reapplyScriptDraft(local, current, original)
      const headers = reapplied.config.http!.default_headers!
      expect(Object.prototype.hasOwnProperty.call(headers, key)).toBe(true)
      expect(headers[key]).toBe('remote own value')
      expect(Object.getPrototypeOf(headers)).toBe(Object.prototype)
      expect(JSON.parse(JSON.stringify(headers))).toEqual(Object.fromEntries([['Accept', 'remote'], [key, 'remote own value']]))
      expect(reapplied.name).toBe('Independent local edit')
    }
  })
  it('ignores helper-added root server clocks while retaining unsaved authored and nested timestamp edits', () => {
    const baseline = authoringFixture().projection.script
    const returned = { ...baseline, created_at: '0001-01-01T00:00:00Z', updated_at: '0001-01-01T00:00:00Z', archived_at: '',
      name: 'Retained local edit', actions: [{ name: 'Rebound', type: 'request', target: '/', metadata: { updated_at: 'authored marker' } }] }
    const authored = withoutScriptAuthoringReadContext(returned)
    expect(authoringChangedPaths(baseline, authored)).toEqual(['actions[0]', 'name'])
    expect(authored.actions[0].metadata).toEqual({ updated_at: 'authored marker' })
    expect(authoringChangedPaths(baseline, withoutScriptAuthoringReadContext({ ...baseline, created_at: '0001-01-01T00:00:00Z', updated_at: '0001-01-01T00:00:00Z' }))).toEqual([])
    expect(returned.created_at).toBe('0001-01-01T00:00:00Z')
    expect(baseline.name).toBe('Fixture')
  })

  it.each([null, undefined, 'draft', [], {}, { ...authoringFixture().projection.script, actions: null }])('refuses a malformed paired helper draft (%s)', draft => {
    expect(() => withoutScriptAuthoringReadContext(draft as never)).toThrow('supported authored script draft')
  })

  it('shows bounded field paths without disclosing credential values', () => {
    const paths = authoringChangedPaths({ config: { password: 'secret-before' } }, { config: { password: 'secret-after' } })
    expect(paths).toEqual(['config.password'])
    expect(JSON.stringify(paths)).not.toContain('secret-')
    expect(authoringChangedPaths({}, Object.fromEntries(Array.from({ length: 1000 }, (_, i) => [`field-${i}`, i]))).length).toBeLessThanOrEqual(51)
  })
  it.each([new Error('lost response'), new ApiError('stale', 412), new ApiError('strict', 428), new ApiError('unknown', 503, { code: 'script_save_outcome_unknown' }), new ApiError('audit', 503, { code: 'script_save_outcome_audit_unavailable' })])('requires explicit reconciliation for nondefinitive writes', cause => {
    expect(scriptSaveNeedsReconciliation(cause)).toBe(true)
  })
  it('retains retry authority after a definitive validation rejection', () => {
    expect(scriptSaveNeedsReconciliation(new ApiError('invalid', 422))).toBe(false)
  })
})
