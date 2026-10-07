// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { it, expect } from 'vitest'
import BaselineBrowserDocuments from './BaselineBrowserDocuments'
import type { RunIdentity } from './model'

const baseline = { id: 'base', name: 'Release 1' } as RunIdentity
const tuple = { baseline_id: 'base', unit: 'ms', frame_scope: 'main_frame', lifecycle_phase: 'navigation', engine: 'chromium', device_profile: 'desktop', current_final: 4, baseline_final: 4 }

it('compares matching tuples and names why others are refused', async () => {
  const node = document.createElement('div'); const root = createRoot(node)
  try {
    await act(async () => root.render(<BaselineBrowserDocuments baselines={[baseline]} softNavigation="unsupported" rows={[
      { ...tuple, metric: 'lcp', comparable: true, current_p75: 800, baseline_p75: 400, delta_p75: 400 },
      { ...tuple, metric: 'cls', unit: 'score', comparable: false, reason: 'cohort_mismatch' },
    ]} />))
    const rows = Array.from(node.querySelectorAll('tbody tr')).map(row => row.textContent)
    expect(rows[0]).toContain('LCP (ms)'); expect(rows[0]).toContain('Release 1'); expect(rows[0]).toContain('+400 ms'); expect(rows[0]).toContain('4 final documents')
    expect(rows[1]).toContain('Not compared: Different actual runtime cohort'); expect(rows[1]).not.toContain('+')
    expect(node.textContent).toContain('Soft (same-document) navigations: unsupported')
    await act(async () => root.render(<BaselineBrowserDocuments baselines={[baseline]} />))
    expect(node.textContent).toBe('')
  } finally { await act(async () => root.unmount()) }
})
