// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import GroupPlacementEditor from './GroupPlacementEditor'
import { GeneratorAssignmentSettings, SystemInventoryGenerator } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(() => {
  act(() => root.unmount())
  container.remove()
})

// Controlled harness mirroring how the editor is used (assignment + onChange owned by
// the parent), so mode transitions exercise the real serialize -> re-derive round-trip.
function Harness({ availableLabels, availableTargets, protocol }: {
  availableLabels?: Record<string, string[]>
  availableTargets?: string[]
  protocol?: string
}) {
  const [assignment, setAssignment] = useState<GeneratorAssignmentSettings | undefined>(undefined)
  return (
    <GroupPlacementEditor
      assignment={assignment}
      onChange={setAssignment}
      availableLabels={availableLabels}
      availableTargets={availableTargets}
      protocol={protocol}
      allowInherit
    />
  )
}

function AssignmentHarness({ initialAssignment, availableTargets = [], generatorInventory, protocol, scope = 'group' }: { initialAssignment: GeneratorAssignmentSettings, availableTargets?: string[], generatorInventory?: SystemInventoryGenerator[], protocol?: string, scope?: 'group' | 'scenario' }) {
  const [assignment, setAssignment] = useState<GeneratorAssignmentSettings | undefined>(initialAssignment)
  return (
    <>
      <GroupPlacementEditor assignment={assignment} onChange={setAssignment} availableTargets={availableTargets} generatorInventory={generatorInventory} protocol={protocol} allowInherit scope={scope} />
      <output data-testid="assignment-json">{JSON.stringify(assignment)}</output>
    </>
  )
}

function runtimeCapacityGenerator(runtimeCapacities: NonNullable<SystemInventoryGenerator['runtime_capacities']>): SystemInventoryGenerator {
  return {
    target: 'gen-a',
    generator_id: 'gen-a',
    status: 'connected',
    supported_protocols: ['http', 'bruno'],
    runtime_capacities: runtimeCapacities,
    available_capacity: 100,
    active_vusers: 0,
    cpu_percent: 0,
    memory_bytes: 0,
    goroutine_count: 0,
    cpu_limit_cores: 4,
    memory_limit_bytes: 1024,
    max_vusers: 100,
  }
}

function radio(text: string): HTMLInputElement {
  const labels = Array.from(container.querySelectorAll('.new-test-placement-toggle label'))
  const label = labels.find(node => node.textContent?.includes(text))
  if (!label) throw new Error(`radio not found: ${text}`)
  return label.querySelector('input[type=radio]') as HTMLInputElement
}

