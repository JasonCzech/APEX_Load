import { expect, it } from 'vitest'
import { analysisProtectedIdentityColumn } from './analysisCredentialColumn'

it('refuses the authoritative credential vocabulary in structured and percent-encoded identity paths', () => {
  for (const path of [
    ['accesskey'], ['access_key_id'], ['ack-token'], ['api_key'], ['api_token'], ['apitoken'], ['%61pi%74oken'], ['Authentication'], ['connection_string'], ['cookie'], ['credentials', 'client_secret'],
    ['csrf_token'], ['deliverytag'], ['dsn'], ['id_token'], ['jms_settlement_token'], ['lock_token'], ['PASSWORD'], ['passwd'], ['private_key'],
    ['proxy-authorization'], ['receipt_handle'], ['refresh_token'], ['sas_signature'], ['secret_access_key'], ['shared_access_signature'],
    ['session_id'], ['sid'], ['storage_state'], ['settlement_token'], ['signature'], ['token'], ['xsrf_token'],
    ['api', 'key'], ['client', 'secret'], ['safe', 'metadata[token]'], ['safe', 'access+token'], ['%70assword'], ['%2570assword'],
    ['%2525252570assword'], ['%70%61%73%73%77%6f%72%64%ff'], ['\u0085token\u0085'], ['\ufefftoken'],
  ]) expect(analysisProtectedIdentityColumn(path), JSON.stringify(path)).toBe(true)
})

it('retains exact legitimate analytical labels and noncredential substrings', () => {
  for (const path of [['source'], ['hostname'], ['service'], ['method'], ['partition'], ['monkey'], ['tokenization'], ['secretariat'], ['passwordless'], ['session_count'], ['密password'], ['a/b~c'], ['__proto__'], ['constructor'], ['safe%zz']]) {
    // session_count contains an exact protected component and intentionally
    // remains protected, whereas sessioncount is a distinct whole name.
    const expected = path[0] === 'session_count'
    expect(analysisProtectedIdentityColumn(path), JSON.stringify(path)).toBe(expected)
  }
  expect(analysisProtectedIdentityColumn(['sessioncount'])).toBe(false)
})

it('admits finite path geometry before percent decoding or allocation', () => {
  for (const path of [[], Array(17).fill('safe'), ['x'.repeat(4 * 1024 * 1024)], ['😀'.repeat(65)], ['\ud800'], ['a\0b']]) expect(analysisProtectedIdentityColumn(path)).toBe(true)
})

it('does not invoke caller iteration or numeric accessors while admitting path geometry', () => {
  let calls = 0
  const iterable = ['safe']
  Object.defineProperty(iterable,Symbol.iterator,{value:()=>{calls++;throw new Error('unbounded iterator')}})
  Object.defineProperty(iterable,'some',{get:()=>{calls++;throw new Error('method')}})
  Object.defineProperty(iterable,'join',{get:()=>{calls++;throw new Error('method')}})
  expect(analysisProtectedIdentityColumn(iterable)).toBe(false)
  const getter = ['safe']
  Object.defineProperty(getter,'0',{get:()=>{calls++;throw new Error('numeric getter')}})
  expect(analysisProtectedIdentityColumn(getter)).toBe(true)
  expect(calls).toBe(0)
})

it('uses the once-admitted own length descriptor rather than mutable array length reads', () => {
  let lengthReads=0
  const path=new Proxy(['safe'],{get(target,key,receiver){if(key==='length'){lengthReads++;return 1000000}return Reflect.get(target,key,receiver)}})
  expect(analysisProtectedIdentityColumn(path)).toBe(false)
  expect(lengthReads).toBe(0)
})
