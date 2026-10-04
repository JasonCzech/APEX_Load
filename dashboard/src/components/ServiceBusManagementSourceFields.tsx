import { useId } from 'react'
import './ServiceBusManagementSourceFields.css'
import { newServiceBusIdentity, newServiceBusScope, serviceBusSourceError, type ServiceBusApprovalEntity, type ServiceBusMonitorSource as Source } from './serviceBusManagementSource'
type Scope = NonNullable<Source['management_scope']>
type Identity = NonNullable<Source['management_identity']>

export default function ServiceBusManagementSourceFields({ source, savedSource, available, onChange }: { source: Source; savedSource?: Source; available: boolean; onChange: (source: Source) => void }) {
  const descriptionId = useId()
  const savedApproval = source.id && savedSource?.id === source.id && savedSource.project_id === source.project_id && savedSource.pack === source.pack && savedSource.pack === 'servicebus-runtime' && savedSource.management_scope?.kind === 'servicebus_runtime' ? savedSource : undefined
  const scope = source.management_scope ?? newServiceBusScope()
  const identity = source.management_identity ?? newServiceBusIdentity()
  const changeScope = (next: Scope) => onChange({ ...source, management_scope: next })
  const changeIdentity = (next: Identity) => onChange({ ...source, management_identity: { ...next, credential_version: undefined } })
  const changeEntity = (index: number, entity: ServiceBusApprovalEntity) => changeScope({ ...scope, entities: scope.entities.map((item, position) => position === index ? entity : item) })
  const error = serviceBusSourceError(source)
  return <fieldset className="servicebus-management-approval" disabled={!available} aria-label="Service Bus management approval">
    <legend>Service Bus management approval</legend>
    <p>Collection reads runtime counts for the explicit queues and subscriptions without consuming messages. Namespace HTTPS routing, static IP mapping and CA trust are approved locally on the collector.</p>
    {!available && <p>This project cannot configure the Service Bus management pack. Saved evidence remains readable.</p>}
    <label>Service Bus namespace hostname<input aria-label="Service Bus namespace hostname" aria-describedby={`${descriptionId}-namespace`} value={scope.provider} maxLength={253} onChange={event => changeScope({ ...scope, provider: event.target.value })} /><small id={`${descriptionId}-namespace`}>Enter the exact canonical lowercase hostname approved on the collector, without a URL or port.</small></label>
    <label>Service Bus management identity<select value={identity.mode} onChange={event => changeIdentity({ ...identity, mode: event.target.value })}>{identity.mode !== 'shared_key' && <option value={identity.mode} disabled>{identity.mode} (unsupported by this reader)</option>}<option value="shared_key">Shared-key Manage policy</option></select></label>
    <label>Approved Service Bus Manage policy name<input aria-label="Approved Service Bus Manage policy name" aria-describedby={`${descriptionId}-policy`} value={identity.principal} maxLength={50} onChange={event => changeIdentity({ ...identity, principal: event.target.value })} /><small id={`${descriptionId}-policy`}>The 1–50 character ASCII policy name must match the sealed management bundle. Manage policies can authorize changes; collection uses fixed reads.</small></label>
    <label>Identity policy revision<input value={identity.policy_revision} maxLength={256} onChange={event => changeIdentity({ ...identity, policy_revision: event.target.value })} /></label>
    <label>Management credential project-secret reference<input aria-label="Management credential project-secret reference" aria-describedby={`${descriptionId}-credential`} value={identity.credential_ref ?? ''} maxLength={128} onChange={event => changeIdentity({ ...identity, credential_ref: event.target.value })} /><small id={`${descriptionId}-credential`}>Use a purpose-bound version 2 shared-key bundle. The source contains the reference and never the key or a connection string.</small></label>
    <label>Collector X25519 public key<input value={source.collector_public_key ?? ''} maxLength={128} onChange={event => onChange({ ...source, collector_public_key: event.target.value })} /></label>
    <div className="servicebus-management-inventory" role="group" aria-label="Approved Service Bus inventory">
      {scope.entities.map((entity, index) => {
        const serviceBus = entity.service_bus
        const kind = serviceBus && ('topic' in serviceBus || 'subscription' in serviceBus) ? 'subscription' : 'queue'
        return <fieldset key={index}>
          <legend>Approved Service Bus entity {index + 1}</legend>
          <label>Entity type {index + 1}<select value={kind} onChange={event => changeEntity(index, { service_bus: event.target.value === 'queue' ? { queue: '' } : { topic: '', subscription: '' } })}><option value="queue">Queue</option><option value="subscription">Topic subscription</option></select></label>
          {kind === 'queue' ? <label>Queue path {index + 1}<input value={serviceBus?.queue ?? ''} maxLength={256} onChange={event => changeEntity(index, { service_bus: { queue: event.target.value } })} /></label> : <>
            <label>Topic path {index + 1}<input value={serviceBus?.topic ?? ''} maxLength={256} onChange={event => changeEntity(index, { service_bus: { topic: event.target.value, subscription: serviceBus?.subscription ?? '' } })} /></label>
            <label>Subscription name {index + 1}<input value={serviceBus?.subscription ?? ''} maxLength={50} onChange={event => changeEntity(index, { service_bus: { topic: serviceBus?.topic ?? '', subscription: event.target.value } })} /></label>
          </>}
          <button type="button" disabled={scope.entities.length <= 1} onClick={() => changeScope({ ...scope, entities: scope.entities.filter((_, position) => position !== index) })}>Remove Service Bus entity {index + 1}</button>
        </fieldset>
      })}
      <button type="button" disabled={scope.entities.length >= 256} onClick={() => changeScope({ ...scope, entities: [...scope.entities, { service_bus: { queue: '' } }] })}>Add Service Bus entity</button>
    </div>
    {available && error && <p role="status">{error}</p>}
    {savedApproval?.management_scope && savedApproval.management_identity && <details><summary>Saved collector approval · revision {savedApproval.revision}</summary><p>Copy this saved scope and identity into the local collector target. Saving a changed credential pins a new opaque version.</p><pre>{JSON.stringify({ scope: savedApproval.management_scope, identity: savedApproval.management_identity }, null, 2)}</pre></details>}
  </fieldset>
}
