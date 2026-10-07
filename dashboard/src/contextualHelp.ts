import type { AppView } from './appRouting'

type HelpTarget = { guide: 'user-guide' | 'admin-guide'; article: string }

const userGuide = (article: string): HelpTarget => ({ guide: 'user-guide', article })

// Every sidebar destination links to its help article. The Help pages themselves
// have none.
const PAGE_HELP: Partial<Record<AppView['page'], HelpTarget>> = {
  dashboard: userGuide('orientation'),
  activity: userGuide('orientation'),
  'new-test': userGuide('scenarios'),
  tests: userGuide('scenarios'),
  'command-center': userGuide('execution'),
  test: userGuide('execution'),
  monitoring: userGuide('execution'),
  fleet: userGuide('execution'),
  schedules: userGuide('execution'),
  results: userGuide('results'),
  report: userGuide('results'),
  repository: userGuide('shared-assets'),
  settings: userGuide('shared-assets'),
  access: { guide: 'admin-guide', article: 'identity' },
}

export function contextualHelpHash(view: AppView): string | undefined {
  const help = view.page === 'scripts'
    ? userGuide(view.mode === 'import' ? 'imports' : 'authoring')
    : PAGE_HELP[view.page]
  return help ? `#/docs?guide=${help.guide}&article=${help.article}` : undefined
}
