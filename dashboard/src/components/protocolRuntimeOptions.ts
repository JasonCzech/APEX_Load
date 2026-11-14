import type { KafkaConfig } from '../types/scripts'

/**
 * Select options and config-patch helpers for the native protocol sections of
 * the Author Runtime Settings view (Kafka, JMS, Event Hubs, database, network,
 * Java over HTTP, browser). Option values mirror the Options of the matching
 * pkg/dsl/config_catalog.go entries; protocolRuntimeOptions.parity.test.ts
 * fails when a value drifts from the catalog.
 */

export interface RuntimeSelectOption {
  value: string
  label: string
}

/** Catalog path -> the options the Author view offers for it. */
export const PROTOCOL_RUNTIME_SELECT_OPTIONS = {
  'config.kafka.profile': [
    { value: '', label: 'Standard Kafka' },
    { value: 'eventhubs', label: 'Azure Event Hubs Kafka endpoint' },
  ],
  'config.kafka.security_protocol': [
    { value: 'plaintext', label: 'Plaintext' },
    { value: 'ssl', label: 'SSL' },
    { value: 'sasl_plaintext', label: 'SASL plaintext' },
    { value: 'sasl_ssl', label: 'SASL SSL' },
  ],
  'config.kafka.sasl_mechanism': [
    { value: '', label: 'None' },
    { value: 'plain', label: 'PLAIN' },
    { value: 'scram-sha-256', label: 'SCRAM-SHA-256' },
    { value: 'scram-sha-512', label: 'SCRAM-SHA-512' },
    { value: 'oauthbearer', label: 'OAUTHBEARER' },
  ],
  'config.kafka.oauth.authentication_method': [
    { value: '', label: 'Default' },
    { value: 'client_secret_basic', label: 'Client secret (basic)' },
    { value: 'client_secret_post', label: 'Client secret (post)' },
    { value: 'kafka_3_3_1_basic', label: 'Kafka 3.3.1 basic' },
  ],
  'config.kafka.tls.min_version': [
    { value: '1.2', label: 'TLS 1.2' },
    { value: '1.3', label: 'TLS 1.3' },
  ],
  'config.kafka.tls.max_version': [
    { value: '1.2', label: 'TLS 1.2' },
    { value: '1.3', label: 'TLS 1.3' },
  ],
  'config.kafka.acks': [
    { value: 'none', label: 'None' },
    { value: 'leader', label: 'Leader' },
    { value: 'all', label: 'All in-sync replicas' },
  ],
  'config.kafka.idempotence': [
    { value: '', label: 'Default' },
    { value: 'enabled', label: 'Enabled' },
    { value: 'disabled', label: 'Disabled' },
  ],
  'config.kafka.compression': [
    { value: 'none', label: 'None' },
    { value: 'gzip', label: 'gzip' },
    { value: 'snappy', label: 'Snappy' },
    { value: 'lz4', label: 'LZ4' },
    { value: 'zstd', label: 'zstd' },
  ],
  'config.kafka.partition_strategy': [
    { value: 'key-hash', label: 'Key hash' },
    { value: 'round-robin', label: 'Round robin' },
    { value: 'explicit', label: 'Explicit partition' },
  ],
  'config.kafka.auto_offset_reset': [
    { value: 'earliest', label: 'Earliest' },
    { value: 'latest', label: 'Latest' },
  ],
  'config.kafka.isolation_level': [
    { value: '', label: 'Default' },
    { value: 'read_uncommitted', label: 'Read uncommitted' },
    { value: 'read_committed', label: 'Read committed' },
  ],
  'config.kafka.serialization': [
    { value: '', label: 'Raw bytes' },
    { value: 'json_schema', label: 'JSON Schema (Schema Registry)' },
    { value: 'avro', label: 'Avro (Schema Registry)' },
  ],
  'config.jms.destination_type': [
    { value: 'queue', label: 'Queue' },
    { value: 'topic', label: 'Topic' },
  ],
  'config.jms.acknowledgement': [
    { value: 'auto', label: 'Auto' },
    { value: 'dups_ok', label: 'Duplicates OK' },
    { value: 'client', label: 'Client (mock only)' },
    { value: 'transacted', label: 'Transacted (mock only)' },
  ],
  'config.jms.delivery_mode': [
    { value: 'persistent', label: 'Persistent' },
    { value: 'non_persistent', label: 'Non-persistent' },
  ],
  'config.eventhubs.auth_type': [
    { value: 'connection_string', label: 'SAS connection string' },
    { value: 'aad', label: 'Azure AD (managed identity)' },
  ],
  'config.eventhubs.consumer_mode': [
    { value: 'observer', label: 'Observer' },
    { value: 'processor', label: 'Processor (checkpointed)' },
    { value: 'exclusive', label: 'Exclusive (owner level)' },
  ],
  'config.eventhubs.start_position': [
    { value: 'latest', label: 'Latest (tail)' },
    { value: 'earliest', label: 'Earliest (partition start)' },
  ],
  'config.network.subprotocol': [
    { value: 'dns', label: 'DNS' },
    { value: 'tcp', label: 'TCP / Winsock' },
    { value: 'udp', label: 'UDP / Winsock' },
    { value: 'ftp', label: 'FTP' },
    { value: 'smtp', label: 'SMTP' },
    { value: 'pop3', label: 'POP3' },
    { value: 'imap', label: 'IMAP' },
    { value: 'ldap', label: 'LDAP' },
  ],
  'config.browser.http_credentials.send': [
    { value: '', label: 'Browser default' },
    { value: 'unauthorized', label: 'After a 401 challenge' },
    { value: 'always', label: 'Always' },
  ],
  'config.java_http.codec': [
    { value: 'java_serialized_object_base64', label: 'java_serialized_object_base64' },
  ],
  'config.java_http.helper_mode': [
    { value: 'embedded', label: 'embedded' },
  ],
  'config.java_http.body_encoding': [
    { value: 'base64', label: 'base64' },
  ],
  // APEX Load ships the sqlite and postgres (pgx) drivers only; the catalog's
  // other spellings (sqlite3, mysql, oracle, odbc) are not offered.
  'config.database.driver': [
    { value: 'sqlite', label: 'SQLite (in-memory)' },
    { value: 'postgres', label: 'PostgreSQL' },
  ],
} as const satisfies Record<string, readonly RuntimeSelectOption[]>

