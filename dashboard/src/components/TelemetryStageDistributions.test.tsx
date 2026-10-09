// @vitest-environment jsdom

import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'

import TelemetryStageDistributions from './TelemetryStageDistributions'
import { STAGE_ADOPTION_SLACK_MS, stageCoverageNote } from './telemetryStageCoverage'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TelemetryStageDistributions', () => {
  it('shows each stage on its own and no figures for an unsampled stage', () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<TelemetryStageDistributions stages={[
        { stage: 'controller_fanout', unit: 'ms', availability: 'measured', samples: 40, p50_ms: 1.25, p95_ms: 12, p99_ms: 30, max_ms: 2400 },
        { stage: 'transport', unit: 'ms', availability: 'unavailable', samples: 0, excluded: { clock_unqualified: 3 } },
        { stage: 'browser_render', unit: 'ms', availability: 'measured', samples: 5, p50_ms: 16, p95_ms: 20, p99_ms: 22, max_ms: 22, advisory: true, excluded: { background_tab: 2 } },
      ]} />)
    })
    const rows = [...container.querySelectorAll('tbody tr')]
    const cells = (stage: string) => [...(rows.find(row => row.getAttribute('data-stage') === stage)?.querySelectorAll('td') || [])].map(cell => cell.textContent)
    expect(cells('controller_fanout')).toEqual(['Controller fan-out', '40', '1.3 ms', '12 ms', '30 ms', '2.40 s', ''])
    expect(cells('transport')).toEqual(['Transport', '0', '—', '—', '—', '—', 'not sampled · excluded: 3 clock not qualified'])
    expect(cells('browser_render')[6]).toBe('advisory, not run health · excluded: 2 background tab')
    act(() => root.unmount())
  })

  it('renders nothing without distributions', () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => root.render(<TelemetryStageDistributions />))
    expect(container.innerHTML).toBe('')
    act(() => root.render(<TelemetryStageDistributions stages={[{ stage: 'future_stage', unit: 'ms', availability: 'unavailable', samples: 0, excluded: { zero_reason: 0, some_new_reason: 1 } }]} />))
    const cells = [...container.querySelectorAll('tbody td')].map(cell => cell.textContent)
    expect(cells[0]).toBe('future_stage')
    expect(cells[6]).toBe('not sampled · excluded: 1 some new reason')
    act(() => root.unmount())
  })

  it('says when the controller adopted the run or never sampled it', () => {
    const started = '2026-10-08T12:00:00.000Z'
    const at = (offsetMs: number) => new Date(Date.parse(started) + offsetMs).toISOString()
    const stage = (sampledFrom?: string, reason?: string) => ({ stage: 'transport', unit: 'ms', availability: 'unavailable', samples: 0, sampled_from: sampledFrom, reason })
    expect(stageCoverageNote([stage(at(STAGE_ADOPTION_SLACK_MS))], started)).toBe('')
    expect(stageCoverageNote([stage(at(STAGE_ADOPTION_SLACK_MS + 1))], started)).toMatch(/took over the run/)
    expect(stageCoverageNote([stage(at(10 * 60_000))])).toBe('')
    expect(stageCoverageNote([stage(undefined, 'recorder_lost')], started)).toMatch(/holds no stage samples for the run/)
    const container = document.createElement('div')
    const root = createRoot(container)
    act(() => {
      root.render(<TelemetryStageDistributions stages={[stage(at(10 * 60_000))]} startedAt={started} />)
    })
    expect(container.querySelector('[role="note"]')?.textContent).toMatch(/earlier publications are not included/)
    act(() => root.unmount())
  })
})
