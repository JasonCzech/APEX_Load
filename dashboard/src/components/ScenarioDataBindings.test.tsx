// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { StoredScript, ScenarioDataPoolBinding } from '../types'
import ScenarioDataBindings from './ScenarioDataBindings'

const { api, sharedAssets } = vi.hoisted(() => ({ api: { getScript: vi.fn(), getDataPool: vi.fn() }, sharedAssets: { props: null as null | Record<string, unknown> } }))
vi.mock('../hooks/useApi', () => ({ useApi: () => api }))
// Shared assets opens over the builder; its own behaviour is SharedAssetsDialog's test.
vi.mock('./SharedAssetsDialog', () => ({ default: (props: { onClose: () => void }) => { sharedAssets.props = props; return <button type="button" onClick={props.onClose}>Close Shared assets</button> } }))

const stored: StoredScript = {
  id: 'checkout', name: 'Checkout', protocol: 'http', version: '1', created_at: '',
  script: { name: 'Checkout', version: '1', protocol: 'http', config: {}, actions: [{ name: 'buy', type: 'request', target: '/{{data.users.email}}' }],
    data_sources: [{ name: 'users', type: 'csv', strategy: 'random', first_row_header: true, inline_data: 'email,segment\n"demo@example.test","retail, priority"' }],
    parameters: [
      { name: 'customer', type: 'file', data_source: 'users', column: 'email', selection: 'unique', update: 'once', on_exhaustion: 'error', start_row: 2, auto_allocate: true },
      { name: 'segment', type: 'file', data_source: 'users', column: 'segment', same_line_as: 'customer' },
      { name: 'count', type: 'counter', start: 0, end: 10, step: 2 },
      { name: 'region', type: 'list', values: ['east', 'west'] },
    ],
  },
}

