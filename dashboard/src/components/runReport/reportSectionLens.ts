import type { ReportLens } from '../../appRouting'
import type { RunArchiveReport, RunArchiveReportProfile } from '../../types'

export interface ReportLensSectionModel {
  visibleSections: string[]
  sectionSet: Set<string>
  emphasizedSections: Set<string>
}

const DEFAULT_SECTION_ORDER = [
  'verdict',
  'kpis',
  'timeline',
  'imported_dashboards',
  'sla',
  'diagnostics',
  'compare',
  'exports',
]

const SECTION_ALIASES: Record<string, string> = {
  actions: 'sla',
  artifact_integrity: 'diagnostics',
  // The Go report profiles declare this section as "dashboard_panels"; the Run Report
  // renders it under the "imported_dashboards" lens id.
  dashboard_panels: 'imported_dashboards',
  audit_identity: 'diagnostics',
  authorization: 'diagnostics',
  browser: 'diagnostics',
  change_attribution: 'compare',
  completeness: 'diagnostics',
  evidence: 'diagnostics',
  evidence_tiers: 'diagnostics',
  errors: 'diagnostics',
  executive_overview: 'kpis',
  summary: 'kpis',
  executive_summary: 'verdict',
  governance: 'diagnostics',
  graphs: 'timeline',
  network_phases: 'diagnostics',
  official_outcome: 'verdict',
  provenance: 'diagnostics',
  raw_exports: 'exports',
  report_authorship: 'diagnostics',
  resources: 'diagnostics',
  release_trend: 'compare',
  risk_summary: 'diagnostics',
  root_cause: 'diagnostics',
  schema_compatibility: 'diagnostics',
  signoff: 'diagnostics',
  sla_detail: 'sla',
  sla_rollup: 'sla',
  soap: 'diagnostics',
  summary_timeline: 'timeline',
  technical_overview: 'kpis',
  transaction_sla: 'sla',
  sla_compliance: 'sla',
  transaction_details: 'diagnostics',
  archive_analysis: 'diagnostics',
  web2_migration: 'diagnostics',
}

const FALLBACK_PROFILE_SECTIONS: Record<ReportLens, string[]> = {
  executive: ['verdict', 'kpis', 'timeline', 'imported_dashboards', 'sla', 'compare', 'exports'],
  engineer: ['verdict', 'kpis', 'timeline', 'imported_dashboards', 'sla', 'diagnostics', 'compare', 'exports'],
  audit: ['verdict', 'kpis', 'timeline', 'sla', 'diagnostics', 'exports'],
}

const EMPHASIS: Record<ReportLens, string[]> = {
  executive: ['verdict', 'kpis', 'compare'],
  engineer: ['timeline', 'sla', 'diagnostics'],
  audit: ['sla', 'diagnostics', 'exports'],
}

function canonicalSection(section: string): string {
  const key = section.trim().toLowerCase()
  return SECTION_ALIASES[key] || key
}

function profileForLens(report: RunArchiveReport | null, lens: ReportLens): RunArchiveReportProfile | undefined {
  return report?.report_profiles?.find(profile => profile.id === lens)
}

function uniqueOrdered(sections: string[]): string[] {
  const seen = new Set<string>()
  const ordered: string[] = []
  sections.forEach(section => {
    const canonical = canonicalSection(section)
    if (!canonical || seen.has(canonical)) return
    seen.add(canonical)
    ordered.push(canonical)
  })
  return ordered
}

export function reportSectionLens(report: RunArchiveReport | null, lens: ReportLens): ReportLensSectionModel {
  const profile = profileForLens(report, lens)
  const included = uniqueOrdered(profile?.included_sections?.length ? profile.included_sections : FALLBACK_PROFILE_SECTIONS[lens])
  const ordered = uniqueOrdered([
    ...(profile?.section_order || []),
    ...DEFAULT_SECTION_ORDER,
    ...included,
  ]).filter(section => included.includes(section) || DEFAULT_SECTION_ORDER.includes(section))
  // imported_dashboards is not part of the Go archive report (it comes from a separate
  // snapshot endpoint), so it never appears in a profile's included_sections. Keep it
  // always-on for executive/engineer lenses; the section component renders nothing when
  // the run had no imported dashboards, so there is no clutter.
  const alwaysOn = lens === 'audit'
    ? ['verdict', 'kpis', 'timeline', 'sla', 'exports']
    : ['verdict', 'kpis', 'timeline', 'imported_dashboards', 'sla', 'exports']
  const visibleSections = ordered.filter(section => included.includes(section) || alwaysOn.includes(section))
  return {
    visibleSections,
    sectionSet: new Set(visibleSections),
    emphasizedSections: new Set(EMPHASIS[lens]),
  }
}
