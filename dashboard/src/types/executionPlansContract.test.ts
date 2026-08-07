// @vitest-environment jsdom

// Transport-seam coverage for the execution-plan API.
//
// The seam had no test at any layer: ExecutionPlansPanel.test.tsx mocks the API
// hook away and asserts only the create payload, and the Go handler test
// hand-writes its own request body, so the client and the server were each
// tested against a payload the other never produces. That is how
// `PUT /execution-plans/{id}` came to 400 on every save of an existing plan —
// the handler requires a positive `revision`, and the panel did not send one.
//
// This suite closes the loop from the client side: it drives the real
// `useExecutionPlansApi` hook through the real `apiClient` (real URL building,
// real `JSON.stringify`, real headers) with only `fetch` stubbed, captures the
// bytes that would reach the controller, and checks them against the generated
// contract for the operation the request actually routes to.
//
// Two independent failure modes are covered, deliberately:
//   * `vitest` fails if the client stops sending a field the handler requires.
//   * `tsc` fails if the Go handler's required-field set changes, because each
//     runtime key list is pinned to `RequiredKeysOf<generated body>`.
// Either half alone is the vacuous version of this test.

import { act, createElement } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, expectTypeOf, it, vi } from 'vitest'

import { useExecutionPlansApi } from '../hooks/api/useExecutionPlansApi'
import type { JsonRequestBody } from './generated/openapi'
import {
  captureFetch,
  contractViolations,
  decodeCapturedBody,
  type CapturedRequest,
  type RequiredKeysOf,
} from './contractGuards'
import type {
  ExecutionPlanEntry,
  ExecutionPlanScheduleRequest,
  ScheduleOccurrenceResolutionRequest,
} from './executionPlans'

type UpdatePlanBody = JsonRequestBody<'handleUpdateExecutionPlan'>
type CreatePlanBody = JsonRequestBody<'handleCreateExecutionPlan'>
type PlanEntryBody = NonNullable<UpdatePlanBody['entries']>[number]
type CreateScheduleBody = JsonRequestBody<'handleCreateExecutionPlanSchedule'>
type ResolveOccurrenceBody = JsonRequestBody<'handleResolveExecutionPlanScheduleOccurrence'>

// `PUT /execution-plans/{id}` rejects the body outright unless both of these
// are present; `revision` additionally has to be positive.
const UPDATE_PLAN_CONTRACT = {
  required: ['name', 'revision'],
  known: [
    'created_at',
    'description',
    'entries',
    'id',
    'name',
    'project_id',
    'revision',
    'run_mode',
    'status',
    'timeslot_id',
    'updated_at',
  ],
} as const

// Spelled out rather than aliased to UPDATE_PLAN_CONTRACT.known: the two lists
// happen to be identical today because both handlers decode the same
// `storage.ExecutionPlan`, but aliasing would validate the POST body against the
// PUT handler's field set and stay green through a create/update DTO split. The
// pin below is what forces this copy to be reconciled if that happens.
const CREATE_PLAN_CONTRACT = {
  required: ['name'],
  known: [
    'created_at',
    'description',
    'entries',
    'id',
    'name',
    'project_id',
    'revision',
    'run_mode',
    'status',
    'timeslot_id',
    'updated_at',
  ],
} as const

const PLAN_ENTRY_CONTRACT = {
  required: [],
  known: [
    'data_pools',
    'enabled',
    'failure_policy',
    'order',
    'runtime_overrides',
    'scenario_id',
    'scenario_name',
    'timeslot_id',
  ],
} as const

const CREATE_SCHEDULE_CONTRACT = {
  required: ['cron_expr'],
  known: [
    'cron_expr',
    'disabled_from',
    'disabled_until',
    'enabled',
    'expected_revision',
    'missed_run_policy',
    'name',
    'run_name_template',
    'timezone',
  ],
} as const

const RESOLVE_OCCURRENCE_CONTRACT = {
  required: ['action'],
  known: ['action', 'expected_revision', 'reason'],
} as const

// The payload ExecutionPlansPanel.submitDraft builds for an existing plan
// (ExecutionPlansPanel.tsx `draftToEntries`), reproduced field-for-field.
const panelEntries: ExecutionPlanEntry[] = [
  {
    order: 0,
    scenario_id: 'scenario-checkout',
    enabled: true,
    failure_policy: 'continue',
    data_pools: [
      {
        pool_id: 'pool-customers',
        alias: 'customers',
        access_mode: 'read_only',
        selection_strategy: 'sequential',
      },
    ],
  },
]

let api: ReturnType<typeof useExecutionPlansApi>

function Capture() {
  api = useExecutionPlansApi()
  return null
}

