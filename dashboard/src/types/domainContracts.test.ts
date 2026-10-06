// Drift guard between the hand-written dashboard domain types and the
// generated Go contract.
//
// `src/types/*.ts` hand-mirrors Go DTOs across ~4,900 lines. Nothing used to
// compare them to the server: the file that previously carried this name
// imported each type twice from a re-export barrel and asserted `A === A`, so
// it could not fail no matter how far the Go side moved. This suite compares
// the hand-written types against `types/generated/openapi.ts`, which is emitted
// from the same handler annotations that produce docs/swagger.yaml and is
// therefore the honest machine-readable statement of what the controller
// serves.
//
// Shape of every guard below:
//
//   1. `IsUntypedJsonObject` must be `false` for the generated body. 195 of the
//      394 generated JSON 200-responses carry no schema at all — 129 are
//      `Record<string, unknown>`, 34 are `Record<string, string>`, 32 are bare
//      `unknown` — because the Go annotation declares none, and no structural
//      assertion means anything against such a type. For the map shapes steps 2
//      and 4 pass vacuously while step 3 stops compiling, because `keyof
//      Record<string, unknown>` is `string` and the `Exclude` can never reduce
//      to `never`; for bare `unknown` the roles swap, and it is steps 3 and 4
//      that pass vacuously while step 2 stops compiling. This step guarantees
//      that a regeneration which degrades a schema produces at least one
//      diagnostic naming the cause. It does not silence the rest — a keyless
//      body also breaks the `Body['field']` projections the aliases below are
//      built from — so read this step's failure first and the collateral
//      second. Note the aliases wrap the generated body in `NonNullable<...>`;
//      `NonNullable<unknown>` is `{}`, which is why `IsUntypedJsonObject`
//      detects on `keyof` rather than on `unknown` itself.
//   2. `Exclude<keyof Handwritten, keyof Generated>` must be `never` — the
//      dashboard may not invent fields the server does not serve.
//   3. `Exclude<keyof Generated, keyof Handwritten>` must equal an explicit
//      union naming every field the dashboard knowingly does not model. A new
//      Go field lands in this set and breaks the build, which is the drift the
//      guard exists to catch.
//   4. `HandwrittenScalars<H, G>` must be assignable to `GeneratedScalars<H, G>`
//      — the fields both sides declare that at least one of them models as a
//      scalar. Steps 2 and 3 compare property *names* only, so a Go `string`
//      that becomes an `int`, or a Go `string` that becomes a struct, leaves
//      both `Exclude` results `never` and slips through; this step is what makes
//      a field's *type* part of the guard. See `HandwrittenScalars` in
//      `contractGuards.ts` for why nullability, optionality and fields *both*
//      sides model as objects are deliberately out of scope — the generator
//      over-approximates all three, so comparing them would report divergences
//      the wire does not have.
//
// These are type-level assertions, so `npm run typecheck` (CI) is what enforces
// them; the runtime `expect` at the end of each block keeps `vitest run` from
// reporting an empty test. Coverage is deliberately depth-first over ten
// high-churn domains rather than nominal coverage of all 4,900 lines. Adding a
// domain means finding an operation whose generated body carries a real schema
// — request bodies almost always do, 200-responses often do not, see
// `IsUntypedJsonObject` — and repeating the four steps above; the remaining
// uncovered domains are listed in the review as O50.

import { describe, expect, expectTypeOf, it } from 'vitest'

import type {
  JsonRequestBody,
  JsonSuccessResponseBody,
} from './generated/openapi'
import type {
  GeneratedScalars,
  HandwrittenScalars,
  IsUntypedJsonObject,
} from './contractGuards'
import type {
  CapacityTimeslot,
  CapacityTimeslotRequest,
} from './capacity'
import type {
  ScenarioDataPoolBinding,
  SharedDataPool,
  SharedDataPoolRow,
} from './dataPools'
import type {
  AllocationPreflight,
  PoolAllocation,
  PoolAllocationInput,
  PoolAllocationResponse,
  Toleration,
} from './cluster'
import type {
  DomainDeploymentTarget,
  DomainDeploymentTargetInput,
  DomainDeploymentTargetListResponse,
} from './deploymentTarget'
import type {
  ExecutionPlan,
  ExecutionPlanEntry,
} from './executionPlans'
import type {
  FleetAlert,
  FleetAlertsResponse,
  FleetCapacityResponse,
  FleetGeneratorMetadata,
  FleetPool,
  FleetPoolMember,
  FleetProjectSlotAccounting,
} from './fleet'
import type { GoalTargetStatus } from './goals'
import type { MonitoringCollector } from './monitoring'
import type {
  ScenarioSchedule,
  ScenarioScheduleGovernance,
  ScenarioScheduleOccurrence,
  ScenarioScheduleWindow,
} from './scheduling'
import type {
  GeneratorAssignmentSettings,
  TestGroup,
} from './testConfiguration'
// Not a `src/types` domain type: this is the request type `useFleetApi`
// serializes into the generator PATCH body, guarded alongside the read type it
// is easily confused with. Type-only, so nothing is imported at runtime.
import type { GeneratorMetadataPatch } from '../hooks/api/useFleetApi'

