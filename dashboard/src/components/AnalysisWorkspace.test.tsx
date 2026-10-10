// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisWorkspace from './AnalysisWorkspace'

const request = vi.hoisted(() => vi.fn())
const apiFetch = vi.hoisted(() => vi.fn())
const downloadBlob = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request, apiFetch }))
vi.mock('../utils/fileUtils', () => ({ downloadBlob }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset(); apiFetch.mockReset(); downloadBlob.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })

it('reports the native capability and hides unsupported imports', async () => {
  request.mockResolvedValue({ native_parquet: false, enabled: true })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  expect(container.textContent).toContain('Native Parquet analysis is not enabled')
  expect(container.querySelector('input[type=file]')).toBeNull()
})

it('allows a viewer to inspect dataset state without upload or deletion controls', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { native_parquet: true, enabled: true } : [{ id: 'one', name: 'My dataset', state: 'cancelled', files: [], accepted: 0, rejected: 0 }])
  await act(async () => root.render(<AnalysisWorkspace projectId="p" />))
  const item = [...container.querySelectorAll('button')].find(b => b.textContent?.includes('My dataset'))!
  await act(async () => item.click())
  expect(container.textContent).toContain('State: cancelled')
  expect(container.querySelector('input[type=file]')).toBeNull()
  expect(container.textContent).not.toContain('Delete dataset')
})

function deferred<T>() {
  let resolve!: (value: T) => void
  return { promise: new Promise<T>(done => { resolve = done }), resolve: (value: T) => resolve(value) }
}
const imported = { id: 'old', project_id: 'p', name: 'Old project evidence', revision: 1, state: 'uploading', files: [{ id: 'file', relative_path: 'sample.parquet', size: 1, uploaded: 1 }], accepted: 0, rejected: 0, mapping: { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{value:['value'],name:'latency',kind:'gauge',unit:'ms'}] } }
const caps = { native_parquet:true,enabled:true,imported_identity_version:2,chunk_bytes:4*1024*1024 }
const button = (label: string) => [...container.querySelectorAll('button')].find(b=>b.textContent===label)!

it('refuses late successful import creation before selection or upload after a project switch', async () => {
  const pending = deferred<typeof imported>(); let createSignal: AbortSignal | undefined
  request.mockImplementation((path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return Promise.resolve(caps)
    if (options?.method==='POST') { createSignal=options.signal as AbortSignal; return pending.promise }
    return Promise.resolve([])
  })
  await act(async()=>root.render(<AnalysisWorkspace projectId="p" canEdit />))
  const input = container.querySelector('input[type=file]')!
  await act(async()=>{ Object.defineProperty(input,'files',{configurable:true,value:[new File(['x'],'sample.parquet')]});input.dispatchEvent(new Event('change',{bubbles:true})) })
  await act(async()=>button('Upload files').click())
  expect(createSignal?.aborted).toBe(false)
  await act(async()=>root.render(<AnalysisWorkspace projectId="q" canEdit />))
  expect(createSignal?.aborted).toBe(true)
  await act(async()=>pending.resolve(imported))
  expect(container.textContent).not.toContain(imported.name)
  expect(container.textContent).not.toContain('Upload complete')
  expect(request.mock.calls.some(([,options])=>options?.method==='PUT')).toBe(false)
})

it('refuses late saved mapping before another commit or old-project state publication', async () => {
  const pending = deferred<typeof imported>(); let mappingSignal: AbortSignal | undefined
  request.mockImplementation((path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return Promise.resolve(caps)
    if (path.includes('/mapping') && options?.method==='PUT') { mappingSignal=options.signal as AbortSignal;return pending.promise }
    if (path.endsWith('/preview')) return Promise.resolve({schema:{rows:1,columns:[{path:'ts',path_components:['ts'],physical_type:'INT64',repeated:false},{path:'value',path_components:['value'],physical_type:'DOUBLE',repeated:false}]},samples:[]})
    if (path==='/analysis/imports?project_id=p') return Promise.resolve([imported])
    return Promise.resolve([])
  })
  await act(async()=>root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async()=>[...container.querySelectorAll('button')].find(b=>b.textContent?.includes(imported.name))!.click())
  await act(async()=>button('Validate and import').click())
  expect(mappingSignal?.aborted).toBe(false)
  await act(async()=>root.render(<AnalysisWorkspace projectId="q" canEdit />))
  expect(mappingSignal?.aborted).toBe(true)
  await act(async()=>pending.resolve({...imported,revision:2,state:'mapped'}))
  expect(request.mock.calls.filter(([path])=>String(path).endsWith('/commit'))).toHaveLength(0)
  expect(container.textContent).not.toContain(imported.name)
  expect(container.querySelector('input[type=file]')).not.toBeNull()
})

