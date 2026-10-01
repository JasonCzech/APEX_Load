// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import TransactionBucketInspector from './TransactionBucketInspector'
import { bucketInspectorParams, transactionWaterfall } from './bucketInspectorModel'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('preserves selection dimensions and exclusive bucket bounds in the inspector', () => {
 const q = { population: 'action' as const, metric: 'response_time', aggregation: 'p95', filters: { hierarchy: 'Store/Checkout', phase: 'action' }, property_name: 'kind' }
 const params = bucketInspectorParams(q, {run:'run',from:'start',to:'end',group:'network_profile',name:'mobile'},1)
 expect(params.get('group_dimension')).toBe('network_profile'); expect(params.get('group_value')).toBe('mobile'); expect(params.get('network_profile')).toBe('mobile'); expect(params.get('hierarchy')).toBe('Store/Checkout'); expect(params.get('phase')).toBe('action');expect(params.get('time_to_exclusive')).toBe('true');expect(params.get('offset')).toBe('25')
 const property = bucketInspectorParams(q, {run:'run',from:'start',to:'end',group:'property',name:'kind=a=b'},0)
 expect(property.get('property_value')).toBe('a=b')
})
it('shows measured phases, missing timings and retained snapshots after bucket selection', async () => {
 const event = {timestamp:'2026-10-01T00:00:00Z',test_id:'run',test_name:'run',action_name:'Checkout',duration_ms:30,success:true,dns_ms:2,ttfb_ms:20}
 expect(transactionWaterfall(event)[1][1]).toBeUndefined()
 request.mockResolvedValue({transactions:[event],total:1})
 const container=document.createElement('div');document.body.append(container);const root=createRoot(container)
 try {
 await act(async()=>root.render(<TransactionBucketInspector query={{metric:'response_time',aggregation:'p95',population:'action'}} selection={{run:'run',from:event.timestamp,to:'2026-10-01T00:00:10Z'}} onClose={()=>{}} />))
 await act(async()=>container.querySelector('details')!.setAttribute('open',''))
 expect(container.textContent).toContain('DNS: 2 ms');expect(container.textContent).toContain('Connect: Unavailable');expect(container.textContent).toContain('Time to first byte (milestone): 20 ms');expect(container.textContent).toContain('No retained request body')
 } finally {act(()=>root.unmount());container.remove()}
})
