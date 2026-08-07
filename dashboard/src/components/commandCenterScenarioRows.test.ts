import { describe, expect, it } from 'vitest'
import { buildScenarioRunRows, type CommandGroup } from './commandCenterScenarioRows'

function commandGroup(overrides: Partial<CommandGroup> = {}): CommandGroup {
  return {
    key: 'g1',
    name: 'Checkout',
    protocol: 'http',
    current: 50,
    target: 50,
    targetVusers: 50,
    targetMode: 'vusers',
    tps: 42.5,
    art: 120,
    errorPct: 0.2,
    p99: 410,
    transactions: 1200,
    errors: 3,
    color: '#4f8ef7',
    ...overrides,
  }
}

describe('buildScenarioRunRows throughput gap', () => {
  it('leaves the gap undefined when no throughput target exists', () => {
    const rows = buildScenarioRunRows([commandGroup()])
    const groupRow = rows.find(row => row.id === 'group-g1')

    expect(groupRow?.targetTps).toBeUndefined()
    // A vuser-driven run has no throughput target, so the Gap column must render
    // '--' rather than a fabricated 0.00% that ties across every group.
    expect(groupRow?.throughputGap).toBeUndefined()
  })

  it('still reports the gap for a tps-driven group', () => {
    const rows = buildScenarioRunRows([
      commandGroup({ targetMode: 'tps', target: 100, targetTps: 100, tps: 80 }),
    ])
    const groupRow = rows.find(row => row.id === 'group-g1')

    expect(groupRow?.targetTps).toBe(100)
    expect(groupRow?.throughputGap).toBeCloseTo(20, 6)
  })
})
