import type { JMeterScenarioDraftGroup } from '../types/scriptImports'

export function messagingScheduleSummary(group: JMeterScenarioDraftGroup) {
  return (group.sampler_schedules || []).map(schedule => {
    const seconds = schedule.segments.reduce((sum, row) => sum + row.duration_seconds, 0)
    const planned = schedule.segments.reduce((sum, row) => sum + (row.start_rps + row.end_rps) * row.duration_seconds / 2, 0)
    const peak = Math.max(1, ...schedule.segments.flatMap(row => [row.start_rps, row.end_rps]))
    let elapsed = 0
    const points = schedule.segments.flatMap(row => {
      const start = `${10 + elapsed / seconds * 480},${90 - row.start_rps / peak * 80}`
      elapsed += row.duration_seconds
      return [start, `${10 + elapsed / seconds * 480},${90 - row.end_rps / peak * 80}`]
    }).join(' ')
    return { id: schedule.id, seconds, planned, peak, points }
  })
}
