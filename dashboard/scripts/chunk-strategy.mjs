// Manual chunk classification for the dashboard production build.
//
// This lives outside vite.config.ts so the classification is unit testable:
// the build wires it into `build.rolldownOptions.output.codeSplitting`, and
// src/viteChunkStrategy.test.ts pins the routing decisions that the bundle
// budgets in scripts/check-bundle-budget.mjs depend on.
//
// Returning `undefined` leaves the module to Rolldown's automatic chunking,
// which is the same meaning the value carried under the deprecated
// `output.manualChunks` hook.
// These modules hold shared context identities or small UI primitives, rather
// than the stateful roots that mount the dashboard. Lazy routes must be able to
// use them without importing the shell entry and everything that it renders.
const workspaceSharedModuleSuffixes = [
  '/src/hooks/apiContext.ts',
  '/src/hooks/useApi.ts',
  '/src/hooks/projectContext.ts',
  '/src/hooks/useProject.tsx',
  '/src/hooks/applicationContext.ts',
  '/src/hooks/useApplication.tsx',
  '/src/hooks/useDevView.ts',
  '/src/hooks/NavigationGuardProvider.tsx',
  '/src/hooks/TopbarContributionProvider.tsx',
  '/src/hooks/KeyboardShortcutProvider.tsx',
  '/src/hooks/useModalDialog.ts',
  '/src/components/dialogBackdropProps.ts',
  '/src/components/ux/DialogLayer.tsx',
  '/src/components/LazyLoadBoundary.tsx',
  '/src/utils/authAccessUtils.ts',
  '/src/utils/clipboardUtils.ts',
  '/src/appRouting.ts',
  '/src/appTopbar.ts',
  '/src/components/settingsNavigation.ts',
  '/src/components/ErrorDiagnostic.tsx',
  '/src/components/Logo.tsx',
  '/src/hooks/useWebSocket.ts',
  '/src/components/ux/LoadingState.tsx',
  '/src/utils/dateTime.ts',
]

// The shell's active-run summaries and the execution route share these pure
// mapping/series models. Keep their functions independent of the shell and of
// the UI primitives used by non-execution routes.
const executionMetricModuleSuffixes = [
  '/src/components/testExecutionMetricsModel.ts',
  '/src/components/testExecutionChartZoomModel.ts',
  '/src/components/generatorImpact.ts',
  '/src/components/latencySeries.ts',
  '/src/components/transactionChartSeries.ts',
]

// These API facades are shared directly by the shell provider and the
// measured authoring/execution routes. Keep their own boundaries so a route
// loads its required APIs without pulling in provider assembly or sibling APIs.
const sharedApiModuleChunks = new Map([
  ['/src/hooks/api/useObjectRepositoryApi.ts', 'api-object-repositories'],
  ['/src/hooks/api/useCorrelationRulesetApi.ts', 'api-correlation-rulesets'],
  ['/src/hooks/api/useLibraryApi.ts', 'api-library'],
  ['/src/hooks/api/useSystemApi.ts', 'api-system'],
  ['/src/hooks/api/useTestApi.ts', 'api-tests'],
  ['/src/hooks/api/useDynatraceConnectionsApi.ts', 'api-dynatrace-connections'],
  ['/src/hooks/api/useAppDynamicsConnectionsApi.ts', 'api-appdynamics-connections'],
  ['/src/hooks/api/useProjectApi.ts', 'api-projects'],
  ['/src/hooks/api/useScriptApi.ts', 'api-scripts'],
  ['/src/hooks/api/useDashboardDefinitionsApi.ts', 'api-dashboard-definitions'],
  ['/src/hooks/api/useELKConnectionsApi.ts', 'api-elk-connections'],
])

