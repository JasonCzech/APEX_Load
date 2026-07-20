import { describe, expectTypeOf, it } from 'vitest'

import type {
  CapacityTimeslot as CompatibilityCapacityTimeslot,
  DashboardAuthStatus as CompatibilityDashboardAuthStatus,
  DashboardSystemSettings as CompatibilityDashboardSystemSettings,
  ClientMetrics as CompatibilityClientMetrics,
  FleetPool as CompatibilityFleetPool,
  ExecutionPlan as CompatibilityExecutionPlan,
  EntitlementsResponse as CompatibilityEntitlementsResponse,
  ScriptDebugSession as CompatibilityScriptDebugSession,
  MonitoringCollector as CompatibilityMonitoringCollector,
  ObjectRepository as CompatibilityObjectRepository,
  RunCalendarResponse as CompatibilityRunCalendarResponse,
  ScenarioSchedule as CompatibilityScenarioSchedule,
  ScenarioDataPoolBinding as CompatibilityScenarioDataPoolBinding,
  RuntimeSettingsOverride as CompatibilityRuntimeSettingsOverride,
  StoredScript as CompatibilityStoredScript,
  TestGroup as CompatibilityTestGroup,
  SystemInventoryResponse as CompatibilitySystemInventoryResponse,
  VersionResponse as CompatibilityVersionResponse,
} from '../types'
import type { DashboardAuthStatus } from './auth'
import type { CapacityTimeslot } from './capacity'
import type { FleetPool } from './fleet'
import type { SystemInventoryResponse, VersionResponse } from './system'
import type { DashboardSystemSettings } from './settings'
import type { RunCalendarResponse, ScenarioSchedule } from './scheduling'
import type { ScriptDebugSession } from './replay'
import type { ExecutionPlan } from './executionPlans'
import type { EntitlementsResponse } from './entitlements'
import type { MonitoringCollector } from './monitoring'
import type { ObjectRepository } from './objectRepository'
import type { ClientMetrics } from './metrics'
import type { ScenarioDataPoolBinding } from './dataPools'
import type { RuntimeSettingsOverride } from './runtimeSettings'
import type { TestGroup } from './testConfiguration'
import type { StoredScript } from './storedContent'

describe('domain transport contract compatibility exports', () => {
  it('keeps the legacy types barrel source-compatible during migration', () => {
    expectTypeOf<CompatibilityCapacityTimeslot>().toEqualTypeOf<CapacityTimeslot>()
    expectTypeOf<CompatibilityClientMetrics>().toEqualTypeOf<ClientMetrics>()
    expectTypeOf<CompatibilityDashboardAuthStatus>().toEqualTypeOf<DashboardAuthStatus>()
    expectTypeOf<CompatibilityDashboardSystemSettings>().toEqualTypeOf<DashboardSystemSettings>()
    expectTypeOf<CompatibilityFleetPool>().toEqualTypeOf<FleetPool>()
    expectTypeOf<CompatibilityExecutionPlan>().toEqualTypeOf<ExecutionPlan>()
    expectTypeOf<CompatibilityEntitlementsResponse>().toEqualTypeOf<EntitlementsResponse>()
    expectTypeOf<CompatibilityScriptDebugSession>().toEqualTypeOf<ScriptDebugSession>()
    expectTypeOf<CompatibilityMonitoringCollector>().toEqualTypeOf<MonitoringCollector>()
    expectTypeOf<CompatibilityObjectRepository>().toEqualTypeOf<ObjectRepository>()
    expectTypeOf<CompatibilityRunCalendarResponse>().toEqualTypeOf<RunCalendarResponse>()
    expectTypeOf<CompatibilityScenarioSchedule>().toEqualTypeOf<ScenarioSchedule>()
    expectTypeOf<CompatibilityScenarioDataPoolBinding>().toEqualTypeOf<ScenarioDataPoolBinding>()
    expectTypeOf<CompatibilityRuntimeSettingsOverride>().toEqualTypeOf<RuntimeSettingsOverride>()
    expectTypeOf<CompatibilityStoredScript>().toEqualTypeOf<StoredScript>()
    expectTypeOf<CompatibilityTestGroup>().toEqualTypeOf<TestGroup>()
    expectTypeOf<CompatibilitySystemInventoryResponse>().toEqualTypeOf<SystemInventoryResponse>()
    expectTypeOf<CompatibilityVersionResponse>().toEqualTypeOf<VersionResponse>()
  })
})
