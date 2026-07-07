import { describe, expect, it } from 'vitest'

import type { RunArchiveReport } from '../../types'
import { reportSectionLens } from './reportSectionLens'

describe('reportSectionLens', () => {
  it('maps profile sections through aliases while preserving core report sections', () => {
    const report = {
      report_profiles: [{
        id: 'engineer',
        label: 'Engineer',
        description: '',
        export_formats: ['html'],
        included_sections: ['overview', 'sla_compliance', 'archive_analysis', 'release_trend'],
        section_order: ['sla_compliance', 'archive_analysis'],
      }],
    } as RunArchiveReport

    const model = reportSectionLens(report, 'engineer')

    expect(model.sectionSet.has('verdict')).toBe(true)
    expect(model.sectionSet.has('timeline')).toBe(true)
    expect(model.sectionSet.has('sla')).toBe(true)
    expect(model.sectionSet.has('diagnostics')).toBe(true)
    expect(model.sectionSet.has('compare')).toBe(true)
  })
})
