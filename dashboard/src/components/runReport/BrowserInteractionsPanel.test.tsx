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
    expect(container.textContent).toContain('2 gestures were dropped by capture bounds or lost before the archive')
    expect(container.textContent).toContain('A repeat is at least 3 click, tap or check gestures within 2000 ms and 32 CSS px (or with one action name, gesture kind and target when no point was dispatched)')
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
    expect(container.textContent).toContain("turned on in the browser script's or scenario group's Runtime Settings › Browser")
    await act(async () => root.unmount())
  })

  it('renders a ready analysis whose lists the server left out, and a lost capture', async () => {
    const { container, root } = await render({ ...analysis, patterns: null, heatmaps: null, notes: null })
    expect(container.textContent).toContain('No pattern was found.')
    await act(async () => root.unmount())
    const lost = await render({ ...analysis, status: 'unavailable', coverage: { ...analysis.coverage, dropped: 7 }, patterns: [], heatmaps: [] })
    expect(lost.container.textContent).toContain('none reached its archive (7 gestures lost)')
    await act(async () => lost.root.unmount())
  })

  it('states the scroll offsets on each axis a map varied in', async () => {
    const scrolled = { ...analysis, heatmaps: [{ ...analysis.heatmaps[0], device_scale_factor: 2, scroll: { min_x: 0, max_x: 600, min_y: 0, max_y: 0 } }] }
    const { container, root } = await render(scrolled)
    expect(container.textContent).toContain('@2x')
    expect(container.textContent).toContain('recorded scroll offsets x 0–600 px, y 0 px')
    expect(container.textContent).not.toContain('y 0–0')
    await act(async () => root.unmount())
  })

  it('shows separate exact scroll cohorts and unknown-context exclusions for v2', async () => {
    const result = { ...analysis, version: 'browser_interaction_patterns.v2',
      coverage: { ...analysis.coverage, excluded: { engine_profile_unknown: 2, layout_unknown: 1 } },
      heatmaps: [0, 0.25].map((y, index) => ({ ...analysis.heatmaps[0], key: `map-${index}`, device_profile: '', scroll: { min_x: 0, max_x: 0, min_y: y, max_y: y } })),
    }
    const { container, root } = await render(result)
    expect(container.querySelectorAll('figure')).toHaveLength(2)
    expect(container.textContent).toContain('recorded scroll offsets x 0 px, y 0 px')
    expect(container.textContent).toContain('recorded scroll offsets x 0 px, y 0.25 px')
    expect(container.textContent).toContain('chromium / default')
    expect(container.textContent).toContain('Not mapped: 2 engine profile unknown, 1 layout unknown.')
    await act(async () => root.unmount())
  })

  it('says a malformed analysis could not be read instead of failing the report', async () => {
    const { container, root } = await render({ status: 'ready' })
    expect(container.textContent).toContain('The interaction analysis could not be read.')
    await act(async () => root.unmount())
  })

  it('describes an unfinalized archive plainly and retries other failures', async () => {
    const rejectWith = (message: string, status: number) => Object.assign(new Error(message), { status })
    mocks.request.mockRejectedValueOnce(rejectWith('the run archive is not finalized', 409)).mockResolvedValueOnce(analysis)
    const container = document.createElement('div')
    const root = createRoot(container)
    await act(async () => root.render(<BrowserInteractionsPanel testId="run-1" />))
    await act(async () => { await Promise.resolve() })
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain("Interaction patterns appear once the run's archive is finalized.")
    expect(container.textContent).not.toContain('the run archive is not finalized')
    await act(async () => { [...container.querySelectorAll('button')].find(button => button.textContent === 'Check again')?.click() })
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('Scripted repetition')

    mocks.request.mockRejectedValueOnce(rejectWith("the run's interaction evidence could not be read", 500)).mockResolvedValueOnce(analysis)
    await act(async () => root.render(<BrowserInteractionsPanel testId="run-2" />))
    await act(async () => { await Promise.resolve() })
    const alert = container.querySelector('[role="alert"]')
    expect(alert?.textContent).toContain('Interaction patterns could not be loaded')
    expect(alert?.querySelector('details')?.textContent).toContain('interaction evidence could not be read')
    await act(async () => { [...container.querySelectorAll('button')].find(button => button.textContent === 'Retry interaction patterns')?.click() })
    await act(async () => { await Promise.resolve() })
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain('Scripted repetition')

    mocks.request.mockRejectedValueOnce(rejectWith('archive not found', 404))
    await act(async () => root.render(<BrowserInteractionsPanel testId="run-3" />))
    await act(async () => { await Promise.resolve() })
    expect(container.querySelector('[role="alert"]')).toBeNull()
    // An HA controller answers 404 until the finalized archive is published,
    // so the panel offers to check again.
    expect(container.querySelector('[role="note"]')?.textContent).toBe("This run's saved archive is not available yet (it may still be publishing), so no interaction patterns can be shown. Check again")
    mocks.request.mockResolvedValueOnce(analysis)
    await act(async () => { [...container.querySelectorAll('button')].find(button => button.textContent === 'Check again')?.click() })
    await act(async () => { await Promise.resolve() })
    expect(container.textContent).toContain('Scripted repetition')
    await act(async () => root.unmount())
  })
  it('distinguishes native patterns, exclusions and historical geometry for v3', async () => {
    const result = { ...analysis, version: 'browser_interaction_patterns.v3',
      coverage: { ...analysis.coverage, pattern_eligible: 3, pattern_excluded: { legacy_identity_unavailable: 2 } },
      patterns: [{ ...analysis.patterns[0], authored_step: { version: 1, script_sha256: 'a'.repeat(64), step_path: 'actions[3]', step_id: 'b'.repeat(64) } }],
      heatmaps: ['native_dispatch', 'post_action'].map((observation, i) => ({ ...analysis.heatmaps[0], key: `v3-${i}`, observation })),
    }
    const { container, root } = await render(result)
    expect(container.textContent).toContain('3 gestures qualify for patterns.')
    expect(container.textContent).toContain('2000 ms of native dispatch')
    expect(container.textContent).toContain('Engine retries are separate.')
    expect(container.textContent).toContain('Excluded from patterns: 2 legacy identity unavailable.')
    expect(container.textContent).toContain('Step actions[3]')
    expect(container.textContent).toContain('native dispatch geometry')
    expect(container.textContent).toContain('historical post-action geometry')
    expect(container.textContent).not.toContain('one action name')
    await act(async () => root.unmount())
    const empty = await render({ ...result, coverage: { ...result.coverage, pattern_eligible: 0 }, patterns: [] })
    expect(empty.container.textContent).toContain('No gestures qualified for pattern analysis.')
    await act(async () => empty.root.unmount())
  })
  it('renders qualified v4 patterns, child-frame transform counts and multi-outcome dead interactions', async () => {
    const result = {
      ...analysis,
      version: 'browser_interaction_patterns.v4',
      coverage: { ...analysis.coverage, mapped: 4, mapped_via_transform: 2, pattern_eligible: 3, pattern_excluded: { legacy_identity_unavailable: 1 } },
      patterns: [
        { ...analysis.patterns[1], id: 'p2-0', outcome_kind: 'dialog', outcome_index: 0, declared: 'dialog', deadline_ms: 1500, detail: 'the declared dialog outcome was not observed within 1500 ms', authored_step: { version: 1, script_sha256: 'a'.repeat(64), step_path: 'actions[1]', step_id: 'b'.repeat(64) } },
        { ...analysis.patterns[1], id: 'p2-1', outcome_kind: 'assertion', outcome_index: 1, declared: 'visible', deadline_ms: 2000, detail: 'the declared visible outcome was not observed within 2000 ms', authored_step: { version: 1, script_sha256: 'a'.repeat(64), step_path: 'actions[1]', step_id: 'b'.repeat(64) } },
      ],
    }
    const { container, root } = await render(result)
    expect(container.textContent).toContain('4 mapped (2 via child-frame transform).')
    expect(container.textContent).toContain('3 gestures qualify for patterns.')
    expect(container.textContent).toContain('2000 ms of native dispatch')
    expect(container.textContent).toContain('Excluded from patterns: 1 legacy identity unavailable.')
    expect(container.textContent).toContain('Outcome dialog[0]')
    expect(container.textContent).toContain('Outcome assertion[1]')
    expect(container.querySelectorAll('tbody tr[data-pattern="dead_interaction"]')).toHaveLength(2)
    await act(async () => root.unmount())
  })

})