export type ProtocolRuntimeSelectPath = keyof typeof PROTOCOL_RUNTIME_SELECT_OPTIONS

/**
 * Paths whose pkg/dsl validator lowercases and trims the value before checking
 * it, so 'PLAIN' or ' SASL_SSL ' is as valid as the catalog spelling. Paths not
 * listed here (consumer_mode, oauth.authentication_method, TLS versions, the
 * java_http codecs) compare exactly.
 */
const CASE_INSENSITIVE_SELECT_PATHS: ReadonlySet<ProtocolRuntimeSelectPath> = new Set<ProtocolRuntimeSelectPath>([
  'config.kafka.profile',
  'config.kafka.security_protocol',
  'config.kafka.sasl_mechanism',
  'config.kafka.acks',
  'config.kafka.idempotence',
  'config.kafka.compression',
  'config.kafka.partition_strategy',
  'config.kafka.auto_offset_reset',
  'config.kafka.isolation_level',
  'config.kafka.serialization',
  'config.jms.destination_type',
  'config.jms.acknowledgement',
  'config.jms.delivery_mode',
  'config.eventhubs.auth_type',
  'config.eventhubs.start_position',
  'config.network.subprotocol',
  'config.browser.http_credentials.send',
  'config.database.driver',
])

/**
 * Accepted spellings that run as one of the offered options: the database
 * runtime opens sqlite3 as sqlite (normalizeDriver), a winsock network script
 * dials TCP, and raw Kafka serialization is the raw-bytes default.
 */
const SELECT_VALUE_ALIASES: Partial<Record<ProtocolRuntimeSelectPath, Record<string, string>>> = {
  'config.database.driver': { sqlite3: 'sqlite' },
  'config.network.subprotocol': { winsock: 'tcp' },
  'config.kafka.serialization': { raw: '' },
}

/**
 * The offered option value a stored value runs as, or undefined when the
 * backend rejects it (or the Author view does not offer it).
 */
export function matchingSelectOptionValue(path: ProtocolRuntimeSelectPath, stored: string | undefined): string | undefined {
  if (stored === undefined) return undefined
  const options: readonly RuntimeSelectOption[] = PROTOCOL_RUNTIME_SELECT_OPTIONS[path]
  if (options.some(option => option.value === stored)) return stored
  if (!CASE_INSENSITIVE_SELECT_PATHS.has(path)) return undefined
  const normalized = stored.trim().toLowerCase()
  const candidate = SELECT_VALUE_ALIASES[path]?.[normalized] ?? normalized
  return options.some(option => option.value === candidate) ? candidate : undefined
}

/**
 * The options for a select, plus the stored value when it is not one of them,
 * so a select never shows a different value than the script stores. A stored
 * spelling the backend normalizes ('PLAIN', 'sqlite3', 'SMTP') takes the place
 * of the option it runs as, keeping that option's label, so the select shows
 * it as chosen without rewriting the script; only a value the backend rejects
 * is listed as not supported.
 */
export function selectOptionsWithStored(path: ProtocolRuntimeSelectPath, stored: string | undefined): RuntimeSelectOption[] {
  const options: RuntimeSelectOption[] = [...PROTOCOL_RUNTIME_SELECT_OPTIONS[path]]
  if (stored === undefined || stored === '' || options.some(option => option.value === stored)) {
    return options
  }
  const match = matchingSelectOptionValue(path, stored)
  if (match !== undefined) {
    return options.map(option => (option.value === match ? { value: stored, label: option.label } : option))
  }
  options.push({ value: stored, label: `${stored} (stored value, not supported here)` })
  return options
}

