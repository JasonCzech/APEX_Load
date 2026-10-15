import { useEffect, useId, useMemo, useRef, useState } from 'react'

import type {
  HTTPConfig,
  ScriptActionDefinition,
  ScriptHeaderEntry,
} from '../types/scripts'
import {
  formatHTTPSettingsSummary,
  parseBulkCookies,
  parseBulkHeaders,
} from './httpRequestDefaultsModel'
import { httpSequenceRequests } from './scriptActionHelpers'
import { isRecordedStateHeader } from './credentialPreviewPolicy'

export type HTTPRuntimeActionType = 'cookie_clear' | 'cache_clear' | 'http_connections_close'

interface HttpRequestDefaultsEditorProps {
  actions?: ScriptActionDefinition[]
  newUserEachIteration?: boolean
  onAddRuntimeAction?: (type: HTTPRuntimeActionType) => void
  onChange: (patch: Partial<HTTPConfig>) => void
  onNewUserEachIterationChange?: (enabled: boolean) => void
  originLabel?: string
  section: 'request' | 'session'
  value: HTTPConfig
}

interface HeaderFinding {
  level: 'error' | 'warning'
  message: string
}

interface PinnedCookie {
  enabled: boolean
  masked?: boolean
  name: string
  value: string
}

interface PreviewHeader extends ScriptHeaderEntry {
  origin: string
}

interface SuppressedPreviewHeader extends PreviewHeader {
  suppressedBy: string
}

interface HeaderPreview {
  effective: PreviewHeader[]
  suppressed: SuppressedPreviewHeader[]
}

interface SocketCompatibilityPanelProps {
  additionalAttributes?: Record<string, string>
  originLabel?: string
}

const COMMON_HEADERS = [
  'Accept',
  'Accept-Charset',
  'Cache-Control',
  'Content-Type',
  'Cookie',
  'If-Match',
  'If-None-Match',
  'Origin',
  'Pragma',
  'Referer',
  'X-Correlation-ID',
  'X-Forwarded-For',
  'X-Request-ID',
  'X-Tenant-ID',
]

const TEMPLATE_OPTIONS = [
  { label: 'Parameter', value: '{{param.name}}' },
  { label: 'Data column', value: '{{data.source.column}}' },
  { label: 'Runtime variable', value: '{{var.name}}' },
  { label: 'Environment', value: '{{env.KEY}}' },
  { label: 'Secret', value: '{{secret.name}}' },
]

const TRANSPORT_OWNED_HEADERS = new Set([
  'content-length',
  'transfer-encoding',
  'trailer',
  'proxy-authorization',
])
const HOP_BY_HOP_HEADERS = new Set(['connection', 'keep-alive', 'proxy-connection', 'te', 'upgrade'])
const TYPED_HEADERS = new Set(['user-agent', 'accept-encoding', 'accept-language', 'authorization', 'traceparent'])
const SENSITIVE_HEADER_PATTERN = /(?:authorization|cookie|api[-_]?key|token|secret|password|credential)/i
const VALID_HEADER_NAME = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/

function enabled(entry: ScriptHeaderEntry): boolean {
  return entry.enabled !== false
}

function canonical(name: string): string {
  return name.trim().toLowerCase()
}

function defaultHeaderEntries(value: HTTPConfig): ScriptHeaderEntry[] {
  const ordered = (value.default_header_list || []).map(header => ({ ...header }))
  const orderedNames = new Set(ordered.map(header => canonical(header.name)).filter(Boolean))
  const compatibility = Object.entries(value.default_headers || {})
    .filter(([name]) => !orderedNames.has(canonical(name)))
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, headerValue]) => ({ name, value: headerValue }))
  return [...ordered, ...compatibility]
}

function cookiePairs(entries: ScriptHeaderEntry[]): PinnedCookie[] {
  return entries
    .filter(entry => canonical(entry.name) === 'cookie')
    .flatMap(entry => {
      if (entry.value === '[REDACTED]') {
        return [{
          enabled: enabled(entry),
          masked: true,
          name: 'Stored pinned cookie',
          value: '[REDACTED]',
        }]
      }
      return entry.value.split(';').map(part => part.trim()).filter(Boolean).map(part => {
        const separator = part.indexOf('=')
        const name = separator >= 0 ? part.slice(0, separator).trim() : part
        const value = separator >= 0 ? part.slice(separator + 1).trim() : ''
        return {
          enabled: enabled(entry),
          masked: value === '[REDACTED]',
          name,
          value,
        }
      })
    })
}

