import { expect, it } from 'vitest'
import { contextualHelpHash } from './contextualHelp'
import { helpArticles } from './helpContent'
import type { AppView } from './appRouting'

it('offers task guidance for authoring, import, load modeling, monitoring and reports', () => {
  const cases: [AppView, string][] = [
    [{ page: 'scripts' }, 'authoring'], [{ page: 'scripts', mode: 'import' }, 'imports'],
    [{ page: 'tests' }, 'scenarios'], [{ page: 'new-test' }, 'scenarios'],
    [{ page: 'command-center' }, 'execution'], [{ page: 'test', testId: 'run' }, 'execution'],
    [{ page: 'results' }, 'results'], [{ page: 'report', testId: 'run' }, 'results'],
  ]
  for (const [view, id] of cases) {
    expect(contextualHelpHash(view)).toBe(`#/docs?guide=user-guide&article=${id}`)
    expect(helpArticles.some(article => article.id === id && article.area === 'user-guide')).toBe(true)
  }
  expect(contextualHelpHash({ page: 'docs' })).toBeUndefined()
  expect(contextualHelpHash({ page: 'access' })).toBeUndefined()
})
