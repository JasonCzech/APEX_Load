import type { JsonSuccessResponseBody } from '../types/generated/openapi'

export type SolaceMonitorSource = JsonSuccessResponseBody<'handleMonitorSources', '200'>[number]
type Scope = NonNullable<SolaceMonitorSource['management_scope']>
type Identity = NonNullable<SolaceMonitorSource['management_identity']>
export type SolaceApprovalEntity = Scope['entities'][number]
type Capabilities = { monitoring_enabled?: boolean; messaging_management_enabled?: boolean; messaging_management_packs?: readonly string[] | null }
export const solaceSourceAvailable = (capabilities?: Capabilities): boolean => capabilities?.monitoring_enabled === true && capabilities.messaging_management_enabled === true && capabilities.messaging_management_packs?.includes('solace-semp') === true
export const newSolaceScope = (): Scope => ({ version: 1, kind: 'solace_semp', provider: '', entities: [{ solace: { vpn: '', queue: '' } }] })
export const newSolaceIdentity = (): Identity => ({ mode: 'basic', principal: '', policy_revision: '', credential_ref: '' })

const sourceShapeError = 'The monitor source is not finite plain JSON within the 16 KiB source limit. Reload a valid source before editing.'

// Own data descriptors are read only after finite population admission. The
// owned clone omits non-enumerable metadata and never invokes input getters.
export function solaceSourceSnapshot(input: SourceInput): { source?: SolaceMonitorSource; error?: string } {
  let bytes = 0; let nodes = 0
  const parents = new Set<object>()
  const charge = (n: number) => { bytes += n; if (bytes > 16 * 1024) throw new Error(sourceShapeError) }
  const text = (value: string) => {
    if (value.length > 16 * 1024) throw new Error(sourceShapeError)
    for (let i = 0; i < value.length; i++) {
      const code = value.charCodeAt(i)
      if (code >= 0xd800 && code <= 0xdbff) { const low = value.charCodeAt(++i); if (!(low >= 0xdc00 && low <= 0xdfff)) throw new Error(sourceShapeError) }
      else if (code >= 0xdc00 && code <= 0xdfff) throw new Error(sourceShapeError)
    }
    const encoded = JSON.stringify(value)
    if (encoded.length > 16 * 1024 - bytes) throw new Error(sourceShapeError)
    charge(new TextEncoder().encode(encoded).length)
  }
  const clone = (value: unknown, depth: number): unknown => {
    if (++nodes > 2048 || depth > 8) throw new Error(sourceShapeError)
    if (value === null) { charge(4); return null }
    if (typeof value === 'string') { text(value); return value }
    if (typeof value === 'boolean') { charge(value ? 4 : 5); return value }
    if (typeof value === 'number' && Number.isFinite(value)) { charge(JSON.stringify(value).length); return value }
    if (!value || typeof value !== 'object' || parents.has(value)) throw new Error(sourceShapeError)
    const prototype = Object.getPrototypeOf(value)
    const array = Array.isArray(value)
    if (array ? prototype !== Array.prototype : prototype !== Object.prototype && prototype !== null) throw new Error(sourceShapeError)
    parents.add(value)
    try {
      charge(2)
      if (array) {
        const length = Object.getOwnPropertyDescriptor(value, 'length')?.value as unknown
        if (typeof length !== 'number' || length > 256) throw new Error(sourceShapeError)
        const result: unknown[] = []
        for (let i = 0; i < length; i++) {
          const descriptor = Object.getOwnPropertyDescriptor(value, String(i))
          if (!descriptor || !('value' in descriptor) || !descriptor.enumerable) throw new Error(sourceShapeError)
          if (i) charge(1)
          result.push(clone(descriptor.value, depth + 1))
        }
        return result
      }
      const result: Record<string, unknown> = Object.create(null)
      let count = 0; let included = 0
      for (const key in value) {
        if (!Object.prototype.hasOwnProperty.call(value, key)) continue
        if (++count > 64 || key.length > 256) throw new Error(sourceShapeError)
        const descriptor = Object.getOwnPropertyDescriptor(value, key)
        if (!descriptor || !('value' in descriptor)) throw new Error(sourceShapeError)
        if (descriptor.value === undefined) continue // JSON object omission only.
        if (included++) charge(1)
        text(key); charge(1)
        result[key] = clone(descriptor.value, depth + 1)
      }
      return result
    } finally { parents.delete(value) }
  }
  try {
    const source = clone(input, 0)
    if (!source || typeof source !== 'object' || Array.isArray(source)) throw new Error(sourceShapeError)
    return { source: source as SolaceMonitorSource }
  } catch { return { error: sourceShapeError } }
}
type SourceInput = unknown

