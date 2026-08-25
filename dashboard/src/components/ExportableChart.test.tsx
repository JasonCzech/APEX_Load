// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { downloadBlob } from '../utils/fileUtils'
import ExportableChart from './ExportableChart'
import { buildChartCSV, buildChartSVG, type ChartExportColumn } from './exportableChartUtils'

vi.mock('../utils/fileUtils', () => ({ downloadBlob: vi.fn().mockResolvedValue('saved') }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

type Row = { time: string; value: number; note: string }
const columns: readonly ChartExportColumn<Row>[] = [
  { key: 'time', label: 'Time', value: row => row.time },
  { key: 'value', label: 'Value', value: row => row.value },
  { key: 'note', label: 'Note', value: row => row.note },
]

describe('ExportableChart', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('builds bounded CSV with quoted values and a stable header', () => {
    const csv = buildChartCSV([{ time: '12:00', value: 5.5, note: 'slow, "tail"' }], columns)
    expect(csv).toBe('\uFEFF"Time","Value","Note"\r\n"12:00","5.5","slow, ""tail"""\r\n')
  })

  it('neutralizes spreadsheet formulas and enforces row and byte limits', () => {
    const csv = buildChartCSV([{ time: '=NOW()', value: 1, note: '  +cmd' }], columns)
    expect(csv).toContain("'=NOW()")
    expect(csv).toContain("'  +cmd")
    expect(() => buildChartCSV([{ time: 'a', value: 1, note: '' }], columns, { maxRows: 0 })).toThrow('row limit')
    expect(() => buildChartCSV([{ time: 'long', value: 1, note: '' }], columns, { maxBytes: 8 })).toThrow('byte limit')
  })

  it('adds accessible title and description to exported SVG', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.innerHTML = '<path d="M0 0"/>'
    const serialized = buildChartSVG(svg, 'Latency', 'P95 latency over time')
    expect(serialized).toContain('<title>Latency</title>')
    expect(serialized).toContain('<desc>P95 latency over time</desc>')
    expect(serialized).toContain('aria-label="P95 latency over time"')
		expect(() => buildChartSVG(svg, 'Latency', 'P95 latency over time', 8)).toThrow('byte limit')
  })

  it('downloads definition-derived CSV and SVG filenames', async () => {
    const rows = [{ time: '12:00', value: 5.5, note: 'tail' }]
    await act(async () => {
      root.render(
        <ExportableChart definitionId="p95" title="Response Time (P95)" description="P95 latency over time" rows={rows} columns={columns}>
          <svg width="640" height="320"><path d="M0 0" /></svg>
        </ExportableChart>,
      )
    })
    const csv = container.querySelector<HTMLButtonElement>('button[aria-label="Export Response Time (P95) data as CSV"]')!
    const svg = container.querySelector<HTMLButtonElement>('button[aria-label="Export Response Time (P95) image as SVG"]')!
    await act(async () => csv.click())
    await act(async () => svg.click())

    expect(downloadBlob).toHaveBeenNthCalledWith(1, expect.any(Blob), 'response-time-p95-p95.csv')
    expect(downloadBlob).toHaveBeenNthCalledWith(2, expect.any(Blob), 'response-time-p95-p95.svg')
    expect(container.querySelector('figure')?.getAttribute('aria-label')).toBe('P95 latency over time')
  })
})
