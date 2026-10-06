// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import AnalysisIdentityMapping from './AnalysisIdentityMapping'
import AnalysisIdentityFilters from './AnalysisIdentityFilters'
import type { AnalysisColumn, AnalysisIdentityFilters as IdentityFilters, AnalysisIdentityMapping as IdentityMapping, AnalysisMapping } from '../types/analysis'
import { buildAnalysisIdentitySelection, emptyAnalysisIdentityFilters } from '../utils/analysisIdentity'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(() => { act(() => root.unmount()); container.remove() })
const control = (label: string) => [...container.querySelectorAll('label')].find(element => element.textContent?.startsWith(label))!.querySelector('input,select') as HTMLInputElement | HTMLSelectElement
async function change(label: string, value: string) {
  await act(async () => {
    const element = control(label)
    Object.getOwnPropertyDescriptor(element.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
  })
}
async function click(name: string) { await act(async () => [...container.querySelectorAll('button')].find(element => element.textContent === name)!.click()) }

it('maps nested source labels in v2 and excludes repeated fields', async () => {
  let actual: AnalysisMapping | undefined
  function Harness() {
    const [mapping, setMapping] = useState<AnalysisMapping>({ version: 1, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['v'], name: 'v', kind: 'gauge', unit: 'count' }] })
    return <AnalysisIdentityMapping mapping={mapping} columns={[{ path: 'nested.source', path_components: ['nested', 'source'], physical_type: 'BYTE_ARRAY', repeated: false }, { path: 'group', path_components: ['group'], physical_type: 'BYTE_ARRAY', repeated: false }, { path: 'array', path_components: ['array'], physical_type: 'BYTE_ARRAY', repeated: true }]} onChange={next => { actual = next; setMapping(next) }} />
  }
  await act(async () => root.render(<Harness />))
  expect(container.textContent).not.toContain('array')
  await change('Source column', '["nested","source"]')
  await click('Add dimension')
  expect(container.textContent).toContain('A label name is required')
  await change('Dimension 1 name', 'a/b~c')
  await change('Dimension 1 column', '["group"]')
  expect(actual).toMatchObject({ version: 2, identity: { source: ['nested', 'source'], dimensions: [{ name: 'a/b~c', value: ['group'] }] } })
  expect(container.querySelector('[role=alert]')).toBeNull()
  await click('Remove dimension 1')
  expect(actual!.identity!.source).toEqual(['nested', 'source'])
})

it('keeps explicit empty label filters and rejects duplicate names', async () => {
  let actual = emptyAnalysisIdentityFilters()
  function Harness() {
    const [filters, setFilters] = useState(emptyAnalysisIdentityFilters)
    return <AnalysisIdentityFilters value={filters} onChange={next => { actual = next; setFilters(next) }} />
  }
  await act(async () => root.render(<Harness />))
  await click('Add dimension filter'); await change('Filter 1 name', '__proto__')
  expect(Object.prototype.hasOwnProperty.call(buildAnalysisIdentitySelection(actual).dimensions!, '__proto__')).toBe(true)
  expect(buildAnalysisIdentitySelection(actual).dimensions!.__proto__).toBe('')
  await click('Add dimension filter'); await change('Filter 2 name', '__proto__')
  expect(container.textContent).toContain('must be unique')
  await click('Remove filter 2')
  expect(container.querySelector('[role=alert]')).toBeNull()
})

for (const identity of [
  { dimensions: 'unsupported' },
  { dimensions: Array(17).fill(null) },
  { source: Array(17).fill('part') },
  { dimensions: [{ name: 'x'.repeat(129), value: ['v'] }] },
  { dimensions: [null] },
]) it('refuses malformed or excessive mapping controls before rendering rows', async () => {
  let reset: AnalysisMapping | undefined
  const mapping: AnalysisMapping = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [], identity: identity as unknown as IdentityMapping }
  await act(async () => root.render(<AnalysisIdentityMapping mapping={mapping} columns={[]} onChange={next => { reset = next }} />))
  expect(container.querySelector('[role=alert]')).not.toBeNull()
  expect(container.querySelectorAll('input,select')).toHaveLength(0)
  await click('Reset identity mapping')
  expect(reset).toMatchObject({ version: 2, identity: {} })
})

for (const value of [
  { source_id: '', host: '', service: '', dimensions: 'unsupported' },
  { source_id: '', host: '', service: '', dimensions: Array(17).fill(null) },
  { source_id: '', host: '', service: '', dimensions: [{ name: 'key', value: '😀'.repeat(33) }] },
  { source_id: '', host: '', service: '', dimensions: [null] },
]) it('refuses malformed or excessive filter controls before rendering rows', async () => {
  let reset: IdentityFilters | undefined
  await act(async () => root.render(<AnalysisIdentityFilters value={value as unknown as IdentityFilters} onChange={next => { reset = next }} />))
  expect(container.querySelector('[role=alert]')).not.toBeNull()
  expect(container.querySelectorAll('input,select')).toHaveLength(0)
  await click('Reset identity filters')
  expect(reset).toEqual(emptyAnalysisIdentityFilters())
})

it('refuses excessive or malformed schema options before serialization', async () => {
  const mapping: AnalysisMapping = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [] }
  for (const columns of [Array(1025).fill(null), [{ path: 'v', path_components: Array(17).fill('part'), repeated: false }]]) {
    await act(async () => root.render(<AnalysisIdentityMapping mapping={mapping} columns={columns as AnalysisColumn[]} onChange={() => {}} />))
    expect(container.querySelector('[role=alert]')).not.toBeNull()
    expect(container.querySelectorAll('input,select,option')).toHaveLength(0)
  }
})

it('keeps protected imported selections visible for correction and refuses them as scalar options', async () => {
  const mapping: AnalysisMapping = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [], identity: { source: ['credentials','password'], dimensions: [{name:'method',value:['api','key']}] } }
  let updated: AnalysisMapping | undefined
  await act(async () => root.render(<AnalysisIdentityMapping mapping={mapping} columns={[
    {path:'credentials.password',path_components:['credentials','password'],physical_type:'BYTE_ARRAY',repeated:false},
    {path:'api.key',path_components:['api','key'],physical_type:'BYTE_ARRAY',repeated:false},
    {path:'tokenization',path_components:['tokenization'],physical_type:'BYTE_ARRAY',repeated:false},
  ]} onChange={next => { updated=next }} />))
  expect(container.querySelector('[role=alert]')?.textContent).toContain('Protected credential columns')
  expect((control('Source column') as HTMLSelectElement).value).toBe('["credentials","password"]')
  for (const select of container.querySelectorAll('select')) {
    expect(Array.from(select.options).find(option=>option.value==='["credentials","password"]')?.disabled).toBe(true)
    expect(Array.from(select.options).find(option=>option.value==='["api","key"]')?.disabled).toBe(true)
    expect(Array.from(select.options).find(option=>option.value==='["tokenization"]')?.disabled).toBe(false)
  }
  await change('Source column','["tokenization"]')
  expect(updated?.identity?.source).toEqual(['tokenization'])
  expect(mapping.identity?.source).toEqual(['credentials','password'])
})
