import type { AppView } from './appRouting'

/** Keep guidance attached to the task even when the user enters through a deep link. */
export function contextualHelpHash(view: AppView): string | undefined {
  const article = view.page === 'scripts' ? (view.mode === 'import' ? 'imports' : 'authoring')
    : view.page === 'tests' || view.page === 'new-test' ? 'scenarios'
      : view.page === 'report' || view.page === 'results' ? 'results'
        : view.page === 'test' || view.page === 'command-center' ? 'execution'
          : undefined
  return article ? `#/docs?guide=user-guide&article=${article}` : undefined
}