describe('GroupPlacementEditor', () => {
  it('keeps the Require capability panel open before a label is chosen (mode does not snap back to auto)', () => {
    act(() => root.render(<Harness availableLabels={{ jvm: ['11', '17'] }} />))

    act(() => radio('Require capability').click())

    // The bug: required_labels serializes as { mode:'auto', required_labels:{} } and
    // the radio derived straight back to "Automatic placement", hiding the panel.
    expect(radio('Require capability').checked).toBe(true)
    expect(radio('Automatic placement').checked).toBe(false)
    const select = container.querySelector('select.settings-input') as HTMLSelectElement | null
    expect(select).not.toBeNull()
  })

  it('serializes a chosen capability as auto + required_labels', () => {
    act(() => root.render(<Harness availableLabels={{ jvm: ['11', '17'] }} />))
    act(() => radio('Require capability').click())

    const select = container.querySelector('select.settings-input') as HTMLSelectElement
    act(() => {
      select.value = '17'
      select.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('jvm=17')
    expect(radio('Require capability').checked).toBe(true)
  })

  it('emits specific_hosts and capacity-weighted sharding for multi-host pins', () => {
    act(() => root.render(<Harness availableTargets={['gen-a:9090', 'gen-b:9090']} protocol="http" />))
    act(() => radio('Pin to host(s)').click())

    const checkboxes = Array.from(container.querySelectorAll('.new-test-generator-option input[type=checkbox]')) as HTMLInputElement[]
    expect(checkboxes.length).toBe(2)
    // Fallback rows (no live inventory) must remain selectable (M3).
    expect(checkboxes.every(box => !box.disabled)).toBe(true)

    act(() => checkboxes[0].click())
    act(() => {
      const boxes = Array.from(container.querySelectorAll('.new-test-generator-option input[type=checkbox]')) as HTMLInputElement[]
      boxes[1].click()
    })
    expect(container.textContent).toContain('capacity weighted')
    expect(container.textContent).toContain('capacity unknown')
    expect(container.textContent).toContain('Selected VUser capacity unknown')
  })

  it('reports the protocol runtime ceiling instead of overstating aggregate capacity', () => {
    const generator: SystemInventoryGenerator = {
      target: 'gen-a',
      generator_id: 'gen-a',
      status: 'connected',
      supported_protocols: ['http', 'bruno'],
      runtime_capacities: [{ protocol: 'bruno', available: true, max_vusers: 16, available_vusers: 8 }],
      available_capacity: 100,
      active_vusers: 0,
      cpu_percent: 0,
      memory_bytes: 0,
      goroutine_count: 0,
      cpu_limit_cores: 4,
      memory_limit_bytes: 1024,
      max_vusers: 100,
    }
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: ['gen-a'] }}
        generatorInventory={[generator]}
        protocol="bruno"
      />,
    ))

    const option = container.querySelector('.new-test-generator-option')
    expect(option?.textContent).toContain('8 capacity')
    expect(option?.textContent).not.toContain('100 capacity')
    expect(container.textContent).toContain('8 selected VUser capacity')
  })

  it('rejects an advertised runtime whose maximum VUser capacity is zero', () => {
    const generator = runtimeCapacityGenerator([
      { protocol: 'bruno', available: true, max_vusers: 0, available_vusers: 8 },
    ])
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: [] }}
        generatorInventory={[generator]}
        protocol="bruno"
      />,
    ))

    const option = container.querySelector('.new-test-generator-option')
    expect(option?.querySelector<HTMLInputElement>('input[type=checkbox]')?.disabled).toBe(true)
    expect(option?.textContent).toContain('No available bruno runtime capacity')
  })

  it('caps advertised available runtime VUsers at the runtime maximum', () => {
    const generator = runtimeCapacityGenerator([
      { protocol: 'bruno', available: true, max_vusers: 4, available_vusers: 8 },
    ])
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: ['gen-a'] }}
        generatorInventory={[generator]}
        protocol="bruno"
      />,
    ))

    const option = container.querySelector('.new-test-generator-option')
    expect(option?.textContent).toContain('4 capacity')
    expect(option?.textContent).not.toContain('8 capacity')
    expect(container.textContent).toContain('4 selected VUser capacity')
  })

  it('uses the minimum capacity when duplicate runtime advertisements exist', () => {
    const generator = runtimeCapacityGenerator([
      { protocol: 'BRUNO', available: true, max_vusers: 16, available_vusers: 12 },
      { protocol: 'bruno', available: true, max_vusers: 16, available_vusers: 3 },
    ])
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: ['gen-a'] }}
        generatorInventory={[generator]}
        protocol="bruno"
      />,
    ))

    const option = container.querySelector('.new-test-generator-option')
    expect(option?.textContent).toContain('3 capacity')
    expect(option?.textContent).not.toContain('12 capacity')
    expect(container.textContent).toContain('3 selected VUser capacity')
  })

  it('disables a host when its protocol-specific runtime has no capacity', () => {
    const generator: SystemInventoryGenerator = {
      target: 'gen-a',
      generator_id: 'gen-a',
      status: 'connected',
      supported_protocols: ['http', 'bruno'],
      runtime_capacities: [{ protocol: 'bruno', available: true, max_vusers: 16, available_vusers: 0 }],
      available_capacity: 100,
      active_vusers: 0,
      cpu_percent: 0,
      memory_bytes: 0,
      goroutine_count: 0,
      cpu_limit_cores: 4,
      memory_limit_bytes: 1024,
      max_vusers: 100,
    }
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: [] }}
        generatorInventory={[generator]}
        protocol="bruno"
      />,
    ))

    const option = container.querySelector('.new-test-generator-option')
    expect(option?.querySelector<HTMLInputElement>('input[type=checkbox]')?.disabled).toBe(true)
    expect(option?.textContent).toContain('No available bruno runtime capacity')
  })

  it('disables a host when generator-wide capacity is exhausted despite available protocol runtime capacity', () => {
    const generator: SystemInventoryGenerator = {
      target: 'gen-a',
      generator_id: 'gen-a',
      status: 'connected',
      supported_protocols: ['http', 'bruno'],
      runtime_capacities: [{ protocol: 'bruno', available: true, max_vusers: 16, available_vusers: 8 }],
      available_capacity: 0,
      active_vusers: 100,
      cpu_percent: 0,
      memory_bytes: 0,
      goroutine_count: 0,
      cpu_limit_cores: 4,
      memory_limit_bytes: 1024,
      max_vusers: 100,
    }
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: [] }}
        generatorInventory={[generator]}
        protocol="bruno"
      />,
    ))

    const option = container.querySelector('.new-test-generator-option')
    expect(option?.querySelector<HTMLInputElement>('input[type=checkbox]')?.disabled).toBe(true)
    expect(option?.textContent).toContain('No available capacity')
  })

  it('inherit clears the per-group assignment', () => {
    act(() => root.render(<Harness availableTargets={['gen-a:9090']} protocol="http" />))
    act(() => radio('Pin to host(s)').click())
    expect(radio('Pin to host(s)').checked).toBe(true)

    act(() => radio('Inherit scenario default').click())
    expect(radio('Inherit scenario default').checked).toBe(true)
    expect(container.textContent).toContain('Inherits the scenario')
  })

  it.each([
    ['region', { mode: 'region_weighted' as const, region_weights: { east: 70, west: 30 } }, 'Region-weighted placement', 'east=70, west=30'],
    ['load-zone', { mode: 'load_zone_weighted' as const, load_zone_weights: { blue: 60, green: 40 } }, 'Load-zone-weighted placement', 'blue=60, green=40'],
  ])('shows a stored %s weighted assignment truthfully and can clear it to auto', (_name, assignment, label, summary) => {
    act(() => root.render(<AssignmentHarness initialAssignment={assignment} scope="scenario" />))

    expect(radio(label).checked).toBe(true)
    expect(container.textContent).toContain(summary)
    expect(radio('Automatic placement').checked).toBe(false)

    act(() => radio('Automatic placement').click())
    expect(radio('Automatic placement').checked).toBe(true)
    expect(container.textContent).toContain('Uses automatic placement')
  })

  it('preserves generator-loss tolerance while editing pinned hosts', () => {
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'specific_hosts', selected_targets: ['gen-a'], tolerate_generator_loss: true }}
        availableTargets={['gen-a', 'gen-b']}
        scope="scenario"
      />,
    ))

    const genB = Array.from(container.querySelectorAll('.new-test-generator-option')).find(option => option.textContent?.includes('gen-b'))
      ?.querySelector<HTMLInputElement>('input[type=checkbox]')
    expect(genB).toBeDefined()
    act(() => genB?.click())

    const serialized = JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    expect(serialized).toMatchObject({
      mode: 'specific_hosts',
      selected_targets: ['gen-a', 'gen-b'],
      shard_strategy: 'capacity_weighted',
      tolerate_generator_loss: true,
    })
  })

  it('authors region weights from live inventory and toggles generator-loss tolerance', () => {
    const generator = (target: string, region: string): SystemInventoryGenerator => ({
      target,
      generator_id: target,
      region,
      status: 'connected',
      available_capacity: 100,
      active_vusers: 0,
      cpu_percent: 0,
      memory_bytes: 0,
      goroutine_count: 0,
      cpu_limit_cores: 4,
      memory_limit_bytes: 1024,
      max_vusers: 100,
    })
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'auto' }}
        generatorInventory={[generator('gen-east', 'east'), generator('gen-west', 'west')]}
        scope="scenario"
      />,
    ))

    act(() => radio('Region-weighted placement').click())
    let serialized = JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    expect(serialized).toMatchObject({ mode: 'region_weighted', region_weights: { east: 1, west: 1 } })

    const east = container.querySelector<HTMLInputElement>('[aria-label="Region east weight"]')
    expect(east).toBeTruthy()
    act(() => {
      if (east) {
        const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
        setter.call(east, '70')
        east.dispatchEvent(new Event('input', { bubbles: true }))
      }
    })
    const loss = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Tolerate generator loss'))?.querySelector<HTMLInputElement>('input')
    act(() => loss?.click())

    serialized = JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    expect(serialized).toMatchObject({ mode: 'region_weighted', region_weights: { east: 70, west: 1 }, tolerate_generator_loss: true })
  })

  it('authors a load-zone weight without inventory', () => {
    act(() => root.render(<AssignmentHarness initialAssignment={{ mode: 'auto' }} scope="scenario" />))
    act(() => radio('Load-zone-weighted placement').click())

    const name = container.querySelector<HTMLInputElement>('[aria-label="New placement weight name"]')
    const value = container.querySelector<HTMLInputElement>('[aria-label="New placement weight value"]')
    const setInput = (input: HTMLInputElement | null, next: string) => {
      if (!input) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(input, next)
      input.dispatchEvent(new Event('input', { bubbles: true }))
    }
    act(() => {
      setInput(name, 'zone-a')
      setInput(value, '100')
    })
    const add = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Add weight')
    act(() => add?.click())

    const serialized = JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    expect(serialized).toMatchObject({ mode: 'load_zone_weighted', load_zone_weights: { 'zone-a': 100 } })
  })

  it('restricts weighted placement and generator-loss tolerance to scenario scope', () => {
    act(() => root.render(
      <AssignmentHarness
        initialAssignment={{ mode: 'region_weighted', region_weights: { east: 1 }, tolerate_generator_loss: true }}
      />,
    ))

    expect(() => radio('Region-weighted placement')).toThrow()
    expect(() => radio('Load-zone-weighted placement')).toThrow()
    expect(container.textContent).toContain('Weighted placement is not supported for individual groups')
    expect(container.textContent).toContain('Generator-loss tolerance is executed only at scenario scope')
    expect(container.textContent).not.toContain('Tolerate generator loss')

    const useAutomatic = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent === 'Use automatic placement')
    act(() => useAutomatic?.click())
    const serialized = JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    expect(serialized).toEqual({ mode: 'auto' })
  })

  it('requires at least one host and positive whole-number weights', () => {
    act(() => root.render(<AssignmentHarness initialAssignment={{ mode: 'specific_hosts', selected_targets: [] }} />))
    expect(container.textContent).toContain('Select at least one load generator host')

    act(() => root.render(
      <AssignmentHarness
        key="weighted"
        initialAssignment={{ mode: 'region_weighted', region_weights: { east: 2 } }}
        scope="scenario"
      />,
    ))
    const weight = container.querySelector<HTMLInputElement>('[aria-label="Region east weight"]')
    expect(weight?.step).toBe('1')
    act(() => {
      if (!weight) return
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
      setter.call(weight, '1.5')
      weight.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).toContain('Weights must be positive whole numbers')
    const serialized = JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    expect(serialized.region_weights).toEqual({ east: 2 })
  })

  it('toggles controller-failover continuity at scenario scope, combines it with loss tolerance, and keeps it across mode changes', () => {
    act(() => root.render(<AssignmentHarness initialAssignment={{ mode: 'auto' }} availableTargets={['gen-a']} scope="scenario" />))
    const serialized = () => JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    const continuity = () => container.querySelector<HTMLInputElement>('input[aria-label="Continue through controller failover"]')
    const cards = () => Array.from(container.querySelectorAll<HTMLLabelElement>('.scenario-option-card'))
    const lossTolerance = () => cards().find(card => card.textContent?.includes('Tolerate generator loss'))?.querySelector<HTMLInputElement>('input[type=checkbox]')

    expect(continuity()).not.toBeNull()
    act(() => continuity()?.click())
    expect(serialized()).toMatchObject({ mode: 'auto', failover_policy: 'continue' })
    expect(lossTolerance()?.disabled).toBe(false)
    act(() => lossTolerance()?.click())
    expect(serialized()).toMatchObject({ mode: 'auto', failover_policy: 'continue', tolerate_generator_loss: true })
    expect(container.textContent).toContain('lost while no controller is leading fails the run')

    act(() => radio('Pin to host(s)').click())
    expect(serialized()).toMatchObject({ mode: 'specific_hosts', failover_policy: 'continue', tolerate_generator_loss: true })

    act(() => continuity()?.click())
    expect(serialized().failover_policy).toBeUndefined()
    expect(serialized().tolerate_generator_loss).toBe(true)
  })

  it('does not offer controller-failover continuity at group scope', () => {
    act(() => root.render(<AssignmentHarness initialAssignment={{ mode: 'auto' }} availableTargets={['gen-a']} scope="group" />))
    expect(container.querySelector('input[aria-label="Continue through controller failover"]')).toBeNull()
  })
  it('preserves the spare quota budget across placement changes and removes it when loss tolerance is disabled', () => {
    act(() => root.render(<AssignmentHarness initialAssignment={{ mode: 'auto', tolerate_generator_loss: true, recovery_spare_vusers: 200 }} availableTargets={['gen-a']} scope="scenario" />))
    const serialized = () => JSON.parse(container.querySelector('[data-testid="assignment-json"]')?.textContent || '{}')
    const input = container.querySelector<HTMLInputElement>('[aria-label="Recovery spare VUser budget"]')!
    expect(input.value).toBe('200')
    act(() => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(input, '300')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(serialized().recovery_spare_vusers).toBe(300)
    act(() => radio('Pin to host(s)').click())
    expect(serialized().recovery_spare_vusers).toBe(300)
    const card = Array.from(container.querySelectorAll<HTMLLabelElement>('.scenario-option-card')).find(value => value.textContent?.includes('Tolerate generator loss'))!
    act(() => card.querySelector<HTMLInputElement>('input')!.click())
    expect(serialized().recovery_spare_vusers).toBeUndefined()
    expect(container.querySelector('[aria-label="Recovery spare VUser budget"]')).toBeNull()
  })

  it('does not offer a spare budget at group scope', () => {
    act(() => root.render(<AssignmentHarness initialAssignment={{ mode: 'auto' }} availableTargets={['gen-a']} scope="group" />))
    expect(container.querySelector('[aria-label="Recovery spare VUser budget"]')).toBeNull()
  })

})
