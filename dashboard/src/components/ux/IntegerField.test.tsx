// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import IntegerField from './IntegerField'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('IntegerField', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })

  it('reports invalid entries instead of silently clamping them', async () => {
    const commits = vi.fn()
    function Harness() {
      const [value, setValue] = useState(5)
      return <IntegerField label="Peak VUsers" min={1} max={100} value={value} onCommit={next => { commits(next); setValue(next) }} />
    }
    await act(async () => root.render(<Harness />))
    const input = container.querySelector('input')!
    const type = (text: string) => act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set?.call(input, text)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    for (const [text, message] of [['0', 'at least 1'], ['-3', 'at least 1'], ['2.5', 'whole number'], ['', 'Enter peak vusers'], ['101', 'at most 100']] as const) {
      await type(text)
      // The typed text stays; nothing is committed; the reason is shown.
      expect(input.value).toBe(text)
      expect(input.getAttribute('aria-invalid')).toBe('true')
      expect(container.querySelector('[role="alert"]')?.textContent).toContain(message)
    }
    expect(commits).not.toHaveBeenCalled()
    await type('40')
    expect(commits).toHaveBeenLastCalledWith(40)
    expect(container.querySelector('[role="alert"]')).toBeNull()
  })
})
