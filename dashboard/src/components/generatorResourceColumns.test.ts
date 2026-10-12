import { describe, expect, it } from 'vitest'

import type { SystemResources } from '../types'
import { generatorResourceColumns } from './generatorResourceColumns'

describe('generatorResourceColumns', () => {
  it('shows process memory when a generator reports no container share, and a reported zero network', () => {
    const columns = generatorResourceColumns({ cpu_process_pct: 0.4, mem_process_mb: 60, net_sent_kbps: 0, net_recv_kbps: 0, tcp_retransmits_per_sec: 3 } as SystemResources)
    expect(columns).toMatchObject({ cpu: 0.4, memory: undefined, memoryMb: 60, network: 0 })
  })

  it('prefers the container share and falls back to retransmits without throughput', () => {
    const columns = generatorResourceColumns({ container_cpu_usage_pct: 40, cpu_process_pct: 1, container_memory_usage_pct: 55, mem_process_mb: 900, tcp_retransmits_per_sec: 2 } as SystemResources)
    expect(columns).toMatchObject({ cpu: 40, memory: 55, memoryMb: undefined, network: 2 })
    expect(generatorResourceColumns(undefined)).toEqual({})
  })
})
