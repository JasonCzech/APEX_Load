import type { ClusterQuota, PoolAllocation, Toleration } from '../types/cluster'

/**
 * Pure validation and conversion for the pool host-allocation editor.
 *
 * Kubernetes quantities are strings with a suffix grammar, not numbers, so they
 * are parsed here rather than fed through numeric inputs. The rules are
 * deliberately stricter than Kubernetes itself: forms an operator would only
 * type by mistake are rejected instead of silently mis-parsed, because a wrong
 * value here changes how much hardware a test run consumes.
 */

/** Node-selector keys owned by the dedicated Node pool / Zone fields. */
export const NODE_SELECTOR_AGENT_POOL = 'agentpool'
export const NODE_SELECTOR_ZONE = 'topology.kubernetes.io/zone'

const CPU_PATTERN = /^(\d+(\.\d+)?)(m)?$/
// Binary suffixes are powers of 1024, decimal powers of 1000. Casing is
// significant: accepting lowercase "mi"/"gi" invites the classic mistake of
// typing "500m" for memory, which Kubernetes reads as half a byte.
const MEMORY_PATTERN = /^(\d+(\.\d+)?)(Ki|Mi|Gi|Ti|Pi|Ei|K|M|G|T|P|E)?$/

const BINARY_SCALE: Record<string, number> = { Ki: 1024, Mi: 1024 ** 2, Gi: 1024 ** 3, Ti: 1024 ** 4, Pi: 1024 ** 5, Ei: 1024 ** 6 }
const DECIMAL_SCALE: Record<string, number> = { K: 1000, M: 1000 ** 2, G: 1000 ** 3, T: 1000 ** 4, P: 1000 ** 5, E: 1000 ** 6 }

/** A toleration row carries a client-side id purely for React keys. */
export interface TolerationRow {
  id: string
  key: string
  operator: 'Equal' | 'Exists'
  value: string
  effect: string
}

export interface SelectorRow {
  id: string
  key: string
  value: string
}

export interface AllocationDraft {
  clusterId: string
  cpuRequest: string
  cpuLimit: string
  memoryRequest: string
  memoryLimit: string
  nodePool: string
  nodePoolLabel: string
  zone: string
  selectors: SelectorRow[]
  tolerations: TolerationRow[]
}

export function emptyAllocationDraft(clusterId = ''): AllocationDraft {
  return {
    clusterId,
    cpuRequest: '',
    cpuLimit: '',
    memoryRequest: '',
    memoryLimit: '',
    nodePool: '',
    nodePoolLabel: '',
    zone: '',
    selectors: [],
    tolerations: [],
  }
}

/**
 * Parses a CPU quantity to millicores. Returns null for an invalid value and
 * undefined for "unset", so callers can tell the two apart.
 */
export function parseCpuQuantity(raw: string): number | null | undefined {
  const value = (raw ?? '').trim()
  if (value === '') return undefined
  const match = CPU_PATTERN.exec(value)
  if (!match) return null
  const amount = Number(match[1])
  if (!Number.isFinite(amount)) return null
  if (match[3] === 'm') {
    // Fractional millicores are rejected rather than rounded.
    return Number.isInteger(amount) ? amount : null
  }
  const millis = amount * 1000
  return Number.isInteger(millis) ? millis : null
}

/** Parses a memory quantity to bytes. Same null/undefined contract as CPU. */
export function parseMemoryQuantity(raw: string): number | null | undefined {
  const value = (raw ?? '').trim()
  if (value === '') return undefined
  const match = MEMORY_PATTERN.exec(value)
  if (!match) return null
  const amount = Number(match[1])
  if (!Number.isFinite(amount)) return null
  const suffix = match[3]
  if (!suffix) return amount
  const scale = BINARY_SCALE[suffix] ?? DECIMAL_SCALE[suffix]
  return scale ? amount * scale : null
}

