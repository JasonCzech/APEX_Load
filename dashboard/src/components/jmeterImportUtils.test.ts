import { describe, expect, it } from 'vitest'

import { normalizeImportResult } from './jmeterImportUtils'

describe('jmeterImportUtils', () => {
  it('normalizes nullable import response arrays for validation previews', () => {
    const result = normalizeImportResult({
      scripts: [{ name: 'Smoke_Users' }],
      warnings: null,
      unsupported_elements: null,
      assumptions: ['flattened'],
      stats: {
        thread_groups: 1,
        samplers: 1,
        converted_actions: 1,
        headers: 0,
        variables: 0,
        csv_sources: 0,
        assertions: 0,
        timers: 0,
        unsupported: 0,
      },
      summary: { file_name: 'smoke.jmx' },
    })

    expect(result.scripts).toHaveLength(1)
    expect(result.warnings).toEqual([])
    expect(result.unsupported_elements).toEqual([])
    expect(result.assumptions).toEqual(['flattened'])
    expect(result.summary).toEqual({ file_name: 'smoke.jmx' })
  })

  it('provides default stats and summary when omitted', () => {
    const result = normalizeImportResult({})

    expect(result.stats).toEqual({
      thread_groups: 0,
      samplers: 0,
      converted_actions: 0,
      headers: 0,
      variables: 0,
      csv_sources: 0,
      assertions: 0,
      timers: 0,
      unsupported: 0,
    })
    expect(result.summary).toEqual({})
  })
})