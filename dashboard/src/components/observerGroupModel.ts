export const OBSERVER_MAX_VUSERS = 10
export const LOAD_GROUP_MAX_VUSERS = 1_000_000

type GroupRole = 'load' | 'observer' | undefined
type GroupDemand = { vusers: number; stages?: Array<{ target_vusers?: number }> }

export function maxVUsersForRole(role: GroupRole): number {
  return role === 'observer' ? OBSERVER_MAX_VUSERS : LOAD_GROUP_MAX_VUSERS
}

export function maxSharedVUsersForRoles(roles: GroupRole[]): number {
  return roles.reduce((limit, role) => Math.min(limit, maxVUsersForRole(role)), LOAD_GROUP_MAX_VUSERS)
}

export function clampVUsersForRole(value: number, role: GroupRole, minimum = 0): number {
  const finiteValue = Number.isFinite(value) ? Math.trunc(value) : minimum
  return Math.min(maxVUsersForRole(role), Math.max(minimum, finiteValue))
}

// The single definition of "this group's demand is owned by its stages".
// Every consumer -- the validator below, the effective-demand helper and the
// editors -- must agree on it, otherwise a surface can present a VUsers value
// the validator will not accept.
export function stagePeakVUsers(stages?: Array<{ target_vusers?: number }>): number {
  return (stages || []).reduce((peak, stage) => {
    const target = Number(stage.target_vusers)
    return Number.isFinite(target) ? Math.max(peak, target) : peak
  }, 0)
}

export function effectiveGroupVUsers(group: GroupDemand): number {
  const stagePeak = stagePeakVUsers(group.stages)
  return stagePeak > 0 ? stagePeak : Number(group.vusers) || 0
}

export function validateAggregateVUserDemand(groups: GroupDemand[], label = 'Test'): string | undefined {
  for (let groupIndex = 0; groupIndex < groups.length; groupIndex += 1) {
    const group = groups[groupIndex]
    if (!Number.isSafeInteger(Number(group.vusers)) || Number(group.vusers) < 0) {
      return `${label}: group ${groupIndex + 1} VUsers must be a non-negative whole number.`
    }
    for (let stageIndex = 0; stageIndex < (group.stages || []).length; stageIndex += 1) {
      const target = group.stages?.[stageIndex].target_vusers
      if (target !== undefined && (!Number.isSafeInteger(Number(target)) || Number(target) < 0)) {
        return `${label}: group ${groupIndex + 1} stage ${stageIndex + 1} target VUsers must be a non-negative whole number.`
      }
    }
    const stagePeak = stagePeakVUsers(group.stages)
    if (stagePeak > 0 && Number(group.vusers) !== stagePeak) {
      return `${label}: group ${groupIndex + 1} VUsers must match its peak stage target of ${stagePeak}.`
    }
  }
  const total = groups.reduce((sum, group) => sum + effectiveGroupVUsers(group), 0)
  if (!Number.isSafeInteger(total) || total > LOAD_GROUP_MAX_VUSERS) {
    return `${label}: aggregate peak demand cannot exceed ${LOAD_GROUP_MAX_VUSERS.toLocaleString()} VUsers.`
  }
  return undefined
}

export function validateGroupExecutionEnvelope({
  role,
  protocol,
  vusers,
  label,
}: {
  role: GroupRole
  protocol?: string
  vusers: number
  label: string
}): string | undefined {
  if (role === 'observer' && protocol?.trim().toLowerCase() !== 'browser') {
    return `${label}: observer canaries require a browser-protocol script.`
  }
  const maximum = maxVUsersForRole(role)
  if (!Number.isSafeInteger(vusers)) {
    return `${label}: VUsers must be a positive whole number.`
  }
  if (vusers < 1 || vusers > maximum) {
    return role === 'observer'
      ? `${label}: observer canaries must use between 1 and ${OBSERVER_MAX_VUSERS} VUsers.`
      : `${label}: load groups must use between 1 and ${LOAD_GROUP_MAX_VUSERS.toLocaleString()} VUsers.`
  }
  return undefined
}
