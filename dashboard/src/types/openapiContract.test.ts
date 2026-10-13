// @vitest-environment jsdom

import { scenarioAuthoringFixture } from '../test/scenarioAuthoringFixtures'
import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'

import { useCapacityApi } from '../hooks/api/useCapacityApi'
import { useMonitoringCollectorsApi } from '../hooks/api/useMonitoringCollectorsApi'
import { useScriptApi } from '../hooks/api/useScriptApi'
import { useTestApi } from '../hooks/api/useTestApi'
import type {
  JsonRequestBody,
  JsonSuccessResponseBody,
} from './generated/openapi'
import {
  captureFetch,
  contractViolations,
  decodeCapturedBody,
  type CapturedRequest,
  type RequiredKeysOf,
} from './contractGuards'
import type { BrowserReplayStartRequest, ScriptDebugStartRequest } from './replay'
import type { ScriptDefinition } from './scripts'

type StartBody = JsonRequestBody<'handleStartScriptDebugScript'>
type StepBody = JsonRequestBody<'handleStepScriptDebugSession'>
type BreakpointsBody = JsonRequestBody<'handleUpdateScriptDebugBreakpoints'>
type OptionsBody = JsonRequestBody<'handleUpdateScriptDebugOptions'>
type StepSuccessBody = JsonSuccessResponseBody<'handleStepScriptDebugSession', '200'>
type DebugSessionContract = NonNullable<StepSuccessBody['session']>
type DebugRuntimeContract = DebugSessionContract['runtime_state']
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
    session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
    state_revision: '0',
    runtime_state: { version: 1, kind: 'full', session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: '0', state: 'unavailable', reason: 'pause_pending', snapshot: null },
    status: 'paused',
    group: 'checkout-debug',
    protocol: 'http',
    log_level: 'debug',
    current_event_index: 0,
    break_on_failure: false,
    continue_on_error: true,
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
    expectTypeOf<'session_incarnation' | 'state_revision' | 'runtime_state'>().toMatchTypeOf<RequiredKeysOf<DebugSessionContract>>()
    expectTypeOf<'version' | 'kind' | 'session_id' | 'session_incarnation' | 'state_revision' | 'state' | 'reason' | 'snapshot'>().toMatchTypeOf<RequiredKeysOf<DebugRuntimeContract>>()
    expectTypeOf<DebugSessionContract['state_revision']>().toEqualTypeOf<string>()
    expectTypeOf<DebugRuntimeContract['state_revision']>().toEqualTypeOf<string>()

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
          session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa',
          state_revision: '0',
          runtime_state: { version: 1, kind: 'full', session_id: 'debug-session-1', session_incarnation: 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', state_revision: '0', state: 'unavailable', reason: 'pause_pending', snapshot: null },
          status: 'paused',
          group: 'checkout-debug',
          protocol: 'http',
          log_level: 'debug',
          current_event_index: 0,
          break_on_failure: false,
          continue_on_error: true,
        },
      },
    })
  })

  it('types debug session starts by the generated contract, with only the script narrowed (AUTH-3)', () => {
    // The request has exactly the generated fields; the draft route takes the
    // same fields as the stored-script route.
    expectTypeOf<keyof ScriptDebugStartRequest>().toEqualTypeOf<keyof StartBody>()
    expectTypeOf<keyof JsonRequestBody<'handleStartScriptDebugDraftScript'>>().toEqualTypeOf<keyof StartBody>()
    // Every field but the script is the transport's own type.
    expectTypeOf<Omit<ScriptDebugStartRequest, 'script'>>().toEqualTypeOf<Omit<StartBody, 'script'>>()
    expectTypeOf<ScriptDebugStartRequest['script']>().toEqualTypeOf<ScriptDefinition | undefined>()
  })

  it('keeps the handwritten browser replay request aligned with the generated contract', () => {
    expectTypeOf<keyof BrowserReplayStartRequest>().toEqualTypeOf<keyof BrowserReplayBody>()
    expectTypeOf<'redaction_rules'>().toMatchTypeOf<keyof BrowserReplayStartRequest>()
    expectTypeOf<'log_level'>().not.toMatchTypeOf<keyof BrowserReplayStartRequest>()
  })
})

