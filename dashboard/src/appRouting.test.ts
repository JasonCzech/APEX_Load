import { describe, expect, it } from 'vitest'

import { parseAppViewFromHash, viewToLocationHash } from './appRouting'

describe('appRouting', () => {
  it('defaults empty and invalid hashes to the dashboard', () => {
    expect(parseAppViewFromHash('')).toEqual({ page: 'dashboard' })
    expect(parseAppViewFromHash('#/unknown')).toEqual({ page: 'dashboard' })
  })

  it('parses refreshable top-level app pages', () => {
    expect(parseAppViewFromHash('#/results')).toEqual({ page: 'results' })
    expect(parseAppViewFromHash('#/test/run-1?tab=archive')).toEqual({ page: 'test', testId: 'run-1', tab: 'archive' })
    expect(parseAppViewFromHash('#/test/run-1?tab=not-real')).toEqual({ page: 'test', testId: 'run-1' })
    expect(parseAppViewFromHash('#/command-center')).toEqual({ page: 'command-center' })
    expect(parseAppViewFromHash('#/command-center/run-1')).toEqual({ page: 'command-center', testId: 'run-1' })
    expect(parseAppViewFromHash('#/command-center?testId=run-2')).toEqual({ page: 'command-center', testId: 'run-2' })
    expect(parseAppViewFromHash('#/new-test')).toEqual({ page: 'new-test' })
    expect(parseAppViewFromHash('#/tests')).toEqual({ page: 'tests' })
    expect(parseAppViewFromHash('#/tests?view=builder')).toEqual({ page: 'tests', scenarioView: 'builder' })
    expect(parseAppViewFromHash('#/docs')).toEqual({ page: 'docs' })
    expect(parseAppViewFromHash('#/docs?guide=authentication')).toEqual({ page: 'docs', guidePage: 'authentication' })
    expect(parseAppViewFromHash('#/docs?guide=not-real')).toEqual({ page: 'docs' })
    expect(parseAppViewFromHash('#/repository')).toEqual({ page: 'repository' })
    expect(parseAppViewFromHash('#/swagger')).toEqual({ page: 'swagger' })
    expect(parseAppViewFromHash('#/repository-guide')).toEqual({ page: 'repository-guide' })
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
    expect(viewToLocationHash({ page: 'swagger' })).toBe('#/swagger')
    expect(viewToLocationHash({ page: 'docs' })).toBe('#/docs')
    expect(viewToLocationHash({ page: 'docs', guidePage: 'general' })).toBe('#/docs')
    expect(viewToLocationHash({ page: 'docs', guidePage: 'mcp' })).toBe('#/docs?guide=mcp')
    expect(viewToLocationHash({ page: 'command-center' })).toBe('#/command-center')
    expect(viewToLocationHash({ page: 'test', testId: 'run/123', tab: 'archive' })).toBe('#/test/run%2F123?tab=archive')
    expect(viewToLocationHash({ page: 'command-center', testId: 'run/123' })).toBe('#/command-center/run%2F123')
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'repository-guide' }))).toEqual({ page: 'repository-guide' })
    expect(viewToLocationHash({ page: 'recorder-panel', sessionId: 'brec/1' })).toBe('#/recorder-panel?session=brec%2F1')
    expect(parseAppViewFromHash('#/recorder-panel?session=brec-1')).toEqual({ page: 'recorder-panel', sessionId: 'brec-1' })
    expect(parseAppViewFromHash(viewToLocationHash({ page: 'recorder-panel', sessionId: 'brec/1' }))).toEqual({
      page: 'recorder-panel',
      sessionId: 'brec/1',
    })
    expect(parseAppViewFromHash('#/recorder-panel')).toEqual({ page: 'recorder-panel' })
  })
})
