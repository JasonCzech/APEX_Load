export interface ProportionalLoadGroup {
  name: string
  selected: boolean
  role?: 'load' | 'observer'
  vusers: number
}

export function proportionalVUserTargets(groups: ProportionalLoadGroup[], factor: number) {
  const eligible = groups.filter(group => group.selected && group.role !== 'observer')
  const desiredTotal = Math.max(0, Math.round(eligible.reduce((sum, group) => sum + group.vusers, 0) * factor))
  const allocations = eligible.map(group => {
    const exact = Math.max(0, group.vusers * factor)
    return { name: group.name, value: Math.floor(exact), remainder: exact - Math.floor(exact) }
  })
  let remaining = desiredTotal - allocations.reduce((sum, allocation) => sum + allocation.value, 0)
  allocations.sort((left, right) => right.remainder - left.remainder || (left.name < right.name ? -1 : left.name > right.name ? 1 : 0))
  for (let index = 0; index < allocations.length && remaining > 0; index++, remaining--) {
    allocations[index].value++
  }
  return new Map(allocations.map(allocation => [allocation.name, allocation.value]))
}

export function runtimeAdjustmentTargets(groups: Array<{ name: string; target_vusers: number; target_tps?: number }>) {
  return Object.fromEntries(groups.map(group => [group.name, group.target_tps ?? group.target_vusers]))
}
