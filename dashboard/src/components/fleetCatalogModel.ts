import type {
  FleetGeneratorMetadata,
  FleetGeneratorMetadataListResponse,
  FleetPool,
  FleetPoolListResponse,
} from '../types'
import { drainBoundedOffsetCatalog } from './catalogPagination'

export function listAllFleetPools(
  listPage: (params: { limit: number; offset: number }) => Promise<FleetPoolListResponse>,
): Promise<FleetPool[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => ({ items: (await listPage({ limit, offset })).fleet_pools || [] }),
    pool => pool.id,
    'Fleet pool catalog',
  )
}

export function listAllGeneratorMetadata(
  listPage: (params: { limit: number; offset: number }) => Promise<FleetGeneratorMetadataListResponse>,
): Promise<FleetGeneratorMetadata[]> {
  return drainBoundedOffsetCatalog(
    async (limit, offset) => ({ items: (await listPage({ limit, offset })).generators || [] }),
    generator => generator.generator_id,
    'Generator metadata catalog',
  )
}

export function runtimePoolMemberCount(
  pool: FleetPool,
  metadata: FleetGeneratorMetadata[],
): number {
  const targets = new Set<string>()
  const add = (target?: string | null) => {
    const normalized = (target || '').trim()
    if (normalized) targets.add(normalized)
  }

  ;(pool.members || []).forEach(member => add(member.target))
  metadata.forEach(generator => {
    if ((generator.fleet_pool_id || '').trim() === pool.id) add(generator.target)
  })
  return targets.size
}
