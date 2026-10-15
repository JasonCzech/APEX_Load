import { describe, expect, it } from 'vitest'

import { terminalRunReportView, scriptsPageStep, scriptsPageStepDepth, APP_HISTORY_INDEX_KEY, handleGuardedRouteChange, mergeAppViewWithWorkspace, navPageForAppView, normalizeReportViewForFeatureFlag, parseAppViewFromHash, parseDesktopDeepLink, projectAssetForAppView, readAppHistoryIndex, recoveryViewForUnscopedProjectAsset, resetProjectScopedAssetView, scopeAppViewToProject, scopeAppViewToWorkspace, scriptsRouteForAuthoringState, testIdForAppView, unscopedProjectAssetForAppView, viewToLocationHash, withAppHistoryIndex, workspaceScopeForAppView, type AppView } from './appRouting'

describe('appRouting', () => {
  it('round-trips analysis baseline and population into a transaction report', () => {
    const view: AppView = {page: 'report', testId: 'run-2', section: 'performance', lens: 'engineer', compare: 'run-1', transaction: 'Checkout & pay', population: 'transaction'}
    expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
    expect(parseAppViewFromHash('#/report/run-2?population=invalid')).toEqual({page: 'report', testId: 'run-2'})
  })

  it('round-trips a root-cause investigation and drops malformed state', () => {
    const view: AppView = {page: 'report', testId: 'run-2', section: 'diagnostics', analysis: 'rca-0123abcd', rev: '3', hypothesis: 'hypothesis-89ab', window: '2026-10-05T12:00:00.000Z~2026-10-05T12:05:00.000Z'}
    expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
    expect(parseAppViewFromHash('#/report/run-2?analysis=%3Cscript%3E&rev=x1&hypothesis=a%20b&window=yesterday')).toEqual({page: 'report', testId: 'run-2'})
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
    expect(navPageForAppView({ page: 'fleet' })).toBe('monitoring')
    expect(navPageForAppView({ page: 'schedules' })).toBe('monitoring')
  })

  it('opens Fleet and Schedules from a monitoring category link', () => {
    expect(parseAppViewFromHash('#/monitoring?category=fleet')).toEqual({ page: 'fleet' })
    expect(parseAppViewFromHash('#/monitoring?category=schedules&projectId=p-1')).toEqual({ page: 'schedules', projectId: 'p-1' })
  })

  it('keeps the Command Center analysis view in the address', () => {
    expect(parseAppViewFromHash('#/command-center/run-1?view=wall')).toEqual({ page: 'command-center', testId: 'run-1', ccView: 'wall' })
    expect(parseAppViewFromHash('#/command-center?view=analysis')).toEqual({ page: 'command-center', ccView: 'analysis' })
    // Unknown views fall back to operations.
    expect(parseAppViewFromHash('#/command-center/run-1?view=bogus')).toEqual({ page: 'command-center', testId: 'run-1' })
    expect(viewToLocationHash({ page: 'command-center', testId: 'run-1', ccView: 'wall' })).toBe('#/command-center/run-1?view=wall')
    expect(viewToLocationHash({ page: 'command-center', testId: 'run-1' })).toBe('#/command-center/run-1')
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
    // A named scenario opens in the builder, at a known step; plans and the library name none.
    expect(parseAppViewFromHash('#/tests?view=builder&scenarioId=s1&step=data&projectId=p1')).toEqual({ page: 'tests', scenarioView: 'builder', scenarioId: 's1', scenarioStep: 'data', projectId: 'p1' })
    expect(parseAppViewFromHash('#/tests?scenarioId=s1&step=nowhere')).toEqual({ page: 'tests', scenarioView: 'builder', scenarioId: 's1' })
    expect(parseAppViewFromHash('#/tests?view=plans&scenarioId=s1')).toEqual({ page: 'tests', scenarioView: 'plans' })
    expect(viewToLocationHash({ page: 'tests', scenarioView: 'builder', scenarioId: 's1', scenarioStep: 'data', projectId: 'p1' })).toBe('#/tests?view=builder&scenarioId=s1&step=data&projectId=p1')
    // A project draft opens in the builder too.
    expect(parseAppViewFromHash('#/tests?view=builder&draftId=d1&projectId=p1')).toEqual({ page: 'tests', scenarioView: 'builder', scenarioDraftId: 'd1', projectId: 'p1' })
    expect(parseAppViewFromHash('#/tests?view=library&draftId=d1')).toEqual({ page: 'tests', scenarioView: 'library' })
    expect(viewToLocationHash({ page: 'tests', scenarioView: 'builder', scenarioDraftId: 'd1', projectId: 'p1' })).toBe('#/tests?view=builder&draftId=d1&projectId=p1')
    // Like any scenario link, one without a project resolves its owner first.
    expect(unscopedProjectAssetForAppView({ page: 'tests', scenarioView: 'builder', scenarioId: 's1' })).toEqual({ kind: 'scenario', id: 's1' })
    expect(scopeAppViewToProject({ page: 'tests', scenarioView: 'builder', scenarioId: 's1' }, 'p1')).toEqual({ page: 'tests', scenarioView: 'builder', scenarioId: 's1', projectId: 'p1' })
    expect(parseAppViewFromHash('#/docs')).toEqual({ page: 'docs' })
    expect(parseAppViewFromHash('#/docs?guide=authentication')).toEqual({ page: 'docs', guidePage: 'authentication' })
    expect(parseAppViewFromHash('#/docs?guide=not-real')).toEqual({ page: 'docs' })
    expect(parseAppViewFromHash('#/repository')).toEqual({ page: 'repository' })
    expect(parseAppViewFromHash('#/swagger')).toEqual({ page: 'swagger' })
    // Old repository-guide bookmarks open the API workflows article at its docs address.
    expect(parseAppViewFromHash('#/repository-guide')).toEqual({ page: 'docs', guideArticle: 'api-workflows' })
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

  it('names the importer in script import routes and translates the old import address', () => {
    // An address written before import routes named their launcher still
    // opens the LoadRunner archive importer it always opened.
    expect(parseAppViewFromHash('#/scripts?mode=import')).toEqual({ page: 'scripts', mode: 'import', importLauncher: 'enterprise_archive' })
    expect(parseAppViewFromHash('#/scripts?mode=import&projectId=p1')).toEqual({ page: 'scripts', mode: 'import', importLauncher: 'enterprise_archive', projectId: 'p1' })
    // A named launcher and mode round-trip.
    const har = { page: 'scripts', mode: 'import', importLauncher: 'traffic_har', importMode: 'auto_detect', projectId: 'p1' } as const
    expect(viewToLocationHash(har)).toBe('#/scripts?mode=import&launcher=traffic_har&importMode=auto_detect&projectId=p1')
    expect(parseAppViewFromHash(viewToLocationHash(har))).toEqual(har)
    expect(parseAppViewFromHash('#/scripts?mode=import&launcher=jmeter')).toEqual({ page: 'scripts', mode: 'import', importLauncher: 'jmeter' })
    // An unknown launcher opens the create options, which list every importer;
    // a malformed mode is dropped.
    expect(parseAppViewFromHash('#/scripts?mode=import&launcher=har-import')).toEqual({ page: 'scripts', mode: 'create' })
    expect(parseAppViewFromHash('#/scripts?mode=import&launcher=sample&importMode=Java%20HTTP')).toEqual({ page: 'scripts', mode: 'import', importLauncher: 'sample' })
    // Changing project keeps the open importer.
    expect(resetProjectScopedAssetView(har, 'p2')).toEqual({ page: 'scripts', mode: 'import', importLauncher: 'traffic_har', importMode: 'auto_detect', projectId: 'p2' })
  })

  it('opens Shared assets by section and row, and sends the old homes of what it holds there', () => {
    const view = parseAppViewFromHash('#/shared-assets?section=test-data&resourceId=pool-1&projectId=p1')
    expect(view).toEqual({ page: 'shared-assets', section: 'test-data', resourceId: 'pool-1', projectId: 'p1' })
    expect(viewToLocationHash(view)).toBe('#/shared-assets?section=test-data&resourceId=pool-1&projectId=p1')
    expect(parseAppViewFromHash('#/shared-assets?section=nonsense')).toEqual({ page: 'shared-assets' })
    expect(viewToLocationHash({ page: 'shared-assets' })).toBe('#/shared-assets')
    expect(navPageForAppView({ page: 'shared-assets' })).toBe('shared-assets')
    // Repository › Shared test data and Live tables, Project secrets (Access or Settings), the Scripts tab.
    expect(parseAppViewFromHash('#/repository?setting=test-data&resourceId=pool-1&projectId=p1')).toEqual({ page: 'shared-assets', section: 'test-data', resourceId: 'pool-1', projectId: 'p1' })
    expect(parseAppViewFromHash('#/repository?setting=live-tables&resourceId=t1&projectId=p1')).toEqual({ page: 'shared-assets', section: 'live-tables', resourceId: 't1', projectId: 'p1' })
    expect(parseAppViewFromHash('#/access?setting=secrets&projectId=p1')).toEqual({ page: 'shared-assets', section: 'secrets', projectId: 'p1' })
    expect(parseAppViewFromHash('#/settings?setting=secrets')).toEqual({ page: 'shared-assets', section: 'secrets' })
    expect(parseAppViewFromHash('#/scripts?mode=assets&projectId=p1')).toEqual({ page: 'shared-assets', section: 'object-repositories', projectId: 'p1' })
    // Other settings stay where they are.
    expect(parseAppViewFromHash('#/access?setting=redaction')).toEqual({ page: 'access', setting: 'redaction' })
  })

  it('writes a scripts route every authoring state reopens at', () => {
    const scope = { projectId: 'p1' }
    const route = (state: Parameters<typeof scriptsRouteForAuthoringState>[0]) => parseAppViewFromHash(viewToLocationHash(scriptsRouteForAuthoringState(state, scope)))
    expect(route({ subView: 'list', editingId: null })).toEqual({ page: 'scripts', mode: 'list', projectId: 'p1' })
    expect(route({ subView: 'edit', editingId: 'script-1' })).toEqual({ page: 'scripts', mode: 'edit', scriptId: 'script-1', projectId: 'p1' })
    expect(route({ subView: 'traffic_har', editingId: null, importLauncher: 'traffic_har', importMode: 'auto_detect' })).toEqual({ page: 'scripts', mode: 'import', importLauncher: 'traffic_har', importMode: 'auto_detect', projectId: 'p1' })
    // The Script builder's steps: the builder on its workflow and Start new
    // tab, Recording Studio with its method, and an unsaved draft.
    expect(route({ subView: 'create-options', editingId: null })).toEqual({ page: 'scripts', mode: 'create', projectId: 'p1' })
    expect(route({ subView: 'create-options', editingId: null, createWorkflow: 'start-new', createCategory: 'templates' })).toEqual({ page: 'scripts', mode: 'create', workflow: 'start-new', category: 'templates', projectId: 'p1' })
    // The Start new tab belongs to that workflow alone.
    expect(route({ subView: 'create-options', editingId: null, createWorkflow: 'source', createCategory: 'templates' })).toEqual({ page: 'scripts', mode: 'create', workflow: 'source', projectId: 'p1' })
    expect(route({ subView: 'recorder', editingId: null, recordMethod: 'proxy' })).toEqual({ page: 'scripts', mode: 'record', method: 'proxy', projectId: 'p1' })
    expect(route({ subView: 'create', editingId: null, createWorkflow: 'existing' })).toEqual({ page: 'scripts', mode: 'draft', projectId: 'p1' })
    // A surface without a route of its own reopens at the builder, not the archive importer.
    expect(route({ subView: 'edit', editingId: null })).toEqual({ page: 'scripts', mode: 'create', projectId: 'p1' })
  })

  it('reads builder steps and drops tabs that do not belong to them', () => {
    expect(parseAppViewFromHash('#/scripts?mode=create&workflow=existing&category=blocks')).toEqual({ page: 'scripts', mode: 'create', workflow: 'existing' })
    expect(parseAppViewFromHash('#/scripts?mode=create&category=blocks')).toEqual({ page: 'scripts', mode: 'create', category: 'blocks' })
    expect(parseAppViewFromHash('#/scripts?mode=create&workflow=bogus&category=nope')).toEqual({ page: 'scripts', mode: 'create' })
    expect(parseAppViewFromHash('#/scripts?mode=record&method=proxy')).toEqual({ page: 'scripts', mode: 'record', method: 'proxy' })
    expect(parseAppViewFromHash('#/scripts?mode=record&method=telnet&workflow=existing')).toEqual({ page: 'scripts', mode: 'record' })
    expect(parseAppViewFromHash('#/scripts?mode=draft&method=proxy')).toEqual({ page: 'scripts', mode: 'draft' })
    expect(scriptsPageStep({ page: 'scripts', mode: 'draft' })).toBe('draft')
    expect(scriptsPageStep({ page: 'scripts' })).toBe('list')
    expect(scriptsPageStep({ page: 'scripts', mode: 'edit', scriptId: 's' })).toBeNull()
    expect(scriptsPageStep({ page: 'scripts', mode: 'import', importLauncher: 'openapi' })).toBe('import')
    expect(scriptsPageStepDepth('create')).toBeLessThan(scriptsPageStepDepth('import'))
    expect(scriptsPageStepDepth('import')).toBeLessThan(scriptsPageStepDepth('draft'))
    expect(scriptsPageStepDepth('list')).toBeLessThan(scriptsPageStepDepth('create'))
    expect(scriptsPageStepDepth('create')).toBeLessThan(scriptsPageStepDepth('record'))
    expect(scriptsPageStepDepth('record')).toBeLessThan(scriptsPageStepDepth('draft'))
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
    expect(viewToLocationHash(parseAppViewFromHash('#/repository-guide'))).toBe('#/docs?article=api-workflows')
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
    // A Shared assets section stays open; the pool, table or application it named was the old project's.
    expect(resetProjectScopedAssetView({ page: 'shared-assets', section: 'live-tables', resourceId: 'table-a', projectId: 'project-a' }, 'project-b')).toEqual({
      page: 'shared-assets',
      section: 'live-tables',
      projectId: 'project-b',
    })
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

it('round trips an archive-tab focus on the report and drops unknown focus targets', () => {
  const view = parseAppViewFromHash('#/report/run-1?section=performance&focus=load-impact')
  expect(view).toMatchObject({ page: 'report', section: 'performance', focus: 'load-impact' })
  expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
  expect(parseAppViewFromHash('#/report/run-1?focus=elsewhere')).not.toHaveProperty('focus')
})

describe('terminal run route compatibility', () => {
  it.each(['COMPLETE', 'FAILED', 'ABORTED'])('keeps %s runs inside Results regardless of legacy entry point', status => {
    const scope = { projectId: 'p', applicationId: 'a', releaseId: 'r' }
    expect(terminalRunReportView({ page: 'command-center', testId: 'run', ...scope }, status, true)).toEqual({ page: 'report', testId: 'run', ...scope, section: 'summary' })
    expect(terminalRunReportView({ page: 'test', testId: 'run', ...scope }, status, true)).toEqual({ page: 'report', testId: 'run', ...scope, section: 'summary' })
    for (const [tab, section] of [['overview', 'summary'], ['control', 'execution'], ['configuration', 'execution'], ['graphs', 'performance'], ['diagnostics', 'diagnostics'], ['archive', 'evidence'], ['action-summary', 'performance'], ['load-impact', 'performance'], ['transaction-drilldown', 'diagnostics']] as const) {
      const focus = ['action-summary', 'load-impact', 'transaction-drilldown'].includes(tab) ? { focus: tab } : {}
      expect(terminalRunReportView({ page: 'test', testId: 'run', tab, ...scope }, status, true)).toEqual({ page: 'report', testId: 'run', ...scope, section, ...focus })
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

it('round-trips an immutable baseline comparison without losing the run scope', () => {
  const view = { page: 'report' as const, testId: 'run/123', section: 'comparison' as const, comparisonId: 'comparison-1', projectId: 'project-1' }
  expect(parseAppViewFromHash(viewToLocationHash(view))).toEqual(view)
})

describe('activity route query', () => {
  it('keeps activity filters and page through parse and serialize', () => {
    const view = parseAppViewFromHash('#/activity?action=stop&q=checkout&page=2&projectId=p1&junk=1')
    expect(view).toEqual({ page: 'activity', activityQuery: 'q=checkout&action=stop&page=2', projectId: 'p1' })
    expect(viewToLocationHash(view)).toBe('#/activity?q=checkout&action=stop&page=2&projectId=p1')
    expect(viewToLocationHash({ page: 'activity' })).toBe('#/activity')
  })

  it('starts activity on the newest events of a newly selected project', () => {
    const view: AppView = { page: 'activity', activityQuery: 'action=stop&page=3', projectId: 'p1' }
    expect(scopeAppViewToWorkspace(resetProjectScopedAssetView(view, 'p2'), { projectId: 'p2' })).toEqual({ page: 'activity', projectId: 'p2' })
  })
})
