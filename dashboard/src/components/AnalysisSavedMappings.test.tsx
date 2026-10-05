// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisSavedMappings from './AnalysisSavedMappings'
import type { AnalysisMapping } from '../types/analysis'

const request = vi.hoisted(()=>vi.fn())
vi.mock('../hooks/apiClient',()=>({request}))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT=true
let container:HTMLElement;let root:Root
beforeEach(()=>{container=document.createElement('div');document.body.appendChild(container);root=createRoot(container);request.mockReset();request.mockResolvedValue([])})
afterEach(()=>{act(()=>root.unmount());container.remove()})

it('refuses saving protected identity values while retaining the original mapping',async()=>{
  const mapping:AnalysisMapping={version:2,timestamp:['ts'],timestamp_encoding:'unix_s',timezone:'UTC',measures:[{value:['value'],name:'latency',kind:'gauge',unit:'ms'}],identity:{service:['connection_string']}}
  await act(async()=>root.render(<AnalysisSavedMappings projectId="p" mapping={mapping} canEdit onLoad={()=>{}} />))
  const input=container.querySelector('input')!
  await act(async()=>{Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'Saved evidence');input.dispatchEvent(new Event('input',{bubbles:true}))})
  const save=Array.from(container.querySelectorAll('button')).find(button=>button.textContent==='Save mapping')!
  expect(save.disabled).toBe(true)
  await act(async()=>save.click())
  expect(request.mock.calls.some(([,options])=>options?.method==='POST')).toBe(false)
  expect(mapping.identity?.service).toEqual(['connection_string'])
})


it('refuses a protected returned saved definition before accepting its receipt', async () => {
  const mapping: AnalysisMapping = { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: ['value'], name: 'latency', kind: 'gauge', unit: 'ms' }], identity: { source: ['source'] } }
  const onLoad = vi.fn()
  request.mockImplementation((_path, options) => Promise.resolve(options?.method === 'POST' ? { id: 'saved', name: 'Saved evidence', revision: 1, deleted: false, definition: { ...mapping, identity: { source: ['password'] } } } : []))
  await act(async () => root.render(<AnalysisSavedMappings projectId="p" mapping={mapping} canEdit onLoad={onLoad} />))
  const input = container.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Saved evidence'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  const save = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save mapping')!
  expect(save.disabled).toBe(false)
  await act(async () => save.click())
  expect(request.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1)
  expect(onLoad).not.toHaveBeenCalled()
  expect(container.querySelector('[role=alert]')?.textContent).toContain('Protected credential columns')
  expect(mapping.identity?.source).toEqual(['source'])
})


for (const source of ['timestamp', 'value', 'name_column']) it(`refuses saved mapping publication of protected v1 ${source} without changing constant output names`, async () => {
  const mapping: AnalysisMapping = { version: 1, timestamp: source === 'timestamp' ? ['password'] : ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ value: source === 'value' ? ['password'] : ['v'], name: source === 'name_column' ? '' : 'password', name_column: source === 'name_column' ? ['password'] : [], kind: 'gauge', unit: 'count' }] }
  await act(async () => root.render(<AnalysisSavedMappings projectId="p" mapping={mapping} canEdit onLoad={() => {}} />))
  const input = container.querySelector('input')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Saved evidence'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  const save = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Save mapping')!
  expect(save.disabled).toBe(true)
  await act(async () => save.click())
  expect(request.mock.calls.some(([, options]) => options?.method === 'POST')).toBe(false)
  expect(mapping.measures[0].name).toBe(source === 'name_column' ? '' : 'password')
})
