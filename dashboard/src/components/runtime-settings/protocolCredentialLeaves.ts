import type { RuntimeProtocolOverrideSections, RuntimeProtocolSectionKey } from '../../types/runtimeSettings'
import { isSensitiveCredentialPreviewHeader, isSensitiveCredentialPreviewName } from '../credentialPreviewPolicy'

// Client mirror of the native protocol credential leaves
// (pkg/api/runtime_protocol_sections.go runtimeProtocolCredentialLeaves): which
// strings of the kafka, jms, eventhubs, grpc, database, network and appium
// override sections can hold a credential, and how each kind is classified.
// The execution-plan check and Apply to groups both read this one list, so a
// leaf can never be guarded in one and missed in the other.

/**
 * - secret: always secret material (a catalog Secret leaf).
 * - identity: a username, secret only beside an active credential.
 * - url: secret when it carries userinfo or a credential query value.
 * - secret_name: names a secret (NAME or one {{secret.NAME}}); never a value.
 * - free_text: a property or capability value, secret when its key or text is credential-shaped.
 * - header: a gRPC metadata value, classified like an HTTP header.
 */
export type ProtocolCredentialKind = 'secret' | 'identity' | 'url' | 'secret_name' | 'free_text' | 'header'

export interface ProtocolCredentialLeaf {
  /** Unique leaf path, as the backend names it in errors (kafka.brokers.0, appium.capabilities["a.b"]). */
  path: string
  /** The override_fields path that owns the leaf (kafka.brokers, appium.capabilities.KEY). */
  fieldPath: string
  kind: ProtocolCredentialKind
  value: string
  /** Map key, for free_text and header leaves. */
  key?: string
  /** An identity beside an active credential. */
  active?: boolean
  /** A string inside a capability array, where the engine resolves no references. */
  inList?: boolean
  /** An Appium capability value (device-cloud key names count as credential names). */
  capability?: boolean
}

