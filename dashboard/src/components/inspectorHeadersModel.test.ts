import { describe, expect, it } from 'vitest'
import type { ScriptActionDefinition } from '../types/scripts'
import {
  MASKED_VALUE,
  authoredValueSource,
  inspectorFormFieldRows,
  inspectorHeaderRows,
  inspectorKeyHeaderRows,
  isFormUrlEncoded,
  isPasswordLikeFieldName,
  requestBodyIsForm,
  withRequestHeaderSources,
  type InspectorSourceContext,
} from './inspectorHeadersModel'

const action = (overrides: Partial<ScriptActionDefinition> = {}): ScriptActionDefinition => ({ name: 'Login', type: 'POST', target: '/login', ...overrides } as ScriptActionDefinition)

describe('inspectorHeaderRows', () => {
  it('puts key response headers first in a fixed order and keeps the rest in order', () => {
    const rows = inspectorHeaderRows({ server: 'sfdcedge', 'Cache-Control': 'no-store', vary: 'Accept-Encoding', 'Content-Type': 'text/html' }, 'response')
    expect(rows.map(row => [row.name, row.key])).toEqual([
      ['Content-Type', true], ['Cache-Control', true], ['server', false], ['vary', false],
    ])
  })

  it('keeps each Set-Cookie occurrence the engine joined with newlines as its own row', () => {
    const rows = inspectorHeaderRows({ 'Set-Cookie': 'rememberUn=false; secure\nlogin=; Max-Age=0' }, 'response')
    expect(rows.map(row => row.value)).toEqual(['rememberUn=false; secure', 'login=; Max-Age=0'])
  })

  it('uses the request key set for request headers', () => {
    const rows = inspectorHeaderRows({ accept: '*/*', Cookie: 'sid=1', 'Content-Type': 'application/json' }, 'request')
    expect(rows.map(row => row.name)).toEqual(['Content-Type', 'Cookie', 'accept'])
  })

  it('returns nothing for missing headers', () => {
    expect(inspectorHeaderRows(undefined, 'request')).toEqual([])
    expect(inspectorHeaderRows({}, 'response')).toEqual([])
  })

  it('keeps an engine-redacted value as it came', () => {
    expect(inspectorHeaderRows({ Authorization: '[REDACTED]' }, 'request')[0].value).toBe('[REDACTED]')
  })
})

describe('inspectorKeyHeaderRows', () => {
  it('offers the key subset only when some headers are not key headers', () => {
    expect(inspectorKeyHeaderRows(inspectorHeaderRows({ 'Content-Type': 'a', server: 'b' }, 'response'))?.map(row => row.name)).toEqual(['Content-Type'])
    expect(inspectorKeyHeaderRows(inspectorHeaderRows({ 'Content-Type': 'a' }, 'response'))).toBeNull()
    expect(inspectorKeyHeaderRows(inspectorHeaderRows({ server: 'b' }, 'response'))).toBeNull()
  })
})

describe('authoredValueSource', () => {
  it('names the variable, parameter or environment value a template reads', () => {
    expect(authoredValueSource('Bearer {{var.token}}')).toMatchObject({ kind: 'variable', label: 'Variable token' })
    expect(authoredValueSource('{{param.p_username}}')).toMatchObject({ kind: 'parameter', label: 'Parameter p_username' })
    expect(authoredValueSource('{{data.user}}')).toMatchObject({ kind: 'parameter', label: 'Parameter user' })
    expect(authoredValueSource('{{env.HOST}}')).toMatchObject({ kind: 'environment', label: 'Environment HOST' })
  })

  it('says a variable never set this run is never set', () => {
    expect(authoredValueSource('{{var.c_interactionid}}', ['c_interactionid'])).toMatchObject({ kind: 'unset', label: 'Never set' })
    expect(authoredValueSource('{{var.other}}', ['c_interactionid'])).toMatchObject({ kind: 'variable' })
  })

  it('prefers a secret over any other reference in the value', () => {
    expect(authoredValueSource('{{var.user}}:{{ secret.API_KEY }}', ['user'])).toMatchObject({ kind: 'secret', label: 'Secret' })
  })

  it('falls back to the plain source, and to Template for other templates', () => {
    expect(authoredValueSource('application/json')).toEqual({ kind: 'literal', label: 'Literal' })
    expect(authoredValueSource('x', [], { kind: 'default', label: 'Script default' })).toEqual({ kind: 'default', label: 'Script default' })
    expect(authoredValueSource('{{uuid}}')).toMatchObject({ kind: 'template', label: 'Template' })
  })
})

