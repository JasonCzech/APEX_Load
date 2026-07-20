import { describe, expect, expectTypeOf, it } from 'vitest'

import type {
  JsonRequestBody,
  JsonSuccessResponseBody,
} from './generated/openapi'

type StartBody = JsonRequestBody<'handleStartScriptDebugScript'>
type StepBody = JsonRequestBody<'handleStepScriptDebugSession'>
type BreakpointsBody = JsonRequestBody<'handleUpdateScriptDebugBreakpoints'>
type OptionsBody = JsonRequestBody<'handleUpdateScriptDebugOptions'>
type StepSuccessBody = JsonSuccessResponseBody<'handleStepScriptDebugSession', '200'>

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
  session: { id: 'debug-session-1' },
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
    expectTypeOf<string>().toMatchTypeOf<keyof StepSuccessBody>()
    expectTypeOf<StepSuccessBody>().toMatchTypeOf<Record<string, unknown>>()

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
      successFixture: { session: { id: 'debug-session-1' } },
    })
  })
})
