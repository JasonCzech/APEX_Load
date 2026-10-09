import { expect, it } from 'vitest'
import { contextualHelpHash } from './contextualHelp'
import { articleForSection, helpArticles, loadHelpSource } from './helpContent'
import type { AppView } from './appRouting'

const cases: [AppView, string, string, string?][] = [
  [{ page: 'dashboard' }, 'user-guide', 'orientation', 'the-dashboard'], [{ page: 'activity' }, 'user-guide', 'orientation', 'the-dashboard'],
  [{ page: 'scripts' }, 'user-guide', 'authoring'], [{ page: 'scripts', mode: 'import' }, 'user-guide', 'imports'],
  [{ page: 'tests' }, 'user-guide', 'scenarios'], [{ page: 'new-test' }, 'user-guide', 'execution', 'launching-a-test'],
  [{ page: 'command-center' }, 'user-guide', 'execution', 'monitoring-a-live-run'], [{ page: 'test', testId: 'run' }, 'user-guide', 'execution', 'monitoring-a-live-run'],
  [{ page: 'monitoring' }, 'user-guide', 'execution', 'monitoring-a-live-run'], [{ page: 'fleet' }, 'user-guide', 'execution', 'monitoring-a-live-run'], [{ page: 'schedules' }, 'user-guide', 'execution', 'scheduling-recurring-runs'],
  [{ page: 'results' }, 'user-guide', 'results'], [{ page: 'report', testId: 'run' }, 'user-guide', 'results'],
  [{ page: 'repository' }, 'user-guide', 'shared-assets', 'repository-and-shared-assets'], [{ page: 'settings' }, 'user-guide', 'shared-assets', 'settings-reference'],
  [{ page: 'settings', setting: 'clusters' }, 'admin-guide', 'clusters'],
  [{ page: 'access' }, 'admin-guide', 'administration', 'the-access-console'],
]

it('links every sidebar destination to the help section that covers it', async () => {
  for (const [view, guide, id, section] of cases) {
    expect(contextualHelpHash(view), view.page).toBe(`#/docs?guide=${guide}&article=${id}${section ? `&section=${section}` : ''}`)
    const article = helpArticles.find(candidate => candidate.id === id && candidate.area === guide)
    expect(article, `${guide}/${id}`).toBeDefined()
    // The section must exist in the bundled article, or the link lands on "not available".
    if (section) expect(articleForSection(article!.path, section, await loadHelpSource(article!)), `${id}#${section}`).toBeDefined()
  }
})

it('sends an importer opened from Script Builder to the import help', () => {
  expect(contextualHelpHash({ page: 'scripts', mode: 'create' }, 'jmeter-import')).toBe('#/docs?guide=user-guide&article=imports')
  expect(contextualHelpHash({ page: 'scripts', mode: 'create' }, 'create')).toBe('#/docs?guide=user-guide&article=authoring')
})

it('offers no page help link on the help pages themselves', () => {
  expect(contextualHelpHash({ page: 'docs' })).toBeUndefined()
  expect(contextualHelpHash({ page: 'swagger' })).toBeUndefined()
})