describe('ScenarioDataBindings script-level data', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => {
    api.getScript.mockReset()
    api.getDataPool.mockReset()
    container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
  })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })
  async function render(scripts: StoredScript[], bindings: ScenarioDataPoolBinding[] = []) {
    await act(async () => root.render(<ScenarioDataBindings scripts={scripts} bindings={bindings} pools={[]} onChange={() => {}} canManage={false} />))
  }
  it.each(['read_only', 'live'] as const)('describes empty %s seeds without changing snapshot guidance', async access_mode => {
    api.getDataPool.mockResolvedValue({ rows: [], total: 0 })
    await render([], [{ pool_id: 'pool', alias: 'work', access_mode, selection_strategy: access_mode === 'live' ? 'manual' : 'sequential' }])
    const preview = [...container.querySelectorAll('summary')].find(element => element.textContent === 'Preview rows and import CSV')!.parentElement as HTMLDetailsElement
    await act(async () => { preview.open = true; preview.dispatchEvent(new Event('toggle')) })
    expect(preview.textContent).toContain(access_mode === 'live' ? 'producers can add runtime rows' : 'Add rows before configuring a run')
    expect(preview.textContent).not.toContain(access_mode === 'live' ? 'Add rows before configuring a run' : 'producers can add runtime rows')
  })
  it('shows source previews, parameter mappings, runtime defaults and configured settings without fetching full scripts again', async () => {
    await render([stored])
    const panel = container.querySelector('.scenario-script-data')!
    expect(panel.textContent).toContain('1 of 1')
    expect(panel.textContent).toContain('1 sources · 4 parameters')
    expect(panel.textContent).toContain('Unique selection · On exhaustion: Error · Start row: 2 · Auto-allocate rows')
    expect(panel.textContent).toContain('Same row as customer')
    expect(panel.textContent).toContain('Each iteration')
    expect(panel.textContent).toContain('Start: 0 · End: 10 · Step: 2')
    expect(panel.querySelector('[aria-label="Data preview for users"]')?.textContent).toContain('"retail, priority"')
    expect(panel.querySelector('input, select, textarea')).toBeNull()
    expect(container.textContent).not.toContain('no script source or scenario pool supplies it')
    expect(api.getScript).not.toHaveBeenCalled()
  })
  it('shows loading, partial failures with script names and retry recovery', async () => {
    const summary = { ...stored, script: undefined }
    let resolve!: (value: StoredScript) => void
    api.getScript.mockImplementationOnce(() => new Promise<StoredScript>(done => { resolve = done }))
    await render([summary])
    expect(container.textContent).toContain('Loading script data and parameters')
    await act(async () => resolve(stored))
    const missing = { ...summary, id: 'missing', name: 'Unavailable checkout' }
    api.getScript.mockRejectedValueOnce(new Error('offline'))
    await render([stored, missing])
    expect(container.textContent).toContain('1 of 2')
    expect(container.textContent).toContain('Unavailable checkoutData unavailable')
    api.getScript.mockResolvedValueOnce({ ...stored, id: 'missing' })
    const retry = [...container.querySelectorAll('button')].find(button => button.textContent === 'Retry script data')!
    await act(async () => retry.click())
    expect(container.textContent).toContain('2 of 2')
    expect(container.textContent).not.toContain('Data unavailable')
  })
  it('handles no selection, no local data and external sources', async () => {
    await render([])
    expect(container.textContent).toContain('Select scripts in the Start step')
    await render([{ ...stored, script: { ...stored.script!, data_sources: [], parameters: [] } }])
    expect(container.textContent).toContain('No script-level data sources or parameters')
    await render([{ ...stored, version: '2', script: { ...stored.script!, parameters: [], data_sources: [{ name: 'external', type: 'csv', strategy: 'sequential', path: '/fixtures/users.csv' }] } }])
    expect(container.textContent).toContain('/fixtures/users.csv')
    expect(container.textContent).toContain('External data contents are not loaded here')
    expect(container.textContent).toContain('No header row')
  })
  it('shows the effective staged asset and does not present an encoded file as readable data', async () => {
    await render([{ ...stored, script: { ...stored.script!, data_sources: [
      { name: 'staged', type: 'csv', strategy: 'sequential', staged_asset: 'asset-123', inline_data: 'old,row' },
      { name: 'binary', type: 'file', strategy: 'sequential', encoding: 'BASE64', inline_data: 'c2VjcmV0' },
    ], parameters: [
      { name: 'missing_file', type: 'file' },
      { name: 'empty_list', type: 'list', values: [] },
    ] } }])
    const staged = [...container.querySelectorAll('.scenario-script-source')].find(element => element.textContent?.includes('staged'))!
    expect(staged.textContent).toContain('asset-123')
    expect(staged.textContent).toContain('staged asset takes precedence')
    expect(staged.textContent).not.toContain('old,row')
    const binary = [...container.querySelectorAll('.scenario-script-source')].find(element => element.textContent?.includes('binary'))!
    expect(binary.textContent).toContain('Encoded file contents are not previewed')
    expect(binary.textContent).not.toContain('c2VjcmV0')
    expect(container.textContent).toContain('No data source configured')
    expect(container.textContent).toContain('No values configured')
  })
  it('shows explicit values before a source and flags parameters without a required column', async () => {
    await render([{ ...stored, script: { ...stored.script!, parameters: [
      { name: 'inline_wins', type: 'file', data_source: 'users', column: 'email', values: ['one', 'two'] },
      { name: 'no_column', type: 'file', data_source: 'users' },
    ] } }])
    const rows = [...container.querySelectorAll('.scenario-script-parameters tbody tr')]
    expect(rows[0].textContent).toContain('2 configured values')
    expect(rows[0].textContent).not.toContain('users')
    expect(rows[1].textContent).toContain('No column selected')
  })
  it('does not describe generated or host parameters as row-selected data', async () => {
    await render([{ ...stored, script: { ...stored.script!, parameters: [
      { name: 'machine', type: 'host', values: ['internal.example.test'] },
      { name: 'random', type: 'random_number', min: 1, max: 3, values: ['ignored'] },
    ] } }])
    const rows = [...container.querySelectorAll('.scenario-script-parameters tbody tr')]
    expect(rows[0].textContent).toContain('Host override configured')
    expect(rows[0].textContent).not.toContain('selection')
    expect(rows[0].textContent).not.toContain('On exhaustion')
    expect(rows[1].textContent).toContain('Generated at runtime')
    expect(rows[1].textContent).not.toContain('configured values')
  })
  it('hides the local panel only when a shared pool is attached and restores it on removal', async () => {
    await render([stored], [{ pool_id: '', alias: '', access_mode: 'read_only', selection_strategy: 'sequential' }])
    expect(container.querySelector('.scenario-script-data')).not.toBeNull()
    await render([stored], [{ pool_id: 'pool', alias: 'users', access_mode: 'read_only', selection_strategy: 'sequential' }])
    expect(container.querySelector('.scenario-script-data')).toBeNull()
    await render([stored])
    expect(container.querySelector('.scenario-script-data')).not.toBeNull()
  })
  it('does not let one script source hide another script’s missing data reference', async () => {
    await render([stored, { ...stored, id: 'other', name: 'Other script', script: { ...stored.script!, data_sources: [] } }])
    const need = container.querySelector('[aria-label="Data the scripts need"] li')!
    expect(need.textContent).toContain('usersNot bound')
    expect(need.textContent).toContain('Used by Other script · columns email · reads a row each iteration')
    expect(need.querySelector('button[aria-label="Bind users"]')).not.toBeNull()
  })
  it('opens the pool in Shared assets over the builder, and shows its rows as they are when it closes', async () => {
    api.getDataPool.mockResolvedValue({ rows: [{ id: 'r1', data: { email: 'a@example.test' } }], total: 1 })
    const onPoolsChanged = vi.fn()
    await act(async () => root.render(<ScenarioDataBindings projectId="project-1" scripts={[]} bindings={[{ pool_id: 'pool', alias: 'users', access_mode: 'read_only', selection_strategy: 'sequential' }]} pools={[]} onChange={() => {}} canManage canManageLiveOutput onPoolsChanged={onPoolsChanged} />))
    const preview = [...container.querySelectorAll('summary')].find(element => element.textContent === 'Preview rows and import CSV')!.parentElement as HTMLDetailsElement
    await act(async () => { preview.open = true; preview.dispatchEvent(new Event('toggle')) })
    expect(preview.textContent).toContain('1 rows in shared pool')
    await act(async () => [...preview.querySelectorAll('button')].find(button => button.textContent === "Edit this pool's rows")!.click())
    expect(sharedAssets.props).toMatchObject({ projectId: 'project-1', section: 'test-data', resourceId: 'pool' })
    api.getDataPool.mockResolvedValue({ rows: [], total: 2 })
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Close Shared assets')!.click())
    expect(onPoolsChanged).toHaveBeenCalledOnce()
    expect(api.getDataPool).toHaveBeenCalledTimes(2)
    expect(preview.textContent).toContain('2 rows in shared pool')
  })
  it('bounds large data previews and ignores obsolete fetch responses after selection changes', async () => {
    let resolve!: (value: StoredScript) => void
    api.getScript.mockImplementationOnce(() => new Promise<StoredScript>(done => { resolve = done }))
    await render([{ ...stored, script: undefined }])
    await render([{ ...stored, id: 'new', script: { ...stored.script!, data_sources: [{ name: 'large', type: 'csv', strategy: 'sequential', inline_data: 'x'.repeat(5000) }] } }])
    await act(async () => resolve(stored))
    expect(container.querySelector('[aria-label="Data preview for large"]')?.textContent?.length).toBe(4000)
    expect(container.querySelector('[aria-label="Data preview for users"]')).toBeNull()
  })
})

