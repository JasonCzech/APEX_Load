import { describe, expect, it } from 'vitest'
import catalogSource from '../../../pkg/dsl/config_catalog.go?raw'
import contractFields from '../../../pkg/api/import_settings_fields.json'
import {
 CANONICAL_RUNTIME_GROUP_IDS,
 importRuntimeFields,
 mergeImportRuntimeFields,
 resolveCanonicalRuntimeGroup,
 type RemoteImportRuntimeField,
} from './importRuntimeSettingsModel'

// The offline import catalog stands in for GET /scripts/import/settings/fields
// when the server list has not arrived. These tests read the server's own
// sources (pkg/dsl/config_catalog.go and pkg/api/import_settings_fields.json)
// so the fallback cannot drift from what the import POST accepts.

type CatalogEntry = { path: string; group: string; type: string; options?: string[]; protocols: string[]; scopes: string[]; secret: boolean }

function parseCatalog(source: string): CatalogEntry[] {
 const constants: Record<string, string> = {}
 for (const match of source.matchAll(/^\s*(ConfigGroup\w+|ConfigFieldType\w+|ConfigScope\w+)\s*=\s*"([^"]*)"/gm)) constants[match[1]] = match[2]
 const lists: Record<string, string[]> = {}
 const parseList = (body: string) => body.split(',').map(item => item.trim()).filter(Boolean).map(item => item.startsWith('"') ? JSON.parse(item) as string : constants[item])
 for (const match of source.matchAll(/^\s*(\w+)\s*=\s*\[\]string\{([^}]*)\}/gm)) lists[match[1]] = parseList(match[2])
 const body = source.slice(source.indexOf('var canonicalConfigCatalog'))
 const entries: CatalogEntry[] = []
 for (const block of body.split(/\n\t\{\n/).slice(1)) {
  const end = block.indexOf('\n\t},')
  const text = `${end >= 0 ? block.slice(0, end) : block}\n`
  const get = (key: string) => text.match(new RegExp(`\\t\\t${key}:\\s*(.+?),\\n`))?.[1]
  const list = (value?: string) => value == null ? undefined : value.startsWith('[]string{') ? parseList(value.slice('[]string{'.length, -1)) : lists[value]
  const path = get('Path')
  if (!path) continue // not a field entry: a constant or helper block
  entries.push({
   path: JSON.parse(path) as string,
   group: constants[get('Group') || ''],
   type: constants[get('Type') || ''],
   options: list(get('Options')),
   protocols: list(get('Protocols')) || [],
   scopes: list(get('Scopes')) ?? ['<unparsed>'],
   secret: get('Secret') === 'true',
  })
 }
 return entries
}

const catalog = parseCatalog(catalogSource)
const byPath = new Map(catalog.map(entry => [entry.path, entry]))
const httpOrSoapImport = catalog.filter(entry => entry.scopes.includes('import') && entry.protocols.some(protocol => ['*', 'http', 'soap'].includes(protocol)))
const editableScalar = (entry: CatalogEntry) => !entry.secret && !['key_value_map', 'object_list', 'object'].includes(entry.type)
const contractByPath = new Map((contractFields as Array<{ path: string; options?: string[] }>).map(field => [field.path, field]))
const resolvedGroupId = (path: string) => CANONICAL_RUNTIME_GROUP_IDS[resolveCanonicalRuntimeGroup('HTTP', path)]

// Fixed values DSL validation accepts (pkg/dsl/validate.go validateWebRuntime*)
// for selects the import editor offers. The Go catalog declares the web runtime
// ones as selects too, guarded by TestConfigCatalogOptionsMatchValidators.
const DSL_ENUMS_FOR_CATALOG_STRINGS: Record<string, string[]> = {
 'config.web_runtime.network_simulation.preset': ['56k', 'dsl', 't1', '10mbps'],
 'config.web_runtime.streaming.mode': ['adaptive', 'fixed'],
 'config.web_runtime.streaming.bandwidth_estimation': ['rolling', 'instant'],
 // Browser identity selects come from the emulation catalog; the import contract lists them as text.
 'config.browser_emulation.browser_type': ['chrome', 'edge', 'firefox', 'safari', 'ie'],
 'config.browser_emulation.platform': ['windows', 'macos', 'linux', 'android', 'ios'],
}