// ---------------------------------------------------------------------------
// Request bodies the dashboard actually puts on the wire.
//
// Everything above proves that the generated helper types resolve to concrete
// object shapes. No layer of the suite used to go further: nothing checked that
// a body the dashboard *sends* satisfies the contract of the handler it is sent
// to, and the 35 component tests that touch these endpoints `vi.mock` the API
// layer away entirely. The cases below drive the real domain hooks through the
// real `apiClient` with only `fetch` stubbed, so the bytes under assertion are
// the bytes a controller would receive.
//
// Each contract is checked twice over:
//   * `tsc` — the runtime key lists are pinned to `RequiredKeysOf<generated>` /
//     `keyof <generated>`, so a Go-side schema change breaks the build.
//   * `vitest` — the captured body is checked against those lists, so a client
//     that drops a required field or invents one the handler ignores fails.

type CreateDataPoolBody = JsonRequestBody<'handleCreateDataPool'>
type UpdateDataPoolBody = JsonRequestBody<'handleUpdateDataPool'>
type ScenarioDataPoolsBody = JsonRequestBody<'handleUpdateScenarioDataPools'>
type ScenarioDataPoolBindingBody = NonNullable<ScenarioDataPoolsBody['data_pools']>[number]
type CreateCollectorBody = JsonRequestBody<'handleCreateMonitoringCollector'>
type PatchCollectorBody = JsonRequestBody<'handlePatchMonitoringCollector'>
type GoalTargetBody = JsonRequestBody<'handleUpdateGoalTarget'>
type CreateScenarioScheduleBody = JsonRequestBody<'handleCreateScenarioSchedule'>
type CreateTimeslotBody = JsonRequestBody<'handleCreateCapacityReservation'>
type BindTimeslotBody = JsonRequestBody<'handleBindCapacityTimeslot'>

const CREATE_DATA_POOL_CONTRACT = {
  required: ['name'],
  known: ['columns', 'description', 'name', 'project_id', 'schema'],
} as const

// The Go handler uses `updated_at` as the optimistic-concurrency token, which
// is why it is the one required field on an otherwise all-optional patch.
const UPDATE_DATA_POOL_CONTRACT = {
  required: ['updated_at'],
  known: ['archived_at', 'columns', 'description', 'name', 'schema', 'updated_at'],
} as const

const SCENARIO_DATA_POOLS_CONTRACT = {
  required: [],
  known: ['data_pools'],
} as const

const SCENARIO_DATA_POOL_BINDING_CONTRACT = {
  required: [],
  known: [
    'access_mode',
    'alias',
    'created_at',
    'id',
    'live_options',
    'pool_id',
    'pool_name',
    'project_table_id',
    'scope',
    'scenario_id',
    'selection_strategy',
    'updated_at',
  ],
} as const

const CREATE_COLLECTOR_CONTRACT = {
  required: [],
  known: [
    'allowed_metric_packs',
    'created_at',
    'description',
    'health',
    'id',
    'last_seen_at',
    'name',
    'project_id',
    'updated_at',
  ],
} as const

const PATCH_COLLECTOR_CONTRACT = {
  required: [],
  known: ['allowed_metric_packs', 'description', 'health', 'name'],
} as const

const GOAL_TARGET_CONTRACT = {
  required: [],
  known: ['group', 'target_tps'],
} as const

const CREATE_SCENARIO_SCHEDULE_CONTRACT = {
  required: ['cron_expr', 'name'],
  known: [
    'approval_policy',
    'availability_windows',
    'blackout_windows',
    'cron_expr',
    'disabled_from',
    'disabled_until',
    'enabled',
    'expected_revision',
    'maintenance_windows',
    'missed_run_policy',
    'name',
    'release_id',
    'run_name_template',
    'timeslot_id',
    'timezone',
  ],
} as const

const CREATE_TIMESLOT_CONTRACT = {
  required: [],
  known: [
    'approval_id',
    'approval_state',
    'bound_test_id',
    'bound_test_status',
    'conflict_summary',
    'created_at',
    'end_at',
    'id',
    'project_id',
    'requested_capacity',
    'scenario_id',
    'start_at',
    'timezone',
    'updated_at',
  ],
} as const

