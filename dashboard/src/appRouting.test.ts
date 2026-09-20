import { describe, expect, it } from 'vitest'

import { terminalRunReportView, APP_HISTORY_INDEX_KEY, handleGuardedRouteChange, mergeAppViewWithWorkspace, navPageForAppView, normalizeReportViewForFeatureFlag, parseAppViewFromHash, parseDesktopDeepLink, projectAssetForAppView, readAppHistoryIndex, recoveryViewForUnscopedProjectAsset, resetProjectScopedAssetView, scopeAppViewToProject, scopeAppViewToWorkspace, testIdForAppView, unscopedProjectAssetForAppView, viewToLocationHash, withAppHistoryIndex, workspaceScopeForAppView, type AppView } from './appRouting'

describe('appRouting', () => {
  it('round-trips analysis baseline and population into a transaction report', () => {
    const view: AppView = {page: 'report', testId: 'run-2', section: 'performance', lens: 'engineer', compare: 'run-1', transaction: 'Checkout & pay', population: 'transaction'}
    expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
    expect(parseAppViewFromHash('#/report/run-2?population=invalid')).toEqual({page: 'report', testId: 'run-2'})
  })

  it('maps run workspaces to the sidebar area that owns their current flow', () => {
    for (const tab of [undefined, 'overview', 'control', 'graphs', 'diagnostics'] as const) {
      expect(navPageForAppView({ page: 'test', testId: 'run-1', ...(tab ? { tab } : {}) })).toBe('command-center')
    }
    for (const tab of ['archive', 'action-summary', 'load-impact', 'transaction-drilldown'] as const) {
      expect(navPageForAppView({ page: 'test', testId: 'run-1', tab })).toBe('results')
    }
    expect(navPageForAppView({ page: 'report', testId: 'run-1' })).toBe('results')
    expect(navPageForAppView({ page: 'tests' })).toBe('tests')
  })

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

  it.each([
    { page: 'dashboard' },
    { page: 'repository' },
    { page: 'tests', scenarioView: 'plans' },
    { page: 'results' },
    { page: 'monitoring', category: 'telemetry' },
    { page: 'fleet' },
    { page: 'schedules' },
    { page: 'docs', guidePage: 'mcp' },
    { page: 'access' },
    { page: 'settings' },
  ] as AppView[])('round-trips project, application, and release scope for generic route $page', (view) => {
    const scoped = scopeAppViewToWorkspace(view, {
      projectId: 'project/1',
      applicationId: 'application/2',
      releaseId: 'release/3',
    })

    expect(parseAppViewFromHash(viewToLocationHash(scoped))).toEqual(scoped)
  })

  it('accepts legacy workspace parameter aliases without adding scope to unscoped links', () => {
    expect(parseAppViewFromHash('#/results?project=project-a&application=app-a&release=release-a')).toEqual({
      page: 'results',
      projectId: 'project-a',
      applicationId: 'app-a',
      releaseId: 'release-a',
    })
    expect(parseAppViewFromHash('#/results')).toEqual({ page: 'results' })
    expect(viewToLocationHash({ page: 'results' })).toBe('#/results')
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
    expect(parseAppViewFromHash(viewToLocationHash({
      page: 'new-test',
      scenarioId: 'scenario/1',
      projectId: 'project/2',
    }))).toEqual({ page: 'new-test', scenarioId: 'scenario/1', projectId: 'project/2' })
    expect(parseAppViewFromHash(viewToLocationHash({
      page: 'scripts',
      mode: 'edit',
      scriptId: 'script/1',
      projectId: 'project/2',
    }))).toEqual({ page: 'scripts', mode: 'edit', scriptId: 'script/1', projectId: 'project/2' })
    expect(parseAppViewFromHash(viewToLocationHash({
      page: 'test',
      testId: 'run/1',
      tab: 'archive',
      projectId: 'project/2',
    }))).toEqual({ page: 'test', testId: 'run/1', tab: 'archive', projectId: 'project/2' })
    expect(parseAppViewFromHash(viewToLocationHash({
      page: 'command-center',
      testId: 'run/1',
      projectId: 'project/2',
    }))).toEqual({ page: 'command-center', testId: 'run/1', projectId: 'project/2' })
    expect(viewToLocationHash({ page: 'swagger' })).toBe('#/swagger')
    expect(viewToLocationHash({ page: 'docs' })).toBe('#/docs')
    expect(viewToLocationHash({ page: 'docs', guidePage: 'general' })).toBe('#/docs?guide=general')
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
    expect(viewToLocationHash({ page: 'recorder-panel', sessionId: 'brec/1', folderId: 'folder/9' })).toBe('#/recorder-panel?session=brec%2F1&folderId=folder%2F9')
    expect(parseAppViewFromHash('#/recorder-panel?session=brec-1&folderId=folder-9')).toEqual({ page: 'recorder-panel', sessionId: 'brec-1', folderId: 'folder-9' })
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
    expect(normalizeReportViewForFeatureFlag({ page: 'report', testId: 'run-1', projectId: 'project-2' }, false)).toEqual({
      page: 'test',
      testId: 'run-1',
      tab: 'archive',
      projectId: 'project-2',
    })
    expect(normalizeReportViewForFeatureFlag({
      page: 'report',
      testId: 'run-1',
      projectId: 'project-2',
      applicationId: 'app-2',
      releaseId: 'release-2',
    }, false)).toEqual({
      page: 'test',
      testId: 'run-1',
      tab: 'archive',
      projectId: 'project-2',
      applicationId: 'app-2',
      releaseId: 'release-2',
    })
  })

  it('replaces stale child scope when a view is moved to another workspace', () => {
    const original: AppView = {
      page: 'results',
      projectId: 'project-a',
      applicationId: 'app-a',
      releaseId: 'release-a',
    }

    expect(scopeAppViewToWorkspace(original, { projectId: 'project-b' })).toEqual({
      page: 'results',
      projectId: 'project-b',
    })
    expect(workspaceScopeForAppView(scopeAppViewToWorkspace(original, {
      projectId: 'project-b',
      applicationId: 'app-b',
    }))).toEqual({
      projectId: 'project-b',
      applicationId: 'app-b',
    })

    expect(mergeAppViewWithWorkspace({ page: 'results' }, workspaceScopeForAppView(original))).toEqual(original)
    expect(mergeAppViewWithWorkspace({ page: 'results', projectId: 'project-b' }, workspaceScopeForAppView(original))).toEqual({
      page: 'results',
      projectId: 'project-b',
    })
    expect(mergeAppViewWithWorkspace({
      page: 'results',
      projectId: 'project-a',
      applicationId: 'app-b',
    }, workspaceScopeForAppView(original))).toEqual({
      page: 'results',
      projectId: 'project-a',
      applicationId: 'app-b',
    })
  })

  it('clears stale asset identifiers when the user changes project scope', () => {
    expect(resetProjectScopedAssetView({ page: 'tests', scenarioView: 'builder' }, 'project-b')).toEqual({
      page: 'tests',
      scenarioView: 'library',
    })
    expect(resetProjectScopedAssetView({ page: 'scripts', mode: 'edit', scriptId: 'script-a', projectId: 'project-a' }, 'project-b')).toEqual({
      page: 'scripts',
      projectId: 'project-b',
    })
    expect(resetProjectScopedAssetView({ page: 'new-test', scenarioId: 'scenario-a' }, 'project-b')).toEqual({
      page: 'new-test',
      projectId: 'project-b',
    })
    expect(resetProjectScopedAssetView({ page: 'command-center', testId: 'run-a' }, 'project-b')).toEqual({
      page: 'command-center',
      projectId: 'project-b',
    })
    expect(resetProjectScopedAssetView({ page: 'test', testId: 'run-a' }, 'project-b')).toEqual({
      page: 'command-center',
      projectId: 'project-b',
    })
    expect(resetProjectScopedAssetView({ page: 'test', testId: 'run-a', tab: 'diagnostics' }, 'project-b')).toEqual({
      page: 'command-center',
      projectId: 'project-b',
    })
    expect(resetProjectScopedAssetView({ page: 'test', testId: 'run-a', tab: 'archive' }, 'project-b')).toEqual({ page: 'results' })
    expect(resetProjectScopedAssetView({ page: 'test', testId: 'run-a', tab: 'load-impact' }, 'project-b')).toEqual({ page: 'results' })
    expect(resetProjectScopedAssetView({ page: 'report', testId: 'run-a' }, 'project-b')).toEqual({ page: 'results' })
  })

  it.each([
    [{ page: 'scripts', mode: 'edit', scriptId: 'script-a' }, { kind: 'script', id: 'script-a' }],
    [{ page: 'new-test', scenarioId: 'scenario-a' }, { kind: 'scenario', id: 'scenario-a' }],
    [{ page: 'test', testId: 'run-a' }, { kind: 'test', id: 'run-a' }],
    [{ page: 'report', testId: 'run-a' }, { kind: 'test', id: 'run-a' }],
    [{ page: 'command-center', testId: 'run-a' }, { kind: 'test', id: 'run-a' }],
  ] as Array<[AppView, { kind: 'script' | 'scenario' | 'test'; id: string }]>) (
    'identifies the owner lookup required for a legacy %s route',
    (view, expectedAsset) => {
      expect(unscopedProjectAssetForAppView(view)).toEqual(expectedAsset)
      expect(unscopedProjectAssetForAppView(scopeAppViewToProject(view, 'project-b'))).toBeNull()
    },
  )

  it('does not resolve ownership for project-scoped routes or asset-free workspaces', () => {
    expect(unscopedProjectAssetForAppView({ page: 'scripts' })).toBeNull()
    expect(unscopedProjectAssetForAppView({ page: 'new-test' })).toBeNull()
    expect(unscopedProjectAssetForAppView({ page: 'command-center' })).toBeNull()
    expect(unscopedProjectAssetForAppView({ page: 'test', testId: 'run-a', projectId: 'project-b' })).toBeNull()
  })

  it('retains asset identity for validating already project-scoped routes', () => {
    expect(projectAssetForAppView({ page: 'scripts', mode: 'edit', scriptId: 'script-a', projectId: 'project-a' })).toEqual({ kind: 'script', id: 'script-a' })
    expect(projectAssetForAppView({ page: 'new-test', scenarioId: 'scenario-a', projectId: 'project-a' })).toEqual({ kind: 'scenario', id: 'scenario-a' })
    expect(projectAssetForAppView({ page: 'new-test', projectId: 'project-a' })).toBeNull()
  })

  it('identifies run routes that require application and release reconciliation', () => {
    expect(testIdForAppView({ page: 'test', testId: 'run-a', projectId: 'project-a' })).toBe('run-a')
    expect(testIdForAppView({ page: 'report', testId: 'run-b', projectId: 'project-b' })).toBe('run-b')
    expect(testIdForAppView({ page: 'command-center', testId: 'run-c', projectId: 'project-c' })).toBe('run-c')
    expect(testIdForAppView({ page: 'command-center' })).toBeUndefined()
    expect(testIdForAppView({ page: 'results' })).toBeUndefined()
  })

  it('provides a safe workspace recovery route for every legacy asset kind', () => {
    expect(recoveryViewForUnscopedProjectAsset({ kind: 'script', id: 'script-a' })).toEqual({ page: 'scripts' })
    expect(recoveryViewForUnscopedProjectAsset({ kind: 'scenario', id: 'scenario-a' })).toEqual({ page: 'tests' })
    expect(recoveryViewForUnscopedProjectAsset({ kind: 'test', id: 'run-a' })).toEqual({ page: 'results' })
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

  it('treats a workspace-only hash change as a real history transition', () => {
    const applied: AppView[] = []

    handleGuardedRouteChange({
      currentView: {
        page: 'results',
        projectId: 'project-a',
        applicationId: 'app-a',
        releaseId: 'release-a',
      },
      locationHash: '#/results?projectId=project-b&applicationId=app-b&releaseId=release-b',
      hasDirtyGuard: () => false,
      runGuarded: () => undefined,
      restoreHash: () => undefined,
      applyView: (view) => applied.push(view),
    })

    expect(applied).toEqual([{
      page: 'results',
      projectId: 'project-b',
      applicationId: 'app-b',
      releaseId: 'release-b',
    }])
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

it('round trips report evidence sections and rejects unknown section names', () => {
  const view = parseAppViewFromHash('#/report/run-1?section=evidence&lens=audit')
  expect(view).toMatchObject({ page: 'report', testId: 'run-1', section: 'evidence', lens: 'audit' })
  expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
  expect(parseAppViewFromHash('#/report/run-1?section=unknown')).not.toHaveProperty('section')
})

describe('terminal run route compatibility', () => {
  it.each(['COMPLETE', 'FAILED', 'ABORTED'])('keeps %s runs inside Results regardless of legacy entry point', status => {
    const scope = { projectId: 'p', applicationId: 'a', releaseId: 'r' }
    expect(terminalRunReportView({ page: 'command-center', testId: 'run', ...scope }, status, true)).toEqual({ page: 'report', testId: 'run', ...scope, section: 'execution' })
    for (const [tab, section] of [['overview', 'execution'], ['control', 'execution'], ['graphs', 'performance'], ['diagnostics', 'diagnostics'], ['archive', 'evidence'], ['action-summary', 'performance'], ['load-impact', 'performance'], ['transaction-drilldown', 'diagnostics']] as const) {
      expect(terminalRunReportView({ page: 'test', testId: 'run', tab, ...scope }, status, true)).toEqual({ page: 'report', testId: 'run', ...scope, section })
    }
  })
  it('preserves live controls, the command-center landing page, and the explicit feature flag', () => {
    const view: AppView = { page: 'command-center', testId: 'live' }
    for (const status of ['RUNNING', 'PAUSED', 'STOPPING', 'QUEUED', 'UNKNOWN']) expect(terminalRunReportView(view, status, true)).toBe(view)
    expect(terminalRunReportView(view, 'FAILED', false)).toBe(view)
    const landing: AppView = { page: 'command-center' }
    expect(terminalRunReportView(landing, 'COMPLETE', true)).toBe(landing)
  })
  it('round trips the Execution section and keeps Results selected', () => {
    const view: AppView = { page: 'report', testId: 'run', section: 'execution' }
    expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
    expect(navPageForAppView(view)).toBe('results')
  })
})


it('round trips Help articles, section links, and API searches through workspace canonicalization', () => {
  for (const hash of ['#/docs?guide=user-guide&article=scenarios&section=sizing-from-a-goal&projectId=p', '#/swagger?search=script%20validate&projectId=p']) {
    const view = parseAppViewFromHash(hash)
    expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
  }
  expect(parseAppViewFromHash('#/docs?guide=user-guide&article=scenarios&section=sizing-from-a-goal')).toMatchObject({ guideArticle: 'scenarios', guideSection: 'sizing-from-a-goal' })
  expect(parseAppViewFromHash('#/swagger?search=script%20validate')).toMatchObject({ search: 'script validate' })
})
