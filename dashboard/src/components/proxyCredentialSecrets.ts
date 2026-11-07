import type { ScriptConfig, ScriptDefinition } from '../types/scripts'

/**
 * When a proxy username or password secret is needed, shared by every surface
 * that counts required secrets (pkg/engine and the import review apply the
 * same rule). A proxy credential is sent only while its proxy is in use:
 * - HTTP: config.http.proxy_mode is custom, or no mode is set and a proxy URL
 *   is (pkg/protocol/http applyProxy; none, system and PAC never send them).
 * - Browser: config.browser.proxy.server is set (Playwright uses the
 *   credentials only with a server).
 * A secret referenced only by an inactive proxy credential is still a
 * reference (listed, bindable, kept in the script for a later switch to
 * Custom), but it is not required and never blocks.
 */

export type ProxyCredentialKind = 'http' | 'browser'
export type ProxyCredentialField = 'username' | 'password'

/** A `{{secret.NAME}}` reference in a proxy username or password field. */
export interface ProxyCredentialSecretRef {
  /** The canonical project secret name (upper case, `.`/`-` as `_`). */
  name: string
  kind: ProxyCredentialKind
  field: ProxyCredentialField
  /** Whether the runtime sends this credential with the current settings. */
  active: boolean
  /** The JSON pointer of the field (`/config/http/proxy/password`). */
  path: string
}

const SECRET_TEMPLATE = /\{\{\s*secret\.\s*([A-Za-z0-9_.-]{1,249})\s*\}\}/g
const FIELDS: readonly ProxyCredentialField[] = ['username', 'password']

/** A project secret name as the controller compares it (upper case, `.`/`-` as `_`). */
export function canonicalSecretName(name: string): string {
  return name.trim().toUpperCase().replace(/[.-]/g, '_')
}