it('refuses late commit and delete results after a new project has selected its own evidence', async () => {
  for (const action of ['commit','delete']) {
    const pending = deferred<typeof imported>(); const old = {...imported,state:action==='delete'?'ready':'uploading',files:action==='delete'?[]:imported.files}
    const current = {...imported,id:'current',project_id:'q',name:'Current project evidence',state:'cancelled',files:[]}
    request.mockImplementation((path: string, options?: RequestInit) => {
      if(path.includes('capabilities')) return Promise.resolve(caps)
      if(options?.method==='DELETE'||path.endsWith('/commit')) return pending.promise
      if(options?.method==='PUT') return Promise.resolve({...old,revision:2,state:'mapped'})
      if(path.endsWith('/preview')) return Promise.resolve({schema:{rows:1,columns:[{path:'ts',path_components:['ts'],physical_type:'INT64',repeated:false},{path:'value',path_components:['value'],physical_type:'DOUBLE',repeated:false}]},samples:[]})
      if(path==='/analysis/imports?project_id=p') return Promise.resolve([old])
      if(path==='/analysis/imports?project_id=q') return Promise.resolve([current])
      return Promise.resolve([])
    })
    await act(async()=>root.render(<AnalysisWorkspace projectId="p" canEdit />))
    await act(async()=>[...container.querySelectorAll('button')].find(b=>b.textContent?.includes(old.name))!.click())
    await act(async()=>button(action==='delete'?'Delete dataset':'Validate and import').click())
    await act(async()=>root.render(<AnalysisWorkspace projectId="q" canEdit />))
    await act(async()=>[...container.querySelectorAll('button')].find(b=>b.textContent?.includes(current.name))!.click())
    await act(async()=>pending.resolve({...old,state:'ready'}))
    expect(container.querySelector('h3')?.textContent).toBe('Datasets and imports')
    expect([...container.querySelectorAll('h3')].some(h=>h.textContent===current.name)).toBe(true)
    expect([...container.querySelectorAll('h3')].some(h=>h.textContent===old.name)).toBe(false)
    // Complete this fixture's exact operations before beginning the next one.
    await act(async()=>root.render(<AnalysisWorkspace projectId={undefined} canEdit />))
  }
})

it('withholds a late rejected-row blob download after the originating project aborts', async () => {
  const pending = deferred<Blob>()
  const item = {...imported,state:'ready',files:[],rejection_artifacts:['bounded-object']}
  request.mockImplementation(async(path:string)=>path.includes('capabilities')?caps:path==='/analysis/imports?project_id=p'?[item]:[])
  const blob = vi.fn(()=>pending.promise)
  apiFetch.mockResolvedValue({ok:true,headers:new Headers({'Content-Length':'12'}),blob})
  await act(async()=>root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async()=>[...container.querySelectorAll('button')].find(b=>b.textContent?.includes(item.name))!.click())
  await act(async()=>button('Download rejected row set 1').click())
  expect(blob).toHaveBeenCalledTimes(1)
  await act(async()=>root.render(<AnalysisWorkspace projectId="q" canEdit />))
  expect(apiFetch.mock.calls[0][1].signal.aborted).toBe(true)
  await act(async()=>pending.resolve(new Blob(['retained data'])))
  expect(downloadBlob).not.toHaveBeenCalled()
  expect(container.textContent).not.toContain(item.name)
})


