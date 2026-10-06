// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import BrowserUXAssessment from './BrowserUXAssessment'
import type { BrowserLiveMetrics } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const explicitZero: BrowserLiveMetrics = { samples: 1, avg_lcp_ms: 0, avg_inp_ms: 0, avg_cls_score: 0, avg_long_task_count: 0, avg_blocking_resources: 0, avg_browser_process_cpu_pct: 0, avg_browser_process_mem_mb: 0 }

describe('BrowserUXAssessment', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })

  it('discloses unscored missing fields without a healthy class or synthetic estimate', async () => {
    await act(async () => root.render(<BrowserUXAssessment summary={{ samples: 2 }} devices={{ missing: { samples: 1 } }} />))
    const panel = container.querySelector('[aria-label="Browser legacy summary assessment"]')!
    expect(panel.classList.contains('unscored')).toBe(true)
    expect(panel.getAttribute('data-model-version')).toBe('legacy-summary-availability-v2')
    expect(panel.textContent).toContain('Unscored')
    expect(panel.textContent).toContain('0/7 required fields available')
    expect(panel.textContent).toContain('Field availability is not document or time coverage')
    expect(panel.textContent).toContain('not a standards-qualified Core Web Vitals score')
    expect(panel.textContent).toContain('1 device summary unranked')
    expect(panel.textContent).not.toContain('100/100')
    expect(panel.textContent).not.toContain('Weakest complete summary:')
    expect(panel.querySelector('.good')).toBeNull()
    expect(panel.querySelectorAll('[data-availability="missing"]')).toHaveLength(7)
  })

  it('keeps invalid values unscored and observed contributions available', async () => {
    await act(async () => root.render(<BrowserUXAssessment summary={{ ...explicitZero, avg_inp_ms: -1, avg_lcp_ms: Infinity }} />))
    expect(container.textContent).toContain('5/7 required fields available')
    expect(container.querySelectorAll('[data-availability="invalid"]')).toHaveLength(2)
    expect(container.textContent).toContain('Value must be finite and nonnegative')
    expect(container.textContent).toContain('Component 100/100 · contribution 14.29/100')
    expect(container.querySelector('.browser-ux-title')?.textContent).toBe('Unscored')
    expect(container.textContent).not.toContain('Infinity')
    expect(container.textContent).not.toContain('-1.0 ms')
  })

  it('labels an explicit zero complete summary as a legacy estimate and excludes an unknown device', async () => {
    await act(async () => root.render(<BrowserUXAssessment summary={explicitZero} devices={{ complete: explicitZero, unknown: { samples: 1 } }} />))
    expect(container.querySelector('.browser-ux-title')?.textContent).toBe('Within legacy thresholds · 100/100 legacy estimate')
    expect(container.textContent).toContain('7/7 required fields available')
    expect(container.textContent).toContain('Weakest complete summary: complete')
    expect(container.textContent).toContain('unknown: Required fields unavailable: LCP (missing)')
    expect(container.textContent).not.toContain('Healthy')
    expect(container.textContent).toContain('0.000')
  })

  it('retains observations with a refused population rather than assigning component scores', async () => {
    await act(async () => root.render(<BrowserUXAssessment summary={{ ...explicitZero, samples: 0 }} />))
    expect(container.querySelector('.browser-ux-title')?.textContent).toBe('Unscored')
    expect(container.textContent).toContain('Sample count must be a positive safe integer')
    expect(container.textContent).toContain('0.0 ms')
    expect(container.querySelector('.good')).toBeNull()
  })
})