function trimmed(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Whether the HTTP runtime sends config.http.proxy username/password. */
export function httpProxyCredentialsActive(config: ScriptConfig | null | undefined): boolean {
  const http = config?.http
  const mode = trimmed(http?.proxy_mode)
  return mode === 'custom' || (mode === '' && trimmed(http?.proxy?.url) !== '')
}

/** Whether the browser runtime uses config.browser.proxy username/password. */
export function browserProxyCredentialsActive(config: ScriptConfig | null | undefined): boolean {
  return trimmed(config?.browser?.proxy?.server) !== ''
}

/** The Proxy mode as the Runtime Settings select names it, for notes. */
export function httpProxyModeLabel(mode: string | null | undefined): string {
  const value = trimmed(mode)
  if (value === '' || value === 'none') return 'No proxy'
  if (value === 'system') return 'System'
  if (value === 'pac') return 'PAC'
  if (value === 'custom') return 'Custom'
  return value
}

/** Every secret reference in a proxy username or password, with whether it is in use. */
export function proxyCredentialSecretRefs(config: ScriptConfig | null | undefined): ProxyCredentialSecretRef[] {
  const refs: ProxyCredentialSecretRef[] = []
  const sources: Array<[ProxyCredentialKind, Record<string, unknown> | undefined, boolean]> = [
    ['http', config?.http?.proxy as Record<string, unknown> | undefined, httpProxyCredentialsActive(config)],
    ['browser', config?.browser?.proxy as Record<string, unknown> | undefined, browserProxyCredentialsActive(config)],
  ]
  for (const [kind, proxy, active] of sources) {
    if (!proxy || typeof proxy !== 'object') continue
    for (const field of FIELDS) {
      const value = proxy[field]
      if (typeof value !== 'string') continue
      for (const match of value.matchAll(SECRET_TEMPLATE)) {
        refs.push({ name: canonicalSecretName(match[1]), kind, field, active, path: `/config/${kind}/proxy/${field}` })
      }
    }
  }
  return refs
}

function withoutCredentials<T extends object>(proxy: T | undefined): T | undefined {
  if (!proxy || typeof proxy !== 'object') return proxy
  const next = { ...proxy } as Record<string, unknown>
  delete next.username
  delete next.password
  return next as T
}

/**
 * The script with its proxy credential fields left out: only those not in use
 * ('inactive'), or every one ('all'). The script itself is returned when
 * nothing is left out, so a caller's identity caches keep working.
 */
export function withoutProxyCredentials<T extends ScriptDefinition>(script: T, scope: 'inactive' | 'all' = 'inactive'): T {
  const config = script?.config
  if (!config) return script
  const stripHTTP = Boolean(config.http?.proxy) && (scope === 'all' || !httpProxyCredentialsActive(config))
    && (config.http?.proxy?.username !== undefined || config.http?.proxy?.password !== undefined)
  const browserProxy = config.browser?.proxy as { username?: unknown; password?: unknown } | undefined
  const stripBrowser = Boolean(browserProxy) && (scope === 'all' || !browserProxyCredentialsActive(config))
    && (browserProxy?.username !== undefined || browserProxy?.password !== undefined)
  if (!stripHTTP && !stripBrowser) return script
  return {
    ...script,
    config: {
      ...config,
      ...(stripHTTP ? { http: { ...config.http, proxy: withoutCredentials(config.http?.proxy) } } : {}),
      ...(stripBrowser ? { browser: { ...config.browser, proxy: withoutCredentials(config.browser?.proxy) } } : {}),
    },
  }
}

// `password uses`, or `username and password use` when both reference it.
function fieldsText(refs: readonly ProxyCredentialSecretRef[]): string {
  const fields = FIELDS.filter(field => refs.some(ref => ref.field === field))
  if (fields.length === 0) return 'password uses'
  return `${fields.join(' and ')} ${fields.length > 1 ? 'use' : 'uses'}`
}

/**
 * The blocker for a secret an in-use proxy credential references and no
 * project secret supplies: what is unbound, and the two ways out. name is the
 * secret as the caller has it (a preflight lists it as the script writes it);
 * it is matched canonically and quoted as given.
 */
export function proxyCredentialBlockerText(name: string, refs: readonly ProxyCredentialSecretRef[]): { title: string; description: string } {
  const canonical = canonicalSecretName(name)
  const own = refs.filter(ref => ref.name === canonical && ref.active)
  const httpRefs = own.filter(ref => ref.kind === 'http')
  const browserRefs = own.filter(ref => ref.kind === 'browser')
  const httpText = `The proxy ${fieldsText(httpRefs)} the secret ${name}, which is not bound to a project secret. Bind a project secret to it, or change Proxy mode to No proxy, System or PAC.`
  const browserText = `The browser proxy ${fieldsText(browserRefs)} the secret ${name}, which is not bound to a project secret. Bind a project secret to it, or clear the browser proxy server.`
  if (browserRefs.length > 0 && httpRefs.length > 0) {
    // Both in-use proxies send it: changing one proxy alone does not clear
    // the blocker, so name both (HTTP first, as the server does).
    return { title: 'Custom proxy credential not bound', description: `${httpText} ${browserText}` }
  }
  return browserRefs.length > 0
    ? { title: 'Browser proxy credential not bound', description: browserText }
    : { title: 'Custom proxy credential not bound', description: httpText }
}

/**
 * The non-blocking notes for proxy credential secrets that are not in use:
 * the reference stays in the script for a later switch, and nothing needs it
 * now. One note per secret and proxy. onlyNames holds the canonical names that
 * nothing else in the script references (scriptSecretReferenceModel
 * inactiveProxyOnlySecretNames): a name also used elsewhere is still needed,
 * so it gets no note, as the server's ReplayInactiveProxySecretNotes does.
 * Callers with a script use scriptInactiveProxyCredentialNotes.
 */
export function inactiveProxyCredentialNotes(config: ScriptConfig | null | undefined, onlyNames: ReadonlySet<string>, kind?: ProxyCredentialKind): string[] {
  const inactive = proxyCredentialSecretRefs(config).filter(ref => !ref.active && onlyNames.has(ref.name) && (!kind || ref.kind === kind))
  const notes: string[] = []
  const seen = new Set<string>()
  for (const ref of inactive) {
    const key = `${ref.kind}:${ref.name}`
    if (seen.has(key)) continue
    seen.add(key)
    const fields = fieldsText(inactive.filter(other => other.kind === ref.kind && other.name === ref.name))
    notes.push(ref.kind === 'http'
      ? `The proxy ${fields} the secret ${ref.name}, but Proxy mode is ${httpProxyModeLabel(config?.http?.proxy_mode)}, so it is not needed. It becomes required if you switch Proxy mode to Custom.`
      : `The browser proxy ${fields} the secret ${ref.name}, but no browser proxy server is set, so it is not needed. It becomes required if you set a browser proxy server.`)
  }
  return notes
}

/**
 * The import review code for an encrypted proxy password. It is a call-out
 * with guided repairs, never a blocker by itself: only an unbound secret on an
 * in-use proxy credential blocks. Imports write it to the step's
 * ADVISORY_CALLOUTS_METADATA_KEY list; steps stored before this rule may
 * still carry it as an execution blocker, which is ignored.
 */
export const ENCRYPTED_PROXY_PASSWORD_CODE = 'encrypted_proxy_password'

/** The step metadata list of advisory call-out codes (dsl.AdvisoryCalloutsMetadataKey). */
export const ADVISORY_CALLOUTS_METADATA_KEY = 'advisory_callouts'

/**
 * Whether an execution blocker code a step carries refuses replay: an
 * advisory call-out never does (dsl.AdvisoryExecutionBlockerCode, which
 * ignores case and surrounding space).
 */
export function executionBlockerCodeBlocks(code: string): boolean {
  return code.trim().toLowerCase() !== ENCRYPTED_PROXY_PASSWORD_CODE
}
