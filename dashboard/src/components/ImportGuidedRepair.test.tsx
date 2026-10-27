// @vitest-environment jsdom
import { act, StrictMode, useCallback, useMemo, useState, type ComponentProps } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ImportGuidedRepair, type RepairResult } from './ImportGuidedRepair'
import { GuidedRepairForm } from './GuidedRepairForm'
import { configReferenceText, draftImportFindings, groupImportFindings, importActionEntries, type ImportFinding, type ImportIssue, type RepairFields } from './importReviewIssues'
import type { ScriptDefinition } from '../types/scripts'
const mock = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => mock)
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const candidateOf = (script: ScriptDefinition) => String(script.metadata?.import?.preserved?.server_import_attestation_v1 || '')
// The importer's side of the contract, as EnterpriseArchiveImport keeps it: it
// groups the draft (or its inspection) with its own other findings, keeps the
// inspection the panel reports, and adopts an applied repair's script.
const NO_FINDINGS: ImportFinding[] = []
function GuidedRepair({ script: given, additionalFindings = NO_FINDINGS, onApplied, onReview, ...props }: Omit<ComponentProps<typeof ImportGuidedRepair>, 'issues' | 'regroup' | 'inspection'> & { additionalFindings?: ImportFinding[] }) {
  const [applied, setApplied] = useState<{ from: ScriptDefinition; result: RepairResult } | null>(null)
  const script = applied?.from === given ? applied.result.script : given
  const scope = `${candidateOf(script)}:${props.projectId}`
  const [inspected, setInspected] = useState<{ scope: string; result: RepairResult } | null>(null)
  const inspection = inspected?.scope === scope ? inspected.result : null
  const issues = useMemo(() => groupImportFindings([...(inspection?.findings ?? draftImportFindings(script)), ...additionalFindings], script), [inspection, script, additionalFindings])
  const regroup = useCallback((result: RepairResult) => groupImportFindings([...result.findings, ...additionalFindings], result.script), [additionalFindings])
  return <ImportGuidedRepair {...props} script={script} issues={issues} inspection={inspection} regroup={regroup}
    onReview={result => { setInspected({ scope, result }); onReview?.(result) }}
    onApplied={result => { setApplied({ from: given, result }); setInspected({ scope: `${candidateOf(result.script)}:${props.projectId}`, result }); onApplied(result) }} />
}

it.each([false, true])('points to the Content-Type header for an XML body in a JSON request (protected review: %s)', async protectedReview => {
  const code = 'request_body_xml_json_content_type_mismatch'
  const script = { name: 'SOAP draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{
    name: 'SOAP request', type: 'POST', target: '/', body: '<Envelope/>', headers: { 'Content-Type': 'application/json' },
    metadata: { execution_blocker: code, source_file: 'Action.c', source_line: 7 },
  }], metadata: { import: { preserved: protectedReview ? { server_import_attestation_v1: 'protected:xml-header' } : {} } } } as ScriptDefinition
  const detail = "The request body is XML, but the Content-Type header declares JSON. Update this request's Content-Type header to the XML media type expected by the service, then re-import and validate."
  if (protectedReview) mock.request.mockResolvedValue({ candidate_id: 'protected:xml-header', revision: 'protected:xml-header', script, before: script, findings: [{ code, action_path: 'actions[0]', source_ref: 'Action.c:7', detail, gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} })
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onViewSource = vi.fn()
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} onViewSource={onViewSource} />))
    const issue = container.querySelector<HTMLButtonElement>('.lr-issue-row')!
    expect(issue.textContent).toContain('Body is XML but Content-Type declares JSON — Action.c:7')
    await act(async () => issue.click())
    expect(container.querySelector('[aria-label="Problem and impact"]')?.textContent).toContain(detail)
    expect(container.querySelector('[aria-label="Repair operation"]')).toBeNull()
    const inspect = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Inspect Action.c:7')!
    await act(async () => inspect.click())
    expect(onViewSource).toHaveBeenCalledWith('actions[0]', expect.objectContaining({ code, source_ref: 'Action.c:7' }))
    expect(script.actions[0].headers?.['Content-Type']).toBe('application/json')
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('requires a redacted preview before applying and retains the reviewed operation', async () => {
  const script = { name: 'Protected draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', preserved: { server_import_attestation_v1: 'protected:one' } } } } as ScriptDefinition
  const result = { candidate_id: 'protected:one', revision: 'protected:one', script, before: script, findings: [{ code: 'restored_body_required', action_path: 'actions[0]', source_ref: 'Action.c:3', detail: 'Restore this body' }], readiness: { replay_ready: false, blocking_codes: ['restored_body_required'] }, expires_at: '2099-09-19', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onApplied = vi.fn(); const onBusyChange = vi.fn()
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} onBusyChange={onBusyChange} />))
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
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} />))
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
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
    await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
    expect(container.textContent).toContain('has expired')
    expect(Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')?.disabled).toBe(true)
    let finish: (value: unknown) => void = () => {}
    mock.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
    const onReview = vi.fn()
    await act(async () => root.render(<GuidedRepair script={script} projectId="other" onApplied={vi.fn()} onReview={onReview} />))
    await act(async () => root.unmount())
    await act(async () => finish(result))
    expect(onReview).not.toHaveBeenCalled()
  } finally { container.remove(); vi.clearAllMocks() }
})