describe('execution-plan transport seam', () => {
  let container: HTMLDivElement
  let root: Root
  let requests: CapturedRequest[]

  beforeEach(async () => {
    const capture = captureFetch({ id: 'plan-1', revision: 8 })
    requests = capture.requests
    vi.stubGlobal('fetch', capture.fetchStub)
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    await act(async () => {
      root.render(createElement(Capture))
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.unstubAllGlobals()
  })

  // If the Go handler's required set ever moves, these fail to compile and the
  // runtime lists below have to be updated with it — that is the whole point.
  it('pins the runtime key lists to the generated handler contract', () => {
    expectTypeOf<(typeof UPDATE_PLAN_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<UpdatePlanBody> & string
    >()
    expectTypeOf<(typeof UPDATE_PLAN_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof UpdatePlanBody & string
    >()
    expectTypeOf<(typeof CREATE_PLAN_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<CreatePlanBody> & string
    >()
    expectTypeOf<(typeof CREATE_PLAN_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof CreatePlanBody & string
    >()
    expectTypeOf<(typeof PLAN_ENTRY_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<PlanEntryBody> & string
    >()
    expectTypeOf<(typeof PLAN_ENTRY_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof PlanEntryBody & string
    >()
    expectTypeOf<(typeof CREATE_SCHEDULE_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<CreateScheduleBody> & string
    >()
    expectTypeOf<(typeof CREATE_SCHEDULE_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof CreateScheduleBody & string
    >()
    expectTypeOf<(typeof RESOLVE_OCCURRENCE_CONTRACT.required)[number]>().toEqualTypeOf<
      RequiredKeysOf<ResolveOccurrenceBody> & string
    >()
    expectTypeOf<(typeof RESOLVE_OCCURRENCE_CONTRACT.known)[number]>().toEqualTypeOf<
      keyof ResolveOccurrenceBody & string
    >()
    expect(true).toBe(true)
  })

  it('serializes a plan update the handler accepts, revision included', async () => {
    await act(async () => {
      await api.updateExecutionPlan('plan-1', {
        revision: 8,
        name: 'Checkout regression',
        run_mode: 'sequential',
        entries: panelEntries,
      })
    })

    expect(requests).toHaveLength(1)
    expect(requests[0].method).toBe('PUT')
    expect(requests[0].url).toContain('/api/v1/execution-plans/plan-1')

    const body = decodeCapturedBody(requests[0]) as Record<string, unknown>
    expect(contractViolations(body, UPDATE_PLAN_CONTRACT)).toEqual([])
    // The handler rejects `revision <= 0` before touching storage, so presence
    // alone is not enough — the value has to survive serialization intact.
    expect(typeof body.revision).toBe('number')
    expect(body.revision as number).toBeGreaterThan(0)

    const entries = body.entries as Array<Record<string, unknown>>
    expect(entries).toHaveLength(1)
    for (const entry of entries) {
      expect(contractViolations(entry, PLAN_ENTRY_CONTRACT)).toEqual([])
    }
  })

  it('fails the same check when a caller omits the revision the handler demands', async () => {
    await act(async () => {
      // Exactly the payload the panel used to send: name, run_mode, entries.
      await api.updateExecutionPlan('plan-1', {
        name: 'Checkout regression',
        run_mode: 'sequential',
        entries: panelEntries,
      })
    })

    const body = decodeCapturedBody(requests[0])
    expect(contractViolations(body, UPDATE_PLAN_CONTRACT)).toEqual([
      'missing required field "revision"',
    ])
  })

  it('serializes a plan create the handler accepts', async () => {
    await act(async () => {
      await api.createExecutionPlan({
        project_id: 'project-1',
        name: 'Checkout regression',
        run_mode: 'parallel',
        entries: panelEntries,
      })
    })

    expect(requests[0].method).toBe('POST')
    const body = decodeCapturedBody(requests[0])
    expect(contractViolations(body, CREATE_PLAN_CONTRACT)).toEqual([])
  })

  it('serializes a plan schedule create the handler accepts', async () => {
    const scheduleRequest: ExecutionPlanScheduleRequest = {
      name: 'Nightly',
      cron_expr: '0 2 * * *',
      timezone: 'America/New_York',
      enabled: true,
      missed_run_policy: 'skip',
      expected_revision: 3,
    }

    await act(async () => {
      await api.createExecutionPlanSchedule('plan-1', scheduleRequest)
    })

    expect(requests[0].method).toBe('POST')
    expect(requests[0].url).toContain('/execution-plans/plan-1/schedules')
    expect(contractViolations(decodeCapturedBody(requests[0]), CREATE_SCHEDULE_CONTRACT)).toEqual([])
  })

  it('serializes an occurrence resolution the handler accepts', async () => {
    const resolution: ScheduleOccurrenceResolutionRequest = {
      action: 'catch_up',
      reason: 'controller restart',
      expected_revision: 3,
    }

    await act(async () => {
      await api.resolveExecutionPlanScheduleOccurrence('plan-1', 'sched-1', 'occ-1', resolution)
    })

    expect(requests[0].method).toBe('POST')
    expect(requests[0].url).toContain('/occurrences/occ-1/resolve')
    expect(
      contractViolations(decodeCapturedBody(requests[0]), RESOLVE_OCCURRENCE_CONTRACT),
    ).toEqual([])
  })
})
