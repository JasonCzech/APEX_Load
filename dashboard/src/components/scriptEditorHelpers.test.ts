import { describe, expect, it } from 'vitest'

import {
  defaultAssertionForProtocol,
  defaultExtractorForProtocol,
  jmsConfigForProtocol,
  jmsProtocolForProvider,
  jmsProviderForProtocol,
} from './scriptEditorHelpers'

describe('JMS script editor helpers', () => {
  it.each([
    ['jms-solace', 'solace'],
    ['jms-servicebus', 'servicebus'],
    ['jms-ibmmq', 'ibmmq'],
  ] as const)('maps %s to its runtime provider', (protocol, provider) => {
    expect(jmsProviderForProtocol(protocol)).toBe(provider)
  })

  it('does not guess a provider for an unknown or family-only protocol', () => {
    expect(jmsProviderForProtocol('jms')).toBeNull()
    expect(jmsProviderForProtocol('kafka')).toBeNull()
  })

  it.each([
    ['solace', 'jms-solace'],
    ['servicebus', 'jms-servicebus'],
    ['ibmmq', 'jms-ibmmq'],
  ] as const)('maps provider %s to its executable protocol', (provider, protocol) => {
    expect(jmsProtocolForProvider(provider)).toBe(protocol)
  })

  it('replaces a stale provider while preserving compatible JMS settings', () => {
    expect(jmsConfigForProtocol('jms-servicebus', {
      provider: 'solace',
      destination: 'orders',
      message_vpn: 'old-solace-vpn',
      mock: false,
    })).toEqual(expect.objectContaining({
      provider: 'servicebus',
      destination: 'orders',
      destination_type: 'queue',
      acknowledgement: 'auto',
      delivery_mode: 'persistent',
      timeout_ms: 5000,
      mock: false,
    }))
    expect(jmsConfigForProtocol('jms-servicebus', {
      message_vpn: 'old-solace-vpn',
    })).not.toHaveProperty('message_vpn')
    expect(jmsConfigForProtocol('jms-solace', {
      subscription: 'orders-subscription',
    })).toEqual(expect.objectContaining({
      provider: 'solace',
      subscription: 'orders-subscription',
    }))
  })

  it('uses JMS extractor and assertion defaults for Service Bus', () => {
    expect(defaultExtractorForProtocol('jms-servicebus')).toEqual(
      expect.objectContaining({ source: 'body', type: 'jsonpath' }),
    )
    expect(defaultAssertionForProtocol('jms-servicebus')).toEqual({ type: 'message_received' })
  })
})