describe('offline import runtime catalog parity with pkg/dsl/config_catalog.go', () => {
 it('parses the catalog', () => {
  expect(catalog.length).toBeGreaterThan(300)
  // Retired fields have an empty scope list; an unparsed one would read as <unparsed>.
  expect(catalog.filter(entry => !entry.group || !entry.type || entry.scopes.includes('<unparsed>') || !entry.protocols.length).map(entry => entry.path)).toEqual([])
 })

 it('offers every editable import-scope HTTP and SOAP catalog field', () => {
  const offered = new Set(importRuntimeFields.map(field => field.path))
  expect(httpOrSoapImport.filter(editableScalar).map(entry => entry.path).filter(path => !offered.has(path))).toEqual([])
 })

 it('files each HTTP and SOAP setting under the catalog group, offline and for rows without a field', () => {
  const offlineMismatches = importRuntimeFields
   .filter(field => byPath.has(field.path) && httpOrSoapImport.includes(byPath.get(field.path)!))
   .filter(field => field.groupId !== byPath.get(field.path)!.group)
   .map(field => `${field.path}: ${field.groupId} != ${byPath.get(field.path)!.group}`)
  expect(offlineMismatches).toEqual([])
  const resolverMismatches = httpOrSoapImport
   .filter(entry => resolvedGroupId(entry.path) !== entry.group)
   .map(entry => `${entry.path}: ${resolvedGroupId(entry.path)} != ${entry.group}`)
  expect(resolverMismatches).toEqual([])
 })

 it('offers only select values the import POST accepts', () => {
  const problems: string[] = []
  for (const field of importRuntimeFields.filter(candidate => candidate.type === 'select')) {
   const entry = byPath.get(field.path)
   const contract = contractByPath.get(field.path)?.options
   // The POST validates against the contract list when it has one, else the catalog list.
   const accepted = contract?.length ? contract : entry?.options?.filter(option => option !== '') ?? DSL_ENUMS_FOR_CATALOG_STRINGS[field.path]
   if (!accepted) { // reported through problems, so the expect below fails
    problems.push(`${field.path}: no server-side option list`)
    continue
   }
   if (field.options?.some(option => option === '')) problems.push(`${field.path}: lists a blank option`)
   const rejected = (field.options || []).filter(option => !accepted.includes(option))
   if (rejected.length) problems.push(`${field.path}: rejected ${rejected.join(', ')}`)
   const missing = accepted.filter(option => !(field.options || []).includes(option))
   const narrowedTls = field.path === 'config.http.tls.min_version' || field.path === 'config.http.tls.max_version'
   if (missing.length && !narrowedTls) problems.push(`${field.path}: missing ${missing.join(', ')}`)
  }
  expect(problems).toEqual([])
 })

 it('keeps catalog-string selects in step with DSL validation', () => {
  for (const [path, values] of Object.entries(DSL_ENUMS_FOR_CATALOG_STRINGS)) {
   expect(importRuntimeFields.find(field => field.path === path)?.options, path).toEqual(values)
  }
 })

 it('uses an editor type the catalog type can hold', () => {
  const compatible: Record<string, string[]> = {
   string: ['text', 'select'],
   int: ['number', 'signed'],
   float: ['number', 'signed'],
   boolean: ['boolean'],
   select: ['select'],
   string_list: ['lines'],
  }
  const mismatches = importRuntimeFields
   .filter(field => byPath.has(field.path))
   .filter(field => !(compatible[byPath.get(field.path)!.type] || []).includes(field.type))
   .map(field => `${field.path}: ${field.type} for ${byPath.get(field.path)!.type}`)
  expect(mismatches).toEqual([])
 })

 it('takes the served options and group over the offline ones once the field list arrives', () => {
  const served: RemoteImportRuntimeField[] = [
   { path: 'config.think_time.type', category: 'Think time', group: 'think_time', type: 'select', options: ['none', 'fixed'] },
   { path: 'config.http.user_agent', category: 'HTTP', group: 'target_identity', type: 'text' },
   { path: 'config.run_logic.termination_mode', category: 'Run logic', group: 'run_logic_pacing', type: 'select', options: ['', 'duration', 'iterations'] },
   { path: 'config.web_runtime.streaming.mode', category: 'HTTP', group: 'protocol_behavior_session', type: 'text' },
   { path: 'config.soap.namespaces', category: 'SOAP', group: 'protocol_behavior_session', type: 'object' },
  ]
  const merged = mergeImportRuntimeFields(served)
  expect(merged.find(field => field.path === 'config.think_time.type')?.options).toEqual(['none', 'fixed'])
  expect(merged.find(field => field.path === 'config.http.user_agent')?.group).toBe('Target & identity')
  expect(merged.find(field => field.path === 'config.run_logic.termination_mode')?.options).toEqual(['duration', 'iterations'])
  // A served string keeps the offline select of DSL-validated values.
  expect(merged.find(field => field.path === 'config.web_runtime.streaming.mode')).toMatchObject({ type: 'select', options: ['adaptive', 'fixed'] })
  expect(merged.find(field => field.path === 'config.soap.namespaces')).toBeUndefined()
 })
})
