import type { JsonSuccessResponseBody } from '../types/generated/openapi'

export type ServiceBusMonitorSource = JsonSuccessResponseBody<'handleMonitorSources', '200'>[number]
type Scope = NonNullable<ServiceBusMonitorSource['management_scope']>
type Identity = NonNullable<ServiceBusMonitorSource['management_identity']>
export type ServiceBusApprovalEntity = Scope['entities'][number]

type Capabilities = { monitoring_enabled?: boolean; messaging_management_enabled?: boolean; messaging_management_packs?: readonly string[] | null }
export const serviceBusSourceAvailable = (capabilities?: Capabilities): boolean => capabilities?.monitoring_enabled === true && capabilities.messaging_management_enabled === true && capabilities.messaging_management_packs?.includes('servicebus-runtime') === true
export const newServiceBusScope = (): Scope => ({ version: 1, kind: 'servicebus_runtime', provider: '', entities: [{ service_bus: { queue: '' } }] })
export const newServiceBusIdentity = (): Identity => ({ mode: 'shared_key', principal: '', policy_revision: '', credential_ref: '' })

const validText = (value: string, limit: number) => value.length > 0 && value.trim() === value && Array.from(value).every(character => { const point = character.codePointAt(0)!; return point > 31 && !(point >= 127 && point <= 159) && !(point >= 55296 && point <= 57343) }) && new TextEncoder().encode(value).length <= limit
const validPolicyName = (value: string) => value.length > 0 && value.length <= 50 && /^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$/.test(value)
const reservedSegments = new Set(['subscriptions', 'messages', 'authorizationrules', 'rules'])
const validSegment = (value: string) => /^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$/.test(value) && !reservedSegments.has(value.toLowerCase())
const validEntityName = (value: string, limit: number, hierarchical: boolean) => value.length > 0 && value.length <= limit && (hierarchical || !value.includes('/')) && value.split('/').every(validSegment)
const validNamespace = (value: string) => {
  const labels = value.split('.')
  const literalIPv4 = labels.length === 4 && labels.every(label => /^(?:0|[1-9][0-9]{0,2})$/.test(label) && Number(label) <= 255)
  return value.length > 0 && value.length <= 253 && value === value.toLowerCase() && labels.length >= 2 && !literalIPv4 && labels.every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
}

// This path is only a canonical duplicate key for local authoring. The source
// never supplies a provider URL; the collector constructs its approved GET.
export function serviceBusEntityPath(entity: ServiceBusApprovalEntity): string | undefined {
  const serviceBus = entity.service_bus
  if (!serviceBus || entity.kafka || entity.solace) return undefined
  const queue = serviceBus.queue ?? ''
  const topic = serviceBus.topic ?? ''
  const subscription = serviceBus.subscription ?? ''
  if (queue && !topic && !subscription && validEntityName(queue, 256, true)) return `/${queue.toLowerCase()}`
  if (!queue && topic && subscription && validEntityName(topic, 256, true) && validEntityName(subscription, 50, false)) return `/${topic.toLowerCase()}/Subscriptions/${subscription.toLowerCase()}`
  return undefined
}

// This validates the initial shared-key profile. Configuration/readiness and
// the controller independently enforce the same finite approval and identity.
export function serviceBusSourceError(source: ServiceBusMonitorSource): string {
  const scope = source.management_scope
  const identity = source.management_identity
  if (source.pack !== 'servicebus-runtime' || !scope || scope.version !== 1 || scope.kind !== 'servicebus_runtime') return 'An explicit Service Bus scope is required.'
  if (!validNamespace(scope.provider)) return 'Enter the exact canonical lowercase Service Bus namespace hostname, without a URL, port or trailing dot.'
  if (scope.isolation) return 'Consumer isolation applies only to Kafka.'
  if (!identity || identity.mode !== 'shared_key') return 'Select the supported Service Bus shared-key management identity.'
  if (!validPolicyName(identity.principal) || !validText(identity.policy_revision, 256)) return 'Enter the approved Manage policy name (1–50 ASCII letters, digits, dots, hyphens or underscores, with alphanumeric boundaries) and identity policy revision.'
  if (source.credential_ref) return 'Use the separate management credential policy.'
  if (!identity.credential_ref || !validText(identity.credential_ref, 128) || !source.collector_public_key || !validText(source.collector_public_key, 128)) return 'Enter the management project-secret reference and enrolled collector public key.'
  if (!/^management:\/\/[A-Za-z0-9_.-]{1,128}$/.test(source.destination ?? '')) return 'Use a public management://alias destination.'
  if (!Number.isInteger(source.interval_seconds) || source.interval_seconds < 30 || source.interval_seconds > 3600) return 'Service Bus collection intervals must be 30–3600 seconds.'
  if (scope.entities.length < 1 || scope.entities.length > 256) return 'Approve between 1 and 256 queues or topic/subscription pairs.'
  const seen = new Set<string>()
  for (const entity of scope.entities) {
    const path = serviceBusEntityPath(entity)
    if (!path) return 'Each entity requires either a queue path or a topic path and subscription name. Queue/topic paths allow 256 ASCII characters; subscriptions allow 50 in one segment. Use letters, digits, dots, hyphens and underscores, with letters or digits at each segment boundary; reserved management segments are excluded.'
    if (seen.has(path)) return 'Remove duplicate or case-aliased Service Bus entities.'
    seen.add(path)
  }
  if (new TextEncoder().encode(JSON.stringify({ ...source, id: source.id || undefined })).length > 16 * 1024) return 'The complete source exceeds the 16 KiB limit; reduce the approved inventory.'
  return ''
}
