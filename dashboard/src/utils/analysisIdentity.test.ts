import { expect, it } from 'vitest'
import type { AnalysisIdentitySelection } from '../types/analysis'
import { analysisIdentityIsGoWhitespace, analysisImportedIdentityAvailable, analysisImportedSelectorsRequested, analysisMappingRequiresImportedIdentity, analysisIdentityMappingError, analysisMappingDisclosureError, analysisIdentityTextError, buildAnalysisIdentitySelection, emptyAnalysisIdentityFilters, restoreAnalysisIdentityFilters, restoreAnalysisImportedIdentityFilters } from './analysisIdentity'

it('validates exact UTF-8 bounds without replacing malformed Unicode', () => {
  expect(analysisIdentityTextError('😀'.repeat(32), 128)).toBeUndefined()
  expect(analysisIdentityTextError('😀'.repeat(33), 128)).toContain('UTF-8')
  expect(analysisIdentityTextError('x'.repeat(129), 128)).toContain('UTF-8')
  for (const value of ['\ud800', '\udc00', '\ud800x', 'a\0b']) expect(analysisIdentityTextError(value, 128)).toBeDefined()
  expect(analysisIdentityTextError('�', 128)).toBeUndefined()
})

it('retains exact label names, special keys and explicit empty values in owned filters', () => {
  const filters = emptyAnalysisIdentityFilters()
  filters.source_id = ' agent '; filters.host = '😀'; filters.service = 'svc'
  filters.dimensions = [{ name: '__proto__', value: '' }, { name: 'a/b~"c', value: "x' quoted" }]
  const selected = buildAnalysisIdentitySelection(filters)
  expect(selected.source_id).toBe(' agent ')
  expect(Object.prototype.hasOwnProperty.call(selected.dimensions!, '__proto__')).toBe(true)
  expect(selected.dimensions!.__proto__).toBe('')
  expect(Object.getPrototypeOf(selected.dimensions)).toBe(Object.prototype)
  filters.dimensions[0].value = 'changed'
  expect(selected.dimensions!.__proto__).toBe('')
  const restored = restoreAnalysisIdentityFilters(selected)
  restored.dimensions[0].value = 'other'
  expect(selected.dimensions!.__proto__).toBe('')
})

it('refuses duplicates, excessive populations and Go Unicode whitespace sources', () => {
  const filters = emptyAnalysisIdentityFilters()
  filters.dimensions = Array.from({ length: 16 }, (_, i) => ({ name: `key${i}`, value: '' }))
  expect(Object.keys(buildAnalysisIdentitySelection(filters).dimensions!)).toHaveLength(16)
  filters.dimensions.push({ name: 'extra', value: '' })
  expect(() => buildAnalysisIdentitySelection(filters)).toThrow('16')
  filters.dimensions = [{ name: 'key', value: 'one' }, { name: 'key', value: 'two' }]
  expect(() => buildAnalysisIdentitySelection(filters)).toThrow('unique')
  filters.dimensions = []; filters.source_id = '\u0085\u2003'
  expect(() => buildAnalysisIdentitySelection(filters)).toThrow('nonblank')
  filters.source_id = '\ufeff'
  expect(buildAnalysisIdentitySelection(filters).source_id).toBe('\ufeff')
})

it('validates nested scalar mapping paths and duplicate dimension names', () => {
  expect(analysisIdentityMappingError({ source: ['nested', 'source'], dimensions: [{ name: 'label', value: ['nested', 'label'] }] })).toBeUndefined()
  expect(analysisIdentityMappingError({ dimensions: [{ name: 'label', value: [] }] })).toContain('scalar column')
  expect(analysisIdentityMappingError({ host: Array(17).fill('part') })).toContain('16')
  expect(analysisIdentityMappingError({ source: ['a\0b'] })).toContain('NUL')
  expect(analysisIdentityMappingError({ dimensions: [{ name: 'one', value: ['a'] }, { name: 'one', value: ['b'] }] })).toContain('unique')
})

