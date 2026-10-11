import { useId } from 'react'
import './KafkaManagementSourceFields.css'
import { kafkaSourceError, newKafkaIdentity, newKafkaScope, type KafkaMonitorSource as Source } from './kafkaManagementSource'
type Scope = NonNullable<Source['management_scope']>
type Identity = NonNullable<Source['management_identity']>

export default function KafkaManagementSourceFields({ source, savedSource, available, onChange }: { source: Source; savedSource?: Source; available: boolean; onChange: (source: Source) => void }) {
  const descriptionId = useId()
  const savedApproval = savedSource?.id && savedSource.id === source.id && savedSource.project_id === source.project_id && savedSource.pack === 'kafka-offsets' && savedSource.management_scope?.kind === 'kafka_offsets' ? savedSource : undefined
  const scope = source.management_scope ?? newKafkaScope()
  const identity = source.management_identity ?? newKafkaIdentity()
  const changeScope = (next: Scope) => onChange({ ...source, management_scope: next })
  const changeIdentity = (next: Identity) => onChange({ ...source, management_identity: { ...next, credential_version: undefined } })
  const error = kafkaSourceError(source)
  return <fieldset className="kafka-management-approval" disabled={!available} aria-label="Kafka management approval">
    <legend>Kafka management approval</legend>
    <p>Collection reads offsets for the explicit inventory without consuming records or joining a group. TLS endpoints, static IP routes and CA trust are approved locally on the collector.</p>
    {!available && <p>This project cannot configure the Kafka management pack. Saved evidence remains readable.</p>}
    <label>Kafka cluster ID<input aria-label="Kafka cluster ID" aria-describedby={`${descriptionId}-cluster`} value={scope.provider} maxLength={256} onChange={event => changeScope({ ...scope, provider: event.target.value })} /><small id={`${descriptionId}-cluster`}>Must match the broker cluster ID and the collector's immutable approval.</small></label>
    <label>Consumer isolation<select value={scope.isolation} onChange={event => changeScope({ ...scope, isolation: event.target.value })}><option value="read_uncommitted">Read uncommitted · high watermark</option><option value="read_committed">Read committed · last stable offset</option></select></label>
    <label>SASL identity<select value={identity.mode} onChange={event => changeIdentity({ ...identity, mode: event.target.value })}>{!['sasl_plain', 'sasl_scram'].includes(identity.mode) && <option value={identity.mode} disabled>{identity.mode} (unsupported by this reader)</option>}<option value="sasl_plain">SASL PLAIN over TLS</option><option value="sasl_scram">SASL SCRAM over TLS</option></select></label>
    <label>Approved SASL username<input value={identity.principal} maxLength={256} onChange={event => changeIdentity({ ...identity, principal: event.target.value })} /></label>
    <label>Identity policy revision<input value={identity.policy_revision} maxLength={256} onChange={event => changeIdentity({ ...identity, policy_revision: event.target.value })} /></label>
    <label>Management credential project-secret reference<input aria-label="Management credential project-secret reference" aria-describedby={`${descriptionId}-credential`} value={identity.credential_ref ?? ''} maxLength={128} onChange={event => changeIdentity({ ...identity, credential_ref: event.target.value })} /><small id={`${descriptionId}-credential`}>Use a purpose-bound version 2 management SASL bundle. The source never contains its password.</small></label>
    <label>Collector X25519 public key<input value={source.collector_public_key ?? ''} onChange={event => onChange({ ...source, collector_public_key: event.target.value })} /></label>
    <div className="kafka-management-inventory" aria-label="Approved Kafka inventory">
      {scope.entities.map((entity, index) => <fieldset key={index}>
        <legend>Approved entity {index + 1}</legend>
        <label>Topic {index + 1}<input value={entity.kafka?.topic ?? ''} maxLength={256} onChange={event => changeScope({ ...scope, entities: scope.entities.map((item, position) => position === index ? { kafka: { topic: event.target.value, partition: item.kafka?.partition ?? 0, group: item.kafka?.group ?? '' } } : item) })} /></label>
        <label>Partition {index + 1}<input type="number" min={0} max={2147483647} step={1} value={Number.isFinite(entity.kafka?.partition) ? entity.kafka?.partition : ''} onChange={event => changeScope({ ...scope, entities: scope.entities.map((item, position) => position === index ? { kafka: { topic: item.kafka?.topic ?? '', partition: event.target.value === '' ? Number.NaN : Number(event.target.value), group: item.kafka?.group ?? '' } } : item) })} /></label>
        <label>Consumer group {index + 1}<input value={entity.kafka?.group ?? ''} maxLength={256} onChange={event => changeScope({ ...scope, entities: scope.entities.map((item, position) => position === index ? { kafka: { topic: item.kafka?.topic ?? '', partition: item.kafka?.partition ?? 0, group: event.target.value } } : item) })} /></label>
        <button type="button" className="btn btn-ghost btn-sm" disabled={scope.entities.length <= 1} onClick={() => changeScope({ ...scope, entities: scope.entities.filter((_, position) => position !== index) })}>Remove entity {index + 1}</button>
      </fieldset>)}
      <button type="button" className="btn btn-secondary btn-sm" disabled={scope.entities.length >= 256} onClick={() => changeScope({ ...scope, entities: [...scope.entities, { kafka: { topic: '', partition: 0, group: '' } }] })}>Add Kafka entity</button>
    </div>
    {available && error && <p role="status">{error}</p>}
    {savedApproval?.management_scope && savedApproval.management_identity && <details><summary>Saved collector approval · revision {savedApproval.revision}</summary><p>Copy this saved scope and identity into the local collector target. Saving a changed credential pins a new opaque version.</p><pre>{JSON.stringify({ scope: savedApproval.management_scope, identity: savedApproval.management_identity }, null, 2)}</pre></details>}
  </fieldset>
}
