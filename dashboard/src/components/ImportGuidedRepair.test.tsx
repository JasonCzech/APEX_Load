// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ImportGuidedRepair } from './ImportGuidedRepair'
import type { ScriptDefinition } from '../types/scripts'
const mock = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', () => mock)
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('requires a redacted preview before applying and retains the reviewed operation', async () => {
  const script = { name: 'Protected draft', version: '1.0', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [], metadata: { import: { source: 'enterprise_script_archive', preserved: { server_import_attestation_v1: 'protected:one' } } } } as ScriptDefinition
  const result = { candidate_id: 'protected:one', revision: 'protected:one', script, before: script, findings: [{ code: 'restored_body_required', action_path: 'actions[0]', source_ref: 'Action.c:3', detail: 'Restore this body' }], readiness: { replay_ready: false, blocking_codes: ['restored_body_required'] }, expires_at: '2026-09-19', fidelity_report: {} }
  mock.request.mockResolvedValue(result)
  const container = document.createElement('div'); document.body.append(container)
  const root = createRoot(container); const onApplied = vi.fn()
  try {
    await act(async () => root.render(<ImportGuidedRepair script={script} projectId="p" onApplied={onApplied} />))
    expect(container.textContent).toContain('Action.c:3')
    expect(container.textContent).not.toContain('Apply reviewed repair')
    const select = container.querySelector('[aria-label="Repair finding"]') as HTMLSelectElement
    await act(async () => { select.value = 'actions[0]:restored_body_required'; select.dispatchEvent(new Event('change', { bubbles: true })) })
    const preview = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Preview repair')!
    await act(async () => preview.click())
    expect(container.textContent).toContain('Redacted before / after')
    const apply = Array.from(container.querySelectorAll('button')).find(b => b.textContent === 'Apply reviewed repair')!
    mock.request.mockResolvedValueOnce({ ...result, candidate_id: 'protected:two', revision: 'protected:two' })
    await act(async () => apply.click())
    expect(onApplied).toHaveBeenCalledOnce()
    const [url, options] = mock.request.mock.calls[mock.request.mock.calls.length - 1]
    expect(url).toBe('/scripts/import/repairs/apply')
    expect(JSON.parse(options.body).revision).toBe('protected:one')
    expect(JSON.parse(options.body).operation).toEqual({ type: 'restore_body', body: '' })
  } finally { await act(async () => root.unmount()); container.remove(); vi.clearAllMocks() }
})
