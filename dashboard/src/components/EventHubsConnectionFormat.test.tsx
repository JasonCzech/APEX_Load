// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { EventHubsConnectionFormat } from './EventHubsConnectionFormat'

// The two contexts the API sends: the converter's
// (eventHubsConnectionStringContext, naming the hub) and the handler's
// fallback for a requirement without one (describeJMeterCredentialRequirements).
const CONVERTER_CONTEXT = 'Event Hubs connection string: Endpoint=sb://<namespace>.servicebus.windows.net/;SharedAccessKeyName=<policy>;SharedAccessKey=<key>, optionally followed by ;EntityPath=<event hub>, which must name this sampler\'s event hub "orders". Whitespace around each key and value is trimmed at run time.'
const FALLBACK_CONTEXT = 'Full Event Hubs connection string: Endpoint=sb://<namespace>.servicebus.windows.net/;SharedAccessKeyName=<policy>;SharedAccessKey=<key>. An EntityPath, if present, must name this sampler\'s event hub.'

function render(context: string | undefined, eventHub?: string): string {
  const container = document.createElement('div')
  const root = createRoot(container)
  act(() => root.render(<EventHubsConnectionFormat id="format" context={context} eventHub={eventHub} />))
  const text = container.querySelector('#format')?.textContent || ''
  act(() => root.unmount())
  return text
}

const occurrences = (text: string, part: string) => text.split(part).length - 1

it('shows the converter\'s context once, without a second lead-in or a repeated hub', () => {
  const text = render(CONVERTER_CONTEXT, 'orders')
  expect(text).toBe(CONVERTER_CONTEXT)
  expect(occurrences(text, 'Event Hubs connection string:')).toBe(1)
  expect(occurrences(text, 'orders')).toBe(1)
})

it('names the hub after a context that does not', () => {
  const text = render(FALLBACK_CONTEXT, 'orders')
  expect(text).toBe(`${FALLBACK_CONTEXT} This script's event hub is orders.`)
  expect(occurrences(text, 'Event Hubs connection string:')).toBe(1)
})

it('falls back to the form\'s own wording without a context', () => {
  const text = render('  ', 'orders')
  expect(text).toContain("The secret's value is the Event Hubs connection string, in the form Endpoint=sb://<namespace>/")
  expect(text).toContain('(orders)')
})
