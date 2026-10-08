// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { AnalysisSavedViews } from './AnalysisSavedViews'
;(
  globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
const container = document.createElement('div')
document.body.append(container)
const root = createRoot(container)
afterEach(() => {
  act(() => root.render(null))
  localStorage.clear()
  vi.restoreAllMocks()
})
it('restores an exact saved baseline, filters and rules after remount', async () => {
  const values = {
    baseline: 'run-1',
    population: 'action',
    end: '120',
    runs: '["run-1","run-2"]',
    rules: 'v1',
  }
  const onRestore = vi.fn()
  const render = () => (
    <AnalysisSavedViews
      storageKey="analysis-test"
      values={values}
      onRestore={onRestore}
    />
  )
  await act(async () => root.render(render()))
  const input = container.querySelector('input')!
  act(() => {
    Object.getOwnPropertyDescriptor(
      HTMLInputElement.prototype,
      'value',
    )!.set!.call(input, 'Checkout')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  })
  act(() =>
    Array.from(container.querySelectorAll('button'))
      .find((button) => button.textContent === 'Save current view')!
      .click(),
  )
  act(() => root.render(null))
  await act(async () => root.render(render()))
  act(() => {
    const select = container.querySelector('select')!
    select.value = 'Checkout'
    select.dispatchEvent(new Event('change', { bubbles: true }))
  })
  act(() =>
    Array.from(container.querySelectorAll('button'))
      .find((button) => button.textContent === 'Restore view')!
      .click(),
  )
  expect(onRestore).toHaveBeenCalledWith(values)
})
it('ignores corrupt saved data and survives disabled storage', async () => {
  localStorage.setItem(
    'analysis-test',
    '[{"name":"bad","values":{"rule":null}}]',
  )
  await act(async () =>
    root.render(
      <AnalysisSavedViews
        storageKey="analysis-test"
        values={{}}
        onRestore={vi.fn()}
      />,
    ),
  )
  expect(container.querySelectorAll('option')).toHaveLength(1)
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
    throw new Error('disabled')
  })
  await act(async () =>
    root.render(
      <AnalysisSavedViews storageKey="other" values={{}} onRestore={vi.fn()} />,
    ),
  )
  expect(container.textContent).toContain('Saved views (this browser)')
})
