import { describe, expect, expectTypeOf, it } from 'vitest'

import type {
  JsonRequestBody,
  JsonSuccessResponseBody,
} from './generated/openapi'
import type { BrowserReplayStartRequest } from './replay'

type StartBody = JsonRequestBody<'handleStartScriptDebugScript'>
type StepBody = JsonRequestBody<'handleStepScriptDebugSession'>
type BreakpointsBody = JsonRequestBody<'handleUpdateScriptDebugBreakpoints'>
type OptionsBody = JsonRequestBody<'handleUpdateScriptDebugOptions'>
type StepSuccessBody = JsonSuccessResponseBody<'handleStepScriptDebugSession', '200'>
type BrowserReplayBody = JsonRequestBody<'handleStartBrowserReplayDraftScript'>

const startFixture = {
  group_name: 'checkout-debug',
  start_paused: true,
  log_level: 'debug',
} satisfies StartBody

const stepFixture = { mode: 'over' } satisfies StepBody

const breakpointsFixture = {
  breakpoints: [
    {
      phase: 'action',
      action_index: 3,
      request_index: 1,
      step_path: ['requests', '1'],
    },
  ],
} satisfies BreakpointsBody

const optionsFixture = { break_on_failure: true } satisfies OptionsBody

const successFixture = {
  session: {
    id: 'debug-session-1',
    status: 'paused',
    group: 'checkout-debug',
    protocol: 'http',
    log_level: 'debug',
    current_event_index: 0,
    break_on_failure: false,
  },
} satisfies StepSuccessBody

describe('generated OpenAPI transport helpers', () => {
	it('resolve script-debug contracts to concrete object shapes', () => {
    expectTypeOf<StartBody>().not.toBeAny()
    expectTypeOf<'group_name'>().toMatchTypeOf<keyof StartBody>()
    expectTypeOf<StartBody>().toMatchTypeOf<{ group_name?: string; start_paused?: boolean }>()

    expectTypeOf<StepBody>().not.toBeAny()
    expectTypeOf<'mode'>().toMatchTypeOf<keyof StepBody>()
    expectTypeOf<StepBody>().toMatchTypeOf<{ mode?: 'into' | 'over' | 'loop_end' }>()

    expectTypeOf<BreakpointsBody>().not.toBeAny()
    expectTypeOf<'breakpoints'>().toMatchTypeOf<keyof BreakpointsBody>()
    expectTypeOf<BreakpointsBody>().toMatchTypeOf<{
      breakpoints: Array<{ phase: 'init' | 'action' | 'teardown'; action_index: number }>
    }>()

    expectTypeOf<OptionsBody>().not.toBeAny()
    expectTypeOf<'break_on_failure'>().toMatchTypeOf<keyof OptionsBody>()
    expectTypeOf<OptionsBody>().toMatchTypeOf<{ break_on_failure: boolean }>()

    expectTypeOf<StepSuccessBody>().not.toBeAny()
    expectTypeOf<'session'>().toMatchTypeOf<keyof StepSuccessBody>()
    expectTypeOf<StepSuccessBody>().toMatchTypeOf<{ session?: { id?: string } | null }>()

    expect({
      startFixture,
      stepFixture,
      breakpointsFixture,
      optionsFixture,
      successFixture,
    }).toEqual({
      startFixture: {
        group_name: 'checkout-debug',
        start_paused: true,
        log_level: 'debug',
      },
      stepFixture: { mode: 'over' },
      breakpointsFixture: {
        breakpoints: [
          {
            phase: 'action',
            action_index: 3,
            request_index: 1,
            step_path: ['requests', '1'],
          },
        ],
      },
      optionsFixture: { break_on_failure: true },
      successFixture: {
        session: {
          id: 'debug-session-1',
          status: 'paused',
          group: 'checkout-debug',
          protocol: 'http',
          log_level: 'debug',
          current_event_index: 0,
          break_on_failure: false,
        },
      },
    })
  })

  it('keeps the handwritten browser replay request aligned with the generated contract', () => {
    expectTypeOf<keyof BrowserReplayStartRequest>().toEqualTypeOf<keyof BrowserReplayBody>()
    expectTypeOf<'redaction_rules'>().toMatchTypeOf<keyof BrowserReplayStartRequest>()
    expectTypeOf<'log_level'>().not.toMatchTypeOf<keyof BrowserReplayStartRequest>()
  })
})
