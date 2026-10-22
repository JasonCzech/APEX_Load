// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { clearNewTestDraft, readNewTestDraft, saveNewTestDraft, type NewTestDraft } from './newTestDraft'
import { scenarioIdentity, targetOrigin } from './newTestPresentation'

export function draftFixture(): NewTestDraft {
  return { version: 1, projectId: 'project', applicationId: 'app', scenarioId: 'scenario', scenarioUpdatedAt: 'v1', pageMode: 'launch', runName: 'Smoke', releaseId: 'release', runDefaultOverrides: { duration: '1m', runtime_settings: { continue_on_error: false, pacing: null } }, groupRunOverrides: { one: { vusers: 20 } }, scheduledStartLocal: '', selectedTimeslotId: '', newReleaseName: '', showNewRelease: false, editingScheduleId: null,
    scheduleDraft: { name: 'Nightly', cron_expr: '@daily', timezone: 'UTC', release_id: '', timeslot_id: '', run_name_template: '', enabled: false, approval_policy: 'manual_review', missed_run_policy: 'skip', blackout_windows: [], maintenance_windows: [], availability_windows: [], disabled_from: '', disabled_until: '' } }
}
describe('New Test draft storage and presentation', () => {
  beforeEach(() => { window.sessionStorage.clear(); vi.restoreAllMocks() })
  it('round-trips explicit false/null overrides and cannot restore a different scope', () => {
    const draft = draftFixture()
    saveNewTestDraft(draft)
    expect(readNewTestDraft('project', 'app')).toEqual(draft)
    expect(readNewTestDraft('other', 'app')).toBeNull()
    expect(readNewTestDraft('project', 'other')).toBeNull()
    clearNewTestDraft('project', 'app')
    expect(readNewTestDraft('project', 'app')).toBeNull()
  })
  it.each(['{broken', 'null', '{"version":1}', JSON.stringify({ ...draftFixture(), runDefaultOverrides: {} }), JSON.stringify({ ...draftFixture(), groupRunOverrides: { one: { vusers: [] } } })])('ignores malformed stored data %s', value => {
    window.sessionStorage.setItem('apexload.newTest.draft.v1.project.app', value)
    expect(readNewTestDraft('project', 'app')).toBeNull()
  })
  it('reports storage failures instead of claiming a draft was saved', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('Storage blocked') })
    expect(() => saveNewTestDraft(draftFixture())).toThrow('Storage blocked')
  })
  it('distinguishes duplicate scenario names and redacts credential-bearing endpoints', () => {
    expect(scenarioIdentity({ name: 'Smoke', id: 'one' })).not.toBe(scenarioIdentity({ name: 'Smoke', id: 'two' }))
    expect(targetOrigin('https://user:password@example.com:8443/private?token=secret#key')).toBe('https://example.com:8443')
    expect(targetOrigin('{{env.URL}}/orders')).toBe('Dynamic target — resolved at runtime')
    expect(targetOrigin('/orders')).toBeNull()
    expect(targetOrigin(undefined)).toBeNull()
  })
})


describe('run policy selection drafts', () => {
  it('preserves immutable revisions and explicit project-default clears', () => {
    for (const analysisPolicySelections of [{}, { browser: { id: 'browser', revision: 2 }, journey: { id: 'journey', revision: 1 } }]) {
      const draft = { ...draftFixture(), analysisPolicySelections }
      saveNewTestDraft(draft)
      expect(readNewTestDraft('project', 'app')?.analysisPolicySelections).toEqual(analysisPolicySelections)
    }
  })
  it('refuses a malformed stored selection without restoring a default draft', () => {
    window.sessionStorage.setItem('apexload.newTest.draft.v1.project.app', JSON.stringify({ ...draftFixture(), analysisPolicySelections: { browser: { id: 'browser', revision: 0 } } }))
    expect(readNewTestDraft('project', 'app')).toBeNull()
  })
})
