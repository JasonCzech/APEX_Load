import { describe, expect, it } from 'vitest'
import { buildGroupRunRows, buildTransactionRunRows, type CommandGroup } from './commandCenterScenarioRows'

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
    rate: 42.5,
    art: 120,
    errorPct: 0.2,
    p99: 410,
    attempts: 1203,
    errors: 3,
    color: '#4f8ef7',
    ...overrides,
  }
}

describe('buildGroupRunRows throughput gap', () => {
  it('leaves the gap undefined when no throughput target exists', () => {
    const [groupRow] = buildGroupRunRows([commandGroup()])

    expect(groupRow?.targetTps).toBeUndefined()
    // A vuser-driven run has no throughput target, so the Gap column must render
    // '--' rather than a fabricated 0.00% that ties across every group.
    expect(groupRow?.throughputGap).toBeUndefined()
  })

  it('reports the gap Group Load shows for a tps-driven group', () => {
    // Measured on the averaged rate against the committed target, not on the
    // last sample: one gap per group on the page.
    const [groupRow] = buildGroupRunRows([
      commandGroup({ targetMode: 'tps', target: 120, targetTps: 120, committedTargetTps: 100, tps: 95, rate: 80, throughputGapPct: 20 }),
    ])

    // The applied target the gap is measured against, not the staged 120.
    expect(groupRow?.targetTps).toBe(100)
    expect(groupRow?.throughputGap).toBe(20)
  })
})

describe('run table rows', () => {
  it('shows a group with the figures Group Load shows', () => {
    const [group] = buildGroupRunRows([commandGroup({ rate: 30, attempts: 1_729, errors: 863, errorPct: 49.9 })], {
      groups: { Checkout: { name: 'Checkout', vusers: 2, transactions: 0, errors: 2, tps: 0, avg_response_ms: 100, p50_ms: 1, p90_ms: 1, p99_ms: 1 } },
    } as never)
    expect(group.rate).toBe(30)
    expect(group.attempts).toBe(1_729)
    expect(group.errors).toBe(863)
    expect(group.errorPct).toBe(49.9)
    expect(group.art).toBe(100)
  })

  it('has no error rate for a group that has attempted nothing yet', () => {
    const [group] = buildGroupRunRows([commandGroup({ attempts: 0, errors: 0, errorPct: 0 })])
    expect(group.errorPct).toBeUndefined()
  })

  it('counts a transaction whose count already includes its errors, at the averaged TPS', () => {
    const metrics = { transactions: { Add_To_Cart: { name: 'Add_To_Cart', tps: 1, count: 2, errors: 1, avg_ms: 10, p50_ms: 1, p90_ms: 1, p99_ms: 1 } } } as never
    const [tx] = buildTransactionRunRows(metrics, undefined, { Add_To_Cart: 1.7 })
    expect(tx.tps).toBe(1.7)
    expect(tx.attempts).toBe(2)
    expect(tx.errorPct).toBe(50)
    expect(buildTransactionRunRows(metrics)[0].tps).toBe(1)
  })

  it('keeps the last transactions when a window completed none', () => {
    const snapshot = { Login: { name: 'Login', tps: 3, count: 3, errors: 0, avg_ms: 10, p50_ms: 1, p90_ms: 1, p99_ms: 1 } } as never
    expect(buildTransactionRunRows({ transactions: {} } as never, snapshot).map(row => row.name)).toEqual(['Login'])
  })
})