it('pauses an owned pending operation immediately and refuses its late finalizer over a new operation', async () => {
  const first = deferred<typeof imported>(); const second = deferred<typeof imported>(); const signals: AbortSignal[] = []
  request.mockImplementation((path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return Promise.resolve(caps)
    if (options?.method === 'POST') { signals.push(options.signal as AbortSignal); return signals.length === 1 ? first.promise : second.promise }
    return Promise.resolve([])
  })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  const input = container.querySelector('input[type=file]')!
  await act(async () => { Object.defineProperty(input, 'files', { configurable: true, value: [new File(['x'], 'sample.parquet')] }); input.dispatchEvent(new Event('change', { bubbles: true })) })
  await act(async () => button('Upload files').click())
  expect((button('Upload files') as HTMLButtonElement).closest('fieldset')?.disabled).toBe(true)
  await act(async () => button('Pause operation').click())
  expect(signals[0].aborted).toBe(true)
  expect((button('Upload files') as HTMLButtonElement).closest('fieldset')?.disabled).toBe(false)
  expect(button('Pause operation')).toBeUndefined()
  await act(async () => button('Upload files').click())
  expect(signals).toHaveLength(2)
  await act(async () => first.resolve(imported))
  expect(signals[1].aborted).toBe(false)
  expect((button('Upload files') as HTMLButtonElement).closest('fieldset')?.disabled).toBe(true)
  expect(button('Pause operation')).toBeDefined()
  expect(container.textContent).not.toContain(imported.name)
  await act(async () => button('Pause operation').click())
  await act(async () => second.resolve(imported))
  expect((button('Upload files') as HTMLButtonElement).closest('fieldset')?.disabled).toBe(false)
  expect(request.mock.calls.filter(([, options]) => options?.method === 'PUT')).toHaveLength(0)
})

for (const files of [[], [{ id: 'incomplete' }]]) it('retains ready dataset mapping and analysis without inventing an uploaded first file', async () => {
  const ready = { ...imported, state: 'ready', files }
  request.mockImplementation(async (path: string) => {
    if (path.includes('capabilities')) return caps
    if (path === '/analysis/imports?project_id=p') return [ready]
    if (path.startsWith('/analysis/comparisons?')) return []
    if (path.includes('/preview')) throw new Error('No admitted uploaded first file exists')
    return []
  })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent?.includes(ready.name))!.click())
  expect(container.textContent).toContain('State: ready')
  expect(container.querySelector('section[aria-label="Dataset comparison"]')).not.toBeNull()
  const metric = [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith('Metric'))!.querySelector('input')!
  expect(metric.value).toBe('latency')
  expect(container.textContent).not.toContain('TypeError')
  expect(request.mock.calls.some(([path]) => String(path).includes('/preview'))).toBe(false)
  expect(request.mock.calls.some(([path]) => String(path).startsWith('/analysis/comparisons?'))).toBe(true)
})


for (const version of [undefined, 1]) it(`preserves a v2 dataset but refuses its query and comparison on old identity capability ${version}`, async () => {
  const original = { ...imported, state: 'ready', files: [] }
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { ...caps, imported_identity_version: version } : path.startsWith('/analysis/imports?') ? [original] : [])
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes(original.name))!.click())
  expect(container.textContent).toContain('This dataset requires imported identity schema 2')
  const query = [...container.querySelectorAll('fieldset')].find(fieldset => fieldset.querySelector('legend')?.textContent === 'Query dataset')!
  expect(query.disabled).toBe(true)
  expect(button('Analyze comparison').disabled).toBe(true)
  expect(request.mock.calls.some(([path]) => String(path).endsWith('/series') || String(path).startsWith('/analysis/compare?'))).toBe(false)
  expect(original.mapping.version).toBe(2)
})

it('keeps version 1 mapping and series available without any identity capability', async () => {
  const original = { ...imported, mapping: { ...imported.mapping, version: 1 } }
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return { native_parquet: true, enabled: true, chunk_bytes: 4 * 1024 * 1024 }
    if (path.startsWith('/analysis/imports?')) return [original]
    if (path.endsWith('/preview')) return { schema: { rows: 1, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64', repeated: false }, { path: 'value', path_components: ['value'], physical_type: 'DOUBLE', repeated: false }] }, samples: [] }
    if (path.endsWith('/mapping')) { expect(JSON.parse(String(options?.body)).mapping.version).toBe(1); return { ...original, state: 'mapped', revision: 2 } }
    if (path.endsWith('/commit')) return { ...original, state: 'ready', revision: 3 }
    if (path.endsWith('/series')) { expect(JSON.parse(String(options?.body))).not.toHaveProperty('identity_schema_version'); return { points: [] } }
    return []
  })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes(original.name))!.click())
  expect(container.querySelector('fieldset[aria-label="Imported measurement identities"]')).toBeNull()
  expect(button('Validate and import').disabled).toBe(false)
  await act(async () => button('Validate and import').click())
  const query = [...container.querySelectorAll('fieldset')].find(fieldset => fieldset.querySelector('legend')?.textContent === 'Query dataset')!
  expect(query.disabled).toBe(false)
  const dates = query.querySelectorAll<HTMLInputElement>('input[type="datetime-local"]')
  await act(async () => {
    for (const [i, element] of [...dates].entries()) { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(element, i ? '2026-01-01T00:01' : '2026-01-01T00:00'); element.dispatchEvent(new Event('input', { bubbles: true })) }
  })
  await act(async () => button('Show series').click())
  expect(request.mock.calls.filter(([path]) => String(path).endsWith('/series'))).toHaveLength(1)
})

