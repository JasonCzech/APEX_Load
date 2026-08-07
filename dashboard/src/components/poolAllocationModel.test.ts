import { describe, expect, it } from 'vitest'

import {
  AllocationDraft,
  NODE_SELECTOR_AGENT_POOL,
  NODE_SELECTOR_ZONE,
  allocationBaseline,
  allocationError,
  allocationToTransport,
  emptyAllocationDraft,
  formatCpuMillis,
  formatMemoryBytes,
  normalizeMemoryQuantity,
  parseCpuQuantity,
  parseMemoryQuantity,
  quotaHeadroom,
  tolerationError,
  tolerationsFromTransport,
  tolerationsToTransport,
} from './poolAllocationModel'

function draft(overrides: Partial<AllocationDraft> = {}): AllocationDraft {
  return { ...emptyAllocationDraft('cluster-1'), ...overrides }
}

let idCounter = 0
const nextId = () => `row-${(idCounter += 1)}`

describe('parseCpuQuantity', () => {
  it('parses the forms an operator types', () => {
    expect(parseCpuQuantity('500m')).toBe(500)
    expect(parseCpuQuantity('2')).toBe(2000)
    expect(parseCpuQuantity('0.5')).toBe(500)
    expect(parseCpuQuantity('1.5')).toBe(1500)
    expect(parseCpuQuantity('0')).toBe(0)
  })

  it('treats blank as unset rather than invalid', () => {
    expect(parseCpuQuantity('')).toBeUndefined()
    expect(parseCpuQuantity('   ')).toBeUndefined()
  })

  it('rejects values that would be silently mis-parsed', () => {
    // Fractional millicores are rejected rather than rounded away.
    expect(parseCpuQuantity('1.5m')).toBeNull()
    expect(parseCpuQuantity('0.0005')).toBeNull()
    expect(parseCpuQuantity('2c')).toBeNull()
    expect(parseCpuQuantity('-1')).toBeNull()
    expect(parseCpuQuantity('1e3')).toBeNull()
    expect(parseCpuQuantity('abc')).toBeNull()
  })
})

describe('parseMemoryQuantity', () => {
  it('parses binary and decimal suffixes distinctly', () => {
    expect(parseMemoryQuantity('512Mi')).toBe(536870912)
    expect(parseMemoryQuantity('1Gi')).toBe(1073741824)
    expect(parseMemoryQuantity('4Gi')).toBe(4294967296)
    expect(parseMemoryQuantity('1M')).toBe(1000000)
    // 1M and 1Mi must never be conflated.
    expect(parseMemoryQuantity('1M')).not.toBe(parseMemoryQuantity('1Mi'))
    expect(parseMemoryQuantity('1024')).toBe(1024)
  })

  it('rejects lowercase binary suffixes', () => {
    // Accepting "512mi" invites typing "500m" for memory, which Kubernetes
    // reads as half a byte.
    expect(parseMemoryQuantity('512mi')).toBeNull()
    expect(parseMemoryQuantity('4gi')).toBeNull()
    expect(parseMemoryQuantity('-1Gi')).toBeNull()
  })
})

describe('normalizeMemoryQuantity', () => {
  it('canonicalizes casing without converting units', () => {
    expect(normalizeMemoryQuantity('4gi')).toBe('4Gi')
    expect(normalizeMemoryQuantity(' 512mi ')).toBe('512Mi')
    // Normalization must never turn "4Gi" into a byte count — operators expect
    // to read back what they typed.
    expect(normalizeMemoryQuantity('4Gi')).toBe('4Gi')
    expect(normalizeMemoryQuantity('1024')).toBe('1024')
  })
})

describe('allocationError', () => {
  it('accepts an empty draft', () => {
    expect(allocationError(draft())).toBeNull()
  })

  it('compares requests against limits numerically across units', () => {
    // "1" exceeds "500m" even though the strings sort the other way.
    expect(allocationError(draft({ cpuRequest: '1', cpuLimit: '500m' }))).toMatch(/cannot exceed/)
    expect(allocationError(draft({ cpuRequest: '1', cpuLimit: '1000m' }))).toBeNull()
    expect(allocationError(draft({ memoryRequest: '4Gi', memoryLimit: '1Gi' }))).toMatch(/cannot exceed/)
  })

  it('allows a limit with no request', () => {
    expect(allocationError(draft({ cpuLimit: '2' }))).toBeNull()
  })

  it('rejects malformed quantities with actionable copy', () => {
    expect(allocationError(draft({ cpuRequest: '2c' }))).toMatch(/500m/)
    expect(allocationError(draft({ memoryRequest: '512mi' }))).toMatch(/512Mi/)
  })

  it('reserves the node pool and zone selector keys', () => {
    const withAgentPool = draft({ selectors: [{ id: 'a', key: NODE_SELECTOR_AGENT_POOL, value: 'loadgen' }] })
    expect(allocationError(withAgentPool)).toMatch(/Node pool field/)
    const withZone = draft({ selectors: [{ id: 'b', key: NODE_SELECTOR_ZONE, value: 'eastus-2' }] })
    expect(allocationError(withZone)).toMatch(/Zone field/)
  })

  it('ignores fully empty selector rows', () => {
    expect(allocationError(draft({ selectors: [{ id: 'a', key: '', value: '' }] }))).toBeNull()
  })
})

