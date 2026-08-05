// Interpret datetime-local fields in the explicitly selected IANA timezone.
export function zonedLocalToISO(local: string, timezone: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(local.trim())
  if (!match) return ''
  const desired = [Number(match[1]), Number(match[2]), Number(match[3]), Number(match[4]), Number(match[5]), Number(match[6] || 0)]
  const wallClockUTC = Date.UTC(desired[0], desired[1] - 1, desired[2], desired[3], desired[4], desired[5] || 0)
  try {
    const formatter = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone.trim() || 'UTC',
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
    })
    let instant = wallClockUTC
    for (let attempt = 0; attempt < 3; attempt += 1) {
      const parts = Object.fromEntries(formatter.formatToParts(new Date(instant)).map(part => [part.type, part.value]))
      const represented = Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day), Number(parts.hour), Number(parts.minute), Number(parts.second))
      instant += wallClockUTC - represented
    }
    const finalParts = Object.fromEntries(formatter.formatToParts(new Date(instant)).map(part => [part.type, part.value]))
    const final = [finalParts.year, finalParts.month, finalParts.day, finalParts.hour, finalParts.minute, finalParts.second].map(Number)
    if (final.some((value, index) => value !== desired[index])) return ''
    return new Date(instant).toISOString()
  } catch {
    return ''
  }
}
