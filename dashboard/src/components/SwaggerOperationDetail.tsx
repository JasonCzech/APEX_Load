import { useEffect, useId, useMemo, useState } from 'react'
import { API_ORIGIN, API_ORIGIN_CHANGED_EVENT } from '../hooks/apiClient'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import type { SwaggerDocument, SwaggerOperation } from './swaggerDocsParser'
import { authLabel, curlExample, mediaExamples, readDefinitions, readOperation, resolveSchema, schemaConstraints, schemaType, type Definitions, type Media, type Schema } from './swaggerDetails'
import './SwaggerOperationDetail.css'

export function CopyableCode({ text, label }: { text: string; label: string }) {
  const [result, setResult] = useState<{ text: string; message: string } | null>(null)
  return <div className="swagger-code">
    <div className="swagger-code-toolbar"><span>{label}</span><button type="button" className="btn btn-secondary btn-sm" onClick={async () => {
      setResult(null)
      const copied = await copyTextToClipboard(text)
      setResult({ text, message: copied ? 'Copied to clipboard.' : 'Copy failed. Select and copy the text below.' })
    }}>Copy {label.toLowerCase()}</button></div>
    <pre tabIndex={0} role="region" aria-label={label}><code>{text}</code></pre>
    <span role="status">{result?.text === text ? result.message : ''}</span>
  </div>
}

function SchemaNode({ schema: raw, definitions, name = 'Body', required = false, depth = 0, seen = [] }: { schema: Schema; definitions: Definitions; name?: string; required?: boolean; depth?: number; seen?: string[] }) {
  const [expanded, setExpanded] = useState(depth === 0)
  const schema = resolveSchema(raw, definitions)
  const recursive = !!raw.$ref && seen.includes(raw.$ref)
  const nextSeen = raw.$ref ? [...seen, raw.$ref] : seen
  const properties = Object.entries(schema.properties || {})
  const variants = schema.oneOf || schema.anyOf || schema.allOf || []
  const hasChildren = properties.length > 0 || !!schema.items || variants.length > 0 || typeof schema.additionalProperties === 'object'
  const metadata = <><code>{name}</code><span className="swagger-field-type">{schemaType(raw)}</span>{required && <span className="swagger-required">Required</span>}</>
  const description = <>
    {schema.description && <p>{schema.description}</p>}
    {schemaConstraints(schema).length > 0 && <p className="swagger-field-notes">{schemaConstraints(schema).join(' · ')}</p>}
    {raw.$ref && schema === raw && <p>Referenced schema is unavailable: <code>{raw.$ref}</code>.</p>}
    {(schema.type === 'object' || schema.properties) && !properties.length && !schema.additionalProperties && <p>Object fields are not specified by this contract.</p>}
    {schema.additionalProperties === true && <p>Additional fields are allowed; their values are not constrained here.</p>}
    {schema.additionalProperties === false && <p>Additional fields are not allowed.</p>}
  </>
  if (recursive || depth >= 10) return <div className="swagger-schema-field">{metadata}<p>{recursive ? 'Recursive reference; see the same schema above.' : 'Further nesting is available in the OpenAPI download.'}</p></div>
  if (!hasChildren) return <div className="swagger-schema-field"><div className="swagger-field-heading">{metadata}</div>{description}</div>
  return <details className="swagger-schema-node" open={expanded} onToggle={event => { if (event.target === event.currentTarget) setExpanded(event.currentTarget.open) }}>
    <summary>{metadata}</summary>
    {expanded && <div className="swagger-schema-children">{description}
      {properties.map(([key, value]) => <SchemaNode key={key} schema={value} name={key} definitions={definitions} required={schema.required?.includes(key)} depth={depth + 1} seen={nextSeen} />)}
      {schema.items && <SchemaNode schema={schema.items} name="Array item" definitions={definitions} depth={depth + 1} seen={nextSeen} />}
      {variants.map((variant, index) => <SchemaNode key={index} schema={variant} name={`${schema.oneOf ? 'One of' : schema.anyOf ? 'Any of' : 'All of'} · ${index + 1}`} definitions={definitions} depth={depth + 1} seen={nextSeen} />)}
      {typeof schema.additionalProperties === 'object' && <SchemaNode schema={schema.additionalProperties} name="Additional field value" definitions={definitions} depth={depth + 1} seen={nextSeen} />}
    </div>}
  </details>
}

