import type { RunArchiveReportProfile } from '../types'

export type ReportProfileId = 'executive' | 'engineer' | 'audit' | (string & {})

export interface ReportTypeOption {
  id: ReportProfileId
  label: string
  blurb: string
  bestFor?: string
}

const FALLBACK_REPORT_TYPES: ReportTypeOption[] = [
  { id: 'executive', label: 'Executive', blurb: 'High-level verdict, key metrics, and release decision narrative for stakeholders.' },
  { id: 'engineer', label: 'Engineer', blurb: 'Full drilldown with per-action stats, timelines, errors, resources, and diagnostics.' },
  { id: 'audit', label: 'Audit', blurb: 'Governance evidence, provenance, completeness, and artifact integrity.' },
]

export function buildReportTypeOptions(profiles?: RunArchiveReportProfile[]): ReportTypeOption[] {
  const htmlProfiles = (profiles || []).filter(profile => profile.export_formats?.includes('html'))
  if (htmlProfiles.length === 0) return FALLBACK_REPORT_TYPES
  return htmlProfiles.map(profile => ({
    id: profile.id,
    label: profile.label,
    blurb: profile.description,
    bestFor: profile.best_for || profile.audience,
  }))
}
