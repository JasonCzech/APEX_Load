import { describe, expect, it } from 'vitest'
import { buildGroupRunRows, buildTransactionRunRows, transactionTableSections, type CommandGroup, type GroupRunRow, type TransactionRunRow } from './commandCenterScenarioRows'
import { groupTransactionKey } from './testExecutionMetricsModel'

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

  it('gives each group that ran a transaction its own row, at its own averaged rate', () => {
    const figures = { avg_ms: 10, p50_ms: 1, p90_ms: 1, p99_ms: 1 }
    const metrics = { transactions: {
      Login: { name: 'Login', tps: 7, count: 9, errors: 1, ...figures, groups: ['buyers', 'sellers'], by_group: {
        sellers: { tps: 4, count: 4, errors: 0, ...figures, avg_ms: 30 },
        buyers: { tps: 3, count: 5, errors: 1, ...figures },
      } },
      Search: { name: 'Search', tps: 2, count: 2, errors: 0, ...figures, groups: ['buyers'] },
      Legacy: { name: 'Legacy', tps: 1, count: 1, errors: 0, ...figures },
    } } as never
    const rows = buildTransactionRunRows(metrics, undefined, { Search: 1.5 }, { [groupTransactionKey('buyers', 'Login')]: 2.5, [groupTransactionKey('buyers', 'Search')]: 1.8 })
    expect(rows.map(row => [row.name, row.group, row.attempts, row.tps])).toEqual([
      ['Login', 'buyers', 5, 2.5],
      ['Login', 'sellers', 4, 4],
      ['Search', 'buyers', 2, 1.8],
      ['Legacy', undefined, 1, 1],
    ])
    expect(rows[1].art).toBe(30)
    expect(new Set(rows.map(row => row.id)).size).toBe(rows.length)
    // An unattributed transaction in a run of one group is that group's.
    expect(buildTransactionRunRows(metrics, undefined, undefined, undefined, 'buyers').find(row => row.name === 'Legacy')?.group).toBe('buyers')
  })

  it('keeps the last transactions when a window completed none', () => {
    const snapshot = { Login: { name: 'Login', tps: 3, count: 3, errors: 0, avg_ms: 10, p50_ms: 1, p90_ms: 1, p99_ms: 1 } } as never
    expect(buildTransactionRunRows({ transactions: {} } as never, snapshot).map(row => row.name)).toEqual(['Login'])
  })
})

describe('transactionTableSections', () => {
  const tx = (name: string, group: string | undefined, figures: Partial<TransactionRunRow> = {}): TransactionRunRow => ({
    id: `transaction-${group ?? ''}:${name}`, name, group, protocol: 'transaction', tps: 1, attempts: 10, errors: 0, errorPct: 0, art: 100, p50: 90, p90: 150, p95: 180, p99: 200, ...figures,
  })
  const groupRow = (name: string, figures: Partial<GroupRunRow> = {}): GroupRunRow => ({
    id: `group-${name}`, name, protocol: 'http', rate: 0, attempts: 0, errors: 0, art: 120, p50: 100, p90: 160, p95: 190, p99: 250, ...figures,
  })

  it('nests transactions under their groups with the group totals and response times', () => {
    const rows = [tx('Login', 'Browse', { tps: 2, attempts: 20, errors: 2, art: 40 }), tx('Search', 'Browse', { tps: 3, attempts: 30, art: 140 }), tx('Login', 'Buy', { attempts: 5 })]
    const sections = transactionTableSections(rows, rows, [groupRow('Browse', { art: 140, p95: 210 }), groupRow('Buy')], { key: 'name', direction: 'asc' })
    expect(sections.map(section => section.label)).toEqual(['Browse', 'Buy'])
    const browse = sections[0]
    expect(browse.rows.map(row => row.name)).toEqual(['Login', 'Search'])
    // ART is its transactions' weighted by attempts; percentiles are the group's own.
    expect(browse.summary).toMatchObject({ tps: 5, attempts: 50, errors: 2, errorPct: 4, art: 100, p95: 210 })
  })

  it('keeps group totals whole while a filter hides some transactions, and lists unattributed rows last', () => {
    const all = [tx('Login', 'Browse', { attempts: 20 }), tx('Search', 'Browse', { attempts: 30 }), tx('Health', undefined, { attempts: 4, art: 50 })]
    const sections = transactionTableSections([all[0], all[2]], all, [groupRow('Browse')], { key: 'attempts', direction: 'desc' })
    expect(sections.map(section => section.label)).toEqual(['Browse', 'No group'])
    expect(sections[0].rows.map(row => row.name)).toEqual(['Login'])
    expect(sections[0].summary.attempts).toBe(50)
    expect(sections[1].summary.art).toBe(50)
    expect(Number.isNaN(sections[1].summary.p50)).toBe(true)
  })

  it('orders groups by the table sort', () => {
    const rows = [tx('A', 'Fast', { art: 10 }), tx('B', 'Slow', { art: 900 })]
    const sections = transactionTableSections(rows, rows, [groupRow('Fast', { art: 10 }), groupRow('Slow', { art: 900 })], { key: 'art', direction: 'desc' })
    expect(sections.map(section => section.label)).toEqual(['Slow', 'Fast'])
  })
})