it('refuses declared null saved identities instead of downgrading to omitted filters', () => {
  for (const selection of [{ dimensions: null }, { source_id: null }, { host: null }, { service: null }]) expect(() => restoreAnalysisIdentityFilters(selection as unknown as AnalysisIdentitySelection)).toThrow()
})


it('uses the exact Go Unicode whitespace set without JavaScript BOM trimming', () => {
  for (const point of [9,10,11,12,13,32,0x85,0xa0,0x1680,...Array.from({length:11},(_,i)=>0x2000+i),0x2028,0x2029,0x202f,0x205f,0x3000]) expect(analysisIdentityIsGoWhitespace(String.fromCodePoint(point))).toBe(true)
  for (const text of ['\ufeff','\u180e','\u200b','x','😀',' x ']) expect(analysisIdentityIsGoWhitespace(text)).toBe(false)
})


it('requires an explicit finite imported identity peer capability before offering schema 2', () => {
  for (const value of [undefined, null, 0, 1, '2', NaN, Infinity, 2.5, Number.MAX_SAFE_INTEGER + 1]) expect(analysisImportedIdentityAvailable(value)).toBe(false)
  for (const value of [2, 3]) expect(analysisImportedIdentityAvailable(value)).toBe(true)
})


it('distinguishes explicit imported selector presence from legacy run monitor selectors without reading getters', () => {
  for (const field of ['source_id', 'host', 'service', 'dimensions', 'transaction', 'protocol', 'group']) expect(analysisImportedSelectorsRequested({ dataset_id: 'a', [field]: '' })).toBe(true)
  expect(analysisImportedSelectorsRequested({ dataset_id: 'a' })).toBe(false)
  expect(analysisImportedSelectorsRequested({ run_id: 'run', source_id: 'monitor', host: 'host', service: 'service', dimensions: { method: 'Get' } })).toBe(false)
  expect(analysisImportedSelectorsRequested({ run_id: 'run', transaction: 'Lookup', protocol: 'grpc', group: 'rpc' })).toBe(false)
  let reads = 0
  const value = Object.defineProperty({}, 'source_id', { get: () => { reads++; return 'hidden' } })
  expect(analysisImportedSelectorsRequested(value)).toBe(true)
  expect(reads).toBe(0)
})

it('folds imported named selectors into exact owned dimensions without duplicating matching aliases', () => {
  const selection = { transaction: 'Lookup', protocol: 'grpc', group: ' exact group ', dimensions: JSON.parse('{"group":" exact group ","__proto__":"ordinary"}') }
  const filters = restoreAnalysisImportedIdentityFilters(selection)
  expect(buildAnalysisIdentitySelection(filters)).toEqual({ dimensions: JSON.parse('{"group":" exact group ","__proto__":"ordinary","transaction":"Lookup","protocol":"grpc"}') })
  filters.dimensions[0].value = 'edited'
  expect(selection.dimensions.group).toBe(' exact group ')
})

it('refuses conflicting or oversized imported alias populations before restoring them', () => {
  expect(() => restoreAnalysisImportedIdentityFilters({ protocol: 'grpc', dimensions: { protocol: 'http' } })).toThrow('Conflicting')
  const dimensions = Object.fromEntries(Array.from({ length: 16 }, (_, index) => [`label${index}`, '']))
  expect(() => restoreAnalysisImportedIdentityFilters({ transaction: 'Lookup', dimensions })).toThrow('16')
  expect(() => restoreAnalysisImportedIdentityFilters({ transaction: 'x'.repeat(129) })).toThrow('UTF-8')
  expect(() => restoreAnalysisImportedIdentityFilters({ group: 'x'.repeat(129) })).toThrow('UTF-8')
  expect(() => restoreAnalysisImportedIdentityFilters({ protocol: 'x'.repeat(65) })).toThrow('UTF-8')
  expect(Object.keys(dimensions)).toHaveLength(16)
})


