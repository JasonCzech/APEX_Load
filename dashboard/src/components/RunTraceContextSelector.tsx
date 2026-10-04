import { useEffect, useId, useState } from 'react'
import { parseTraceContextOrigins, traceContextErrors, type RunTraceContextSettings } from './runTraceContext'

interface Props {
  value: RunTraceContextSettings | undefined
  onChange: (value: RunTraceContextSettings | undefined) => void
  // suggest is true when a monitoring capture that can use trace IDs is on.
  suggest: boolean
}

export default function RunTraceContextSelector({ value, onChange, suggest }: Props) {
  const id = useId()
  const enabled = Boolean(value?.enabled)
  const marker = Boolean(value?.transaction_marker)
  const active = enabled || marker
  const errors = traceContextErrors(value)
  // Keep the operator's raw text while typing; resync only when the value
  // changes from outside, such as a restored draft.
  const originsKey = (value?.origins ?? []).join('\n')
  const [originsText, setOriginsText] = useState(originsKey)
  useEffect(() => {
    setOriginsText(current => (parseTraceContextOrigins(current).join('\n') === originsKey ? current : originsKey))
  }, [originsKey])
  const update = (next: Partial<RunTraceContextSettings>) => {
    const merged: RunTraceContextSettings = { enabled, transaction_marker: marker, origins: value?.origins ?? [], ...next }
    onChange(!merged.enabled && !merged.transaction_marker && (merged.origins ?? []).length === 0 ? undefined : merged)
  }
  return (
    <div className="setting-row run-trace-context">
      <div className="setting-info">
        <span className="setting-label" id={`${id}-label`}>Trace context</span>
        <span className="setting-desc">
          Send correlation headers only to the origins you approve, so APM and log tools can follow each request. Headers the script authored are never changed.
        </span>
      </div>
      <div className="run-trace-context-controls" role="group" aria-labelledby={`${id}-label`}>
        <label className="checkbox-label">
          <input type="checkbox" checked={enabled} onChange={event => update({ enabled: event.target.checked })} />
          <span>Propagate W3C trace context (traceparent)</span>
        </label>
        <label className="checkbox-label">
          <input type="checkbox" checked={marker} onChange={event => update({ transaction_marker: event.target.checked })} />
          <span>Add Dynatrace transaction markers (x-dynatrace-test)</span>
        </label>
        {suggest && !active && (
          <p className="setting-desc run-trace-context-hint" role="note">
            Monitoring capture is on for this run. Turning on trace context lets captured logs and traces be matched to individual requests.
          </p>
        )}
        {active && (
          <label className="run-trace-context-origins">
            <span>Approved origins (one per line, for example https://shop.example.com)</span>
            <textarea
              className="settings-input"
              rows={3}
              value={originsText}
              onChange={event => {
                setOriginsText(event.target.value)
                update({ origins: parseTraceContextOrigins(event.target.value) })
              }}
              aria-invalid={errors.length > 0}
              aria-describedby={errors.length > 0 ? `${id}-errors` : undefined}
            />
          </label>
        )}
        {active && errors.length > 0 && (
          <ul className="run-trace-context-errors" id={`${id}-errors`} role="alert">
            {errors.map(error => <li key={error}>{error}</li>)}
          </ul>
        )}
      </div>
    </div>
  )
}
