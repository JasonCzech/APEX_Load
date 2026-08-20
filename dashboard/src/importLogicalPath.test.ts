import { describe, expect, it } from 'vitest'

import { importFileLogicalPath, normalizeImportLogicalPath } from './importLogicalPath'

describe('import logical paths', () => {
  it('normalizes Windows collection separators without host path semantics', () => {
    expect(normalizeImportLogicalPath('CTA_Collection\\CTA\\request.yml')).toBe('CTA_Collection/CTA/request.yml')
  })

  it.each(['C:\\secret\\request.yml', '\\\\server\\share\\request.yml', '../request.yml'])(
    'rejects host or traversal path %s',
    value => expect(() => normalizeImportLogicalPath(value)).toThrow(),
  )

  it('uses a defensive basename for a standalone native file', () => {
    const file = new File(['{}'], 'C:\\imports\\collection.json')
    expect(importFileLogicalPath(file)).toBe('collection.json')
  })
})