it('retains an imported v2 mapping draft instead of lowering it for an older peer', async () => {
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { ...caps, imported_identity_version: undefined } : path.startsWith('/analysis/imports?') ? [imported] : path.endsWith('/preview') ? { schema: { rows: 1, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64', repeated: false }] }, samples: [] } : [])
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes(imported.name))!.click())
  expect(container.textContent).toContain('This identity mapping is retained')
  expect([...container.querySelectorAll('button')].some(item => item.textContent === 'Save mapping')).toBe(false)
  expect(button('Validate and import').disabled).toBe(true)
  expect(request.mock.calls.some(([, options]) => options?.method === 'PUT' || options?.method === 'POST')).toBe(false)
  expect(imported.mapping.version).toBe(2)
})


it('refuses ready v1 identity metadata on an older peer without changing its stored declaration', async () => {
  const original = { ...imported, state: 'ready', files: [], mapping: { ...imported.mapping, version: 1, identity: { source: ['source'] } } }
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? { ...caps, imported_identity_version: undefined } : path.startsWith('/analysis/imports?') ? [original] : [])
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes(original.name))!.click())
  const query = [...container.querySelectorAll('fieldset')].find(fieldset => fieldset.querySelector('legend')?.textContent === 'Query dataset')!
  expect(query.disabled).toBe(true)
  expect(button('Analyze comparison').disabled).toBe(true)
  expect(original.mapping).toMatchObject({ version: 1, identity: { source: ['source'] } })
  expect(request.mock.calls.some(([path]) => String(path).endsWith('/series') || String(path).startsWith('/analysis/compare?'))).toBe(false)
})

for (const state of ['uploading', 'ready']) it(`retains protected identity mapping in ${state} state while refusing label publication/query`, async () => {
  const identity = {source:['credentials','password'], dimensions:[{name:'method',value:['api','key']}]}
  const original = {...imported,state,mapping:{...imported.mapping,identity}, files:state==='ready'?[]:imported.files}
  request.mockImplementation(async(path:string)=>path.includes('capabilities')?caps:path.startsWith('/analysis/imports?')?[original]:path.endsWith('/preview')?{schema:{rows:1,columns:[{path:'ts',path_components:['ts'],physical_type:'INT64',repeated:false},{path:'value',path_components:['value'],physical_type:'DOUBLE',repeated:false},{path:'credentials.password',path_components:['credentials','password'],physical_type:'BYTE_ARRAY',repeated:false},{path:'api.key',path_components:['api','key'],physical_type:'BYTE_ARRAY',repeated:false}]},samples:[]}:[])
  await act(async()=>root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async()=>[...container.querySelectorAll('button')].find(item=>item.textContent?.includes(original.name))!.click())
  expect(container.textContent).toContain('Protected credential columns')
  if(state==='uploading') expect(button('Validate and import').disabled).toBe(true)
  else expect([...container.querySelectorAll('fieldset')].find(fieldset=>fieldset.querySelector('legend')?.textContent==='Query dataset')?.disabled).toBe(true)
  expect(request.mock.calls.some(([path,options])=>String(path).endsWith('/series')||options?.method==='PUT'||String(path).endsWith('/commit'))).toBe(false)
  expect(original.mapping.identity).toEqual(identity)
})

it('refuses a protected mapping reply before the follow-up commit',async()=>{
  request.mockImplementation(async(path:string,options?:RequestInit)=>{
    if(path.includes('capabilities'))return caps
    if(path.startsWith('/analysis/imports?'))return [imported]
    if(path.endsWith('/preview'))return {schema:{rows:1,columns:[{path:'ts',path_components:['ts'],physical_type:'INT64',repeated:false},{path:'value',path_components:['value'],physical_type:'DOUBLE',repeated:false}]},samples:[]}
    if(options?.method==='PUT')return {...imported,state:'mapped',revision:2,mapping:{...imported.mapping,identity:{service:['password']}}}
    return []
  })
  await act(async()=>root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async()=>[...container.querySelectorAll('button')].find(item=>item.textContent?.includes(imported.name))!.click())
  await act(async()=>button('Validate and import').click())
  expect(container.textContent).toContain('Protected credential columns')
  expect(request.mock.calls.filter(([,options])=>options?.method==='PUT')).toHaveLength(1)
  expect(request.mock.calls.some(([path])=>String(path).endsWith('/commit'))).toBe(false)
})


