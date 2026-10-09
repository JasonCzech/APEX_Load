// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import RunAnnotations from './RunAnnotations'
import { runAnnotationTimestamp } from './runAnnotationModel'
const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
it('lets viewers read retained notes while hiding editor mutation controls', async () => {
 request.mockResolvedValue({revision:2,annotations:[{id:'a',timestamp:'2026-10-01T00:00:00Z',label:'Deployment',created_by:'editor'},{id:'b',timestamp:'2026-10-01T00:00:00Z',label:'Deleted',created_by:'editor',deleted_at:'2026-10-01T00:01:00Z'}]})
 const container=document.createElement('div');document.body.append(container);const root=createRoot(container)
 try {await act(async()=>root.render(<RunAnnotations testId="run" canEdit={false} />));expect(container.textContent).toContain('Deployment');expect(container.textContent).not.toContain('Deleted');expect(container.querySelector('form')).toBeNull();expect(container.textContent).not.toContain('Delete annotation')}
 finally {act(()=>root.unmount());container.remove()}
})

it('preserves execution-commit timestamp precision for a zero-offset annotation', () => {
 const start = '2026-10-01T00:00:00.123456Z'
 expect(runAnnotationTimestamp(start, 0)).toBe(start)
 expect(runAnnotationTimestamp(start, 0.001)).toBe('2026-10-01T00:00:00.124Z')
 expect(() => runAnnotationTimestamp(start, 0.0001)).toThrow()
 expect(() => runAnnotationTimestamp(start, NaN)).toThrow()
})

it('shows run time, reports notes for the timeline, and confirms before deleting', async () => {
 const confirmAction = await import('./ux/confirmAction')
 const confirm = vi.spyOn(confirmAction, 'confirmAction').mockResolvedValue(false)
 request.mockReset()
 request.mockResolvedValue({revision:2,annotations:[{id:'a',timestamp:'2026-10-01T00:00:15Z',label:'Deployment',created_by:'editor'}]})
 const onChange = vi.fn()
 const container=document.createElement('div');document.body.append(container);const root=createRoot(container)
 try {
  await act(async()=>root.render(<RunAnnotations testId="run" canEdit startedAt="2026-10-01T00:00:00Z" onAnnotationsChange={onChange} />))
  expect(container.textContent).toContain('at 0:15 (')
  expect(onChange).toHaveBeenLastCalledWith([{ id: 'a', label: 'Deployment', timestamp: '2026-10-01T00:00:15Z' }])
  const remove = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Delete annotation Deployment')!
  await act(async()=>remove.click())
  expect(confirm).toHaveBeenCalledWith(expect.stringContaining('Deployment'), expect.objectContaining({ destructive: true }))
  // Declined: nothing is deleted.
  expect(request).toHaveBeenCalledTimes(1)
 } finally {act(()=>root.unmount());container.remove();confirm.mockRestore()}
})