function MediaBody({ media, definitions, mediaType }: { media: Media; definitions: Definitions; mediaType: string }) {
  const examples = mediaExamples(media, definitions)
  const schemaExample = media.schema && resolveSchema(media.schema, definitions).example
  if (!examples.length && schemaExample !== undefined) examples.push({ name: 'Schema example', value: schemaExample })
  return <>
    {examples.length ? examples.map((example, index) => <div key={index}>
      {example.description && <p>{example.description}</p>}
      {example.value !== undefined && <CopyableCode label={example.name === 'Example' ? 'Example body' : example.name} text={typeof example.value === 'string' && !mediaType.includes('json') ? example.value : JSON.stringify(example.value, null, 2)} />}
    </div>) : <p className="swagger-contract-note">No example body is documented. Use the schema below to construct a payload.</p>}
    <div className="swagger-schema">{media.schema ? <SchemaNode schema={media.schema} definitions={definitions} /> : <p>No body schema is documented.</p>}</div>
  </>
}

export default function SwaggerOperationDetail({ operation, document }: { operation: SwaggerOperation; document: SwaggerDocument }) {
  const parsed = useMemo(() => {
    try { return { details: readOperation(operation), definitions: readDefinitions(document) } }
    catch { return null }
  }, [operation, document])
  if (!parsed) return <p role="alert">This endpoint’s contract could not be parsed. Download the OpenAPI document to inspect it.</p>
  return <OperationContent key={operation.operationId + operation.path} operation={operation} {...parsed} />
}

