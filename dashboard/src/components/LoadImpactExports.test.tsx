// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { MetricsHistoryPoint } from '../types'
import LoadImpactHeatmap from './LoadImpactHeatmap'
import { downloadBlob } from '../utils/fileUtils'

vi.mock('../utils/fileUtils', () => ({ downloadBlob: vi.fn().mockResolvedValue('saved') }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('exports all selected matrix columns without clipping and labels interval coverage', async () => {
 const container = document.createElement('div'); document.body.appendChild(container)
 const root = createRoot(container)
 const points = Array.from({ length: 10 }, (_, i) => ({ time: `${i}`, vusers: i + 1, avgMs: 10 + i, tps: i, errorPct: 0, p50: 1, p90: 2, p99: 3 } as MetricsHistoryPoint))
 try {
  await act(async () => root.render(<LoadImpactHeatmap runId="retained-run" points={points} />))
  const svg = container.querySelector<SVGSVGElement>('figure[data-chart-definition="load-impact-matrix"] svg')!
  const width = Number(svg.getAttribute('width'))
  expect(width).toBe(1200)
  for (const rect of svg.querySelectorAll('rect')) expect(Number(rect.getAttribute('x')) + Number(rect.getAttribute('width'))).toBeLessThanOrEqual(width)
  await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="Export Load Impact selected matrix data as CSV"]')!.click())
  const blob = vi.mocked(downloadBlob).mock.calls[0][0]
  const csv = await new Promise<string>(resolve => { const reader = new FileReader(); reader.onload = () => resolve(String(reader.result)); reader.readAsText(blob) })
  expect(csv).toContain('retained-run')
  expect(csv).toContain('Retained telemetry intervals')
  expect(csv).toContain('"vusers-10"')
  expect(csv).toContain('"ms"')
 } finally { await act(async () => root.unmount()); container.remove() }
})
