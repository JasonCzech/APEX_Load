import { RunArchiveReport } from '../types'

// Baseline group discovered from a completed run's archive report. The Goal-Driven
// worksheet matches these to scenario groups to import their response times.
export interface BaselineGroupOption {
  group: string
  script_id?: string
  baseline_art_ms: number
  basis: 'group' | 'overview'
}

// baselineGroupOptions derives the sizable groups from a baseline archive
// report. When per-group rollup is present it uses by_group; otherwise it falls
// back to a single synthetic group backed by the overall average response time.
export function baselineGroupOptions(report: RunArchiveReport | null | undefined): BaselineGroupOption[] {
  if (!report) return []
  const manifestGroups = report.manifest?.provenance?.groups || []
  const scriptByGroup = new Map<string, string>()
  for (const g of manifestGroups) {
    if (g?.name) scriptByGroup.set(g.name, g.script_id || '')
  }

  const byGroup = report.by_group || {}
  const names = Object.keys(byGroup)
  if (names.length > 0) {
    return names
      .map(name => ({
        group: name,
        script_id: scriptByGroup.get(name) || undefined,
        baseline_art_ms: byGroup[name]?.avg_response_ms ?? 0,
        basis: 'group' as const,
      }))
      .filter(option => option.baseline_art_ms > 0)
      .sort((a, b) => a.group.localeCompare(b.group))
  }

  const overviewART = report.overview?.avg_response_ms ?? 0
  if (overviewART > 0) {
    const single = manifestGroups[0]
    return [{
      group: single?.name || 'All transactions',
      script_id: single?.script_id || undefined,
      baseline_art_ms: overviewART,
      basis: 'overview',
    }]
  }
  return []
}

// littlesLawVUsers mirrors the backend formula (VUsers = ceil(TPS * ART_s),
// min 1) used by POST /api/v1/scenarios/size-from-goal, so the Goal-Driven
// worksheet can size VUsers without a round trip.
export function littlesLawVUsers(targetTps: number, artMs: number): number {
  if (!(targetTps > 0) || !(artMs > 0)) return 0
  const vusers = Math.ceil(targetTps * (artMs / 1000) - 1e-9)
  return vusers < 1 ? 1 : vusers
}
