// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import JMeterBatchRepairs from './JMeterBatchRepairs'
import type { JMeterImportResult } from '../types/scriptImports'

const repair = vi.hoisted(() => vi.fn())
// The requirement context the converter writes (eventHubsConnectionStringContext).
const EVENTHUBS_CONTEXT = 'Event Hubs connection string: Endpoint=sb://<namespace>.servicebus.windows.net/;SharedAccessKeyName=<policy>;SharedAccessKey=<key>, optionally followed by ;EntityPath=<event hub>, which must name this sampler\'s event hub "orders". Whitespace around each key and value is trimmed at run time.'
vi.mock('../hooks/useApi', () => ({ useApi: () => ({ repairJMeterBatch: repair }) }))
it('requires the reviewed operation and replaces the batch only after apply', async () => {
  const result = { batch: { id: 'batch', revision: 'one' }, scripts: [{ name: 'Producer', protocol: 'kafka', config: { jmeter: { source_group_id: 'group' } }, actions: [] }] } as unknown as JMeterImportResult
  const response = { batch: { id: 'batch', revision: 'two' }, preview_digest: 'digest', scripts: result.scripts, before: result.scripts, readiness: {} }
  repair.mockResolvedValue(response)
  const onApplied = vi.fn()
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={onApplied} onBusyChange={() => {}} />))
    const buttons = container.querySelectorAll('button')
    expect(buttons[1].disabled).toBe(true)
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => buttons[0].click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ batch_id: 'batch', revision: 'one', operations: [{ type: 'confirm_reference_context', timezone: 'UTC', confirm_no_overrides: true }] }), false)
    expect(onApplied).not.toHaveBeenCalled()
    expect(buttons[1].disabled).toBe(false)
    await act(async () => buttons[1].click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ revision: 'one', preview_digest: 'digest' }), true)
    expect(onApplied).toHaveBeenCalledWith(response)
  } finally { act(() => root.unmount()); container.remove() }
})
it('retains operator selections when a revision conflict occurs', async () => {
  repair.mockRejectedValue(new Error('Batch revision has already been replaced'))
  const result = { batch: { id: 'batch', revision: 'one' }, scripts: [{ name: 'Producer', config: {}, actions: [] }] } as unknown as JMeterImportResult
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={() => {}} onBusyChange={() => {}} />))
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => container.querySelector('button')!.click())
    expect(container.querySelector('[role=alert]')?.textContent).toContain('already been replaced')
    expect(container.querySelector<HTMLInputElement>('input[type=checkbox]')!.checked).toBe(true)
  } finally { act(() => root.unmount()); container.remove() }
})
it('opens on the requested repair and group with the secret names the importer chose', async () => {
  repair.mockReset()
  repair.mockResolvedValue({ batch: { id: 'batch', revision: 'one' }, preview_digest: 'd', scripts: [], before: [], readiness: {} })
  const result = {
    batch: { id: 'batch', revision: 'one' },
    scripts: [
      { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' }, kafka: { oauth: { client_id: '{{secret.KAFKA_ID}}', client_secret_ref: '{{secret.KAFKA_SECRET}}' } } }, actions: [] },
      { name: 'Hub east', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' }, eventhubs: { event_hub: 'orders-east', connection_string: '{{secret.HUB_EAST}}' } }, actions: [] },
    ],
    secret_binding_requirements: [{ id: 'r1', script_index: 1, path: '/config/eventhubs/connection_string', suggested_name: 'HUB_EAST_SUGGESTED', label: 'Event Hubs connection' }],
  } as unknown as JMeterImportResult
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" request={{ type: 'bind_eventhubs_credentials', group: 1, nonce: 1, revision: 'one' }} onApplied={() => {}} onBusyChange={() => {}} />))
    const selects = container.querySelectorAll('select')
    expect(selects[0].value).toBe('bind_eventhubs_credentials')
    expect(selects[1].value).toBe('1')
    expect(document.activeElement).toBe(selects[0])
    const secretInput = container.querySelector<HTMLInputElement>('input:not([type])')!
    expect(secretInput.value).toBe('HUB_EAST_SUGGESTED')
    expect(container.textContent).toContain('Endpoint=sb://<namespace>/;SharedAccessKeyName=<name>;SharedAccessKey=<key>')
    expect(container.textContent).toContain(';EntityPath=<hub>')
    expect(container.textContent).toContain('orders-east')
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Preview batch repair')!.click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [expect.objectContaining({ type: 'bind_eventhubs_credentials', source_group_id: 'g1', secret_name: 'HUB_EAST_SUGGESTED' })] }), false)

    // Kafka falls back to the references the draft carries.
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" request={{ type: 'bind_kafka_credentials', group: 0, nonce: 2, revision: 'one' }} onApplied={() => {}} onBusyChange={() => {}} />))
    const inputs = container.querySelectorAll<HTMLInputElement>('input:not([type])')
    expect(Array.from(inputs).map(input => input.value)).toEqual(['KAFKA_SECRET', 'KAFKA_ID'])
  } finally { act(() => root.unmount()); container.remove() }
})
it('confirms UTC or the zone the import was converted with, or another IANA zone', async () => {
  repair.mockReset()
  repair.mockResolvedValue({ batch: { id: 'batch', revision: 'one' }, preview_digest: 'd', scripts: [], before: [], readiness: {} })
  const result = { batch: { id: 'batch', revision: 'one' }, scripts: [{ name: 'Producer', protocol: 'kafka', config: { jmeter: { source_group_id: 'g', timezone: 'America/Chicago' } }, actions: [] }] } as unknown as JMeterImportResult
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const preview = () => Array.from(container.querySelectorAll('button')).find(button => button.textContent?.startsWith('Preview'))!
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={() => {}} onBusyChange={() => {}} />))
    const zone = container.querySelectorAll('select')[1]
    expect(Array.from(zone.options).map(option => option.value)).toEqual(['UTC', 'America/Chicago', '__other__'])
    // The import's own zone is the starting choice.
    expect(zone.value).toBe('America/Chicago')
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => preview().click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [{ type: 'confirm_reference_context', timezone: 'America/Chicago', confirm_no_overrides: true }] }), false)
    await act(async () => { zone.value = '__other__'; zone.dispatchEvent(new Event('change', { bubbles: true })) })
    const other = container.querySelector<HTMLInputElement>('input[placeholder="America/Chicago"]')!
    const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    await act(async () => { setValue.call(other, 'Europe/Berlin'); other.dispatchEvent(new Event('input', { bubbles: true })) })
    await act(async () => preview().click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [{ type: 'confirm_reference_context', timezone: 'Europe/Berlin', confirm_no_overrides: true }] }), false)
  } finally { act(() => root.unmount()); container.remove() }
})
it('locks the reference timezone to UTC for an import converted with time mode utc', async () => {
  repair.mockReset()
  repair.mockResolvedValue({ batch: { id: 'batch', revision: 'one' }, preview_digest: 'd', scripts: [], before: [], readiness: {} })
  // The converter keeps opts.Timezone in config.jmeter.timezone and records the mode in preserved time_resolution.
  const result = { batch: { id: 'batch', revision: 'one' }, scripts: [
    { name: 'Source', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0', timezone: 'America/Chicago' } }, metadata: { import: { preserved: { time_resolution: { mode: 'utc', timezone: 'America/Chicago' } } } }, actions: [] },
    { name: 'Other', protocol: 'kafka', config: { jmeter: { source_group_id: 'g1' } }, actions: [] },
  ] } as unknown as JMeterImportResult
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={() => {}} onBusyChange={() => {}} />))
    const zone = container.querySelectorAll('select')[1]
    expect(Array.from(zone.options).map(option => option.value)).toEqual(['UTC'])
    expect(zone.value).toBe('UTC')
    expect(zone.disabled).toBe(true)
    expect(container.textContent).toContain('converted with time mode utc, so only UTC can be confirmed')
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent?.startsWith('Preview'))!.click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [{ type: 'confirm_reference_context', timezone: 'UTC', confirm_no_overrides: true }] }), false)

    // A source-mode import still offers its own zone and any other.
    const source = { ...result, scripts: [{ ...result.scripts[0], metadata: { import: { preserved: { time_resolution: { mode: 'source', timezone: 'America/Chicago' } } } } }] } as unknown as JMeterImportResult
    await act(async () => root.render(<JMeterBatchRepairs result={source} projectId="p" onApplied={() => {}} onBusyChange={() => {}} />))
    const sourceZone = container.querySelectorAll('select')[1]
    expect(Array.from(sourceZone.options).map(option => option.value)).toEqual(['UTC', 'America/Chicago', '__other__'])
    expect(sourceZone.disabled).toBe(false)
    expect(container.textContent).not.toContain('only UTC can be confirmed')
  } finally { act(() => root.unmount()); container.remove() }
})
it('previews several repairs as one revision and shows the exact drafts the API returns', async () => {
  repair.mockReset()
  const result = {
    batch: { id: 'batch', revision: 'one' },
    scripts: [
      { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' } }, actions: [] },
      { name: 'Hub', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' }, eventhubs: { event_hub: 'orders' } }, actions: [] },
    ],
    secret_binding_requirements: [
      { id: 'r0', script_index: 0, path: '/config/kafka/oauth/client_secret_ref', suggested_name: 'KAFKA_SECRET', label: 'Kafka client secret', context: '' },
      { id: 'r0b', script_index: 0, path: '/config/kafka/oauth/client_id', suggested_name: 'KAFKA_ID', label: 'Kafka client ID', context: '' },
      { id: 'r1', script_index: 1, path: '/config/eventhubs/connection_string', suggested_name: 'HUB', label: 'Event Hubs connection', context: EVENTHUBS_CONTEXT },
    ],
  } as unknown as JMeterImportResult
  // The preview carries the exact draft (no scriptdisclosure projection).
  const exact = { name: 'Hub', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' }, eventhubs: { event_hub: 'orders', shared_access_key_name: 'send-policy', connection_string: '{{secret.HUB}}' } }, actions: [] }
  repair.mockResolvedValue({ batch: { id: 'batch', revision: 'one' }, preview_digest: 'd', scripts: [result.scripts[0], exact], before: result.scripts, readiness: {} })
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const button = (text: string) => Array.from(container.querySelectorAll('button')).find(item => item.textContent?.startsWith(text))!
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" request={{ type: 'bind_kafka_credentials', group: 0, nonce: 1, revision: 'one' }} onApplied={() => {}} onBusyChange={() => {}} />))
    await act(async () => button('Add another repair').click())
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" request={{ type: 'bind_eventhubs_credentials', group: 1, nonce: 2, revision: 'one' }} onApplied={() => {}} onBusyChange={() => {}} />))
    // The API's format text is shown as written, in place of the form's own
    // copy: no second lead-in, and the hub it already names is not repeated.
    expect(container.querySelector('#jmeter-eventhubs-connection-format')?.textContent).toBe(EVENTHUBS_CONTEXT)
    expect(container.querySelector('[aria-label="Repairs in this revision"]')?.textContent).toContain('Bind Kafka credential references · Kafka')
    await act(async () => button('Preview 2 batch repairs').click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [
      { type: 'bind_kafka_credentials', source_group_id: 'g0', secret_name: 'KAFKA_SECRET', client_id_secret: 'KAFKA_ID' },
      { type: 'bind_eventhubs_credentials', source_group_id: 'g1', secret_name: 'HUB' },
    ] }), false)
    const comparison = container.querySelector('[aria-label="Batch repair comparison"]')!.textContent || ''
    expect(comparison).toContain('Changed')
    // Nothing here expects or adds [REDACTED] markers: the reviewer sees the
    // hub, the key name and the reference the save will send back.
    expect(comparison).toContain(JSON.stringify(exact, null, 2))
    expect(comparison).not.toContain('REDACTED')
  } finally { act(() => root.unmount()); container.remove() }
})
it('does not reopen a request made for an earlier revision when the form remounts', async () => {
  repair.mockReset()
  const result = (revision: string) => ({
    batch: { id: 'batch', revision },
    scripts: [
      { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' } }, actions: [] },
      { name: 'Hub', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' } }, actions: [] },
    ],
  }) as unknown as JMeterImportResult
  const request = { type: 'bind_eventhubs_credentials', group: 1, nonce: 1, revision: 'one' }
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<JMeterBatchRepairs key="one" result={result('one')} projectId="p" request={request} onApplied={() => {}} onBusyChange={() => {}} />))
    expect(container.querySelectorAll('select')[0].value).toBe('bind_eventhubs_credentials')
    ;(document.activeElement as HTMLElement | null)?.blur()
    // An applied repair (or a new inspection) is a new revision; the parent
    // still holds the old request.
    await act(async () => root.render(<JMeterBatchRepairs key="two" result={result('two')} projectId="p" request={request} onApplied={() => {}} onBusyChange={() => {}} />))
    const select = container.querySelectorAll('select')[0]
    expect(select.value).toBe('confirm_reference_context')
    expect(document.activeElement).not.toBe(select)
    // A new request for the new revision opens the form again.
    await act(async () => root.render(<JMeterBatchRepairs key="two" result={result('two')} projectId="p" request={{ ...request, nonce: 2, revision: 'two' }} onApplied={() => {}} onBusyChange={() => {}} />))
    expect(container.querySelectorAll('select')[0].value).toBe('bind_eventhubs_credentials')
  } finally { act(() => root.unmount()); container.remove() }
})
it('sends only the queued repairs until the form is edited again', async () => {
  repair.mockReset()
  repair.mockResolvedValue({ batch: { id: 'batch', revision: 'one' }, preview_digest: 'd', scripts: [], before: [], readiness: {} })
  const result = {
    batch: { id: 'batch', revision: 'one' },
    scripts: [
      { name: 'Kafka', protocol: 'kafka', config: { jmeter: { source_group_id: 'g0' } }, actions: [] },
      { name: 'Hub', protocol: 'eventhubs', config: { jmeter: { source_group_id: 'g1' } }, actions: [] },
    ],
    secret_binding_requirements: [
      { id: 'r0', script_index: 0, path: '/config/kafka/oauth/client_secret_ref', suggested_name: 'KAFKA_SECRET', label: 'Kafka client secret' },
      { id: 'r0b', script_index: 0, path: '/config/kafka/oauth/client_id', suggested_name: 'KAFKA_ID', label: 'Kafka client ID' },
    ],
  } as unknown as JMeterImportResult
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const button = (text: string) => Array.from(container.querySelectorAll('button')).find(item => item.textContent?.startsWith(text))!
  const list = () => container.querySelector('[aria-label="Repairs in this revision"]')?.textContent || ''
  try {
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" onApplied={() => {}} onBusyChange={() => {}} />))
    // The confirmation is queued; the reset form (unchecked) is not a repair.
    await act(async () => container.querySelector<HTMLInputElement>('input[type=checkbox]')!.click())
    await act(async () => button('Add another repair').click())
    expect(container.querySelector<HTMLInputElement>('input[type=checkbox]')!.checked).toBe(false)
    expect(list()).not.toContain('being edited')
    expect(button('Add another repair').disabled).toBe(true)
    await act(async () => button('Preview batch repair').click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [{ type: 'confirm_reference_context', timezone: 'UTC', confirm_no_overrides: true }] }), false)

    // A bind repair, queued: the reset form fills in the same names, which
    // would repeat it.
    await act(async () => root.render(<JMeterBatchRepairs result={result} projectId="p" request={{ type: 'bind_kafka_credentials', group: 0, nonce: 1, revision: 'one' }} onApplied={() => {}} onBusyChange={() => {}} />))
    expect(list()).toContain('being edited')
    await act(async () => button('Add another repair').click())
    expect(list()).not.toContain('being edited')
    await act(async () => button('Preview 2 batch repairs').click())
    expect(repair).toHaveBeenLastCalledWith(expect.objectContaining({ operations: [
      { type: 'confirm_reference_context', timezone: 'UTC', confirm_no_overrides: true },
      { type: 'bind_kafka_credentials', source_group_id: 'g0', secret_name: 'KAFKA_SECRET', client_id_secret: 'KAFKA_ID' },
    ] }), false)

    // Editing the form makes it a repair of its own again.
    const secret = container.querySelector<HTMLInputElement>('input:not([type])')!
    const setValue = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
    await act(async () => { setValue.call(secret, 'OTHER_SECRET'); secret.dispatchEvent(new Event('input', { bubbles: true })) })
    expect(list()).toContain('being edited')
    await act(async () => button('Preview 3 batch repairs').click())
    expect(repair.mock.lastCall?.[0].operations).toHaveLength(3)
    expect(repair.mock.lastCall?.[0].operations[2]).toEqual({ type: 'bind_kafka_credentials', source_group_id: 'g0', secret_name: 'OTHER_SECRET', client_id_secret: 'KAFKA_ID' })
  } finally { act(() => root.unmount()); container.remove() }
})
