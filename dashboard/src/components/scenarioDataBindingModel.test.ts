import { describe, expect, it } from 'vitest'

import type { ScenarioDataPoolBinding } from '../types'
import { describeDataBinding, liveBindingSummary, scenarioLiveTables } from './scenarioDataBindingModel'

const names = (id: string) => ({ 'pool-customers': 'Customers', 'pool-jobs': 'Jobs' } as Record<string, string>)[id]

describe('data binding descriptions', () => {
  it('says what a launch reads, where a live table starts and what happens to it', () => {
    expect(describeDataBinding({ alias: 'customers', pool_id: 'pool-customers', access_mode: 'read_only', selection_strategy: 'unique' }, names))
      .toEqual({ text: 'customers: Customers (read-only snapshot, unique rows)', writesBack: false })
    const writeBack: ScenarioDataPoolBinding = { alias: 'work', pool_id: 'pool-jobs', access_mode: 'live', selection_strategy: 'lease', live_options: { end_mode: 'write_back' } }
    expect(describeDataBinding(writeBack, names)).toEqual({ text: 'work: live table for this run, starts from Jobs, written back to Jobs at the end, changing that pool', writesBack: true })
    expect(describeDataBinding({ alias: 'queue', pool_id: '', access_mode: 'live', selection_strategy: 'manual', live_options: { seed_policy: 'empty', end_mode: 'save_as_new', output_pool_name: 'Queue output' } }, names).text)
      .toBe('queue: live table for this run, starts empty, saved as a new pool "Queue output" at the end')
    expect(describeDataBinding({ alias: 'shared', pool_id: '', access_mode: 'live', selection_strategy: 'manual', scope: 'project', project_table_id: 't1' }, names).text)
      .toBe('shared: project live table, shared across project runs')
  })

  it('names live bindings kept in a plan and the live tables a scenario brings', () => {
    expect(liveBindingSummary({ alias: 'work', pool_id: '', access_mode: 'live', selection_strategy: 'lease' })).toBe('work — live table, this run only')
    expect(scenarioLiveTables([
      { alias: 'customers', pool_id: 'p', access_mode: 'read_only', selection_strategy: 'sequential' },
      { alias: 'shared', pool_id: '', access_mode: 'live', selection_strategy: 'manual', scope: 'project' },
    ])).toEqual({ list: 'shared (shared across project runs)', runScoped: false })
    expect(scenarioLiveTables([])).toBeNull()
  })
})
