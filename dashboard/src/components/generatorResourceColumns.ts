import type { SystemResources } from '../types'

/** The resource columns a Command Center infrastructure row shows for one generator. */
export interface GeneratorResourceColumns {
  cpu?: number
  /** Container memory share, in percent. */
  memory?: number
  /** Process memory in MB, for a generator that reports no container memory share. */
  memoryMb?: number
  network?: number
  disk?: number
  gc?: number
}

export function generatorResourceColumns(resources?: SystemResources): GeneratorResourceColumns {
  if (!resources) return {}
  const memory = resources.container_memory_usage_pct
  const reportsThroughput = resources.net_sent_kbps !== undefined || resources.net_recv_kbps !== undefined
  return {
    cpu: resources.container_cpu_usage_pct ?? resources.cpu_process_pct,
    memory,
    memoryMb: memory === undefined ? resources.mem_process_mb : undefined,
    // A reported zero is a value; only a generator reporting no throughput falls back to retransmits.
    network: reportsThroughput ? (resources.net_sent_kbps || 0) + (resources.net_recv_kbps || 0) : resources.tcp_retransmits_per_sec,
    disk: resources.disk_io_time_pct,
    gc: resources.gc_pause_ms,
  }
}
