import type { SystemInventoryGenerator } from '../types'

/**
 * A generator as people know it: its ID and region ("gen-a · us-east"), with
 * the address only when the inventory does not know the target.
 */
export function generatorDisplayName(target: string, inventory: SystemInventoryGenerator[] | undefined): string {
  const generator = inventory?.find(item => item.target === target)
  if (!generator) return target
  const name = generator.generator_id || target
  return generator.region ? `${name} · ${generator.region}` : name
}

/** A load estimate to two significant figures, so a small real value never reads as 0.00. */
export function formatEstimate(value: number): string {
  return Number.isFinite(value) ? value.toLocaleString(undefined, { maximumSignificantDigits: 2 }) : '—'
}