/** Fields the dashboard declares that the generated contract does not. */
type Invented<Handwritten, Generated> = Exclude<keyof Handwritten, keyof Generated>
/** Fields the generated contract declares that the dashboard does not model. */
type Unmodelled<Handwritten, Generated> = Exclude<keyof Generated, keyof Handwritten>
// `HandwrittenScalars`/`GeneratedScalars` (contractGuards.ts) are the two sides
// of step 4: the same key set — every field both types declare that at least one
// of them calls a scalar — typed as the dashboard declares it and as the
// generated contract declares it. Asserting the first `toMatchTypeOf` the second
// is what makes a field's type, not just its name, part of the guard.

// --- execution plans -------------------------------------------------------
type GetPlanBody = NonNullable<JsonSuccessResponseBody<'handleGetExecutionPlan', '200'>>
type GetPlanEntry = GetPlanBody['entries'][number]

// --- capacity timeslots ----------------------------------------------------
type BoundTimeslotBody = NonNullable<
  JsonSuccessResponseBody<'handleBindCapacityTimeslot', '200'>
>
type GeneratedTimeslotRequest = NonNullable<BoundTimeslotBody['requested_capacity']>

// --- monitoring collectors -------------------------------------------------
type GetCollectorBody = NonNullable<
  JsonSuccessResponseBody<'handleGetMonitoringCollector', '200'>
>

// --- shared data pools -----------------------------------------------------
type GetDataPoolBody = JsonSuccessResponseBody<'handleGetDataPool', '200'>
type GeneratedDataPool = NonNullable<GetDataPoolBody['data_pool']>
type GeneratedDataPoolRow = NonNullable<GetDataPoolBody['rows'][number]>
type GeneratedPoolBinding = NonNullable<
  JsonRequestBody<'handleUpdateScenarioDataPools'>['data_pools']
>[number]

// --- goal targets ----------------------------------------------------------
type GoalTargetBody = NonNullable<JsonSuccessResponseBody<'handleUpdateGoalTarget', '200'>>
type GeneratedGoalTarget = GoalTargetBody['goal_targets'][string]

// --- scenario schedules ----------------------------------------------------
type ScenarioScheduleBody = NonNullable<
  JsonSuccessResponseBody<'handleUpdateScenarioSchedule', '200'>
>
type GeneratedScheduleOccurrence = NonNullable<ScenarioScheduleBody['last_occurrence']>
type GeneratedScheduleWindow = NonNullable<ScenarioScheduleBody['blackout_windows']>[number]
type GeneratedScheduleGovernance = NonNullable<ScenarioScheduleBody['governance']>

// --- fleet pools, host allocation and capacity -----------------------------
// The allocation block carries node placement, so a field that silently stops
// being modelled here does not fail a request — it schedules generator pods
// somewhere else. Both the pool projection that reads it back and the PUT body
// that writes it are guarded.
type GetFleetPoolBody = NonNullable<JsonSuccessResponseBody<'handleGetFleetPool', '200'>>
type GeneratedFleetPoolMember = NonNullable<NonNullable<GetFleetPoolBody['members']>[number]>
type GeneratedPoolAllocation = NonNullable<GetFleetPoolBody['allocation']>
type GeneratedToleration = NonNullable<GeneratedPoolAllocation['tolerations']>[number]
type UpdateAllocationBody = JsonRequestBody<'handleUpdateFleetPoolAllocation'>
type UpdateAllocationResult = NonNullable<
  JsonSuccessResponseBody<'handleUpdateFleetPoolAllocation', '200'>
