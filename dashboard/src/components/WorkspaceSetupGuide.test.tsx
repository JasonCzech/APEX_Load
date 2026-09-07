// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import WorkspaceSetupGuide from './WorkspaceSetupGuide'

it('retains a failed creation and explains restricted creation', async () => {
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 const create = vi.fn().mockRejectedValue(new Error('Server unavailable'))
 const step = {label:'Project',description:'A shared project',choices:[],canCreate:true,create,select:vi.fn()}
 act(() => root.render(<WorkspaceSetupGuide steps={[step]} />))
 const input = host.querySelector('input')!
 act(() => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!.call(input,'Checkout'); input.dispatchEvent(new Event('input',{bubbles:true})) })
 await act(async () => { Array.from(host.querySelectorAll('button')).find(button => button.textContent === 'Create project and continue')!.click() })
 expect(create).toHaveBeenCalledWith('Checkout')
 expect(host.querySelector('[role=alert]')?.textContent).toBe('Server unavailable')
 expect(input.value).toBe('Checkout')
 act(() => root.render(<WorkspaceSetupGuide steps={[{...step,canCreate:false}]} />))
 expect(host.querySelector('input')).toBeNull()
 expect(host.textContent).toContain('ask an administrator')
 act(() => root.unmount());host.remove()
})
