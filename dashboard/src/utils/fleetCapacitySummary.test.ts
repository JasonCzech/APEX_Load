import { describe, expect, it } from 'vitest'
import type { FleetCapacityResponse } from '../types/fleet'
import type { HealthResponse } from '../types/system'
import { fleetCapacitySummary } from './fleetCapacitySummary'

const health = { capacity_availability: { source: 'configured', configured_available_generators: 12, effective_available_generators: 12 } } as HealthResponse
const emptyFleet = { total_generators: 0, eligible_generators: 0, active_slots: 3, available_slots: 0, max_slots: 0 } as FleetCapacityResponse

describe('fleetCapacitySummary', () => {
  it('uses configured capacity when the placement API has no live inventory (local mode)', () => {
    const summary = fleetCapacitySummary(emptyFleet, health)
    expect(summary.effectiveAvailable).toBe(12)
    expect(summary.eligibleLabel).toBe('12 configured')
    expect(summary.sourceLabel).toBe('Configured inventory · no live generator inventory')
    expect(summary.hasLiveInventory).toBe(false)
  })

  it('uses the fleet placement numbers when live generators exist', () => {
    const summary = fleetCapacitySummary({ ...emptyFleet, total_generators: 4, eligible_generators: 3, available_slots: 30 }, health)
    expect(summary).toMatchObject({ effectiveAvailable: 30, eligibleLabel: '3/4', hasLiveInventory: true, configuredGenerators: 12 })
  })

  it('falls back to configured capacity when the placement API failed', () => {
    expect(fleetCapacitySummary(null, health)).toMatchObject({ effectiveAvailable: 12, sourceLabel: 'Configured inventory' })
  })

  it('reports nothing it does not know', () => {
    expect(fleetCapacitySummary(null, null)).toMatchObject({ effectiveAvailable: undefined, eligibleLabel: '-', sourceLabel: '-' })
  })
})
