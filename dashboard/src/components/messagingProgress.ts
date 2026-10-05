import type { MessagingDurationSummary, MessagingProgressPosition, MessagingProgressRow, MessagingProgressSnapshot } from '../types'

const maxSequence = 9223372036854775807n
function decimal(value: string | undefined): bigint | undefined {
  if (!value || value.length > 19 || !/^(0|[1-9][0-9]*)$/.test(value)) return undefined
  const parsed = BigInt(value)
  return parsed <= maxSequence ? parsed : undefined
}
function fresh(age: number | undefined): boolean {
  return age !== undefined && Number.isSafeInteger(age) && age >= 0 && age <= 30000
}
// Source-monotonic ages qualify only the captured observation. No remote wall
// clock is treated as proof of freshness at the controller or browser.
export function capturedSequenceLag(snapshot: MessagingProgressSnapshot, row: MessagingProgressRow, cursor: 'delivered' | 'checkpoint'): string {
  const position = cursor === 'checkpoint' ? row.checkpoint_position : row.delivered_position
  if (snapshot.version !== 1 || snapshot.incomplete || row.incomplete || row.ownership !== 'owned' || !row.inventory_complete || !fresh(row.ownership_verified_age_ms) || !fresh(row.tail_position.observation_age_ms) || !fresh(position.observation_age_ms) || row.tail_position.state !== 'observed' || position.state !== 'observed' || (cursor === 'checkpoint' && row.checkpoint.in_flight > 0)) return 'Unavailable'
  const tail = decimal(row.tail_position.sequence)
  const at = decimal(position.sequence)
  if (tail === undefined || at === undefined || tail < at) return 'Unavailable'
  return (tail - at).toString()
}
export function positionText(position: MessagingProgressPosition): string {
  if (position.state !== 'observed') return decimal(position.sequence) === undefined ? position.state : `${position.state} (last observed ${position.sequence})`
  return decimal(position.sequence) === undefined ? 'Unavailable' : `${position.sequence}${position.provider_offset ? ` (provider offset ${position.provider_offset})` : ''}`
}

export function checkpointTiming(duration: MessagingDurationSummary): string {
  const distribution = duration.distribution_ms
  if (distribution.count === 0) return `Unavailable (${duration.eligible} eligible operations; 0 observed)`
  return `${(distribution.sum / distribution.count).toFixed(3)} ms mean; ${distribution.count}/${duration.eligible} operations observed`
}
