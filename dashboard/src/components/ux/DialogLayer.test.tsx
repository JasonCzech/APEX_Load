// @vitest-environment jsdom
import { act, StrictMode, useRef } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import DialogLayer from './DialogLayer'
import { useModalDialog } from '../../hooks/useModalDialog'
vi.unmock('./DialogLayer')

function Example() {
  const ref = useRef<HTMLDivElement>(null)
  useModalDialog({ dialogRef: ref })
  return <DialogLayer><div role="dialog" ref={ref}><button>Close</button></div></DialogLayer>
}

it('escapes clipped ancestors, isolates the page, and restores prior state on unmount', async () => {
  const container = document.createElement('div')
  container.style.transform = 'translateX(0)'
  container.style.overflow = 'hidden'
  document.body.appendChild(container)
  const opener = document.createElement('button')
  document.body.appendChild(opener)
  opener.focus()
  document.body.style.overflow = 'auto'
  const root = createRoot(container)
  await act(async () => { root.render(<StrictMode><Example /></StrictMode>) })
  expect(container.querySelector('[role="dialog"]')).toBeNull()
  expect(document.body.querySelector('.ux-dialog-layer [role="dialog"]')).not.toBeNull()
  expect(container.hasAttribute('inert')).toBe(true)
  expect(document.body.style.overflow).toBe('hidden')
  await act(async () => { root.unmount() })
  expect(document.querySelector('.ux-dialog-layer')).toBeNull()
  expect(container.hasAttribute('inert')).toBe(false)
  expect(document.body.style.overflow).toBe('auto')
  expect(document.activeElement).toBe(opener)
  container.remove(); opener.remove(); document.body.style.overflow = ''
})
