import { expect, it } from 'vitest'
import { contextualHelpHash } from './contextualHelp'
import { articleForSection, helpArticles, loadHelpSource } from './helpContent'
import type { AppView } from './appRouting'

const cases: [AppView, string, string, string?][] = [
  [{ page: 'dashboard' }, 'user-guide', 'orientation', 'the-dashboard'], [{ page: 'activity' }, 'user-guide', 'orientation', 'the-dashboard'],
  [{ page: 'scripts' }, 'user-guide', 'authoring'], [{ page: 'scripts', mode: 'import' }, 'user-guide', 'imports'],
  [{ page: 'scripts', mode: 'record' }, 'user-guide', 'replay', 'recording-and-correlation'],
  [{ page: 'recorder-panel', sessionId: 's1' }, 'user-guide', 'replay', 'recording-and-correlation'],
  [{ page: 'tests' }, 'user-guide', 'scenarios'],
  [{ page: 'tests', scenarioView: 'plans' }, 'user-guide', 'execution', 'chaining-runs-with-execution-plans'],
  [{ page: 'new-test' }, 'user-guide', 'execution', 'launching-a-test'],
  [{ page: 'command-center' }, 'user-guide', 'execution', 'monitoring-a-live-run'], [{ page: 'test', testId: 'run' }, 'user-guide', 'execution', 'monitoring-a-live-run'],
  [{ page: 'test', testId: 'run', tab: 'archive' }, 'user-guide', 'results'],
  [{ page: 'monitoring' }, 'user-guide', 'execution', 'monitoring-a-live-run'], [{ page: 'fleet' }, 'user-guide', 'execution', 'monitoring-a-live-run'], [{ page: 'schedules' }, 'user-guide', 'execution', 'scheduling-recurring-runs'],
  [{ page: 'results' }, 'user-guide', 'results'], [{ page: 'report', testId: 'run' }, 'user-guide', 'results'],
  [{ page: 'report', testId: 'run', section: 'browser' }, 'user-guide', 'browser-intelligence', 'the-browser-area-of-a-run-report'],
  [{ page: 'repository' }, 'user-guide', 'shared-assets', 'repository-and-shared-assets'], [{ page: 'settings' }, 'user-guide', 'shared-assets', 'settings-reference'],
  [{ page: 'shared-assets', section: 'live-tables' }, 'user-guide', 'project-live-tables'],
  [{ page: 'settings', setting: 'clusters' }, 'admin-guide', 'clusters'],
  [{ page: 'report', testId: 'run', section: 'diagnostics' }, 'user-guide', 'root-cause'], [{ page: 'settings', setting: 'analysis' }, 'user-guide', 'root-cause'],
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

it('sends an importer opened from script builder to the import help', () => {
  expect(contextualHelpHash({ page: 'scripts', mode: 'create' }, 'jmeter-import')).toBe('#/docs?guide=user-guide&article=imports')
  expect(contextualHelpHash({ page: 'scripts', mode: 'create' }, 'create')).toBe('#/docs?guide=user-guide&article=authoring')
  // What scripts share has its own page and help.
  expect(contextualHelpHash({ page: 'shared-assets', section: 'secrets' })).toBe('#/docs?guide=user-guide&article=shared-assets&section=repository-and-shared-assets')
})

it('offers no page help link on the help pages themselves', () => {
  expect(contextualHelpHash({ page: 'docs' })).toBeUndefined()
  expect(contextualHelpHash({ page: 'swagger' })).toBeUndefined()
})
