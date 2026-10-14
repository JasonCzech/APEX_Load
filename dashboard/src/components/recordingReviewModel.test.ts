import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import { isNoReusableCorrelationNote } from './importIssueWorkspaceModel'
import { formatStepThinkTime, recordedScriptSteps, sentenceCaseNote } from './recordingReviewModel'

const step = (overrides: Partial<ScriptActionDefinition>): ScriptActionDefinition => ({
  name: 'GET /',
  type: 'http',
  method: 'GET',
  target: '/',
  ...overrides,
})

describe('recordedScriptSteps', () => {
  it('numbers every leaf step across phases and opens containers', () => {
    const script = {
      name: 'Recorded',
      version: '1.0.0',
      protocol: 'http',
      config: {},
      init: [step({ name: 'Login', method: 'post', target: '/login' })],
      actions: [
        {
          name: 'Maybe',
          type: 'control_if',
          target: '',
          control: { steps: [step({ name: 'Then' })], else: [step({ name: 'Else' })] },
        } as ScriptActionDefinition,
      ],
      teardown: [step({ name: 'Logout' })],
    } as unknown as ScriptDefinition
    expect(recordedScriptSteps(script).map(row => [row.index, row.name, row.method])).toEqual([
      [1, 'Login', 'POST'],
      [2, 'Then', 'GET'],
      [3, 'Else', 'GET'],
      [4, 'Logout', 'GET'],
    ])
    expect(recordedScriptSteps(null)).toEqual([])
  })
})

describe('formatStepThinkTime', () => {
  it('words a step pause and leaves the script default blank', () => {
    expect(formatStepThinkTime(step({ think_time: { type: 'fixed', mean_ms: 850 } }))).toBe('850 ms before')
    expect(formatStepThinkTime(step({ think_time: { type: 'fixed', mean_ms: 2500 }, think_time_after: true }))).toBe('2.5 s after')
    expect(formatStepThinkTime(step({ think_time: { type: 'fixed', mean_ms: 900, ignore: true } }))).toBe('')
    expect(formatStepThinkTime(step({ think_time: { type: 'fixed', mean_ms: 0 } }))).toBe('')
    expect(formatStepThinkTime(step({}))).toBe('')
  })
})

describe('conversion notes', () => {
  it('tells the no-correlation note from warnings and capitalizes plain notes only', () => {
    expect(isNoReusableCorrelationNote('no reusable correlation values were detected across the provided samples')).toBe(true)
    expect(isNoReusableCorrelationNote('body was truncated')).toBe(false)
    expect(sentenceCaseNote(' body was truncated')).toBe('Body was truncated')
    expect(sentenceCaseNote('aura.token was not found')).toBe('aura.token was not found')
    expect(sentenceCaseNote('http://shop.example/cart returned 500')).toBe('http://shop.example/cart returned 500')
    expect(sentenceCaseNote('')).toBe('')
  })
})
