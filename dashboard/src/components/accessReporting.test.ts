import { describe, expect, it } from 'vitest'
import { csvBlob, reviewPriority, activityDateError, activitySnapshotBefore } from './accessReporting'
import { accessHref, ACCESS_VIEWS } from './accessDestinations'
import { parseAppViewFromHash, viewToLocationHash } from '../appRouting'

describe('Access reporting and destinations', () => {
  it('quotes CSV fields and neutralizes spreadsheet formulas', async () => {
    const text = await csvBlob([['a,"b', '=SUM(A1)', '  +1', '\t@evil', 'safe', 12, null]]).text()
    expect(text).toContain('"a,""b"')
    expect(text).toContain('"\'=SUM(A1)"')
    expect(text).toContain('"\'  +1"')
    expect(text).toContain('"\'\t@evil"')
    expect(text).toContain('"safe","12",""')
  })
  it('distinguishes actionable and informational review findings', () => {
    expect(reviewPriority(['disabled', 'expired'])).toBe('Informational')
    expect(reviewPriority(['overdue_rotation'])).toBe('Action needed')
    expect(reviewPriority(['never_used'])).toBe('Review recommended')
  })
  it('freezes future event dates at the history snapshot', () => {
    const snapshot='2026-03-01T00:00:00.000Z'
    expect(activitySnapshotBefore('',snapshot)).toBe(snapshot)
    expect(activitySnapshotBefore('2026-04-01T00:00:00Z',snapshot)).toBe(snapshot)
    expect(activitySnapshotBefore('2026-02-01T00:00:00Z',snapshot)).toBe('2026-02-01T00:00:00.000Z')
  })
  it('validates optional date ranges', () => {
    expect(activityDateError('', '')).toBe('')
    expect(activityDateError('invalid', '')).toBe('Enter valid dates.')
    expect(activityDateError('2026-02-02', '2026-01-01')).toContain('start date')
  })
  it.each(ACCESS_VIEWS)('roundtrips %s and preserves workspace scope', (view) => {
    const href = accessHref(
      view,
      '#/settings?projectId=a%2Fb&applicationId=app&releaseId=rel&setting=features',
    )
    const route = parseAppViewFromHash(href)
    expect(route).toMatchObject({
      page: 'access',
      accessView: view,
      projectId: 'a/b',
      applicationId: 'app',
      releaseId: 'rel',
    })
    expect(parseAppViewFromHash(viewToLocationHash(route))).toEqual(route)
    expect(href).not.toContain('setting=')
  })
  it('ignores invalid Access views', () => {
    expect(parseAppViewFromHash('#/access?accessView=unknown')).toEqual({ page: 'access' })
  })
})
