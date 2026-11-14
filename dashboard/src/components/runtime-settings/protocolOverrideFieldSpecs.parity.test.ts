import { describe, expect, it } from 'vitest'

import { normalizeRuntimeOverrideFieldPath, RUNTIME_PROTOCOL_OVERRIDE_SECTIONS } from '../runtimeSettingsModel'
import { KAFKA_EVENTHUBS_PROFILE_DEFAULTS, protocolOverrideProfileDefault, PROTOCOL_OVERRIDE_ENGINE_DEFAULTS, PROTOCOL_OVERRIDE_VALIDATOR_OPTIONS, PROTOCOL_OVERRIDE_FIELD_SPECS, type ProtocolOverrideSpecSection } from './protocolOverrideFieldSpecs'

interface CatalogEntry {
  path: string
  label: string
  options?: string[]
}

// The Go catalog is read at run time, so the dashboard build needs no copy of it.
async function loadCatalog(): Promise<CatalogEntry[]> {
  // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
  const { readFileSync } = await import('node:fs')
  const source = readFileSync(new URL('../../../../pkg/dsl/config_catalog.go', import.meta.url), 'utf8') as string
  const entries: CatalogEntry[] = []
  for (const match of source.matchAll(/\{\s*\n\s*Path:\s*"config\.([^"]+)",([\s\S]*?)\n\t\},/g)) {
    const body = match[2]
    const label = /\bLabel:\s*"([^"]*)"/.exec(body)?.[1] ?? ''
    const options = /\bOptions:\s*\[\]string\{([^}]*)\}/.exec(body)?.[1]
    entries.push({
      path: match[1],
      label,
      // The catalog's "" choice is kept: it is an explicit none, distinct from Inherit.
      options: options === undefined ? undefined : Array.from(options.matchAll(/"([^"]*)"/g), option => option[1]),
    })
  }
  expect(entries.length).toBe((source.match(/\bPath:\s*"config\./g) || []).length)
  return entries
}

// Java Env fields its own editor (JavaEnvSettingsFields) already renders, and
// the operator runner fields a scenario can never steer.
const JAVA_ENV_EDITOR_FIELDS = new Set([
  'enabled', 'runner_command', 'harness_jar', 'classpath', 'classpath_assets', 'work_dir', 'max_processes',
  'jdk_selection', 'jdk_home', 'additional_vm_parameters', 'use_xbootclasspath', 'dedicated_class_loader',
  'skip_parameter_evaluation', 'jvm_sharing', 'vusers_per_jvm',
])

// The standing database-driver rule: the editor offers SQLite and PostgreSQL (pgx) only.
const RESTRICTED_OPTIONS: Record<string, string[]> = { 'database.driver': ['sqlite', 'postgres'] }

function readRepoFile(relative: string, readFileSync: (url: URL, encoding: string) => string): string {
  return readFileSync(new URL(`../../../../${relative}`, import.meta.url), 'utf8')
}

describe('protocol override field specs parity with pkg/dsl/config_catalog.go', () => {
  it('covers every catalog field of each section, with its label and options', async () => {
    const catalog = await loadCatalog()
    const sections: ProtocolOverrideSpecSection[] = [...RUNTIME_PROTOCOL_OVERRIDE_SECTIONS, 'websocket', 'java_vuser']
    for (const section of sections) {
      const expected = catalog.filter(entry => entry.path.startsWith(`${section}.`))
        .filter(entry => section !== 'java_vuser' || !JAVA_ENV_EDITOR_FIELDS.has(entry.path.slice('java_vuser.'.length)))
      const specs = PROTOCOL_OVERRIDE_FIELD_SPECS[section]
      expect(specs.map(spec => spec.path).sort()).toEqual(expected.map(entry => entry.path).sort())
      for (const entry of expected) {
        const spec = specs.find(candidate => candidate.path === entry.path)!
        expect([entry.path, spec.label]).toEqual([entry.path, entry.label])
        if (spec.unsupported) {
          // The validator refuses every value: only the explicit none (or removal) is offered.
          if (spec.kind === 'map') expect([entry.path, spec.removeOnly]).toEqual([entry.path, true])
          else expect([entry.path, spec.kind, spec.options]).toEqual([entry.path, 'select', ['']])
        } else if (RESTRICTED_OPTIONS[entry.path]) {
          expect(spec.options).toEqual(RESTRICTED_OPTIONS[entry.path])
          for (const option of spec.options ?? []) expect(entry.options).toContain(option)
        } else if (entry.options) {
          expect([entry.path, spec.options]).toEqual([entry.path, entry.options])
        }
      }
    }
  })

  it('uses only paths the API accepts in override_fields', () => {
    for (const specs of Object.values(PROTOCOL_OVERRIDE_FIELD_SPECS)) {
      for (const spec of specs) expect([spec.path, normalizeRuntimeOverrideFieldPath(spec.path)]).toEqual([spec.path, spec.path])
    }
  })

  it('marks the catalog Secret leaves as secret controls', async () => {
    // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const source = readFileSync(new URL('../../../../pkg/dsl/config_catalog.go', import.meta.url), 'utf8') as string
    const secretPaths = Array.from(source.matchAll(/Path:\s*"config\.([^"]+)",[\s\S]*?\n\t\},/g))
      .filter(match => /\bSecret:\s*true/.test(match[0]))
      .map(match => match[1])
    const specs = Object.values(PROTOCOL_OVERRIDE_FIELD_SPECS).flat()
    for (const spec of specs) {
      if (secretPaths.includes(spec.path)) expect([spec.path, spec.kind]).toEqual([spec.path, 'secret'])
    }
    expect(specs.filter(spec => spec.kind === 'secret').map(spec => spec.path).sort())
      .toEqual(specs.filter(spec => secretPaths.includes(spec.path)).map(spec => spec.path).sort())
  })

  it('offers exactly the values the validator accepts where the catalog lists none', async () => {
    // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const validation = readRepoFile('pkg/dsl/catalog_kafka_validation.go', readFileSync)
    expect(validation).toContain('o.Mode != "client_credentials"')
    expect(PROTOCOL_OVERRIDE_VALIDATOR_OPTIONS['kafka.oauth.mode']).toEqual(['client_credentials'])
    for (const [path, options] of Object.entries(PROTOCOL_OVERRIDE_VALIDATOR_OPTIONS)) {
      const spec = Object.values(PROTOCOL_OVERRIDE_FIELD_SPECS).flat().find(candidate => candidate.path === path)
      expect([path, spec?.kind, spec?.options]).toEqual([path, 'select', options])
    }
  })

  it('never offers a java_http value the embedded validator rejects', async () => {
    // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const validation = readRepoFile('pkg/dsl/catalog_java_http_validation.go', readFileSync)
    const rejected = Array.from(validation.matchAll(/"config\.(java_http\.[a-z_]+) is not supported by embedded java_http"/g), match => match[1]).sort()
    expect(rejected).toEqual(['java_http.extractor_type', 'java_http.object_schema', 'java_http.request_mapping'])
    const unsupported = PROTOCOL_OVERRIDE_FIELD_SPECS.java_http.filter(spec => spec.unsupported).map(spec => spec.path).sort()
    expect(unsupported).toEqual(rejected)
    for (const spec of PROTOCOL_OVERRIDE_FIELD_SPECS.java_http.filter(candidate => candidate.unsupported)) {
      if (spec.kind === 'map') expect(spec.removeOnly).toBe(true)
      else expect([spec.path, spec.kind, spec.options]).toEqual([spec.path, 'select', ['']])
    }
    // helper_mode accepts only embedded (sidecar is refused for embedded java_http).
    expect(validation).toMatch(/helper_mode/)
    expect(PROTOCOL_OVERRIDE_FIELD_SPECS.java_http.find(spec => spec.path === 'java_http.helper_mode')?.options).not.toContain('sidecar')
  })

  it('shows the engine default where it differs from the catalog default (grpc.use_reflection)', async () => {
    // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const catalog = readRepoFile('pkg/dsl/config_catalog.go', readFileSync)
    const engine = readRepoFile('pkg/protocol/grpc/grpc.go', readFileSync)
    const types = readRepoFile('pkg/dsl/types.go', readFileSync)
    // The catalog documents true, but the field is a plain omitempty bool the
    // engine reads as false when unset. If either side changes, revisit the spec.
    expect(/Path:\s*"config\.grpc\.use_reflection",[\s\S]*?Default:\s*true,/.test(catalog)).toBe(true)
    expect(types).toMatch(/UseReflection\s+bool\s+`json:"use_reflection,omitempty"`/)
    expect(engine).toContain('g.useReflect, _ = boolConfig(config, "grpc_use_reflection")')
    expect(PROTOCOL_OVERRIDE_ENGINE_DEFAULTS['grpc.use_reflection']).toBe(false)
    const spec = PROTOCOL_OVERRIDE_FIELD_SPECS.grpc.find(candidate => candidate.path === 'grpc.use_reflection')
    expect(spec?.defaultValue).toBe(false)
  })

  it('shows the engine fallback, not the catalog default, as the inherit placeholder', async () => {
    // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const jms = readRepoFile('pkg/protocol/jms/jms.go', readFileSync)
    const eventhubs = readRepoFile('pkg/protocol/eventhubs/eventhubs.go', readFileSync)
    const database = readRepoFile('pkg/protocol/database/database.go', readFileSync)
    const grpc = readRepoFile('pkg/protocol/grpc/grpc.go', readFileSync)
    const types = readRepoFile('pkg/dsl/types.go', readFileSync)
    const kafka = readRepoFile('pkg/protocol/kafka/kafka.go', readFileSync)
    expect(kafka).toMatch(/const defaultMockPartitions = 4\b/)
    expect(kafka).toContain('boundedConfigInt(cfg, "kafka_partitions", defaultMockPartitions,')
    expect(kafka).toContain('boundedConfigInt(cfg, "kafka_timeout_ms", int((5*time.Second)/time.Millisecond),')
    expect(kafka).toMatch(/if k\.cfg\.Timeout <= 0 \{\s*k\.cfg\.Timeout = 5 \* time\.Second/)
    expect(jms).toMatch(/if j\.cfg\.Timeout <= 0 \{\s*j\.cfg\.Timeout = 5 \* time\.Second/)
    expect(eventhubs).toMatch(/defaultTimeout\s+= 5 \* time\.Second/)
    expect(database).toMatch(/defaultDatabaseMaxRows\s+= 100\b/)
    expect(grpc).toMatch(/if g\.maxRecvBytes <= 0 \{\s*g\.maxRecvBytes = dsl\.MaxGRPCStreamBytes/)
    expect(types).toMatch(/MaxGRPCStreamBytes\s+= 8 << 20/)
    // The pool limits apply only when set (> 0); unset leaves database/sql's own behaviour.
    expect(database).toContain('if maxOpen := protocol.ConfigInt(config, "database_max_open_conns"); maxOpen > 0 {')
    expect(database).toContain('if maxIdle := protocol.ConfigInt(config, "database_max_idle_conns"); maxIdle > 0 {')
    expect(database).toContain('if lifetimeMS := protocol.ConfigInt(config, "database_conn_max_lifetime_ms"); lifetimeMS > 0 {')
    const expected: Record<string, number | undefined> = {
      'jms.timeout_ms': 5000,
      'eventhubs.timeout_ms': 5000,
      'kafka.timeout_ms': 5000,
      'kafka.partitions': 4,
      'database.max_rows': 100,
      'grpc.max_recv_msg_size': 8 << 20,
      'database.max_open_conns': undefined,
      'database.max_idle_conns': undefined,
      'database.conn_max_lifetime_ms': undefined,
    }
    const specs = Object.values(PROTOCOL_OVERRIDE_FIELD_SPECS).flat()
    for (const [path, value] of Object.entries(expected)) {
      expect([path, PROTOCOL_OVERRIDE_ENGINE_DEFAULTS[path]]).toEqual([path, value])
      expect([path, specs.find(candidate => candidate.path === path)?.defaultValue]).toEqual([path, value])
    }
  })

  it('shows the eventhubs Kafka profile defaults the engine fills in (pkg/protocol/kafka/kafka.go)', async () => {
    // @ts-expect-error Vitest runs in Node, while the dashboard app intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const kafka = readRepoFile('pkg/protocol/kafka/kafka.go', readFileSync)
    const profileBlock = /if k\.cfg\.Profile == "eventhubs" \{([\s\S]*?)\n\t\}\n/.exec(kafka)?.[1] ?? ''
    expect(profileBlock).toMatch(new RegExp(`if k\\.cfg\\.SecurityProtocol == "" \\{\\s*k\\.cfg\\.SecurityProtocol = "${KAFKA_EVENTHUBS_PROFILE_DEFAULTS.security_protocol}"`))
    expect(profileBlock).toMatch(new RegExp(`if k\\.cfg\\.SASLMechanism == "" \\{\\s*k\\.cfg\\.SASLMechanism = "${KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_mechanism}"`))
    expect(profileBlock).toContain(`if k.cfg.SASLMechanism == "${KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_mechanism}" && k.cfg.SASLUsername == "" {\n\t\t\tk.cfg.SASLUsername = "${KAFKA_EVENTHUBS_PROFILE_DEFAULTS.sasl_username}"`)
    const eventhubs = (values: Record<string, unknown>) => (path: string) => values[path]
    expect(protocolOverrideProfileDefault('kafka.security_protocol', eventhubs({ 'kafka.profile': 'eventhubs' }))).toBe('sasl_ssl')
    expect(protocolOverrideProfileDefault('kafka.sasl_mechanism', eventhubs({ 'kafka.profile': 'eventhubs' }))).toBe('plain')
    expect(protocolOverrideProfileDefault('kafka.sasl_username', eventhubs({ 'kafka.profile': 'eventhubs' }))).toBe('$ConnectionString')
    expect(protocolOverrideProfileDefault('kafka.sasl_username', eventhubs({ 'kafka.profile': 'eventhubs', 'kafka.sasl_mechanism': 'oauthbearer' }))).toBeUndefined()
    expect(protocolOverrideProfileDefault('kafka.security_protocol', eventhubs({}))).toBeUndefined()
    // catalog_kafka_validation.go and kafka.go trim and lowercase the profile before comparing.
    expect(kafka).toMatch(/Profile:\s+strings\.ToLower\(strings\.TrimSpace\(stringValue\(cfg, "kafka_profile"\)\)\)/)
    expect(protocolOverrideProfileDefault('kafka.security_protocol', eventhubs({ 'kafka.profile': ' EventHubs ' }))).toBe('sasl_ssl')
  })
})
