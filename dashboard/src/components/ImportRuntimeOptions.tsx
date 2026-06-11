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

  const numberValue = (raw: string) => Math.max(0, Number(raw) || 0)

  return (
    <details className="import-runtime-options">
      <summary>
        <span>Runtime Settings</span>
        <small>think time · pacing · timeouts · auth</small>
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
        Changes apply to the generated script before save. Auth selections insert environment-variable
        placeholders; supply the values at runtime.
      </p>
    </details>
  )
}
