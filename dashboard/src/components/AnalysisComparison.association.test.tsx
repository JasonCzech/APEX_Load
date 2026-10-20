// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import AnalysisComparison from './AnalysisComparison'
import type { AnalysisImport } from '../types/analysis'

const request = vi.hoisted(() => vi.fn())
vi.mock('../hooks/apiClient', () => ({ request }))
;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
let container: HTMLElement; let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); request.mockReset() })
afterEach(() => { act(() => root.unmount()); container.remove() })
const dataset: AnalysisImport = { id: 'a', project_id: 'p', name: 'A', state: 'ready', files: [], revision: 4, accepted: 2, rejected: 0, mapping: { version: 2, timestamp: ['ts'], timestamp_encoding: 'unix_s', timezone: 'UTC', measures: [{ name: 'latency', value: ['v'], kind: 'summary', unit: 'ms', statistic: 'p95', window_seconds: 60 }] } }
async function change(name: string, value: string) {
  await act(async () => {
    const element = [...container.querySelectorAll('label')].find(x => x.textContent?.startsWith(name))!.querySelector('input,select') as HTMLInputElement | HTMLSelectElement
    const prototype = element.tagName === 'SELECT' ? HTMLSelectElement.prototype : HTMLInputElement.prototype
    Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value)
    element.dispatchEvent(new Event(element.tagName === 'SELECT' ? 'change' : 'input', { bubbles: true }))
  })
}
const button = () => [...container.querySelectorAll('button')].find(x => x.textContent === 'Associate with run')!

for (const version of [undefined, 2]) it(`retains advanced mapping without a matching association capability (${version})`, async () => {
  request.mockResolvedValue([])
  const applied = vi.fn()
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} mappingAssociationContract={version} projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={applied} />))
  await change('Compare with', 'run'); await change('Run ID', 'run')
  expect(button().disabled).toBe(true)
  expect(container.textContent).toContain('cannot safely reassociate')
  await act(async () => button().click())
  expect(request.mock.calls.some(([, options]) => options?.method === 'PUT' || options?.method === 'PATCH')).toBe(false)
  expect(applied).not.toHaveBeenCalled()
})

for (const valid of [true, false]) it(`uses PATCH and requires a mapping-preserving response (${valid})`, async () => {
  const applied = vi.fn()
  const returned = { ...dataset, revision: 5, run_id: 'run', ...(valid ? {} : { mapping: undefined }) }
  request.mockImplementation(async (path: string) => path.endsWith('/association') ? { contract_version: 1, import: returned } : [])
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} mappingAssociationContract={1} projectId="p" dataset={dataset} datasets={[dataset]} canEdit onAssociated={applied} />))
  await change('Compare with', 'run'); await change('Run ID', 'run')
  expect(button().disabled).toBe(false)
  await act(async () => button().click())
  expect(request).toHaveBeenCalledWith('/analysis/datasets/a/association', expect.objectContaining({ method: 'PATCH', body: JSON.stringify({ contract_version: 1, run_id: 'run', revision: 4 }) }))
  if (valid) expect(applied).toHaveBeenCalledWith(returned)
  else { expect(applied).not.toHaveBeenCalled(); expect(container.textContent).toContain('did not confirm the mapping-preserving association') }
})

it('does not downgrade an unavailable mapping to the legacy write route', async () => {
  request.mockResolvedValue([])
  const applied = vi.fn()
  const missing = { ...dataset, mapping: undefined }
  await act(async () => root.render(<AnalysisComparison importedIdentityVersion={2} mappingAssociationContract={1} projectId="p" dataset={missing} datasets={[missing]} canEdit onAssociated={applied} />))
  await change('Compare with', 'run'); await change('Run ID', 'run')
  expect(button().disabled).toBe(true)
  expect(container.textContent).toContain('Mapping details are unavailable')
  await act(async () => button().click())
  expect(request.mock.calls.some(([, options]) => options?.method === 'PUT' || options?.method === 'PATCH')).toBe(false)
  expect(applied).not.toHaveBeenCalled()
})
