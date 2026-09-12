// @vitest-environment jsdom
import { beforeEach, expect, it } from 'vitest'
import { readCalendarPreferences, saveCalendarPreferences, requestMonitoringTabFocus, takeMonitoringTabFocus } from './monitoringWorkspaceModel'
beforeEach(() => sessionStorage.clear())
it('retains calendar context per project and tolerates corrupt preferences', () => {
 const value = { view: 'day' as const, filter: 'cron' as const, anchor: new Date('2026-09-10T12:00:00Z'), showQueued: false }
 saveCalendarPreferences('a', value)
 expect(readCalendarPreferences('a')).toEqual(value)
 expect(readCalendarPreferences('b').view).toBe('month')
 sessionStorage.setItem('apexload.monitoring.calendar.a', '{broken')
 expect(readCalendarPreferences('a').view).toBe('month')
 sessionStorage.setItem('apexload.monitoring.calendar.a', JSON.stringify({ view: 'bad', anchor: 'bad', showQueued: 'bad' }))
 expect(readCalendarPreferences('a').showQueued).toBe(true)
 expect(Number.isFinite(readCalendarPreferences('a').anchor.getTime())).toBe(true)
})
it('consumes a requested tab focus only once', () => {
 requestMonitoringTabFocus('telemetry')
 expect(takeMonitoringTabFocus()).toBe('telemetry')
 expect(takeMonitoringTabFocus()).toBeNull()
})
