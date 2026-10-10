// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import BrowserResourceInspector from './BrowserResourceInspector'
import { RESOURCE_GENERATION, resourcePageFixture } from './browserResourcePage.testFixtures'
import type { BrowserResourcePageLoader } from './useBrowserResourcePage'
const props = { testID: 'run-one', projectID: 'project-one', sourceIdentity: 'source-one' }
const button = (node: HTMLElement, text: string) => Array.from(node.querySelectorAll('button')).find(element => element.textContent === text)!

describe('retained browser resource inspector', () => {
  it('renders zero, false, nanoseconds, overlapping native phases and independent availability', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const loader: BrowserResourcePageLoader = async () => resourcePageFixture()
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      expect(node.textContent).toContain('Native browser request details appear only when explicitly retained');
      expect(node.textContent).toContain('80 retained entry observations'); expect(node.textContent).toContain('Manifest and artifact integrity verified')
      expect(node.textContent).toContain('2026-10-05T12:00:00.123456789Z'); expect(node.textContent).toContain('0 ms / 0 B')
      expect(node.textContent).toContain('false (observed)'); expect(node.textContent).toContain('0 ms · clock unverified'); expect(node.textContent).toContain('may overlap')
      expect(node.querySelector('.measured-point')).not.toBeNull()
      expect(node.querySelector('.browser-resource-phase-bar')?.getAttribute('style')).toContain('width: 0%')
      const table = node.querySelector('[aria-label="Retained archive resources table"]')!; expect(table.getAttribute('tabindex')).toBe('0')
      expect(node.querySelectorAll('tbody tr')).toHaveLength(1)
    } finally { await act(async () => root.unmount()) }
  })
  it('paginates with the exact cursor then retries the first page without carrying transport state into filters', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const loader = vi.fn(async () => resourcePageFixture()) as BrowserResourcePageLoader
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      await act(async () => button(node, 'Next resource page').click())
      expect(vi.mocked(loader).mock.calls[1][2].cursor).toBe(`payload.${'A'.repeat(43)}`); expect(node.textContent).toContain('Page 2:')
      await act(async () => button(node, 'First page / retry').click()); expect(vi.mocked(loader).mock.calls[2][2]).not.toHaveProperty('cursor'); expect(node.textContent).toContain('Page 1:')
      await act(async () => button(node, 'First page / retry').click()); expect(loader).toHaveBeenCalledTimes(4)
      await act(async () => node.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
      expect(vi.mocked(loader).mock.calls[4][2]).toEqual({ limit: 50, sort_by: 'capture_order', sort_dir: 'asc' })
    } finally { await act(async () => root.unmount()) }
  })
  it('names selectable controls independently of option text and applies draft page size only on submit', async () => {
    const node = document.createElement('div'); const root = createRoot(node); const loader = vi.fn(async () => resourcePageFixture()) as BrowserResourcePageLoader
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      for (const name of ['Failure classification', 'Sort retained observations', 'Sort direction', 'Resources per page', 'Native request state']) {
        expect(node.querySelector(`select[aria-label="${name}"]`)).not.toBeNull()
      }
      const size = node.querySelector<HTMLSelectElement>('select[aria-label="Resources per page"]')!
      expect(size.value).toBe('50')
      await act(async () => { size.value = '25'; size.dispatchEvent(new Event('change', { bubbles: true })) })
      expect(loader).toHaveBeenCalledOnce()
      await act(async () => node.querySelector('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
      expect(vi.mocked(loader).mock.calls[1][2]).toEqual({ limit: 25, sort_by: 'capture_order', sort_dir: 'asc' })
    } finally { await act(async () => root.unmount()) }
  })
  it('shows explicit unavailable, filter-zero and legacy integrity without a healthy zero verdict', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    const loader: BrowserResourcePageLoader = async () => { const page = resourcePageFixture(); page.items = []; delete page.next_cursor; page.source_integrity = 'legacy_scan_fingerprint'; page.population = { availability: 'unavailable', transaction_records: 2, retained_entry_observations: null, matched_retained_observations: null }; return page }
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      expect(node.textContent).toContain('resource projection is unavailable'); expect(node.textContent).toContain('producer integrity is unverified'); expect(node.textContent).not.toContain('0 retained entry observations'); expect(node.textContent).not.toContain('0 retained observations')
      const zero: BrowserResourcePageLoader = async () => { const page = resourcePageFixture(); page.items = []; delete page.next_cursor; page.population.matched_retained_observations = 0; return page }
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={zero} />)); expect(node.textContent).toContain('0 match these filters out of 80'); expect(node.textContent).toContain('No retained observations match')
    } finally { await act(async () => root.unmount()) }
  })
  it('does not expose a rejected response or a previous run after switching archive owners', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    const loader: BrowserResourcePageLoader = async id => ({ ...resourcePageFixture(), test_id: id, items: [{ ...resourcePageFixture().items[0], url: id }] })
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />)); expect(node.textContent).toContain('run-one')
      await act(async () => root.render(<BrowserResourceInspector {...props} testID="run-two" loadPage={loader} />)); expect(node.textContent).not.toContain('run-one'); expect(node.textContent).toContain('run-two')
      const malformed: BrowserResourcePageLoader = async () => ({ ...resourcePageFixture(), test_id: 'secret-foreign-run' })
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={malformed} />)); expect(node.textContent).toContain('Unable to load'); expect(node.textContent).not.toContain('secret-foreign-run'); expect(button(node, 'Next resource page').disabled).toBe(true)
    } finally { await act(async () => root.unmount()) }
  })
  it('drills from a request into its document and draws that document waterfall', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    const documentID = `${RESOURCE_GENERATION}-d1`
    const associated = () => {
      const page = resourcePageFixture()
      page.items[0].browser_evidence = { ...page.items[0].browser_evidence!, version: 3, document_id: documentID, document_association: 'cdp_loader_join', document_offset_ms: 12, initiator_type: 'parser', initiator_origin: 'https://example.test' }
      return page
    }
    const loader = vi.fn(async () => associated()) as BrowserResourcePageLoader
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      expect(node.textContent).toContain('Chromium loader of the issuing document')
      expect(node.textContent).toContain('parser from https://example.test')
      expect(node.querySelector('[aria-label="Document request waterfall"]')).toBeNull()
      await act(async () => button(node, "Show this document's resources").click())
      const calls = vi.mocked(loader).mock.calls
      expect(calls[calls.length - 1][2]).toMatchObject({ document_id: documentID })
      expect(calls[calls.length - 1][2]).not.toHaveProperty('cursor')
      const waterfall = node.querySelector('[aria-label="Document request waterfall"]')!
      expect(waterfall.textContent).toContain(documentID)
      expect(waterfall.querySelectorAll('.browser-document-waterfall-phase').length).toBe(3)
      expect(waterfall.querySelector('[aria-label*="starts 12.0 ms"]')).not.toBeNull()
      // Version 3 evidence predates protocol capture: the column says Unknown.
      expect(waterfall.querySelector('.browser-document-waterfall-protocol')?.textContent).toBe('Unknown')
      expect(waterfall.querySelector('.browser-document-waterfall-track')?.getAttribute('data-protocol')).toBe('unknown')
    } finally { await act(async () => root.unmount()) }
  })
  it('shows the negotiated protocol of each waterfall request', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    const documentID = `${RESOURCE_GENERATION}-d1`
    const loader = vi.fn(async () => {
      const page = resourcePageFixture()
      page.items[0].browser_evidence = { ...page.items[0].browser_evidence!, version: 4, identity_unavailable_reason: 'cdp_unobserved', protocol: 'h3', document_id: documentID, document_association: 'cdp_loader_join', document_offset_ms: 12, initiator_type: 'parser', initiator_origin: 'https://example.test' } as never
      return page
    }) as BrowserResourcePageLoader
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      await act(async () => button(node, "Show this document's resources").click())
      const waterfall = node.querySelector('[aria-label="Document request waterfall"]')!
      expect(waterfall.querySelector('.browser-document-waterfall-protocol')?.textContent).toBe('HTTP/3 (QUIC)')
      expect(waterfall.querySelector('.browser-document-waterfall-track')?.getAttribute('data-protocol')).toBe('h3')
      expect(waterfall.querySelector('[aria-label*="HTTP/3 (QUIC), starts 12.0 ms"]')).not.toBeNull()
    } finally { await act(async () => root.unmount()) }
  })
  it('clears request-scoped filters when drilling into a document', async () => {
    const node = document.createElement('div'); const root = createRoot(node)
    const documentID = `${RESOURCE_GENERATION}-d1`
    const associated = () => {
      const page = resourcePageFixture()
      page.items[0].browser_evidence = { ...page.items[0].browser_evidence!, version: 3, document_id: documentID, document_association: 'cdp_loader_join', document_offset_ms: 12 }
      return page
    }
    const loader = vi.fn(async () => associated()) as BrowserResourcePageLoader
    const setInput = (label: string, value: string) => {
      const input = Array.from(node.querySelectorAll('label')).find(element => element.textContent?.startsWith(label))!.querySelector('input')!
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.dispatchEvent(new Event('change', { bubbles: true }))
    }
    try {
      await act(async () => root.render(<BrowserResourceInspector {...props} loadPage={loader} />))
      await act(async () => { setInput('Native request identity', `${RESOURCE_GENERATION}-r1`); setInput('Redirect chain identity', `${RESOURCE_GENERATION}-c1`) })
      await act(async () => { node.querySelector('form[aria-label="Archive resource filters"]')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })) })
      let calls = vi.mocked(loader).mock.calls
      expect(calls[calls.length - 1][2]).toMatchObject({ request_id: `${RESOURCE_GENERATION}-r1`, redirect_chain_id: `${RESOURCE_GENERATION}-c1` })
      await act(async () => button(node, "Show this document's resources").click())
      calls = vi.mocked(loader).mock.calls
      expect(calls[calls.length - 1][2]).toMatchObject({ document_id: documentID })
      expect(calls[calls.length - 1][2]).not.toHaveProperty('request_id')
      expect(calls[calls.length - 1][2]).not.toHaveProperty('redirect_chain_id')
    } finally { await act(async () => root.unmount()) }
  })
})
