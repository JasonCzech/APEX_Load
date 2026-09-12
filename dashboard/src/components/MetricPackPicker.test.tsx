// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import MetricPackPicker from './MetricPackPicker'
const catalog = vi.hoisted(() => vi.fn())
vi.mock('../hooks/api/useSystemApi',()=>({useSystemApi:()=>({getInfrastructureMonitorPacks:catalog})}))
it('keeps checkbox labels adjacent and preserves custom identifiers while toggling packs',async()=>{
 catalog.mockResolvedValue({packs:[{id:'linux-hosts',name:'Linux hosts',status:'supported',signals:['CPU','Memory']}]})
 function Harness(){const[value,setValue]=useState('custom-pack');return <MetricPackPicker value={value} onChange={setValue}/>}
 const host=document.createElement('div');document.body.appendChild(host);const root=createRoot(host)
 try {
  await act(async()=>root.render(<Harness/>))
  const checkbox=host.querySelector<HTMLInputElement>('input[type="checkbox"]')!
  expect(checkbox.getAttribute('aria-label')).toBe('Linux hosts')
  expect(checkbox.closest('label')?.textContent).toContain('Native support — CPU, Memory')
  await act(async()=>checkbox.click())
  expect(host.querySelector<HTMLInputElement>('input:not([type="checkbox"])')?.value).toBe('custom-pack, linux-hosts')
  await act(async()=>checkbox.click())
  expect(host.querySelector<HTMLInputElement>('input:not([type="checkbox"])')?.value).toBe('custom-pack')
 }finally{act(()=>root.unmount());host.remove()}
})
