// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import KafkaManagementSourceFields from './KafkaManagementSourceFields'
import { kafkaSourceError, newKafkaIdentity, newKafkaScope } from './kafkaManagementSource'
import type { JsonSuccessResponseBody } from '../types/generated/openapi'

type Source = JsonSuccessResponseBody<'handleMonitorSources', '200'>[number]
const validSource = (): Source => ({ id: 'source', project_id: 'project', revision: 1, name: 'Offsets', collector_id: 'collector', pack: 'kafka-offsets', enabled: true, interval_seconds: 10, destination: 'management://broker', collector_public_key: 'public-key', management_scope: { ...newKafkaScope(), provider: 'cluster', entities: [{ kafka: { topic: 'orders', partition: 0, group: 'workers' } }] }, management_identity: { ...newKafkaIdentity(), principal: 'reader', policy_revision: 'v1', credential_ref: 'management-secret', credential_version: 'saved-version' } })

it('keeps zero partitions and rejects ambiguous or oversized inventory before save', () => {
  expect(kafkaSourceError(validSource())).toBe('')
  for (const partition of [-1, 0.5, 2147483648, Number.NaN]) {
    const source = validSource(); source.management_scope!.entities[0].kafka!.partition = partition
    expect(kafkaSourceError(source)).toContain('int32 partition')
  }
  const duplicate = validSource(); duplicate.management_scope!.entities.push(duplicate.management_scope!.entities[0])
  expect(kafkaSourceError(duplicate)).toContain('duplicate')
  const many = validSource(); many.management_scope!.entities = Array.from({ length: 257 }, (_, partition) => ({ kafka: { topic: 'orders', partition, group: 'workers' } }))
  expect(kafkaSourceError(many)).toContain('256')
  const large = validSource(); large.management_scope!.entities = Array.from({ length: 90 }, (_, partition) => ({ kafka: { topic: 't'.repeat(256), partition, group: 'workers' } }))
  expect(kafkaSourceError(large)).toContain('16 KiB')
  const unknown = validSource(); unknown.management_identity!.mode = 'anonymous'
  expect(kafkaSourceError(unknown)).toContain('supported Kafka SASL')
  const fractional = validSource(); fractional.interval_seconds = 10.5
  expect(kafkaSourceError(fractional)).toContain('interval')
  const unicode = validSource(); unicode.management_scope!.provider = 'é'.repeat(129)
  expect(kafkaSourceError(unicode)).toContain('cluster ID')
  const alias = validSource(); alias.destination = 'management://broker/path'
  expect(kafkaSourceError(alias)).toContain('management://alias')
})

it('shows saved approval separately from an unsaved draft and preserves inventory during edits', async () => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  const saved = validSource(); const draft = validSource(); draft.management_scope!.provider = 'unsaved-cluster'
  let changed: Source | undefined
  try {
    await act(async () => root.render(<KafkaManagementSourceFields source={draft} savedSource={saved} available onChange={source => { changed = source }} />))
    expect(container.querySelector('pre')!.textContent).toContain('"provider": "cluster"')
    expect(container.querySelector('pre')!.textContent).not.toContain('unsaved-cluster')
    expect(container.querySelector('pre')!.textContent).toContain('saved-version')
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Add Kafka entity')!.click())
    expect(changed!.management_scope!.entities).toHaveLength(2)
    expect(changed!.management_scope!.entities[0]).toEqual(draft.management_scope!.entities[0])
    expect(saved.management_scope!.entities).toHaveLength(1)
    await act(async () => root.render(<KafkaManagementSourceFields source={draft} savedSource={saved} available={false} onChange={() => { throw new Error('disabled approval changed') }} />))
    expect(container.querySelector('fieldset')!.disabled).toBe(true)
    expect(container.textContent).toContain('cannot configure')
  } finally { act(() => root.unmount()); container.remove() }
})


it('does not present another provider or source approval as the Kafka draft approval', async () => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  const source = validSource()
  const savedSources = [
    { ...validSource(), pack: 'servicebus-runtime' },
    { ...validSource(), management_scope: { ...newKafkaScope(), kind: 'servicebus_runtime' } },
    { ...validSource(), id: 'another-source' },
    { ...validSource(), project_id: 'another-project' },
  ]
  try {
    for (const savedSource of savedSources) {
      await act(async () => root.render(<KafkaManagementSourceFields source={source} savedSource={savedSource} available onChange={() => { throw new Error('readonly approval mutated') }} />))
      expect(container.querySelector('details')).toBeNull()
      expect(container.textContent).not.toContain('saved-version')
    }
  } finally { act(() => root.unmount()); container.remove() }
})


it('keeps help text out of control names and associates unique descriptions', async () => {
  ;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
  const container = document.createElement('div'); document.body.appendChild(container)
  const root = createRoot(container)
  try {
    await act(async () => root.render(<><KafkaManagementSourceFields source={validSource()} available onChange={() => {}} /><KafkaManagementSourceFields source={validSource()} available onChange={() => {}} /></>))
    const described = Array.from(container.querySelectorAll<HTMLInputElement>('input[aria-describedby]'))
    expect(described).toHaveLength(4)
    expect(new Set(described.map(input => input.getAttribute('aria-describedby'))).size).toBe(4)
    for (const input of described) {
      const name = input.getAttribute('aria-label')
      const description = document.getElementById(input.getAttribute('aria-describedby')!)
      expect(name === 'Kafka cluster ID' || name === 'Management credential project-secret reference').toBe(true)
      expect(description?.textContent).toMatch(/Must match|purpose-bound/)
      expect(input.closest('label')!.contains(description)).toBe(true)
    }
  } finally { act(() => root.unmount()); container.remove() }
})
