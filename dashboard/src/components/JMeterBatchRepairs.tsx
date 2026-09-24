import { useState } from 'react'
import { useApi } from '../hooks/useApi'
import type { JMeterImportResult, JMeterBatchOperation, JMeterBatchRepairResponse } from '../types/scriptImports'
import { getErrorMessage } from '../utils/errorUtils'

interface Props { result: JMeterImportResult; projectId: string; disabled?: boolean; onApplied: (value: JMeterBatchRepairResponse) => void; onBusyChange: (busy: boolean) => void }
const choices = [
  ['confirm_reference_context', 'Confirm UTC and no property overrides'],
  ['normalize_oauth_options', 'Normalize Kafka OAuth option names'],
  ['bind_kafka_credentials', 'Bind Kafka credential references'],
  ['bind_eventhubs_credentials', 'Bind Event Hubs credential reference'],
  ['utc_timestamp', 'Replace a payload timestamp with UTC'],
  ['eventhubs_property', 'Add an Event Hubs application property'],
  ['parameterize_payload', 'Parameterize a payload field'],
]
export default function JMeterBatchRepairs({ result, projectId, disabled, onApplied, onBusyChange }: Props) {
  const api = useApi()
  const [type, setType] = useState(choices[0][0])
  const [group, setGroup] = useState(0)
  const [sampler, setSampler] = useState(0)
  const [secret, setSecret] = useState('')
  const [client, setClient] = useState('')
  const [pointer, setPointer] = useState('')
  const [message, setMessage] = useState(0)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [uuid, setUUID] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [preview, setPreview] = useState<JMeterBatchRepairResponse | null>(null)
  const [reviewedOperation, setReviewedOperation] = useState<JMeterBatchOperation | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (!result.batch) return null
  const script = result.scripts[group]
  const actions = script?.actions || []
  const needsSampler = ['utc_timestamp', 'parameterize_payload', 'eventhubs_property'].includes(type)
  const operation: JMeterBatchOperation = type === 'confirm_reference_context'
    ? { type, timezone: 'UTC', confirm_no_overrides: confirmed }
    : {
      type, source_group_id: script?.config.jmeter?.source_group_id,
      ...(needsSampler ? { source_node_id: String(actions[sampler]?.metadata?.source_node_id || '') } : {}),
      ...(type.startsWith('bind_') ? { secret_name: secret } : {}),
      ...(type === 'bind_kafka_credentials' ? { client_id_secret: client } : {}),
      ...(type === 'eventhubs_property' ? { property_name: name, property_value: value, uuid } : {}),
      ...(['utc_timestamp', 'parameterize_payload'].includes(type) ? { payload_pointer: pointer, message_index: message } : {}),
      ...(type === 'parameterize_payload' ? { parameter: { name, type: 'list', values: value.split('\n'), update: 'each_iteration', selection: 'sequential' } } : {}),
    }
  const previewCurrent = preview && JSON.stringify(operation) === JSON.stringify(reviewedOperation)
  const run = async (apply: boolean) => {
    if (!result.batch || (apply && !previewCurrent)) return
    setBusy(true); onBusyChange(true); setError('')
    try {
      const response = await api.repairJMeterBatch({ batch_id: result.batch.id, revision: result.batch.revision, project_id: projectId, operations: [operation], ...(apply ? { preview_digest: preview?.preview_digest } : {}) }, apply)
      if (apply) { onApplied(response); setPreview(null); setReviewedOperation(null) }
      else { setPreview(response); setReviewedOperation(operation) }
    } catch (err) { setError(getErrorMessage(err, 'Batch repair failed. Your selections have been retained.')) }
    finally { setBusy(false); onBusyChange(false) }
  }
  return <section aria-label="Reviewed JMeter batch repairs" className="lr-warnings">
    <h4>Reviewed repairs for all scripts</h4>
    <p>Preview and apply a revision of the complete import. Remaining execution blockers stay enforced.</p>
    <fieldset disabled={disabled || busy}>
      <label>Repair <select value={type} onChange={e => setType(e.target.value)}>{choices.map(([id, label]) => <option key={id} value={id}>{label}</option>)}</select></label>
      {type === 'confirm_reference_context' ? <label><input type="checkbox" checked={confirmed} onChange={e => setConfirmed(e.target.checked)} /> Use UTC and confirm there were no JMeter property overrides</label> : <label>Source group <select value={group} onChange={e => { setGroup(Number(e.target.value)); setSampler(0) }}>{result.scripts.map((s, i) => <option key={i} value={i}>{s.name}</option>)}</select></label>}
      {needsSampler && <label>Source sampler <select value={sampler} onChange={e => setSampler(Number(e.target.value))}>{actions.map((a, i) => <option key={i} value={i}>{a.name}</option>)}</select></label>}
      {type.startsWith('bind_') && <label>Credential secret reference <input value={secret} onChange={e => setSecret(e.target.value)} autoComplete="off" /></label>}
      {type === 'bind_kafka_credentials' && <label>Client ID secret reference <input value={client} onChange={e => setClient(e.target.value)} autoComplete="off" /></label>}
      {['utc_timestamp', 'parameterize_payload'].includes(type) && <><label>Payload field (JSON pointer) <input value={pointer} onChange={e => setPointer(e.target.value)} placeholder="/event/timestamp" /></label><label>Message index <input type="number" min="0" value={message} onChange={e => setMessage(Number(e.target.value))} /></label></>}
      {['parameterize_payload', 'eventhubs_property'].includes(type) && <><label>{type === 'eventhubs_property' ? 'Property name' : 'Parameter name'} <input value={name} onChange={e => setName(e.target.value)} /></label><label>{type === 'eventhubs_property' ? 'Property value' : 'Parameter values (one per line)'} <textarea value={value} onChange={e => setValue(e.target.value)} /></label></>}
      {type === 'eventhubs_property' && <label><input type="checkbox" checked={uuid} onChange={e => setUUID(e.target.checked)} /> Generate a UUID</label>}
      <button type="button" className="btn btn-secondary" onClick={() => void run(false)}>Preview batch repair</button>
      <button type="button" className="btn btn-primary" disabled={!previewCurrent} onClick={() => void run(true)}>Apply reviewed batch repair</button>
    </fieldset>
    {error && <p role="alert">{error}</p>}
    {previewCurrent && <div aria-label="Batch repair comparison">{preview.scripts.map((s, i) => <details key={i}><summary>{s.name} — {JSON.stringify(s) === JSON.stringify(preview.before[i]) ? 'Unchanged' : 'Changed'}</summary><h5>Before</h5><pre>{JSON.stringify(preview.before[i], null, 2)}</pre><h5>After</h5><pre>{JSON.stringify(s, null, 2)}</pre></details>)}<details><summary>Scenario and remaining blockers</summary><pre>{JSON.stringify({ scenario: preview.scenario_draft, readiness: preview.readiness }, null, 2)}</pre></details></div>}
  </section>
}
