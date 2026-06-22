import { type ScriptConfig, type ScriptDefinition } from '../types/scripts'
import './ImportRuntimeOptions.css'

interface Props {
  script: ScriptDefinition
  onChange: (script: ScriptDefinition) => void
  disabled?: boolean
}

// ImportRuntimeOptions is the shared post-convert runtime settings editor: it
// edits the generated script's config (think time, pacing, timeouts, retries,
// auth) in the review phase, so every importer offers the same runtime knobs
// the sample importer exposes — without each converter needing new options.
export default function ImportRuntimeOptions({ script, onChange, disabled = false }: Props) {
  const config = script.config
  const isKafka = script.protocol === 'kafka'
  const isJMS = script.protocol?.startsWith('jms-')

  const updateConfig = (mutate: (config: ScriptConfig) => ScriptConfig) => {
    onChange({ ...script, config: mutate(structuredClone(config)) })
  }

  const thinkTimeMs = config.think_time?.mean_ms ?? 0
  const pacingType = config.pacing?.type || 'none'
  const pacingDelayMs = config.pacing?.delay_ms ?? 0
  const requestTimeoutMs = config.http?.request_timeout_ms ?? 0
  const maxRetries = config.error_handling?.max_retries ?? 0
  const continueOnError = config.error_handling?.continue_on_error ?? false
  const authType = config.http?.auth?.type || 'none'
  const kafka = config.kafka || {}
  const jms = config.jms || {}

  const numberValue = (raw: string) => Math.max(0, Number(raw) || 0)
  const updateKafka = (patch: NonNullable<ScriptConfig['kafka']>) => updateConfig(next => ({ ...next, kafka: { ...(next.kafka || {}), ...patch } }))
  const updateJMS = (patch: NonNullable<ScriptConfig['jms']>) => updateConfig(next => ({ ...next, jms: { ...(next.jms || {}), ...patch } }))

  return (
    <details className="import-runtime-options">
      <summary>
        <span>Runtime Settings</span>
        <small>{isKafka ? 'Kafka brokers · serde · replay' : isJMS ? 'JMS broker · destination · replay' : 'think time · pacing · timeouts · auth'}</small>
      </summary>
      <div className="import-runtime-grid">
        <label>
          <span>Think Time (ms)</span>
          <input
            type="number"
            min={0}
            value={thinkTimeMs}
            disabled={disabled}
            onChange={event => {
              const meanMs = numberValue(event.target.value)
              updateConfig(next => ({
                ...next,
                think_time: { ...(next.think_time || { type: 'fixed' }), type: next.think_time?.type || 'fixed', mean_ms: meanMs },
              }))
            }}
          />
        </label>
        <label>
          <span>Pacing</span>
          <select
            value={pacingType}
            disabled={disabled}
            onChange={event => {
              const type = event.target.value
              updateConfig(next => ({
                ...next,
                pacing: type === 'none' ? { type: 'none' } : { ...(next.pacing || {}), type },
              }))
            }}
          >
            <option value="none">None</option>
            <option value="fixed_delay">Fixed delay</option>
          </select>
        </label>
        {pacingType === 'fixed_delay' && (
          <label>
            <span>Pacing Delay (ms)</span>
            <input
              type="number"
              min={0}
              value={pacingDelayMs}
              disabled={disabled}
              onChange={event => {
                const delayMs = numberValue(event.target.value)
                updateConfig(next => ({
                  ...next,
                  pacing: { ...(next.pacing || { type: 'fixed_delay' }), type: 'fixed_delay', delay_ms: delayMs },
                }))
              }}
            />
          </label>
        )}
        {isKafka ? (
          <>
            <label className="import-runtime-span-2">
              <span>Brokers</span>
              <input
                value={(kafka.brokers || []).join(', ')}
                disabled={disabled}
                placeholder="broker-a:9092, broker-b:9092"
                onChange={event => updateKafka({ brokers: event.target.value.split(',').map(value => value.trim()).filter(Boolean) })}
              />
            </label>
            <label className="import-runtime-toggle">
              <input
                type="checkbox"
                checked={kafka.mock ?? true}
                disabled={disabled}
                onChange={event => updateKafka({ mock: event.target.checked })}
              />
              Mock broker
            </label>
            <label>
              <span>Offset Reset</span>
              <select value={kafka.auto_offset_reset || ''} disabled={disabled} onChange={event => updateKafka({ auto_offset_reset: event.target.value })}>
                <option value="">Default</option>
                <option value="earliest">Earliest</option>
                <option value="latest">Latest</option>
              </select>
            </label>
            <label>
              <span>Serialization</span>
              <select value={kafka.serialization || ''} disabled={disabled} onChange={event => updateKafka({ serialization: event.target.value })}>
                <option value="">Raw</option>
                <option value="json_schema">JSON Schema</option>
                <option value="avro">Avro</option>
              </select>
            </label>
            <label>
              <span>Schema Registry URL</span>
              <input value={kafka.schema_registry_url || ''} disabled={disabled} placeholder="https://registry.example.com" onChange={event => updateKafka({ schema_registry_url: event.target.value })} />
            </label>
            <label>
              <span>Schema ID</span>
              <input type="number" min={0} value={kafka.schema_id || 0} disabled={disabled} onChange={event => updateKafka({ schema_id: numberValue(event.target.value) || undefined })} />
            </label>
            <label>
              <span>Subject</span>
              <input value={kafka.subject || ''} disabled={disabled} placeholder="topic-value" onChange={event => updateKafka({ subject: event.target.value })} />
            </label>
            <label>
              <span>SASL Secret</span>
              <input value={kafka.sasl_password_secret || ''} disabled={disabled} placeholder="secret reference" onChange={event => updateKafka({ sasl_password_secret: event.target.value })} />
            </label>
          </>
        ) : isJMS ? (
          <>
            <label className="import-runtime-span-2">
              <span>Broker URL</span>
              <input value={jms.broker_url || ''} disabled={disabled} placeholder="amqps://broker.example.com:5671" onChange={event => updateJMS({ broker_url: event.target.value })} />
            </label>
            <label>
              <span>Provider</span>
              <select value={jms.provider || 'solace'} disabled={disabled} onChange={event => updateJMS({ provider: event.target.value })}>
                <option value="solace">Solace / AMQP</option>
                <option value="ibmmq">IBM MQ</option>
                <option value="servicebus">Azure Service Bus</option>
              </select>
            </label>
            <label>
              <span>Destination Type</span>
              <select value={jms.destination_type || 'queue'} disabled={disabled} onChange={event => updateJMS({ destination_type: event.target.value })}>
                <option value="queue">Queue</option>
                <option value="topic">Topic</option>
              </select>
            </label>
            <label>
              <span>Subscription</span>
              <input value={jms.subscription || ''} disabled={disabled} placeholder="asyncapi-replay" onChange={event => updateJMS({ subscription: event.target.value })} />
            </label>
            <label>
              <span>Timeout (ms)</span>
              <input type="number" min={0} value={jms.timeout_ms || 0} disabled={disabled} onChange={event => updateJMS({ timeout_ms: numberValue(event.target.value) })} />
            </label>
            <label className="import-runtime-toggle">
              <input
                type="checkbox"
                checked={jms.mock ?? true}
                disabled={disabled}
                onChange={event => updateJMS({ mock: event.target.checked })}
              />
              Mock broker
            </label>
          </>
        ) : (
          <>
            <label>
              <span>Request Timeout (ms)</span>
              <input
                type="number"
                min={0}
                value={requestTimeoutMs}
                disabled={disabled}
                onChange={event => {
                  const timeoutMs = numberValue(event.target.value)
                  updateConfig(next => ({
                    ...next,
                    http: { ...(next.http || {}), request_timeout_ms: timeoutMs },
                  }))
                }}
              />
            </label>
            <label>
              <span>Authentication</span>
              <select
                value={authType}
                disabled={disabled}
                onChange={event => {
                  const type = event.target.value
                  updateConfig(next => {
                    const http = { ...(next.http || {}) }
                    if (type === 'none') {
                      delete http.auth
                    } else if (type === 'basic') {
                      http.auth = { type: 'basic', username: '{{env.API_USERNAME}}', password: '{{env.API_PASSWORD}}' }
                    } else {
                      http.auth = { type: 'bearer', token: '{{env.API_TOKEN}}' }
                    }
                    return { ...next, http }
                  })
                }}
              >
                <option value="none">None</option>
                <option value="basic">Basic ({'{{env.API_USERNAME}}'} / {'{{env.API_PASSWORD}}'})</option>
                <option value="bearer">Bearer ({'{{env.API_TOKEN}}'})</option>
              </select>
            </label>
          </>
        )}
        <label>
          <span>Max Retries</span>
          <input
            type="number"
            min={0}
            value={maxRetries}
            disabled={disabled}
            onChange={event => {
              const retries = numberValue(event.target.value)
              updateConfig(next => ({
                ...next,
                error_handling: { ...(next.error_handling || {}), max_retries: retries },
              }))
            }}
          />
        </label>
        <label className="import-runtime-toggle">
          <input
            type="checkbox"
            checked={continueOnError}
            disabled={disabled}
            onChange={event => {
              const continueOn = event.target.checked
              updateConfig(next => ({
                ...next,
                error_handling: { ...(next.error_handling || {}), continue_on_error: continueOn },
              }))
            }}
          />
          Continue on error
        </label>
      </div>
      <p className="import-runtime-note">
        Changes apply to the generated script before save. Messaging credentials use secret references;
        supply referenced secrets in the runtime environment.
      </p>
    </details>
  )
}
