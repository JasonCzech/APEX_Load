// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { CODE_LINE_WRAP_STORAGE_KEY, useCodeLineWrapPreference } from './codeLineWrapPreference'

function PreferenceButton({ label }: { label: string }) {
  const [enabled, setEnabled] = useCodeLineWrapPreference()
  return <button type="button" onClick={() => setEnabled(!enabled)}>{label}: {enabled ? 'On' : 'Off'}</button>
}

describe('code line-wrap preference', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    window.localStorage.removeItem(CODE_LINE_WRAP_STORAGE_KEY)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
    window.localStorage.removeItem(CODE_LINE_WRAP_STORAGE_KEY)
  })

  it('defaults on, persists changes, and synchronizes open source viewers', async () => {
    await act(async () => {
      root.render(<><PreferenceButton label="Source" /><PreferenceButton label="Mapping" /></>)
    })

    const buttons = Array.from(container.querySelectorAll('button'))
    expect(buttons.map(button => button.textContent)).toEqual(['Source: On', 'Mapping: On'])

    await act(async () => {
      buttons[0].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(Array.from(container.querySelectorAll('button')).map(button => button.textContent)).toEqual(['Source: Off', 'Mapping: Off'])
    expect(window.localStorage.getItem(CODE_LINE_WRAP_STORAGE_KEY)).toBe('off')

    await act(async () => {
      root.unmount()
      root = createRoot(container)
      root.render(<PreferenceButton label="Reopened" />)
    })
    expect(container.textContent).toBe('Reopened: Off')
  })
})
