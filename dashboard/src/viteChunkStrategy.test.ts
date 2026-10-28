import { describe, expect, it } from 'vitest'

import { chunkGroupOrder, chunkGroups, chunkNameForModule } from '../scripts/chunk-strategy.mjs'

// vite.config.ts feeds this classifier to Rolldown as one prioritized
// `build.rolldownOptions.output.codeSplitting` group per chunk name
// (chunkGroups). The route budgets in
// scripts/check-bundle-budget.mjs forbid CodeMirror and Three.js assets on the
// initial shell and the execution route, so the split below is a shipping
// invariant rather than a cosmetic preference.
const modulePath = (suffix: string) => `/repo/dashboard/node_modules/${suffix}`

describe('chunkNameForModule', () => {
  it('isolates the HTTP version formatter from the runtime-editor chunk', () => {
    expect(chunkNameForModule('/repo/dashboard/src/components/httpVersionModel.ts')).toBe('http-version-model')
  })

  it.each([
    'hooks/apiContext.ts',
    'hooks/useApi.ts',
    'hooks/projectContext.ts',
    'hooks/useProject.tsx',
    'hooks/applicationContext.ts',
    'hooks/useApplication.tsx',
    'hooks/useDevView.ts',
    'hooks/NavigationGuardProvider.tsx',
    'hooks/TopbarContributionProvider.tsx',
    'hooks/KeyboardShortcutProvider.tsx',
    'hooks/useModalDialog.ts',
    'components/dialogBackdropProps.ts',
    'components/ux/DialogLayer.tsx',
    'components/LazyLoadBoundary.tsx',
    'utils/authAccessUtils.ts',
    'utils/clipboardUtils.ts',
    'appRouting.ts',
    'appTopbar.ts',
    'components/settingsNavigation.ts',
    'components/ErrorDiagnostic.tsx',
    'components/Logo.tsx',
    'hooks/useWebSocket.ts',
    'components/ux/LoadingState.tsx',
    'utils/dateTime.ts',
  ])('separates the shared workspace module %s from the shell entry', suffix => {
    expect(chunkNameForModule(`/repo/dashboard/src/${suffix}`)).toBe('workspace-shared')
  })

  it.each([
    'components/testExecutionMetricsModel.ts',
    'components/testExecutionChartZoomModel.ts',
    'components/generatorImpact.ts',
    'components/latencySeries.ts',
    'components/transactionChartSeries.ts',
  ])('separates the pure execution metric model %s from shell and workspace UI', suffix => {
    expect(chunkNameForModule(`/repo/dashboard/src/${suffix}`)).toBe('execution-metrics-model')
  })

  it.each([
    ['useObjectRepositoryApi', 'api-object-repositories'],
    ['useCorrelationRulesetApi', 'api-correlation-rulesets'],
    ['useLibraryApi', 'api-library'],
    ['useSystemApi', 'api-system'],
    ['useTestApi', 'api-tests'],
    ['useDynatraceConnectionsApi', 'api-dynatrace-connections'],
    ['useProjectApi', 'api-projects'],
    ['useScriptApi', 'api-scripts'],
    ['useDashboardDefinitionsApi', 'api-dashboard-definitions'],
    ['useELKConnectionsApi', 'api-elk-connections'],
  ])('keeps the measured shared API facade %s independent of its siblings', (name, chunk) => {
    expect(chunkNameForModule(`/repo/dashboard/src/hooks/api/${name}.ts`)).toBe(chunk)
  })

  it('leaves unshared API facades, helpers and neighboring paths automatic', () => {
    expect(chunkNameForModule('/repo/dashboard/src/hooks/api/useCapacityApi.ts')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/hooks/api/requestOptions.ts')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/hooks/api/custom/useLibraryApi.ts')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/hooks/api/useLibraryApi.test.ts')).toBeUndefined()
    expect(chunkNameForModule(modulePath('some-package/src/hooks/api/useLibraryApi.ts'))).toBe('vendor')
  })

  it.each([
    'App.tsx',
    'hooks/ApiProvider.ts',
    'hooks/ProjectProvider.tsx',
    'hooks/ApplicationProvider.tsx',
    'hooks/DevViewProvider.tsx',
    'hooks/TopbarContributionRoot.tsx',
    'hooks/KeyboardShortcutRoot.tsx',
    'components/ScriptEditor.tsx',
    'components/TestExecution.tsx',
    'components/TestExecutionArchive.tsx',
    'components/testExecutionChartModel.ts',
    'components/LiveRunMenu.tsx',
  ])('leaves stateful roots and feature surfaces such as %s to automatic chunking', suffix => {
    expect(chunkNameForModule(`/repo/dashboard/src/${suffix}`)).toBeUndefined()
  })

  // The shell and lazy routes share these with the shell-held modules they
  // import, so a route that needs one never imports the shell entry.
  it.each([
    ['hooks/NavigationGuardRoot.tsx', 'navigation-guard'],
    ['components/UnsavedChangesModal.tsx', 'navigation-guard'],
    ['desktop/useDesktopCloseGuard.ts', 'navigation-guard'],
    ['desktop/reloadPage.ts', 'navigation-guard'],
    ['desktop/wailsRuntime.ts', 'navigation-guard'],
    ['desktop/desktopFiles.ts', 'desktop-files'],
    ['utils/fileUtils.ts', 'desktop-files'],
    ['utils/authoringRevision.ts', 'script-authoring'],
    ['utils/scriptAuthoringRevision.ts', 'script-authoring'],
    ['utils/scenarioAuthoringRevision.ts', 'script-authoring'],
    ['importLogicalPath.ts', 'script-authoring'],
  ])('keeps the route-shared module %s out of the shell entry', (suffix, chunk) => {
    expect(chunkNameForModule(`/repo/dashboard/src/${suffix}`)).toBe(chunk)
  })

  it('claims generic vendor code first and each shared app chunk before the route-shared chunks', () => {
    const groups = chunkGroups()
    expect(groups.map(group => group.name)).toEqual([...chunkGroupOrder])
    expect(new Set(chunkGroupOrder).size).toBe(chunkGroupOrder.length)
    // Higher priority claims first; Rolldown adds a captured module's
    // dependencies to its group, so the order decides contested modules.
    const priorities = groups.map(group => group.priority)
    expect(priorities).toEqual([...priorities].sort((a, b) => b - a))
    const rank = (name: string) => chunkGroupOrder.indexOf(name)
    expect(rank('vendor')).toBe(0)
    for (const name of ['vendor-virtual', 'vendor-yaml', 'vendor-three', 'vendor-recharts', 'vendor-codemirror-core', 'workspace-shared']) {
      expect(rank('vendor')).toBeLessThan(rank(name))
    }
    for (const name of ['navigation-guard', 'desktop-files', 'script-authoring', 'api-scripts']) {
      expect(rank('workspace-shared')).toBeLessThan(rank(name))
    }
  })

  it('gives every chunk name the classifier returns a group, and a group only its own modules', () => {
    const samples = [
      modulePath('react-dom/client.js'),
      modulePath('three/build/three.module.js'),
      modulePath('@lezer/highlight/dist/index.js'),
      '\u0000vite/preload-helper.js',
      '/repo/dashboard/src/components/httpVersionModel.ts',
      '/repo/dashboard/src/hooks/useApi.ts',
      '/repo/dashboard/src/components/latencySeries.ts',
      '/repo/dashboard/src/hooks/api/useScriptApi.ts',
      '/repo/dashboard/src/hooks/NavigationGuardRoot.tsx',
      '/repo/dashboard/src/utils/fileUtils.ts',
      '/repo/dashboard/src/utils/authoringRevision.ts',
    ]
    const groups = chunkGroups()
    for (const id of samples) {
      const name = chunkNameForModule(id)
      expect(groups.filter(group => group.test(id)).map(group => group.name)).toEqual([name])
    }
    expect(groups.some(group => group.test('/repo/dashboard/src/App.tsx'))).toBe(false)
  })

  it('matches shared workspace modules by their complete path, excluding dependency code', () => {
    expect(chunkNameForModule('/repo/dashboard/src/hooks/custom/useProject.tsx')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/hooks/useProject.tsx.backup')).toBeUndefined()
    expect(chunkNameForModule(modulePath('some-package/src/hooks/useProject.tsx'))).toBe('vendor')
    expect(chunkNameForModule('/repo/dashboard/src/components/custom/latencySeries.ts')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/components/latencySeries.test.ts')).toBeUndefined()
    expect(chunkNameForModule(modulePath('some-package/src/components/latencySeries.ts'))).toBe('vendor')
  })

  it('separates the actual Vite preload helper without classifying other virtual modules', () => {
    expect(chunkNameForModule('\u0000vite/preload-helper.js')).toBe('vite-preload-helper')
    expect(chunkNameForModule('\u0000vite/another-helper.js')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/vite/preload-helper.js')).toBeUndefined()
  })

  it('leaves other first-party modules to automatic chunking', () => {
    expect(chunkNameForModule('/repo/dashboard/src/components/ScriptEditor.tsx')).toBeUndefined()
    expect(chunkNameForModule('/repo/dashboard/src/components/TestExecution.tsx')).toBeUndefined()
    expect(chunkNameForModule('\u0000virtual:some-plugin-module')).toBeUndefined()
  })

  it('routes Three.js, Recharts and YAML to their own vendor chunks', () => {
    expect(chunkNameForModule(modulePath('three/build/three.module.js'))).toBe('vendor-three')
    expect(chunkNameForModule(modulePath('recharts/es6/chart/LineChart.js'))).toBe('vendor-recharts')
    expect(chunkNameForModule(modulePath('yaml/browser/dist/index.js'))).toBe('vendor-yaml')
  })

  it('splits CodeMirror into language, parser and core chunks', () => {
    expect(chunkNameForModule(modulePath('@codemirror/lang-json/dist/index.js'))).toBe('vendor-codemirror-languages')
    expect(chunkNameForModule(modulePath('@lezer/highlight/dist/index.js'))).toBe('vendor-codemirror-parser')
    expect(chunkNameForModule(modulePath('@codemirror/state/dist/index.js'))).toBe('vendor-codemirror-core')
    expect(chunkNameForModule(modulePath('@uiw/react-codemirror/esm/index.js'))).toBe('vendor-codemirror-core')
    expect(chunkNameForModule(modulePath('@uiw/codemirror-theme-github/esm/index.js'))).toBe('vendor-codemirror-core')
    expect(chunkNameForModule(modulePath('codemirror/dist/index.js'))).toBe('vendor-codemirror-core')
  })

  it('keeps the language chunk ahead of the core chunk for @codemirror/lang-* packages', () => {
    // Both the `/@codemirror/lang-` and `/@codemirror/` tests match this id;
    // reordering them would fold every grammar into the core editor chunk.
    expect(chunkNameForModule(modulePath('@codemirror/lang-javascript/dist/index.js'))).not.toBe('vendor-codemirror-core')
  })

  it.each([
    '@tanstack/react-virtual/dist/esm/index.js',
    '@tanstack/virtual-core/dist/esm/index.js',
    '.pnpm/@tanstack+virtual-core@3.13.12/node_modules/@tanstack/virtual-core/dist/esm/index.js',
  ])('separates lazy workspace virtualization dependency %s from the shell vendor chunk', suffix => {
    expect(chunkNameForModule(modulePath(suffix))).toBe('vendor-virtual')
  })

  it('does not classify other Tanstack packages or first-party virtualizer names as virtualization dependencies', () => {
    expect(chunkNameForModule(modulePath('@tanstack/query-core/dist/index.js'))).toBe('vendor')
    expect(chunkNameForModule(modulePath('@tanstack/react-virtual-extra/dist/index.js'))).toBe('vendor')
    expect(chunkNameForModule('/repo/dashboard/src/@tanstack/react-virtual/List.tsx')).toBeUndefined()
  })

  it('sends every other dependency to the shared vendor chunk', () => {
    expect(chunkNameForModule(modulePath('react-dom/client.js'))).toBe('vendor')
  })

  it('does not treat a first-party path that merely mentions a vendor name as vendor code', () => {
    expect(chunkNameForModule('/repo/dashboard/src/components/three/Heatmap.tsx')).toBeUndefined()
  })
})
