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

  it('maps backend engineer profile evidence sections into report panels', () => {
    const report = {
      report_profiles: [{
        id: 'engineer',
        label: 'Engineer',
        description: '',
        export_formats: ['html'],
        included_sections: ['technical_overview', 'graphs', 'sla_detail', 'network_phases', 'errors', 'resources', 'root_cause', 'raw_exports'],
        section_order: ['technical_overview', 'graphs', 'sla_detail', 'network_phases', 'raw_exports'],
      }],
    } as RunArchiveReport

    const model = reportSectionLens(report, 'engineer')

    expect(model.visibleSections).toEqual(expect.arrayContaining(['kpis', 'timeline', 'sla', 'diagnostics', 'exports']))
  })

  it('maps backend audit profile evidence sections into diagnostics instead of dropping them', () => {
    const report = {
      report_profiles: [{
        id: 'audit',
        label: 'Audit',
        description: '',
        export_formats: ['html'],
        included_sections: ['audit_identity', 'authorization', 'governance', 'schema_compatibility', 'evidence_tiers', 'artifact_integrity', 'official_outcome'],
        section_order: ['audit_identity', 'authorization', 'schema_compatibility', 'artifact_integrity', 'official_outcome'],
      }],
    } as RunArchiveReport

    const model = reportSectionLens(report, 'audit')

    expect(model.sectionSet.has('diagnostics')).toBe(true)
    expect(model.sectionSet.has('verdict')).toBe(true)
  })
})
