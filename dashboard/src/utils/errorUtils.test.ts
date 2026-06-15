import { describe, expect, it } from 'vitest'

import { diagnoseError, getErrorMessage } from './errorUtils'

describe('error diagnostics', () => {
  it('keeps unknown error messages unchanged for existing simple surfaces', () => {
    const diagnostic = diagnoseError(new Error('boom'), 'Fallback')

    expect(diagnostic.category).toBe('unknown')
    expect(diagnostic.summary).toBe('boom')
    expect(getErrorMessage(new Error('boom'), 'Fallback')).toBe('boom')
  })

  it('classifies browser fetch failures as an unreachable controller', () => {
    const diagnostic = diagnoseError(new TypeError('Failed to fetch'), 'Unable to load auth status')

    expect(diagnostic.category).toBe('api_unreachable')
    expect(diagnostic.severity).toBe('critical')
    expect(diagnostic.summary).toBe('API controller is unreachable')
    expect(diagnostic.nextSteps.join(' ')).toContain('Docker Compose stack or controller process')
  })

  it('classifies dashboard HTML responses as API route drift', () => {
    const diagnostic = diagnoseError(
      {
        message: 'API endpoint returned the dashboard HTML instead of JSON. The backend route may be unavailable or the local dev proxy may be pointed at an older controller.',
        status: 200,
        payload: {
          content_type: 'text/html; charset=utf-8',
          body: '<!DOCTYPE html><html><head><title>APEX Load</title></head></html>',
        },
      },
      'Unable to load route',
    )

    expect(diagnostic.category).toBe('api_route')
    expect(diagnostic.summary).toContain('dashboard HTML instead of JSON')
    expect(diagnostic.evidence).toContain('HTTP status: 200')
    expect(diagnostic.evidence).toContain('Response content type: text/html; charset=utf-8')
    expect(diagnostic.evidence?.some((item) => item.includes('<!DOCTYPE html>'))).toBe(false)
  })

  it('preserves coded capacity messages while adding capacity next steps', () => {
    const diagnostic = diagnoseError(
      {
        message: 'Need more generators',
        status: 409,
        code: 'capacity_shortfall',
        payload: {
          code: 'capacity_shortfall',
          capacity_advisory: {
            available_generators: 1,
            recommended_generators: 3,
          },
        },
      },
      'Unable to start test',
    )

    expect(diagnostic.category).toBe('capacity')
    expect(diagnostic.summary).toBe('Need more generators')
    expect(diagnostic.evidence).toContain('Generators: 1 available, 3 recommended')
    expect(getErrorMessage({ message: 'Need more generators', status: 409, code: 'capacity_shortfall' }, 'Unable to start test')).toBe('Need more generators')
  })

  it('classifies import and conversion failures', () => {
    const diagnostic = diagnoseError('JMeter import failed: unsupported timer configuration', 'Import failed')

    expect(diagnostic.category).toBe('import')
    expect(diagnostic.summary).toBe('JMeter import failed: unsupported timer configuration')
    expect(diagnostic.nextSteps.join(' ')).toContain('selected importer')
  })

  it('classifies validation failures from status codes', () => {
    const diagnostic = diagnoseError({ message: 'Bad Request', status: 400, payload: { field: 'schedule.cron' } }, 'Unable to save schedule')

    expect(diagnostic.category).toBe('validation')
    expect(diagnostic.summary).toBe('The request needs valid input before it can continue')
    expect(diagnostic.evidence).toContain('Field: schedule.cron')
  })

  it('redacts credential-shaped values from response body previews', () => {
    const token = 'eyJhbGciOiJIUzI1NiJ9.payloadpayload.signaturesignature'
    const diagnostic = diagnoseError(
      {
        message: 'Upstream rejected the request',
        status: 502,
        payload: {
          body: `{"error":"bad gateway","access_token":"${token}","hint":"Authorization: Bearer ${token}"}`,
        },
      },
      'Request failed',
    )

    const preview = diagnostic.evidence?.find((item) => item.startsWith('Response preview:'))
    expect(preview).toBeDefined()
    expect(preview).not.toContain(token)
    expect(preview).toContain('[redacted]')
  })

  it('redacts credential-shaped values from the user-visible message and rawMessage', () => {
    const token = 'eyJhbGciOiJIUzI1NiJ9.payloadpayload.signaturesignature'
    const diagnostic = diagnoseError(
      { message: `request failed: Authorization: Bearer ${token}` },
      'Request failed',
    )
    expect(diagnostic.summary).not.toContain(token)
    expect(diagnostic.rawMessage).not.toContain(token)
  })

  it('classifies common string-only HTTP messages after hooks store simple errors', () => {
    const diagnostic = diagnoseError('test not found', 'Unable to load test execution details')

    expect(diagnostic.category).toBe('api_route')
    expect(diagnostic.summary).toBe('test not found')
    expect(diagnostic.nextSteps.join(' ')).toContain('stale IDs')
  })
})
