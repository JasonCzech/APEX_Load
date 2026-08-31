import type { TrafficProfile, TrafficProfileMapping } from '../types'
import { parseDurationToSeconds } from '../utils/timeUtils'

export const trafficProfilePreviewPointLimit = 1000

function normalizedGroupName(value?: string): string {
  return (value || '').trim().toLowerCase()
}

export function trafficProfileMappingForGroup(profile: TrafficProfile | undefined, groupName?: string): TrafficProfileMapping | undefined {
  if (!profile || !groupName) return undefined
  const key = normalizedGroupName(groupName)
  return profile.mappings.find(mapping => normalizedGroupName(mapping.group_name) === key)
}

export function trafficProfileTargetAt(mapping: TrafficProfileMapping | undefined, elapsedSeconds: number): number {
  const points = mapping?.points || []
  if (!points.length || elapsedSeconds * 1000 < points[0].offset_ms) return 0
  const elapsedMilliseconds = elapsedSeconds * 1000
  let low = 0
  let high = points.length - 1
  while (low < high) {
    const middle = Math.ceil((low + high) / 2)
    if (points[middle].offset_ms <= elapsedMilliseconds) low = middle
    else high = middle - 1
  }
  return Math.max(0, Number(points[low].target) || 0)
}

export function trafficProfileTimelineSeconds(profile: TrafficProfile | undefined, limit = trafficProfilePreviewPointLimit): number[] {
  if (!profile || limit < 2) return []
  const durationSeconds = Math.max(0, parseDurationToSeconds(profile.target_duration))
  const offsets = new Set<number>([0, durationSeconds])
  const required = new Set<number>([0, durationSeconds])

  profile.mappings.forEach(mapping => {
    let minimum = mapping.points[0]
    let maximum = mapping.points[0]
    mapping.points.forEach(point => {
      const second = Math.max(0, Math.min(durationSeconds, point.offset_ms / 1000))
      offsets.add(second)
      if (!minimum || point.target < minimum.target) minimum = point
      if (!maximum || point.target > maximum.target) maximum = point
    })
    if (minimum) required.add(Math.max(0, Math.min(durationSeconds, minimum.offset_ms / 1000)))
    if (maximum) required.add(Math.max(0, Math.min(durationSeconds, maximum.offset_ms / 1000)))
  })

  const sorted = Array.from(offsets).sort((left, right) => left - right)
  if (sorted.length <= limit) return sorted

  const requiredSorted = Array.from(required).sort((left, right) => left - right)
  if (requiredSorted.length >= limit) {
    const result = new Set<number>()
    for (let index = 0; index < limit; index += 1) {
      const requiredIndex = Math.round((index * (requiredSorted.length - 1)) / (limit - 1))
      result.add(requiredSorted[requiredIndex])
    }
    return Array.from(result).sort((left, right) => left - right)
  }

  const result = new Set(requiredSorted)
  const candidates = sorted.filter(second => !result.has(second))
  const budget = Math.max(0, limit - result.size)
  for (let index = 0; index < budget; index += 1) {
    const candidateIndex = budget === 1 ? 0 : Math.round((index * (candidates.length - 1)) / (budget - 1))
    result.add(candidates[candidateIndex])
  }
  return Array.from(result).sort((left, right) => left - right)
}