describe('withRequestHeaderSources', () => {
  const wire = inspectorHeaderRows({
    'Content-Type': 'application/x-www-form-urlencoded',
    Cookie: 'sid=•••',
    Authorization: 'Bearer abc',
    'X-Trace': 'uuid-1',
    'Accept-Encoding': 'gzip',
    'User-Agent': 'apex',
    'X-Api-Key': '[REDACTED]',
    'X-Csrf': '{{var.csrf}}',
  }, 'request')

  const context = (overrides: Partial<InspectorSourceContext> = {}): InspectorSourceContext => ({
    view: 'wire',
    authored: {
      action: action({
        headers: { 'content-type': 'application/x-www-form-urlencoded', Authorization: 'Bearer {{var.token}}', 'X-Csrf': '{{var.csrf}}' },
        header_list: [{ name: 'X-Api-Key', value: '{{secret.API_KEY}}' }, { name: 'X-Disabled', value: 'x', enabled: false }],
      }),
      http: { default_headers: { 'X-Trace': '{{uuid}}', 'Accept-Encoding': 'br' }, user_agent: 'apex' },
    },
    unresolvedVariables: ['csrf'],
    ...overrides,
  })

  it('derives every source from the authored step, script defaults and runtime settings', () => {
    const sources = Object.fromEntries(withRequestHeaderSources(wire, context()).map(row => [row.name, row.source?.label]))
    expect(sources).toEqual({
      'Content-Type': 'Literal',
      Cookie: 'Cookie jar',
      Authorization: 'Variable token',
      'Accept-Encoding': 'Script default',
      'X-Trace': 'Template',
      'User-Agent': 'Runtime setting',
      'X-Api-Key': 'Secret',
      'X-Csrf': 'Never set',
    })
  })

  it('masks a secret-sourced header and never hands its value on', () => {
    const row = withRequestHeaderSources(wire, context()).find(entry => entry.name === 'X-Api-Key')
    expect(row).toMatchObject({ masked: true, value: MASKED_VALUE })
  })

  it('says a wire header nothing authored was added at runtime, and Unknown in the scripted view', () => {
    const rows = inspectorHeaderRows({ 'X-Client': '1' }, 'request')
    expect(withRequestHeaderSources(rows, context())[0].source).toEqual({ kind: 'added', label: 'Added at runtime' })
    expect(withRequestHeaderSources(rows, context({ view: 'scripted' }))[0].source).toEqual({ kind: 'unknown', label: 'Unknown' })
  })

  it('ignores a disabled header-list entry', () => {
    const rows = inspectorHeaderRows({ 'X-Disabled': 'x' }, 'request')
    expect(withRequestHeaderSources(rows, context())[0].source?.kind).toBe('added')
  })

  it('gives no row a source, not even Cookie, when the authored step is unavailable', () => {
    const rows = withRequestHeaderSources(wire, { view: 'wire', authored: null })
    expect(rows.map(row => row.source)).toEqual(wire.map(() => undefined))
    expect(withRequestHeaderSources(wire, { view: 'wire' }).find(row => row.name === 'Cookie')?.source).toBeUndefined()
    // Values are left as they came (nothing is revealed or newly hidden).
    expect(rows.map(row => row.value)).toEqual(wire.map(row => row.value))
  })

  it('says an authored Cookie is the script plus the cookie jar', () => {
    const rows = inspectorHeaderRows({ Cookie: 'a=1; jar=2' }, 'request')
    expect(withRequestHeaderSources(rows, { view: 'wire', authored: { action: action({ headers: { Cookie: 'a={{var.a}}' } }) } })[0].source)
      .toEqual({ kind: 'script-jar', label: 'Script + cookie jar', detail: '{{var.a}}' })
    expect(withRequestHeaderSources(rows, { view: 'wire', authored: { action: action({ headers: { Cookie: 'a=1' } }) } })[0].source?.label).toBe('Script + cookie jar')
  })

  it('masks a merged Cookie when any authored Cookie reads a secret', () => {
    // The client merges every authored Cookie into one header and the jar appends to it.
    const rows = inspectorHeaderRows({ Cookie: 'a=1; sid=live-sid; jar=x' }, 'request')
    const authored = { action: action({ header_list: [{ name: 'Cookie', value: 'a=1' }, { name: 'Cookie', value: 'sid={{secret.SID}}' }] }) }
    const [row] = withRequestHeaderSources(rows, { view: 'wire', authored })
    expect(row).toMatchObject({ masked: true, value: MASKED_VALUE, source: { kind: 'secret', label: 'Secret' } })
    expect(JSON.stringify(row)).not.toContain('live-sid')
    // A script default Cookie with a secret masks it too.
    const viaDefault = withRequestHeaderSources(rows, { view: 'wire', authored: { action: action({ headers: { Cookie: 'a=1' } }), http: { default_headers: { Cookie: 'sid={{secret.SID}}' } } } })[0]
    expect(viaDefault).toMatchObject({ masked: true, value: MASKED_VALUE })
  })

  it('judges a header sent fewer times than authored over every authored value', () => {
    const rows = inspectorHeaderRows({ 'X-Multi': 'one, live-token' }, 'request')
    const authored = { action: action({ header_list: [{ name: 'X-Multi', value: 'one' }, { name: 'X-Multi', value: '{{secret.TOKEN}}' }] }) }
    expect(withRequestHeaderSources(rows, { view: 'wire', authored })[0]).toMatchObject({ masked: true, value: MASKED_VALUE, source: { kind: 'secret' } })
    // A step header that shadows a secret default but is sent once still masks.
    const shadow = withRequestHeaderSources(inspectorHeaderRows({ 'X-Key': 'step-value' }, 'request'), {
      view: 'wire',
      authored: { action: action({ headers: { 'X-Key': 'step-value' } }), http: { default_headers: { 'X-Key': '{{secret.KEY}}' } } },
    })[0]
    expect(shadow).toMatchObject({ masked: true, value: MASKED_VALUE })
  })

  it('matches repeated authored headers by occurrence', () => {
    const rows = inspectorHeaderRows({ 'X-Multi': 'one\ntwo' }, 'request')
    const authored = { action: action({ header_list: [{ name: 'X-Multi', value: 'one' }, { name: 'x-multi', value: '{{var.two}}' }] }) }
    expect(withRequestHeaderSources(rows, { view: 'wire', authored }).map(row => row.source?.label)).toEqual(['Literal', 'Variable two'])
  })
})