it('renews an expired draft kept with applied repairs from its archive\'s Validate again, not from archive setup', async () => {
  const script = { name: 'Expired', protocol: 'http', actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:expired' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:expired', revision: 'protected:expired', script, findings: [{ code: 'legacy_tls_replacement', action_path: 'actions[0]', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2000-01-01', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); const root = createRoot(container)
  const onReimport = vi.fn(); const onValidate = vi.fn()
  const banner = () => container.querySelector<HTMLElement>('.lr-issue-unavailable')!
  const buttons = () => Array.from(banner().querySelectorAll('button'))
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} onReimport={onReimport} validateAgain={{ disabled: false, onValidate }} />))
    expect(banner().querySelector('strong')?.textContent).toBe('This protected draft has expired')
    expect(banner().querySelector('p')?.textContent).toBe('It can no longer be saved. Only Validate again on this archive renews it, and the repairs applied to it are discarded.')
    expect(buttons().map(button => button.textContent)).toEqual(['Validate again'])
    await act(async () => buttons()[0].click())
    expect(onValidate).toHaveBeenCalledOnce()
    expect(onReimport).not.toHaveBeenCalled()
    // While the importer is busy it waits, as the archive's own control does.
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} onReimport={onReimport} validateAgain={{ disabled: true, onValidate }} />))
    expect(buttons()[0].disabled).toBe(true)
    // A draft without applied repairs is renewed from archive setup, as before.
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} onReimport={onReimport} />))
    expect(banner().querySelector('p')?.textContent).toBe('Revalidate the archive to renew its repair session.')
    expect(buttons().map(button => button.textContent)).toEqual(['Open archive setup to revalidate'])
    await act(async () => buttons()[0].click())
    expect(onReimport).toHaveBeenCalledOnce()
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('asks to save an expired draft in an unconfirmed save first, and offers nothing that would drop that save', async () => {
  const script = { name: 'Unconfirmed', protocol: 'http', actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:unconfirmed' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:unconfirmed', revision: 'protected:unconfirmed', script, findings: [{ code: 'legacy_tls_replacement', action_path: 'actions[0]', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2000-01-01', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); const root = createRoot(container)
  const banner = () => container.querySelector<HTMLElement>('.lr-issue-unavailable')!
  try {
    // The importer withholds Validate again while the save is unconfirmed.
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} onReimport={vi.fn()} unconfirmedSave />))
    expect(banner().querySelector('strong')?.textContent).toBe('This protected draft has expired')
    expect(banner().querySelector('p')?.textContent).toBe('APEX has not confirmed the earlier save of this draft. Save first: it resends the unconfirmed save, which APEX answers if it was saved.')
    expect(banner().querySelectorAll('button')).toHaveLength(0)
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('previews duration and iteration allocation from the data tab without applying a repair', async () => {
  const script = { name: 'Allocation', protocol: 'http', actions: [], parameters: [{ name: 'ids', type: 'list', values: ['a', 'b'], selection: 'unique', auto_allocate: true }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:allocation' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:allocation', revision: 'protected:allocation', script, findings: [], readiness: { replay_ready: true }, expires_at: '2099-01-01', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" view="data" onApplied={vi.fn()} />))
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
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" view="data" onApplied={onApplied} />))
    await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview allocation')!.click())
    expect(container.textContent).toContain('2–3')
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" view="hidden" onApplied={onApplied} />))
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" view="data" onApplied={onApplied} />))
    expect(container.textContent).toContain('2–3')
    expect(container.textContent).toContain('does not clear other replay blockers')
    expect(onApplied).not.toHaveBeenCalled()
    await act(async () => root.render(<GuidedRepair script={script} projectId="another-project" view="data" onApplied={onApplied} />))
    expect(container.querySelector('[aria-label="Allocation result"]')).toBeNull()
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it('opens the authoritative issue for a source mapping focus request', async () => {
  const script = { name: 'TLS', protocol: 'http', actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:tls' } } } } as unknown as ScriptDefinition
  mock.request.mockResolvedValue({ candidate_id: 'protected:tls', script, findings: [{ code: 'legacy_tls_replacement', action_path: 'init[0]', gate: 'execution', detail: '' }], readiness: { replay_ready: false }, expires_at: '2099-01-01' })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" focusPath="init[0]" focusRequest={1} onApplied={vi.fn()} />))
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')?.value).toBe('select_tls')
    expect(container.textContent).toContain('intentional migration change')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

 it('shares protected candidate inspection across StrictMode effect replays', async () => {
  const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:strict' } } } } as ScriptDefinition
  mock.request.mockResolvedValue({ script, findings: [], readiness: { replay_ready: true }, expires_at: '2099-09-19' })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<StrictMode><GuidedRepair script={script} projectId="p" onApplied={vi.fn()} /></StrictMode>))
    expect(mock.request).toHaveBeenCalledTimes(1)
    expect(container.textContent).not.toContain('Checking current repair requirements')
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})

it.each(['none', 'sequential', 'random'])('hides the allocation preview when selection is %s', async selection => {
  const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], parameters: selection === 'none' ? [] : [{ name: 'ids', type: 'list', selection, values: ['a'] }] } as ScriptDefinition
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" view="data" onApplied={vi.fn()} />))
    expect(container.querySelector<HTMLElement>('[aria-label="Preview unique data allocation"]')?.hidden).toBe(true)
  } finally { await act(async () => root.unmount()); vi.clearAllMocks() }
})
it.each(['missing', 'expired', 'locked', 'available'])('explains allocation preview availability: %s', async state => {
  const script = { name: 'Draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], parameters: [{ name: 'ids', type: 'list', selection: 'unique', values: ['a'], auto_allocate: true }], metadata: { import: { preserved: { server_import_attestation_v1: state === 'missing' ? '' : 'protected:one' } } } } as ScriptDefinition
  mock.request.mockResolvedValue({ script, findings: [], readiness: {}, expires_at: state === 'expired' ? '2000-01-01' : '2099-01-01' })
  const container = document.createElement('div'); const root = createRoot(container)
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" view="data" disabled={state === 'locked'} disabledReason="This draft has already been saved." onApplied={vi.fn()} />))
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
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} categoryFocus={{ category: 'values', revision: 1 }} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
    expect(container.querySelector('.lr-issue-row')?.textContent).toContain('customer')
    await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} categoryFocus={{ category: 'runtime', revision: 2 }} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(1)
    expect(container.querySelector('[aria-label="Selected issue"]')?.textContent).toContain('Select an issue')
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} focusPath="actions[0]" focusRequest={1} />))
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
  await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} />))
  await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
  let finish!: (result: unknown) => void
  mock.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  await act(async () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')!.click())
  await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} categoryFocus={{ category: 'runtime', revision: 1 }} />))
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
  await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
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
  await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} focusPath="actions[0]" focusRequest={2} />))
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
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} additionalFindings={notes} onReviewedChange={onReviewedChange} />))
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