describe('tolerations', () => {
  it('rejects a value alongside the Exists operator', () => {
    expect(tolerationError({ id: 'a', key: 'spot', operator: 'Exists', value: 'yes', effect: '' })).toMatch(/must not set a value/)
    expect(tolerationError({ id: 'a', key: 'spot', operator: 'Exists', value: '', effect: '' })).toBeNull()
  })

  it('requires a key for the Equal operator', () => {
    expect(tolerationError({ id: 'a', key: '', operator: 'Equal', value: 'yes', effect: '' })).toMatch(/needs a key/)
  })

  it('drops empty rows and omits the value under Exists', () => {
    const transport = tolerationsToTransport([
      { id: 'a', key: '', operator: 'Equal', value: '', effect: '' },
      { id: 'b', key: 'spot', operator: 'Exists', value: '', effect: 'NoSchedule' },
      { id: 'c', key: 'apexload/loadgen', operator: 'Equal', value: 'true', effect: '' },
    ])
    expect(transport).toHaveLength(2)
    expect('value' in transport[0]).toBe(false)
    expect(transport[0].effect).toBe('NoSchedule')
    expect(transport[1]).toEqual({ key: 'apexload/loadgen', operator: 'Equal', value: 'true' })
  })

  it('round-trips independently of row ids', () => {
    const original = tolerationsToTransport([
      { id: 'a', key: 'apexload/loadgen', operator: 'Equal', value: 'true', effect: 'NoSchedule' },
    ])
    const rows = tolerationsFromTransport(original, nextId)
    expect(tolerationsToTransport(rows)).toEqual(original)
  })
})

describe('allocationBaseline', () => {
  it('is computed from the transport shape so re-assigned row ids do not look dirty', () => {
    const first = draft({ tolerations: tolerationsFromTransport([{ key: 'k', operator: 'Exists' }], nextId) })
    const second = draft({ tolerations: tolerationsFromTransport([{ key: 'k', operator: 'Exists' }], nextId) })
    expect(first.tolerations[0].id).not.toBe(second.tolerations[0].id)
    expect(allocationBaseline(first)).toBe(allocationBaseline(second))
  })
})

describe('allocationToTransport', () => {
  it('omits blank fields and keeps the dedicated placement fields separate', () => {
    const transport = allocationToTransport(
      draft({ cpuRequest: '1', nodePool: 'loadgen', zone: 'eastus-2', selectors: [{ id: 'a', key: 'disktype', value: 'ssd' }] }),
    )
    expect(transport.cpu_request).toBe('1')
    expect(transport.cpu_limit).toBeUndefined()
    expect(transport.node_pool).toBe('loadgen')
    expect(transport.zone).toBe('eastus-2')
    expect(transport.node_selector).toEqual({ disktype: 'ssd' })
  })
})

describe('quotaHeadroom', () => {
  it('reports not enforced when the namespace has no quota', () => {
    expect(quotaHeadroom([], draft(), 0, 3, undefined).enforced).toBe(false)
  })

  it('projects the change relative to what the workload already consumes', () => {
    const headroom = quotaHeadroom(
      [{ name: 'apex', hard: { 'requests.cpu': '8', pods: '20' }, used: { 'requests.cpu': '2', pods: '2' } }],
      draft({ cpuRequest: '1' }),
      2,
      6,
      { cpu: '1' },
    )
    expect(headroom.enforced).toBe(true)
    // used 2000m, adding 6 pods x 1000m and removing the 2 x 1000m already
    // counted leaves 6000m.
    expect(headroom.cpu).toEqual({ used: 2000, limit: 8000, projected: 6000 })
    expect(headroom.pods).toEqual({ used: 2, limit: 20, projected: 6 })
  })

  it('falls back to the limit when no request is set', () => {
    const headroom = quotaHeadroom(
      [{ name: 'apex', hard: { 'requests.cpu': '8' }, used: {} }],
      draft({ cpuLimit: '2' }),
      0,
      2,
      undefined,
    )
    expect(headroom.cpu?.projected).toBe(4000)
  })
})

describe('formatters', () => {
  it('renders quantities back in operator-readable form', () => {
    expect(formatCpuMillis(500)).toBe('500m')
    expect(formatCpuMillis(2000)).toBe('2')
    expect(formatMemoryBytes(0)).toBe('0')
    expect(formatMemoryBytes(536870912)).toBe('512Mi')
    expect(formatMemoryBytes(4294967296)).toBe('4Gi')
  })
})
