// @vitest-environment jsdom

import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import TestExecutionHeroMetrics from './TestExecutionHeroMetrics'
import type { RunArchiveReport } from '../types'

describe('TestExecutionHeroMetrics', () => {
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
  })

  it('labels the archived interval average as Avg TPS instead of the peak', async () => {
    const archiveReport = {
      overview: {
        peak_tps: 100,
        peak_active_vusers: 5,
        avg_response_ms: 10,
        error_pct: 0,
        transactions: 30,
      },
      summary_timeline: [{ tps: 10 }, { tps: 20 }, { tps: 30 }],
    } as RunArchiveReport

    await act(async () => root.render(
      <TestExecutionHeroMetrics archiveReport={archiveReport} summary={null} />,
    ))

    const cards = Array.from(container.querySelectorAll('.hero-card'))
    expect(cards[1]?.textContent).toContain('20.0')
    expect(cards[1]?.textContent).not.toContain('100.0')
  })
})