it('waits to inspect a candidate the importer is consuming and keeps an inspection already in flight', async () => {
  const draft = (token: string) => ({ name: 'Paused draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', preserved: { server_import_attestation_v1: token } } } }) as ScriptDefinition
  const inspected = (token: string) => ({ candidate_id: token, revision: token, script: draft(token), before: draft(token), findings: [{ code: 'restored_body_required', action_path: 'actions[0]', source_ref: 'Action.c:9', detail: 'Restore this body' }], readiness: { replay_ready: false, blocking_codes: ['restored_body_required'] }, expires_at: '2099-09-19', fidelity_report: {} })
  const inspectCalls = () => mock.request.mock.calls.filter(([url, options]) => url === '/scripts/import/repairs/preview' && JSON.parse(options.body).operation.type === 'inspect')
  let finishFirst: (value: unknown) => void = () => {}
  mock.request.mockImplementationOnce(() => new Promise(resolve => { finishFirst = resolve }))
  mock.request.mockResolvedValue(inspected('protected:two'))
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onReview = vi.fn()
  const render = (token: string, inspectionPaused: boolean) => root.render(<StrictMode><GuidedRepair script={draft(token)} projectId="p" onApplied={vi.fn()} onReview={onReview} inspectionPaused={inspectionPaused} /></StrictMode>)
  try {
    await act(async () => render('protected:one', false))
    expect(inspectCalls()).toHaveLength(1)
    // A save starts while the first inspection is still running: it is still delivered.
    await act(async () => render('protected:one', true))
    await act(async () => finishFirst(inspected('protected:one')))
    expect(onReview).toHaveBeenLastCalledWith(expect.objectContaining({ candidate_id: 'protected:one' }))
    // The rotated candidate arrives while the save consumes it: no inspection races the save.
    await act(async () => render('protected:two', true))
    expect(inspectCalls()).toHaveLength(1)
    expect(container.textContent).not.toContain('stale')
    // The save failed and released the draft: inspect it once, and not again on later toggles.
    await act(async () => render('protected:two', false))
    expect(inspectCalls()).toHaveLength(2)
    expect(JSON.parse(inspectCalls()[1][1].body).candidate_id).toBe('protected:two')
    await act(async () => render('protected:two', true))
    await act(async () => render('protected:two', false))
    expect(inspectCalls()).toHaveLength(2)
    expect(onReview).toHaveBeenCalledTimes(2)
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('locks repairs while the importer consumes the draft', async () => {
  const script = { name: 'Locked draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', preserved: { server_import_attestation_v1: 'protected:locked' } } } } as ScriptDefinition
  mock.request.mockResolvedValue({ candidate_id: 'protected:locked', revision: 'protected:locked', script, before: script, findings: [{ code: 'restored_body_required', action_path: 'actions[0]', source_ref: 'Action.c:4', detail: 'Restore this body' }], readiness: { replay_ready: false, blocking_codes: ['restored_body_required'] }, expires_at: '2099-09-19', fidelity_report: {} })
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container)
  const render = (inspectionPaused: boolean) => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} inspectionPaused={inspectionPaused} />)
  const previewButton = () => Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')
  try {
    await act(async () => render(false))
    await act(async () => { Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes('Restore protected request body'))!.click() })
    expect(previewButton()?.disabled).toBe(false)
    // A save or conversion of this draft starts: the repair must not race it.
    await act(async () => render(true))
    expect(previewButton()?.disabled).toBe(true)
    await act(async () => render(false))
    expect(previewButton()?.disabled).toBe(false)
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('lists exactly the importer\'s issues and does not inspect a candidate the importer already inspected', async () => {
  const script = { name: 'Given issues', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ name: 'Request', type: 'request', target: '/', metadata: { execution_blocker: 'legacy_tls_replacement' } }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:given' } } } } as ScriptDefinition
  const issue = (id: string, title: string, severity: ImportIssue['severity'], category: ImportIssue['category']): ImportIssue => ({ id, title, severity, category, blocking: severity === 'blocker', findings: [{ code: id, action_path: 'actions[0]', detail: '' }] })
  // Not what the draft's own gates would group to: the panel must not regroup them.
  const issues = [issue('archive', 'Archive gate', 'blocker', 'review'), issue('missing_producer:token', 'Missing value: token', 'warning', 'values'), issue('diagnostic:runtime', 'Runtime note', 'note', 'runtime')]
  const inspection = { candidate_id: 'protected:given', revision: 'protected:given', script, before: script, findings: [], readiness: { replay_ready: false, promotion_ready: false }, expires_at: '2099-09-19', fidelity_report: {}, secret_binding_requirements: [] }
  const regroup = vi.fn(() => issues)
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const filterText = () => Array.from(container.querySelectorAll('option')).map(option => option.textContent).join('|')
  try {
    await act(async () => root.render(<StrictMode><ImportGuidedRepair script={script} projectId="p" issues={issues} inspection={inspection} regroup={regroup} onApplied={vi.fn()} /></StrictMode>))
    // Listed by category, as given.
    expect(Array.from(container.querySelectorAll('.lr-issue-row-title')).map(row => row.textContent)).toEqual(['Missing value: token', 'Runtime note', 'Archive gate'])
    expect(Array.from(container.querySelectorAll('.lr-issue-row .import-issue-severity-tag')).map(tag => tag.textContent)).toEqual(['Warning', 'Review note', 'Blocker'])
    expect(filterText()).toContain('All unresolved (3)|Blockers (1)|Warnings (1)|Review notes (1)')
    expect(Array.from(container.querySelectorAll('.lr-issue-queue summary span')).map(span => span.textContent)).toEqual(['1 issue', '1 issue', '1 issue'])
    expect(mock.request).not.toHaveBeenCalled()
    // The inspection is the importer's: repairs are available without one of the panel's own.
    await act(async () => Array.from(container.querySelectorAll<HTMLButtonElement>('.lr-issue-row')).find(row => row.textContent?.includes('Missing value: token'))!.click())
    expect(container.querySelector('[aria-label="Repair operation"]')).not.toBeNull()
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('reads "Checking…" on every count while the importer waits for the inspection', async () => {
  const script = { name: 'Pending', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ name: 'Request', type: 'request', target: '/', metadata: { execution_blocker: 'legacy_tls_replacement' } }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:pending' } } } } as ScriptDefinition
  let finish: (value: unknown) => void = () => {}
  mock.request.mockImplementationOnce(() => new Promise(resolve => { finish = resolve }))
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const summaries = () => Array.from(container.querySelectorAll('.lr-issue-queue summary span')).map(span => span.textContent)
  const filterText = () => Array.from(container.querySelectorAll('option')).map(option => option.textContent).join('|')
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
    expect(summaries()).toEqual(['Checking…'])
    expect(filterText()).toContain('All unresolved|Blockers|Warnings|Review notes')
    await act(async () => finish({ candidate_id: 'protected:pending', revision: 'protected:pending', script, before: script, findings: [{ code: 'legacy_tls_replacement', action_path: 'actions[0]', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} }))
    expect(summaries()).toEqual(['1 issue'])
    expect(filterText()).toContain('All unresolved (1)|Blockers (1)|Warnings (0)|Review notes (0)')
    // The importer's own pending state covers the panel too.
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" issues={[]} regroup={() => []} countsPending onApplied={vi.fn()} />))
    expect(filterText()).toContain('All unresolved|Blockers|Warnings|Review notes')
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('adopts an applied repair as its candidate\'s inspection and announces it with the importer\'s other findings', async () => {
  const draft = (token: string, gated: boolean) => ({ name: 'TLS', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, init: [0, 1].map(line => ({ name: `web_set_sockets_option_${line}`, type: 'source_review', target: '', metadata: gated ? { execution_blocker: 'legacy_tls_replacement', source_file: 'vuser_init.c', source_line: line + 3 } : { source_file: 'vuser_init.c', source_line: line + 3 } })), actions: [], metadata: { import: { preserved: { server_import_attestation_v1: token } } } }) as ScriptDefinition
  const script = draft('protected:one', true)
  const response = (token: string, gated: boolean) => ({ candidate_id: token, revision: token, script: draft(token, gated), before: script, findings: gated ? draftImportFindings(script) : [], readiness: { replay_ready: !gated, promotion_ready: !gated }, expires_at: '2099-09-19', fidelity_report: {}, secret_binding_requirements: [] })
  mock.request.mockImplementation(async (url: string, options: { body: string }) => {
    const { operation } = JSON.parse(options.body)
    if (operation.type === 'inspect') return response('protected:one', true)
    return url.endsWith('/apply') ? response('protected:two', false) : { ...response('protected:one', false), candidate_id: 'protected:one' }
  })
  // The importer still holds a validation blocker the repair does not touch.
  const additionalFindings = [{ code: 'diagnostic:validation', detail: 'script protocol is required', gate: 'execution' }]
  const inspections = () => mock.request.mock.calls.filter(([, options]) => JSON.parse(options.body).operation.type === 'inspect')
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container); const onApplied = vi.fn()
  const button = (text: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent?.includes(text))!
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={onApplied} additionalFindings={additionalFindings} />))
    expect(container.querySelectorAll('.lr-issue-row')).toHaveLength(3)
    await act(async () => button('Choose supported TLS settings — vuser_init.c:3').click())
    expect(container.querySelector('.lr-repair-form .lr-subtitle')?.textContent).toContain('every legacy TLS step')
    await act(async () => button('Preview repair').click())
    // The preview outcome counts the importer's other blocker too.
    expect(container.querySelector('[aria-label="Repair outcome"]')?.textContent).toContain('Blocking issues1grouped issues')
    await act(async () => button('Apply reviewed repair').click())
    expect(onApplied).toHaveBeenCalledOnce()
    expect(container.querySelector('.lr-repair-announcement')?.textContent).toBe('Repair applied. 1 blocks replay · 0 warnings · 0 review notes.')
    expect(Array.from(container.querySelectorAll('.lr-issue-row-title')).map(row => row.textContent)).toEqual(['Resolve draft validation checks'])
    expect(inspections()).toHaveLength(1)
    expect(container.textContent).not.toContain('Checking repair requirements')
  } finally { await act(async () => root.unmount()); container.remove(); mock.request.mockReset(); vi.clearAllMocks() }
})

// The repair form's selects and text box, set as a user would.
async function choose(select: HTMLSelectElement, value: string) {
  await act(async () => { select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
}
async function type(input: HTMLTextAreaElement, value: string) {
  await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
}

it('previews TLS through the shared form with its 1.2 to 1.3 default and the chosen minimum', async () => {
  const script = { name: 'TLS draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, init: [{ name: 'Socket option', type: 'source_review', target: 'web_set_sockets_option', metadata: { execution_blocker: 'legacy_tls_replacement', source_file: 'vuser_init.c', source_line: 3 } }], actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:tls' } } } } as unknown as ScriptDefinition
  const result = { candidate_id: 'protected:tls', revision: 'protected:tls', script, before: script, findings: [{ code: 'legacy_tls_replacement', action_path: 'init[0]', source_ref: 'vuser_init.c:3', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const button = (text: string) => [...container.querySelectorAll('button')].find(b => b.textContent === text)!
  const lastOperation = () => JSON.parse(mock.request.mock.calls[mock.request.mock.calls.length - 1][1].body).operation
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
    await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')!.value).toBe('select_tls')
    expect(container.querySelector('.lr-repair-form .lr-subtitle')?.textContent).toBe("Sets the script's TLS range, which resolves every legacy TLS step in this draft at once.")
    await act(async () => button('Preview repair').click())
    expect(lastOperation()).toEqual({ type: 'select_tls', tls_min: '1.2', tls_max: '1.3' })
    expect(button('Apply reviewed repair')).toBeDefined()
    // A changed choice drops the preview that no longer describes it.
    await choose([...container.querySelectorAll<HTMLSelectElement>('.lr-repair-form select')].find(select => select.closest('label')?.textContent?.startsWith('Minimum TLS'))!, '1.3')
    expect(button('Apply reviewed repair')).toBeUndefined()
    await act(async () => button('Preview repair').click())
    expect(lastOperation()).toEqual({ type: 'select_tls', tls_min: '1.3', tls_max: '1.3' })
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('lists the fields the repair changes, not the review records the server restamps on the draft', async () => {
  const script = { name: 'TLS draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, init: [{ name: 'Socket option', type: 'source_review', target: 'web_set_sockets_option', metadata: { execution_blocker: 'legacy_tls_replacement', source_file: 'vuser_init.c', source_line: 3 } }], actions: [], metadata: { import: { source: 'enterprise_script_archive', preserved: { server_import_attestation_v1: 'protected:tls', fidelity_report: { promotion_status: 'blocked', blockers: ['legacy_tls_replacement'] } } } } } as unknown as ScriptDefinition
  const finding = { code: 'legacy_tls_replacement', action_path: 'init[0]', source_ref: 'vuser_init.c:3', detail: '', gate: 'execution' }
  const inspection = { candidate_id: 'protected:tls', revision: 'protected:tls', script, before: script, findings: [finding], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} }
  // The preview's draft as the server returns it: the TLS range and the step's
  // migration note, plus its re-evaluation stamped into the preserved import.
  const repaired = {
    ...script,
    config: { ...script.config, http: { tls: { min_version: '1.2', max_version: '1.3' } } },
    init: [{ ...script.init![0], metadata: { source_file: 'vuser_init.c', source_line: 3, migration_change: 'Operator selected TLS 1.2–1.3' } }],
    metadata: { import: { source: 'enterprise_script_archive', preserved: {
      server_import_attestation_v1: 'protected:tls',
      guided_repair_candidate: true,
      fidelity_report: { promotion_status: 'promotable', blockers: [], warnings: [], secret_gated: 0 },
      replay_preflight: { valid: true, action_count: 1, expected_action_count: 1, issues: [], warnings: [] },
    } } },
  } as unknown as ScriptDefinition
  mock.request.mockImplementation(async (_url: string, options: { body: string }) => JSON.parse(options.body).operation.type === 'inspect'
    ? inspection
    : { ...inspection, before: script, script: repaired, findings: [], readiness: { replay_ready: true } })
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
    await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
    await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Preview repair')!.click())
    const fields = container.querySelector('.lr-repair-preview-fields')!
    expect([...fields.querySelectorAll('li')].map(item => item.textContent)).toEqual(['config.http', 'init[0].metadata.execution_blocker', 'init[0].metadata.migration_change'])
    expect(fields.querySelector('.lr-repair-preview-section-heading span')?.textContent).toBe('3 shown')
    expect(fields.textContent).not.toContain('metadata.import')
  } finally { await act(async () => root.unmount()); container.remove(); mock.request.mockReset(); vi.clearAllMocks() }
})

it('sends add_parameter and add_extractor exactly as before through the shared form', async () => {
  const script = { name: 'Values draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ name: 'Login', type: 'POST', target: '/login' }, { name: 'Cart', type: 'POST', target: '/cart', body: '{{var.c_token}}' }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:values' } } } } as ScriptDefinition
  const result = { candidate_id: 'protected:values', revision: 'protected:values', script, before: script, findings: [{ code: 'missing_producer:c_token', action_path: 'actions[1]', source_ref: 'Action.c:9', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const button = (text: string) => [...container.querySelectorAll('button')].find(b => b.textContent?.includes(text))!
  const lastOperation = () => JSON.parse(mock.request.mock.calls[mock.request.mock.calls.length - 1][1].body).operation
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" onApplied={vi.fn()} />))
    await act(async () => button('Missing value: c_token').click())
    const operation = () => container.querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')!
    await choose(operation(), 'add_parameter')
    await type(container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!, 'first\nsecond')
    await act(async () => button('Preview repair').click())
    expect(lastOperation()).toEqual({ type: 'add_parameter', parameter: { name: 'c_token', type: 'list', values: ['first', 'second'], update: 'once', selection: 'sequential' } })
    await choose(operation(), 'add_extractor')
    // The value is per operation; an extractor also needs its response.
    expect(container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!.value).toBe('')
    const responses = container.querySelector<HTMLSelectElement>('[aria-label="Preceding response path"]')!
    expect([...responses.options].map(option => option.value)).toEqual(['', 'actions[0]'])
    await type(container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!, 'token=(\\w+)')
    expect(button('Preview repair').disabled).toBe(true)
    await choose(responses, 'actions[0]')
    await choose([...container.querySelectorAll<HTMLSelectElement>('.lr-repair-form select')].find(select => select.closest('label')?.textContent?.startsWith('Extractor type'))!, 'regex')
    await act(async () => button('Preview repair').click())
    expect(lastOperation()).toEqual({ type: 'add_extractor', response_path: 'actions[0]', extractor: { name: 'c_token', type: 'regex', source: 'body', expression: 'token=(\\w+)', on_miss: 'fail' } })
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('keeps the repair fields with its caller and names the saved script for a script-wide repair', async () => {
  const script = { name: 'Saved', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, init: [{ name: 'Socket option', type: 'source_review', target: 'web_set_sockets_option' }], actions: [] } as unknown as ScriptDefinition
  const finding = { code: 'encrypted_proxy_password', action_path: 'init[0]', detail: '', gate: 'execution' }
  const onPreview = vi.fn()
  function Harness() {
    const [fields, setFields] = useState<RepairFields>({ kind: 'replace_proxy_secret', value: '', responsePath: '', extractorType: 'jsonpath' })
    return <GuidedRepairForm finding={finding} occurrences={[finding]} options={[{ value: 'replace_proxy_secret', label: 'Choose proxy secret binding' }]} entries={importActionEntries(script)} fields={fields}
      onChange={change => setFields(previous => ({ ...previous, ...change }))} onOccurrence={vi.fn()} locked={false} busy={false} onPreview={onPreview} subject="script"><p>Preview slot</p></GuidedRepairForm>
  }
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<Harness />))
    expect(container.querySelector('[aria-label="Repair finding"]')).toBeNull()
    expect(container.textContent).toContain('Project secret name')
    expect(container.textContent).toContain('Preview slot')
    const preview = [...container.querySelectorAll('button')].find(b => b.textContent === 'Preview repair')!
    expect(preview.disabled).toBe(true)
    await type(container.querySelector<HTMLTextAreaElement>('[aria-label="Repair value"]')!, 'PROXY_PASSWORD')
    await act(async () => preview.click())
    expect(onPreview).toHaveBeenCalledWith({ type: 'replace_proxy_secret', secret_name: 'PROXY_PASSWORD' })
    await act(async () => root.render(<GuidedRepairForm finding={{ ...finding, code: 'legacy_tls_replacement' }} occurrences={[finding]} options={[{ value: 'select_tls', label: 'Select supported TLS' }]} entries={[]} fields={{ kind: 'select_tls', value: '', responsePath: '', extractorType: 'jsonpath' }} onChange={vi.fn()} onOccurrence={vi.fn()} locked busy={false} onPreview={onPreview} subject="script" />))
    expect(container.querySelector('.lr-subtitle')?.textContent).toBe("Sets the script's TLS range, which resolves every legacy TLS step in this script at once.")
    expect([...container.querySelectorAll('button')].find(b => b.textContent === 'Preview repair')!.disabled).toBe(true)
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})

it('reads replay checks as blocked when the grouped preview still has a blocker the server readiness does not see', async () => {
  // A configuration reference comes from replay readiness, never from the
  // server's evaluation of the repaired draft, which can call it replay-ready.
  const script = { name: 'TLS and auth', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test', http: { auth: { username: '{{var.pUser}}' } } }, init: [{ name: 'Socket option', type: 'source_review', target: 'web_set_sockets_option' }], actions: [{ name: 'Home', type: 'GET', target: '/' }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:tls-auth' } } } } as unknown as ScriptDefinition
  const inspection = { candidate_id: 'protected:tls-auth', revision: 'protected:tls-auth', script, before: script, findings: [{ code: 'legacy_tls_replacement', action_path: 'init[0]', source_ref: 'vuser_init.c:3', detail: '', gate: 'execution' }], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} }
  mock.request.mockImplementation(async (_url: string, options: { body: string }) => JSON.parse(options.body).operation.type === 'inspect'
    ? inspection
    : { ...inspection, findings: [], readiness: { replay_ready: true } })
  const additionalFindings = [{ code: 'config_reference:pUser', detail: configReferenceText('pUser'), gate: 'execution' }]
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" additionalFindings={additionalFindings} onApplied={vi.fn()} />))
    await act(async () => [...container.querySelectorAll<HTMLButtonElement>('.lr-issue-row')].find(row => row.textContent?.includes('Choose supported TLS settings'))!.click())
    await act(async () => [...container.querySelectorAll('button')].find(b => b.textContent === 'Preview repair')!.click())
    const outcome = container.querySelector('[aria-label="Repair outcome"]')!
    const [replay, blocking] = [...outcome.children]
    expect(replay.querySelector('strong')?.textContent).toBe('Blocked')
    expect(replay.querySelector('strong')?.className).toBe('danger')
    expect(replay.querySelector('small')?.textContent).toBe('Execution remains blocked')
    expect(blocking.querySelector('strong')?.textContent).toBe('1')
  } finally { await act(async () => root.unmount()); container.remove(); mock.request.mockReset(); vi.clearAllMocks() }
})

