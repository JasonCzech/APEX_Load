// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import MonitoringEvidenceCard from './MonitoringEvidenceCard'
import { request } from '../hooks/apiClient'
vi.mock('../hooks/apiClient', () => ({ request: vi.fn() }))
it('queries the selected time range and distinguishes no samples from unavailable data', async () => {
 const host=document.createElement('div');document.body.appendChild(host);const root=createRoot(host)
 vi.mocked(request).mockResolvedValue({enabled:true,configured:true,generated_at:new Date().toISOString(),graphs:[]})
 try {
  await act(async()=>root.render(<MonitoringEvidenceCard canManage/>))
  expect(host.textContent).toContain('Configured; no samples returned')
  const range=host.querySelector('select')!
  await act(async()=>{range.value='1440';range.dispatchEvent(new Event('change',{bubbles:true}))})
  const path=vi.mocked(request).mock.calls[1][0]
  const params=new URLSearchParams(path.split('?')[1])
  expect(Date.parse(params.get('end')!)-Date.parse(params.get('start')!)).toBe(24*3600_000)
  vi.mocked(request).mockRejectedValueOnce(new Error('Datasource unreachable'))
  await act(async()=>host.querySelector('button')!.click())
  expect(host.querySelector('[role="alert"]')?.textContent).toContain('Datasource unreachable')
  expect(host.textContent).toContain('Previously loaded samples may be stale')
 }finally{act(()=>root.unmount());host.remove();vi.resetAllMocks()}
})
