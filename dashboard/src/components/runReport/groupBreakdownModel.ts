import type { RunArchiveActionStats, RunArchiveReport } from '../../types'
import { reportPopulationActions, type MetricPopulation } from './reportPresentation'

export type GroupBreakdownStats = RunArchiveActionStats & { average_rate_per_second?: number }

export interface GroupBreakdownItem {
  name: string
  stats: GroupBreakdownStats
}

export interface GroupBreakdownGroup {
  /** The group's name as recorded; '' for records without a group. */
  name: string
  /** Undefined when the report has no per-group totals (an older report). */
  totals?: GroupBreakdownStats
  items: GroupBreakdownItem[]
}

export interface GroupBreakdown {
  overall?: GroupBreakdownStats
  groups: GroupBreakdownGroup[]
  /** The report predates per-group breakdowns: items are whole-run, not per group. */
  ungrouped: boolean
  truncated: boolean
}

const byName = (left: { name: string }, right: { name: string }) => left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: 'base' })

function items(record: Record<string, GroupBreakdownStats> | undefined): GroupBreakdownItem[] {
  return Object.entries(record || {}).map(([name, stats]) => ({ name, stats })).sort(byName)
}

// groupBreakdown lays the selected population out by group, then by the
// transactions (or requests) inside each group. A report without the
// breakdown lists the population's names under one whole-run section.
export function groupBreakdown(report: RunArchiveReport | null, population: MetricPopulation): GroupBreakdown {
  // A report from before metric populations has only its mixed overview.
  const overall = report?.metric_populations ? report.metric_populations[population] : report?.overview
  const breakdown = report?.group_breakdown
  if (!breakdown) {
    const names = items(reportPopulationActions(report, population))
    return { overall, groups: names.length ? [{ name: '', items: names }] : [], ungrouped: true, truncated: false }
  }
  const groups = Object.entries(breakdown[population] || {})
    .map(([name, group]) => ({ name, totals: group.totals, items: items(group.items) }))
    .sort((left, right) => left.name === '' ? 1 : right.name === '' ? -1 : byName(left, right))
  return { overall, groups, ungrouped: false, truncated: Boolean(report?.group_breakdown_truncated?.includes(population)) }
}

export function groupBreakdownLabel(name: string): string {
  return name || 'No group'
}
