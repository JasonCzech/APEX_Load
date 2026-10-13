import { describe, expect, it } from 'vitest'

import type { ScriptActionDefinition, ScriptDefinition } from '../types/scripts'
import type { DslCatalogAction } from './dslActionCatalog'
import {
  activeVariant,
  catalogFieldLabel,
  chosenChoice,
  fieldUpdate,
  fieldValue,
  formFields,
  hasHandBuiltEditor,
  int64Problem,
  liveTableFormContext,
  paramsForChoice,
  strayParams,
  switchOperation,
} from './catalogActionFormModel'
import { LIVE_TABLE_SPEC } from './catalogActionFormFixture'

const action = (raw: Record<string, unknown>) => ({ name: 'step', target: '', ...raw }) as unknown as ScriptActionDefinition
const liveTable = (params: Record<string, unknown>) => action({ type: 'live_table', params })
const spec: DslCatalogAction = LIVE_TABLE_SPEC

describe('field paths', () => {
  it('reads and writes top-level, params and nested fields, keeping the other keys in order', () => {
    const subject = action({ type: 'eventhubs_consume', params: { event_hub: 'orders', timeout_ms: 5 }, config: { eventhubs: { start_position: 'latest' }, keep: true } })
    expect(fieldValue(subject, 'params.event_hub')).toBe('orders')
    expect(fieldValue(subject, 'config.eventhubs.start_position')).toBe('latest')
    expect(fieldValue(subject, 'params.missing.deeper')).toBeUndefined()
    expect(fieldUpdate(subject, 'target', 'hub')).toEqual({ key: 'target', value: 'hub' })
    const params = fieldUpdate(subject, 'params.event_hub', 'payments')
    expect(params.key).toBe('params')
    expect(Object.keys(params.value as object)).toEqual(['event_hub', 'timeout_ms'])
    expect(fieldUpdate(subject, 'params.timeout_ms', undefined).value).toEqual({ event_hub: 'orders' })
    expect(fieldUpdate(subject, 'config.eventhubs.consumer_mode', 'dedicated').value).toEqual({ eventhubs: { start_position: 'latest', consumer_mode: 'dedicated' }, keep: true })
    // The action itself is never changed.
    expect(subject.params).toEqual({ event_hub: 'orders', timeout_ms: 5 })
  })
})

describe('operations', () => {
  it('shows only the fields the operation takes, without the discriminator', () => {
    const read = liveTable({ alias: 'orders', operation: 'read', position: 1 })
    const variant = activeVariant(spec, read)!
    expect(variant.label).toBe('Read a row by ID or position')
    expect(formFields(spec, variant).map(field => field.name)).toEqual(['params.alias', 'params.result_prefix', 'params.record_id', 'params.position', 'params.columns', 'params.wait_ms', 'params.limit', 'params.on_empty'])
    expect(activeVariant(spec, liveTable({ alias: 'orders', operation: 'teleport' }))).toBeUndefined()
  })

  it('knows which choice an action makes and drops the other one', () => {
    const update = activeVariant(spec, liveTable({ operation: 'update' }))!
    expect(chosenChoice(update, liveTable({ operation: 'update', position: 2 }))).toBe(1)
    expect(chosenChoice(update, liveTable({ operation: 'update', record_id: 'r', expected_version: '3' }))).toBe(0)
    expect(chosenChoice(update, liveTable({ operation: 'update', expected_version: '3' }))).toBe(0)
    expect(chosenChoice(update, liveTable({ operation: 'update' }))).toBe(0)
    const both = liveTable({ alias: 'a', operation: 'update', record_id: 'r', expected_version: '3', position: 2, values: { s: 'x' } })
    expect(paramsForChoice(update, both, 1)).toEqual({ alias: 'a', operation: 'update', position: 2, values: { s: 'x' } })
    expect(paramsForChoice(update, both, 0)).toEqual({ alias: 'a', operation: 'update', record_id: 'r', expected_version: '3', values: { s: 'x' } })
  })

  it('switches operation: keeps what the new one takes, applies its defaults, names what goes', () => {
    const read = liveTable({ alias: 'orders', operation: 'read', position: 3, columns: ['status'], wait_ms: 100, mystery: true, result_prefix: 'row' })
    const push = spec.variants!.find(variant => variant.value === 'push')!
    expect(switchOperation(spec, read, push)).toEqual({
      params: { alias: 'orders', operation: 'push', mystery: true, result_prefix: 'row' },
      removed: ['position', 'columns', 'wait_ms'],
    })
    const increment = spec.variants!.find(variant => variant.value === 'increment')!
    expect(switchOperation(spec, read, increment)).toEqual({
      params: { alias: 'orders', operation: 'increment', position: 3, columns: ['status'], mystery: true, result_prefix: 'row', delta: '1' },
      removed: ['wait_ms'],
    })
    // A default never adds a second choice to one the action already makes.
    const byId = liveTable({ alias: 'orders', operation: 'update', record_id: '{{var.row.record_id}}', expected_version: '{{var.row.record_version}}', values: { s: 'x' } })
    const read2 = spec.variants!.find(variant => variant.value === 'read')!
    expect(switchOperation(spec, byId, read2).params).toEqual({ alias: 'orders', operation: 'read', record_id: '{{var.row.record_id}}' })
  })

  it('lists params the operation does not take, and ones the catalog does not know', () => {
    const next = liveTable({ alias: 'orders', operation: 'next', position: 2, mystery: 1 })
    expect(strayParams(spec, activeVariant(spec, next), next)).toEqual([{ key: 'position', value: 2 }, { key: 'mystery', value: 1 }])
  })
})