/**
 * Trims and canonicalizes casing. It deliberately does NOT convert units —
 * operators expect to read back what they typed — and should only be applied on
 * blur, never per keystroke, or a mid-typed "4G" becomes "4000000000" under the
 * caret.
 */
export function normalizeCpuQuantity(raw: string): string {
  return (raw ?? '').trim()
}

export function normalizeMemoryQuantity(raw: string): string {
  const value = (raw ?? '').trim()
  const match = /^(\d+(?:\.\d+)?)\s*([a-zA-Z]{0,2})$/.exec(value)
  if (!match) return value
  const [, amount, rawSuffix] = match
  if (!rawSuffix) return amount
  const canonical = Object.keys(BINARY_SCALE).find((key) => key.toLowerCase() === rawSuffix.toLowerCase())
  if (canonical) return `${amount}${canonical}`
  const decimal = Object.keys(DECIMAL_SCALE).find((key) => key.toLowerCase() === rawSuffix.toLowerCase())
  // A one-letter decimal suffix is only canonicalized when unambiguous; "m"
  // stays untouched so it still fails validation rather than becoming "M".
  if (decimal && rawSuffix.length === 1 && rawSuffix !== 'm') return `${amount}${decimal}`
  return value
}

/** Formats bytes for display, preferring the largest exact binary unit. */
export function formatMemoryBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes === 0) return '0'
  const units: Array<[string, number]> = [['Ei', BINARY_SCALE.Ei], ['Pi', BINARY_SCALE.Pi], ['Ti', BINARY_SCALE.Ti], ['Gi', BINARY_SCALE.Gi], ['Mi', BINARY_SCALE.Mi], ['Ki', BINARY_SCALE.Ki]]
  for (const [suffix, scale] of units) {
    if (Math.abs(bytes) >= scale) {
      const scaled = bytes / scale
      return `${Number.isInteger(scaled) ? scaled : scaled.toFixed(1)}${suffix}`
    }
  }
  return String(bytes)
}

export function formatCpuMillis(millis: number): string {
  if (!Number.isFinite(millis)) return '0'
  return millis % 1000 === 0 ? String(millis / 1000) : `${millis}m`
}

/**
 * The single validator called during render. Returns the first problem so one
 * aggregate error renders above the actions, matching the existing fleet forms.
 */
export function allocationError(draft: AllocationDraft): string | null {
  const cpuRequest = parseCpuQuantity(draft.cpuRequest)
  const cpuLimit = parseCpuQuantity(draft.cpuLimit)
  if (cpuRequest === null) return 'CPU request must be a quantity like "500m" or "2".'
  if (cpuLimit === null) return 'CPU limit must be a quantity like "500m" or "2".'

  const memoryRequest = parseMemoryQuantity(draft.memoryRequest)
  const memoryLimit = parseMemoryQuantity(draft.memoryLimit)
  if (memoryRequest === null) return 'Memory request must be a quantity like "512Mi" or "4Gi".'
  if (memoryLimit === null) return 'Memory limit must be a quantity like "512Mi" or "4Gi".'

  if (cpuRequest === 0) return 'CPU request must be greater than zero.'
  if (cpuLimit === 0) return 'CPU limit must be greater than zero.'
  if (memoryRequest === 0) return 'Memory request must be greater than zero.'
  if (memoryLimit === 0) return 'Memory limit must be greater than zero.'

  // Compare parsed numbers so "1" and "1000m" compare equal rather than as
  // different strings.
  if (cpuRequest !== undefined && cpuLimit !== undefined && cpuRequest > cpuLimit) {
    return `CPU request (${draft.cpuRequest.trim()}) cannot exceed the CPU limit (${draft.cpuLimit.trim()}).`
  }
  if (memoryRequest !== undefined && memoryLimit !== undefined && memoryRequest > memoryLimit) {
    return `Memory request (${draft.memoryRequest.trim()}) cannot exceed the memory limit (${draft.memoryLimit.trim()}).`
  }

  for (const row of draft.selectors) {
    const key = row.key.trim()
    if (key === '' && row.value.trim() === '') continue
    if (key === '') return 'Every node selector needs a key.'
    // Two controls must not fight over one map key.
    if (key === NODE_SELECTOR_AGENT_POOL) return 'Use the Node pool field for the "agentpool" selector.'
    if (key === NODE_SELECTOR_ZONE) return 'Use the Zone field for the zone selector.'
    if (row.value.trim() === '') return `Node selector "${key}" needs a value.`
  }

  for (const row of draft.tolerations) {
    const error = tolerationError(row)
    if (error) return error
  }
  return null
}