it('opens a configuration reference reached from its step as the configuration change it needs, with no step repair', async () => {
  // The step's own finding for the value is folded into the configuration
  // reference; varflow refuses the reference whatever a step supplies.
  const script = { name: 'Tenant draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test', http: { default_headers: { 'X-Tenant': '{{var.c_tenant}}' } } }, init: [{ name: 'Socket option', type: 'source_review', target: 'web_set_sockets_option' }], actions: [{ name: 'Tenant', type: 'GET', target: '/tenants/{{var.c_tenant}}' }], metadata: { import: { preserved: { server_import_attestation_v1: 'protected:tenant' } } } } as unknown as ScriptDefinition
  mock.request.mockResolvedValue({ candidate_id: 'protected:tenant', revision: 'protected:tenant', script, before: script, findings: [
    { code: 'missing_producer:c_tenant', action_path: 'actions[0]', source_ref: 'Action.c:4', detail: '', gate: 'execution' },
    { code: 'legacy_tls_replacement', action_path: 'init[0]', source_ref: 'vuser_init.c:3', detail: '', gate: 'execution' },
  ], readiness: { replay_ready: false }, expires_at: '2099-09-19', fidelity_report: {} })
  const additionalFindings = [{ code: 'config_reference:c_tenant', detail: configReferenceText('c_tenant'), gate: 'execution' }]
  const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
  const row = (text: string) => [...container.querySelectorAll<HTMLButtonElement>('.lr-issue-row')].find(item => item.textContent?.includes(text))!
  const expectConfigurationIssue = () => {
    const detail = container.querySelector('[aria-label="Selected issue"]')!
    expect(detail.querySelector('h4')?.textContent).toBe('Replace configuration reference: c_tenant')
    expect(detail.querySelector('[aria-label="Repair operation"]')).toBeNull()
    expect(detail.querySelector('[aria-label="Problem and impact"]')?.textContent).toContain(configReferenceText('c_tenant'))
    expect(detail.textContent).toContain('This requires a supported source or configuration change.')
    expect(detail.textContent).not.toContain('no reachable producer')
  }
  try {
    await act(async () => root.render(<GuidedRepair script={script} projectId="p" additionalFindings={additionalFindings} focusPath="actions[0]" focusRequest={1} onApplied={vi.fn()} />))
    expectConfigurationIssue()
    // Another issue and back: no step form was kept for it either.
    await act(async () => row('Choose supported TLS settings').click())
    expect(container.querySelector<HTMLSelectElement>('[aria-label="Repair operation"]')?.value).toBe('select_tls')
    await act(async () => row('Replace configuration reference: c_tenant').click())
    expectConfigurationIssue()
  } finally { await act(async () => root.unmount()); container.remove(); mock.request.mockReset(); vi.clearAllMocks() }
})

