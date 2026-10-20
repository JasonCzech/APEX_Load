import { expect, it } from 'vitest'
import type { AnalysisImport, AnalysisMapping } from '../types/analysis'
import { admitMappingAssociationResponse, mappingAssociationCapabilityError, mappingRequiresNegotiatedAssociation } from './analysisMappingAssociation'

const mapping: AnalysisMapping = { version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['v'], name: 'latency', kind: 'summary', unit: 'ms' }] }
const before: AnalysisImport = { id: 'dataset', project_id: 'p', revision: 2, state: 'ready', files: [], accepted: 1, rejected: 0, name: 'Dataset', mapping }

it('keeps plain legacy mappings and display-only summaries on the old route', () => {
  expect(mappingRequiresNegotiatedAssociation(mapping)).toBe(false)
  expect(mappingAssociationCapabilityError(mapping)).toBeUndefined()
})

for (const [name, declaration] of Object.entries({ statistic: { statistic: 'p95' }, window: { window_seconds: 60 }, count: { sample_count_column: ['n'] }, sum: { sample_sum_column: ['s'] }, histogram: { kind: 'histogram' } })) {
  it(`requires an exact preservation contract for ${name}`, () => {
    const advanced = { ...mapping, measures: [{ ...mapping.measures[0], ...declaration }] }
    expect(mappingRequiresNegotiatedAssociation(advanced)).toBe(true)
    for (const version of [undefined, 0, 2, -1, NaN]) expect(mappingAssociationCapabilityError(advanced, version)).toBeTruthy()
    expect(mappingAssociationCapabilityError(advanced, 1)).toBeUndefined()
  })
}
it('requires the contract for identity mappings and refuses unknown future mappings', () => {
  expect(mappingAssociationCapabilityError({ ...mapping, version: 2 })).toBeTruthy()
  expect(mappingAssociationCapabilityError({ ...mapping, version: 2 }, 1)).toBeUndefined()
  expect(mappingAssociationCapabilityError({ ...mapping, version: 3 }, 1)).toBeTruthy()
})
it('admits only the same mapping and exact next association revision', () => {
  const advanced = { ...before, mapping: { ...mapping, measures: [{ ...mapping.measures[0], statistic: 'p95', window_seconds: 60 }] } }
  const correct = { contract_version: 1, import: { ...advanced, revision: 3, run_id: 'run' } }
  expect(admitMappingAssociationResponse(advanced, 'run', correct)).toBe(correct.import)
  const mutations = [
    { ...correct, contract_version: 2 },
    { ...correct, import: undefined },
    ...[{ id: 'foreign' }, { project_id: 'foreign' }, { revision: 2 }, { revision: 4 }, { run_id: 'other' }, { state: 'deleted' }, { mapping }].map(change => ({ ...correct, import: { ...correct.import, ...change } })),
  ]
  for (const response of mutations) expect(() => admitMappingAssociationResponse(advanced, 'run', response)).toThrow('did not confirm')
  const reordered = JSON.parse(JSON.stringify(correct))
  reordered.import.mapping = Object.fromEntries(Object.entries(reordered.import.mapping).reverse())
  expect(admitMappingAssociationResponse(advanced, 'run', reordered)).toBe(reordered.import)
})

it('refuses reassociation when mapping metadata is unavailable', () => {
  expect(mappingRequiresNegotiatedAssociation(undefined)).toBe(true)
  for (const advertised of [undefined, 1, 2]) expect(mappingAssociationCapabilityError(undefined, advertised)).toContain('Mapping details are unavailable')
})
