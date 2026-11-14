import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

import catalogSource from '../../../pkg/dsl/config_catalog.go?raw'
import databaseValidationSource from '../../../pkg/dsl/catalog_database_validation.go?raw'
import javaHTTPValidationSource from '../../../pkg/dsl/catalog_java_http_validation.go?raw'
import kafkaValidationSource from '../../../pkg/dsl/catalog_kafka_validation.go?raw'
import type { ScriptDefinition } from '../types/scripts'
import { BrowserAccessFields } from './BrowserAccessFields'
import { KafkaRuntimeSection } from './KafkaRuntimeSection'
import { KAFKA_OAUTH_MODE, PROTOCOL_RUNTIME_SELECT_OPTIONS } from './protocolRuntimeOptions'
import { JavaHTTPRuntimeSection } from './ScriptRuntimeSettingsProtocolSections'

// Runs in the node environment: Vite serves the Go sources (?raw) only there,
// so the sections are rendered to static markup and read with attribute scans.

// The Author view's native protocol controls must match pkg/dsl: select values
// are the catalog's Options, every rendered field is a catalog path, and each
// field sits under the canonical runtime group the catalog assigns it.

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function catalogBlock(path: string): string | undefined {
  const start = catalogSource.search(new RegExp(`Path:\\s*"${escapeRegExp(path)}"`))
  if (start < 0) return undefined
  return catalogSource.slice(start, catalogSource.indexOf('\n\t},', start))
}

function catalogOptions(path: string): string[] | undefined {
  const match = catalogBlock(path)?.match(/Options:\s*\[\]string\{([^}]*)\}/)
  if (!match) return undefined
  return match[1].split(',').map(item => item.trim()).filter(Boolean).map(item => JSON.parse(item) as string)
}

const GROUP_IDS = Object.fromEntries(
  [...catalogSource.matchAll(/(ConfigGroup\w+)\s*=\s*"([a-z_]+)"/g)].map(match => [match[1], match[2]]),
)

function catalogGroup(path: string): string | undefined {
  const constant = catalogBlock(path)?.match(/Group:\s*(ConfigGroup\w+)/)?.[1]
  return constant ? GROUP_IDS[constant] : undefined
}

function catalogPathsWithPrefix(prefix: string): string[] {
  return [...catalogSource.matchAll(/Path:\s*"([^"]+)"/g)].map(match => match[1]).filter(path => path.startsWith(prefix))
}

// Accepted spellings the Author view does not offer, with the reason.
const NOT_OFFERED: Record<string, string[]> = {
  // Empty helper mode is the embedded default; the view offers embedded itself.
  'config.java_http.helper_mode': [''],
  // APEX Load ships only the sqlite and postgres (pgx) database drivers.
  'config.database.driver': ['mysql', 'oracle', 'odbc', 'sqlite3'],
}

describe('protocol runtime select options parity with pkg/dsl', () => {
  it('offers exactly the catalog options for every select', () => {
    const problems: string[] = []
    for (const [path, options] of Object.entries(PROTOCOL_RUNTIME_SELECT_OPTIONS)) {
      const accepted = catalogOptions(path) ?? []
      if (accepted.length === 0) problems.push(`${path}: no catalog Options`)
      const offered = options.map(option => option.value as string)
      const rejected = offered.filter(value => !accepted.includes(value))
      const missing = accepted.filter(value => !offered.includes(value) && !(NOT_OFFERED[path] ?? []).includes(value))
      if (rejected.length) problems.push(`${path}: offers ${rejected.join(', ')} the catalog rejects`)
      if (missing.length) problems.push(`${path}: misses ${missing.join(', ')}`)
    }
    expect(problems).toEqual([])
  })

  it('offers only the sqlite and postgres drivers, both of which validation accepts', () => {
    expect(PROTOCOL_RUNTIME_SELECT_OPTIONS['config.database.driver'].map(option => option.value)).toEqual(['sqlite', 'postgres'])
    const accepted = databaseValidationSource.match(/isAllowedValue\([^,]+,\s*([^)]*)\)/)?.[1] ?? ''
    expect(accepted).toContain('"sqlite"')
    expect(accepted).toContain('"postgres"')
  })

  it('uses the only Kafka OAuth mode validation accepts', () => {
    expect(kafkaValidationSource).toContain(`o.Mode != "${KAFKA_OAUTH_MODE}"`)
  })
})