it.each(['legacy_tls_replacement', 'diagnostic:warnings:tls', 'diagnostic:warnings:migration'])('edits %s inline without protected repair storage', async code => {
 const { ImportSettingsProvider } = await import('./ImportSettingsContext')
 const script = { name: 'TLS draft', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { preserved: { server_import_attestation_v1: 'signed:tls' } } } } as unknown as ScriptDefinition
 const detail = code.endsWith('migration') ? 'Automatic SSL/TLS selection "AUTO" was replaced with TLS 1.3' : 'web_set_sockets_option SSL_VERSION="AUTO" is unsupported; select a supported TLS range'
 const finding = { code, action_path: 'init[0]', detail, gate: code.startsWith('diagnostic:') ? 'promotion' : 'execution' }
 const issue = groupImportFindings([finding], script)[0]
 const container = document.createElement('div'); document.body.append(container); const root = createRoot(container)
 const button = (text: string) => [...container.querySelectorAll('button')].find(b => b.textContent === text)!
 try {
  await act(async () => root.render(<ImportSettingsProvider script={script} disabled={false} onApplied={vi.fn()} onBusyChange={vi.fn()}><ImportGuidedRepair script={script} projectId="p" issues={[issue]} regroup={() => []} onApplied={vi.fn()} /></ImportSettingsProvider>))
  await act(async () => container.querySelector<HTMLButtonElement>('.lr-issue-row')!.click())
  expect(container.querySelector('[aria-label="TLS settings"]')).toBeTruthy()
  expect(container.textContent).not.toContain('Open all runtime settings')
  await act(async () => button('Edit HTTP here').click())
  const minimum = container.querySelector<HTMLSelectElement>('[aria-label="HTTP: Minimum TLS version"]')!
  await choose(minimum, '1.3')
  await act(async () => button('Preview HTTP changes').click())
  expect(mock.request).not.toHaveBeenCalled()
  expect(container.querySelector('[aria-label="HTTP change preview"]')?.textContent).toContain('1.3')
  mock.request.mockResolvedValue({ script, findings: [], readiness: {}, secret_binding_requirements: [] })
  await act(async () => button('Apply HTTP changes').click())
  expect(mock.request.mock.calls[0][0]).toBe('/scripts/import/settings')
  expect(JSON.parse(mock.request.mock.calls[0][1].body).changes).toEqual([{path:'config.http.tls.min_version', value:'1.3'}])
 } finally { await act(async () => root.unmount()); container.remove(); mock.request.mockReset() }
})
