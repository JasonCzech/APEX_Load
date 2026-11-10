import { describe, expect, it } from 'vitest'

import { evaluateExtractor } from './extractorMatch'
import { translateGoRegex } from './goRegex'

// The live-app expression that failed in the tester with "Invalid group".
const CSHC = `(?i)(?:^|[?&{,\\"\\s]|&amp;)cshc[\\"']?\\s*[:=]\\s*[\\"']?([^\\"&',}<\\s]+)`

function compile(expr: string): RegExp {
  const translated = translateGoRegex(expr)
  if (translated.kind !== 'ok') throw new Error(`${translated.kind}: ${translated.reason}`)
  return new RegExp(translated.source, translated.flags)
}

describe('translateGoRegex', () => {
  it('turns a leading (?i) into the JS i flag', () => {
    const translated = translateGoRegex('(?i)token=(\\w+)')
    expect(translated).toMatchObject({ kind: 'ok', flags: 'i' })
    expect(compile('(?i)token=(\\w+)').exec('TOKEN=abc')?.[1]).toBe('abc')
  })

  it('evaluates the Salesforce cshc extractor as the engine does', () => {
    const body = 'var x = {"CSHC":"Zx9-AbC_1", "other": 1}'
    const result = evaluateExtractor('regex', CSHC, body)
    expect(result.error).toBeUndefined()
    expect(result.previewable).toBe(true)
    expect(result.matches).toEqual(['Zx9-AbC_1'])
    expect(evaluateExtractor('regex', CSHC, '/s?a=1&amp;cshc=qq42&b=2').matches).toEqual(['qq42'])
  })

  it('keeps the engine defaults: case-sensitive, "." stops only at \\n, ^/$ at the text ends', () => {
    expect(compile('abc').test('ABC')).toBe(false)
    // JS "." also stops at \r; Go's does not.
    expect(compile('a.b').test('a\rb')).toBe(true)
    expect(compile('a.b').test('a\nb')).toBe(false)
    expect(compile('^b').test('a\nb')).toBe(false)
    expect(compile('a$').test('a\nb')).toBe(false)
  })

  it('applies (?s) and (?m) the Go way', () => {
    expect(compile('(?s)a.b').test('a\nb')).toBe(true)
    expect(compile('(?m)^b$').test('a\nb\nc')).toBe(true)
    // Go's multi-line ^ follows \n only, not \r.
    expect(compile('(?m)^b').test('a\rb')).toBe(false)
    expect(compile('(?is)A.B').test('a\nb')).toBe(true)
    expect(compile('(?i-s)A.B').test('a\nb')).toBe(false)
  })

  it('scopes (?flags:...) groups', () => {
    expect(compile('(?i:abc)').test('ABC')).toBe(true)
    expect(compile('x(?s:.)y').test('x\ny')).toBe(true)
    expect(compile('x(?s:.).').test('x\n\n')).toBe(false)
  })

  it('reads \\s as Go does (ASCII white space only)', () => {
    expect(compile('a\\sb').test('a b')).toBe(false)
    expect(compile('a\\sb').test('a\tb')).toBe(true)
    expect(compile('a[\\s]b').test('a b')).toBe(false)
    expect(compile('a[^\\S]b').test('a b')).toBe(true)
    expect(compile('a\\Sb').test('a b')).toBe(true)
  })

  it('rewrites Go-only syntax JS can express', () => {
    expect(compile('(?P<id>\\d+)').exec('n=42')?.groups?.id).toBe('42')
    expect(compile('\\Qa.b(\\E+').test('a.b((')).toBe(true)
    expect(compile('\\Qa.b\\E').test('axb')).toBe(false)
    expect(compile('\\Aab\\z').test('ab')).toBe(true)
    expect(compile('\\Aab\\z').test('ab\n')).toBe(false)
    expect(compile('\\x{41}').test('A')).toBe(true)
    expect(compile('[]a]+').exec(']a]')?.[0]).toBe(']a]')
  })

  it('reports syntax Go rejects as invalid, as the replay would', () => {
    expect(translateGoRegex('a(?=b)')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('(?<!a)b')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('(a)\\1')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('\\y')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('(?x)a')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('(a')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('a\\E')).toMatchObject({ kind: 'invalid' })
    expect(translateGoRegex('[\\b]')).toMatchObject({ kind: 'invalid' })
    // An empty flag group is a no-op for Go.
    expect(translateGoRegex('(?)a')).toMatchObject({ kind: 'ok', source: 'a' })
    expect(evaluateExtractor('regex', 'a(?=b)', 'ab').error).toMatch(/invalid regular expression for the engine/i)
  })

  it('names Go syntax the browser cannot test instead of calling it invalid', () => {
    for (const expr of ['\\pL+', '[[:alpha:]]+', '(?U)a+', 'a(?i)b', '((?i)a)b']) {
      expect(translateGoRegex(expr)).toMatchObject({ kind: 'untestable' })
    }
    const result = evaluateExtractor('regex', '\\p{Greek}+', 'αβγ')
    expect(result.previewable).toBe(false)
    expect(result.error).toBeUndefined()
    expect(result.note).toMatch(/uses Go regex syntax the browser can’t test/)
  })

  it('accepts (?i) that covers every matching part', () => {
    expect(compile('^(?i)abc$').test('ABC')).toBe(true)
    expect(translateGoRegex('abc(?i)')).toMatchObject({ kind: 'ok', flags: '' })
  })
})
