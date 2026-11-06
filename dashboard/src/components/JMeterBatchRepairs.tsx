import { useEffect, useRef, useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { JMeterImportResult, JMeterBatchOperation, JMeterBatchRepairResponse } from '../types/scriptImports'
import { getErrorMessage } from '../utils/errorUtils'
import { EventHubsConnectionFormat } from './EventHubsConnectionFormat'
import { batchSecretKey } from './jmeterImportUtils'
import { JMETER_BATCH_REPAIR_CHOICES } from './jmeterIssueActions'

/**
 * Opens the form on one repair for one source group; a new nonce opens it
 * again. revision is the batch revision the request was made for: the form
 * remounts on every new revision (an applied repair, a new inspection), and a
 * request made for an earlier one is not applied again.
 */
export interface JMeterBatchRepairRequest { type: string; group: number; nonce: number; revision: string }
interface Props {
  result: JMeterImportResult; projectId: string; disabled?: boolean; request?: JMeterBatchRepairRequest | null; onApplied: (value: JMeterBatchRepairResponse) => void; onBusyChange: (busy: boolean) => void
  /**
   * Project secrets this review migrated the recorded values into, by
   * batchSecretKey(script, requirement path): the bind repairs fill these in.
   */
  migratedSecretNames?: Readonly<Record<string, string>>
}
const choices = JMETER_BATCH_REPAIR_CHOICES

// The batch endpoint applies 1-32 operations, in order, as one revision.
const MAX_BATCH_OPERATIONS = 32

const SECRET_REFERENCE = /^\s*\{\{\s*secret\.([\w.-]+)\s*\}\}\s*$/
const OTHER_ZONE = '__other__'
const EVENTHUBS_CONNECTION_PATH = '/config/eventhubs/connection_string'

function bindingRequirement(result: JMeterImportResult, group: number, path: string) {
  return (result.secret_binding_requirements || []).find(item => (item.script_index ?? 0) === group && item.path === path)
}

// The secret name for one credential of a group: the project secret this
// review migrated its recorded value into, else the importer's suggestion,
// else the reference the draft carries.
function suggestedSecretName(result: JMeterImportResult, group: number, path: string, current: unknown, migrated?: Readonly<Record<string, string>>): string {
  const requirement = bindingRequirement(result, group, path)
  const migratedName = migrated?.[batchSecretKey(group, path)]
  if (requirement && migratedName) return migratedName
  if (requirement?.suggested_name) return requirement.suggested_name
  return typeof current === 'string' ? SECRET_REFERENCE.exec(current)?.[1] || '' : ''
}

// The reference timezones worth offering: UTC, and each zone the import was
// converted with (config.jmeter.timezone, the source timezone picked at
// import). The API accepts any IANA zone, so another can be typed.
function referenceZones(result: JMeterImportResult): string[] {
  const zones = ['UTC']
  for (const script of result.scripts) {
    const zone = script.config?.jmeter?.timezone?.trim()
    if (zone && !zones.some(existing => existing.toLowerCase() === zone.toLowerCase())) zones.push(zone)
  }
  return zones
}

// True when a script was converted with time mode utc (its preserved
// time_resolution.mode): its clock parameters send true UTC, so the API
// confirms only UTC for it, and the confirmation covers every script.
function convertedWithUTCTimeMode(result: JMeterImportResult): boolean {
  return result.scripts.some(script => {
    const resolution = script.metadata?.import?.preserved?.time_resolution
    return !!resolution && typeof resolution === 'object' && (resolution as Record<string, unknown>).mode === 'utc'
  })
}

function operationLabel(operation: JMeterBatchOperation, result: JMeterImportResult): string {
  const label = choices.find(([id]) => id === operation.type)?.[1] || operation.type
  if (operation.type === 'confirm_reference_context') return `${label} · ${operation.timezone || 'no timezone'} · all scripts`
  const script = result.scripts.find(item => item.config?.jmeter?.source_group_id === operation.source_group_id)
  return `${label} · ${script?.name || 'all scripts'}`
}

export default function JMeterBatchRepairs({ result, projectId, disabled, request, onApplied, onBusyChange, migratedSecretNames }: Props) {
  const api = useApi()
  const [type, setType] = useState<string>(choices[0][0])
  const [group, setGroup] = useState(0)
  const [sampler, setSampler] = useState(0)
  // null until edited: the field shows the name the importer chose.
  const [secret, setSecret] = useState<string | null>(null)
  const [client, setClient] = useState<string | null>(null)
  const [pointer, setPointer] = useState('')
  const [message, setMessage] = useState(0)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [uuid, setUUID] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  // null until chosen: the zone the import was converted with, else UTC.
  const [zoneChoice, setZoneChoice] = useState<string | null>(null)
  const [otherZone, setOtherZone] = useState('')
  // Repairs added to this revision before the one being edited.
  const [queued, setQueued] = useState<JMeterBatchOperation[]>([])
  // The form's fields right after a repair was queued: until the operator
  // changes one, the form holds no repair of its own and is not sent.
  const [queuedFields, setQueuedFields] = useState<string | null>(null)
  const [preview, setPreview] = useState<JMeterBatchRepairResponse | null>(null)
  const [reviewedOperations, setReviewedOperations] = useState<JMeterBatchOperation[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const sectionRef = useRef<HTMLElement>(null)
  const repairSelectRef = useRef<HTMLSelectElement>(null)
  useEffect(() => {
    if (!request || request.revision !== result.batch?.revision) return
    if (choices.some(([id]) => id === request.type)) setType(request.type)
    setGroup(Math.max(0, Math.min(request.group, result.scripts.length - 1)))
    setSampler(0)
    setSecret(null)
    setClient(null)
    sectionRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
    repairSelectRef.current?.focus()
    // Only a new request moves the form; later script updates keep the operator's choice.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request?.nonce])
  if (!result.batch) return null
  const script = result.scripts[group]
  const actions = script?.actions || []
  const kafkaOAuth = script?.config?.kafka?.oauth
  const utcOnly = convertedWithUTCTimeMode(result)
  const zones = utcOnly ? ['UTC'] : referenceZones(result)
  const defaultZone = zones.find(zone => zone === result.scripts[0]?.config?.jmeter?.timezone?.trim()) || 'UTC'
  const selectedZone = utcOnly ? 'UTC' : zoneChoice ?? defaultZone
  const timezone = selectedZone === OTHER_ZONE ? otherZone.trim() : selectedZone
  const secretValue = secret ?? (type === 'bind_kafka_credentials'
    ? suggestedSecretName(result, group, '/config/kafka/oauth/client_secret_ref', kafkaOAuth?.client_secret_ref, migratedSecretNames)
    : type === 'bind_eventhubs_credentials'
      ? suggestedSecretName(result, group, EVENTHUBS_CONNECTION_PATH, script?.config?.eventhubs?.connection_string_secret || script?.config?.eventhubs?.connection_string, migratedSecretNames)
      : '')
  const clientValue = client ?? suggestedSecretName(result, group, '/config/kafka/oauth/client_id', kafkaOAuth?.client_id, migratedSecretNames)
  // The API states the connection-string format on the requirement; the
  // form's own wording is the fallback for a response without it.
  const connectionFormat = type === 'bind_eventhubs_credentials' ? bindingRequirement(result, group, EVENTHUBS_CONNECTION_PATH)?.context?.trim() : ''
  const needsSampler = ['utc_timestamp', 'parameterize_payload', 'eventhubs_property'].includes(type)
  const operation: JMeterBatchOperation = type === 'confirm_reference_context'
    ? { type, timezone, confirm_no_overrides: confirmed }
    : {
      type, source_group_id: script?.config.jmeter?.source_group_id,
      ...(needsSampler ? { source_node_id: String(actions[sampler]?.metadata?.source_node_id || '') } : {}),
      ...(type.startsWith('bind_') ? { secret_name: secretValue } : {}),
      ...(type === 'bind_kafka_credentials' ? { client_id_secret: clientValue } : {}),
      ...(type === 'eventhubs_property' ? { property_name: name, property_value: value, uuid } : {}),
      ...(['utc_timestamp', 'parameterize_payload'].includes(type) ? { payload_pointer: pointer, message_index: message } : {}),
      ...(type === 'parameterize_payload' ? { parameter: { name, type: 'list', values: value.split('\n'), update: 'each_iteration', selection: 'sequential' } } : {}),
    }
  // The repair being edited is always part of the revision, after the queued
  // ones. The preview's drafts are the exact drafts the API keeps (their
  // credentials are already {{secret.*}} references) and are shown as sent.
  // A form left as it was reset after queuing, or one that repeats a queued
  // repair, adds nothing: only the queued repairs are sent.
  const fieldsKey = (fields: { secret: string | null; client: string | null; pointer: string; message: number; name: string; value: string; uuid: boolean; confirmed: boolean }) =>
    JSON.stringify([type, group, sampler, fields.secret, fields.client, fields.pointer, fields.message, fields.name, fields.value, fields.uuid, fields.confirmed, zoneChoice, otherZone])
  const operationKey = JSON.stringify(operation)
  const alreadyQueued = queued.some(item => JSON.stringify(item) === operationKey)
  const editing = queued.length === 0 || (fieldsKey({ secret, client, pointer, message, name, value, uuid, confirmed }) !== queuedFields && !alreadyQueued)
  const operations = editing ? [...queued, operation] : queued
  const previewCurrent = preview && JSON.stringify(operations) === JSON.stringify(reviewedOperations)
  const resetOperationFields = () => {
    setSecret(null); setClient(null); setConfirmed(false); setPointer(''); setMessage(0); setName(''); setValue(''); setUUID(false)
  }
  const queueOperation = () => {
    if (!editing || alreadyQueued || operations.length >= MAX_BATCH_OPERATIONS) return
    setQueued(current => [...current, operation])
    resetOperationFields()
    setQueuedFields(fieldsKey({ secret: null, client: null, pointer: '', message: 0, name: '', value: '', uuid: false, confirmed: false }))
  }
  const run = async (apply: boolean) => {
    if (!result.batch || (apply && !previewCurrent)) return
    setBusy(true); onBusyChange(true); setError('')
    try {
      const response = await api.repairJMeterBatch({ batch_id: result.batch.id, revision: result.batch.revision, project_id: projectId, operations, ...(apply ? { preview_digest: preview?.preview_digest } : {}) }, apply)
      if (apply) { onApplied(response); setPreview(null); setReviewedOperations(null); setQueued([]) }
      else { setPreview(response); setReviewedOperations(operations) }
    } catch (err) { setError(getErrorMessage(err, 'Batch repair failed. Your selections have been retained.')) }
    finally { setBusy(false); onBusyChange(false) }
  }
  return <section ref={sectionRef} aria-label="Reviewed JMeter batch repairs" className="lr-warnings">
    <h4>Reviewed repairs for all scripts</h4>
    <p>Preview and apply a revision of the complete import. Remaining execution blockers stay enforced. Add several repairs to apply them together in one revision.</p>
    <fieldset disabled={disabled || busy}>
      <label>Repair <select ref={repairSelectRef} value={type} onChange={e => { setType(e.target.value); setSecret(null); setClient(null) }}>{choices.map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></label>
      {type === 'confirm_reference_context' ? <>
        <label>Reference timezone <select value={selectedZone} disabled={utcOnly} onChange={e => setZoneChoice(e.target.value)}>
          {zones.map(zone => <option key={zone} value={zone}>{zone === 'UTC' ? 'UTC' : `${zone} (source timezone chosen at import)`}</option>)}
          {!utcOnly && <option value={OTHER_ZONE}>Another IANA timezone</option>}
        </select></label>
        {selectedZone === OTHER_ZONE && <label>IANA timezone <input value={otherZone} onChange={e => setOtherZone(e.target.value)} placeholder="America/Chicago" autoComplete="off" /></label>}
        <p>Choose the timezone the JMeter host ran in: JMeter formats <code>__time()</code> in that zone. It is written to every script and its timestamp parameters. A plan converted with time mode utc sends true UTC time, so it confirms UTC only.</p>
        {utcOnly && <p>This import was converted with time mode utc, so only UTC can be confirmed. To reproduce another zone, import the plan again with the source time mode.</p>}
        <label><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} /> The JMeter host ran in {timezone || 'this timezone'}, and no JMeter property overrides (-J options) applied</label>
      </> : <label>Source group <select value={group} onChange={e => { setGroup(Number(e.target.value)); setSampler(0); setSecret(null); setClient(null) }}>{result.scripts.map((s, i) => <option key={i} value={i}>{s.name}</option>)}</select></label>}
      {needsSampler && <label>Source sampler <select value={sampler} onChange={e => setSampler(Number(e.target.value))}>{actions.map((a, i) => <option key={i} value={i}>{a.name}</option>)}</select></label>}
      {type.startsWith('bind_') && <p>Enter the name of an existing project secret, not its value. Create the secret in Project Secrets first{result.source_secret_session_id && result.source_secret_migration_allowed ? ', or migrate the recorded value under Recorded credential values' : ''}; the name the importer chose, or the one a recorded value was migrated to, is filled in.</p>}
      {type.startsWith('bind_') && <label>{type === 'bind_kafka_credentials' ? 'Client secret reference' : 'Connection string secret reference'} <input value={secretValue} onChange={e => setSecret(e.target.value)} autoComplete="off" aria-describedby={type === 'bind_eventhubs_credentials' ? 'jmeter-eventhubs-connection-format' : undefined} /></label>}
      {type === 'bind_eventhubs_credentials' && <EventHubsConnectionFormat id="jmeter-eventhubs-connection-format" context={connectionFormat} eventHub={script?.config?.eventhubs?.event_hub} />}
      {type === 'bind_kafka_credentials' && <label>Client ID secret reference <input value={clientValue} onChange={e => setClient(e.target.value)} autoComplete="off" /></label>}
      {['utc_timestamp', 'parameterize_payload'].includes(type) && <><label>Payload field (JSON pointer) <input value={pointer} onChange={e => setPointer(e.target.value)} placeholder="/event/timestamp" /></label><label>Message index <input type="number" min="0" value={message} onChange={e => setMessage(Number(e.target.value))} /></label></>}
      {['parameterize_payload', 'eventhubs_property'].includes(type) && <><label>{type === 'eventhubs_property' ? 'Property name' : 'Parameter name'} <input value={name} onChange={e => setName(e.target.value)} /></label><label>{type === 'eventhubs_property' ? 'Property value' : 'Parameter values (one per line)'} <textarea value={value} onChange={e => setValue(e.target.value)} /></label></>}
      {type === 'eventhubs_property' && <label><input type="checkbox" checked={uuid} onChange={e => setUUID(e.target.checked)} /> Generate a UUID</label>}
      <button type="button" className="btn btn-secondary" onClick={() => void run(false)}>{operations.length > 1 ? `Preview ${operations.length} batch repairs` : 'Preview batch repair'}</button>
      <button type="button" className="btn btn-primary" disabled={!previewCurrent} onClick={() => void run(true)}>Apply reviewed batch repair</button>
      <button type="button" className="btn btn-secondary" disabled={!editing || alreadyQueued || operations.length >= MAX_BATCH_OPERATIONS} onClick={queueOperation}>Add another repair</button>
    </fieldset>
    {queued.length > 0 && <div aria-label="Repairs in this revision">
      <p>This revision applies, in order:</p>
      <ol>
        {queued.map((item, index) => <li key={index}>{operationLabel(item, result)} <button type="button" className="btn btn-ghost btn-sm" disabled={disabled || busy} onClick={() => setQueued(current => current.filter((_, position) => position !== index))}>Remove</button></li>)}
        {editing && <li>{operationLabel(operation, result)} (being edited)</li>}
      </ol>
    </div>}
    {error && <p role="alert">{error}</p>}
    {previewCurrent && <div aria-label="Batch repair comparison">{preview.scripts.map((s, i) => <details key={i}><summary>{s.name} — {JSON.stringify(s) === JSON.stringify(preview.before[i]) ? 'Unchanged' : 'Changed'}</summary><h5>Before</h5><pre>{JSON.stringify(preview.before[i], null, 2)}</pre><h5>After</h5><pre>{JSON.stringify(s, null, 2)}</pre></details>)}<details><summary>Scenario and remaining blockers</summary><pre>{JSON.stringify({ scenario: preview.scenario_draft, readiness: preview.readiness }, null, 2)}</pre></details></div>}
  </section>
}
