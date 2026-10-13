import { describe, expect, it } from 'vitest'
import { SETTINGS_DESTINATIONS, settingsDestination, settingsHref, settingsOwner, settingsPageDestination, workspaceHref } from './settingsNavigation'
import { parseAppViewFromHash, viewToLocationHash } from '../appRouting'

describe('settings task navigation', () => {
  it('sends test data, live tables and project secrets to Shared assets, still found by name', () => {
    expect(settingsOwner('live-tables')).toBe('shared-assets')
    expect(parseAppViewFromHash(settingsHref('live-tables', '#/repository?projectId=p'))).toEqual({ page: 'shared-assets', section: 'live-tables', projectId: 'p' })
    expect(parseAppViewFromHash(settingsHref('test-data', '#/tests?projectId=p', 'pool-1'))).toEqual({ page: 'shared-assets', section: 'test-data', resourceId: 'pool-1', projectId: 'p' })
    expect(parseAppViewFromHash(settingsHref('secrets', '#/access?projectId=p'))).toEqual({ page: 'shared-assets', section: 'secrets', projectId: 'p' })
    expect(settingsDestination('test-data')?.title).toBe('Shared data pools')
    expect(settingsPageDestination('secrets')).toBeUndefined()
    expect(settingsPageDestination('redaction')?.id).toBe('redaction')
  })
  it('gives every destination one address with preserved workspace context', () => {
    expect(new Set(SETTINGS_DESTINATIONS.map(item => item.id)).size).toBe(SETTINGS_DESTINATIONS.length)
    for (const destination of SETTINGS_DESTINATIONS.filter(item => !item.sharedAssets)) {
      const hash = settingsHref(destination.id, '#/fleet?projectId=p&applicationId=a&releaseId=r&category=telemetry')
      expect(parseAppViewFromHash(hash)).toEqual({ page: settingsOwner(destination.id), setting: destination.id, projectId: 'p', applicationId: 'a', releaseId: 'r', returnTo: '#/fleet?projectId=p&applicationId=a&releaseId=r&category=telemetry' })
      expect(viewToLocationHash(parseAppViewFromHash(hash))).toBe(hash)
      expect(new URLSearchParams(hash.split('?')[1]).has('category')).toBe(false)
    }
  })
  it('keeps standing allocations separate from schedule defaults and handles invalid destinations', () => {
    expect(settingsDestination('allocations')?.subsection).toBe('reservations')
    expect(settingsDestination('scheduling')?.governance).toBe('schedule-defaults')
    expect(settingsDestination('invalid')).toBeUndefined()
    expect(settingsHref('invalid', '')).toBe('#/settings?setting=platform')
  })
  it.each(['#/dashboard?projectId=p', '#/schedules?projectId=p', '#/test/run-1?tab=control', '#/command-center?testId=run-1'])('preserves workflow and exact resource from %s', origin => {
    const hash = settingsHref('pools', origin, 'pool/a & b')
    const view = parseAppViewFromHash(hash)
    expect(view).toMatchObject({ page: 'settings', setting: 'pools', resourceId: 'pool/a & b', returnTo: origin })
    expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
  })
  it('rejects external return destinations', () => {
    expect(parseAppViewFromHash('#/settings?setting=grafana&returnTo=https%3A%2F%2Fevil.test')).toEqual({ page: 'settings', setting: 'grafana' })
  })
  it('round trips alert monitoring routes', () => {
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'monitoring', category: 'alerts' }))).toEqual({ page: 'monitoring', category: 'alerts' })
  })
})

describe('operational workspace handoffs', () => {
  it('preserves hierarchy and drops settings-only state', () => {
    const hash = '#/settings?setting=allocations&projectId=p&applicationId=a&releaseId=r&resourceId=pool'
    expect(workspaceHref('schedules', hash, 'p')).toBe('#/schedules?projectId=p&applicationId=a&releaseId=r')
    expect(workspaceHref('fleet', hash)).toBe('#/fleet?projectId=p&applicationId=a&releaseId=r')
  })
  it('does not attach children from a different project or invent an empty scope', () => {
    expect(workspaceHref('schedules', '#/settings?projectId=old&applicationId=a&releaseId=r', 'new')).toBe('#/schedules?projectId=new')
    expect(workspaceHref('fleet', '#/settings')).toBe('#/fleet')
    expect(workspaceHref('tests', '#/new-test?projectId=p&applicationId=a&releaseId=r&scenarioId=s', undefined, { view: 'plans' })).toBe('#/tests?view=plans&projectId=p&applicationId=a&releaseId=r')
  })
})

 it('distinguishes project target-system connections from platform graph configuration', () => {
   expect(settingsDestination('prometheus-capture')).toMatchObject({ scope: 'project', title: 'Prometheus test monitoring' })
   expect(settingsDestination('prometheus')).toMatchObject({ scope: 'deployment', title: 'APEX platform graphs' })
 })
