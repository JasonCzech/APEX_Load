// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import PodCapacityMeter from './PodCapacityMeter'
import type { PodCapacitySnapshot } from '../types/capacity'

describe('PodCapacityMeter', () => {
  it('shows headroom separately from reserved capacity and does not fabricate availability', () => {
    const container = document.createElement('div')
    const root = createRoot(container)
    const vector = { cpu_cores: 4, memory_mb: 8000, network_mbps: 800, vusers: 8000 }
    const capacity: PodCapacitySnapshot = { state: 'ready', model_version: 'peak_capacity_v1', incarnation: 'one', headroom_pct: 20, schedulable: vector, reserved: vector, remaining: vector, reserved_pct: 50, remaining_pct: 50, observed_at_unix_ms: Date.now(), confidence: 'estimated', dedicated: false }
    act(() => root.render(<PodCapacityMeter capacity={capacity} />))
    expect(container.textContent).toContain('50% reserved / 50% remaining')
    expect(container.textContent).toContain('20% headroom excluded')
    expect(container.querySelector('progress')?.value).toBe(50)
    act(() => root.render(<PodCapacityMeter capacity={{ ...capacity, state: 'unreconciled' }} />))
    expect(container.querySelector('progress')).toBeNull()
    expect(container.textContent).toContain('Peak capacity unavailable')
    act(() => root.unmount())
  })
})