describe('isPasswordLikeFieldName', () => {
  it('matches password-like names case-insensitively', () => {
    for (const name of ['pw', 'PW', 'password', 'Passwd', 'pwd', 'client_secret', 'refresh_token', 'secret', 'token']) {
      expect(isPasswordLikeFieldName(name), name).toBe(true)
    }
  })

  it('leaves ordinary field names alone', () => {
    for (const name of ['username', 'un', 'startURL', 'display', 'lt', 'loginType']) {
      expect(isPasswordLikeFieldName(name), name).toBe(false)
    }
  })
})

describe('isFormUrlEncoded', () => {
  it('reads the Content-Type case-insensitively, with parameters', () => {
    expect(isFormUrlEncoded({ 'content-type': 'application/x-www-form-urlencoded; charset=UTF-8' })).toBe(true)
    expect(isFormUrlEncoded({ 'Content-Type': 'Application/X-WWW-Form-Urlencoded' })).toBe(true)
    expect(isFormUrlEncoded({ 'Content-Type': 'application/json' })).toBe(false)
    expect(isFormUrlEncoded(undefined)).toBe(false)
  })
})

describe('inspectorFormFieldRows', () => {
  const authored = {
    action: action({ body: 'username={{param.p_username}}&pw={{secret.SF_PASSWORD}}&startURL=%2Fsetup&recordId={{var.c_id}}&display=touch' }),
  }

  it('decodes fields and derives each source from the authored body', () => {
    const rows = inspectorFormFieldRows('username=jane%40example.test&pw=hunter2&startURL=%2Fsetup&recordId=%7B%7Bvar.c_id%7D%7D&display=touch&extra=1', true, {
      view: 'wire', authored, unresolvedVariables: ['c_id'],
    })
    expect(rows?.map(row => [row.name, row.value, row.source.label])).toEqual([
      ['username', 'jane@example.test', 'Parameter p_username'],
      ['pw', MASKED_VALUE, 'Secret'],
      ['startURL', '/setup', 'Literal'],
      ['recordId', '{{var.c_id}}', 'Never set'],
      ['display', 'touch', 'Literal'],
      ['extra', '1', 'Unknown'],
    ])
  })

  it('masks password-like fields even when nothing authored them, and never returns the value', () => {
    const rows = inspectorFormFieldRows('user=a&Password=hunter2&client_secret=s3cr3t', true, { view: 'wire' })
    expect(rows?.filter(row => row.masked).map(row => row.name)).toEqual(['Password', 'client_secret'])
    expect(JSON.stringify(rows)).not.toContain('hunter2')
    expect(JSON.stringify(rows)).not.toContain('s3cr3t')
    expect(rows?.every(row => row.source.kind === 'unknown')).toBe(true)
  })

  it('masks a secret-sourced field whatever its name', () => {
    const rows = inspectorFormFieldRows('code=abc', true, { view: 'wire', authored: { action: action({ body: 'code={{secret.CODE}}' }) } })
    expect(rows?.[0]).toMatchObject({ masked: true, value: MASKED_VALUE, source: { kind: 'secret' } })
  })

  it('keeps plus as a space and a malformed escape as written', () => {
    const rows = inspectorFormFieldRows('q=a+b&bad=%E0%A4%A', true, { view: 'scripted' })
    expect(rows?.map(row => row.value)).toEqual(['a b', '%E0%A4%A'])
  })

  it('is null for a body that is not a form, or empty', () => {
    expect(inspectorFormFieldRows('{"a":1}', false, { view: 'wire' })).toBeNull()
    expect(inspectorFormFieldRows('', true, { view: 'wire' })).toBeNull()
    expect(inspectorFormFieldRows('a=1', false, { view: 'wire' })).toBeNull()
  })

  it('says Unknown when the authored body is a file, multipart or encoded', () => {
    const sources = (overrides: Partial<ScriptActionDefinition>) => inspectorFormFieldRows('a=1', true, { view: 'wire', authored: { action: action({ body: 'a=1', ...overrides }) } })?.[0].source.kind
    expect(sources({ body_file: 'form.txt' })).toBe('unknown')
    expect(sources({ body_encoding: 'base64' })).toBe('unknown')
    expect(sources({})).toBe('literal')
  })
})

