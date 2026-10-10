import { useId } from 'react'
import type { HTTPQUICConfig } from '../types/scripts'
import { ALT_SVC_CACHE_OPTIONS, QUIC_LIMITS, quicFieldIssues, quicUnsupportedKeys, type QUICField } from './httpQUICModel'
import './HTTPQUICSettingsFields.css'

type Props = {
  /** This layer's config.http.quic. */
  value?: HTTPQUICConfig
  /** The value a lower layer supplies, shown as placeholders (group editors). */
  inherited?: HTTPQUICConfig
  onChange: (patch: Partial<HTTPQUICConfig>) => void
  /** Why the settings cannot apply; the fields are disabled while it is set. */
  disabledReason?: string
  disabled?: boolean
}

const NUMBER_FIELDS: ReadonlyArray<{ field: Exclude<QUICField, 'alt_svc_cache'>; label: string; hint: string; fallback: number; max?: number }> = [
  { field: 'handshake_timeout_ms', label: 'Handshake timeout (ms)', hint: 'QUIC handshake limit; the request timeout still covers the whole request.', fallback: QUIC_LIMITS.handshakeTimeoutMs.fallback, max: QUIC_LIMITS.handshakeTimeoutMs.max },
  { field: 'max_idle_timeout_ms', label: 'Max idle timeout (ms)', hint: 'Closes a QUIC connection with no traffic for this long.', fallback: QUIC_LIMITS.maxIdleTimeoutMs.fallback, max: QUIC_LIMITS.maxIdleTimeoutMs.max },
  { field: 'keep_alive_ms', label: 'Keep-alive interval (ms)', hint: 'PING interval that keeps NAT bindings open; 0 turns it off.', fallback: QUIC_LIMITS.keepAliveMs.fallback },
  { field: 'broken_ttl_ms', label: 'Broken origin TTL (ms)', hint: 'How long auto+h3 keeps an origin on TCP after its QUIC connection failed; 0 forgets at once.', fallback: QUIC_LIMITS.brokenTTLMs.fallback, max: QUIC_LIMITS.brokenTTLMs.max },
]

/**
 * The five config.http.quic fields. They apply to requests that use HTTP/3
 * (auto+h3 or 3); receive windows, header and stream limits and packet
 * sizing stay operator controls.
 */
export function HTTPQUICSettingsFields({ value, inherited, onChange, disabledReason, disabled = false }: Props) {
  const id = useId()
  const effective = value ?? inherited
  const issues = quicFieldIssues(effective)
  const unsupported = quicUnsupportedKeys(effective)
  const locked = disabled || Boolean(disabledReason)
  const placeholder = (field: QUICField, fallback: number | string) => {
    const layered = inherited?.[field]
    return layered !== undefined && layered !== '' ? `Inherited: ${typeof layered === 'number' ? layered.toLocaleString() : layered}` : `Default: ${typeof fallback === 'number' ? fallback.toLocaleString() : fallback}`
  }
  return <fieldset className="http-quic-settings" aria-describedby={`${id}-scope`} disabled={locked}>
    <legend>QUIC (HTTP/3)</legend>
    <p id={`${id}-scope`} className="form-hint">Applies to requests that use HTTP/3 (auto+h3 or 3). Empty fields take the default.</p>
    {disabledReason && <p className="http-quic-disabled" role="note">{disabledReason}</p>}
    <div className="http-quic-grid">
      {NUMBER_FIELDS.map(({ field, label, hint, fallback, max }) => {
        const issue = issues[field]
        const current = value?.[field]
        return <label key={field} className="http-quic-field">
          <span>{label}</span>
          <input className="settings-input" type="number" inputMode="numeric" min={0} max={max} step={1}
            value={current ?? ''} placeholder={placeholder(field, fallback)}
            aria-invalid={issue ? true : undefined} aria-describedby={`${id}-${field}-hint${issue ? ` ${id}-${field}-error` : ''}`}
            onChange={event => {
              const raw = event.target.value
              onChange({ [field]: raw === '' ? undefined : Number(raw) })
            }} />
          <small id={`${id}-${field}-hint`}>{hint}</small>
          {issue && <small id={`${id}-${field}-error`} className="http-quic-error" role="alert">{issue}</small>}
        </label>
      })}
      <label className="http-quic-field">
        <span>Alt-Svc cache</span>
        <select className="settings-input" value={value?.alt_svc_cache ?? ''} aria-invalid={issues.alt_svc_cache ? true : undefined}
          aria-describedby={`${id}-alt-svc-hint${issues.alt_svc_cache ? ` ${id}-alt-svc-error` : ''}`}
          onChange={event => onChange({ alt_svc_cache: (event.target.value || undefined) as HTTPQUICConfig['alt_svc_cache'] })}>
          <option value="">{inherited?.alt_svc_cache ? `Inherited (${ALT_SVC_CACHE_OPTIONS.find(option => option.value === inherited.alt_svc_cache)?.label ?? inherited.alt_svc_cache})` : 'Default (per virtual user)'}</option>
          {ALT_SVC_CACHE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
          {value?.alt_svc_cache && !ALT_SVC_CACHE_OPTIONS.some(option => option.value === value.alt_svc_cache) && <option value={value.alt_svc_cache} disabled>Unsupported value ({value.alt_svc_cache})</option>}
        </select>
        <small id={`${id}-alt-svc-hint`}>Scope of remembered HTTP/3 advertisements (auto+h3); per iteration forgets them at each iteration.</small>
        {issues.alt_svc_cache && <small id={`${id}-alt-svc-error`} className="http-quic-error" role="alert">{issues.alt_svc_cache}</small>}
      </label>
    </div>
    {unsupported.length > 0 && <p className="http-quic-error" role="alert">
      Not script settings: {unsupported.join(', ')}. QUIC receive windows, header limits, stream budgets and packet sizing are operator controls (APEXLOAD_QUIC_*); validation reports these keys until they are removed in the source JSON.
    </p>}
  </fieldset>
}
