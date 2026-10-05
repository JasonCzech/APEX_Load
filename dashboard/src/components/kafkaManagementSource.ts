import type { JsonSuccessResponseBody } from '../types/generated/openapi'

export type KafkaMonitorSource = JsonSuccessResponseBody<'handleMonitorSources', '200'>[number]
type Scope = NonNullable<KafkaMonitorSource['management_scope']>
type Identity = NonNullable<KafkaMonitorSource['management_identity']>

export const newKafkaScope = (): Scope => ({ version: 1, kind: 'kafka_offsets', provider: '', isolation: 'read_uncommitted', entities: [{ kafka: { topic: '', partition: 0, group: '' } }] })
export const newKafkaIdentity = (): Identity => ({ mode: 'sasl_plain', principal: '', policy_revision: '', credential_ref: '' })
const validText = (value: string, limit: number) => value.length > 0 && value.trim() === value && Array.from(value).every(character => { const point = character.codePointAt(0)!; return point > 31 && !(point >= 127 && point <= 159) && !(point >= 55296 && point <= 57343) }) && new TextEncoder().encode(value).length <= limit

// Client validation helps operators correct a draft; the controller and the
// collector independently enforce scope, principal and transport approval.
export function kafkaSourceError(source: KafkaMonitorSource): string {
  const scope = source.management_scope
  const identity = source.management_identity
  if (!scope || scope.version !== 1 || scope.kind !== 'kafka_offsets') return 'An explicit Kafka scope is required.'
  if (!validText(scope.provider, 256)) return 'Enter the exact Kafka cluster ID.'
  if (!['read_committed', 'read_uncommitted'].includes(scope.isolation ?? '')) return 'Select the consumer isolation mode.'
  if (!identity || !['sasl_plain', 'sasl_scram'].includes(identity.mode)) return 'Select a supported Kafka SASL identity.'
  if (!validText(identity.principal, 256) || !validText(identity.policy_revision, 256)) return 'Enter the approved SASL username and policy revision.'
  if (!identity.credential_ref || !validText(identity.credential_ref, 128) || !source.collector_public_key) return 'Enter the project secret reference and enrolled collector public key.'
  if (!/^management:\/\/[A-Za-z0-9_.-]{1,128}$/.test(source.destination ?? '')) return 'Use a public management://alias destination.'
  if (!Number.isInteger(source.interval_seconds) || source.interval_seconds < 10 || source.interval_seconds > 3600) return 'Kafka collection intervals must be 10–3600 seconds.'
  if (scope.entities.length < 1 || scope.entities.length > 256) return 'Approve between 1 and 256 topic, partition and group entities.'
  const seen = new Set<string>()
  for (const entity of scope.entities) {
    const kafka = entity.kafka
    if (!kafka || entity.service_bus || entity.solace || !validText(kafka.topic, 256) || !validText(kafka.group, 256) || !Number.isInteger(kafka.partition) || kafka.partition < 0 || kafka.partition > 2147483647) return 'Each entity requires a topic, a nonnegative int32 partition and a consumer group.'
    const key = JSON.stringify([kafka.topic, kafka.partition, kafka.group])
    if (seen.has(key)) return 'Remove duplicate topic, partition and group entities.'
    seen.add(key)
  }
  if (new TextEncoder().encode(JSON.stringify({ ...source, id: source.id || undefined })).length > 16 * 1024) return 'The complete source exceeds the 16 KiB limit; reduce the approved inventory.'
  return ''
}