interface RenderedField {
  path: string
  group?: string
}

/**
 * Every data-catalog-path in the markup, with the data-canonical-group of the
 * sub-group it sits in. Group sub-groups are siblings, so a field belongs to
 * the last group marker before it.
 */
function renderedFields(markup: string): RenderedField[] {
  const fields: RenderedField[] = []
  let group: string | undefined
  for (const match of markup.matchAll(/data-(canonical-group|catalog-path)="([^"]+)"/g)) {
    if (match[1] === 'canonical-group') group = match[2]
    else fields.push({ path: match[2], group })
  }
  return fields
}

function groupProblems(fields: RenderedField[]): string[] {
  const problems: string[] = []
  for (const { path, group } of fields) {
    // Fields of an object-list entry (client certificate passphrase) belong to their list.
    const catalogPath = catalogBlock(path) ? path : path.split('.').slice(0, -1).join('.')
    if (!catalogBlock(catalogPath)) {
      problems.push(`${path}: not a catalog path`)
      continue
    }
    if (!group) problems.push(`${path}: outside a canonical group`)
    else if (catalogGroup(catalogPath) !== group) problems.push(`${path}: rendered under ${group}, catalog group ${catalogGroup(catalogPath)}`)
  }
  return problems
}

describe('native protocol sections render catalog fields in their catalog group', () => {
  it('renders every Kafka catalog field under its catalog group', () => {
    const script: ScriptDefinition = {
      name: 'Kafka', version: '1', protocol: 'kafka', actions: [],
      config: {
        base_url: '',
        // OAuth on and preserved properties present, so their fields render too.
        kafka: { oauth: { mode: KAFKA_OAUTH_MODE }, properties: { 'linger.ms': '5' } },
      },
    }
    const fields = renderedFields(renderToStaticMarkup(<KafkaRuntimeSection script={script} sectionIsActive={() => true} onSelectSection={vi.fn()} onUpdateField={vi.fn()} />))
    expect(groupProblems(fields)).toEqual([])
    const rendered = new Set(fields.map(field => field.path))
    expect(catalogPathsWithPrefix('config.kafka.').filter(path => !rendered.has(path))).toEqual([])
  })

  it('renders the browser access fields under their catalog group', () => {
    const fields = renderedFields(renderToStaticMarkup(<BrowserAccessFields browser={{ client_certificates: [{ origin: 'https://mtls.example.test' }] }} onChange={vi.fn()} />))
    expect(groupProblems(fields)).toEqual([])
    const rendered = new Set(fields.map(field => field.path))
    for (const path of ['locale', 'timezone', 'storage_state', 'http_credentials.username', 'http_credentials.password', 'http_credentials.origin', 'http_credentials.send', 'proxy.server', 'proxy.bypass', 'proxy.username', 'proxy.password', 'client_certificates']) {
      expect(rendered.has(`config.browser.${path}`), path).toBe(true)
    }
    expect(fields.every(field => field.path.startsWith('config.browser.'))).toBe(true)
  })

  it('offers Java HTTP editors only for fields the embedded validator accepts', () => {
    const script: ScriptDefinition = {
      name: 'Java HTTP', version: '1', protocol: 'java_http', actions: [],
      config: { base_url: 'https://java.example.test', java_http: { codec: 'java_serialized_object_base64' } },
    }
    const editable = renderedFields(renderToStaticMarkup(<JavaHTTPRuntimeSection script={script} sectionIsActive={() => true} onSelectSection={vi.fn()} onUpdateField={vi.fn()} />)).map(field => field.path)
    const rejected = [...javaHTTPValidationSource.matchAll(/config\.java_http\.([a-z_]+) is not supported/g)].map(match => `config.java_http.${match[1]}`)
    expect(rejected).toEqual(expect.arrayContaining(['config.java_http.object_schema', 'config.java_http.request_mapping', 'config.java_http.extractor_type']))
    expect(editable.length).toBeGreaterThan(0)
    expect(editable.filter(path => rejected.includes(path))).toEqual([])
    expect(editable.every(path => catalogBlock(path) !== undefined)).toBe(true)
  })
})
