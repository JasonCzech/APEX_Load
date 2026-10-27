import { isArchiveTestWorkspaceTab, type AppView } from './appRouting'

type HelpTarget = { guide: 'user-guide' | 'admin-guide'; article: string; section?: string }

const userGuide = (article: string, section?: string): HelpTarget => ({ guide: 'user-guide', article, section })
const adminGuide = (article: string, section?: string): HelpTarget => ({ guide: 'admin-guide', article, section })

// Every sidebar destination links to the help section that covers it, not just
// a loosely related article. The Help pages themselves have none.
const PAGE_HELP: Partial<Record<AppView['page'], HelpTarget>> = {
  dashboard: userGuide('orientation', 'the-dashboard'),
  activity: userGuide('orientation', 'the-dashboard'),
  'new-test': userGuide('execution', 'launching-a-test'),
  tests: userGuide('scenarios'),
  'command-center': userGuide('execution', 'monitoring-a-live-run'),
  test: userGuide('execution', 'monitoring-a-live-run'),
  monitoring: userGuide('execution', 'monitoring-a-live-run'),
  fleet: userGuide('execution', 'monitoring-a-live-run'),
  schedules: userGuide('execution', 'scheduling-recurring-runs'),
  results: userGuide('results'),
  report: userGuide('results'),
  'recorder-panel': userGuide('replay', 'recording-and-correlation'),
  repository: userGuide('shared-assets', 'repository-and-shared-assets'),
  'shared-assets': userGuide('shared-assets', 'repository-and-shared-assets'),
  settings: userGuide('shared-assets', 'settings-reference'),
  access: adminGuide('administration', 'the-access-console'),
}

function helpTarget(view: AppView, scriptsSubView?: string): HelpTarget | undefined {
  if (view.page === 'scripts') {
    if (view.mode === 'record' || scriptsSubView === 'recorder') return userGuide('replay', 'recording-and-correlation')
    // Importers opened from script builder keep mode=create in the URL; the
    // open sub-view says which help applies.
    return userGuide(view.mode === 'import' || scriptsSubView?.endsWith('import') ? 'imports' : 'authoring')
  }
  if (view.page === 'tests' && view.scenarioView === 'plans') return userGuide('execution', 'chaining-runs-with-execution-plans')
  if (view.page === 'test' && isArchiveTestWorkspaceTab(view.tab)) return userGuide('results')
  if (view.page === 'shared-assets' && view.section === 'live-tables') return userGuide('project-live-tables')
  if (view.page === 'settings' && view.setting === 'clusters') return adminGuide('clusters')
  // A report's Diagnostics section leads with the root-cause panel.
  if (view.page === 'report' && view.section === 'diagnostics') return userGuide('root-cause')
  if (view.page === 'report' && view.section === 'browser') return userGuide('browser-intelligence', 'the-browser-area-of-a-run-report')
  if (view.page === 'settings' && view.setting === 'analysis') return userGuide('root-cause')
  return PAGE_HELP[view.page]
}

export function contextualHelpHash(view: AppView, scriptsSubView?: string): string | undefined {
  const help = helpTarget(view, scriptsSubView)
  if (!help) return undefined
  const params = new URLSearchParams({ guide: help.guide, article: help.article })
  if (help.section) params.set('section', help.section)
  return `#/docs?${params}`
}
