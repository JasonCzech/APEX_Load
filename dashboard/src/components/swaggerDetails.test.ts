import { describe, expect, it } from 'vitest'
import { parseSwaggerDocument } from './swaggerDocsParser'
import { authLabel, curlExample, mediaExamples, readDefinitions, readOperation, resolveSchema, schemaConstraints, schemaType, type Definitions } from './swaggerDetails'

import { referenceFixture as source } from './swaggerDetails.fixture'


describe('OpenAPI operation details', () => {
  it('parses nested bodies, inline YAML, required flags, refs, headers and responses on demand', () => {
    const document = parseSwaggerDocument(source)
    const operation = readOperation(document.operations[0])
    const definitions = readDefinitions(document)
    expect(readDefinitions(document)).toBe(definitions)
    expect(operation.requestBody?.content?.['application/json'].schema?.properties?.nested.items?.$ref).toContain('/Node')
    expect(operation.responses?.['201'].content?.['application/json'].example).toEqual({ id: 'widget-1' })
    expect(resolveSchema(operation.responses!['400'].content!['application/json'].schema!, definitions).required).toEqual(['error'])
    expect(operation.parameters?.[2]).toMatchObject({ name: 'If-Match', required: true, description: 'Exact revision' })
    expect(schemaType({ type: 'array', items: { type: 'integer' } })).toBe('integer[]')
    expect(schemaType({ oneOf: [{ type: 'string' }, { type: 'integer' }] })).toBe('one of')
    expect(schemaConstraints({ enum: ['active'], default: false, minimum: 0, maxItems: 3, nullable: true })).toEqual(['Allowed: "active"', 'Default: false', 'Allows null', 'Minimum: 0', 'Max items: 3'])
  })

  it('uses explicit falsy examples and local example refs without fetching external content or inventing values', () => {
    const definitions = readDefinitions(parseSwaggerDocument(source))
    expect(mediaExamples({ example: null }, definitions)[0].value).toBeNull()
    expect(mediaExamples({ examples: { local: { $ref: '#/components/examples/falseValue' }, remote: { externalValue: 'https://example.com/private' } } }, definitions)).toEqual([{ name: 'Disabled', value: false, description: undefined }, { name: 'remote', value: undefined, description: 'This example is stored externally. Download the OpenAPI document for its location.' }])
    expect(mediaExamples({ schema: { type: 'object', additionalProperties: true } }, definitions)).toEqual([])
    expect(resolveSchema({ $ref: 'https://example.com/schema' }, definitions)).toEqual({ $ref: 'https://example.com/schema' })
    expect(readOperation(parseSwaggerDocument(source).operations[0]).security).toBeUndefined()
  })

  it('builds shell-quoted requests with placeholders, required query/header params and the selected auth alternative', () => {
    const document = parseSwaggerDocument(source)
    const operation = document.operations[0]
    const details = readOperation(operation)
    const definitions = readDefinitions(document)
    const payload = details.requestBody?.content?.['application/json'].example
    const command = curlExample(operation, details, definitions, 'https://apex.example/', 'application/json', payload)
    expect(command).toContain("--url 'https://apex.example/api/v1/widgets/<id>?revision=<revision>'")
    expect(command).toContain("--header 'If-Match: <If-Match>'")
    expect(command).toContain("--header 'Authorization: Bearer <token>'")
    expect(command).toContain('O\'"\'"\'Brien')
    expect(command).not.toContain('document.cookie')
    expect(curlExample(operation, { ...details, security: [] }, definitions, 'https://apex.example', '')).not.toContain('Authorization')
    expect(curlExample(operation, { ...details, security: [{ collector: [] }] }, definitions, 'https://apex.example', 'application/octet-stream')).toContain("--header 'X-Collector-Token: <token>'")
    expect(curlExample(operation, details, definitions, 'https://apex.example', 'multipart/form-data')).toContain("--form '<field>=@<file-path>'")
    expect(curlExample(operation, details, definitions, 'https://apex.example', 'application/json')).toContain("--data-binary '@request-body'")
  })

  it('preserves AND requirements, cookies, query auth and public alternatives', () => {
    const document = parseSwaggerDocument(source)
    const definitions: Definitions = { components: { securitySchemes: { cookie: { type: 'apiKey', in: 'cookie', name: 'session' }, query: { type: 'apiKey', in: 'query', name: 'key' } } } }
    expect(authLabel('missing')).toContain('definition unavailable')
    const command = curlExample(document.operations[0], { security: [{ cookie: [], query: [] }, {}] }, definitions, 'https://example.com', '')
    expect(command).toContain('?key=<token>')
    expect(command).toContain("--cookie 'session=<session-cookie>'")
    expect(curlExample(document.operations[0], { security: [{}] }, definitions, 'https://example.com', '')).not.toContain('--header')
  })
})