for (const version of [1, 2]) for (const source of ['timestamp', 'value', 'name_column']) for (const state of ['uploading', 'ready']) it(`retains protected ${source} mapping v${version}/${state} and refuses normalization or queries`, async () => {
  const originalMapping = { ...imported.mapping, version, ...(source === 'timestamp' ? { timestamp: ['password'] } : { measures: [{ ...imported.mapping.measures[0], ...(source === 'value' ? { value: ['password'] } : { name: '', name_column: ['password'] }) }] }) }
  const original = { ...imported, state, mapping: originalMapping, files: state === 'ready' ? [] : imported.files }
  request.mockImplementation(async (path: string) => path.includes('capabilities') ? caps : path.startsWith('/analysis/imports?') ? [original] : path.endsWith('/preview') ? { schema: { rows: 1, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64', repeated: false }, { path: 'value', path_components: ['value'], physical_type: 'DOUBLE', repeated: false }, { path: 'password', path_components: ['password'], physical_type: 'DOUBLE', repeated: false }] }, samples: [] } : [])
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes(original.name))!.click())
  expect(container.textContent).toContain('Protected credential columns')
  if (state === 'uploading') {
    expect(button('Validate and import').disabled).toBe(true)
    const timestamp = [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith('Timestamp column'))!.querySelector('select')!
    expect(Array.from(timestamp.options).find(option => option.value === '["password"]')?.disabled).toBe(true)
    const protectedMeasure = [...container.querySelectorAll('.analysis-measure label')].find(label => label.textContent?.startsWith('password'))?.querySelector('input')
    if (source === 'value') {
      expect(protectedMeasure?.checked).toBe(true)
      expect(protectedMeasure?.disabled).toBe(false) // Existing selection may be removed.
    } else if (source !== 'timestamp') expect(protectedMeasure?.disabled).toBe(true)
    const nameColumn = [...container.querySelectorAll('label')].find(label => label.textContent?.startsWith('Metric name column'))?.querySelector('select')
    expect(nameColumn).toBeTruthy()
    expect(Array.from(nameColumn?.options ?? []).find(option => option.value === '["password"]')?.disabled).toBe(true)
  } else {
    expect([...container.querySelectorAll('fieldset')].find(fieldset => fieldset.querySelector('legend')?.textContent === 'Query dataset')?.disabled).toBe(true)
    expect(button('Analyze comparison').disabled).toBe(true)
  }
  expect(request.mock.calls.some(([path, options]) => String(path).endsWith('/series') || options?.method === 'PUT' || String(path).endsWith('/commit'))).toBe(false)
  expect(original.mapping).toEqual(originalMapping)
})

for (const source of ['timestamp', 'value', 'name_column']) it(`refuses a returned protected ${source} mapping before commit`, async () => {
  request.mockImplementation(async (path: string, options?: RequestInit) => {
    if (path.includes('capabilities')) return caps
    if (path.startsWith('/analysis/imports?')) return [imported]
    if (path.endsWith('/preview')) return { schema: { rows: 1, columns: [{ path: 'ts', path_components: ['ts'], physical_type: 'INT64', repeated: false }, { path: 'value', path_components: ['value'], physical_type: 'DOUBLE', repeated: false }] }, samples: [] }
    if (options?.method === 'PUT') return { ...imported, state: 'mapped', revision: 2, mapping: { ...imported.mapping, ...(source === 'timestamp' ? { timestamp: ['password'] } : { measures: [{ ...imported.mapping.measures[0], ...(source === 'value' ? { value: ['password'] } : { name: '', name_column: ['password'] }) }] }) } }
    return []
  })
  await act(async () => root.render(<AnalysisWorkspace projectId="p" canEdit />))
  await act(async () => [...container.querySelectorAll('button')].find(item => item.textContent?.includes(imported.name))!.click())
  await act(async () => button('Validate and import').click())
  expect(container.textContent).toContain('Protected credential columns')
  expect(request.mock.calls.filter(([, options]) => options?.method === 'PUT')).toHaveLength(1)
  expect(request.mock.calls.some(([path]) => String(path).endsWith('/commit'))).toBe(false)
})
