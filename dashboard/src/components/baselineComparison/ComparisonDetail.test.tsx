// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { anatomyOf, requestRow, transaction } from './__fixtures__/anatomyDiff'
import { ComparisonDetail } from './ComparisonDetail'
import { defaultPolicy, type ComparisonRow, type ComparisonSnapshot, type Measurement, type RunIdentity } from './model'

vi.mock('../SeriesChartExport', () => ({default: ({children, context}: {children: ReactNode; context: unknown}) => <section data-export-context={JSON.stringify(context)}>{children}</section>}))
vi.mock('recharts', () => {
 const Container = ({children}: {children?: ReactNode}) => <div>{children}</div>
 return Object.fromEntries(['CartesianGrid','Legend','Line','LineChart','ResponsiveContainer','Tooltip','XAxis','YAxis'].map(key => [key,Container]))
})

it('shows canonical Prometheus comparison provenance, zero and gaps without request percentiles', async () => {
 const node=document.createElement('div');document.body.appendChild(node);const root=createRoot(node)
 const measurement={mean:0,min:0,max:0,samples:18,coverage_pct:90,instances:1,mean_statistic:'evaluation_grid_mean',available_statistics:['mean','min','max','samples','coverage_pct','instances'],points:[{seconds:0,value:0,load:100}]} as Measurement
 const row:ComparisonRow={id:'resource/0',name:'prometheus/cpu',source:'prometheus',group:'',unit:'percent',current:measurement,provider:{name:'prometheus',backend_profile:'azure_managed',query_id:'cpu',definition_hash:'a'.repeat(64),statistic:'gauge',evaluation_step_seconds:15,dimensions:{job:'checkoutapi'},entity_ids:{series:'b'.repeat(64)},display_labels:{Job:'CheckoutAPI'}},baselines:{baseline:{measurement,qualification:['Evaluation grid differs'],approximate:false}}}
 const snapshot:ComparisonSnapshot={id:'comparison',project_id:'default',current:{id:'current'} as RunIdentity,baselines:[{id:'baseline',name:'Baseline'} as RunIdentity],created_at:'2026-10-04T00:00:00Z',rule_version:'baseline-comparison-v1',request:{baseline_ids:['baseline'],population:'transaction',allow_other_scenarios:false},policy:defaultPolicy,transactions:[],resources:[row],setup:[],findings:[],warnings:[]}
 try {
  await act(async()=>root.render(<ComparisonDetail row={row} snapshot={snapshot} onClose={()=>{}} />))
  const dialog=document.querySelector('[role="dialog"]')!
  expect(dialog.textContent).toContain('Job: CheckoutAPI')
  expect(dialog.textContent).toContain('15s grid')
  expect(dialog.textContent).toContain('Query definition: '+ 'a'.repeat(64))
  expect(dialog.textContent).toContain('Mean of evaluation grid')
  expect(dialog.textContent).toContain('Evaluation grid coverage %')
  expect(dialog.textContent).toContain('absent grid cells remain missing')
  expect(dialog.textContent).toContain('Change unavailable')
  expect(Array.from(dialog.querySelectorAll('th[scope="row"]')).map(element=>element.textContent)).not.toContain('P95')
  expect(dialog.querySelector('tr:nth-child(2) td')?.textContent).toBe('0')
  expect(dialog.querySelector('[data-export-context]')?.getAttribute('data-export-context')).toContain('"definition_hash":"'+ 'a'.repeat(64)+'"')
 } finally {await act(async()=>root.unmount());node.remove()}
})

it('closes the time anatomy waterfall tooltip on Escape without closing the drawer', async () => {
 ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
 vi.stubGlobal('ResizeObserver', class { observe() {} disconnect() {} unobserve() {} })
 const node=document.createElement('div');document.body.appendChild(node);const root=createRoot(node)
 const run=(id:string)=>({id,name:id,release_id:id,release_name:'R1',scenario_id:'s',status:'COMPLETE',vusers:1,peak_vusers:1,pinned:true,archive_available:true}) as RunIdentity
 const row:ComparisonRow={id:'tx/0',name:'Login',source:'transaction',group:'g',unit:'ms',baselines:{}}
 const snapshot:ComparisonSnapshot={id:'c',project_id:'p',current:run('cur'),baselines:[run('base')],created_at:'',rule_version:'v1',request:{baseline_ids:['base'],population:'transaction',allow_other_scenarios:false},policy:defaultPolicy,transactions:[row],resources:[],setup:[],findings:[],warnings:[]}
 const load=(server:number)=>({status:'ready' as const,anatomy:anatomyOf([transaction('Login',[requestRow(1,'POST /login',{server}),requestRow(2,'GET /home',{server:server/2},{offset:server})],{server})])})
 const onClose=vi.fn()
 const escape=(target:Element)=>act(()=>{target.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}))})
 try {
  await act(async()=>root.render(<ComparisonDetail row={row} snapshot={snapshot} onClose={onClose} anatomy={{cur:load(200),base:load(100)}} />))
  const grid=document.querySelector<HTMLElement>('[role="dialog"] [role="treegrid"]')!
  expect(grid).toBeTruthy()
  act(()=>grid.focus())
  act(()=>{grid.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowDown',bubbles:true,cancelable:true}))})
  // The keyboard tooltip anchors to its row after an animation frame.
  await act(async()=>{await new Promise(resolve=>setTimeout(resolve,50))})
  expect(document.querySelector('[role="tooltip"]')).toBeTruthy()
  escape(grid)
  expect(document.querySelector('[role="tooltip"]')).toBeNull()
  expect(onClose).not.toHaveBeenCalled()
  // With no tooltip open, Escape closes the drawer as before.
  escape(grid)
  expect(onClose).toHaveBeenCalledTimes(1)
 } finally {await act(async()=>root.unmount());node.remove();vi.unstubAllGlobals()}
})
