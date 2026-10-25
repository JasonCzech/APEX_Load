// @vitest-environment jsdom

import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ReplayPreflight, ScriptDefinition } from '../types/scripts'
import ReplayPreflightCard from './ReplayPreflightCard'
import { replayReadinessIssues } from './replayReadinessIssues'

vi.mock('./replayReadinessIssues', async importOriginal => {
  const actual = await importOriginal<typeof import('./replayReadinessIssues')>()
  return { ...actual, replayReadinessIssues: vi.fn(actual.replayReadinessIssues) }
})

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// The editor renders the card on every render. Titling a blocking value can
// analyze the whole script, so it runs once per preflight and script.
describe('ReplayPreflightCard value titles', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.mocked(replayReadinessIssues).mockClear()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('titles blocking values once per preflight and script, not once per render', () => {
    const script: ScriptDefinition = { name: 's', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [
      { name: 'Use', type: 'GET', method: 'GET', target: '/{{param.region}}' },
    ] }
    const preflight: ReplayPreflight = {
      valid: false, action_count: 1, expected_action_count: 1, data_source_count: 0, parameter_count: 0, browser_required: false,
      blocking_variables: ['region'],
      issues: ['actions[0] Use: review gate missing_producer:region (execution blocker)'],
      checks: [{ id: 'review_gates', label: 'Review gates', status: 'fail', detail: 'missing_producer:region' }],
    }
    const render = (props: { preflight: ReplayPreflight, script: ScriptDefinition, compact: boolean }) => act(() => {
      root.render(<ReplayPreflightCard {...props} onSelectIssue={() => undefined} />)
    })

    render({ preflight, script, compact: false })
    render({ preflight, script, compact: true })
    render({ preflight, script, compact: false })
    expect(replayReadinessIssues).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('region')

    render({ preflight: { ...preflight }, script, compact: false })
    render({ preflight: { ...preflight }, script: { ...script }, compact: false })
    expect(replayReadinessIssues).toHaveBeenCalledTimes(3)
  })
})
