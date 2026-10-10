import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import type { ReportRootCause } from '../../types/rca'
import ReportRootCauseView from './ReportRootCauseView'

describe('ReportRootCauseView', () => {
  it('previews the browser baseline as the exports render it, qualification included', () => {
    const view: ReportRootCause = {
      source: 'v2', schema_version: 'apexload.root_cause.v2', outcome: 'completed', summary: 'One hypothesis.', hypotheses: [],
      browser_baseline: {
        version: 'browser_baseline.v1', status: 'complete', baseline: 'base-run', compared: 2, anomalies: 1, unscored: 1,
        summary: '2 cohorts compared with baseline run base-run at the same load, 1 anomaly, 1 unscored.',
        findings: ['LCP p75 3100 ms, higher than the baseline'],
        qualification: ['Baseline run base-run partial: pacing changed', 'Browser policy policy-1@3', 'Excluded outside steady load: 4 buckets'],
        limitations: ['The baseline run has no browser evidence for one cohort.'],
      },
    }
    const html = renderToStaticMarkup(<ReportRootCauseView view={view} />)
    expect(html).toContain('2 cohorts compared with baseline run base-run')
    expect(html).toContain('LCP p75 3100 ms, higher than the baseline')
    expect(html).toContain('Baseline run base-run partial: pacing changed; Browser policy policy-1@3; Excluded outside steady load: 4 buckets.')
    expect(html).toContain('Limitation: The baseline run has no browser evidence for one cohort.')
  })
})