export const DATABASE_DSN_PLACEHOLDERS: Record<string, string> = {
  sqlite: ':memory: or file:testdb?mode=memory&cache=shared',
  postgres: 'postgres://app_user@db.example.test:5432/orders?sslmode=require',
}

/** Placeholder for config.database.dsn that the selected driver accepts. */
export function databaseDSNPlaceholder(driver: string | undefined): string {
  const normalized = (driver || '').trim().toLowerCase()
  if (normalized === 'postgres') return DATABASE_DSN_PLACEHOLDERS.postgres
  return DATABASE_DSN_PLACEHOLDERS.sqlite
}

const SECRET_TEMPLATE = /\{\{\s*secret\.\s*([A-Za-z0-9_.-]{1,249})\s*\}\}/g

/** Project secret names a value references with {{secret.NAME}}, in order, without duplicates. */
export function secretReferenceNames(value: unknown): string[] {
  if (typeof value !== 'string') return []
  const names: string[] = []
  for (const match of value.matchAll(SECRET_TEMPLATE)) {
    if (!names.includes(match[1])) names.push(match[1])
  }
  return names
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}

function isEmptyValue(value: unknown): boolean {
  if (value === undefined || value === null || value === '') return true
  if (Array.isArray(value)) return value.length === 0
  if (isPlainObject(value)) return Object.keys(value).length === 0
  return false
}

/**
 * Returns a copy of base with the dot-separated path set to value. An empty
 * value (undefined, '', [] or {}) removes the key, and a nested object left
 * empty is removed too, so clearing the last field of config.kafka.oauth does
 * not leave an `oauth: {}` the backend reads as a configured (and invalid)
 * OAuth block. Keys the patch does not name are kept as they are.
 */
export function patchConfigBlock<T extends object>(base: T | undefined, path: string, value: unknown): T {
  const parts = path.split('.').filter(Boolean)
  const apply = (current: unknown, index: number): Record<string, unknown> | undefined => {
    const next: Record<string, unknown> = isPlainObject(current) ? { ...current } : {}
    const key = parts[index]
    if (index === parts.length - 1) {
      if (isEmptyValue(value)) delete next[key]
      else next[key] = value
    } else {
      const child = apply(next[key], index + 1)
      if (child === undefined) delete next[key]
      else next[key] = child
    }
    return Object.keys(next).length === 0 && index > 0 ? undefined : next
  }
  return (apply(base, 0) ?? {}) as T
}

/** Parses an optional integer input: '' clears the value, anything else is a whole number. */
export function parseOptionalInteger(raw: string): number | undefined {
  if (raw.trim() === '') return undefined
  const parsed = Number(raw)
  return Number.isFinite(parsed) ? Math.trunc(parsed) : undefined
}

/** One entry per line, trimmed, blank lines dropped. */
export function parseLines(raw: string): string[] {
  return raw.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
}

/**
 * Mirrors dsl validateJavaVuserParamBraces: both delimiters or neither, each
 * 1 to 8 visible characters with no whitespace. Returns '' when valid.
 */
export function javaVuserParamBraceIssue(left: string | undefined, right: string | undefined): string {
  const l = left ?? ''
  const r = right ?? ''
  if (l === '' && r === '') return ''
  if (l === '' || r === '') return 'Set both parameter delimiters, or leave both empty.'
  // eslint-disable-next-line no-control-regex
  const invalid = (value: string) => [...value].length > 8 || /\s/.test(value) || /[\u0000-\u001f\u007f]/.test(value)
  if (invalid(l) || invalid(r)) return 'Each parameter delimiter must be 1 to 8 visible characters with no spaces.'
  return ''
}

/** dsl.KafkaConfig fields the shared KafkaConfig type does not declare yet. */
export type AuthorKafkaConfig = Omit<KafkaConfig, 'oauth' | 'tls'> & {
  consumer_transactions?: boolean
  producer?: {
    retries?: number
    linger_ms?: number
    batch_bytes?: number
    buffer_bytes?: number
    admission_timeout_ms?: number
    delivery_timeout_ms?: number
    request_timeout_ms?: number
    max_in_flight_requests?: number
  }
  oauth?: NonNullable<KafkaConfig['oauth']> & { client_secret?: string }
  tls?: NonNullable<KafkaConfig['tls']> & { min_version?: string; max_version?: string }
}

/** The only OAuth mode validateKafkaScriptIssues accepts. */
export const KAFKA_OAUTH_MODE = 'client_credentials'

export function kafkaRuntimeSummary(kafka: AuthorKafkaConfig | undefined): string {
  if (kafka?.mock) return 'Mock broker'
  const brokers = kafka?.brokers?.length ?? 0
  if (brokers === 0) return 'Not configured'
  return `${brokers} ${brokers === 1 ? 'broker' : 'brokers'} · ${kafka?.security_protocol || 'plaintext'}`
}
