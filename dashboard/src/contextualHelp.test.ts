import { expect, it } from 'vitest'
import { contextualHelpHash } from './contextualHelp'
import { helpArticles } from './helpContent'
import type { AppView } from './appRouting'

it('links every sidebar destination to task guidance', () => {
  const cases: [AppView, string, string][] = [
    [{ page: 'dashboard' }, 'user-guide', 'orientation'], [{ page: 'activity' }, 'user-guide', 'orientation'],
    [{ page: 'scripts' }, 'user-guide', 'authoring'], [{ page: 'scripts', mode: 'import' }, 'user-guide', 'imports'],
    [{ page: 'tests' }, 'user-guide', 'scenarios'], [{ page: 'new-test' }, 'user-guide', 'scenarios'],
    [{ page: 'command-center' }, 'user-guide', 'execution'], [{ page: 'test', testId: 'run' }, 'user-guide', 'execution'],
    [{ page: 'monitoring' }, 'user-guide', 'execution'], [{ page: 'fleet' }, 'user-guide', 'execution'], [{ page: 'schedules' }, 'user-guide', 'execution'],
    [{ page: 'results' }, 'user-guide', 'results'], [{ page: 'report', testId: 'run' }, 'user-guide', 'results'],
    [{ page: 'repository' }, 'user-guide', 'shared-assets'], [{ page: 'settings' }, 'user-guide', 'shared-assets'],
    [{ page: 'access' }, 'admin-guide', 'identity'],
  ]
  for (const [view, guide, id] of cases) {
    expect(contextualHelpHash(view), view.page).toBe(`#/docs?guide=${guide}&article=${id}`)
    expect(helpArticles.some(article => article.id === id && article.area === guide), `${guide}/${id}`).toBe(true)
  }
})

it('offers no page help link on the help pages themselves', () => {
  expect(contextualHelpHash({ page: 'docs' })).toBeUndefined()
  expect(contextualHelpHash({ page: 'swagger' })).toBeUndefined()
  expect(contextualHelpHash({ page: 'repository-guide' })).toBeUndefined()
})
