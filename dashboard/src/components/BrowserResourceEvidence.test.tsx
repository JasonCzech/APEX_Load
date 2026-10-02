// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect } from 'vitest'
import BrowserResourceEvidence from './BrowserResourceEvidence'
describe('browser resource evidence', () => {
 it('keeps measured zero distinct from missing and filters retained failures', async () => {
 const node=document.createElement('div'); const root=createRoot(node)
 try {
 await act(async()=>root.render(<BrowserResourceEvidence resources={[{timestamp:'now',url:'/zero',action_name:'Navigate',duration_ms:0,bytes_received:0,status_code:200},{timestamp:'later',url:'/failed',page_url:'/page',action_name:'Click',failure_kind:'timeout'}]} />))
 expect(node.textContent).toContain('0.0 ms');expect(node.textContent).toContain('0 B');expect(node.textContent).toContain('Unavailable');expect(node.textContent).toContain('/page')
 const checkbox=node.querySelector('input')!;await act(async()=>checkbox.click())
 expect(node.textContent).toContain('1 of 2 retained');expect(node.textContent).not.toContain('/zero');expect(node.textContent).toContain('timeout')
 } finally {await act(async()=>root.unmount())}
 })
 it('filters classified transport failures without inventing failure from a zero status', async () => {
 const node=document.createElement('div'); const root=createRoot(node)
 try {
 await act(async()=>root.render(<BrowserResourceEvidence resources={[{timestamp:'now',url:'/aborted',failure_kind:'transport',status_code:0},{timestamp:'now',url:'/unknown',status_code:0},{timestamp:'now',url:'/http-error',status_code:503,failure_kind:'http'}]} />))
 await act(async()=>node.querySelector('input')!.click())
 expect(node.querySelectorAll('tbody tr')).toHaveLength(2)
 expect(node.textContent).toContain('/aborted');expect(node.textContent).toContain('/http-error');expect(node.textContent).not.toContain('/unknown')
 expect(node.textContent).toContain('503 · http')
 } finally {await act(async()=>root.unmount())}
 })
 it('caps visible evidence and does not imply a complete census', async()=>{
 const node=document.createElement('div');const root=createRoot(node)
 try{await act(async()=>root.render(<BrowserResourceEvidence resources={Array.from({length:60},(_,i)=>({timestamp:'now',url:`/${i}`}))}/>));expect(node.querySelectorAll('tbody tr')).toHaveLength(50);expect(node.textContent).toContain('individual request start times');}finally{await act(async()=>root.unmount())}
 })
})