describe('ScenarioDataBindings adding a binding', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })
  const pools = [
    { id: 'pool-1', name: 'Customer accounts', created_at: '', updated_at: '' },
    { id: 'pool-2', name: 'Orders', created_at: '', updated_at: '' },
  ]
  async function render(bindings: ScenarioDataPoolBinding[], onChange: (next: ScenarioDataPoolBinding[]) => void) {
    await act(async () => root.render(<ScenarioDataBindings scripts={[]} bindings={bindings} pools={pools} onChange={onChange} canManage={false} />))
  }
  const choose = async (index: number, value: string) => {
    const select = [...container.querySelectorAll('label')].filter(label => label.querySelector('span')?.textContent === 'Data pool')[index].querySelector('select')!
    await act(async () => { select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
  }

  it('offers one way to add a binding and holds its errors until it is touched', async () => {
    const onChange = vi.fn()
    await render([], onChange)
    expect(container.querySelector('select')).toBeNull()
    await act(async () => [...container.querySelectorAll('button')].find(button => button.textContent === 'Add data binding')!.click())
    const added = onChange.mock.calls[0][0] as ScenarioDataPoolBinding[]
    await render(added, onChange)
    expect(container.querySelector('[role="alert"]')).toBeNull()
    expect(container.textContent).toContain('Choose a data pool, or a live table above.')
    // A typed but invalid alias is touched, so its error shows.
    await render([{ ...added[0], alias: '1bad' }], onChange)
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Binding 1')
  })

  it('names a binding after its pool unless the alias was typed', async () => {
    const onChange = vi.fn()
    await render([{ pool_id: '', alias: '', access_mode: 'read_only', selection_strategy: 'sequential' }, { pool_id: 'pool-2', alias: 'customer_accounts', access_mode: 'read_only', selection_strategy: 'sequential' }], onChange)
    await choose(0, 'pool-1')
    expect(onChange.mock.lastCall![0][0]).toMatchObject({ pool_id: 'pool-1', alias: 'customer_accounts_2' })
    await render([{ pool_id: 'pool-1', alias: 'customer_accounts_2', access_mode: 'read_only', selection_strategy: 'sequential' }, { pool_id: 'pool-2', alias: 'customer_accounts', access_mode: 'read_only', selection_strategy: 'sequential' }], onChange)
    await choose(0, 'pool-2')
    expect(onChange.mock.lastCall![0][0]).toMatchObject({ pool_id: 'pool-2', alias: 'orders' })
    await render([{ pool_id: 'pool-1', alias: 'buyers', access_mode: 'read_only', selection_strategy: 'sequential' }], onChange)
    await choose(0, 'pool-2')
    expect(onChange.mock.lastCall![0][0]).toMatchObject({ pool_id: 'pool-2', alias: 'buyers' })
  })
})