>
type GeneratedAllocationPreflight = NonNullable<UpdateAllocationResult['preflight']>
type FleetCapacityBody = NonNullable<JsonSuccessResponseBody<'handleGetFleetCapacity', '200'>>
type GeneratedProjectSlotAccounting = NonNullable<FleetCapacityBody['project_slots']>[number]
type FleetAlertsBody = NonNullable<JsonSuccessResponseBody<'handleGetFleetAlerts', '200'>>
type GeneratedFleetAlert = FleetAlertsBody['alerts'][number]
// No operation serves `FleetGeneratorMetadata` under a schema: the list op
// (`handleListGeneratorMetadata`) is `Record<string, unknown>` and the only
// single-resource route, PATCH /fleet/generators/{id}, returns `unknown`. Its
// *request* body is the one schema'd surface that names the same fields, so the
// guard runs against that — see the generator-metadata block below for what
// that does and does not cover.
type PatchGeneratorMetadataBody = JsonRequestBody<'handlePatchGeneratorMetadata'>

// --- domain deployment targets ---------------------------------------------
type DeploymentTargetListBody = NonNullable<
  JsonSuccessResponseBody<'handleListDomainDeploymentTargets', '200'>
>
type GeneratedDeploymentTarget = DeploymentTargetListBody['targets'][number]
type UpdateDeploymentTargetBody = JsonRequestBody<'handleUpdateDomainDeploymentTarget'>

// --- test groups -----------------------------------------------------------
type GetTestBody = NonNullable<JsonSuccessResponseBody<'handleGetTest', '200'>>
type GeneratedTestGroup = GetTestBody['groups'][number]
type GeneratedAssignment = NonNullable<GeneratedTestGroup['assignment']>

