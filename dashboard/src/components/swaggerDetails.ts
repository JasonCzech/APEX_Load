import { parse } from 'yaml'
import type { SwaggerDocument, SwaggerOperation } from './swaggerDocsParser'

export type Schema = {
  $ref?: string; type?: string; format?: string; description?: string
  properties?: Record<string, Schema>; items?: Schema; required?: string[]
  enum?: unknown[]; default?: unknown; example?: unknown; nullable?: boolean
  minimum?: number; maximum?: number; minLength?: number; maxLength?: number
  minItems?: number; maxItems?: number; pattern?: string
  readOnly?: boolean; writeOnly?: boolean; deprecated?: boolean
  additionalProperties?: boolean | Schema
  oneOf?: Schema[]; anyOf?: Schema[]; allOf?: Schema[]
}
export type Example = { summary?: string; description?: string; value?: unknown; externalValue?: string; $ref?: string }
export type Media = { schema?: Schema; example?: unknown; examples?: Record<string, Example> }
export type Parameter = { name: string; in: string; required?: boolean; description?: string; schema?: Schema; example?: unknown }
export type Response = { description?: string; content?: Record<string, Media>; headers?: Record<string, { description?: string; schema?: Schema }> }
export type SecurityScheme = { type?: string; scheme?: string; name?: string; in?: string; description?: string; bearerFormat?: string }
export type Security = Record<string, string[]>[]
export type OperationDetails = {
  parameters?: Parameter[]; requestBody?: { required?: boolean; description?: string; content?: Record<string, Media> }
  responses?: Record<string, Response>; security?: Security; deprecated?: boolean
  'x-server'?: string; 'x-conditional'?: string
}
export type Definitions = { security?: Security; components?: { schemas?: Record<string, Schema>; securitySchemes?: Record<string, SecurityScheme>; examples?: Record<string, Example> } }
const definitionsCache = new WeakMap<SwaggerDocument, Definitions>()
export function readDefinitions(document: SwaggerDocument): Definitions {
  let result = definitionsCache.get(document)
  if (!result) {
    result = parse(document.definitions, { maxAliasCount: 0 }) || {}
    definitionsCache.set(document, result!)
  }
  return result!
}
export function readOperation(operation: SwaggerOperation): OperationDetails {
  return parse(operation.source, { maxAliasCount: 0 }) || {}
}
export function resolveSchema(schema: Schema, definitions: Definitions): Schema {
  if (!schema.$ref) return schema
  const prefix = '#/components/schemas/'
  if (!schema.$ref.startsWith(prefix)) return schema
  const name = schema.$ref.slice(prefix.length).replace(/~1/g, '/').replace(/~0/g, '~')
  return definitions.components?.schemas?.[name] || schema
}
export function schemaType(schema: Schema): string {
  if (schema.$ref) return schema.$ref.split('/').slice(-1)[0] || 'reference'
  if (schema.oneOf) return 'one of'
  if (schema.anyOf) return 'any of'
  if (schema.allOf) return 'all of'
  if (schema.type === 'array') return `${schema.items ? schemaType(schema.items) : 'any'}[]`
  return schema.type || (schema.properties ? 'object' : 'any')
}
export function schemaConstraints(schema: Schema): string[] {
  const values: string[] = []
  if (schema.enum) values.push(`Allowed: ${schema.enum.map(value => JSON.stringify(value)).join(', ')}`)
  if (schema.default !== undefined) values.push(`Default: ${JSON.stringify(schema.default)}`)
  if (schema.format) values.push(`Format: ${schema.format}`)
  if (schema.nullable) values.push('Allows null')
  if (schema.readOnly) values.push('Read only')
  if (schema.writeOnly) values.push('Write only')
  if (schema.deprecated) values.push('Deprecated')
  for (const [key, label] of Object.entries({ minimum: 'Minimum', maximum: 'Maximum', minLength: 'Min length', maxLength: 'Max length', minItems: 'Min items', maxItems: 'Max items', pattern: 'Pattern' })) {
    const value = schema[key as keyof Schema]
    if (value !== undefined) values.push(`${label}: ${value}`)
  }
  return values
}

