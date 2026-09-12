type CalendarView = 'month' | 'week' | 'day'
type CalendarFilter = 'all' | 'cron' | 'ad_hoc' | 'execution_plan' | 'current'
export type CalendarPreferences = { view: CalendarView; filter: CalendarFilter; anchor: Date; showQueued: boolean }
const key = (project?: string) => `apexload.monitoring.calendar.${project || 'none'}`

export function readCalendarPreferences(project?: string): CalendarPreferences {
  const fallback: CalendarPreferences = { view: 'month', filter: 'all', anchor: new Date(), showQueued: true }
  try {
    const value = JSON.parse(sessionStorage.getItem(key(project)) || 'null')
    if (!value || typeof value !== 'object') return fallback
    const anchor = new Date(value.anchor)
    return {
      view: ['month', 'week', 'day'].includes(value.view) ? value.view : fallback.view,
      filter: ['all', 'cron', 'ad_hoc', 'execution_plan', 'current'].includes(value.filter) ? value.filter : fallback.filter,
      anchor: typeof value.anchor === 'string' && Number.isFinite(anchor.getTime()) ? anchor : fallback.anchor,
      showQueued: typeof value.showQueued === 'boolean' ? value.showQueued : true,
    }
  } catch { return fallback }
}

export function saveCalendarPreferences(project: string | undefined, value: CalendarPreferences) {
  try { sessionStorage.setItem(key(project), JSON.stringify(value)) } catch { /* Storage may be disabled. */ }
}

let pendingTab: string | null = null
export function requestMonitoringTabFocus(category: string) { pendingTab = category }
export function takeMonitoringTabFocus() {
  const result = pendingTab
  pendingTab = null
  return result
}
