// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import BrowserHAREntryView from './BrowserHAREntryView'

const read = vi.hoisted(() => vi.fn())
vi.mock('../hooks/api/useBrowserHARApi', () => ({ readArchiveBrowserHAREntry: read }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); read.mockReset() })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })
const click = async (label: string) => { await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === label)!.dispatchEvent(new MouseEvent('click', { bubbles: true })) }) }

it('shows a synthetic HAR entry on request (BI-4 drill-down)', async () => {
  read.mockResolvedValue({ request_id: 'r-1', fidelity: 'synthetic', entry: { request: { url: 'https://shop.example/' } } })
  await act(async () => root.render(<BrowserHAREntryView testID="run-1" requestID="r-1" />))
  expect(read).not.toHaveBeenCalled()
  await click("Show this request's HAR entry")
  expect(read).toHaveBeenCalledWith('run-1', 'r-1')
  expect(container.querySelector('pre[aria-label="HAR entry"]')?.textContent).toContain('https://shop.example/')
})

it('names the native HAR that holds a request and offers to download it', async () => {
  read.mockResolvedValue({ request_id: 'r-2', fidelity: 'native', artifact: 'browser_har_x.har' })
  const onDownload = vi.fn()
  await act(async () => root.render(<BrowserHAREntryView testID="run-1" requestID="r-2" onDownload={onDownload} />))
  await click("Show this request's HAR entry")
  expect(container.textContent).toContain('browser_har_x.har')
  await click('Download that HAR')
  expect(onDownload).toHaveBeenCalledWith('browser_har_x.har')
})

it('says why the synthetic HAR leaves a request out, and when native HARs may hold it', async () => {
  read.mockResolvedValue({ request_id: 'r-4', fidelity: 'omitted', omitted_reason: 'invalid_url', native_har_coverage_unknown: 2 })
  await act(async () => root.render(<BrowserHAREntryView testID="run-1" requestID="r-4" />))
  await click("Show this request's HAR entry")
  expect(container.textContent).toContain('The synthetic HAR leaves this request out (invalid url)')
  expect(container.textContent).toContain('2 native HARs that name no browser context')
  expect(container.querySelector('pre[aria-label="HAR entry"]')).toBeNull()
})

it('says a 503 is a busy controller and offers a retry', async () => {
  read.mockRejectedValueOnce(new ApiError('internal server error', 503))
  await act(async () => root.render(<BrowserHAREntryView testID="run-1" requestID="r-5" />))
  await click("Show this request's HAR entry")
  expect(container.textContent).toContain('The controller is reading another browser archive, or ran out of time reading this one.')
  expect(Array.from(container.querySelectorAll('button')).some(button => button.textContent === 'Retry')).toBe(true)
})

it('keeps the server text behind details and offers a retry on failure', async () => {
  read.mockRejectedValueOnce(new Error('no retained browser request has this request_id')).mockResolvedValueOnce({ request_id: 'r-3', fidelity: 'synthetic', entry: {} })
  await act(async () => root.render(<BrowserHAREntryView testID="run-1" requestID="r-3" />))
  await click("Show this request's HAR entry")
  expect(container.querySelector('[role="alert"]')?.textContent).toContain('The HAR entry could not be read')
  await click('Retry')
  expect(read).toHaveBeenCalledTimes(2)
  expect(container.querySelector('pre[aria-label="HAR entry"]')).not.toBeNull()
})