// Only explicit examples are copyable payloads. A schema's arbitrary values,
// unconstrained maps and union branches cannot promise a valid API request.
export function mediaExamples(media: Media, definitions: Definitions): { name: string; value?: unknown; description?: string }[] {
  if (Object.prototype.hasOwnProperty.call(media, 'example')) return [{ name: 'Example', value: media.example }]
  return Object.entries(media.examples || {}).map(([name, raw]) => {
    const prefix = '#/components/examples/'
    const example = raw.$ref?.startsWith(prefix) ? definitions.components?.examples?.[raw.$ref.slice(prefix.length)] : raw
    return { name: example?.summary || name, value: example?.value, description: example?.description || (example?.externalValue ? 'This example is stored externally. Download the OpenAPI document for its location.' : undefined) }
  })
}
export function authLabel(name: string, scheme?: SecurityScheme): string {
  if (!scheme) return `${name} (definition unavailable)`
  if (scheme.type === 'http') return `${scheme.bearerFormat || scheme.scheme || name} in Authorization header`
  return `${scheme.name || name} in ${scheme.in || 'request'}`
}
const shellQuote = (value: string) => `'${value.replace(/'/g, `'"'"'`)}'`
export function curlExample(operation: SwaggerOperation, details: OperationDetails, definitions: Definitions, origin: string, mediaType: string, payload?: unknown, securityIndex = 0): string {
  const security = details.security ?? definitions.security ?? []
  const requirement = security[securityIndex] || {}
  // Never embed the operator's session, credentials or page query string.
  const queries = (details.parameters || []).filter(parameter => parameter.in === 'query' && parameter.required).map(parameter => `${encodeURIComponent(parameter.name)}=<${parameter.name}>`)
  for (const name of Object.keys(requirement)) {
    const scheme = definitions.components?.securitySchemes?.[name]
    if (scheme?.type === 'apiKey' && scheme.in === 'query') queries.push(`${encodeURIComponent(scheme.name || name)}=<token>`)
  }
  const query = queries.length ? `?${queries.join('&')}` : ''
  const parts = [`curl --request ${operation.method} \\\n  --url ${shellQuote(origin.replace(/\/$/, '') + operation.path.replace(/\{([^}]+)\}/g, '<$1>') + query)}`]
  for (const [name] of Object.entries(requirement)) {
    const scheme = definitions.components?.securitySchemes?.[name]
    if (scheme?.type === 'http' && scheme.scheme === 'bearer') parts.push(`--header ${shellQuote('Authorization: Bearer <token>')}`)
    else if (scheme?.type === 'apiKey' && scheme.in === 'header') parts.push(`--header ${shellQuote(`${scheme.name}: <token>`)}`)
    else if (scheme?.type === 'apiKey' && scheme.in === 'cookie') parts.push(`--cookie ${shellQuote(`${scheme.name}=<session-cookie>`)}`)
  }
  for (const parameter of details.parameters || []) {
    if (parameter.in === 'header' && parameter.required) parts.push(`--header ${shellQuote(`${parameter.name}: <${parameter.name}>`)}`)
  }
  if (mediaType) {
    if (mediaType === 'multipart/form-data') parts.push('--form \'<field>=@<file-path>\'')
    else {
      parts.push(`--header ${shellQuote(`Content-Type: ${mediaType}`)}`)
      if (payload !== undefined && (mediaType === 'application/json' || mediaType.endsWith('+json'))) parts.push(`--data-raw ${shellQuote(JSON.stringify(payload, null, 2))}`)
      else parts.push('--data-binary \'@request-body\'')
    }
  }
  return parts.join(' \\\n  ')
}
