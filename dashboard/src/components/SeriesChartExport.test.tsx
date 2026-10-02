// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { downloadBlob } from '../utils/fileUtils'
import SeriesChartExport from './SeriesChartExport'
import { buildChartCSV, buildChartSVG } from './exportableChartUtils'

vi.mock('../utils/fileUtils', () => ({ downloadBlob: vi.fn().mockResolvedValue('saved') }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function readBlob(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.onerror = reject; reader.readAsText(blob) })
}

describe('selected chart evidence exports', () => {
 it('retains zero, missing cells, source identities and filters while excluding hidden series', async () => {
  vi.clearAllMocks()
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  try {
   await act(async () => root.render(<SeriesChartExport id="selected" title="Selected latency" rows={[{ time: '2026-10-01T00:00:00Z', shown: 0, hidden: 999 }, { time: '2026-10-01T00:00:01Z', shown: undefined, hidden: 888 }]} axes={[{ key: 'utc', label: 'UTC', value: row => row.time }]} series={[{ key: 'shown', label: 'API', unit: 'ms' }]} context={{ run_id: '=external', filter: { phase: 'active' }, coverage: '1/2' }}><svg><path d="M0 0" /></svg></SeriesChartExport>))
   await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label$="data as CSV"]')!.click())
   const csv = await readBlob(vi.mocked(downloadBlob).mock.calls[0][0])
   expect(csv).toContain('API (ms) [shown]')
   expect(csv).toContain('"0"')
   expect(csv).toContain('00:00:01Z",,')
   expect(csv).toContain("'=external")
   expect(csv).toContain('active')
   expect(csv).toContain('1/2')
   expect(csv).not.toContain('hidden')
   expect(csv).not.toContain('999')
  } finally { await act(async () => root.unmount()); container.remove() }
 })
 it('never serializes nonfinite values as measured evidence', () => {
  const csv = buildChartCSV([{ value: NaN }, { value: Infinity }, { value: 0 }], [{ key: 'v', label: 'Value', value: row => row.value }])
  expect(csv).not.toMatch(/NaN|Infinity/)
  expect(csv).toContain('"0"')
 })
 it('removes active and external SVG content, resolves paint and preserves local gradients', () => {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
  svg.innerHTML = '<defs><linearGradient id="local" /></defs><path fill="url(#local)" stroke="red" onclick="bad()" /><script>bad()</script><foreignObject /><image href="https://outside.example/a" /><use href="https://outside.example/icon" /><rect style="fill: url(https://outside.example/paint)" />'
  document.body.appendChild(svg)
  try {
   const output = buildChartSVG(svg, 'Measured chart', 'Selected scope')
   expect(output).not.toMatch(/script|foreignObject|outside\.example|onclick/)
   expect(output).toContain('url(#local)')
   expect(output).toContain('red')
   expect(output).toContain('Selected scope')
  } finally { svg.remove() }
 })
})
