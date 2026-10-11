import { useId } from 'react'
import './SolaceManagementSourceFields.css'
import { newSolaceIdentity, newSolaceScope, solaceApprovalProjection, solaceSourceError, solaceSourceRenderError, solaceSourceSnapshot, type SolaceApprovalEntity, type SolaceMonitorSource as Source } from './solaceManagementSource'
type Scope = NonNullable<Source['management_scope']>
type Identity = NonNullable<Source['management_identity']>

export default function SolaceManagementSourceFields({ source: input, savedSource: savedInput, available, onChange }: { source: Source; savedSource?: Source; available: boolean; onChange: (source: Source) => void }) {
  const descriptionId = useId()
  const snapshot = solaceSourceSnapshot(input)
  const shapeError = snapshot.error || snapshot.source && solaceSourceRenderError(snapshot.source)
  if (!snapshot.source || shapeError) return <fieldset aria-label="Solace management approval"><legend>Solace management approval</legend><p role="alert">{shapeError}</p></fieldset>
  const source = snapshot.source
  const savedSnapshot = savedInput ? solaceSourceSnapshot(savedInput) : undefined
  const savedError = savedSnapshot?.error || savedSnapshot?.source && solaceSourceRenderError(savedSnapshot.source)
  const savedSource = savedError ? undefined : savedSnapshot?.source
  const savedApproval = source.id && savedSource?.id === source.id && savedSource.project_id === source.project_id && savedSource.pack === source.pack && savedSource.pack === 'solace-semp' && savedSource.management_scope?.kind === 'solace_semp' ? savedSource : undefined
  const savedProjection = savedApproval ? solaceApprovalProjection(savedApproval) : undefined
  const scope = source.management_scope ?? newSolaceScope()
  const identity = source.management_identity ?? newSolaceIdentity()
  const changeScope = (next: Scope) => onChange({ ...source, management_scope: next })
  const changeIdentity = (next: Identity) => onChange({ ...source, management_identity: { ...next, credential_version: undefined } })
  const changeEntity = (index: number, entity: SolaceApprovalEntity) => changeScope({ ...scope, entities: scope.entities.map((item, position) => position === index ? entity : item) })
  const error = solaceSourceError(source)
  const inventoryShapeValid = Array.isArray(scope.entities) && scope.entities.length <= 256 && scope.entities.every(entity => entity && typeof entity === 'object' && entity.solace && typeof entity.solace === 'object' && ['vpn', 'queue', 'topic_endpoint'].every(key => { const value = entity.solace![key as keyof NonNullable<SolaceApprovalEntity['solace']>]; return value === undefined || typeof value === 'string' && value.length <= 256 }))
  return <fieldset className="solace-management-approval" disabled={!available} aria-label="Solace management approval">
    <legend>Solace management approval</legend>
    <p>Collection reads explicit individual SEMP v2 queue and topic-endpoint depths. The collector requires local static HTTPS routing and CA trust plus permission to log out its own management session. It sends only fixed entity reads and that fixed logout operation.</p>
    {!available && <p>This project cannot configure the Solace management pack. Saved evidence remains readable.</p>}
    <label>Solace management hostname<input aria-label="Solace management hostname" aria-describedby={`${descriptionId}-hostname`} value={scope.provider} maxLength={253} onChange={event => changeScope({ ...scope, provider: event.target.value })} /><small id={`${descriptionId}-hostname`}>Enter the exact canonical lowercase hostname approved on the collector; the port and IP route remain local.</small></label>
    <label>Solace management identity<select value={identity.mode} onChange={event => changeIdentity({ ...identity, mode: event.target.value })}>{identity.mode !== 'basic' && <option value={identity.mode} disabled>{identity.mode} (unsupported by this reader)</option>}<option value="basic">Pinned Basic management principal</option></select></label>
    <label>Approved Solace management username<input aria-label="Approved Solace management username" aria-describedby={`${descriptionId}-principal`} value={identity.principal} maxLength={256} onChange={event => changeIdentity({ ...identity, principal: event.target.value })} /><small id={`${descriptionId}-principal`}>The exact principal must match the sealed bundle; a colon is not permitted.</small></label>
    <label>Identity policy revision<input value={identity.policy_revision} maxLength={256} onChange={event => changeIdentity({ ...identity, policy_revision: event.target.value })} /></label>
    <label>Management credential project-secret reference<input aria-label="Management credential project-secret reference" aria-describedby={`${descriptionId}-credential`} value={identity.credential_ref ?? ''} maxLength={128} onChange={event => changeIdentity({ ...identity, credential_ref: event.target.value })} /><small id={`${descriptionId}-credential`}>Use a purpose-bound version 2 bundle. The source stores a reference, never the password or session cookie.</small></label>
    <label>Collector X25519 public key<input value={source.collector_public_key ?? ''} maxLength={128} onChange={event => onChange({ ...source, collector_public_key: event.target.value })} /></label>
    <div className="solace-management-inventory" role="group" aria-label="Approved Solace inventory">
      {inventoryShapeValid ? scope.entities.map((entity, index) => {
        const solace = entity.solace
        const kind = solace && 'topic_endpoint' in solace ? 'topic_endpoint' : 'queue'
        return <fieldset key={index}>
          <legend>Approved Solace entity {index + 1}</legend>
          <label>VPN name {index + 1}<input value={solace?.vpn ?? ''} maxLength={256} onChange={event => changeEntity(index, { solace: { vpn: event.target.value, ...(kind === 'queue' ? { queue: solace?.queue ?? '' } : { topic_endpoint: solace?.topic_endpoint ?? '' }) } })} /></label>
          <label>Entity type {index + 1}<select value={kind} onChange={event => changeEntity(index, { solace: { vpn: solace?.vpn ?? '', ...(event.target.value === 'queue' ? { queue: '' } : { topic_endpoint: '' }) } })}><option value="queue">Queue</option><option value="topic_endpoint">Topic endpoint</option></select></label>
          <label>{kind === 'queue' ? `Queue name ${index + 1}` : `Topic endpoint name ${index + 1}`}<input value={kind === 'queue' ? solace?.queue ?? '' : solace?.topic_endpoint ?? ''} maxLength={256} onChange={event => changeEntity(index, { solace: { vpn: solace?.vpn ?? '', ...(kind === 'queue' ? { queue: event.target.value } : { topic_endpoint: event.target.value }) } })} /></label>
          <button type="button" className="btn btn-ghost btn-sm" disabled={scope.entities.length <= 1} onClick={() => changeScope({ ...scope, entities: scope.entities.filter((_, position) => position !== index) })}>Remove Solace entity {index + 1}</button>
        </fieldset>
      }) : <p role="alert">The saved inventory has an invalid shape. Reload a valid source before editing.</p>}
      <button type="button" className="btn btn-secondary btn-sm" disabled={!inventoryShapeValid || scope.entities.length >= 256} onClick={() => changeScope({ ...scope, entities: [...scope.entities, { solace: { vpn: '', queue: '' } }] })}>Add Solace entity</button>
    </div>
    {available && error && <p role="status">{error}</p>}
    <p>Ambiguous session cleanup blocks new authentication and retains the protected collector journal. Changing the source, principal or controller cannot establish provider release. Ask the administrator to resolve the retained provider session before restarting the source; this reader has no automatic session-reset operation.</p>
    {(savedError || savedApproval && !savedProjection) && <p role="alert">The saved collector approval is malformed or exceeds supported bounds. Reload a valid receipt before copying approval.</p>}
    {savedApproval && savedProjection && <details><summary>Saved collector approval · revision {savedApproval.revision}</summary><p>Copy this saved scope and identity into the local collector target. Saving a changed credential pins a new opaque version.</p><pre>{JSON.stringify(savedProjection, null, 2)}</pre></details>}
  </fieldset>
}
