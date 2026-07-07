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
  'sla',
  'diagnostics',
  'compare',
  'exports',
]

const SECTION_ALIASES: Record<string, string> = {
  summary: 'kpis',
  executive_summary: 'verdict',
  release_trend: 'compare',
  transaction_sla: 'sla',
  sla_compliance: 'sla',
  transaction_details: 'diagnostics',
  archive_analysis: 'diagnostics',
  evidence: 'diagnostics',
}

const FALLBACK_PROFILE_SECTIONS: Record<ReportLens, string[]> = {
  executive: ['verdict', 'kpis', 'timeline', 'sla', 'compare', 'exports'],
  engineer: ['verdict', 'kpis', 'timeline', 'sla', 'diagnostics', 'compare', 'exports'],
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
  const visibleSections = ordered.filter(section => included.includes(section) || ['verdict', 'kpis', 'timeline', 'sla', 'exports'].includes(section))
  return {
    visibleSections,
    sectionSet: new Set(visibleSections),
    emphasizedSections: new Set(EMPHASIS[lens]),
  }
}
