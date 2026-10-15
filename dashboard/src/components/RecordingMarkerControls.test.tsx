// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RecordingMarkerControls from './RecordingMarkerControls'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('RecordingMarkerControls', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('waits for a marker request before taking another click', async () => {
    let finish: () => void = () => {}
    const onEndTransaction = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    await act(async () => root.render(
      <RecordingMarkerControls
        openTransactions={['Login']}
        onStartTransaction={vi.fn(async () => true)}
        onEndTransaction={onEndTransaction}
        onAddNote={vi.fn(async () => true)}
        onInvalid={vi.fn()}
      />,
    ))
    const end = container.querySelector('button[aria-label="End transaction Login"]') as HTMLButtonElement
    await act(async () => {
      end.click()
      end.click()
    })
    expect(onEndTransaction).toHaveBeenCalledTimes(1)
    expect(end.disabled).toBe(true)
    expect((Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add note') as HTMLButtonElement).disabled).toBe(true)
    await act(async () => finish())
    expect(end.disabled).toBe(false)
  })
})
