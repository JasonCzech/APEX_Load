import { expect, it } from 'vitest'
import { nativeDeclarationErrors, nativeJavaScriptTemplate, NATIVE_JAVA_EXAMPLES } from './nativeServicesModel'

it('validates exact aliases and guest operation declarations', () => {
  expect(nativeDeclarationErrors([{ alias: 'work', operations: ['lease', 'ack'] }])).toEqual([])
  expect(nativeDeclarationErrors([{ alias: 'a'.repeat(64), operations: ['read'] }])).toEqual([])
  expect(nativeDeclarationErrors([{ alias: 'a'.repeat(65), operations: ['read'] }])[0]).toContain('at most 64')
  expect(nativeDeclarationErrors([{ alias: 'work', operations: [] }, { alias: 'work', operations: ['reset'] }])).toEqual(expect.arrayContaining(['Declaration 1: select at least one allowed operation.', 'Duplicate live-table declaration: work.', 'Declaration 2: an operation is not supported.']))
  expect(nativeDeclarationErrors([{ alias: 'unsafe.alias', operations: ['read'] }])[0]).toContain('valid alias')
  expect(nativeDeclarationErrors(Array.from({ length: 17 }, (_, index) => ({ alias: `a${index}`, operations: ['read'] })))[0]).toContain('at most 16')
})

it('keeps source literal and resolved inputs separate in native examples', () => {
  const producer = nativeJavaScriptTemplate('producer')
  expect(producer.params?.source).not.toContain('{{')
  expect(producer.params?.inputs).toEqual({ id: '{{var.id}}' })
  expect(nativeJavaScriptTemplate('update').params?.source).toContain('expected_version: input.version')
  expect(nativeJavaScriptTemplate('lease').params?.source).toContain('table.ack(result.lease_ref)')
})

it('uses the published Java SDK contract and explicit operations for all workflows', () => {
  expect(NATIVE_JAVA_EXAMPLES.map(example => example.name)).toEqual(['Producer', 'Consumer', 'Conditional update', 'Lease and acknowledge'])
  for (const example of NATIVE_JAVA_EXAMPLES) {
    expect(example.source).toContain('import apexload.data.Data;')
    expect(example.source).toContain('public int action()')
    expect(nativeDeclarationErrors([{ alias: 'work', operations: example.operations }])).toEqual([])
  }
  expect(NATIVE_JAVA_EXAMPLES[2].source).toContain('table.update(row.id(), row.version(),')
  expect(NATIVE_JAVA_EXAMPLES[3].source).toContain('table.ack(result.leaseRef())')
  expect(nativeDeclarationErrors([{ alias: 'Work', operations: ['read'] }, { alias: 'work', operations: ['read', 'read'] }])).toHaveLength(2)
})