const BIND_TIMESLOT_CONTRACT = {
  required: [],
  known: ['test_id'],
} as const

let capacityApi: ReturnType<typeof useCapacityApi>
let collectorsApi: ReturnType<typeof useMonitoringCollectorsApi>
let scriptApi: ReturnType<typeof useScriptApi>
let testApi: ReturnType<typeof useTestApi>

function CaptureApis() {
  capacityApi = useCapacityApi()
  collectorsApi = useMonitoringCollectorsApi()
  scriptApi = useScriptApi()
  testApi = useTestApi()
  return null
}

describe('request bodies the dashboard sends versus the generated handler contract', () => {
  let container: HTMLDivElement
  let root: Root
  let requests: CapturedRequest[]

  beforeEach(async () => {
    const capture = captureFetch({ ok: true })
    requests = capture.requests
    vi.stubGlobal('fetch', capture.fetchStub)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    await act(async () => {
      root.render(createElement(CaptureApis))
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.unstubAllGlobals()
  })

  it('pins every runtime key list to the generated handler contract', () => {
    expectTypeOf<(typeof CREATE_DATA_POOL_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<CreateDataPoolBody> & string
    >()
    expectTypeOf<(typeof CREATE_DATA_POOL_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof CreateDataPoolBody & string
    >()
    expectTypeOf<(typeof UPDATE_DATA_POOL_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<UpdateDataPoolBody> & string
    >()
    expectTypeOf<(typeof UPDATE_DATA_POOL_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof UpdateDataPoolBody & string
    >()
    expectTypeOf<(typeof SCENARIO_DATA_POOLS_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<ScenarioDataPoolsBody> & string
    >()
    expectTypeOf<(typeof SCENARIO_DATA_POOLS_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof ScenarioDataPoolsBody & string
    >()
    expectTypeOf<(typeof SCENARIO_DATA_POOL_BINDING_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<ScenarioDataPoolBindingBody> & string
    >()
    expectTypeOf<(typeof SCENARIO_DATA_POOL_BINDING_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof ScenarioDataPoolBindingBody & string
    >()
    expectTypeOf<(typeof CREATE_COLLECTOR_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<CreateCollectorBody> & string
    >()
    expectTypeOf<(typeof CREATE_COLLECTOR_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof CreateCollectorBody & string
    >()
    expectTypeOf<(typeof PATCH_COLLECTOR_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<PatchCollectorBody> & string
    >()
    expectTypeOf<(typeof PATCH_COLLECTOR_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof PatchCollectorBody & string
    >()
    expectTypeOf<(typeof GOAL_TARGET_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<GoalTargetBody> & string
    >()
    expectTypeOf<(typeof GOAL_TARGET_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof GoalTargetBody & string
    >()
    expectTypeOf<(typeof CREATE_SCENARIO_SCHEDULE_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<CreateScenarioScheduleBody> & string
    >()
    expectTypeOf<(typeof CREATE_SCENARIO_SCHEDULE_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof CreateScenarioScheduleBody & string
    >()
    expectTypeOf<(typeof CREATE_TIMESLOT_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<CreateTimeslotBody> & string
    >()
    expectTypeOf<(typeof CREATE_TIMESLOT_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof CreateTimeslotBody & string
    >()
    expectTypeOf<(typeof BIND_TIMESLOT_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<BindTimeslotBody> & string
    >()
    expectTypeOf<(typeof BIND_TIMESLOT_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof BindTimeslotBody & string
    >()
    expect(true).toBe(true)
  })

  it('sends data-pool create and update bodies the handlers accept', async () => {
    await act(async () => {
      await scriptApi.createDataPool({
        name: 'customers',
        description: 'checkout identities',
        project_id: 'project-1',
        schema: { email: 'string' },
      })
    })
    expect(requests[0].method).toBe('POST')
    expect(contractViolations(decodeCapturedBody(requests[0]), CREATE_DATA_POOL_CONTRACT)).toEqual([])

    await act(async () => {
      await scriptApi.updateDataPool('pool-1', {
        name: 'customers',
        description: 'checkout identities',
        // Optimistic-concurrency token; the handler requires it.
        updated_at: '2026-08-02T10:00:00Z',
      })
    })
    expect(requests[1].method).toBe('PUT')
    expect(contractViolations(decodeCapturedBody(requests[1]), UPDATE_DATA_POOL_CONTRACT)).toEqual([])
  })

  it('sends a scenario data-pool binding body the handler accepts, bindings included', async () => {
    await act(async () => {
      await scriptApi.updateScenarioDataPools('scenario-1', [
        {
          pool_id: 'pool-1',
          alias: 'customers',
          access_mode: 'consume',
          selection_strategy: 'unique',
        },
      ], scenarioAuthoringFixture({}, 'scenario-1'))
    })

    expect(requests[0].method).toBe('PUT')
    const body = decodeCapturedBody(requests[0]) as Record<string, unknown>
    expect(contractViolations(body, SCENARIO_DATA_POOLS_CONTRACT)).toEqual([])
    const bindings = body.data_pools as Array<Record<string, unknown>>
    expect(bindings).toHaveLength(1)
    for (const binding of bindings) {
      expect(contractViolations(binding, SCENARIO_DATA_POOL_BINDING_CONTRACT)).toEqual([])
    }
  })

  it('sends monitoring-collector create and patch bodies the handlers accept', async () => {
    await act(async () => {
      await collectorsApi.createCollector({
        project_id: 'project-1',
        name: 'edge-collector',
        description: 'us-east',
        allowed_metric_packs: ['node', 'process'],
      })
    })
    expect(contractViolations(decodeCapturedBody(requests[0]), CREATE_COLLECTOR_CONTRACT)).toEqual([])

    await act(async () => {
      await collectorsApi.patchCollector('collector-1', {
        name: 'edge-collector-2',
        health: 'degraded',
        allowed_metric_packs: ['node'],
      })
    })
    expect(requests[1].method).toBe('PATCH')
    expect(contractViolations(decodeCapturedBody(requests[1]), PATCH_COLLECTOR_CONTRACT)).toEqual([])
  })

  it('sends a goal-target patch body the handler accepts', async () => {
    await act(async () => {
      await testApi.updateGoalTarget('test-1', 'checkout', 42.5)
    })

    expect(requests[0].method).toBe('PATCH')
    expect(requests[0].url).toContain('/tests/test-1/goal-targets')
    expect(contractViolations(decodeCapturedBody(requests[0]), GOAL_TARGET_CONTRACT)).toEqual([])
  })

  it('sends a scenario-schedule create body the handler accepts', async () => {
    await act(async () => {
      await testApi.createScenarioSchedule('scenario-1', {
        name: 'Nightly',
        cron_expr: '0 2 * * *',
        timezone: 'America/New_York',
        enabled: true,
        approval_policy: 'auto',
        missed_run_policy: 'skip',
        blackout_windows: [{ name: 'freeze', start_time: '00:00', end_time: '06:00' }],
        expected_revision: 4,
      })
    })

    expect(requests[0].method).toBe('POST')
    expect(
      contractViolations(decodeCapturedBody(requests[0]), CREATE_SCENARIO_SCHEDULE_CONTRACT),
    ).toEqual([])
  })

  it('sends capacity timeslot create and bind bodies the handlers accept', async () => {
    await act(async () => {
      await capacityApi.createTimeslot({
        project_id: 'project-1',
        scenario_id: 'scenario-1',
        start_at: '2026-08-10T22:00:00Z',
        end_at: '2026-08-10T23:00:00Z',
        timezone: 'UTC',
        requested_capacity: { generators: 4, vusers: 2000, protocol: 'http', pool_id: 'pool-a' },
      })
    })
    expect(contractViolations(decodeCapturedBody(requests[0]), CREATE_TIMESLOT_CONTRACT)).toEqual([])

    await act(async () => {
      await capacityApi.bindTimeslot('timeslot-1', 'test-1')
    })
    expect(requests[1].url).toContain('/capacity/timeslots/timeslot-1/bind')
    expect(contractViolations(decodeCapturedBody(requests[1]), BIND_TIMESLOT_CONTRACT)).toEqual([])
  })
})
