// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import BrowserAuthoredPages from './BrowserAuthoredPages'
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let node: HTMLDivElement, root: Root
beforeEach(() => { node = document.createElement('div'); document.body.appendChild(node); root = createRoot(node) })
afterEach(async () => { await act(async () => root.unmount()); node.remove() })
const generation = 'c'.repeat(32)
const binding = (index = 1) => ({ version: 1, state: 'bound', observed_at: '2026-10-21T10:00:00Z', page_id: 'd'.repeat(64), source_generation: generation, request_id: `${generation}-r${index}`, document_id: `${generation}-d${index}`, lifecycle_id: `${generation}-l${index}`, association: 'cdp_loader_join', authored_step: { version: 2, script_sha256: 'a'.repeat(64), source_structure_sha256: 'b'.repeat(64), step_path: 'actions[0]', step_id: 'e'.repeat(64) } })
const report = () => ({ version: 1, receipt_records: 2, bindings: [binding()], dropped: 0, incomplete: false, unavailable: { navigation_join_unavailable: 1 } })
it('exposes separate source, authored and native document identities without a qualification claim', async () => {
  await act(async () => root.render(<BrowserAuthoredPages value={report()} />))
  expect(node.textContent).toContain('2 retained receipts · 1 distinct document bindings')
  expect(node.textContent).toContain('navigation join unavailable (1)')
  expect(node.textContent).toContain('d'.repeat(64)); expect(node.textContent).toContain('actions[0]')
  expect(node.textContent).toContain('They do not change document measurements or establish compatible runtime state')
  expect(node.querySelector('button[aria-label="Export Authored browser pages data as CSV"]')).not.toBeNull()
  expect(node.querySelector('button[aria-label="Export Authored browser pages image as SVG"]')).toBeNull()
})
it('distinguishes historical absence, explicit empty and malformed reports', async () => {
  await act(async () => root.render(<BrowserAuthoredPages />)); expect(node.textContent).toContain('were not retained')
  await act(async () => root.render(<BrowserAuthoredPages value={{ ...report(), receipt_records: 1, bindings: [] }} />)); expect(node.textContent).toContain('No proved authored page bindings')
  for (const value of [{ ...report(), version: 2 }, { ...report(), bindings: [null] }, { ...report(), bindings: [binding(), binding()] }, { ...report(), bindings: [{ ...binding(), document_id: 'foreign' }] }, { ...report(), unavailable: { bad: -1 } }]) {
    await act(async () => root.render(<BrowserAuthoredPages value={value} />)); expect(node.textContent).toContain('malformed or unsupported')
  }
})
it('bounds the rendered list while retaining export coverage and dropped/conflicting disclosure', async () => {
  await act(async () => root.render(<BrowserAuthoredPages value={{ ...report(), receipt_records: 306, bindings: Array.from({ length: 300 }, (_, i) => binding(i)), incomplete: true, dropped: 5, unavailable: { conflicting_binding: 1 } }} />))
  expect(node.querySelectorAll('details')).toHaveLength(256)
  expect(node.textContent).toContain('Showing 256 of 300 retained bindings')
  expect(node.textContent).toContain('5 dropped receipts')
  expect(node.textContent).toContain('Page binding evidence is incomplete')
})

it('accepts proved native commit receipts and rejects mixed or unsafe proof versions', async () => {
  const native = { ...binding(), version: 2, association: 'native_navigation_commit', navigation_sequence: 2 }
  await act(async () => root.render(<BrowserAuthoredPages value={{ ...report(), bindings: [native] }} />))
  expect(node.textContent).toContain('Native navigation sequence')
  expect(node.textContent).toContain('native_navigation_commit')
  expect(node.textContent).not.toContain('malformed or unsupported')
  for (const item of [{ ...native, version: 1 }, { ...native, navigation_sequence: 0 }, { ...native, navigation_sequence: undefined }, { ...native, navigation_sequence: 1.5 }, { ...native, navigation_sequence: Number.MAX_SAFE_INTEGER + 1 }, { ...native, association: 'cdp_loader_join' }, { ...binding(), navigation_sequence: 2 }]) {
    await act(async () => root.render(<BrowserAuthoredPages value={{ ...report(), bindings: [item] }} />))
    expect(node.textContent).toContain('malformed or unsupported')
  }
})
