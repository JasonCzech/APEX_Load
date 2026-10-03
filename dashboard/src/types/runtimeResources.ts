export interface RuntimeResourceDemand {
  processors: number
  workers: number
  max_partitions_per_processor: number
}

export interface RuntimeResourceClaim {
  contract: string
  protocol: string
  peak_vusers?: number
  requested?: RuntimeResourceDemand
}

export interface RuntimeResourceAdmission {
  claim?: RuntimeResourceClaim
  admitted?: RuntimeResourceDemand
  current?: boolean
}

export interface RuntimeResourceCapacity {
  available: boolean
  max_processors: number
  available_processors: number
  reserved_processors: number
  active_processors: number
  draining_processors: number
  max_workers: number
  available_workers: number
  reserved_workers: number
  active_workers: number
  draining_workers: number
  max_partitions_per_processor: number
}