/** Validates one toleration row; empty rows are ignored. */
export function tolerationError(row: TolerationRow): string | null {
  const key = row.key.trim()
  const value = row.value.trim()
  if (key === '' && value === '') return null
  if (row.operator === 'Exists') {
    // Kubernetes rejects a value alongside the Exists operator.
    if (value !== '') return 'A toleration using "Exists" must not set a value.'
    return null
  }
  if (key === '') return 'A toleration using "Equal" needs a key.'
  return null
}

/** Drops empty rows and applies the operator-specific field rules. */
export function tolerationsToTransport(rows: TolerationRow[]): Toleration[] {
  return rows
    .filter((row) => row.key.trim() !== '' || row.value.trim() !== '')
    .map((row) => {
      const toleration: Toleration = { key: row.key.trim(), operator: row.operator }
      if (row.operator !== 'Exists' && row.value.trim() !== '') toleration.value = row.value.trim()
      if (row.effect.trim() !== '') toleration.effect = row.effect.trim()
      return toleration
    })
}

export function tolerationsFromTransport(list: Toleration[] | undefined, nextId: () => string): TolerationRow[] {
  return (list ?? []).map((toleration) => ({
    id: nextId(),
    key: toleration.key ?? '',
    operator: toleration.operator === 'Exists' ? 'Exists' : 'Equal',
    value: toleration.value ?? '',
    effect: toleration.effect ?? '',
  }))
}

export function selectorsToTransport(rows: SelectorRow[]): Record<string, string> | undefined {
  const selector: Record<string, string> = {}
  rows.forEach((row) => {
    const key = row.key.trim()
    if (key !== '' && row.value.trim() !== '') selector[key] = row.value.trim()
  })
  return Object.keys(selector).length > 0 ? selector : undefined
}

export function selectorsFromTransport(
  selector: Record<string, string> | undefined,
  nextId: () => string,
): SelectorRow[] {
  return Object.entries(selector ?? {})
    .filter(([key]) => key !== NODE_SELECTOR_AGENT_POOL && key !== NODE_SELECTOR_ZONE)
    .map(([key, value]) => ({ id: nextId(), key, value }))
}

/** Converts a draft to the transport allocation shape. */
export function allocationToTransport(draft: AllocationDraft): PoolAllocation {
  return {
    cpu_request: draft.cpuRequest.trim() || undefined,
    cpu_limit: draft.cpuLimit.trim() || undefined,
    memory_request: draft.memoryRequest.trim() || undefined,
    memory_limit: draft.memoryLimit.trim() || undefined,
    node_pool: draft.nodePool.trim() || undefined,
    node_pool_label: draft.nodePoolLabel.trim() || undefined,
    zone: draft.zone.trim() || undefined,
    node_selector: selectorsToTransport(draft.selectors),
    tolerations: tolerationsToTransport(draft.tolerations),
  }
}

export function allocationFromTransport(
  allocation: PoolAllocation | undefined,
  clusterId: string,
  nextId: () => string,
): AllocationDraft {
  return {
    clusterId,
    cpuRequest: allocation?.cpu_request ?? '',
    cpuLimit: allocation?.cpu_limit ?? '',
    memoryRequest: allocation?.memory_request ?? '',
    memoryLimit: allocation?.memory_limit ?? '',
    nodePool: allocation?.node_pool ?? '',
    nodePoolLabel: allocation?.node_pool_label ?? '',
    zone: allocation?.zone ?? '',
    selectors: selectorsFromTransport(allocation?.node_selector, nextId),
    tolerations: tolerationsFromTransport(allocation?.tolerations, nextId),
  }
}

