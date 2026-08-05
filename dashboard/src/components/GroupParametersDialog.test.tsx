// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import GroupParametersDialog from './GroupParametersDialog'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('GroupParametersDialog', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    vi.restoreAllMocks()
    container.remove()
  })

  function jsonResponse(body: unknown) {
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }

  function requestUrl(input: RequestInfo | URL) {
    if (typeof input === 'string') return input
    if (input instanceof URL) return input.toString()
    return input.url
  }

  async function flush(times = 5) {
    for (let i = 0; i < times; i++) {
      await act(async () => {
        await Promise.resolve()
      })
    }
  }

  const editableView = {
    test_id: 'queued-1',
    group: 'Buyers',
    group_index: 0,
    script_name: 'Checkout',
    protocol: 'http',
    status: 'QUEUED',
    editable: true,
    revision: 'sha256:parameters-v1',
    parameters: [
      { name: 'AccountUser', type: 'file', data_source: 'accounts', column: 'user', update: 'each_iteration', selection: 'sequential' },
      { name: 'Region', type: 'list', values: ['east', 'west'], update: 'once', selection: 'random' },
    ],
    data_sources: [{ name: 'accounts', type: 'csv', columns: ['user', 'region'], row_count: 2 }],
  }

  it('edits a parameter and saves the full list back to the run', async () => {
    const putBodies: unknown[] = []
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/parameters') && init?.method === 'PUT') {
        putBodies.push(JSON.parse(String(init.body)))
        return jsonResponse({
          test_id: 'queued-1',
          group: 'Buyers',
          changed: ['Region'],
          parameters: [],
          revision: 'sha256:parameters-v2',
        })
      }
      if (url.includes('/parameters')) return jsonResponse(editableView)
      return jsonResponse({})
    })
    const onSaved = vi.fn()
    await act(async () => {
      root.render(
        <GroupParametersDialog testId="queued-1" groupName="Buyers" onClose={() => {}} onSaved={onSaved} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    expect(dialog.textContent).toContain('Parameters — Buyers')
    const listButtons = Array.from(dialog.querySelectorAll('.group-parameters-list button'))
    expect(listButtons.map(button => button.querySelector('strong')?.textContent)).toEqual(['AccountUser', 'Region'])

    // Select the list-backed parameter and edit its inline values.
    await act(async () => {
      (listButtons[1] as HTMLButtonElement).click()
    })
    const valuesLabel = Array.from(dialog.querySelectorAll('label')).find(label => label.textContent?.includes('Inline Values'))
    expect(valuesLabel).toBeDefined()
    const valuesInput = valuesLabel?.querySelector('textarea') as HTMLTextAreaElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(valuesInput, 'east\nwest\ncentral')
      valuesInput.dispatchEvent(new Event('input', { bubbles: true }))
      valuesInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const saveButton = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining('/api/v1/tests/queued-1/groups/Buyers/parameters'),
      expect.objectContaining({ method: 'PUT' }),
    )
    expect(putBodies).toHaveLength(1)
    const body = putBodies[0] as { expected_revision: string; parameters: Array<{ name: string; values?: string[] }> }
    expect(body.expected_revision).toBe('sha256:parameters-v1')
    expect(body.parameters).toHaveLength(2)
    expect(body.parameters[1].name).toBe('Region')
    expect(body.parameters[1].values).toEqual(['east', 'west', 'central'])
    expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ changed: ['Region'] }))
  })

  it('reports when there is nothing to save instead of calling the API', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = requestUrl(input)
      if (url.includes('/parameters')) return jsonResponse(editableView)
      return jsonResponse({})
    })
    const onSaved = vi.fn()
    await act(async () => {
      root.render(
        <GroupParametersDialog testId="queued-1" groupName="Buyers" onClose={() => {}} onSaved={onSaved} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const saveButton = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    const callsBeforeSave = fetchMock.mock.calls.length
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(dialog.textContent).toContain('No changes to save.')
    expect(fetchMock.mock.calls.length).toBe(callsBeforeSave)
    expect(onSaved).not.toHaveBeenCalled()
  })

  it('does not adopt a newer revision while preserving a rejected draft', async () => {
    let getCount = 0
    const putBodies: Array<{ expected_revision?: string }> = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/parameters') && init?.method === 'PUT') {
        putBodies.push(JSON.parse(String(init.body)))
        return new Response(JSON.stringify({ error: 'parameter policy is invalid' }), {
          status: 400,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (url.includes('/parameters')) {
        getCount++
        return jsonResponse({
          ...editableView,
          revision: getCount === 1 ? 'sha256:parameters-old' : 'sha256:parameters-new',
          parameters: getCount === 1
            ? editableView.parameters
            : [editableView.parameters[0], { ...editableView.parameters[1], values: ['server-value'] }],
        })
      }
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupParametersDialog testId="queued-1" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const listButtons = Array.from(dialog.querySelectorAll('.group-parameters-list button'))
    await act(async () => {
      (listButtons[1] as HTMLButtonElement).click()
    })
    const valuesInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Inline Values'))
      ?.querySelector('textarea') as HTMLTextAreaElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(valuesInput, 'client-value')
      valuesInput.dispatchEvent(new Event('input', { bubbles: true }))
      valuesInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const saveButton = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(putBodies).toEqual([{ expected_revision: 'sha256:parameters-old', parameters: expect.any(Array) }])
    expect(getCount).toBe(2)
    expect(dialog.textContent).toContain('Parameters changed in another session')
    expect(saveButton.disabled).toBe(true)
    expect(valuesInput.value).toBe('client-value')
  })

  it('keeps a stale revision fenced until the user explicitly reloads', async () => {
    let getCount = 0
    const putBodies: Array<{ expected_revision?: string }> = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = requestUrl(input)
      if (url.includes('/parameters') && init?.method === 'PUT') {
        putBodies.push(JSON.parse(String(init.body)))
        return new Response(JSON.stringify({ error: 'group settings changed since they were loaded' }), {
          status: 409,
          headers: { 'Content-Type': 'application/json' },
        })
      }
      if (url.includes('/parameters')) {
        getCount++
        return jsonResponse({
          ...editableView,
          revision: getCount === 1 ? 'sha256:stale' : 'sha256:fresh',
          parameters: getCount === 1
            ? editableView.parameters
            : [editableView.parameters[0], { ...editableView.parameters[1], values: ['server-value'] }],
        })
      }
      return jsonResponse({})
    })
    await act(async () => {
      root.render(
        <GroupParametersDialog testId="queued-1" groupName="Buyers" onClose={() => {}} onSaved={() => {}} />,
      )
    })
    await flush()

    const dialog = document.querySelector('.group-run-settings-dialog') as HTMLElement
    const listButtons = Array.from(dialog.querySelectorAll('.group-parameters-list button'))
    await act(async () => {
      (listButtons[1] as HTMLButtonElement).click()
    })
    const valuesInput = Array.from(dialog.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Inline Values'))
      ?.querySelector('textarea') as HTMLTextAreaElement
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')?.set
      setter?.call(valuesInput, 'client-value')
      valuesInput.dispatchEvent(new Event('input', { bubbles: true }))
      valuesInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const saveButton = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Save to run')) as HTMLButtonElement
    await act(async () => {
      saveButton.click()
    })
    await flush()

    expect(putBodies).toEqual([{ expected_revision: 'sha256:stale', parameters: expect.any(Array) }])
    expect(getCount).toBe(1)
    expect(dialog.textContent).toContain('Parameters changed in another session')
    expect(saveButton.disabled).toBe(true)

    const reloadButton = Array.from(dialog.querySelectorAll('button'))
      .find(button => button.textContent?.includes('Reload parameters')) as HTMLButtonElement
    await act(async () => {
      reloadButton.click()
    })
    await flush()
    expect(getCount).toBe(2)
    expect(dialog.textContent).not.toContain('Parameters changed in another session')
  })
})