describe('hand-written domain types versus the generated Go contract', () => {
  // A guard that compares against `Record<string, unknown>` proves nothing.
  // Every operation this suite leans on has to carry a real schema.
  it('only guards operations whose generated body actually has a schema', () => {
    expectTypeOf<IsUntypedJsonObject<GetPlanBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<BoundTimeslotBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<GetCollectorBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<GetDataPoolBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<GeneratedPoolBinding>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<GoalTargetBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<ScenarioScheduleBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<GetTestBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<GetFleetPoolBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<UpdateAllocationBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<UpdateAllocationResult>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<FleetCapacityBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<FleetAlertsBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<PatchGeneratorMetadataBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<DeploymentTargetListBody>>().toEqualTypeOf<false>()
    expectTypeOf<IsUntypedJsonObject<UpdateDeploymentTargetBody>>().toEqualTypeOf<false>()

    // Sanity checks on the detector itself: an unschema'd operation and a
    // partially-schema'd one must both be reported as untyped, otherwise step 1
    // above is decoration too.
    expectTypeOf<
      IsUntypedJsonObject<JsonSuccessResponseBody<'handleGetEntitlements', '200'>>
    >().toEqualTypeOf<true>()
    expectTypeOf<
      IsUntypedJsonObject<JsonRequestBody<'handleCreateDataPoolRow'>>
    >().toEqualTypeOf<true>()
    // The two above are both index-signature shapes, which only exercise the
    // detector's first branch. Every alias in the step-1 list is written as
    // `NonNullable<JsonSuccessResponseBody<...>>`, and `NonNullable<unknown>`
    // is `{}` — not `unknown` — so a body degraded to bare `unknown` reaches
    // the detector keyless rather than as `unknown`. That is the shape 32 of
    // the 394 generated 200-responses already have, so pin it against a real
    // one: PATCH /fleet/generators/{id} returns bare `unknown` today (the
    // generator-metadata block below is built on its *request* body for
    // exactly that reason). If the Go side ever annotates that response, this
    // assertion fails and that block's comment needs updating with it.
    expectTypeOf<
      IsUntypedJsonObject<
        NonNullable<JsonSuccessResponseBody<'handlePatchGeneratorMetadata', '200'>>
      >
    >().toEqualTypeOf<true>()
    expect(true).toBe(true)
  })

  // Sanity checks on step 4's comparison itself, on synthetic pairs whose key
  // sets are identical so steps 2 and 3 cannot be what fails. Without these the
  // step-4 assertions below could quietly stop comparing anything — a field that
  // drops out of both mapped types passes an assignability check vacuously.
  it('rejects a field whose type moved even though its name did not', () => {
    type Scalar = { id: string; health: string }

    // Matching scalars pass — the baseline the real assertions rely on.
    expectTypeOf<HandwrittenScalars<Scalar, Scalar>>().toMatchTypeOf<
      GeneratedScalars<Scalar, Scalar>
    >()

    // A Go `string` that became an `int`.
    type Retyped = { id: string; health: number }
    expectTypeOf<Exclude<keyof Scalar, keyof Retyped>>().toEqualTypeOf<never>()
    expectTypeOf<Exclude<keyof Retyped, keyof Scalar>>().toEqualTypeOf<never>()
    expectTypeOf<HandwrittenScalars<Scalar, Retyped>>().not.toMatchTypeOf<
      GeneratedScalars<Scalar, Retyped>
    >()

    // A Go `string` that became a struct: the key stays put, so only the
    // one-sided scalar comparison can catch it.
    type Structured = { id: string; health: { state: string; since: string } }
    expectTypeOf<Exclude<keyof Scalar, keyof Structured>>().toEqualTypeOf<never>()
    expectTypeOf<Exclude<keyof Structured, keyof Scalar>>().toEqualTypeOf<never>()
    expectTypeOf<HandwrittenScalars<Scalar, Structured>>().not.toMatchTypeOf<
      GeneratedScalars<Scalar, Structured>
    >()

    // And the mirror: the dashboard modelling an object the server sends as a
    // scalar.
    expectTypeOf<HandwrittenScalars<Structured, Scalar>>().not.toMatchTypeOf<
      GeneratedScalars<Structured, Scalar>
    >()

    // A field *both* sides model as an object stays out of scope, because the
    // generator's rendering of nested structs does not reflect the wire.
    type Degraded = { id: string; health: Record<string, unknown> }
    expectTypeOf<HandwrittenScalars<Structured, Degraded>>().toMatchTypeOf<
      GeneratedScalars<Structured, Degraded>
    >()

    expect(true).toBe(true)
  })

  it('keeps execution-plan types aligned with GET /execution-plans/{id}', () => {
    expectTypeOf<Invented<ExecutionPlan, GetPlanBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<ExecutionPlan, GetPlanBody>>().toEqualTypeOf<never>()
    expectTypeOf<Invented<ExecutionPlanEntry, GetPlanEntry>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<ExecutionPlanEntry, GetPlanEntry>>().toEqualTypeOf<never>()

    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<ExecutionPlan, GetPlanBody>>().toMatchTypeOf<
      GeneratedScalars<ExecutionPlan, GetPlanBody>
    >()
    expectTypeOf<HandwrittenScalars<ExecutionPlanEntry, GetPlanEntry>>().toMatchTypeOf<
      GeneratedScalars<ExecutionPlanEntry, GetPlanEntry>
    >()

    // `revision` is the field whose absence made every Save of an existing
    // plan 400. Pin it as required on both sides so it cannot be dropped again.
    expectTypeOf<ExecutionPlan['revision']>().toEqualTypeOf<number>()
    expectTypeOf<GetPlanBody['revision']>().toEqualTypeOf<number>()
    expect(true).toBe(true)
  })

  it('keeps capacity-timeslot types aligned with the capacity API', () => {
    expectTypeOf<Invented<CapacityTimeslot, BoundTimeslotBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<CapacityTimeslot, BoundTimeslotBody>>().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<CapacityTimeslotRequest, GeneratedTimeslotRequest>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<CapacityTimeslotRequest, GeneratedTimeslotRequest>
    >().toEqualTypeOf<never>()
    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<CapacityTimeslot, BoundTimeslotBody>>().toMatchTypeOf<
      GeneratedScalars<CapacityTimeslot, BoundTimeslotBody>
    >()
    expectTypeOf<
      HandwrittenScalars<CapacityTimeslotRequest, GeneratedTimeslotRequest>
    >().toMatchTypeOf<
      GeneratedScalars<CapacityTimeslotRequest, GeneratedTimeslotRequest>
    >()

    expect(true).toBe(true)
  })

  it('keeps monitoring-collector types aligned with GET /monitoring/collectors/{id}', () => {
    expectTypeOf<Invented<MonitoringCollector, GetCollectorBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<MonitoringCollector, GetCollectorBody>>().toEqualTypeOf<never>()
    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<MonitoringCollector, GetCollectorBody>>().toMatchTypeOf<
      GeneratedScalars<MonitoringCollector, GetCollectorBody>
    >()

    expect(true).toBe(true)
  })

  it('keeps shared data-pool types aligned with the data-pool API', () => {
    expectTypeOf<Invented<SharedDataPool, GeneratedDataPool>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<SharedDataPool, GeneratedDataPool>>().toEqualTypeOf<never>()
    expectTypeOf<Invented<SharedDataPoolRow, GeneratedDataPoolRow>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<SharedDataPoolRow, GeneratedDataPoolRow>>().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<ScenarioDataPoolBinding, GeneratedPoolBinding>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<ScenarioDataPoolBinding, GeneratedPoolBinding>
    >().toEqualTypeOf<never>()
    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<SharedDataPool, GeneratedDataPool>>().toMatchTypeOf<
      GeneratedScalars<SharedDataPool, GeneratedDataPool>
    >()
    expectTypeOf<HandwrittenScalars<SharedDataPoolRow, GeneratedDataPoolRow>>().toMatchTypeOf<
      GeneratedScalars<SharedDataPoolRow, GeneratedDataPoolRow>
    >()
    expectTypeOf<HandwrittenScalars<ScenarioDataPoolBinding, GeneratedPoolBinding>>().toMatchTypeOf<
      GeneratedScalars<ScenarioDataPoolBinding, GeneratedPoolBinding>
    >()

    expect(true).toBe(true)
  })

  it('keeps goal-target status aligned with PATCH /tests/{id}/goal-targets', () => {
    expectTypeOf<Invented<GoalTargetStatus, GeneratedGoalTarget>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<GoalTargetStatus, GeneratedGoalTarget>>().toEqualTypeOf<never>()
    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<GoalTargetStatus, GeneratedGoalTarget>>().toMatchTypeOf<
      GeneratedScalars<GoalTargetStatus, GeneratedGoalTarget>
    >()

    expect(true).toBe(true)
  })

  it('keeps scenario-schedule types aligned with the schedule API', () => {
    expectTypeOf<Invented<ScenarioSchedule, ScenarioScheduleBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<ScenarioSchedule, ScenarioScheduleBody>>().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<ScenarioScheduleOccurrence, GeneratedScheduleOccurrence>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<ScenarioScheduleOccurrence, GeneratedScheduleOccurrence>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<ScenarioScheduleWindow, GeneratedScheduleWindow>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<ScenarioScheduleWindow, GeneratedScheduleWindow>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<ScenarioScheduleGovernance, GeneratedScheduleGovernance>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<ScenarioScheduleGovernance, GeneratedScheduleGovernance>
    >().toEqualTypeOf<never>()
    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<ScenarioSchedule, ScenarioScheduleBody>>().toMatchTypeOf<
      GeneratedScalars<ScenarioSchedule, ScenarioScheduleBody>
    >()
    expectTypeOf<
      HandwrittenScalars<ScenarioScheduleOccurrence, GeneratedScheduleOccurrence>
    >().toMatchTypeOf<
      GeneratedScalars<ScenarioScheduleOccurrence, GeneratedScheduleOccurrence>
    >()
    expectTypeOf<
      HandwrittenScalars<ScenarioScheduleWindow, GeneratedScheduleWindow>
    >().toMatchTypeOf<
      GeneratedScalars<ScenarioScheduleWindow, GeneratedScheduleWindow>
    >()
    expectTypeOf<
      HandwrittenScalars<ScenarioScheduleGovernance, GeneratedScheduleGovernance>
    >().toMatchTypeOf<
      GeneratedScalars<ScenarioScheduleGovernance, GeneratedScheduleGovernance>
    >()

    expect(true).toBe(true)
  })

  // The list endpoints for this domain (GET /fleet/pools, GET
  // /fleet/generators) declare no schema, so their envelope types
  // (`FleetPoolListResponse`, `FleetGeneratorMetadataListResponse`) cannot be
  // guarded until the Go annotations grow one. Not because such a guard would
  // pass silently — it cannot be written at all. Measured against
  // `handleListFleetPools`, whose 200 body is `Record<string, unknown>`:
  // steps 1 and 3 fail permanently and steps 2 and 4 say nothing. Step 1 is the
  // designed tripwire (`IsUntypedJsonObject` is `true`). Step 3 is the
  // surprise: `keyof Record<string, unknown>` is `string`, so
  // `Unmodelled<FleetPoolListResponse, ListBody>` is `Exclude<string,
  // 'fleet_pools' | 'count' | 'limit' | 'offset'>` = `string`, and the
  // `toEqualTypeOf<never>()` assertion stops compiling ("Expected 1 arguments,
  // but got 0"). Meanwhile `Invented` is `never` and `GeneratedScalars` has no
  // keys at all — `Extract<string, 'count' | 'limit' | 'offset'>` is `never` —
  // so the dashboard side is compared against `{}`. Adding such a guard buys a
  // red build, not coverage.
  //
  // The two element types are in different states, so do not read them as one:
  //   * `FleetPool` is reachable through `handleGetFleetPool`, whose 200 body
  //     carries a real schema, and is fully guarded in this block.
  //   * `FleetGeneratorMetadata` has no schema'd response anywhere. It is
  //     guarded against the PATCH *request* body instead (see below), which
  //     reaches all seven mutable fields by name and six of them by scalar type
  //     — `tags` is a map on both sides, so only the key-set steps cover it —
  //     and leaves `generator_id`, `created_at` and `updated_at` uncompared.
  //     Read that as narrowly as it is written. The Go declaration behind that
  //     request body is the anonymous patch struct in
  //     `handlePatchGeneratorMetadata` (pkg/api/handlers_fleet_management.go),
  //     *not* `storage.GeneratorMetadata` (pkg/storage/fleet.go), which is what
  //     every read of this type actually decodes. None of its ten fields is
  //     reconciled to that response DTO, so renaming a json tag there alone —
  //     `provider_status` to `status`, say — still passes this suite.
  it('keeps fleet-pool and host-allocation types aligned with the fleet API', () => {
    expectTypeOf<Invented<FleetPool, GetFleetPoolBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<FleetPool, GetFleetPoolBody>>().toEqualTypeOf<never>()
    expectTypeOf<Invented<FleetPoolMember, GeneratedFleetPoolMember>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<FleetPoolMember, GeneratedFleetPoolMember>>().toEqualTypeOf<never>()
    expectTypeOf<Invented<PoolAllocation, GeneratedPoolAllocation>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<PoolAllocation, GeneratedPoolAllocation>>().toEqualTypeOf<never>()
    expectTypeOf<Invented<Toleration, GeneratedToleration>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<Toleration, GeneratedToleration>>().toEqualTypeOf<never>()

    // The write side: the PUT body the allocation editor sends, and the
    // envelope it gets back.
    expectTypeOf<Invented<PoolAllocationInput, UpdateAllocationBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<PoolAllocationInput, UpdateAllocationBody>>().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<PoolAllocationResponse, UpdateAllocationResult>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<PoolAllocationResponse, UpdateAllocationResult>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<AllocationPreflight, GeneratedAllocationPreflight>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<AllocationPreflight, GeneratedAllocationPreflight>
    >().toEqualTypeOf<never>()

    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<FleetPool, GetFleetPoolBody>>().toMatchTypeOf<
      GeneratedScalars<FleetPool, GetFleetPoolBody>
    >()
    expectTypeOf<
      HandwrittenScalars<FleetPoolMember, GeneratedFleetPoolMember>
    >().toMatchTypeOf<
      GeneratedScalars<FleetPoolMember, GeneratedFleetPoolMember>
    >()
    expectTypeOf<HandwrittenScalars<PoolAllocation, GeneratedPoolAllocation>>().toMatchTypeOf<
      GeneratedScalars<PoolAllocation, GeneratedPoolAllocation>
    >()
    expectTypeOf<HandwrittenScalars<Toleration, GeneratedToleration>>().toMatchTypeOf<
      GeneratedScalars<Toleration, GeneratedToleration>
    >()
    expectTypeOf<
      HandwrittenScalars<PoolAllocationInput, UpdateAllocationBody>
    >().toMatchTypeOf<
      GeneratedScalars<PoolAllocationInput, UpdateAllocationBody>
    >()
    expectTypeOf<
      HandwrittenScalars<PoolAllocationResponse, UpdateAllocationResult>
    >().toMatchTypeOf<
      GeneratedScalars<PoolAllocationResponse, UpdateAllocationResult>
    >()
    expectTypeOf<
      HandwrittenScalars<AllocationPreflight, GeneratedAllocationPreflight>
    >().toMatchTypeOf<
      GeneratedScalars<AllocationPreflight, GeneratedAllocationPreflight>
    >()

    // `node_pool_label` decides which node label `node_pool` is matched
    // against; dropping it from either side silently repins every EKS pool to
    // the default "agentpool" key, which matches no EKS node. Pin it by name so
    // a rename cannot pass the key-set assertions above by symmetry.
    expectTypeOf<PoolAllocation['node_pool_label']>().toEqualTypeOf<string | undefined>()
    expectTypeOf<GeneratedPoolAllocation['node_pool_label']>().toEqualTypeOf<string | undefined>()
    expect(true).toBe(true)
  })

  // Generator metadata is guarded against a *request* body, so the two key-set
  // steps swap roles relative to every other block here. `Unmodelled` still has
  // to be `never` — a field the PATCH handler accepts that the dashboard does
  // not model is a generator attribute the fleet page can neither show nor
  // edit. `Invented` cannot be `never`, because a partial-update body has no
  // reason to carry the server-assigned identity and timestamps; it is pinned to
  // exactly those three so a *fourth* field falling out of the contract still
  // fails. Six of the seven fields the PATCH body declares are then compared by
  // scalar type — `tags` is a map on both sides, so `HandwrittenScalars` skips
  // it and only the two key-set steps above cover it — which is what puts
  // `fleet_pool_id` and `cordoned`, the two fields pool membership and drain
  // state are read off, under the type guard.
  //
  // `GeneratorMetadataPatch` is guarded in the same block because it, not
  // `FleetGeneratorMetadata`, is the type `patchGeneratorMetadata` actually
  // serializes into this request body. Both directions have to be `never` for
  // it: a key it invents is a field the handler silently drops, and a key it
  // omits is an edit the fleet page cannot make. Neither guard says anything
  // about the *response* to this PATCH, or to GET /fleet/generators — both are
  // schema-less, so `storage.GeneratorMetadata` is unreconciled here.
  it('keeps generator metadata aligned with the fleet generator PATCH contract', () => {
    expectTypeOf<
      Unmodelled<FleetGeneratorMetadata, PatchGeneratorMetadataBody>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<FleetGeneratorMetadata, PatchGeneratorMetadataBody>
    >().toEqualTypeOf<'generator_id' | 'created_at' | 'updated_at'>()

    // The request type the PATCH call actually sends. No allowance list: it
    // exists only to serialize this body, so any asymmetry is drift.
    expectTypeOf<
      Invented<GeneratorMetadataPatch, PatchGeneratorMetadataBody>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<GeneratorMetadataPatch, PatchGeneratorMetadataBody>
    >().toEqualTypeOf<never>()

    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<
      HandwrittenScalars<FleetGeneratorMetadata, PatchGeneratorMetadataBody>
    >().toMatchTypeOf<
      GeneratedScalars<FleetGeneratorMetadata, PatchGeneratorMetadataBody>
    >()
    expectTypeOf<
      HandwrittenScalars<GeneratorMetadataPatch, PatchGeneratorMetadataBody>
    >().toMatchTypeOf<
      GeneratedScalars<GeneratorMetadataPatch, PatchGeneratorMetadataBody>
    >()
    // The comparison is only worth anything if it actually reaches those two
    // fields, so pin the compared key set rather than trusting it.
    expectTypeOf<
      keyof HandwrittenScalars<FleetGeneratorMetadata, PatchGeneratorMetadataBody>
    >().toEqualTypeOf<
      'target' | 'fleet_pool_id' | 'region' | 'provider_id' | 'provider_status' | 'cordoned'
    >()
    expectTypeOf<
      keyof HandwrittenScalars<GeneratorMetadataPatch, PatchGeneratorMetadataBody>
    >().toEqualTypeOf<
      'target' | 'fleet_pool_id' | 'region' | 'provider_id' | 'provider_status' | 'cordoned'
    >()

    expect(true).toBe(true)
  })

  it('keeps fleet capacity and alert types aligned with the fleet API', () => {
    expectTypeOf<Invented<FleetCapacityResponse, FleetCapacityBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<FleetCapacityResponse, FleetCapacityBody>>().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<FleetProjectSlotAccounting, GeneratedProjectSlotAccounting>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<FleetProjectSlotAccounting, GeneratedProjectSlotAccounting>
    >().toEqualTypeOf<never>()
    expectTypeOf<Invented<FleetAlertsResponse, FleetAlertsBody>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<FleetAlertsResponse, FleetAlertsBody>>().toEqualTypeOf<never>()
    expectTypeOf<Invented<FleetAlert, GeneratedFleetAlert>>().toEqualTypeOf<never>()
    expectTypeOf<Unmodelled<FleetAlert, GeneratedFleetAlert>>().toEqualTypeOf<never>()

    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<
      HandwrittenScalars<FleetCapacityResponse, FleetCapacityBody>
    >().toMatchTypeOf<
      GeneratedScalars<FleetCapacityResponse, FleetCapacityBody>
    >()
    expectTypeOf<
      HandwrittenScalars<FleetProjectSlotAccounting, GeneratedProjectSlotAccounting>
    >().toMatchTypeOf<
      GeneratedScalars<FleetProjectSlotAccounting, GeneratedProjectSlotAccounting>
    >()
    expectTypeOf<HandwrittenScalars<FleetAlertsResponse, FleetAlertsBody>>().toMatchTypeOf<
      GeneratedScalars<FleetAlertsResponse, FleetAlertsBody>
    >()
    expectTypeOf<HandwrittenScalars<FleetAlert, GeneratedFleetAlert>>().toMatchTypeOf<
      GeneratedScalars<FleetAlert, GeneratedFleetAlert>
    >()

    expect(true).toBe(true)
  })

  it('keeps domain deployment-target types aligned with the deployment-target API', () => {
    expectTypeOf<
      Invented<DomainDeploymentTargetListResponse, DeploymentTargetListBody>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<DomainDeploymentTargetListResponse, DeploymentTargetListBody>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<DomainDeploymentTarget, GeneratedDeploymentTarget>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<DomainDeploymentTarget, GeneratedDeploymentTarget>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Invented<DomainDeploymentTargetInput, UpdateDeploymentTargetBody>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<DomainDeploymentTargetInput, UpdateDeploymentTargetBody>
    >().toEqualTypeOf<never>()

    // Names line up; now the scalar field *types* have to line up too. The
    // dashboard narrows `provider` to a three-member union where Go serves a
    // plain string, which is a legal narrowing in this direction — the guard
    // fails only if the narrowing stops being a subset of what Go sends.
    expectTypeOf<
      HandwrittenScalars<DomainDeploymentTargetListResponse, DeploymentTargetListBody>
    >().toMatchTypeOf<
      GeneratedScalars<DomainDeploymentTargetListResponse, DeploymentTargetListBody>
    >()
    expectTypeOf<
      HandwrittenScalars<DomainDeploymentTarget, GeneratedDeploymentTarget>
    >().toMatchTypeOf<
      GeneratedScalars<DomainDeploymentTarget, GeneratedDeploymentTarget>
    >()
    expectTypeOf<
      HandwrittenScalars<DomainDeploymentTargetInput, UpdateDeploymentTargetBody>
    >().toMatchTypeOf<
      GeneratedScalars<DomainDeploymentTargetInput, UpdateDeploymentTargetBody>
    >()

    // `updated_at` is the optimistic-concurrency token the retarget PUT echoes
    // back. It has to survive on the write type, not just the read type.
    expectTypeOf<DomainDeploymentTargetInput['updated_at']>().toEqualTypeOf<string | undefined>()
    expectTypeOf<UpdateDeploymentTargetBody['updated_at']>().toEqualTypeOf<string | undefined>()
    expect(true).toBe(true)
  })

  it('keeps test-group types aligned with GET /tests/{id}', () => {
    expectTypeOf<Invented<TestGroup, GeneratedTestGroup>>().toEqualTypeOf<never>()
    expectTypeOf<TestGroup['browser_document_capture_requested']>().toEqualTypeOf<GeneratedTestGroup['browser_document_capture_requested']>()
    // Known, deliberate gap: the controller serves per-group run-logic goals
    // that no dashboard surface reads yet. Naming it here means the *next*
    // field the Go side adds fails this assertion instead of disappearing.
    expectTypeOf<Unmodelled<TestGroup, GeneratedTestGroup>>().toEqualTypeOf<'run_logic_goals'>()

    expectTypeOf<
      Invented<GeneratorAssignmentSettings, GeneratedAssignment>
    >().toEqualTypeOf<never>()
    expectTypeOf<
      Unmodelled<GeneratorAssignmentSettings, GeneratedAssignment>
    >().toEqualTypeOf<never>()
    // Names line up; now the scalar field *types* have to line up too.
    expectTypeOf<HandwrittenScalars<TestGroup, GeneratedTestGroup>>().toMatchTypeOf<
      GeneratedScalars<TestGroup, GeneratedTestGroup>
    >()
    expectTypeOf<
      HandwrittenScalars<GeneratorAssignmentSettings, GeneratedAssignment>
    >().toMatchTypeOf<
      GeneratedScalars<GeneratorAssignmentSettings, GeneratedAssignment>
    >()

    expect(true).toBe(true)
  })
})
