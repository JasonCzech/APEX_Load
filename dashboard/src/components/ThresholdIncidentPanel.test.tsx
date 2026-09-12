// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import Panel from './ThresholdIncidentPanel'
import { incidentHealth, type IncidentResponse, type Incident } from './thresholdIncidentModel'
import { request } from '../hooks/apiClient'
vi.mock('../hooks/apiClient', () => ({ request: vi.fn() }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const stamp = new Date().toISOString()
const incident: Incident = { id:'one', project_id:'p', test_id:'test', test_name:'Checkout', key:'key', severity:'danger', payload:{metric:'error_pct',value:7,threshold:5,action:'stop_requested'}, active:true, first_seen_at:stamp, last_seen_at:stamp }
const base: IncidentResponse = {incidents:[],limit:50,offset:0,global_policy_count:0,last_evaluated_at:null,generated_at:stamp,active_count:0,unacknowledged_count:0,danger_count:0}
let root: Root; let container: HTMLElement
beforeEach(() => { container=document.createElement('div');document.body.appendChild(container);root=createRoot(container);vi.mocked(request).mockResolvedValue(base) })
afterEach(() => { act(()=>root.unmount());container.remove();vi.resetAllMocks() })
it('only declares coverage healthy with enabled policies and recent samples', () => {
 expect(incidentHealth(base,null).label).toBe('Not configured')
 expect(incidentHealth({...base,global_policy_count:1},null).label).toBe('Waiting for samples')
 expect(incidentHealth({...base,global_policy_count:1,last_evaluated_at:stamp},null).label).toBe('No breaches observed')
 expect(incidentHealth({...base,active_count:1,danger_count:1},null).severity).toBe('danger')
 expect(incidentHealth({...base,persistence_warning:'Disk unavailable'},null).label).toBe('History incomplete')
 expect(incidentHealth(base,'Offline').severity).toBe('unknown')
})
it('persists acknowledgement, preserves active severity and opens the test', async () => {
 let saved = incident
 vi.mocked(request).mockImplementation(async (_path, options) => {
  if (options?.method==='PATCH') {saved={...incident,acknowledged_by:'operator',acknowledged_at:stamp};return saved}
  return {...base,incidents:[saved],active_count:1,danger_count:1,unacknowledged_count:saved.acknowledged_at?0:1}
 })
 const health=vi.fn(); const open=vi.fn()
 await act(async()=>root.render(<Panel projectId="p" canManage onHealth={health} onSelectTest={open} />))
 expect(container.textContent).toContain('Auto-stop requested')
 const button=(name:string)=>Array.from(container.querySelectorAll('button')).find(b=>b.textContent===name)!
 await act(async()=>button('Acknowledge').click())
 expect(request).toHaveBeenCalledWith('/monitoring/threshold-incidents/one/ack',expect.objectContaining({method:'PATCH',body:'{"acknowledged":true}'}))
 expect(container.textContent).toContain('Acknowledged by operator')
 expect(health).toHaveBeenLastCalledWith(expect.objectContaining({severity:'danger'}))
 await act(async()=>button('Open test').click());expect(open).toHaveBeenCalledWith('test')
})
it('ignores a delayed response from the previous project', async()=>{
 let resolve!:(value:IncidentResponse)=>void
 vi.mocked(request).mockImplementationOnce(()=>new Promise<IncidentResponse>(r=>{resolve=r})).mockResolvedValue(base)
 const health=vi.fn()
 await act(async()=>root.render(<Panel projectId="old" canManage={false} onHealth={health}/>))
 await act(async()=>root.render(<Panel projectId="new" canManage={false} onHealth={health}/>))
 await act(async()=>resolve({...base,incidents:[incident],active_count:1}))
 expect(container.textContent).not.toContain('Checkout')
 expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Acknowledge')).toBe(false)
})
