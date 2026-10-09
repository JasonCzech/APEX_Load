// @vitest-environment jsdom
import { act, StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ImportGuidedRepair } from './ImportGuidedRepair'
import type { ScriptDefinition } from '../types/scripts'
const mock = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => mock)
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('requires a redacted preview before applying and retains the reviewed operation', async () => {
  const script = { name: 'Protected draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', preserved: { server_import_attestation_v1: 'protected:one' } } } } as ScriptDefinition
  const result = { candidate_id: 'protected:one', revision: 'protected:one', script, before: script, findings: [{ code: 'restored_body_required', action_path: 'actions[0]', source_ref: 'Action.c:3', detail: 'Restore this body' }], readiness: { replay_ready: false, blocking_codes: ['restored_body_required'] }, expires_at: '2099-09-19', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onApplied = vi.fn(); const onBusyChange = vi.fn()
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} onBusyChange={onBusyChange} />))
    expect(container.textContent).not.toContain('Apply reviewed repair')
    await act(async () => { Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Restore protected request body'))!.click() })
    expect(container.textContent).toContain('Action.c:3')
    const preview = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')!
    await act(async () => preview.click())
    expect(container.textContent).toContain('Redacted before / after')
    expect(container.querySelector('[aria-label="Repair outcome"]')?.textContent).toContain('Blocked')
    expect(container.querySelector('.lr-repair-preview-fields')?.textContent).toContain('No field paths changed')
    expect(container.querySelector('[aria-label="Before repair JSON"]')).toBeNull()
    const apply = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Apply reviewed repair')!
    mock.request.mockResolvedValueOnce({ ...result, candidate_id: 'protected:two', revision: 'protected:two' })
    await act(async () => apply.click())
    expect(onApplied).toHaveBeenCalledOnce()
    expect(onBusyChange).toHaveBeenCalledWith(true)
    expect(onBusyChange).toHaveBeenLastCalledWith(false)
    const [url, options] = mock.request.mock.calls[mock.request.mock.calls.length - 1]
    expect(url).toBe('/scripts/import/repairs/apply')
    expect(JSON.parse(options.body).revision).toBe('protected:one')
    expect(JSON.parse(options.body).operation).toEqual({ type: 'restore_body', body: '' })
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('groups consumers, offers contextual repairs, and retains unresolved occurrences after partial apply', async () => {
  const makeScript = (token: string) => ({ name: 'Grouped draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [0, 1].map(i => ({ name: `Request ${i}`, type: 'request', target: '/', body: '{{var.tstamp}}' })), metadata: { import: { preserved: { server_import_attestation_v1: token } } } }) as ScriptDefinition
  const script = makeScript('protected:one')
  const findings = [0, 1].map(i => ({ code: 'missing_producer:tstamp', action_path: `actions[${i}]`, source_ref: `Action.c:${i + 3}`, detail: '', gate: 'execution' }))
  const initial = { candidate_id: 'protected:one', revision: 'protected:one', script, before: script, findings, readiness: { replay_ready: false, blocking_codes: ['missing_producer:tstamp'] }, expires_at: '2099-09-19', fidelity_report: {} }
  const partial = { ...initial, findings: [findings[1]] }
  mock.request.mockResolvedValueOnce(initial).mockResolvedValueOnce(partial).mockResolvedValueOnce({ ...partial, candidate_id: 'protected:two', revision: 'protected:two', script: makeScript('protected:two') })
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onApplied = vi.fn()
  const button = (text: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes(text))!
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
    expect(container.textContent).toContain('2 affected locations')
    await act(async () => button('Missing value: tstamp').click())
    const operations = container.querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')!
    expect(operations.value).toBe('bind_variable')
    expect(Array.from(operations.options).map(o => o.value)).not.toContain('restore_body')
    const input = container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'existingTimestamp')
      input.dispatchEvent(new Event('input', { bubbles: true }))
    })
    await act(async () => button('Preview repair').click())
    await act(async () => button('Apply reviewed repair').click())
    expect(onApplied).toHaveBeenCalledOnce()
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
    expect(container.textContent).toContain('1 affected location')
    expect(container.textContent).toContain('0 resolved in this session')
    const payload = JSON.parse(mock.request.mock.calls[mock.request.mock.calls.length - 1][1].body)
    expect(payload.action_path).toBe('actions[0]')
    expect(payload.operation).toEqual({ type: 'bind_variable', producer: 'existingTimestamp' })
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('keeps expired draft evidence inspectable but disables repair and ignores late scope responses', async () => {
  const script = { name: 'Expired', protocol: 'http', actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:expired' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:expired', revision: 'protected:expired', script, findings: [{ code: 'legacy_tls_replacement', action_path: 'actions[0]', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2000-01-01', fidelity_report: {} }
  mock.request.mockResolvedValueOnce(result)
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
    await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
    expect(container.textContent).toContain('has expired')
    expect(Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')?.disabled).toBe(true)
    let finish: (value: unknown) => void = () => {}
    mock.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const onReview = vi.fn()
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="other" onApplied={vi.fn()} onReview={onReview} />))
    await act(async () => root.unmount())
    await act(async () => finish(result))
    expect(onReview).not.toHaveBeenCalled()
  } finally { container.remove(); vi.clearAllMocks() }
})

it('previews duration and iteration allocation from the data tab without applying a repair', async () => {
  const script = { name: 'Allocation', protocol: 'http', actions: [], parameters: [{ name: 'ids', type: 'list', values: ['a', 'b'], selection: 'unique', auto_allocate: true }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:allocation' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:allocation', revision: 'protected:allocation', script, findings: [], readiness: { replay_ready: true }, expires_at: '2099-01-01', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" view="data" onApplied={vi.fn()} />))
    const preview = () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview allocation')!
    await act(async () => preview().click())
    expect(JSON.parse(mock.request.mock.calls[1][1].body).operation).toEqual({ type: 'preview_allocation', vusers: 2, iterations: 0 })
    const mode = container.querySelector<HTMLSelectElement>('select')!
    await act(async () => { mode.value = 'iterations'; mode.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => preview().click())
    expect(JSON.parse(mock.request.mock.calls[2][1].body).operation).toEqual({ type: 'preview_allocation', vusers: 2, iterations: 1 })
    expect(container.textContent).not.toContain('Apply reviewed repair')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('retains allocation across tabs, calculates source row ranges, and invalidates it for a new candidate', async () => {
  const script = { name: 'Allocation', protocol: 'http', actions: [], parameters: [{ name: 'ids', type: 'list', values: ['a', 'b', 'c', 'd', 'e'], selection: 'unique', start_row: 2, auto_allocate: true }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:allocation' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:allocation', revision: 'protected:allocation', script, findings: [], readiness: { replay_ready: false }, expires_at: '2099-01-01', fidelity_report: {}, allocation: [{ name: 'ids', rows: 4, block_size: 2, population: 2 }] }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); const root = createRoot(container); const onApplied = vi.fn()
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" view="data" onApplied={onApplied} />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview allocation')!.click())
    expect(container.textContent).toContain('2–3')
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" view="hidden" onApplied={onApplied} />))
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" view="data" onApplied={onApplied} />))
    expect(container.textContent).toContain('2–3')
    expect(container.textContent).toContain('does not clear other replay blockers')
    expect(onApplied).not.toHaveBeenCalled()
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="another-project" view="data" onApplied={onApplied} />))
    expect(container.querySelector('[aria-label="Allocation result"]')).toBeNull()
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('opens the authoritative issue for a source mapping focus request', async () => {
  const script = { name: 'TLS', protocol: 'http', actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:tls' } } } } as unknown as ScriptDefinition
  mock.request.mockResolvedValue({ candidate_id: 'protected:tls', script, findings: [{ code: 'legacy_tls_replacement', action_path: 'init[0]', gate: 'execution', detail: '' }], readiness: { replay_ready: false }, expires_at: '2099-01-01' })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" focusPath="init[0]" focusRequest={1} onApplied={vi.fn()} />))
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')?.value).toBe('select_tls')
    expect(container.textContent).toContain('intentional migration change')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

 it('shares protected candidate inspection across StrictMode effect replays', async () => {
  const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:strict' } } } } as ScriptDefinition
  mock.request.mockResolvedValue({ script, findings: [], readiness: { replay_ready: true }, expires_at: '2099-09-19' })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<StrictMode><ImportGuidedRepair script={script} projectId="p" onApplied={vi.fn()} /></StrictMode>))
    expect(mock.request).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Checking current repair requirements')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it.each(['none', 'sequential', 'random'])('hides the allocation preview when selection is %s', async selection => {
  const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], parameters: selection === 'none' ? [] : [{ name: 'ids', type: 'list', selection, values: ['a'] }] } as ScriptDefinition
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" view="data" onApplied={vi.fn()} />))
    expect(container.querySelector<HTMLElement>('[aria-label="Preview unique data allocation"]')?.hidden).toBe(true)
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})
it.each(['missing', 'expired', 'locked', 'available'])('explains allocation preview availability: %s', async state => {
  const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], parameters: [{ name: 'ids', type: 'list', selection: 'unique', values: ['a'], auto_allocate: true }], metadata: { import: { preserved: { server_import_attestation_v1: state === 'missing' ? '' : 'protected:one' } } } } as ScriptDefinition
  mock.request.mockResolvedValue({ script, findings: [], readiness: {}, expires_at: state === 'expired' ? '2000-01-01' : '2099-01-01' })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" view="data" disabled={state === 'locked'} disabledReason="This draft has already been saved." onApplied={vi.fn()} />))
    expect(container.querySelector<HTMLElement>('[aria-label="Preview unique data allocation"]')?.hidden).toBe(false)
    expect(container.querySelector<HTMLFieldSetElement>('.lr-allocation-form')?.disabled).toBe(state !== 'available')
    const reasons = { missing: 'requires a protected import draft', expired: 'has expired', locked: 'already been saved', available: 'Edit rules in Parameters' }
    expect(container.textContent).toContain(reasons[state as keyof typeof reasons])
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('opens a scoped issue queue, clears its selection on navigation, and lets source links override the category', async () => {
  const script = { name: 'Scoped draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ name: 'Request', type: 'request', target: '/' }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:scoped' } } } } as ScriptDefinition
  mock.request.mockResolvedValue({ candidate_id: 'protected:scoped', script, findings: [
    { code: 'missing_producer:customer', action_path: 'actions[0]', detail: '', gate: 'execution' },
    { code: 'legacy_tls_replacement', action_path: 'config.http.tls', detail: '', gate: 'execution' },
  ], readiness: { replay_ready: false }, expires_at: '2099-01-01' })
  const container = document.createElement('div'); const root = createRoot(container)
  const onApplied = vi.fn()
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} categoryFocus={{ category: 'values', revision: 1 }} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
    expect(container.querySelector('.lr-issue-row')?.textContent).toContain('customer')
    await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} categoryFocus={{ category: 'runtime', revision: 2 }} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
    expect(container.querySelector('[aria-label="Selected issue"]')?.textContent).toContain('Select an issue')
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} focusPath="actions[0]" focusRequest={1} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(2)
    expect(container.querySelector('[aria-label="Selected issue"]')?.textContent).toContain('customer')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('discards an in-flight preview when a category handoff changes the selected issue', async () => {
 const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: '' }, actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:race' } } } } as ScriptDefinition
 const result = { candidate_id: 'protected:race', script, findings: [{ code: 'restored_body_required', action_path: 'actions[0]', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2099-01-01' }
 mock.request.mockResolvedValueOnce(result)
 const container = document.createElement('div'); const root = createRoot(container); const onApplied = vi.fn()
 try {
  await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} />))
  await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
  let finish!: (result: unknown) => void
  mock.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')!.click())
  await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} categoryFocus={{ category: 'runtime', revision: 1 }} />))
  await act(async () => finish(result))
  expect(container.querySelector('[aria-label="Redacted repair preview"]')).toBeNull()
  expect(container.textContent).not.toContain('Apply reviewed repair')
  expect(onApplied).not.toHaveBeenCalled()
 } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('retains pending repair values while navigating and never treats a reviewed limitation as a repair', async () => {
 const script = { name: 'Draft', protocol: 'http', actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:draft' } } } } as unknown as ScriptDefinition
 const findings = [{ code: 'missing_producer:token', action_path: 'actions[0]', detail: '', gate: 'execution' }, { code: 'diagnostic:warnings:0', title: 'Review assertion scope', detail: 'Only the main body is checked', gate: 'promotion' }]
 mock.request.mockResolvedValue({ script, findings, readiness: { replay_ready: false }, expires_at: '2099-01-01' })
 const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
 const button = (text: string) => [...container.querySelectorAll('button')].find(b => b.textContent?.includes(text))!
 try {
  await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
  await act(async () => button('Missing value: token').click())
  expect(container.querySelector('.lr-issue-row .import-issue-severity-tag')?.textContent).toBe('Blocker')
  expect(container.querySelector('.lr-issue-detail .import-issue-severity-tag')).toBeNull()
  expect(button('Mark reviewed with limitation')).toBeUndefined()
  const input = container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, 'retained-value'); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await act(async () => button('Review assertion scope').click())
  expect([...container.querySelectorAll('.lr-issue-row .import-issue-severity-tag')].some(tag => tag.textContent === 'Review note')).toBe(true)
  expect(container.querySelector('.lr-issue-detail')?.textContent).toContain('Script-wide')
  await act(async () => button('Mark reviewed with limitation').click())
  expect(container.textContent).toContain('Replay requirements are unchanged')
  expect(container.querySelector('.lr-issue-detail')?.textContent).toContain('Reviewed with limitation · this session')
  // A reviewed note leaves the unresolved queue until its decision is reopened.
  expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
  await act(async () => button('Reopen review decision').click())
  expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(2)
  await act(async () => button('Missing value: token').click())
  expect(container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!.value).toBe('retained-value')
  await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={vi.fn()} focusPath="actions[0]" focusRequest={2} />))
  expect(container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!.value).toBe('retained-value')
  const rows = [...container.querySelectorAll<HTMLButtonElement>('.lr-issue-row')]
  rows[0].focus()
  await act(async () => rows[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
  expect(document.activeElement).toBe(rows[1])
  rows[1].closest('details')!.open = false
  rows[0].focus()
  await act(async () => rows[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true })))
  expect(document.activeElement).toBe(rows[0])
  const heading = container.querySelector<HTMLHeadingElement>('.lr-issue-detail h4')!
  heading.focus()
  await act(async () => heading.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
  expect(document.activeElement).toBe(rows[0])
  expect(button('Back to issue list')).toBeUndefined()
  expect(mock.request).toHaveBeenCalledTimes(1) // inspection only; the review note is not a repair
 } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('moves notes marked reviewed out of the unresolved counts and reports them upward', async () => {
  const script = { name: 'Notes', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { preserved: {} } } } as unknown as ScriptDefinition
  const notes = [{ code: 'diagnostic:warnings', action_path: '', detail: 'Converted check differs.' }, { code: 'import_review_required', action_path: 'actions[1]', detail: 'Review the redirect.' }]
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onReviewedChange = vi.fn()
  const button = (text: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes(text))!
  const filterText = () => Array.from(container.querySelectorAll('option')).map(option => option.textContent).join('|')
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={vi.fn()} additionalFindings={notes} onReviewedChange={onReviewedChange} />))
    expect(filterText()).toContain('All unresolved (2)')
    await act(async () => button('Review conversion warnings').click())
    await act(async () => button('Mark reviewed with limitation').click())
    expect(filterText()).toContain('All unresolved (1)')
    expect(filterText()).toContain('Review notes (1)')
    expect(filterText()).toContain('Reviewed with limitation (1)')
    expect(container.textContent).toContain('0 resolved and 1 reviewed in this session')
    expect(Array.from(container.querySelectorAll('.lr-issue-row')).map(row => row.textContent)).not.toContainEqual(expect.stringContaining('Review conversion warnings'))
    expect(onReviewedChange).toHaveBeenLastCalledWith(['diagnostic:warnings:'])
    // An empty Blockers view is empty because there are none, not because of the review mark.
    const show = Array.from(container.querySelectorAll('select')).find(select => select.querySelector('option[value="blockers"]'))!
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(show, 'blockers'); show.dispatchEvent(new Event('change', { bubbles: true })) })
    expect(container.textContent).toContain('No issues in this view.')
    expect(container.textContent).not.toContain('marked reviewed. Show')
    await act(async () => { Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')!.set!.call(show, 'reviewed'); show.dispatchEvent(new Event('change', { bubbles: true })) })
    await act(async () => button('Review conversion warnings').click())
    await act(async () => button('Reopen review decision').click())
    expect(filterText()).toContain('All unresolved (2)')
    expect(onReviewedChange).toHaveBeenLastCalledWith([])
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})
