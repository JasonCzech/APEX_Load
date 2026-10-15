import { describe, expect, it } from 'vitest'
import {
  RAW_HTTP_REQUEST_DELIMITER,
  RAW_HTTP_RESPONSE_DELIMITER,
  describeMergedFiles,
  mergePrimarySamples,
  mergeSecondarySamples,
} from './sampleImportUtils'

describe('sampleImportUtils', () => {
  it('merges multiple REST JSON files into one requests array', () => {
    const result = mergePrimarySamples('rest_json', [
      {
        name: 'one.json',
        text: JSON.stringify({ requests: [{ request: { method: 'POST', url: 'https://api.example.com/v1/session' } }] }),
      },
      {
        name: 'two.json',
        text: JSON.stringify({ requests: [{ request: { method: 'GET', url: 'https://api.example.com/v1/session/1/orders' } }] }),
      },
    ])

    expect(result.mergedCount).toBe(2)
    expect(JSON.parse(result.content)).toEqual({
      requests: [
        { request: { method: 'POST', url: 'https://api.example.com/v1/session' } },
        { request: { method: 'GET', url: 'https://api.example.com/v1/session/1/orders' } },
      ],
    })
  })

  it('joins multiple raw HTTP request and response files with batch delimiters', () => {
    const requests = mergePrimarySamples('raw_http', [
      { name: 'request-1.http', text: 'GET /first HTTP/1.1\nHost: api.example.com' },
      { name: 'request-2.http', text: 'GET /second HTTP/1.1\nHost: api.example.com' },
    ])
    const responses = mergeSecondarySamples('raw_http', [
      { name: 'response-1.http', text: 'HTTP/1.1 200 OK\n\n{}' },
      { name: 'response-2.http', text: 'HTTP/1.1 204 No Content' },
    ])

    expect(requests.mergedCount).toBe(2)
    expect(requests.content).toContain(RAW_HTTP_REQUEST_DELIMITER)
    expect(responses.mergedCount).toBe(2)
    expect(responses.content).toContain(RAW_HTTP_RESPONSE_DELIMITER)
  })

  it('describes merged files with richer summary text', () => {
    expect(describeMergedFiles('rest_json', 'primary', ['a.json', 'b.json'], 3)).toBe('2 files merged into 3 request samples')
    expect(describeMergedFiles('raw_http', 'secondary', ['a.http', 'b.http'], 2)).toBe('2 response files will be paired by order')
  })
})