function headerFindings(entry: ScriptHeaderEntry, all: ScriptHeaderEntry[], httpVersion: string): HeaderFinding[] {
  if (!enabled(entry)) return []
  const name = entry.name.trim()
  const normalized = canonical(name)
  const findings: HeaderFinding[] = []
  if (!name || !VALID_HEADER_NAME.test(name)) {
    findings.push({ level: 'error', message: 'Enter a valid HTTP header name.' })
    return findings
  }
  if (/[\r\n]/.test(entry.value)) {
    findings.push({ level: 'error', message: `${name} contains characters that are not valid on the HTTP wire.` })
  }
  if (TRANSPORT_OWNED_HEADERS.has(normalized)) {
    findings.push({ level: 'error', message: `${name} is owned by the HTTP transport and cannot be set manually.` })
  }
  if (HOP_BY_HOP_HEADERS.has(normalized) && httpVersion !== '1.1') {
    findings.push({ level: 'error', message: `${name} requires exact HTTP/1.1 mode.` })
  }
  if ((normalized === 'host' || normalized === 'user-agent') &&
    all.filter(candidate => enabled(candidate) && canonical(candidate.name) === normalized).length > 1) {
    findings.push({ level: 'error', message: `${name} can appear only once.` })
  }
  if (TYPED_HEADERS.has(normalized)) {
    findings.push({ level: 'warning', message: `${name} duplicates a typed HTTP setting; this row is authoritative.` })
  }
  if (sensitiveHeader(name) &&
    entry.value !== '[REDACTED]' &&
    !entry.value.includes('{{secret.')) {
    findings.push({ level: 'warning', message: `${name} may contain a credential. Prefer a {{secret.*}} reference.` })
  }
  return findings
}

function cookieNameError(cookie: PinnedCookie, all: PinnedCookie[]): string {
  if (!cookie.enabled) return ''
  if (cookie.masked) return ''
  const name = cookie.name.trim()
  if (!name || !VALID_HEADER_NAME.test(name)) return 'Enter a valid cookie name.'
  if (all.filter(candidate => candidate.enabled && candidate.name.trim() === name).length > 1) {
    return 'Cookie names must be unique in the pinned-cookie editor.'
  }
  return ''
}

function socketOptionWasMapped(key: string, value: string): boolean {
  const option = key.trim().toLowerCase()
  if (/web_set_sockets_option\.(?:ssl_version|tls_version)$/.test(option)) {
    return /^(?:tls(?:\s*1\.[0-3]|1[0-3])|tls)$/i.test(value.trim())
  }
  if (/web_set_sockets_option\.(?:close_keepalive_connections|close_keep_alive_connections)$/.test(option)) {
    return /^(?:1|true|yes|on)$/i.test(value.trim())
  }
  if (/web_set_sockets_option\.(?:max_connections_per_host|max_connections)$/.test(option)) {
    return Number.isInteger(Number(value)) && Number(value) > 0
  }
  return false
}

export function SocketCompatibilityPanel({
  additionalAttributes,
  originLabel,
}: SocketCompatibilityPanelProps) {
  const options = Object.entries(additionalAttributes || {})
    .filter(([key]) => key.toLowerCase().startsWith('web_set_sockets_option'))
    .sort(([left], [right]) => left.localeCompare(right))
  if (options.length === 0) return null

  return (
    <section className="http-socket-compatibility" aria-labelledby="socket-compatibility-heading">
      <div className="http-settings-card-heading">
        <div>
          <h5 id="socket-compatibility-heading">Imported Socket Compatibility</h5>
          <p>Source options are preserved for traceability. Supported values are mapped to typed settings; review-only values are never passed through as operating-system socket strings.</p>
        </div>
        {originLabel && <span className="http-setting-origin">{originLabel}</span>}
      </div>
      <div className="http-socket-compatibility-list" role="list">
        {options.map(([key, value]) => {
          const mapped = socketOptionWasMapped(key, value)
          return (
            <div role="listitem" key={key}>
              <div><code>{key.replace(/^web_set_sockets_option\./i, '') || 'Unspecified option'}</code><span>{value || '(empty)'}</span></div>
              <strong className={mapped ? 'mapped' : 'review'}>{mapped ? 'Mapped to typed setting' : 'Review required'}</strong>
            </div>
          )
        })}
      </div>
    </section>
  )
}

function flattenActions(actions: ScriptActionDefinition[]): ScriptActionDefinition[] {
  return actions.flatMap(action => [
    action,
    ...flattenActions(httpSequenceRequests(action)),
    ...flattenActions(action.control?.steps || []),
    ...flattenActions(action.control?.else || []),
  ])
}