const REDACTED = '[REDACTED]'
const EXACT_REFERENCE = /^\{\{\s*secret\.([A-Za-z0-9_.-]{1,249})\s*\}\}$/
const ANY_REFERENCE = /\{\{\s*secret\.([A-Za-z0-9_.-]{1,249})\s*\}\}/g
const RESERVED_PREFIX = 'APEX_RUNTIME_OVERRIDE_'
const LOGICAL_SECRET_NAME = /^[A-Za-z0-9_.-]{1,249}$/
/** sensitiveEnvValue's credential shapes. */
export const CREDENTIAL_SHAPED_TEXT = /^(bearer|basic|apikey)\s|sharedaccesskey=|accountkey=|sig=|^(AKIA|ASIA)[A-Z0-9]{12,}/i
/** A credential-named assignment inside free text (JAAS password="x", DSN password=x). */
const CREDENTIAL_ASSIGNMENT = /([A-Za-z0-9_.-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s;&,"']+))/g
/** Legacy env-key DSN markers (legacySensitiveEnvKey). */
const DSN_KEY_MARKERS = ['database_url', 'db_url', 'jdbc_url', 'redis_url', 'cache_url', 'broker_url', 'amqp_url', 'dsn', 'connection_string']
const CAPABILITY_CREDENTIAL_TAILS = new Set(['key', 'accesskey', 'apikey', 'token', 'accesstoken', 'authtoken', 'secret', 'password', 'passwd', 'pwd'])

function decodeSafely(value: string): string {
  try { return decodeURIComponent(value.replace(/\+/g, '%20')) } catch { return value }
}

function normalizedSecretName(name: string): string {
  return name.trim().toUpperCase()
}

/** One exact {{secret.NAME}} reference to a project credential (never a controller-reserved one). */
export function isProjectSecretReference(value: string): boolean {
  const match = EXACT_REFERENCE.exec(value.trim())
  return Boolean(match && !normalizedSecretName(match[1]).startsWith(RESERVED_PREFIX))
}

/** A controller-reserved APEX_RUNTIME_OVERRIDE_* reference anywhere in the text. */
export function hasReservedSecretReference(text: string): boolean {
  for (const match of text.matchAll(ANY_REFERENCE)) {
    if (normalizedSecretName(match[1]).startsWith(RESERVED_PREFIX)) return true
  }
  return false
}

function hasAnySecretReference(text: string): boolean {
  return new RegExp(ANY_REFERENCE.source).test(text)
}

/** Every credential part is a project reference: an Authorization scheme followed by one counts. */
export function isSchemedProjectSecretReference(value: string): boolean {
  if (isProjectSecretReference(value)) return true
  const schemed = /^(bearer|basic|apikey|token|digest|negotiate|ntlm)\s+(.+)$/i.exec(value.trim())
  return Boolean(schemed && isProjectSecretReference(schemed[2]))
}

/** sensitiveEnvKey: a credential name, or a DSN/connection-string style name. */
export function isSensitiveSettingName(name: string): boolean {
  if (isSensitiveCredentialPreviewName(name)) return true
  const normalized = name.trim().toLowerCase().replace(/[-. ]/g, '_')
  return DSN_KEY_MARKERS.some(marker => normalized === marker || normalized.endsWith(`_${marker}`))
}

/** credentialCapabilityKey: an Appium capability whose final segment names a credential (browserstack.key). */
export function isCredentialCapabilityKey(key: string): boolean {
  let name = key.trim().toLowerCase()
  const segment = Math.max(name.lastIndexOf('.'), name.lastIndexOf(':'))
  if (segment >= 0) name = name.slice(segment + 1)
  if (CAPABILITY_CREDENTIAL_TAILS.has(name)) return true
  const part = Math.max(name.lastIndexOf('_'), name.lastIndexOf('-'))
  return part >= 0 && CAPABILITY_CREDENTIAL_TAILS.has(name.slice(part + 1))
}

/**
 * A URL (or scheme-less authority) carrying userinfo, or a credential-named or
 * signature query/fragment value, as plain text. A credential part that is one
 * exact project reference is not typed.
 */
export function urlCarriesTypedCredential(raw: string): boolean {
  const value = raw.trim()
  if (!value) return false
  const authority = /^(?:[a-z][a-z0-9+.-]*:\/\/)?([^/?#\s]*)@[^/?#\s]/i.exec(value)
  if (authority && authority[1]) {
    const parts = authority[1].split(':')
    if (!parts.every(part => part === '' || isProjectSecretReference(decodeSafely(part)))) return true
  }
  const queryStart = value.search(/[?#]/)
  if (queryStart < 0) return false
  for (const pair of value.slice(queryStart + 1).split(/[&#;]/)) {
    const separator = pair.indexOf('=')
    if (separator <= 0) continue
    const name = decodeSafely(pair.slice(0, separator))
    const paramValue = decodeSafely(pair.slice(separator + 1))
    if (!paramValue || isProjectSecretReference(paramValue)) continue
    if (isSensitiveCredentialPreviewName(name) || /^(sig|key|code|access_token|api_key)$/i.test(name)) return true
  }
  return false
}

/** executionPlanCookieOfReferences: every cookie pair's value is a project reference. */
export function isCookieOfProjectReferences(value: string): boolean {
  const pairs = value.split(';').map(part => part.trim()).filter(Boolean)
  return pairs.length > 0 && pairs.every(pair => {
    const separator = pair.indexOf('=')
    return separator > 0 && isProjectSecretReference(pair.slice(separator + 1))
  })
}

/**
 * executionPlanHeaderNeedsReference: a header (or gRPC metadata) value that must
 * be a project reference and is not. A Cookie passes when every pair's value is
 * one; a credential-named or credential-shaped header when it is one (an
 * Authorization scheme followed by one counts).
 */
export function headerNeedsReference(name: string, value: string): boolean {
  if (!value.trim()) return false
  if (/^cookie$/i.test(name.trim())) return !isCookieOfProjectReferences(value)
  if (isSensitiveCredentialPreviewHeader(name) || CREDENTIAL_SHAPED_TEXT.test(value.trim())) return !isSchemedProjectSecretReference(value)
  return false
}

/** sensitiveEnvValue: an Authorization-, key- or URL-credential-shaped value. */
export function isCredentialShapedValue(value: string): boolean {
  const trimmed = value.trim()
  if (!trimmed) return false
  return CREDENTIAL_SHAPED_TEXT.test(trimmed) || urlCarriesTypedCredential(trimmed)
}

/**
 * importSettingTextCarriesCredential: literal userinfo in any token, or a
 * credential-named assignment whose value is not a project reference.
 */
export function textCarriesCredential(text: string): boolean {
  if (/sharedaccesskey=|accountkey=|sig=/i.test(text)) {
    const literal = text.replace(new RegExp(ANY_REFERENCE.source, 'g'), '')
    if (/(sharedaccesskey|accountkey|sig)=[^;&\s"']+/i.test(literal)) return true
  }
  for (const token of text.split(/\s+/)) {
    const trimmed = token.replace(/^[("'<[]+|[)"'>\],;]+$/g, '')
    if (trimmed.includes('@') && urlCarriesTypedCredential(trimmed)) return true
  }
  for (const match of text.matchAll(CREDENTIAL_ASSIGNMENT)) {
    const value = match[2] ?? match[3] ?? match[4] ?? ''
    if (!value || isProjectSecretReference(value)) continue
    if (isSensitiveCredentialPreviewName(match[1])) return true
  }
  return false
}

/** runtimeFreeTextSecretBearing for a property, metadata-like or capability value. */
export function freeTextSecretBearing(key: string, value: string, capability = false): boolean {
  const trimmed = value.trim()
  if (!trimmed) return false
  const sensitiveKey = isSensitiveSettingName(key) || (capability && isCredentialCapabilityKey(key))
  if (EXACT_REFERENCE.test(trimmed)) return sensitiveKey
  if (hasAnySecretReference(value)) return textCarriesCredential(value)
  return sensitiveKey || isCredentialShapedValue(value) || textCarriesCredential(value)
}

/**
 * runtimeSecretNameFieldIssue: a secret-name field holds nothing, a logical
 * secret name or one exact {{secret.NAME}} reference, never a reserved name.
 * The engine reads anything else as the credential text itself.
 */
export function secretNameFieldIssue(value: string): string | undefined {
  const trimmed = value.trim()
  if (!trimmed) return undefined
  const exact = EXACT_REFERENCE.exec(trimmed)
  const name = exact ? exact[1] : trimmed
  if (!exact && (trimmed.includes('{{') || !LOGICAL_SECRET_NAME.test(trimmed))) {
    return 'Name a secret (NAME or {{secret.NAME}}); enter the credential itself in the Credentials store.'
  }
  if (normalizedSecretName(name).startsWith(RESERVED_PREFIX)) {
    return 'Cannot name a reserved internal secret; use a configured project secret.'
  }
  return undefined
}

/** runtimeCredentialLeafSecretBearing: the leaf's value is credential material. */
export function protocolLeafSecretBearing(leaf: ProtocolCredentialLeaf): boolean {
  const value = leaf.value
  if (!value.trim()) return false
  switch (leaf.kind) {
    case 'secret':
      return true
    case 'identity':
      return Boolean(leaf.active) || isCredentialShapedValue(value)
    case 'url':
      if (urlCarriesTypedCredential(value)) return true
      return !hasAnySecretReference(value) && isCredentialShapedValue(value)
    case 'free_text':
      return freeTextSecretBearing(leaf.key ?? '', value, leaf.capability)
    case 'header':
      return isSensitiveCredentialPreviewHeader(leaf.key ?? '') || isCredentialShapedValue(value)
    default:
      return false
  }
}

/** anyMapLeafPath: a key with a path delimiter is quoted, so it never shares a path with a nested spelling. */
function mapLeafPath(path: string, key: string): string {
  return /[.[\]"\\]/.test(key) ? `${path}[${JSON.stringify(key)}]` : `${path}.${key}`
}

function stringLeaf(path: string, kind: ProtocolCredentialKind, value: unknown, extra: Partial<ProtocolCredentialLeaf> = {}): ProtocolCredentialLeaf[] {
  return typeof value === 'string' ? [{ path, fieldPath: path, kind, value, ...extra }] : []
}

function stringMapLeaves(prefix: string, kind: ProtocolCredentialKind, values: unknown): ProtocolCredentialLeaf[] {
  if (!values || typeof values !== 'object' || Array.isArray(values)) return []
  return Object.keys(values as Record<string, unknown>).sort().flatMap(key => {
    const value = (values as Record<string, unknown>)[key]
    return typeof value === 'string' ? [{ path: `${prefix}.${key}`, fieldPath: `${prefix}.${key}`, kind, key, value }] : []
  })
}

/** anyMapLeaves: every string an Appium capability map holds, nested option objects and lists included. */
function capabilityLeaves(prefix: string, values: unknown): ProtocolCredentialLeaf[] {
  const leaves: ProtocolCredentialLeaf[] = []
  const walk = (path: string, fieldPath: string, key: string, node: unknown, inList: boolean) => {
    if (typeof node === 'string') {
      leaves.push({ path, fieldPath, kind: 'free_text', key, value: node, inList, capability: true })
    } else if (Array.isArray(node)) {
      node.forEach((item, index) => walk(`${path}.${index}`, fieldPath, key, item, true))
    } else if (node && typeof node === 'object') {
      for (const child of Object.keys(node as Record<string, unknown>).sort()) {
        walk(mapLeafPath(path, child), fieldPath, child, (node as Record<string, unknown>)[child], inList)
      }
    }
  }
  if (!values || typeof values !== 'object' || Array.isArray(values)) return leaves
  for (const key of Object.keys(values as Record<string, unknown>).sort()) {
    walk(mapLeafPath(prefix, key), `${prefix}.${key}`, key, (values as Record<string, unknown>)[key], false)
  }
  return leaves
}

/**
 * The credential-capable strings of the native protocol sections a row
 * carries, in the backend's order. Only `sections` are walked (all when empty).
 */
export function protocolCredentialLeaves(
  row: RuntimeProtocolOverrideSections | undefined | null,
  sections: readonly RuntimeProtocolSectionKey[] = [],
): ProtocolCredentialLeaf[] {
  if (!row) return []
  const want = (section: RuntimeProtocolSectionKey) => sections.length === 0 || sections.includes(section)
  const leaves: ProtocolCredentialLeaf[] = []
  const kafka = row.kafka
  if (kafka && want('kafka')) {
    const active = Boolean(kafka.sasl_mechanism || kafka.sasl_password || kafka.sasl_password_secret)
    leaves.push(
      ...stringLeaf('kafka.sasl_username', 'identity', kafka.sasl_username ?? '', { active }),
      ...stringLeaf('kafka.sasl_password', 'secret', kafka.sasl_password ?? ''),
      ...stringLeaf('kafka.sasl_password_secret', 'secret_name', kafka.sasl_password_secret ?? ''),
      ...stringLeaf('kafka.schema_registry_url', 'url', kafka.schema_registry_url ?? ''),
    )
    if (kafka.oauth) {
      leaves.push(
        ...stringLeaf('kafka.oauth.client_secret', 'secret', kafka.oauth.client_secret ?? ''),
        ...stringLeaf('kafka.oauth.client_secret_ref', 'secret_name', kafka.oauth.client_secret_ref ?? ''),
        ...stringLeaf('kafka.oauth.token_url', 'url', kafka.oauth.token_url ?? ''),
      )
    }
    ;(kafka.brokers ?? []).forEach((broker, index) => {
      leaves.push(...stringLeaf(`kafka.brokers.${index}`, 'url', broker, { fieldPath: 'kafka.brokers' }))
    })
    leaves.push(...stringMapLeaves('kafka.properties', 'free_text', kafka.properties))
  }
  const jms = row.jms
  if (jms && want('jms')) {
    leaves.push(
      ...stringLeaf('jms.username', 'identity', jms.username ?? '', { active: Boolean(jms.password) }),
      ...stringLeaf('jms.password', 'secret', jms.password ?? ''),
      ...stringLeaf('jms.broker_url', 'url', jms.broker_url ?? ''),
    )
    if (jms.tls) leaves.push(...stringLeaf('jms.tls.ca_cert_secret', 'secret_name', jms.tls.ca_cert_secret ?? ''))
    leaves.push(...stringMapLeaves('jms.properties', 'free_text', jms.properties))
  }
  const eventhubs = row.eventhubs
  if (eventhubs && want('eventhubs')) {
    leaves.push(
      ...stringLeaf('eventhubs.connection_string', 'secret', eventhubs.connection_string ?? ''),
      ...stringLeaf('eventhubs.connection_string_secret', 'secret_name', eventhubs.connection_string_secret ?? ''),
      ...stringLeaf('eventhubs.checkpoint_container_url', 'url', eventhubs.checkpoint_container_url ?? ''),
    )
  }
  if (row.grpc && want('grpc')) leaves.push(...stringMapLeaves('grpc.metadata', 'header', row.grpc.metadata))
  const database = row.database
  if (database && want('database')) {
    leaves.push(
      ...stringLeaf('database.dsn', 'secret', database.dsn ?? ''),
      ...stringLeaf('database.dsn_secret', 'secret_name', database.dsn_secret ?? ''),
    )
  }
  const network = row.network
  if (network && want('network')) {
    leaves.push(
      ...stringLeaf('network.username', 'identity', network.username ?? '', { active: Boolean(network.password) }),
      ...stringLeaf('network.password', 'secret', network.password ?? ''),
      ...stringLeaf('network.address', 'url', network.address ?? ''),
    )
  }
  const appium = row.appium
  if (appium && want('appium')) {
    leaves.push(...stringLeaf('appium.server_url', 'url', appium.server_url ?? ''))
    leaves.push(...capabilityLeaves('appium.capabilities', appium.capabilities))
  }
  return leaves
}

/**
 * runtimeProtocolCredentialFieldPaths: the override_fields paths of the
 * credential leaves (and literal-holding secret-name fields) a section holds,
 * which Apply to groups never copies between groups.
 */
export function protocolCredentialFieldPaths(row: RuntimeProtocolOverrideSections | undefined | null, section: RuntimeProtocolSectionKey): string[] {
  const paths: string[] = []
  for (const leaf of protocolCredentialLeaves(row, [section])) {
    if (!leaf.value.trim()) continue
    // A masked echo ([REDACTED], or a URL with masked userinfo) stands for a stored credential.
    let credential = protocolLeafSecretBearing(leaf) || hasReservedSecretReference(leaf.value) || leaf.value.includes(REDACTED)
    if (leaf.kind === 'secret_name' && secretNameFieldIssue(leaf.value)) credential = true
    if (leaf.kind === 'identity' && !leaf.active) credential = isCredentialShapedValue(leaf.value) || hasReservedSecretReference(leaf.value)
    if (credential && !paths.includes(leaf.fieldPath)) paths.push(leaf.fieldPath)
  }
  return paths
}

/**
 * runtimeProtocolUncopyableCredentialPaths: credential paths a copy cannot
 * leave out without changing what it means (a credential broker inside the
 * brokers list would drop the whole list).
 */
export function protocolUncopyableCredentialPaths(row: RuntimeProtocolOverrideSections | undefined | null, section: RuntimeProtocolSectionKey): string[] {
  const credentialPaths = protocolCredentialFieldPaths(row, section)
  const paths: string[] = []
  for (const leaf of protocolCredentialLeaves(row, [section])) {
    if (leaf.fieldPath === leaf.path || !credentialPaths.includes(leaf.fieldPath) || paths.includes(leaf.fieldPath)) continue
    // A list element (kafka.brokers.N): its field path is the whole list.
    const listElement = leaf.fieldPath === 'kafka.brokers' || (Boolean(leaf.inList) && /\.\d+$/.test(leaf.path))
    if (listElement) paths.push(leaf.fieldPath)
  }
  return paths
}

/**
 * executionPlanProtocolCredentials: the leaf paths of a plan entry row that
 * hold a typed credential (or a literal in a secret-name field) instead of a
 * project credential reference.
 */
export function protocolPlanTypedCredentialPaths(row: RuntimeProtocolOverrideSections | undefined | null): string[] {
  const paths: string[] = []
  for (const leaf of protocolCredentialLeaves(row)) {
    const value = leaf.value
    if (!value.trim()) continue
    let typed = false
    switch (leaf.kind) {
      case 'secret':
        typed = !isProjectSecretReference(value)
        break
      case 'url':
        typed = urlCarriesTypedCredential(value)
          || (isCredentialShapedValue(value) && !hasAnySecretReference(value))
          || hasReservedSecretReference(value)
        break
      case 'secret_name':
        typed = secretNameFieldIssue(value) !== undefined
        break
      case 'header':
        typed = hasReservedSecretReference(value) || headerNeedsReference(leaf.key ?? '', value)
        break
      case 'free_text':
        typed = hasReservedSecretReference(value)
          || (!isSchemedProjectSecretReference(value) && freeTextSecretBearing(leaf.key ?? '', value, leaf.capability))
        break
      default:
        typed = false
    }
    if (typed && !paths.includes(leaf.path)) paths.push(leaf.path)
  }
  return paths
}