function OperationContent({ operation, details, definitions }: { operation: SwaggerOperation; details: ReturnType<typeof readOperation>; definitions: Definitions }) {
  const [apiOrigin, setAPIOrigin] = useState(API_ORIGIN)
  useEffect(() => {
    const update = () => setAPIOrigin(API_ORIGIN)
    window.addEventListener(API_ORIGIN_CHANGED_EVENT, update)
    return () => window.removeEventListener(API_ORIGIN_CHANGED_EVENT, update)
  }, [])
  const security = details.security ?? definitions.security ?? []
  const requestMedia = Object.entries(details.requestBody?.content || {})
  const responses = Object.entries(details.responses || {})
  const [requestType, setRequestType] = useState(requestMedia[0]?.[0] || '')
  const [responseStatus, setResponseStatus] = useState(responses.find(([status]) => /^2(?:[0-9]{2}|XX)$/i.test(status))?.[0] || responses[0]?.[0] || '')
  const [securityIndex, setSecurityIndex] = useState(0)
  const [requestExampleIndex, setRequestExampleIndex] = useState(0)
  const request = details.requestBody?.content?.[requestType]
  const requestExamples = request ? mediaExamples(request, definitions).filter(example => example.value !== undefined) : []
  const response = details.responses?.[responseStatus]
  const [responseType, setResponseType] = useState('')
  const responseMedia = operation.method === 'HEAD' || responseStatus === '204' ? [] : Object.entries(response?.content || {})
  const selectedResponseType = response?.content?.[responseType] ? responseType : responseMedia[0]?.[0] || ''
  const id = useId()
  const origin = details['x-server'] || operation.path === '/metrics' ? 'https://<server-address>' : apiOrigin
  const requestCode = curlExample(operation, details, definitions, origin, requestType, requestExamples[requestExampleIndex]?.value, securityIndex)
  return <div className="swagger-reference-detail">
    {details.deprecated && <p className="swagger-contract-note">Deprecated endpoint. Check the workflow guide for its replacement before integrating.</p>}
    {details['x-server'] && <p><strong>Server:</strong> {details['x-server']}</p>}
    {details['x-conditional'] && <p><strong>Availability:</strong> {details['x-conditional']}</p>}
    <section aria-labelledby={`${id}-auth`} className="swagger-reference-section">
      <h5 id={`${id}-auth`}>Authentication</h5>
      {!security.length ? <p>No authentication required by this operation.</p> : <>
        <label className="swagger-select-label">Credential option<select value={securityIndex} onChange={event => setSecurityIndex(Number(event.target.value))}>
          {security.map((requirement, index) => <option value={index} key={index}>{Object.keys(requirement).map(name => authLabel(name, definitions.components?.securitySchemes?.[name])).join(' + ') || 'No authentication'}</option>)}
        </select></label>
        {Object.keys(security[securityIndex] || {}).map(name => <p key={name}>{definitions.components?.securitySchemes?.[name]?.description || authLabel(name, definitions.components?.securitySchemes?.[name])}{security[securityIndex][name].length > 0 && ` · Scopes: ${security[securityIndex][name].join(', ')}`}</p>)}
      </>}
      <a href="#/docs?guide=general&article=authentication">API authentication</a>
    </section>

    <section aria-labelledby={`${id}-params`} className="swagger-reference-section">
      <h5 id={`${id}-params`}>Parameters</h5>
      {details.parameters?.length ? <div className="swagger-parameter-list">{details.parameters.map(parameter => <div key={`${parameter.in}-${parameter.name}`}>
        <div className="swagger-field-heading"><code>{parameter.name}</code><span>{parameter.in}</span><span className="swagger-field-type">{schemaType(parameter.schema || {})}</span><span>{parameter.required ? 'Required' : 'Optional'}</span></div>
        {parameter.description && <p>{parameter.description}</p>}
        <p className="swagger-field-notes">{schemaConstraints(parameter.schema || {}).join(' · ')}</p>
      </div>)}</div> : <p>No parameters are declared.</p>}
    </section>

    <section aria-labelledby={`${id}-request`} className="swagger-reference-section">
      <div className="swagger-section-heading"><h5 id={`${id}-request`}>Request body {details.requestBody ? details.requestBody.required ? '(required)' : '(optional)' : ''}</h5>
        {requestMedia.length > 0 && <label className="swagger-select-label">Request format<select value={requestType} onChange={event => { setRequestType(event.target.value); setRequestExampleIndex(0) }}>{requestMedia.map(([type]) => <option key={type}>{type}</option>)}</select></label>}
      </div>
      {details.requestBody?.description && <p>{details.requestBody.description}</p>}
      {request ? <MediaBody media={request} definitions={definitions} mediaType={requestType} /> : <p>No request body is declared for this operation.</p>}
      {requestExamples.length > 1 && <label className="swagger-select-label">Request example<select value={requestExampleIndex} onChange={event => setRequestExampleIndex(Number(event.target.value))}>{requestExamples.map((example, index) => <option value={index} key={index}>{example.name}</option>)}</select></label>}
      <details className="swagger-curl"><summary>Copyable request · cURL</summary>
        <p>Replace angle-bracket placeholders with your own values. {request && !requestExamples.length ? 'Save your payload in a file named request-body, or supply the multipart fields shown in the schema. ' : ''}Add query parameters from the list above as needed. Copying does not send a request.</p>
        <CopyableCode label="cURL request" text={requestCode} />
      </details>
    </section>

    <section aria-labelledby={`${id}-responses`} className="swagger-reference-section">
      <div className="swagger-section-heading"><h5 id={`${id}-responses`}>Responses</h5><label className="swagger-select-label">Response status<select value={responseStatus} onChange={event => setResponseStatus(event.target.value)}>{responses.map(([status, value]) => <option value={status} key={status}>{status} · {value.description || 'Response'}</option>)}</select></label></div>
      {response?.description && <p>{response.description}</p>}
      {responseMedia.length > 0 ? <>
        <label className="swagger-select-label">Response format<select value={selectedResponseType} onChange={event => setResponseType(event.target.value)}>{responseMedia.map(([type]) => <option key={type}>{type}</option>)}</select></label>
        <MediaBody media={response!.content![selectedResponseType]} definitions={definitions} mediaType={selectedResponseType} />
      </> : <p>{responseStatus === '204' || operation.method === 'HEAD' ? 'No response body.' : 'No response body schema or media type is documented for this status.'}</p>}
      {Object.entries(response?.headers || {}).length > 0 && <div className="swagger-response-headers"><h6>Response headers</h6>{Object.entries(response!.headers!).map(([name, header]) => <p key={name}><code>{name}</code> · {schemaType(header.schema || {})} — {header.description} {schemaConstraints(header.schema || {}).join(' · ')}</p>)}</div>}
    </section>
    <p className="swagger-contract-note">Examples are illustrative. Required markers and constraints reflect the published schema; additional validation rules may apply. Unspecified fields and responses remain handler-defined.</p>
  </div>
}
