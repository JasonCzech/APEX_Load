// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { handleTabKey } from './tabKeyboard'
it('wraps, skips disabled and nested tabs, and supports Home/End', async () => {
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container); const select = vi.fn()
  await act(async () => { root.render(<div role="tablist" tabIndex={-1} onKeyDown={handleTabKey}><button role="tab" onClick={() => select('a')}>A</button><button role="tab" disabled>B</button><button role="tab" onClick={() => select('c')}>C</button><div role="tablist"><button role="tab">Nested</button></div></div>) })
  const tabs = container.querySelectorAll<HTMLButtonElement>('button')
  tabs[0].focus()
  for (const [key, selected] of [['ArrowRight', 'c'], ['ArrowRight', 'a'], ['End', 'c'], ['Home', 'a']]) {
    await act(async () => { document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true })) })
    expect(select).toHaveBeenLastCalledWith(selected)
  }
  await act(async () => root.unmount()); container.remove()
})