/**
 * The dirty-detection baseline must be computed from the TRANSPORT shape, not
 * the row arrays: re-assigned React key ids would otherwise make every load
 * look dirty and fire the navigation guard on close.
 */
export function allocationBaseline(draft: AllocationDraft): string {
  return JSON.stringify(allocationToTransport(draft))
}

export interface HeadroomDimension {
  used: number
  limit: number
  projected: number
}

export interface QuotaHeadroom {
  enforced: boolean
  cpu?: HeadroomDimension
  memory?: HeadroomDimension
  pods?: HeadroomDimension
}

/**
 * Projects the pool's requested allocation against the namespace quota so the
 * operator sees a breach BEFORE saving. Pure arithmetic — no fetching.
 */
export function quotaHeadroom(
  quotas: ClusterQuota[],
  draft: AllocationDraft,
  currentReplicas: number,
  targetReplicas: number,
  currentRequests: Record<string, string> | undefined,
): QuotaHeadroom {
  const quota = quotas.find((entry) => entry.hard && Object.keys(entry.hard).length > 0)
  if (!quota?.hard) return { enforced: false }

  const cpuPerPod = firstDefined(parseCpuQuantity(draft.cpuRequest), parseCpuQuantity(draft.cpuLimit)) ?? 0
  const memoryPerPod = firstDefined(parseMemoryQuantity(draft.memoryRequest), parseMemoryQuantity(draft.memoryLimit)) ?? 0
  const currentCpu = firstDefined(parseCpuQuantity(currentRequests?.cpu ?? '')) ?? 0
  const currentMemory = firstDefined(parseMemoryQuantity(currentRequests?.memory ?? '')) ?? 0

  const headroom: QuotaHeadroom = { enforced: true }
  const cpuHard = quotaValue(quota.hard, ['requests.cpu', 'cpu'], parseCpuQuantity)
  if (cpuHard !== undefined) {
    const used = quotaValue(quota.used, ['requests.cpu', 'cpu'], parseCpuQuantity) ?? 0
    // Projection is relative to what the workload already consumes, matching
    // the server-side preflight; computing from zero would over-report.
    headroom.cpu = { used, limit: cpuHard, projected: used + cpuPerPod * targetReplicas - currentCpu * currentReplicas }
  }
  const memoryHard = quotaValue(quota.hard, ['requests.memory', 'memory'], parseMemoryQuantity)
  if (memoryHard !== undefined) {
    const used = quotaValue(quota.used, ['requests.memory', 'memory'], parseMemoryQuantity) ?? 0
    headroom.memory = {
      used,
      limit: memoryHard,
      projected: used + memoryPerPod * targetReplicas - currentMemory * currentReplicas,
    }
  }
  const podsHard = quotaValue(quota.hard, ['pods', 'count/pods'], (raw) => Number(raw))
  if (podsHard !== undefined) {
    const used = quotaValue(quota.used, ['pods', 'count/pods'], (raw) => Number(raw)) ?? 0
    headroom.pods = { used, limit: podsHard, projected: used + targetReplicas - currentReplicas }
  }
  return headroom
}

function quotaValue(
  source: Record<string, string> | undefined,
  keys: string[],
  parse: (raw: string) => number | null | undefined,
): number | undefined {
  if (!source) return undefined
  for (const key of keys) {
    if (source[key] === undefined) continue
    const parsed = parse(source[key])
    if (typeof parsed === 'number' && Number.isFinite(parsed)) return parsed
    return undefined
  }
  return undefined
}

function firstDefined(...values: Array<number | null | undefined>): number | undefined {
  for (const value of values) {
    if (typeof value === 'number') return value
  }
  return undefined
}
