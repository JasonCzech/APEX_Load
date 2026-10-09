// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'

import BrowserInteractionsPanel from './BrowserInteractionsPanel'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../../hooks/apiClient', () => ({ request: mocks.request }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const analysis = {
  run_id: 'run-1', version: 'browser_interaction_patterns.v1', status: 'ready',
  thresholds: { repeat_min_gestures: 3, repeat_window_ms: 2000, repeat_radius_px: 32, heatmap_grid: 64 },
  coverage: { interactions: 5, records: 1, dropped: 2, incomplete: false, by_provenance: { scripted: 5 }, mapped: 4, excluded: { child_frame_unobserved: 1 } },
  patterns: [
    { id: 'p1', kind: 'scripted_repetition', provenance: 'scripted', route: '/cart', action_name: 'Tap buy', count: 3, start: '2026-10-08T12:00:00Z', end: '2026-10-08T12:00:01Z', detail: '3 gestures within 2000 ms and 32 CSS px', interactions: ['a', 'b', 'c'] },
    { id: 'p2', kind: 'dead_interaction', provenance: 'scripted', route: '/cart', count: 1, start: '2026-10-08T12:00:02Z', end: '2026-10-08T12:00:02Z', declared: 'element_visible', deadline_ms: 5000, detail: 'the declared element_visible outcome was not observed within 5000 ms', interactions: ['d'] },
  ],
  heatmaps: [
    { key: 'm1', route: '/cart', layout_revision: '00000000000000aa', viewport_width: 400, viewport_height: 800, orientation: 'portrait', engine: 'chromium', device_profile: 'mobile', columns: 64, rows: 64,
      cells: [[0, 0, 1], [63, 63, 3]], points: 4, targets: [{ role: 'textbox', label_sha256: 'a'.repeat(64), count: 2 }], by_kind: { tap: 4 }, by_provenance: { scripted: 4 } },
  ],
  notes: ['Patterns describe the run\'s recorded gestures.'],
}

async function render(response: unknown) {
  mocks.request.mockResolvedValue(response)
  const container = document.createElement('div')
  const root = createRoot(container)
  await act(async () => root.render(<BrowserInteractionsPanel testId="run-1" />))
  await act(async () => { await Promise.resolve() })
  return { container, root }
}

describe('BrowserInteractionsPanel', () => {
  afterEach(() => mocks.request.mockReset())

  it('lists the measured patterns and draws each layout\'s grid', async () => {
    const { container, root } = await render(analysis)
    expect(mocks.request).toHaveBeenCalledWith('/tests/run-1/archive/browser-interactions', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    const rows = [...container.querySelectorAll('tbody tr[data-pattern]')].map(row => row.textContent || '')
    expect(rows[0]).toContain('Scripted repetition')
    expect(rows[0]).toContain('Tap buy')
    expect(rows[1]).toContain('Dead interaction')
    expect(container.textContent).toContain('2 gestures were dropped by capture bounds')
    expect(container.textContent).toContain('A repeat is at least 3 gestures within 2000 ms and 32 CSS px')
    const cells = [...container.querySelectorAll('rect[data-cell]')]
    expect(cells.map(cell => cell.getAttribute('data-cell'))).toEqual(['0,0', '63,63'])
    expect(Number(cells[1].getAttribute('fill-opacity'))).toBeGreaterThan(Number(cells[0].getAttribute('fill-opacity')))
    expect(container.textContent).toContain('/cart · 400×800 portrait · chromium / mobile')
    expect(container.textContent).toContain('textbox')
    expect(container.textContent).toContain('Not mapped: 1 child frame unobserved.')
    await act(async () => root.unmount())
  })

  it('says when a run recorded no interaction evidence', async () => {
    const { container, root } = await render({ ...analysis, status: 'not_captured', patterns: [], heatmaps: [] })
    expect(container.textContent).toContain('This run recorded no interaction evidence.')
    await act(async () => root.unmount())
  })
})
