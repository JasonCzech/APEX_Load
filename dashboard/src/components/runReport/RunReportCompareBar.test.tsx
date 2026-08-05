// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import RunReportCompareBar from './RunReportCompareBar'

describe('RunReportCompareBar', () => {
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

  it('confirms a successful pin and clears the confirmation for another comparison', async () => {
    const onPinBaseline = vi.fn().mockResolvedValue(true)
    const render = (compareId: string) => (
      <RunReportCompareBar
        compareId={compareId}
        deltas={{}}
        onCompareChange={vi.fn()}
        onPinBaseline={onPinBaseline}
      />
    )
    await act(async () => root.render(render('baseline-1')))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Pin baseline')?.click()
      await Promise.resolve()
    })
    expect(container.querySelector('[role="status"]')?.textContent).toBe('Pinned')

    await act(async () => root.render(render('baseline-2')))
    expect(container.querySelector('[role="status"]')).toBeNull()
  })

  it('does not show stale pin success after the comparison changes in flight', async () => {
    let resolvePin!: (value: boolean) => void
    const onPinBaseline = vi.fn(() => new Promise<boolean>(resolve => { resolvePin = resolve }))
    const render = (compareId: string) => (
      <RunReportCompareBar
        compareId={compareId}
        deltas={{}}
        onCompareChange={vi.fn()}
        onPinBaseline={onPinBaseline}
      />
    )
    await act(async () => root.render(render('baseline-1')))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Pin baseline')?.click()
    })

    await act(async () => root.render(render('baseline-2')))
    await act(async () => {
      resolvePin(true)
      await Promise.resolve()
    })

    expect(container.querySelector('[role="status"]')).toBeNull()
    expect(Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Pin baseline')?.disabled).toBe(false)
  })
})