describe('ScenarioDataBindings binding what the scripts need', () => {
  let container: HTMLDivElement
  let root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(async () => { await act(async () => root.unmount()); container.remove() })
  const worker: StoredScript = { id: 'worker', name: 'Worker', protocol: 'http', version: '1', created_at: '', script: { name: 'Worker', version: '1', protocol: 'http', config: {}, actions: [
    { name: 'Lease', type: 'live_table', target: '', params: { alias: 'jobs', operation: 'lease', result_prefix: 'job' } },
    { name: 'Call', type: 'request', target: '/jobs/{{var.job.values.id}}' },
  ] } }
  const pools = [{ id: 'pool-1', name: 'Jobs', created_at: '', updated_at: '', columns: ['key'] }]
  async function render(bindings: ScenarioDataPoolBinding[], onChange: (next: ScenarioDataPoolBinding[]) => void) {
    await act(async () => root.render(<ScenarioDataBindings scripts={[worker]} bindings={bindings} pools={pools} onChange={onChange} canManage={false} />))
  }

  it('binds an alias the scripts lease from as a live table, and flags what the table lacks', async () => {
    const onChange = vi.fn()
    await render([], onChange)
    await act(async () => container.querySelector<HTMLButtonElement>('button[aria-label="Bind jobs"]')!.click())
    const [bound] = onChange.mock.lastCall![0] as ScenarioDataPoolBinding[]
    expect(bound).toMatchObject({ alias: 'jobs', access_mode: 'live', scope: 'run', live_options: { seed_policy: 'current_at_start' } })
    await render([{ ...bound, pool_id: 'pool-1' }], onChange)
    expect(container.querySelector('[aria-label="Data the scripts need"]')!.textContent).toContain('Bound: live table for this run')
    const choices = [...container.querySelectorAll<HTMLInputElement>('.scenario-data-table-choice input')]
    expect(choices.map(choice => [choice.value, choice.checked])).toEqual([['snapshot', false], ['run', true], ['project', false]])
    expect([...container.querySelectorAll('.scenario-data-mismatch')].map(node => node.textContent)).toEqual(['jobs has no column id, which Worker uses.'])
    // A snapshot cannot run the lease at all.
    await act(async () => { choices[0].click() })
    const snapshot = (onChange.mock.lastCall![0] as ScenarioDataPoolBinding[])[0]
    expect(snapshot).toMatchObject({ access_mode: 'read_only', selection_strategy: 'sequential' })
    await render([snapshot], onChange)
    expect(container.querySelector('.scenario-data-mismatch')!.textContent).toBe('Worker changes rows in jobs with table steps, which a read-only snapshot cannot run. Choose a live table.')
  })
})