describe('values', () => {
  it('checks a signed 64-bit integer on its text', () => {
    for (const ok of ['0', '1', '-5', '9007199254740993', '9223372036854775807', '-9223372036854775808']) expect(int64Problem(ok)).toBe('')
    for (const bad of ['-0', '01', '+1', '1.5', '', ' 1', '1e3']) expect(int64Problem(bad)).not.toBe('')
    expect(int64Problem('9223372036854775808')).toContain('between')
  })

  it('names a field by its label, or its path in words', () => {
    expect(catalogFieldLabel({ name: 'params.lease_ref', label: 'Lease' })).toBe('Lease')
    expect(catalogFieldLabel({ name: 'params.consumer_group' })).toBe('Consumer group')
  })
})

describe('liveTableFormContext', () => {
  it('collects aliases and columns, and the outputs of earlier steps only', () => {
    const script = {
      name: 's', protocol: 'http', config: { native_services: { live_tables: [{ alias: 'declared' }] } },
      init: [{ name: 'take', type: 'live_table', params: { alias: 'work', operation: 'lease', result_prefix: 'job' } }],
      actions: [
        { name: 'read', type: 'live_table', params: { alias: 'orders', operation: 'read', position: 1, result_prefix: 'row', columns: ['status'] } },
        { name: 'wrap', type: 'control_if', control: { steps: [{ name: 'add', type: 'live_table', params: { alias: 'orders', operation: 'push', values: { owner: 'me' }, result_prefix: 'added' } }] } },
        { name: 'current', type: 'live_table', params: { alias: 'orders', operation: 'update' } },
        { name: 'later', type: 'live_table', params: { alias: 'orders', operation: 'read', position: 2, result_prefix: 'after' } },
      ],
      teardown: [],
    } as unknown as ScriptDefinition
    const context = liveTableFormContext(script, { phase: 'action', position: 'before', anchor: { phase: 'action', actionIndex: 2 } })
    expect(context.aliases).toEqual(['declared', 'orders', 'work'])
    expect(context.columnsByAlias.orders).toEqual(['status', 'owner'])
    expect(context.outputs).toEqual({
      record_id: ['{{var.job.record_id}}', '{{var.row.record_id}}', '{{var.added.record_id}}'],
      record_version: ['{{var.job.record_version}}', '{{var.row.record_version}}', '{{var.added.record_version}}'],
      lease: ['job'],
    })
  })
})

describe('liveTableFormContext inside a block', () => {
  it('offers an earlier read in the same block, and not one in the other branch', () => {
    const read = (prefix: string) => ({ name: prefix, type: 'live_table', params: { alias: 'orders', operation: 'read', position: 1, result_prefix: prefix } })
    const script = {
      name: 's', protocol: 'http', config: {},
      actions: [{ name: 'guard', type: 'control_if', control: { steps: [read('inner'), { name: 'current', type: 'live_table', params: { alias: 'orders', operation: 'update' } }], else: [read('other')] } }],
    } as unknown as ScriptDefinition
    const context = liveTableFormContext(script, { phase: 'action', position: 'before', anchor: { phase: 'action', actionIndex: 0, stepPath: ['then[1]'] } })
    expect(context.outputs.record_id).toEqual(['{{var.inner.record_id}}'])
  })
})

describe('hasHandBuiltEditor', () => {
  it('keeps hand-built editors and sends every other type to the catalog form', () => {
    for (const type of ['GET', 'request', 'http_sequence', 'http_oauth2_token', 'cookie_clear', 'browser_click', 'control_if', 'jms_publish', 'ws_send', 'grpc_unary', 'db_query', 'dns_query', 'source_transaction', 'rendezvous', 'native_javascript']) {
      expect(hasHandBuiltEditor(action({ type })), type).toBe(true)
    }
    for (const type of ['live_table', 'kafka_produce', 'eventhubs_consume', 'sapgui_set', 'java_vuser_run', 'cookie_set', 'error_message', 'http_sync']) {
      expect(hasHandBuiltEditor(action({ type })), type).toBe(false)
    }
  })
})
