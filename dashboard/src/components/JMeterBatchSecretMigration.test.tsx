// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import JMeterBatchSecretMigration from './JMeterBatchSecretMigration'
import type { JMeterImportResult } from '../types/scriptImports'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const api = vi.hoisted(() => ({ getProjectSecretStatuses: vi.fn(), migrateImportSecrets: vi.fn() }))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))

const scripts = [
  { name: 'Hub east', protocol: 'eventhubs', config: {}, actions: [] },
  { name: 'Hub west', protocol: 'eventhubs', config: {}, actions: [] },
  { name: 'Producer', protocol: 'kafka', config: {}, actions: [] },
]
const eventHubs = (id: string, scriptIndex: number, suggested: string) => ({ id, script_index: scriptIndex, path: '/config/eventhubs/connection_string', label: 'Event Hubs connection', context: '', suggested_name: suggested, source_value_available: true })
const result = (requirements: unknown[], extra: Record<string, unknown> = {}) => ({
  batch: { id: 'batch', revision: 'one' }, scripts, secret_binding_requirements: requirements,
  source_secret_session_id: 'session', source_secret_session_expires_at: '2999-01-01T00:00:00Z', source_secret_migration_allowed: true, ...extra,
}) as unknown as JMeterImportResult

let container: HTMLDivElement
let root: Root
beforeEach(() => {
  container = document.createElement('div'); document.body.append(container); root = createRoot(container)
  api.getProjectSecretStatuses.mockResolvedValue({ project_id: 'p', capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true, source_secret_migration_policy_enabled: true }, secrets: [] })
  api.migrateImportSecrets.mockResolvedValue({ project_id: 'p', secrets: [], count: 1 })
})
afterEach(() => { act(() => root.unmount()); container.remove(); vi.clearAllMocks() })

const render = (value: JMeterImportResult, props: Partial<Parameters<typeof JMeterBatchSecretMigration>[0]> = {}) => act(async () => root.render(
  <JMeterBatchSecretMigration result={value} projectId="p" migrated={[]} onBusyChange={() => {}} onMigrated={vi.fn()} onSessionEnded={vi.fn()} onBind={vi.fn()} {...props} />,
))
const migrateButton = () => Array.from(container.querySelectorAll('button')).find(item => /^Migrate/.test(item.textContent || ''))!

it('refuses one name for two credentials whose values are not proven equal, before any request', async () => {
  // Same suggestion in two scripts: one request would create the name twice
  // with possibly different values, fail, and use the offer up.
  const onMigrated = vi.fn()
  await render(result([eventHubs('a', 0, 'HUB_CONNECTION'), eventHubs('b', 1, 'HUB_CONNECTION')]), { onMigrated })
  await act(async () => migrateButton().click())
  expect(container.querySelector('[role="alert"]')!.textContent).toContain('HUB_CONNECTION is chosen for more than one credential')
  expect(api.getProjectSecretStatuses).not.toHaveBeenCalled()
  expect(api.migrateImportSecrets).not.toHaveBeenCalled()

  // Leaving one out migrates the other, and reports where it went.
  await act(async () => container.querySelectorAll<HTMLInputElement>('input[type=checkbox]')[1].click())
  await act(async () => migrateButton().click())
  expect(api.migrateImportSecrets).toHaveBeenCalledWith({ project_id: 'p', source_secret_session_id: 'session', secrets: [{ requirement_id: 'a', secret_name: 'HUB_CONNECTION' }] })
  expect(onMigrated).toHaveBeenCalledWith('session', [{ script_index: 0, path: '/config/eventhubs/connection_string', secret_name: 'HUB_CONNECTION' }])
})

it('lets two credentials of one script share a name when the server proved their values equal', async () => {
  await render(result([{ ...eventHubs('a', 0, 'HUB_CONNECTION'), value_group_id: 'v1' }, { ...eventHubs('b', 0, 'HUB_CONNECTION'), path: '/config/eventhubs/other', value_group_id: 'v1' }]))
  await act(async () => migrateButton().click())
  expect(api.migrateImportSecrets).toHaveBeenCalledWith(expect.objectContaining({ secrets: [{ requirement_id: 'a', secret_name: 'HUB_CONNECTION' }, { requirement_id: 'b', secret_name: 'HUB_CONNECTION' }] }))
})

