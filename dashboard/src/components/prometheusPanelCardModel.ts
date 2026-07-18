import type { ImportedDashboardPanelThreshold } from '../types'

export function thresholdAwareYAxisMax(dataMax: number, thresholds: ImportedDashboardPanelThreshold[]): number {
  return thresholds.reduce((maximum, step) => (
    typeof step.value === 'number' && Number.isFinite(step.value)
      ? Math.max(maximum, step.value)
      : maximum
  ), dataMax)
}
