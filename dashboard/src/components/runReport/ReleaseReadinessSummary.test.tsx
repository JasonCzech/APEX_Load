// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import ReleaseReadinessSummary from './ReleaseReadinessSummary'
import type { RunArchiveReport } from '../../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('ReleaseReadinessSummary', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })

  it('answers trend, risks and coverage when no acceptance criteria are set', async () => {
    const report = {
      verdict: { status: 'Not assessed', conditional: true, failed: false, explanation: '' },
      completeness: { score_pct: 92 },
      release_trend: { release_id: 'r', release_name: 'PI29', run_count: 2, has_baseline: true, regressed: true, regressions: ['error rate regressed'], current: { run_name: 'Run B' }, baseline: { run_name: 'Run A' }, trajectory: {} },
    } as unknown as RunArchiveReport
    await act(async () => root.render(<ReleaseReadinessSummary report={report} findings={[{ id: 'f', title: 'Recurring error pattern', severity: 'major', observed: '"got 503" occurred 915 time(s)' }, { id: 'i', title: 'Info only', severity: 'info' }]} />))
    expect(container.textContent).toContain('Regressed against Run A: error rate regressed.')
    expect(container.textContent).toContain('Recurring error pattern')
    expect(container.textContent).not.toContain('Info only')
    expect(container.textContent).toContain('Evidence coverage: 92%')
  })

  it('stays out of the way when acceptance criteria give a verdict', async () => {
    await act(async () => root.render(<ReleaseReadinessSummary report={{ verdict: { status: 'Pass', conditional: false, failed: false, explanation: '' } } as unknown as RunArchiveReport} />))
    expect(container.textContent).toBe('')
  })
})