// Semantic blanks remain editable, but malformed scalar/object shapes never
// reach controlled inputs or inventory JSX.
export function solaceSourceRenderError(source: SolaceMonitorSource): string | undefined {
  const textShape = (value: unknown, limit: number, optional = false) => optional && value === undefined || typeof value === 'string' && value.length <= limit
  const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
  for (const field of ['id', 'project_id', 'name', 'collector_id', 'pack'] as const) if (!textShape(source[field], 256)) return sourceShapeError
  for (const field of ['destination', 'credential_ref', 'service'] as const) if (!textShape(source[field], 256, true)) return sourceShapeError
  if (!Number.isSafeInteger(source.revision) || source.revision < 0 || !textShape(source.collector_public_key, 128, true)) return sourceShapeError
  if (source.management_scope !== undefined) {
    const scope = source.management_scope
    if (!object(scope) || typeof scope.version !== 'number' || !textShape(scope.kind, 64) || !textShape(scope.provider, 253) || !textShape(scope.isolation, 64, true) || !Array.isArray(scope.entities) || scope.entities.length > 256) return sourceShapeError
    for (const entity of scope.entities) {
      if (!object(entity) || !object(entity.solace) || !textShape(entity.solace.vpn, 256) || !textShape(entity.solace.queue, 256, true) || !textShape(entity.solace.topic_endpoint, 256, true)) return sourceShapeError
    }
  }
  if (source.management_identity !== undefined) {
    const identity = source.management_identity
    if (!object(identity) || !textShape(identity.mode, 64) || !textShape(identity.principal, 256) || !textShape(identity.policy_revision, 256) || !textShape(identity.credential_ref, 128, true) || !textShape(identity.credential_version, 256, true)) return sourceShapeError
  }
}

const boundaryWhitespace = /^[\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]|[\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]$/u
const validText = (value: unknown, limit: number): value is string => typeof value === 'string' && value.length > 0 && value.length <= limit && !boundaryWhitespace.test(value) && Array.from(value).every(character => { const point = character.codePointAt(0)!; return point > 31 && !(point >= 127 && point <= 159) && !(point >= 55296 && point <= 57343) }) && new TextEncoder().encode(value).length <= limit
const validProvider = (value: string) => {
  if (typeof value !== 'string' || value.length < 1 || value.length > 253 || value !== value.toLowerCase()) return false
  const labels = value.split('.')
  const ipv4 = labels.length === 4 && labels.every(label => /^(?:0|[1-9][0-9]{0,2})$/.test(label) && Number(label) <= 255)
  return labels.length >= 2 && !ipv4 && labels.every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
}

// The tuple is a local duplicate key only. The collector constructs the fixed,
// escaped individual SEMP route from its immutable local approval.
export function solaceEntityIdentity(entity: SolaceApprovalEntity): string | undefined {
  const solace = entity?.solace
  if (!solace || entity.kafka || entity.service_bus || !validText(solace.vpn, 256)) return
  const queue = solace.queue ?? ''
  const endpoint = solace.topic_endpoint ?? ''
  if (queue && !endpoint && validText(queue, 256)) return JSON.stringify([solace.vpn, 'queue', queue])
  if (endpoint && !queue && validText(endpoint, 256)) return JSON.stringify([solace.vpn, 'topic_endpoint', endpoint])
}

export function solaceSourceError(input: SolaceMonitorSource): string {
  const snapshot = solaceSourceSnapshot(input)
  if (!snapshot.source) return snapshot.error!
  const source = snapshot.source
  const shape = solaceSourceRenderError(source)
  if (shape) return shape
  const scope = source.management_scope
  const identity = source.management_identity
  if (source.pack !== 'solace-semp' || !scope || scope.version !== 1 || scope.kind !== 'solace_semp') return 'An explicit Solace scope is required.'
  if (!validProvider(scope.provider)) return 'Enter the exact canonical lowercase Solace management hostname, without a URL, port or trailing dot.'
  if (scope.isolation) return 'Consumer isolation applies only to Kafka.'
  if (!identity || identity.mode !== 'basic') return 'Select the supported Solace Basic management identity.'
  if (!validText(identity.principal, 256) || identity.principal.includes(':') || !validText(identity.policy_revision, 256)) return 'Enter the approved management username without a colon and its identity policy revision.'
  if (source.credential_ref) return 'Use the separate management credential policy.'
  if (!validText(identity.credential_ref, 128) || !validText(source.collector_public_key, 128)) return 'Enter the management project-secret reference and enrolled collector public key.'
  if (!/^management:\/\/[A-Za-z0-9_.-]{1,128}$/.test(source.destination ?? '')) return 'Use a public management://alias destination.'
  if (!Number.isInteger(source.interval_seconds) || source.interval_seconds < 30 || source.interval_seconds > 3600) return 'Solace collection intervals must be 30–3600 seconds.'
  if (!Array.isArray(scope.entities) || scope.entities.length < 1 || scope.entities.length > 256) return 'Approve between 1 and 256 VPN queue or topic-endpoint tuples.'
  const seen = new Set<string>()
  for (const entity of scope.entities) {
    const key = solaceEntityIdentity(entity)
    if (!key) return 'Each entity requires a VPN and exactly one queue or topic endpoint. Each name is exact, nonblank text of at most 256 UTF-8 bytes without boundary whitespace or control characters.'
    if (seen.has(key)) return 'Remove duplicate Solace entities.'
    seen.add(key)
  }
  return ''
}

// Disclose only the closed, validated public approval fields. Unknown metadata
// is never rendered merely because a caller supplied a savedSource object.
export function solaceApprovalProjection(input: SolaceMonitorSource): object | undefined {
  const snapshot = solaceSourceSnapshot(input)
  if (!snapshot.source || solaceSourceError(snapshot.source)) return
  const source = snapshot.source
  const scope = source.management_scope!; const identity = source.management_identity!
  return {
    scope: { version: scope.version, kind: scope.kind, provider: scope.provider, entities: scope.entities.map(entity => ({ solace: { vpn: entity.solace!.vpn, ...(entity.solace!.queue ? { queue: entity.solace!.queue } : { topic_endpoint: entity.solace!.topic_endpoint }) } })) },
    identity: { mode: identity.mode, principal: identity.principal, policy_revision: identity.policy_revision, credential_ref: identity.credential_ref, credential_version: identity.credential_version },
  }
}
