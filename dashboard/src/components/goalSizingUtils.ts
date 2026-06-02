import { GoalSizingReport, GoalSizingScenarioDraft, RunArchiveReport, ScenarioMode } from '../types'

// Baseline group discovered from a completed run's archive report. Used to
// render one target-TPS input per group in the Build-from-goal panel.
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
// min 1) so the panel can show an instant estimate before the server responds.
// The backend remains the source of truth for the applied draft.
export function littlesLawVUsers(targetTps: number, artMs: number): number {
  if (!(targetTps > 0) || !(artMs > 0)) return 0
  const vusers = Math.ceil(targetTps * (artMs / 1000) - 1e-9)
  return vusers < 1 ? 1 : vusers
}

export interface GoalSizedGroup {
  name: string
  script_id: string
  vusers: number
  ramp_up: string
}

// goalSizedGroups maps a server scenario draft onto the scenario form's group
// shape (vusers clamped to >= 1).
export function goalSizedGroups(draft: GoalSizingScenarioDraft): GoalSizedGroup[] {
  return (draft.groups || []).map((group, index) => ({
    name: group.name || (index === 0 ? '' : `Group ${index + 1}`),
    script_id: group.script_id || '',
    vusers: Math.max(1, Math.round(group.vusers || 1)),
    ramp_up: group.ramp_up || '',
  }))
}

export interface GoalSizedGoalFields {
  scenario_mode: ScenarioMode
  goal_target_average_tps?: number
  goal_min_vusers?: number
  goal_max_vusers?: number
}

// goalSizedGoalFields extracts the goal-oriented form fields from a draft.
export function goalSizedGoalFields(draft: GoalSizingScenarioDraft): GoalSizedGoalFields {
  return {
    scenario_mode: (draft.scenario_mode as ScenarioMode) || 'goal_oriented',
    goal_target_average_tps: draft.goal_config?.target_average_tps,
    goal_min_vusers: draft.goal_config?.min_vusers,
    goal_max_vusers: draft.goal_config?.max_vusers,
  }
}

// formatGoalSizingSummary renders a one-line summary of a sizing report.
export function formatGoalSizingSummary(report: GoalSizingReport): string {
  const groupCount = report.groups.length
  return `${report.total_vusers} VUsers across ${groupCount} group${groupCount === 1 ? '' : 's'} for ${report.total_target_tps} TPS`
}
