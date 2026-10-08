import type { FleetCapacityResponse } from '../types/fleet'
import type { HealthResponse } from '../types/system'

// The fleet placement API counts slots on live generator inventory. A local or
// statically configured controller has none, so that API reports 0/0 while the
// controller's configured capacity (what New Test and Settings › Capacity use)
// is, say, 12. The Fleet card must show the number launches are admitted
// against and name its source, instead of letting an empty inventory read as
// "no capacity".

export interface FleetCapacitySummary {
  effectiveAvailable: number | undefined
  eligibleLabel: string
  sourceLabel: string
  hasLiveInventory: boolean
  configuredGenerators: number | undefined
}

export function fleetCapacitySummary(fleetCapacity: FleetCapacityResponse | null | undefined, health: HealthResponse | null | undefined): FleetCapacitySummary {
  const configured = health?.capacity_availability
  const hasLiveInventory = Boolean(fleetCapacity && fleetCapacity.total_generators > 0)
  if (fleetCapacity && hasLiveInventory) {
    return {
      effectiveAvailable: fleetCapacity.available_slots,
      eligibleLabel: `${fleetCapacity.eligible_generators}/${fleetCapacity.total_generators}`,
      sourceLabel: 'Live generator inventory (fleet placement)',
      hasLiveInventory,
      configuredGenerators: configured?.configured_available_generators,
    }
  }
  if (configured) {
    const liveNote = fleetCapacity ? ' · no live generator inventory' : ''
    return {
      effectiveAvailable: configured.effective_available_generators,
      eligibleLabel: `${configured.configured_available_generators.toLocaleString()} configured`,
      sourceLabel: `${configured.source === 'configured_plus_live' ? 'Configured inventory capped by live availability' : 'Configured inventory'}${liveNote}`,
      hasLiveInventory,
      configuredGenerators: configured.configured_available_generators,
    }
  }
  return {
    effectiveAvailable: fleetCapacity?.available_slots,
    eligibleLabel: fleetCapacity ? `${fleetCapacity.eligible_generators}/${fleetCapacity.total_generators}` : '-',
    sourceLabel: fleetCapacity ? 'Fleet placement (no live generator inventory)' : '-',
    hasLiveInventory,
    configuredGenerators: undefined,
  }
}
