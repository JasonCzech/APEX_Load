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

  it('distinguishes loading, empty and failed discovery with an actionable Results link', async () => {
    const render = (optionsLoading = false, error = '') => root.render(<RunReportCompareBar compareId="" deltas={{}} optionsLoading={optionsLoading} error={error} onCompareChange={vi.fn()} onPinBaseline={vi.fn()} />)
    await act(async () => render(true))
    expect(container.textContent).toContain('Finding archived baseline runs')
    expect(container.textContent).not.toContain('No other archived runs')
    expect(container.querySelector('select')?.disabled).toBe(true)
    await act(async () => render())
    expect(container.textContent).toContain('No other archived runs')
    expect(container.querySelector('a')?.getAttribute('href')).toContain('#/results')
    expect(container.querySelector('.run-report-compare-deltas')).toBeNull()
    await act(async () => render(false, 'Discovery unavailable'))
    expect(container.textContent).toContain('Discovery unavailable')
    expect(container.textContent).not.toContain('No other archived runs')
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

  it('analyzes the root cause against the selected baseline and says what happened', async () => {
    const onAnalyzeRootCause = vi.fn().mockResolvedValueOnce('Analysis revision 3 is queued.').mockRejectedValueOnce(new Error('The analysis could not be started; the Root cause panel says why.'))
    const render = (compareId: string) => root.render(<RunReportCompareBar compareId={compareId} deltas={{}} onCompareChange={vi.fn()} onPinBaseline={vi.fn()} onAnalyzeRootCause={onAnalyzeRootCause} />)
    await act(async () => render(''))
    const analyze = () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Analyze root cause against this baseline') as HTMLButtonElement | undefined
    expect(analyze()).toBeUndefined()
    await act(async () => render('run-0'))
    await act(async () => analyze()!.click())
    expect(onAnalyzeRootCause).toHaveBeenCalledTimes(1)
    expect(container.querySelector('[role="status"]')?.textContent).toBe('Analysis revision 3 is queued.')
    await act(async () => analyze()!.click())
    expect(container.querySelector('[role="alert"]')?.textContent).toBe('The analysis could not be started; the Root cause panel says why.')
  })
})
