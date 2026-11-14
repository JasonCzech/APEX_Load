import { describe, expect, it } from 'vitest'

import {
  databaseDSNPlaceholder,
  javaVuserParamBraceIssue,
  kafkaRuntimeSummary,
  matchingSelectOptionValue,
  parseOptionalInteger,
  patchConfigBlock,
  PROTOCOL_RUNTIME_SELECT_OPTIONS,
  secretReferenceNames,
  selectOptionsWithStored,
} from './protocolRuntimeOptions'

describe('patchConfigBlock', () => {
  it('sets a nested value without touching sibling keys', () => {
    expect(patchConfigBlock({ brokers: ['b:9092'], tls: { ca_cert_file: 'ca.pem' } }, 'tls.min_version', '1.3'))
      .toEqual({ brokers: ['b:9092'], tls: { ca_cert_file: 'ca.pem', min_version: '1.3' } })
  })

  it('drops a nested block once its last field is cleared', () => {
    expect(patchConfigBlock({ topic: 'orders', oauth: { token_url: 'https://idp.example.test/token' } }, 'oauth.token_url', ''))
      .toEqual({ topic: 'orders' })
    expect(patchConfigBlock({ producer: { retries: 3 } }, 'producer.retries', undefined)).toEqual({})
  })

  it('removes empty strings and lists but keeps zero and false', () => {
    expect(patchConfigBlock({ brokers: ['a'] }, 'brokers', [])).toEqual({})
    expect(patchConfigBlock({}, 'producer.retries', 0)).toEqual({ producer: { retries: 0 } })
    expect(patchConfigBlock({}, 'tls.insecure_skip_verify', false)).toEqual({ tls: { insecure_skip_verify: false } })
  })

  it('does not mutate the input', () => {
    const base = { oauth: { mode: 'client_credentials' } }
    patchConfigBlock(base, 'oauth.scope', 'kafka')
    expect(base).toEqual({ oauth: { mode: 'client_credentials' } })
  })
})

describe('selectOptionsWithStored', () => {
  it('appends a stored value the view does not offer so the select shows it', () => {
    expect(selectOptionsWithStored('config.database.driver', 'mysql').map(option => option.value)).toEqual(['sqlite', 'postgres', 'mysql'])
    expect(selectOptionsWithStored('config.database.driver', 'postgres').map(option => option.value)).toEqual(['sqlite', 'postgres'])
    expect(selectOptionsWithStored('config.database.driver', undefined)).toHaveLength(2)
  })

  it('shows a stored spelling the backend normalizes as the option it runs as, not as unsupported', () => {
    const kafka = selectOptionsWithStored('config.kafka.sasl_mechanism', 'PLAIN')
    expect(kafka.find(option => option.value === 'PLAIN')?.label).toBe('PLAIN')
    expect(kafka.map(option => option.value)).not.toContain('plain')
    expect(kafka.some(option => option.label.includes('not supported'))).toBe(false)
    expect(kafka).toHaveLength(PROTOCOL_RUNTIME_SELECT_OPTIONS['config.kafka.sasl_mechanism'].length)

    const security = selectOptionsWithStored('config.kafka.security_protocol', 'SASL_SSL')
    expect(security.find(option => option.value === 'SASL_SSL')?.label).toBe('SASL SSL')

    const driver = selectOptionsWithStored('config.database.driver', 'sqlite3')
    expect(driver).toEqual([{ value: 'sqlite3', label: 'SQLite (in-memory)' }, { value: 'postgres', label: 'PostgreSQL' }])

    const network = selectOptionsWithStored('config.network.subprotocol', 'SMTP')
    expect(network.find(option => option.value === 'SMTP')?.label).toBe('SMTP')
    expect(network.some(option => option.label.includes('not supported'))).toBe(false)
    expect(selectOptionsWithStored('config.network.subprotocol', 'winsock').find(option => option.value === 'winsock')?.label).toBe('TCP / Winsock')

    expect(matchingSelectOptionValue('config.kafka.serialization', 'RAW')).toBe('')
    expect(matchingSelectOptionValue('config.database.driver', ' Postgres ')).toBe('postgres')
  })

  it('keeps the not-supported entry for values the backend rejects or compares exactly', () => {
    const last = (options: { label: string }[]) => options[options.length - 1]
    expect(last(selectOptionsWithStored('config.kafka.sasl_mechanism', 'GSSAPI')).label).toBe('GSSAPI (stored value, not supported here)')
    // consumer_mode is compared exactly by ValidateExclusiveSettings.
    expect(last(selectOptionsWithStored('config.eventhubs.consumer_mode', 'Processor')).label).toBe('Processor (stored value, not supported here)')
    // mysql passes validation but APEX Load ships only sqlite and postgres.
    expect(last(selectOptionsWithStored('config.database.driver', 'MySQL')).label).toBe('MySQL (stored value, not supported here)')
  })
})

describe('secretReferenceNames', () => {
  it('lists each referenced project secret once', () => {
    expect(secretReferenceNames('https://{{secret.USER}}:{{ secret.KEY }}@hub/{{secret.USER}}')).toEqual(['USER', 'KEY'])
    expect(secretReferenceNames('plain-password')).toEqual([])
    expect(secretReferenceNames(undefined)).toEqual([])
  })
})

describe('databaseDSNPlaceholder', () => {
  it('suggests a DSN the selected driver accepts', () => {
    expect(databaseDSNPlaceholder('sqlite')).toBe(':memory: or file:testdb?mode=memory&cache=shared')
    expect(databaseDSNPlaceholder(undefined)).toBe(':memory: or file:testdb?mode=memory&cache=shared')
    expect(databaseDSNPlaceholder(' Postgres ')).toMatch(/^postgres:\/\//)
    expect(databaseDSNPlaceholder('postgres')).not.toContain(':memory:')
  })
})

describe('javaVuserParamBraceIssue', () => {
  it('mirrors validateJavaVuserParamBraces', () => {
    expect(javaVuserParamBraceIssue(undefined, undefined)).toBe('')
    expect(javaVuserParamBraceIssue('<<', '>>')).toBe('')
    expect(javaVuserParamBraceIssue('<<', '')).toContain('both')
    expect(javaVuserParamBraceIssue('< <', '>')).toContain('1 to 8')
    expect(javaVuserParamBraceIssue('123456789', '>')).toContain('1 to 8')
  })
})

describe('parseOptionalInteger', () => {
  it('treats blank as unset and keeps zero', () => {
    expect(parseOptionalInteger('')).toBeUndefined()
    expect(parseOptionalInteger('0')).toBe(0)
    expect(parseOptionalInteger('12.7')).toBe(12)
  })
})

describe('kafkaRuntimeSummary', () => {
  it('summarizes mock, unconfigured and broker-backed Kafka', () => {
    expect(kafkaRuntimeSummary({ mock: true, brokers: ['b:9092'] })).toBe('Mock broker')
    expect(kafkaRuntimeSummary(undefined)).toBe('Not configured')
    expect(kafkaRuntimeSummary({ brokers: ['a:9092', 'b:9092'], security_protocol: 'sasl_ssl' })).toBe('2 brokers · sasl_ssl')
    expect(kafkaRuntimeSummary({ brokers: ['a:9092'] })).toBe('1 broker · plaintext')
  })
})
