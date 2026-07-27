import { type ScriptConfig, type ScriptDefinition } from '../types/scripts'
import { jmsConfigForProtocol, jmsProtocolForProvider } from './scriptEditorHelpers'
import {
  canonicalThinkTimeType,
	thinkTimeConfigForType,
	thinkTimeUsesBounds,
  thinkTimeUsesMean,
  thinkTimeUsesStdDeviation,
} from './thinkTimeConfig'
import ThinkTimeMultiplierInput from './ThinkTimeMultiplierInput'
import SecondsFromMillisecondsInput from './SecondsFromMillisecondsInput'
import './ImportRuntimeOptions.css'

interface Props {
  draftScope?: string
  script: ScriptDefinition
  onChange: (script: ScriptDefinition) => void
  disabled?: boolean
}

// ImportRuntimeOptions is the shared post-convert runtime settings editor: it
// edits the generated script's config (think time, pacing, timeouts, retries,
// auth) in the review phase, so every importer offers the same runtime knobs
// the sample importer exposes — without each converter needing new options.
export default function ImportRuntimeOptions({ draftScope, script, onChange, disabled = false }: Props) {
  const config = script.config
  const isKafka = script.protocol === 'kafka'
  const isJMS = script.protocol?.startsWith('jms-')

  const updateConfig = (mutate: (config: ScriptConfig) => ScriptConfig) => {
    onChange({ ...script, config: mutate(structuredClone(config)) })
  }

  const thinkTimeMs = config.think_time?.mean_ms ?? 0
  const thinkTimeType = canonicalThinkTimeType(config.think_time?.type || 'fixed')
  const pacingType = config.pacing?.type || 'none'
  const requestTimeoutMs = config.http?.request_timeout_ms ?? 0
  const maxRetries = config.error_handling?.max_retries ?? 0
  const continueOnError = config.error_handling?.continue_on_error ?? false
  const authType = config.http?.auth?.type || 'none'
  const kafka = config.kafka || {}
  const jms = config.jms || {}

  const numberValue = (raw: string) => Math.max(0, Number(raw) || 0)
  const updateKafka = (patch: NonNullable<ScriptConfig['kafka']>) => updateConfig(next => ({ ...next, kafka: { ...(next.kafka || {}), ...patch } }))
  const updateJMS = (patch: NonNullable<ScriptConfig['jms']>) => updateConfig(next => ({ ...next, jms: { ...(next.jms || {}), ...patch } }))
  const updateJMSProvider = (provider: string) => {
    const protocol = jmsProtocolForProvider(provider)
    if (!protocol) return
    const nextConfig = structuredClone(config)
    nextConfig.jms = jmsConfigForProtocol(protocol, nextConfig.jms || {}) || undefined
    onChange({ ...script, protocol, config: nextConfig })
  }

  return (
    <details className="import-runtime-options">
      <summary>
        <span>Runtime Settings</span>
        <small>{isKafka ? 'Kafka brokers · serde · replay' : isJMS ? 'JMS broker · destination · replay' : 'think time · pacing · timeouts · auth'}</small>
      </summary>
      <div className="import-runtime-grid">
        <label>
          <span>Think Time Mode</span>
          <select
            value={thinkTimeType}
            disabled={disabled}
            onChange={event => updateConfig(next => ({
              ...next,
              think_time: thinkTimeConfigForType(next.think_time, event.target.value),
            }))}
          >
            <option value="none">None</option>
            <option value="fixed">Fixed</option>
            <option value="recorded">Replay think time as recorded</option>
            <option value="gaussian">Gaussian</option>
            <option value="uniform">Uniform</option>
          </select>
        </label>
        {thinkTimeUsesMean(thinkTimeType) && (
        <label>
          <span>{thinkTimeType === 'gaussian' ? 'Mean Think Time (ms)' : 'Think Time (ms)'}</span>
          <input
            type="number"
            min={0}
            value={thinkTimeMs}
            disabled={disabled}
            onChange={event => {
              const meanMs = numberValue(event.target.value)
              updateConfig(next => ({
                ...next,
                think_time: thinkTimeConfigForType({
                  ...(next.think_time || { type: 'fixed' }),
                  mean_ms: meanMs,
                }, thinkTimeType),
              }))
            }}
          />
        </label>
        )}
        {thinkTimeUsesStdDeviation(thinkTimeType) && (
          <label>
            <span>Think Time Std Dev (ms)</span>
            <input type="number" min={0} value={config.think_time?.std_ms ?? 0} disabled={disabled}
              onChange={event => updateConfig(next => ({
                ...next,
                think_time: {
                  ...thinkTimeConfigForType(next.think_time, next.think_time?.type || thinkTimeType),
                  std_ms: numberValue(event.target.value),
                },
              }))} />
          </label>
        )}
        {thinkTimeUsesBounds(thinkTimeType) && (
          <>
            <label>
              <span>Minimum Think Time (ms)</span>
              <input type="number" min={0} value={config.think_time?.min_ms ?? 0} disabled={disabled}
                onChange={event => updateConfig(next => ({
                  ...next,
                  think_time: {
                    ...thinkTimeConfigForType(next.think_time, next.think_time?.type || thinkTimeType),
                    min_ms: numberValue(event.target.value),
                  },
                }))} />
            </label>
            <label>
              <span>Maximum Think Time (ms)</span>
              <input type="number" min={0} value={config.think_time?.max_ms ?? 0} disabled={disabled}
                onChange={event => updateConfig(next => ({
                  ...next,
                  think_time: {
                    ...thinkTimeConfigForType(next.think_time, next.think_time?.type || thinkTimeType),
                    max_ms: numberValue(event.target.value),
                  },
                }))} />
            </label>
          </>
        )}
        <ThinkTimeMultiplierInput
          draftScope={draftScope}
          label="Think Time Multiplier"
          value={config.think_time?.multiplier}
          disabled={disabled}
          onValueChange={multiplier => updateConfig(next => ({
            ...next,
            think_time: {
              ...thinkTimeConfigForType(next.think_time, next.think_time?.type || thinkTimeType),
              multiplier,
            },
          }))}
        />
        <label>
          <span>Think Time Cap (ms)</span>
          <input type="number" min={0} value={config.think_time?.limit_ms ?? 0} disabled={disabled}
            onChange={event => updateConfig(next => ({
              ...next,
              think_time: {
                ...thinkTimeConfigForType(next.think_time, next.think_time?.type || thinkTimeType),
                limit_ms: numberValue(event.target.value),
              },
            }))} />
        </label>
        <label className="import-runtime-toggle">
          <input type="checkbox" checked={config.think_time?.ignore ?? false} disabled={disabled}
            onChange={event => updateConfig(next => ({
              ...next,
              think_time: {
                ...thinkTimeConfigForType(next.think_time, next.think_time?.type || thinkTimeType),
                ignore: event.target.checked,
              },
            }))} />
          Ignore all think times
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
            <option value="random_delay">Random delay</option>
            <option value="fixed_interval">Fixed interval</option>
            <option value="random_interval">Random interval</option>
            <option value="tps">Target TPS</option>
          </select>
        </label>
        {pacingType === 'fixed_delay' && (
          <label>
            <span>Pacing Delay (seconds)</span>
            <SecondsFromMillisecondsInput
              valueMs={config.pacing?.delay_ms}
              disabled={disabled}
              onChangeMs={delayMs => {
                updateConfig(next => ({
                  ...next,
                  pacing: { ...(next.pacing || { type: 'fixed_delay' }), type: 'fixed_delay', delay_ms: delayMs ?? 0 },
                }))
              }}
            />
          </label>
        )}
        {pacingType === 'fixed_interval' && (
          <label>
            <span>Pacing Interval (seconds)</span>
            <SecondsFromMillisecondsInput valueMs={config.pacing?.interval_ms} disabled={disabled}
              onChangeMs={intervalMs => updateConfig(next => ({ ...next, pacing: { ...(next.pacing || {}), type: 'fixed_interval', interval_ms: intervalMs ?? 0 } }))} />
          </label>
        )}
        {pacingType === 'random_delay' && (
          <>
            <label><span>Minimum Delay (seconds)</span><SecondsFromMillisecondsInput valueMs={config.pacing?.min_delay_ms} disabled={disabled}
              onChangeMs={minDelayMs => updateConfig(next => ({ ...next, pacing: { ...(next.pacing || {}), type: 'random_delay', min_delay_ms: minDelayMs ?? 0 } }))} /></label>
            <label><span>Maximum Delay (seconds)</span><SecondsFromMillisecondsInput valueMs={config.pacing?.max_delay_ms} disabled={disabled}
              onChangeMs={maxDelayMs => updateConfig(next => ({ ...next, pacing: { ...(next.pacing || {}), type: 'random_delay', max_delay_ms: maxDelayMs ?? 0 } }))} /></label>
          </>
        )}
        {pacingType === 'random_interval' && (
          <>
            <label><span>Minimum Interval (seconds)</span><SecondsFromMillisecondsInput valueMs={config.pacing?.min_interval_ms} disabled={disabled}
              onChangeMs={minIntervalMs => updateConfig(next => ({ ...next, pacing: { ...(next.pacing || {}), type: 'random_interval', min_interval_ms: minIntervalMs ?? 0 } }))} /></label>
            <label><span>Maximum Interval (seconds)</span><SecondsFromMillisecondsInput valueMs={config.pacing?.max_interval_ms} disabled={disabled}
              onChangeMs={maxIntervalMs => updateConfig(next => ({ ...next, pacing: { ...(next.pacing || {}), type: 'random_interval', max_interval_ms: maxIntervalMs ?? 0 } }))} /></label>
          </>
        )}
        {pacingType === 'tps' && (
          <label><span>Target TPS</span><input type="number" min={0} step="0.1" value={config.pacing?.tps ?? 0} disabled={disabled}
            onChange={event => updateConfig(next => ({ ...next, pacing: { ...(next.pacing || {}), type: 'tps', tps: numberValue(event.target.value) } }))} /></label>
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
              <select value={jms.provider || 'solace'} disabled={disabled} onChange={event => updateJMSProvider(event.target.value)}>
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
