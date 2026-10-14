import type { BrunoNativeScriptCandidate } from '../types/scriptImports'

// One decision per field across the selected requests: a trace header's
// parts, a message ID, a time stamp. Each request's suggestions keep their
// own IDs, which the commit applies per request.
export interface BrunoGeneratedValueGroup {
  key: string
  description: string
  requests: number
  /** Every member arrived selected: the field names a per-request ID or time stamp. */
  suggested: boolean
  members: Array<{ candidateId: string; suggestionId: string }>
}

export function brunoGeneratedValueGroups(candidates: readonly BrunoNativeScriptCandidate[]): BrunoGeneratedValueGroup[] {
  const groups = new Map<string, BrunoGeneratedValueGroup>()
  const requestIds = new Map<string, Set<string>>()
  for (const candidate of candidates) {
    for (const suggestion of candidate.parameterization?.suggestions ?? []) {
      if (suggestion.kind !== 'generated' || suggestion.pre_applied) continue
      // A field's second value in one request is named field_2.
      const key = `${suggestion.rule_id}\u0000${suggestion.var_name.replace(/_\d+$/, '')}`
      let group = groups.get(key)
      if (!group) {
        group = { key, description: suggestion.rationale, requests: 0, suggested: true, members: [] }
        groups.set(key, group)
        requestIds.set(key, new Set())
      }
      group.members.push({ candidateId: candidate.candidate_id, suggestionId: suggestion.id })
      requestIds.get(key)!.add(candidate.candidate_id)
      group.requests = requestIds.get(key)!.size
      group.suggested = group.suggested && suggestion.auto_apply
    }
  }
  return [...groups.values()]
    .sort((left, right) => Number(right.suggested) - Number(left.suggested) || left.description.localeCompare(right.description))
}

/** The suggestion IDs to apply, per request, for the checked groups. */
export function brunoGeneratedValueSelections(groups: readonly BrunoGeneratedValueGroup[], choices: Record<string, boolean>): Record<string, string[]> {
  const selections: Record<string, string[]> = {}
  for (const group of groups) {
    if (!(choices[group.key] ?? group.suggested)) continue
    for (const member of group.members) (selections[member.candidateId] ||= []).push(member.suggestionId)
  }
  return selections
}