it('keeps the offer when the provider retained the session, and ends it when the session is used up', async () => {
  const onSessionEnded = vi.fn()
  const failure = (payload: Record<string, unknown>) => Object.assign(new Error('failed'), { payload })
  api.migrateImportSecrets.mockRejectedValueOnce(failure({ session_retained: true }))
  await render(result([eventHubs('a', 0, 'HUB_CONNECTION')]), { onSessionEnded })
  await act(async () => migrateButton().click())
  expect(onSessionEnded).not.toHaveBeenCalled()
  expect(container.querySelector('[role="alert"]')!.textContent).toContain('the protected import session was retained')

  api.migrateImportSecrets.mockRejectedValueOnce(failure({ source_secret_session_consumed: true }))
  await act(async () => migrateButton().click())
  expect(onSessionEnded).toHaveBeenCalledWith('session', true, expect.stringContaining('cannot be retried'))
})

it('records the secrets a partial migration created before the session ended', async () => {
  const onMigrated = vi.fn()
  const onSessionEnded = vi.fn()
  // The provider failed on the second name after creating the first.
  api.migrateImportSecrets.mockRejectedValueOnce(Object.assign(new Error('failed'), { payload: { partial: true, source_secret_session_consumed: true, migrated: [{ name: 'HUB_EAST_RENAMED', provider: 'database' }], remaining_names: ['HUB_WEST_CONNECTION'] } }))
  await render(result([eventHubs('a', 0, 'HUB_EAST_CONNECTION'), eventHubs('b', 1, 'HUB_WEST_CONNECTION')]), { onMigrated, onSessionEnded })
  const nameInput = container.querySelectorAll<HTMLInputElement>('input:not([type=checkbox])')[0]
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(nameInput, 'HUB_EAST_RENAMED')
    nameInput.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => migrateButton().click())
  expect(onMigrated).toHaveBeenCalledWith('session', [{ script_index: 0, path: '/config/eventhubs/connection_string', secret_name: 'HUB_EAST_RENAMED' }], expect.stringContaining('cannot be retried'))
  expect(onSessionEnded).not.toHaveBeenCalled()
})

it('offers no migration without an allowed, unexpired session, and one bind repair per script for migrated Kafka credentials', async () => {
  await render(result([eventHubs('a', 0, 'HUB_CONNECTION')], { source_secret_migration_allowed: false }))
  expect(container.textContent).toBe('')
  await render(result([eventHubs('a', 0, 'HUB_CONNECTION')], { source_secret_session_expires_at: '2000-01-01T00:00:00Z' }))
  expect(migrateButton()).toBeUndefined()
  expect(container.textContent).toContain('has expired')

  const onBind = vi.fn()
  const kafka = [
    { id: 'k1', script_index: 2, path: '/config/kafka/oauth/client_id', label: 'Client ID', context: '', suggested_name: 'PRODUCER_CLIENT_ID' },
    { id: 'k2', script_index: 2, path: '/config/kafka/oauth/client_secret_ref', label: 'Client secret', context: '', suggested_name: 'PRODUCER_CLIENT_SECRET' },
  ]
  await render(result(kafka, { source_secret_session_id: undefined }), {
    onBind,
    migrated: [
      { script_index: 2, path: '/config/kafka/oauth/client_id', secret_name: 'PRODUCER_CLIENT_ID' },
      { script_index: 2, path: '/config/kafka/oauth/client_secret_ref', secret_name: 'PRODUCER_CLIENT_SECRET' },
      // Already bound: its requirement is gone.
      { script_index: 0, path: '/config/eventhubs/connection_string', secret_name: 'HUB_CONNECTION' },
    ],
  })
  const entries = container.querySelectorAll('[aria-label="Migrated secrets to bind"] li')
  expect(entries).toHaveLength(1)
  expect(entries[0].textContent).toContain('PRODUCER_CLIENT_ID, PRODUCER_CLIENT_SECRET for Producer')
  await act(async () => entries[0].querySelector('button')!.click())
  expect(onBind).toHaveBeenCalledWith('bind_kafka_credentials', 2)
})
