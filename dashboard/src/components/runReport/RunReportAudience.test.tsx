import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import RunReportLensSwitcher from './RunReportLensSwitcher'
import RunReportVerdictBanner from './RunReportVerdictBanner'
import type { ReportAnnotations } from '../../types'

const annotations: ReportAnnotations = {
  test_id: 'run-1', profile: 'engineer', title: '', prepared_by: '', executive_narrative: '', section_comments: {},
  verdict_override: 'Pass', verdict_explanation: 'Signed off', updated_at: '2026-10-07T10:00:00Z', updated_by: 'Engineer author',
  verdict_profile: 'executive', verdict_updated_at: '2026-10-07T09:00:00Z', verdict_updated_by: 'Release manager',
}

describe('report audience', () => {
  it('labels the audience switcher and marks the active audience as pressed', () => {
    const html = renderToStaticMarkup(<RunReportLensSwitcher activeLens="engineer" onLensChange={() => undefined} />)
    expect(html).toContain('Report audience')
    expect(html).toContain('Affects exports and report commentary')
    expect(html).toMatch(/aria-pressed="true"[^>]*>Engineer</)
    expect(html).not.toContain('role="tab"')
  })

  it('names who set the shared analyst verdict and from which audience', () => {
    const html = renderToStaticMarkup(
      <RunReportVerdictBanner report={null} annotations={annotations} status="Pass" explanation="Signed off" saving={false}
        onStatusChange={() => undefined} onExplanationChange={() => undefined} onSaveOverride={async () => true} />,
    )
    expect(html).toContain('Set by Release manager from the Executive report')
    expect(html).toContain('it appears in every report audience and export')
  })
})