// Cookies and session, anti-forgery and tracing headers are state a
// recording or import carries, not credentials.
function sensitiveHeader(name: string): boolean {
  return SENSITIVE_HEADER_PATTERN.test(name) && !isRecordedStateHeader(name)
}

function maskedValue(name: string, value: string): string {
  if (sensitiveHeader(name) && value && !value.includes('{{')) return '••••••••'
  return value
}

function previewHeaders(
  http: HTTPConfig,
  action?: ScriptActionDefinition,
  defaultOrigin = 'Script default',
): HeaderPreview {
  const typed: PreviewHeader[] = [
    ...(http.user_agent ? [{ name: 'User-Agent', value: http.user_agent, origin: 'Typed setting' }] : []),
    ...(http.accept_encoding ? [{ name: 'Accept-Encoding', value: http.accept_encoding, origin: 'Typed setting' }] : []),
    ...(http.accept_language ? [{ name: 'Accept-Language', value: http.accept_language, origin: 'Typed setting' }] : []),
    ...(http.auth?.type && http.auth.type !== 'none'
      ? [{ name: 'Authorization', value: '(generated from typed authentication)', origin: 'Typed authentication' }]
      : []),
    ...(http.traceparent === false
      ? []
      : [{ name: 'Traceparent', value: '(generated at runtime)', origin: 'Generated' }]),
  ]
  const defaultCandidates: PreviewHeader[] = defaultHeaderEntries(http)
    .map(header => ({ ...header, origin: defaultOrigin }))
  const defaultNames = new Set(defaultCandidates.map(header => canonical(header.name)))
  const validDefault = (header: PreviewHeader) =>
    enabled(header) &&
    !headerFindings(header, defaultCandidates, http.version || '')
      .some(finding => finding.level === 'error')
  const enabledDefaults = defaultCandidates.filter(validDefault)
  const defaultSuppressor = (name: string) => {
    const matching = defaultCandidates.filter(candidate => canonical(candidate.name) === canonical(name))
    if (matching.some(validDefault)) return defaultOrigin
    if (matching.some(enabled)) return 'Invalid default row'
    return 'Disabled default row'
  }
  const orderedNames = new Set((http.default_header_list || []).map(header => canonical(header.name)))
  const compatibilitySuppressed: SuppressedPreviewHeader[] = Object.entries(http.default_headers || {})
    .filter(([name]) => orderedNames.has(canonical(name)))
    .map(([name, value]) => ({
      name,
      value,
      origin: 'Compatibility default',
      suppressedBy: defaultSuppressor(name),
    }))
  const suppressedDefaults: SuppressedPreviewHeader[] = [
    ...typed
      .filter(header => defaultNames.has(canonical(header.name)))
      .map(header => ({
        ...header,
        suppressedBy: defaultSuppressor(header.name),
      })),
    ...compatibilitySuppressed,
  ]
  const inheritedDefaults: PreviewHeader[] = [
    ...typed.filter(header => !defaultNames.has(canonical(header.name))),
    ...enabledDefaults,
  ]
  if (!action) return { effective: inheritedDefaults, suppressed: suppressedDefaults }

  const actionList = action.header_list || []
  const validAction = (header: ScriptHeaderEntry) =>
    enabled(header) &&
    !headerFindings(header, actionList, http.version || '')
      .some(finding => finding.level === 'error')
  const enabledActionList = actionList.filter(validAction)
  const actionSuppressor = (name: string) => {
    const matching = actionList.filter(header => canonical(header.name) === canonical(name))
    if (matching.some(validAction)) return 'Action override'
    if (matching.some(enabled)) return 'Invalid action row'
    return 'Disabled action row'
  }
  const actionListNames = new Set(actionList.map(header => canonical(header.name)))
  const actionNames = new Set([
    ...Object.keys(action.headers || {}).map(canonical),
    ...actionList.map(header => canonical(header.name)),
  ])
  const inherited = inheritedDefaults.filter(header => !actionNames.has(canonical(header.name)))
  const actionMap: PreviewHeader[] = Object.entries(action.headers || {})
    .filter(([name]) => !actionListNames.has(canonical(name)))
    .map(([name, value]) => ({ name, value, origin: 'Action override' }))
  const actionOrdered: PreviewHeader[] = enabledActionList.map(header => ({
    ...header,
    origin: 'Action override',
  }))
  const suppressed: SuppressedPreviewHeader[] = [
    ...suppressedDefaults,
    ...inheritedDefaults
      .filter(header => actionNames.has(canonical(header.name)))
      .map(header => {
        const name = canonical(header.name)
        return {
          ...header,
          suppressedBy: actionListNames.has(name) ? actionSuppressor(name) : 'Action override',
        }
      }),
    ...Object.entries(action.headers || {})
      .filter(([name]) => actionListNames.has(canonical(name)))
      .map(([name, value]) => ({
        name,
        value,
        origin: 'Action compatibility map',
        suppressedBy: actionSuppressor(name) === 'Action override'
          ? 'Ordered action header'
          : actionSuppressor(name),
      })),
  ]
  return { effective: [...inherited, ...actionMap, ...actionOrdered], suppressed }
}

