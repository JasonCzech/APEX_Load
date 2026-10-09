// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { RunArchiveReport } from '../../types'
import RunReportVerdictBanner from './RunReportVerdictBanner'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('RunReportVerdictBanner', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.append(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })

  it('is one flat panel that names the computed verdict once', async () => {
    await act(async () => root.render(<RunReportVerdictBanner report={{ overview: {} } as unknown as RunArchiveReport} annotations={null} status="" explanation="" saving={false}
      onStatusChange={vi.fn()} onExplanationChange={vi.fn()} onSaveOverride={vi.fn(async () => true)} />))
    // The page wraps it in the one "Analyst assessment" disclosure; no second level inside.
    expect(container.querySelector('details')).toBeNull()
    expect(container.querySelector('h1')).toBeNull()
    const badge = container.querySelector('.run-report-verdict-badge')!
    const occurrences = (container.textContent || '').split(badge.textContent || '').length - 1
    expect(occurrences).toBe(1)
    expect(container.querySelector('select')).not.toBeNull()
    expect(container.querySelector('textarea')).not.toBeNull()
  })
})