// The shell and lazy routes share these modules too, each with the shell-held
// modules it imports, so a route that needs one never imports the shell entry:
// the app-wide navigation guard (also mounted by dialogs) and the desktop
// close guard behind it; desktop-aware file saving; and script and scenario
// authoring revisions with the script API's import paths.
const sharedRouteModuleChunks = new Map([
  ['/src/hooks/NavigationGuardRoot.tsx', 'navigation-guard'],
  ['/src/components/UnsavedChangesModal.tsx', 'navigation-guard'],
  ['/src/desktop/useDesktopCloseGuard.ts', 'navigation-guard'],
  ['/src/desktop/reloadPage.ts', 'navigation-guard'],
  ['/src/desktop/wailsRuntime.ts', 'navigation-guard'],
  ['/src/desktop/desktopFiles.ts', 'desktop-files'],
  ['/src/utils/fileUtils.ts', 'desktop-files'],
  ['/src/utils/authoringRevision.ts', 'script-authoring'],
  ['/src/utils/scriptAuthoringRevision.ts', 'script-authoring'],
  ['/src/utils/scenarioAuthoringRevision.ts', 'script-authoring'],
  ['/src/importLogicalPath.ts', 'script-authoring'],
])

export function chunkNameForModule(id) {
  // The execution route only needs the tiny, dependency-free HTTP version
  // formatter. Keep Rolldown from coalescing it with shared runtime-editor
  // components (and their CodeMirror dependencies).
  if (id.endsWith('/src/components/httpVersionModel.ts')) {
    return 'http-version-model'
  }
  // Vite injects this module into both the entry and lazy routes. Leaving it
  // in the entry creates the same reverse dependency even after shared hooks
  // have been separated. Match only Vite's actual virtual module identity.
  if (id === '\0vite/preload-helper.js') {
    return 'vite-preload-helper'
  }
  if (!id.includes('node_modules')) {
    for (const [suffix, name] of sharedApiModuleChunks) {
      if (id.endsWith(suffix)) return name
    }
    for (const [suffix, name] of sharedRouteModuleChunks) {
      if (id.endsWith(suffix)) return name
    }
    if (workspaceSharedModuleSuffixes.some(suffix => id.endsWith(suffix))) {
      return 'workspace-shared'
    }
    return executionMetricModuleSuffixes.some(suffix => id.endsWith(suffix))
      ? 'execution-metrics-model'
      : undefined
  }
  // Virtual lists belong to editor/results workspaces; the initial shell does
  // not need either the React binding or its core implementation.
  if (id.includes('/node_modules/@tanstack/react-virtual/') || id.includes('/node_modules/@tanstack/virtual-core/')) {
    return 'vendor-virtual'
  }
  if (id.includes('/three/')) {
    return 'vendor-three'
  }
  if (id.includes('/recharts/')) {
    return 'vendor-recharts'
  }
  if (id.includes('/@codemirror/lang-')) {
    return 'vendor-codemirror-languages'
  }
  if (id.includes('/@lezer/')) {
    return 'vendor-codemirror-parser'
  }
  if (id.includes('/@codemirror/') || id.includes('/@uiw/react-codemirror/') || id.includes('/@uiw/codemirror') || id.includes('/codemirror/')) {
    return 'vendor-codemirror-core'
  }
  return 'vendor'
}

// Claim order, highest priority first. Rolldown adds a captured module's
// dependencies to the group that captures it, so with one group a module
// visited early could claim modules mapped to other chunks (React itself has
// landed in an app chunk that way). Generic vendor code is claimed first, so
// the specialized vendor chunks keep only their own packages; then the shared
// app chunks, each before the route-shared chunks that import it.
export const chunkGroupOrder = Object.freeze([
  'vendor',
  'vendor-virtual',
  'vendor-three',
  'vendor-recharts',
  'vendor-codemirror-languages',
  'vendor-codemirror-parser',
  'vendor-codemirror-core',
  'vite-preload-helper',
  'http-version-model',
  'workspace-shared',
  'execution-metrics-model',
  ...new Set(sharedApiModuleChunks.values()),
  ...new Set(sharedRouteModuleChunks.values()),
])

// One Rolldown codeSplitting group per chunk name, in claim order.
export function chunkGroups() {
  return chunkGroupOrder.map((name, index) => ({
    name,
    priority: chunkGroupOrder.length - index,
    test: (id) => chunkNameForModule(id) === name,
  }))
}