it('requires imported identity for version 2 or explicit identity metadata without evaluating descriptors', () => {
  expect(analysisMappingRequiresImportedIdentity({ version: 1 })).toBe(false)
  for (const value of [{ version: 2 }, { version: 3 }, { version: 1, identity: {} }]) expect(analysisMappingRequiresImportedIdentity(value)).toBe(true)
  let reads = 0
  for (const field of ['identity', 'version']) {
    const value = Object.defineProperty({}, field, { get: () => { reads++; return 2 } })
    expect(analysisMappingRequiresImportedIdentity(value)).toBe(true)
  }
  expect(reads).toBe(0)
})

it('refuses protected value columns while preserving arbitrary dimension output names', () => {
  for (const field of ['source', 'host', 'service'] as const) expect(analysisIdentityMappingError({ [field]: ['credentials', 'password'] })).toContain('Protected credential columns')
  expect(analysisIdentityMappingError({ dimensions: [{ name: 'method', value: ['api', 'key'] }] })).toContain('Protected credential columns')
  expect(analysisIdentityMappingError({ dimensions: [{ name: 'token', value: ['method'] }, { name: '__proto__', value: ['partition'] }] })).toBeUndefined()
})


it('refuses protected timestamp, value and name-column projection in every scalar mapping version', () => {
  const safe = { timestamp: ['ts'], measures: [{ value: ['value'], name: 'password', name_column: [], kind: 'gauge', unit: 'count' }], identity: { dimensions: [{ name: 'token', value: ['method'] }] } }
  for (const version of [1, 2]) {
    expect(analysisMappingDisclosureError({ ...safe, version })).toBeUndefined()
    for (const changed of [
      { ...safe, timestamp: ['api', 'key'] },
      { ...safe, measures: [{ ...safe.measures[0], value: ['credentials', 'password'] }] },
      { ...safe, measures: [{ ...safe.measures[0], name: '', name_column: ['%70assword'] }] },
    ]) expect(analysisMappingDisclosureError({ ...changed, version })).toContain('Protected credential columns')
  }
  expect(analysisMappingDisclosureError({ timestamp: [], measures: [] })).toBeUndefined()
})

it('admits only finite own source paths before optional getters, iteration or nested copying', () => {
  let reads = 0
  const fail = () => { reads++; throw new Error('unadmitted') }
  const timestamp = Object.defineProperty({}, 'timestamp', { get: fail })
  const measure = Object.defineProperty({}, 'name_column', { get: fail })
  const identity = Object.defineProperty({}, 'source', { get: fail })
  for (const mapping of [timestamp, { measures: [measure] }, { identity }, { timestamp: Array(17).fill('safe') }, { measures: Array(65).fill({}) }, { measures: [{ value: ['x'.repeat(4 * 1024 * 1024)] }] }]) expect(analysisMappingDisclosureError(mapping)).toBeDefined()
  const path = ['safe']
  Object.defineProperty(path, Symbol.iterator, { value: fail })
  const inherited = Object.create({ name_column: 'password' })
  expect(analysisMappingDisclosureError({ timestamp: path })).toBeUndefined()
  expect(analysisMappingDisclosureError({ measures: [inherited] })).toBeDefined()
  const own = Object.defineProperty({}, 'revision', { get: fail })
  expect(analysisMappingDisclosureError(own)).toBeUndefined()
  expect(reads).toBe(0)
})


it('refuses hostile descriptor-trap errors without inspecting their messages', () => {
  let reads = 0
  const hostile = new Error()
  Object.defineProperty(hostile, 'message', { get: () => { reads++; throw new Error('message getter') } })
  const source = new Proxy({}, { getOwnPropertyDescriptor() { throw hostile } })
  expect(analysisMappingDisclosureError(source)).toBe('Mapped source columns must use finite scalar paths.')
  expect(reads).toBe(0)
})
