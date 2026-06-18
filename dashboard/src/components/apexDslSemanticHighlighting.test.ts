import { describe, expect, it } from 'vitest'
import { EditorState } from '@codemirror/state'

import {
  APEX_DSL_SEMANTIC_MAX_DOC_CHARS,
  buildApexDslSemanticDecorations,
  collectApexDslSemanticRanges,
  type ApexDslSemanticKind,
} from './apexDslSemanticHighlighting'

const script = {
  name: 'Checkout Flow',
  version: '1.0.0',
  protocol: 'http',
  config: {
    base_url: 'https://api.example.test',
    think_time: { type: 'fixed', mean_ms: 1000 },
  },
  parameters: [
    { name: 'customer_id', type: 'list', values: ['c-100', 'c-200'] },
  ],
  data_sources: [
    { name: 'users', type: 'csv', path: 'users.csv', strategy: 'sequential' },
  ],
  actions: [
    {
      name: 'Submit Order',
      type: 'POST',
      target: '/orders/{{param.customer_id}}',
      metadata: {
        source_transaction: 'OrderSubmit',
      },
      extractors: [
        { name: 'order_id', source: 'body', type: 'jsonpath', expression: '$.id' },
      ],
      assertions: [
        { type: 'status_code', expected: 201 },
      ],
    },
  ],
}

function kinds(ranges: ReturnType<typeof collectApexDslSemanticRanges>) {
  return new Set(ranges.map(range => range.kind))
}

function rangesFor(ranges: ReturnType<typeof collectApexDslSemanticRanges>, kind: ApexDslSemanticKind) {
  return ranges.filter(range => range.kind === kind)
}

describe('collectApexDslSemanticRanges', () => {
  it('highlights semantic DSL concepts in wrapped Script JSON', () => {
    const text = JSON.stringify({ script }, null, 2)
    const ranges = collectApexDslSemanticRanges(text)
    const rangeKinds = kinds(ranges)

    expect(rangeKinds).toContain('sectionKey')
    expect(rangeKinds).toContain('actionBlock')
    expect(rangeKinds).toContain('actionName')
    expect(rangeKinds).toContain('actionMethod')
    expect(rangeKinds).toContain('actionTarget')
    expect(rangeKinds).toContain('parameterBlock')
    expect(rangeKinds).toContain('parameterName')
    expect(rangeKinds).toContain('parameterReference')
    expect(rangeKinds).toContain('dataSourceBlock')
    expect(rangeKinds).toContain('dataSourceName')
    expect(rangeKinds).toContain('extractorBlock')
    expect(rangeKinds).toContain('assertionBlock')
    expect(rangeKinds).toContain('configKey')
    expect(rangeKinds).toContain('transactionMetadata')

    expect(rangesFor(ranges, 'actionBlock').map(range => range.path)).toContain('script.actions[0]')
    expect(rangesFor(ranges, 'parameterName').map(range => range.path)).toContain('script.parameters[0].name')
    expect(rangesFor(ranges, 'dataSourceName').map(range => range.path)).toContain('script.data_sources[0].name')
    expect(rangesFor(ranges, 'transactionMetadata').map(range => range.path)).toContain('script.actions[0].metadata.source_transaction')
  })

  it('uses the same normalized paths for raw script JSON', () => {
    const text = JSON.stringify(script, null, 2)
    const ranges = collectApexDslSemanticRanges(text)

    expect(rangesFor(ranges, 'sectionKey').map(range => range.path)).toEqual(
      expect.arrayContaining(['script.config', 'script.parameters', 'script.data_sources', 'script.actions']),
    )
    expect(rangesFor(ranges, 'actionName').map(range => range.path)).toContain('script.actions[0].name')
  })

  it('supports compact JSON without relying on line formatting', () => {
    const text = JSON.stringify({ script })
    const ranges = collectApexDslSemanticRanges(text)

    expect(rangesFor(ranges, 'actionBlock')).toHaveLength(1)
    expect(rangesFor(ranges, 'parameterReference')).toHaveLength(1)
    expect(ranges.every(range => range.from >= 0 && range.to <= text.length && range.to > range.from)).toBe(true)
  })

  it('returns no semantic ranges for invalid JSON', () => {
    expect(collectApexDslSemanticRanges('{"script": {"actions": [}')).toEqual([])
  })
})

describe('buildApexDslSemanticDecorations', () => {
  it('decorates ordinary documents', () => {
    const text = JSON.stringify({ script }, null, 2)
    const state = EditorState.create({ doc: text })
    expect(buildApexDslSemanticDecorations(state).size).toBeGreaterThan(0)
  })

  it('skips decorations for documents above the size threshold (avoids the large-import freeze)', () => {
    const text = JSON.stringify({ script }, null, 2)
    const padded = text + '\n'.repeat(APEX_DSL_SEMANTIC_MAX_DOC_CHARS + 1)
    expect(padded.length).toBeGreaterThan(APEX_DSL_SEMANTIC_MAX_DOC_CHARS)
    const state = EditorState.create({ doc: padded })
    expect(buildApexDslSemanticDecorations(state).size).toBe(0)
  })
})
