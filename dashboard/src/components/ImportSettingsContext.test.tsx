// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { ScriptDefinition } from '../types/scripts'
import ImportRuntimeSettingsTable from './ImportRuntimeSettingsTable'
import { ImportSettingsProvider } from './ImportSettingsContext'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
vi.mock('../hooks/apiClient', async importOriginal => ({
  ...await importOriginal<typeof import('../hooks/apiClient')>(),
  request: mocks.request,
}))

describe('ImportSettingsContext out-of-catalog (imported) select handling', () => {
  it('keeps an out-of-catalog (imported) option selectable and drops it from staged changes when re-selected', async () => {
    const draft: ScriptDefinition = {
      name: 'Legacy HTTP version draft',
      version: '1.0',
      protocol: 'http',
      config: {
        base_url: 'https://example.test',
        http: {
          version: '1.0',
          request_timeout_ms: 3000,
        },
      },
      actions: [],
    }
    const onApplied = vi.fn()
    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    try {
      await act(async () => {
        root.render(
          <ImportSettingsProvider script={draft} disabled={false} onApplied={onApplied} onBusyChange={vi.fn()}>
            <ImportRuntimeSettingsTable script={draft} />
          </ImportSettingsProvider>,
        )
      })

      const editHttpButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent === 'Edit HTTP')!
      await act(async () => editHttpButton.click())

      const versionSelect = container.querySelector<HTMLSelectElement>('select[aria-label="HTTP: HTTP version"]')!
      expect(versionSelect).toBeTruthy()
      expect(versionSelect.value).toBe('1.0')
      expect(Array.from(versionSelect.options).map(option => option.textContent)).toContain('1.0 (imported)')

      // Switch to a valid catalog option ('1.1'); the '1.0 (imported)' option remains available.
      await act(async () => {
        versionSelect.value = '1.1'
        versionSelect.dispatchEvent(new Event('change', { bubbles: true }))
      })
      expect(versionSelect.value).toBe('1.1')
      expect(Array.from(versionSelect.options).map(option => option.textContent)).toContain('1.0 (imported)')

      // Re-select '1.0 (imported)' while also changing timeout; 'config.http.version' must be removed from changes
      // so /scripts/import/settings is not sent the out-of-catalog '1.0' value (which would fail with HTTP 422).
      await act(async () => {
        versionSelect.value = '1.0'
        versionSelect.dispatchEvent(new Event('change', { bubbles: true }))
      })
      expect(versionSelect.value).toBe('1.0')

      const timeoutInput = container.querySelector<HTMLInputElement>('input[aria-label="HTTP: Request timeout (ms)"]')!
      await act(async () => {
        Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(timeoutInput, '4500')
        timeoutInput.dispatchEvent(new Event('input', { bubbles: true }))
      })

      mocks.request.mockResolvedValueOnce({
        script: {
          ...draft,
          config: {
            ...draft.config,
            http: { ...draft.config.http, request_timeout_ms: 4500 },
          },
        },
        findings: [],
        readiness: {},
        secret_binding_requirements: [],
      })

      const applyHttpButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent === 'Apply HTTP')!
      await act(async () => applyHttpButton.click())

      expect(mocks.request).toHaveBeenCalledTimes(1)
      const requestBody = JSON.parse(mocks.request.mock.calls[0][1].body as string) as {
        changes: Array<{ path: string, value: unknown }>
      }
      expect(requestBody.changes).toEqual([
        { path: 'config.http.request_timeout_ms', value: 4500 },
      ])
      expect(onApplied).toHaveBeenCalledOnce()
    } finally {
      await act(async () => root.unmount())
      container.remove()
      mocks.request.mockReset()
    }
  })
})
