// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import ImportIssueRuntimeEditor from './ImportIssueRuntimeEditor'
import { ImportSettingsProvider } from './ImportSettingsContext'
const mock = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', async original => ({ ...await original<typeof import('../hooks/apiClient')>(), request: mock.request }))

it('edits runtime differences in place with a selection-sensitive preview before applying', async () => {
 const script = { name: 'Draft', version: '1', protocol: 'http', config: { base_url: 'https://example.test', http: { receive_buffer_bytes: 100 } }, actions: [], metadata: { import: { preserved: { runtime_files: { 'default.cfg': { HTTP: { NetBufSize: '100' } } }, runtime_setting_dispositions: { 'default.cfg': { HTTP: { NetBufSize: { status: 'partial', parity_id: 'buffer', fields: ['config.http.receive_buffer_bytes'] } } } }, runtime_settings_parity: [{ id: 'buffer', area: 'HTTP', concept: 'Buffer', status: 'partial', fields: ['config.http.receive_buffer_bytes'], notes: 'Buffer handling differs' }] } } } } as ScriptDefinition
 const container = document.createElement('div'); const root = createRoot(container); const applied = vi.fn()
 const button = (label: string) => [...container.querySelectorAll('button')].find(b => b.textContent === label)!
 try {
  await act(async () => root.render(<ImportSettingsProvider script={script} disabled={false} onBusyChange={() => {}} onApplied={applied}><ImportIssueRuntimeEditor script={script} migrationPending={false} /></ImportSettingsProvider>))
  expect(container.textContent).toContain('Buffer handling differs')
  expect(button('Edit HTTP here').compareDocumentPosition(container.querySelector('table')!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  await act(async () => button('Edit HTTP here').click())
  const input = container.querySelector('input')!
  const change = async (value: string) => act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value); input.dispatchEvent(new Event('input', { bubbles: true })) })
  await change('200')
  await act(async () => button('Preview HTTP changes').click())
  expect(mock.request).not.toHaveBeenCalled()
  expect(container.querySelector('[aria-label="HTTP change preview"]')?.textContent).toContain('100 → 200')
  await change('300')
  expect(button('Apply HTTP changes')).toBeUndefined()
  await act(async () => button('Preview HTTP changes').click())
  mock.request.mockResolvedValue({ script, findings: [], readiness: {}, secret_binding_requirements: [] })
  await act(async () => button('Apply HTTP changes').click())
  expect(JSON.parse(mock.request.mock.calls[0][1].body).changes).toEqual([{ path: 'config.http.receive_buffer_bytes', value: 300 }])
  expect(applied).toHaveBeenCalledOnce()
 } finally { await act(async () => root.unmount()); mock.request.mockReset() }
})