describe('requestBodyIsForm', () => {
  const form = { 'Content-Type': 'application/x-www-form-urlencoded; charset=UTF-8' }
  it('is a form when the wire or the resolved step headers say so', () => {
    expect(requestBodyIsForm({ wireHeaders: form })).toBe(true)
    expect(requestBodyIsForm({ scriptedHeaders: { 'content-type': 'application/x-www-form-urlencoded' } })).toBe(true)
    expect(requestBodyIsForm({ wireHeaders: { 'Content-Type': 'application/json' }, scriptedHeaders: {} })).toBe(false)
  })

  it('reads a script default Content-Type the resolved step headers never carry', () => {
    const authored = { action: action({ body: 'un=jane&pw=hunter2' }), http: { default_headers: { 'Content-Type': 'application/x-www-form-urlencoded' } } }
    expect(requestBodyIsForm({ scriptedHeaders: {}, authored })).toBe(true)
    // Then the scripted rows mask the password exactly as the wire rows do.
    const rows = inspectorFormFieldRows('un=jane&pw=hunter2', requestBodyIsForm({ scriptedHeaders: {}, authored }), { view: 'scripted', authored })
    expect(rows?.map(row => [row.name, row.value, row.masked])).toEqual([['un', 'jane', false], ['pw', MASKED_VALUE, true]])
    expect(JSON.stringify(rows)).not.toContain('hunter2')
  })

  it('reads a header-list Content-Type on the step and skips a disabled one', () => {
    expect(requestBodyIsForm({ authored: { action: action({ header_list: [{ name: 'Content-Type', value: 'application/x-www-form-urlencoded' }] }) } })).toBe(true)
    expect(requestBodyIsForm({ authored: { action: action({ header_list: [{ name: 'Content-Type', value: 'application/x-www-form-urlencoded', enabled: false }] }) } })).toBe(false)
    expect(requestBodyIsForm({ authored: { action: action({ headers: { 'Content-Type': 'text/plain' } }) } })).toBe(false)
  })

  it('lets the first source naming a Content-Type decide: wire, resolved step, step, then default', () => {
    const json = { 'Content-Type': 'application/json' }
    const form = { 'Content-Type': 'application/x-www-form-urlencoded' }
    // A step's JSON overrides the script's form default.
    const overridden = { action: action({ headers: json }), http: { default_headers: form } }
    expect(requestBodyIsForm({ scriptedHeaders: {}, authored: overridden })).toBe(false)
    expect(requestBodyIsForm({ authored: { action: action({ header_list: [{ name: 'content-type', value: 'application/json' }] }), http: { default_headers: form } } })).toBe(false)
    // What was sent wins over anything authored, either way.
    expect(requestBodyIsForm({ wireHeaders: json, authored: { action: action({ headers: form }) } })).toBe(false)
    expect(requestBodyIsForm({ wireHeaders: form, authored: overridden })).toBe(true)
    // The resolved step headers win over the authored ones.
    expect(requestBodyIsForm({ wireHeaders: { Accept: '*/*' }, scriptedHeaders: json, authored: { action: action(), http: { default_headers: form } } })).toBe(false)
    // A wire capture without a Content-Type falls through to the next source.
    expect(requestBodyIsForm({ wireHeaders: { Accept: '*/*' }, scriptedHeaders: {}, authored: { action: action(), http: { default_headers: form } } })).toBe(true)
  })
})
