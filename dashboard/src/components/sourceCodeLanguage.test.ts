import { describe, expect, it } from 'vitest'

import { detectSourceCodeLanguage } from './sourceCodeLanguage'

describe('detectSourceCodeLanguage', () => {
  it.each([
    ['request.json', 'not JSON', 'json'],
    ['request.xml', 'not XML', 'xml'],
    ['request.yml', '<embedded>XML</embedded>', 'yaml'],
    ['request.yaml', '{}', 'yaml'],
    ['request.txt', '  <Envelope />', 'xml'],
    ['request.txt', '  {"ok": true}', 'json'],
    ['request.txt', 'http:\n  method: GET', 'yaml'],
  ] as const)('detects %s as %s source', (path, source, expected) => {
    expect(detectSourceCodeLanguage(path, source)).toBe(expected)
  })
})
