import { describe, expect, it } from 'vitest'

import { APP_HISTORY_INDEX_KEY, handleGuardedRouteChange, normalizeReportViewForFeatureFlag, parseAppViewFromHash, parseDesktopDeepLink, readAppHistoryIndex, viewToLocationHash, withAppHistoryIndex, type AppView } from './appRouting'

describe('appRouting', () => {
  it('defaults empty and invalid hashes to the dashboard', () => {
    expect(parseAppViewFromHash('')).toEqual({ page: 'dashboard' })
    expect(parseAppViewFromHash('#/unknown')).toEqual({ page: 'dashboard' })
  })

  it('parses refreshable top-level app pages', () => {
    expect(parseAppViewFromHash('#/results')).toEqual({ page: 'results' })
    expect(parseAppViewFromHash('#/fleet')).toEqual({ page: 'fleet' })
    expect(parseAppViewFromHash('#/monitoring?category=telemetry')).toEqual({ page: 'monitoring', category: 'telemetry' })
    expect(parseAppViewFromHash('#/monitoring?category=not-real')).toEqual({ page: 'monitoring' })
    expect(parseAppViewFromHash('#/test/run-1?tab=archive')).toEqual({ page: 'test', testId: 'run-1', tab: 'archive' })
    expect(parseAppViewFromHash('#/test/run-1?tab=not-real')).toEqual({ page: 'test', testId: 'run-1' })
    expect(parseAppViewFromHash('#/command-center')).toEqual({ page: 'command-center' })
    expect(parseAppViewFromHash('#/command-center/run-1')).toEqual({ page: 'command-center', testId: 'run-1' })
    expect(parseAppViewFromHash('#/command-center?testId=run-2')).toEqual({ page: 'command-center', testId: 'run-2' })
    expect(parseAppViewFromHash('#/report/run-1?lens=engineer&compare=run-0')).toEqual({ page: 'report', testId: 'run-1', lens: 'engineer', compare: 'run-0' })
    expect(parseAppViewFromHash('#/report/run-1?lens=not-real')).toEqual({ page: 'report', testId: 'run-1' })
    expect(parseAppViewFromHash('#/new-test')).toEqual({ page: 'new-test' })
    expect(parseAppViewFromHash('#/tests')).toEqual({ page: 'tests' })
    expect(parseAppViewFromHash('#/tests?view=builder')).toEqual({ page: 'tests', scenarioView: 'builder' })
    expect(parseAppViewFromHash('#/docs')).toEqual({ page: 'docs' })
    expect(parseAppViewFromHash('#/docs?guide=authentication')).toEqual({ page: 'docs', guidePage: 'authentication' })
    expect(parseAppViewFromHash('#/docs?guide=not-real')).toEqual({ page: 'docs' })
    expect(parseAppViewFromHash('#/repository')).toEqual({ page: 'repository' })
    expect(parseAppViewFromHash('#/swagger')).toEqual({ page: 'swagger' })
    expect(parseAppViewFromHash('#/repository-guide')).toEqual({ page: 'repository-guide' })
  })

  it('round-trips deep links for runs, scenario launches, and scripts', () => {
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'test', testId: 'run/123' }))).toEqual({
      page: 'test',
      testId: 'run/123',
    })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'test', testId: 'run/123', tab: 'archive' }))).toEqual({
      page: 'test',
      testId: 'run/123',
      tab: 'archive',
    })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'report', testId: 'run/123', lens: 'audit', compare: 'baseline/1' }))).toEqual({
      page: 'report',
      testId: 'run/123',
      lens: 'audit',
      compare: 'baseline/1',
    })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'new-test', scenarioId: 'scenario-1' }))).toEqual({
      page: 'new-test',
      scenarioId: 'scenario-1',
    })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'new-test', prefillStartLocal: '2026-08-15T09:00' }))).toEqual({
      page: 'new-test',
      prefillStartLocal: '2026-08-15T09:00',
    })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'tests', scenarioView: 'builder' }))).toEqual({
      page: 'tests',
      scenarioView: 'builder',
    })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'scripts', mode: 'edit', scriptId: 'script-1' }))).toEqual({
      page: 'scripts',
      mode: 'edit',
      scriptId: 'script-1',
    })
    expect(viewToLocationHash({ page: 'swagger' })).toBe('#/swagger')
    expect(viewToLocationHash({ page: 'docs' })).toBe('#/docs')
    expect(viewToLocationHash({ page: 'docs', guidePage: 'general' })).toBe('#/docs')
    expect(viewToLocationHash({ page: 'docs', guidePage: 'mcp' })).toBe('#/docs?guide=mcp')
    expect(viewToLocationHash({ page: 'fleet' })).toBe('#/fleet')
    expect(viewToLocationHash({ page: 'monitoring', category: 'telemetry' })).toBe('#/monitoring?category=telemetry')
    expect(viewToLocationHash({ page: 'command-center' })).toBe('#/command-center')
    expect(viewToLocationHash({ page: 'test', testId: 'run/123', tab: 'archive' })).toBe('#/test/run%2F123?tab=archive')
    expect(viewToLocationHash({ page: 'command-center', testId: 'run/123' })).toBe('#/command-center/run%2F123')
    expect(viewToLocationHash({ page: 'report', testId: 'run/123', lens: 'executive', compare: 'base/123' })).toBe('#/report/run%2F123?lens=executive&compare=base%2F123')
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'repository-guide' }))).toEqual({ page: 'repository-guide' })
    expect(viewToLocationHash({ page: 'recorder-panel', sessionId: 'brec/1' })).toBe('#/recorder-panel?session=brec%2F1')
    expect(parseAppViewFromHash('#/recorder-panel?session=brec-1')).toEqual({ page: 'recorder-panel', sessionId: 'brec-1' })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'recorder-panel', sessionId: 'brec/1' }))).toEqual({
      page: 'recorder-panel',
      sessionId: 'brec/1',
    })
    expect(parseAppViewFromHash('#/recorder-panel')).toEqual({ page: 'scripts' })
  })

  it('parses allowlisted desktop path, hash, and route-query deep links', () => {
    expect(parseDesktopDeepLink('apexload://scripts?mode=create')).toEqual({ page: 'scripts', mode: 'create' })
    expect(parseDesktopDeepLink('apexload:///test/run-1?tab=archive')).toEqual({ page: 'test', testId: 'run-1', tab: 'archive' })
    expect(parseDesktopDeepLink('apexload://open#/results')).toEqual({ page: 'results' })
    expect(parseDesktopDeepLink('apexload://open?route=%23%2Fcommand-center%2Frun-2')).toEqual({ page: 'command-center', testId: 'run-2' })
  })

  it('rejects unsupported desktop schemes, commands, and route-query hosts', () => {
    expect(parseDesktopDeepLink('https://example.test/#/results')).toBeNull()
    expect(parseDesktopDeepLink('apexload://unknown-command')).toBeNull()
    expect(parseDesktopDeepLink('apexload://evil.example?route=%23%2Fresults')).toBeNull()
    expect(parseDesktopDeepLink('apexload://open?route=https%3A%2F%2Fevil.example')).toBeNull()
  })

  it('stores a bounded internal index without discarding other history state', () => {
    const state = withAppHistoryIndex({ source: 'desktop' }, 3)

    expect(state).toEqual({ source: 'desktop', [APP_HISTORY_INDEX_KEY]: 3 })
    expect(readAppHistoryIndex(state)).toBe(3)
    expect(readAppHistoryIndex(withAppHistoryIndex(null, -4))).toBe(0)
    expect(readAppHistoryIndex({ [APP_HISTORY_INDEX_KEY]: 1.5 })).toBeNull()
  })

  it('falls direct report routes back to legacy archives when the report flag is disabled', () => {
    expect(normalizeReportViewForFeatureFlag({ page: 'report', testId: 'run-1', lens: 'audit' }, false)).toEqual({
      page: 'test',
      testId: 'run-1',
      tab: 'archive',
    })
    expect(normalizeReportViewForFeatureFlag({ page: 'report', testId: 'run-1' }, true)).toEqual({ page: 'report', testId: 'run-1' })
  })

  it('applies browser route changes immediately when no dirty guard is registered', () => {
    const applied: AppView[] = []
    let guarded = false

    handleGuardedRouteChange({
      currentView: { page: 'scripts', mode: 'edit', scriptId: 'script-1' },
      locationHash: '#/results',
      hasDirtyGuard: () => false,
      runGuarded: () => {
        guarded = true
      },
      restoreHash: () => {
        throw new Error('hash should not be restored')
      },
      applyView: (view) => applied.push(view),
    })

    expect(guarded).toBe(false)
    expect(applied).toEqual([{ page: 'results' }])
  })

  it('restores the current hash and waits for guard confirmation before applying browser route changes', () => {
    const applied: AppView[] = []
    const restored: string[] = []
    let proceed: (() => void) | undefined

    handleGuardedRouteChange({
      currentView: { page: 'scripts', mode: 'edit', scriptId: 'script-1' },
      locationHash: '#/results',
      hasDirtyGuard: () => true,
      runGuarded: (next) => {
        proceed = next
      },
      restoreHash: (hash) => restored.push(hash),
      applyView: (view) => applied.push(view),
    })

    expect(restored).toEqual(['#/scripts?mode=edit&scriptId=script-1'])
    expect(applied).toEqual([])

    proceed?.()
    expect(applied).toEqual([{ page: 'results' }])
  })
})
