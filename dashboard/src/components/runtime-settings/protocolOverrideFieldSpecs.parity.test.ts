import { describe, expect, it } from 'vitest'

import { normalizeRuntimeOverrideFieldPath, RUNTIME_PROTOCOL_OVERRIDE_SECTIONS } from '../runtimeSettingsModel'
import { PROTOCOL_OVERRIDE_ENGINE_DEFAULTS, PROTOCOL_OVERRIDE_VALIDATOR_OPTIONS, PROTOCOL_OVERRIDE_FIELD_SPECS, type ProtocolOverrideSpecSection } from './protocolOverrideFieldSpecs'

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
        if (RESTRICTED_OPTIONS[entry.path]) {
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
})
