import { describe, expect, it, vi } from 'vitest'

import type { FleetGeneratorMetadata, FleetPool } from '../types'
import { listAllFleetPools, listAllGeneratorMetadata, runtimePoolMemberCount } from './fleetCatalogModel'

function pool(id: string): FleetPool {
  return {
    id,
    name: id,
    cloud_provider: 'kubernetes',
    status: 'active',
    created_at: '',
    updated_at: '',
  }
}

function generator(id: string): FleetGeneratorMetadata {
  return { generator_id: id, created_at: '', updated_at: '' }
}

describe('fleet catalog pagination', () => {
  it('drains pool and generator catalogs beyond the API default page size', async () => {
    const pools = Array.from({ length: 205 }, (_, index) => pool(`pool-${String(index).padStart(3, '0')}`))
    const generators = Array.from({ length: 201 }, (_, index) => generator(`generator-${String(index).padStart(3, '0')}`))
    const listPools = vi.fn(async ({ limit, offset }: { limit: number; offset: number }) => ({
      fleet_pools: pools.slice(offset, offset + limit), count: Math.min(limit, pools.length - offset), limit, offset,
    }))
    const listGenerators = vi.fn(async ({ limit, offset }: { limit: number; offset: number }) => ({
      generators: generators.slice(offset, offset + limit), count: Math.min(limit, generators.length - offset), limit, offset,
    }))

    await expect(listAllFleetPools(listPools)).resolves.toHaveLength(205)
    await expect(listAllGeneratorMetadata(listGenerators)).resolves.toHaveLength(201)
    expect(listPools).toHaveBeenNthCalledWith(3, { limit: 100, offset: 200 })
    expect(listGenerators).toHaveBeenNthCalledWith(3, { limit: 100, offset: 200 })
  })

  it('fails closed when a full page repeats without advancing', async () => {
    const repeated = Array.from({ length: 100 }, (_, index) => pool(`pool-${index}`))
    const listPools = vi.fn(async ({ limit, offset }: { limit: number; offset: number }) => ({
      fleet_pools: repeated, count: repeated.length, limit, offset,
    }))

    await expect(listAllFleetPools(listPools)).rejects.toThrow('pagination did not advance')
    expect(listPools).toHaveBeenCalledTimes(2)
  })

  it('counts the deduplicated runtime union of explicit and metadata targets', () => {
    const item = pool('pool-1')
    item.members = [
      { id: 'one', pool_id: 'pool-1', generator_id: 'one', target: 'one:9090', status: 'ready', joined_at: '' },
      { id: 'duplicate', pool_id: 'pool-1', generator_id: 'duplicate', target: 'one:9090', status: 'ready', joined_at: '' },
    ]
    const metadata: FleetGeneratorMetadata[] = [
      { ...generator('meta-one'), target: 'one:9090', fleet_pool_id: 'pool-1' },
      { ...generator('meta-two'), target: 'two:9090', fleet_pool_id: 'pool-1' },
      { ...generator('other'), target: 'other:9090', fleet_pool_id: 'pool-2' },
      { ...generator('missing-target'), fleet_pool_id: 'pool-1' },
    ]

    expect(runtimePoolMemberCount(item, metadata)).toBe(2)
  })
})
