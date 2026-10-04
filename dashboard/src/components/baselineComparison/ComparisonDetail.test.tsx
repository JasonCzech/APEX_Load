// @vitest-environment jsdom
import { act, type ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
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