export default function HttpRequestDefaultsEditor({
  actions = [],
  newUserEachIteration = false,
  onAddRuntimeAction,
  onChange,
  onNewUserEachIterationChange,
  originLabel,
  section,
  value,
}: HttpRequestDefaultsEditorProps) {
  const suggestionId = useId()
  const rootRef = useRef<HTMLDivElement>(null)
  const headerNameRefs = useRef<Array<HTMLInputElement | null>>([])
  const bulkTextareaRef = useRef<HTMLTextAreaElement>(null)
  const allEntries = useMemo(() => defaultHeaderEntries(value), [value])
  const headers = allEntries.filter(entry => canonical(entry.name) !== 'cookie')
  const cookies = cookiePairs(allEntries)
  const requestActions = useMemo(
    () => flattenActions(actions).filter(action => /^(?:GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|request)$/i.test(action.type)),
    [actions],
  )
  const [previewActionIndex, setPreviewActionIndex] = useState(0)
  const [previewOpen, setPreviewOpen] = useState(false)
  const [bulkEditor, setBulkEditor] = useState<'cookies' | 'headers' | null>(null)
  const [bulkText, setBulkText] = useState('')
  const [bulkErrors, setBulkErrors] = useState<string[]>([])
  const firstInvalidHeaderIndex = headers.findIndex(entry =>
    headerFindings(entry, headers, value.version || '').some(finding => finding.level === 'error'))

  useEffect(() => {
    if (section !== 'request' || firstInvalidHeaderIndex < 0) return
    if (rootRef.current?.contains(document.activeElement)) return
    headerNameRefs.current[firstInvalidHeaderIndex]?.focus()
  }, [firstInvalidHeaderIndex, section])

  useEffect(() => {
    if (bulkEditor) bulkTextareaRef.current?.focus()
  }, [bulkEditor])

  useEffect(() => {
    setPreviewActionIndex(current =>
      Math.min(current, Math.max(0, requestActions.length - 1)))
  }, [requestActions.length])

  const commitEntries = (next: ScriptHeaderEntry[]) => {
    onChange({
      default_header_list: next.length ? next : undefined,
      default_headers: undefined,
    })
  }
  const replaceHeaderAt = (headerIndex: number, patch: Partial<ScriptHeaderEntry>) => {
    const source = headers[headerIndex]
    const sourceIndex = allEntries.indexOf(source)
    commitEntries(allEntries.map((entry, index) => index === sourceIndex ? { ...entry, ...patch } : entry))
  }
  const removeHeaderAt = (headerIndex: number) => {
    const source = headers[headerIndex]
    commitEntries(allEntries.filter(entry => entry !== source))
  }
  const moveHeader = (headerIndex: number, direction: -1 | 1) => {
    const targetHeaderIndex = headerIndex + direction
    if (targetHeaderIndex < 0 || targetHeaderIndex >= headers.length) return
    const next = [...allEntries]
    const sourceIndex = next.indexOf(headers[headerIndex])
    const targetIndex = next.indexOf(headers[targetHeaderIndex])
    ;[next[sourceIndex], next[targetIndex]] = [next[targetIndex], next[sourceIndex]]
    commitEntries(next)
  }
  const commitCookies = (nextCookies: PinnedCookie[]) => {
    const nonCookies = allEntries.filter(entry => canonical(entry.name) !== 'cookie')
    const cookieEntries = nextCookies
      .map(cookie => ({
        name: 'Cookie',
        value: cookie.masked && cookie.name !== 'Stored pinned cookie'
          ? `${cookie.name.trim()}=[REDACTED]`
          : cookie.masked
            ? '[REDACTED]'
            : `${cookie.name.trim()}=${cookie.value}`,
        enabled: cookie.enabled,
      }))
    commitEntries([...nonCookies, ...cookieEntries])
  }

  const selectedAction = requestActions[Math.min(previewActionIndex, Math.max(0, requestActions.length - 1))]
  const headerPreview = previewHeaders(value, selectedAction, originLabel || 'Script default')
  const previewValidationErrors = [
    ...headers.flatMap(header => headerFindings(header, headers, value.version || '')),
    ...(selectedAction?.header_list || []).flatMap(header =>
      headerFindings(header, selectedAction?.header_list || [], value.version || '')),
  ].filter(finding => finding.level === 'error').length
  const absoluteHosts = new Set(requestActions.map(action => {
    try {
      return new URL(action.target).host
    } catch {
      return ''
    }
  }).filter(Boolean))
  const sensitiveDefaults = allEntries.filter(entry => enabled(entry) && sensitiveHeader(entry.name))
  const closeBulkEditor = () => {
    setBulkEditor(null)
    setBulkText('')
    setBulkErrors([])
  }
  const importBulkHeaders = () => {
    const result = parseBulkHeaders(bulkText)
    if (result.errors.length > 0) {
      setBulkErrors(result.errors)
      return
    }
    commitEntries([...allEntries, ...result.entries])
    closeBulkEditor()
  }
  const importBulkCookies = () => {
    const result = parseBulkCookies(bulkText)
    if (result.errors.length > 0) {
      setBulkErrors(result.errors)
      return
    }
    commitCookies([
      ...cookies,
      ...result.entries.map(cookie => ({ ...cookie, enabled: true })),
    ])
    closeBulkEditor()
  }

  if (section === 'session') {
    return (
      <div className="http-settings-stack">
        <section className="http-settings-card" aria-labelledby="pinned-cookie-heading">
          <div className="http-settings-card-heading">
            <div>
              <h5 id="pinned-cookie-heading">Pinned Cookies</h5>
              <p>Use fixed routing or dark-launch cookies that must be sent on every authored request. Cookie-jar cleanup does not remove these values.</p>
            </div>
            <div className="http-settings-heading-actions">
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => {
                setBulkEditor('cookies')
                setBulkErrors([])
              }}>Paste cookies</button>
              <button type="button" className="btn btn-sm btn-ghost" onClick={() => commitCookies([...cookies, { enabled: true, name: '', value: '' }])}>+ Add cookie</button>
            </div>
          </div>
          {bulkEditor === 'cookies' && (
            <div className="http-bulk-editor">
              <label>
                <span>Cookie pairs</span>
                <textarea
                  ref={bulkTextareaRef}
                  value={bulkText}
                  onChange={event => {
                    setBulkText(event.target.value)
                    setBulkErrors([])
                  }}
                  placeholder={'dark-route=blue; feature-preview={{secret.preview_cookie}}\nsession-mode=isolated'}
                />
              </label>
              <p>Paste semicolon- or line-separated <code>name=value</code> pairs, or a complete <code>Cookie:</code> header.</p>
              {bulkErrors.length > 0 && <div className="http-bulk-errors" role="alert">{bulkErrors.map(error => <span key={error}>{error}</span>)}</div>}
              <div className="http-bulk-actions">
                <button type="button" className="btn btn-sm btn-secondary" onClick={closeBulkEditor}>Cancel</button>
                <button type="button" className="btn btn-sm btn-primary" disabled={!bulkText.trim()} onClick={importBulkCookies}>Import cookies</button>
              </div>
            </div>
          )}
          {cookies.length === 0 ? (
            <div className="http-settings-empty">No pinned cookies. Learned response cookies can still be managed by the cookie jar.</div>
          ) : (
            <div className="http-cookie-grid" role="table" aria-label="Pinned cookies">
              <div className="http-cookie-grid-head" role="row">
                <span role="columnheader">Enabled</span><span role="columnheader">Name</span><span role="columnheader">Value</span><span role="columnheader">Actions</span>
              </div>
              {cookies.map((cookie, index) => (
                <div className={`http-cookie-grid-row ${cookieNameError(cookie, cookies) ? 'invalid' : ''}`} role="row" key={`cookie-${index}`}>
                  <span role="cell"><input aria-label={`Pinned cookie ${index + 1} enabled`} type="checkbox" checked={cookie.enabled} onChange={event => commitCookies(cookies.map((item, itemIndex) => itemIndex === index ? { ...item, enabled: event.target.checked } : item))} /></span>
                  <span role="cell"><input aria-invalid={Boolean(cookieNameError(cookie, cookies))} aria-label={`Pinned cookie ${index + 1} name`} disabled={cookie.masked} value={cookie.name} onChange={event => commitCookies(cookies.map((item, itemIndex) => itemIndex === index ? { ...item, name: event.target.value } : item))} placeholder="dark-route" /></span>
                  <span role="cell"><input aria-label={`Pinned cookie ${index + 1} value`} disabled={cookie.masked} type="password" value={cookie.value} onChange={event => commitCookies(cookies.map((item, itemIndex) => itemIndex === index ? { ...item, value: event.target.value } : item))} placeholder="{{secret.dark_cookie}}" /></span>
                  <span role="cell"><button type="button" className="btn-icon danger" aria-label={`Remove pinned cookie ${index + 1}`} onClick={() => commitCookies(cookies.filter((_, itemIndex) => itemIndex !== index))}>×</button></span>
                  {(cookie.masked || cookieNameError(cookie, cookies)) && <span className="http-cookie-finding" role="cell">{cookie.masked ? 'Stored value is masked. Remove this row to clear it, then add a replacement.' : cookieNameError(cookie, cookies)}</span>}
                </div>
              ))}
            </div>
          )}
          {cookies.some(cookie => cookie.value && cookie.value !== '[REDACTED]' && !cookie.value.includes('{{secret.')) && (
            <div className="http-setting-warning" role="note">Pinned cookie values are sensitive. Prefer a {'{{secret.*}}'} reference.</div>
          )}
        </section>

        <section className="http-settings-card" aria-labelledby="session-policy-heading">
          <div className="http-settings-card-heading">
            <div><h5 id="session-policy-heading">Cookie Jar & Cache Lifecycle</h5><p>Control state learned from responses independently from pinned cookies.</p></div>
          </div>
          <div className="scenario-option-grid">
            <label className={`scenario-option-card ${value.cookies_enabled !== false ? 'selected' : ''}`}>
              <input type="checkbox" checked={value.cookies_enabled !== false} onChange={event => onChange({ cookies_enabled: event.target.checked })} />
              <div><strong>Enable cookie jar</strong><span>Store and send cookies learned from server responses.</span></div>
            </label>
            <label className={`scenario-option-card ${value.clear_cookies_per_iteration ? 'selected' : ''}`}>
              <input type="checkbox" checked={Boolean(value.clear_cookies_per_iteration)} disabled={newUserEachIteration} onChange={event => onChange({ clear_cookies_per_iteration: event.target.checked })} />
              <div><strong>Clear learned cookies each iteration</strong><span>Start each iteration without response-created cookie state.</span></div>
            </label>
            <label className={`scenario-option-card ${value.cache_enabled ? 'selected' : ''}`}>
              <input type="checkbox" checked={Boolean(value.cache_enabled)} onChange={event => onChange({ cache_enabled: event.target.checked })} />
              <div><strong>Simulate HTTP cache</strong><span>Maintain a bounded per-VUser response cache.</span></div>
            </label>
            <label className={`scenario-option-card ${newUserEachIteration ? 'selected' : ''}`}>
              <input type="checkbox" checked={newUserEachIteration} onChange={event => onNewUserEachIterationChange?.(event.target.checked)} />
              <div><strong>Simulate a new user each iteration</strong><span>Clear both learned cookies and simulated cache before the next iteration.</span></div>
            </label>
          </div>
        </section>

        <section className="http-settings-card" aria-labelledby="runtime-cleanup-heading">
          <div className="http-settings-card-heading">
            <div><h5 id="runtime-cleanup-heading">Precise Runtime Cleanup</h5><p>Add a native action at the end of the current action list, then move it to the exact lifecycle point you need.</p></div>
          </div>
          {onAddRuntimeAction ? (
            <div className="http-runtime-actions">
              {([
                ['cookie_clear', 'Clear learned cookies'],
                ['cache_clear', 'Clear simulated cache'],
                ['http_connections_close', 'Close idle connections'],
              ] as Array<[HTTPRuntimeActionType, string]>).map(([type, label]) => (
                <button key={type} type="button" className="btn btn-sm btn-secondary" onClick={() => onAddRuntimeAction(type)}>{label}</button>
              ))}
            </div>
          ) : (
            <div className="http-settings-empty">Cleanup actions are authored in the script sequence. Open the script’s Session &amp; Cache settings to insert one at an exact step.</div>
          )}
        </section>
      </div>
    )
  }

  return (
    <div className="http-settings-stack" ref={rootRef}>
      <div className="http-settings-summary" role="status">
        <span>{formatHTTPSettingsSummary(value, newUserEachIteration)}</span>
        {originLabel && <span className="http-setting-origin">{originLabel}</span>}
      </div>
      {absoluteHosts.size > 1 && sensitiveDefaults.length > 0 && (
        <div className="http-setting-warning" role="note">
          Sensitive defaults may be sent to {absoluteHosts.size} authored hosts. Use action-level values when credentials are host-specific.
        </div>
      )}

      <section className="http-settings-card" aria-labelledby="default-header-heading">
        <div className="http-settings-card-heading">
          <div><h5 id="default-header-heading">Default Request Headers</h5><p>Applied to every authored HTTP request. Action-level headers override matching names.</p></div>
          <div className="http-settings-heading-actions">
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => {
              setBulkEditor('headers')
              setBulkErrors([])
            }}>Paste headers</button>
            <button type="button" className="btn btn-sm btn-ghost" onClick={() => commitEntries([...allEntries, { name: '', value: '', enabled: true }])}>+ Add header</button>
          </div>
        </div>
        {bulkEditor === 'headers' && (
          <div className="http-bulk-editor">
            <label>
              <span>Header lines</span>
              <textarea
                ref={bulkTextareaRef}
                value={bulkText}
                onChange={event => {
                  setBulkText(event.target.value)
                  setBulkErrors([])
                }}
                placeholder={'X-Tenant-ID: {{param.tenant_id}}\n-H "X-Feature-Flag: dark-launch"\nAccept: application/json'}
              />
            </label>
            <p>Paste one <code>Name: Value</code> header per line. Repeated names and cURL <code>-H</code>/<code>--header</code> arguments retain their authored order.</p>
            {bulkErrors.length > 0 && <div className="http-bulk-errors" role="alert">{bulkErrors.map(error => <span key={error}>{error}</span>)}</div>}
            <div className="http-bulk-actions">
              <button type="button" className="btn btn-sm btn-secondary" onClick={closeBulkEditor}>Cancel</button>
              <button type="button" className="btn btn-sm btn-primary" disabled={!bulkText.trim()} onClick={importBulkHeaders}>Import headers</button>
            </div>
          </div>
        )}
        <datalist id={suggestionId}>{COMMON_HEADERS.map(name => <option value={name} key={name}>{name}</option>)}</datalist>
        {headers.length === 0 ? (
          <div className="http-settings-empty">No custom defaults. Typed browser identity and tracing settings still apply.</div>
        ) : (
          <div className="http-header-grid" role="table" aria-label="Default HTTP headers">
            <div className="http-header-grid-head" role="row">
              <span role="columnheader">Enabled</span><span role="columnheader">Name</span><span role="columnheader">Value</span><span role="columnheader">Insert</span><span role="columnheader">Actions</span>
            </div>
            {headers.map((entry, index) => {
              const findings = headerFindings(entry, headers, value.version || '')
              return (
                <div className={`http-header-grid-row ${findings.some(finding => finding.level === 'error') ? 'invalid' : ''}`} role="row" key={`header-${index}-${entry.name}`}>
                  <span role="cell"><input aria-label={`Header ${index + 1} enabled`} type="checkbox" checked={enabled(entry)} onChange={event => replaceHeaderAt(index, { enabled: event.target.checked })} /></span>
                  <span role="cell"><input ref={node => { headerNameRefs.current[index] = node }} aria-invalid={findings.some(finding => finding.level === 'error')} aria-label={`Header ${index + 1} name`} list={suggestionId} value={entry.name} onChange={event => replaceHeaderAt(index, { name: event.target.value })} placeholder="X-Tenant-ID" /></span>
                  <span role="cell"><input aria-label={`Header ${index + 1} value`} type={sensitiveHeader(entry.name) ? 'password' : 'text'} value={entry.value} onChange={event => replaceHeaderAt(index, { value: event.target.value })} placeholder="{{param.tenant_id}}" /></span>
                  <span role="cell">
                    <select aria-label={`Insert template into header ${index + 1}`} value="" onChange={event => {
                      if (!event.target.value) return
                      replaceHeaderAt(index, { value: `${entry.value}${event.target.value}` })
                    }}>
                      <option value="">Template…</option>
                      {TEMPLATE_OPTIONS.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
                    </select>
                  </span>
                  <span role="cell" className="http-header-row-actions">
                    <button type="button" className="btn-icon" disabled={index === 0} aria-label={`Move header ${index + 1} up`} onClick={() => moveHeader(index, -1)}>↑</button>
                    <button type="button" className="btn-icon" disabled={index === headers.length - 1} aria-label={`Move header ${index + 1} down`} onClick={() => moveHeader(index, 1)}>↓</button>
                    <button type="button" className="btn-icon danger" aria-label={`Remove header ${index + 1}`} onClick={() => removeHeaderAt(index)}>×</button>
                  </span>
                  {findings.length > 0 && (
                    <div className="http-header-findings" role="cell">
                      {findings.map((finding, findingIndex) => <span className={finding.level} key={`${finding.message}-${findingIndex}`}>{finding.message}</span>)}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </section>

      <section className="http-settings-card" aria-labelledby="typed-default-heading">
        <div className="http-settings-card-heading"><div><h5 id="typed-default-heading">Typed Request Behavior</h5><p>Use dedicated controls for browser identity and trace generation; custom rows above remain authoritative.</p></div></div>
        <div className="form-grid">
          <label className="full-width"><span>User-Agent</span><input value={value.user_agent || ''} onChange={event => onChange({ user_agent: event.target.value || undefined })} placeholder="Mozilla/5.0 …" /></label>
          <label><span>Accept-Encoding</span><input value={value.accept_encoding || ''} onChange={event => onChange({ accept_encoding: event.target.value || undefined })} placeholder="gzip, deflate, br" /></label>
          <label><span>Accept-Language</span><input value={value.accept_language || ''} onChange={event => onChange({ accept_language: event.target.value || undefined })} placeholder="en-US,en;q=0.9" /></label>
          <label><span>Trace context</span><select value={value.traceparent === false ? 'false' : 'true'} onChange={event => onChange({ traceparent: event.target.value === 'true' })}><option value="true">Generate / propagate</option><option value="false">Disabled</option></select></label>
          <label><span>Generated trace sampling</span><select value={value.traceparent_sampled === false ? 'false' : 'true'} onChange={event => onChange({ traceparent_sampled: event.target.value === 'true' })}><option value="true">Sampled</option><option value="false">Not sampled</option></select></label>
        </div>
      </section>

      <section className="http-settings-card" aria-labelledby="effective-preview-heading">
        <div className="http-settings-card-heading">
          <div><h5 id="effective-preview-heading">Effective Request Preview</h5><p>Review the winning header layer without exposing stored credentials.</p></div>
          <button type="button" className="btn btn-sm btn-ghost" onClick={() => setPreviewOpen(open => !open)} aria-expanded={previewOpen}>{previewOpen ? 'Hide preview' : 'Show preview'}</button>
        </div>
        {previewOpen && (
          <div className="http-effective-preview">
            <label><span>Request action</span><select value={previewActionIndex} disabled={requestActions.length === 0} onChange={event => setPreviewActionIndex(Number(event.target.value))}>
              {requestActions.length === 0
                ? <option>No request actions available</option>
                : requestActions.map((action, index) => <option value={index} key={`${action.name}-${index}`}>{action.name || `${action.type} ${action.target}`}</option>)}
            </select></label>
            {selectedAction && (
              <div className="http-preview-request-context" aria-label="Selected request context">
                <strong>{selectedAction.type || 'HTTP'}</strong>
                <code>{selectedAction.target || '(target resolved at runtime)'}</code>
                <span>{value.version ? `HTTP/${value.version}` : 'HTTP version: auto'}</span>
              </div>
            )}
            {previewValidationErrors > 0 && (
              <div className="http-setting-warning" role="note">
                {previewValidationErrors} header validation {previewValidationErrors === 1 ? 'error prevents' : 'errors prevent'} this request from executing. Invalid rows are excluded from the effective preview.
              </div>
            )}
            <div className="http-effective-preview-list" role="list" aria-label="Effective request headers">
              {headerPreview.effective.map((header, index) => (
                <div role="listitem" key={`${header.name}-${index}`}>
                  <code>{header.name}</code>
                  <span>{maskedValue(header.name, header.value) || '(empty)'}</span>
                  <strong className="http-preview-origin">{header.origin}</strong>
                </div>
              ))}
              {headerPreview.effective.length === 0 && <div className="http-settings-empty">No effective authored or generated headers.</div>}
            </div>
            {headerPreview.suppressed.length > 0 && (
              <details className="http-preview-suppressed">
                <summary>{headerPreview.suppressed.length} overridden or suppressed source{headerPreview.suppressed.length === 1 ? '' : 's'}</summary>
                <div className="http-effective-preview-list" role="list" aria-label="Overridden or suppressed header sources">
                  {headerPreview.suppressed.map((header, index) => (
                    <div role="listitem" key={`${header.origin}-${header.name}-${index}`}>
                      <code>{header.name}</code>
                      <span>{maskedValue(header.name, header.value) || '(empty)'}</span>
                      <strong className="http-preview-origin">{header.origin} → {header.suppressedBy}</strong>
                    </div>
                  ))}
                </div>
              </details>
            )}
          </div>
        )}
      </section>
    </div>
  )
}
