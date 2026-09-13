// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SampleScriptImport from './SampleScriptImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  importSample: vi.fn(),
  uploadImportAsset: vi.fn(),
  createScript: vi.fn(),
  commitGenericImportBatch: vi.fn(),
  applySecretBindings: vi.fn(),
  discardImportSecretSession: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
}))

describe('SampleScriptImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.importSample.mockResolvedValue({
      script: {
        name: 'POST_v1_session',
        protocol: 'http',
        config: {
          base_url: 'https://api.example.com',
        },
        actions: [
          {
            name: '01 - POST v1 session',
            extractors: [],
          },
        ],
      },
      warnings: ['no reusable correlation values were detected across the provided samples'],
      correlations: [
        {
          name: 'session_id',
          source: 'response',
          expression: '$.sessionId',
          sample_value: 'session-12345',
          found_in_step: 1,
          used_in_steps: [2],
        },
      ],
      stats: {
        format: 'rest_json',
        total_requests: 1,
        converted_actions: 1,
        assertions: 1,
        extractors: 1,
        correlations_detected: 1,
      },
      source_review: {
        source_files: [{ path: 'sample.json', language: 'json', content: '{"requests":[]}' }],
        mappings: [{ id: 'sample-request', source_file: 'sample.json', start_line: 1, function: 'rest_json_request', target_path: 'actions[0]', target_name: '01 - POST v1 session', target_kind: 'action', status: 'converted' }],
      },
    })
    apiMock.createScript.mockImplementation(async (script: { name?: string; protocol?: string; version?: string }) => ({
      id: 'script-1',
      name: script.name || 'Saved Script',
      protocol: script.protocol || 'http',
      version: script.version || '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
    }))
    apiMock.commitGenericImportBatch.mockImplementation(async (payload: {
      scripts: Array<{ name?: string; protocol?: string; version?: string; project_id?: string }>
      folder_id?: string | null
    }) => ({
      scripts: [await apiMock.createScript(payload.scripts[0], payload.scripts[0]?.project_id, payload.folder_id ?? null)],
      count: 1,
    }))
    apiMock.uploadImportAsset.mockResolvedValue({
      import_id: 'sample-import',
      name: 'upload_bin',
      file_name: 'upload.bin',
      content_type: 'application/octet-stream',
      size_bytes: 3,
      sha256: 'abc123',
      storage_ref: 'file:///tmp/upload.bin',
      data_source: {
        name: 'upload_bin',
        type: 'file',
        file_name: 'upload.bin',
        content_type: 'application/octet-stream',
        storage_ref: 'file:///tmp/upload.bin',
        strategy: 'sequential',
      },
      uploaded_at: '2026-06-22T00:00:00Z',
    })
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('renders the sample import close action as a Back button', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport onClose={onClose} />)
    })

    const closeButtons = Array.from(container.querySelectorAll('button'))
      .filter(button => button.textContent?.trim() === 'Back')
    expect(closeButtons).toHaveLength(1)

    await act(async () => {
      closeButtons[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('ignores a conversion that finishes after a source option changes', async () => {
    let resolveImport: ((value: unknown) => void) | undefined
    apiMock.importSample.mockImplementationOnce(() => new Promise(resolve => { resolveImport = resolve }))
    await act(async () => {
      root.render(<SampleScriptImport projectId="project-1" />)
    })

    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Convert Sample'))
    await act(async () => {
      convert?.click()
      await Promise.resolve()
    })
    const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="Order Flow Sample"]')
    await act(async () => {
      if (nameInput) setControlValue(nameInput, 'Changed while converting')
    })
    await act(async () => {
      resolveImport?.({
        script: { name: 'Stale result', protocol: 'http', config: { base_url: 'https://stale.test' }, actions: [] },
        source_secret_session_id: 'stale-sample-session',
        warnings: [],
        correlations: [],
        stats: { format: 'rest_json', total_requests: 1, converted_actions: 0, assertions: 0, extractors: 0, correlations_detected: 0 },
      })
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('3. Review Generated Script')
    expect(container.textContent).not.toContain('Stale result')
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'stale-sample-session',
    })
  })

  it('publishes a completed save even when the sample review changes in flight', async () => {
    const onImported = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.createScript.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))
    await act(async () => {
      root.render(<SampleScriptImport projectId="project-1" onImported={onImported} />)
    })

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Sample'))?.click()
    })
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Save and Continue Importing'))?.click()
    })
    const nameInput = container.querySelector<HTMLInputElement>('input[placeholder="Order Flow Sample"]')
    await act(async () => {
      if (nameInput) setControlValue(nameInput, 'Changed after save started')
    })
    await act(async () => {
      resolveSave({
        id: 'saved-after-change',
        name: 'POST_v1_session',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      })
      await Promise.resolve()
    })

    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'saved-after-change' })],
      stayInImportReview: true,
    }))
  })

  it('publishes a save completed after Back without navigating away from the next review', async () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.createScript.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))
    await act(async () => {
      root.render(<SampleScriptImport projectId="project-1" onClose={onClose} onImported={onImported} />)
    })

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Sample'))?.click()
    })
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Save and Continue Importing'))?.click()
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.trim() === 'Back')?.click()
    })
    await act(async () => {
      resolveSave({
        id: 'saved-after-back',
        name: 'POST_v1_session',
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      })
      await Promise.resolve()
    })

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'saved-after-back' })],
      stayInImportReview: true,
    }))
  })

  it('reuses one atomic operation id after a sample save loses its response', async () => {
    apiMock.commitGenericImportBatch
      .mockRejectedValueOnce(new Error('sample batch response lost'))
      .mockResolvedValueOnce({
        scripts: [{ id: 'sample-reconciled', name: 'POST_v1_session', protocol: 'http', version: '1.0.0' }],
        count: 1,
      })
    await act(async () => root.render(<SampleScriptImport projectId="project-1" />))
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Convert Sample'))?.click()
    })
    await flushPromises()

    const save = () => Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save and Continue Importing'))
    await act(async () => save()?.click())
    await flushPromises()
    expect(container.textContent).toContain('sample batch response lost')
    await act(async () => save()?.click())
    await flushPromises()

    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledTimes(2)
    expect(apiMock.commitGenericImportBatch.mock.calls[0][0]).toEqual(expect.objectContaining({
      source_profile: 'sample',
      operation_id: expect.any(String),
    }))
    expect(apiMock.commitGenericImportBatch.mock.calls[1][0].operation_id)
      .toBe(apiMock.commitGenericImportBatch.mock.calls[0][0].operation_id)
  })

  it('renders the Java over HTTP sample close action as a Back button', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport preset="java-http" onClose={onClose} />)
    })

    expect(container.textContent).toContain('Import Java over HTTP Samples')
    const closeButtons = Array.from(container.querySelectorAll('button'))
      .filter(button => button.textContent?.trim() === 'Back')
    expect(closeButtons).toHaveLength(1)

    await act(async () => {
      closeButtons[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('converts a sample response and saves the generated script', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport onImported={onImported} />)
    })

    expect(container.textContent).toContain('SOAP-shaped raw HTTP, curl, REST JSON, and HAR request samples can now be promoted into SOAP scripts')
    expect(container.textContent).toContain('Mixed sample sets still stay on the HTTP protocol surface')
    const pacingSelect = Array.from(container.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing'))
      ?.querySelector('select')
    const pacingDelay = Array.from(container.querySelectorAll('label'))
      .find(label => label.textContent?.includes('Pacing Delay (seconds)'))
      ?.querySelector('input')
    expect(pacingSelect).toBeTruthy()
    expect(pacingDelay).toBeTruthy()
    if (!pacingSelect || !pacingDelay) return
    await act(async () => {
      setControlValue(pacingSelect, 'fixed_delay')
      setControlValue(pacingDelay, '2.5')
    })
    expect(pacingDelay.value).toBe('2.5')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({
      project_id: undefined,
      format: 'rest_json',
      options: expect.objectContaining({
        include_assertions: true,
        infer_extractors: true,
        sanitize_sensitive: true,
      }),
    }))
    expect(container.textContent).toContain('Review Generated Script')
    expect(container.textContent).toContain('POST_v1_session')
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('rest_json_request')
    expect(container.textContent).toContain('actions[0]')
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 40)) })
    expect(container.textContent).toContain('session_id')
    const warningDetails = container.querySelector<HTMLButtonElement>('button[aria-label="View 1 import warning detail"]')
    expect(warningDetails).not.toBeNull()
    await act(async () => {
      warningDetails?.click()
    })
    expect(document.body.querySelector('[role="dialog"]')?.textContent)
      .toContain('no reusable correlation values were detected across the provided samples')
    await act(async () => {
      Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'))
        .find(button => button.textContent?.trim() === 'Close')
        ?.click()
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'POST_v1_session',
      protocol: 'http',
      metadata: expect.objectContaining({
        import: expect.objectContaining({
          source: 'sample-import',
          preserved: expect.objectContaining({
            correlation_suggestions: [expect.objectContaining({
              name: 'session_id',
              found_at: 'Step 1',
              used_at: ['Step 2'],
            })],
          }),
        }),
      }),
      config: expect.objectContaining({
        base_url: 'https://api.example.com',
        pacing: { type: 'fixed_delay', delay_ms: 2500 },
      }),
    }), undefined, null)
    expect(apiMock.commitGenericImportBatch).toHaveBeenCalledWith(expect.objectContaining({
      source_profile: 'sample',
      operation_id: expect.any(String),
      scripts: [expect.objectContaining({ name: 'POST_v1_session' })],
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        id: 'script-1',
        name: 'POST_v1_session',
        protocol: 'http',
        script: expect.objectContaining({
          metadata: expect.objectContaining({
            import: expect.objectContaining({
              source: 'sample-import',
            }),
          }),
        }),
      })],
    }))
  })

  it('saves a credential-preserving sample before opening the persisted editor', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    const importedScript = {
      name: 'Imported Login Sample',
      version: '1.0.0',
      protocol: 'http',
      metadata: {
        import: {
          source: 'sample-import',
          preserved: {
            server_import_attestation_v1: 'protected:sample-login',
          },
        },
      },
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: 'password=imported-runtime-password',
      }],
    }
    apiMock.importSample.mockResolvedValueOnce({
      scripts: [importedScript],
      script: importedScript,
      import_draft_expires_at: '2099-07-26T12:30:00Z',
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'rest_json',
        total_requests: 1,
        converted_actions: 1,
        assertions: 0,
        extractors: 0,
        correlations_detected: 0,
      },
      summary: {},
      secret_binding_requirements: [{
        id: 'login-password',
        path: '/actions/0/body',
        label: 'Login password',
        context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD',
        destination_value_preserved: true,
      }],
    })

    await act(async () => root.render(
      <SampleScriptImport onImported={onImported} onEdit={onEdit} />,
    ))
    const convertButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Convert Sample'))!
    await act(async () => convertButton.click())
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Save and Open in Visual Editor')
    expect(editButton).toBeDefined()
    await act(async () => editButton?.click())
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({ body: 'password=imported-runtime-password' })],
    }), undefined, null)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'script-1' })],
    }))
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('applies a prefilled secret reference from the primary save action', async () => {
    const redactedScript = {
      name: 'Imported Login',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        headers: { Authorization: 'Bearer [REDACTED]' },
      }],
    }
    const resolvedScript = {
      ...redactedScript,
      actions: [{
        ...redactedScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      }],
    }
    apiMock.importSample.mockResolvedValueOnce({
      script: redactedScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { format: 'rest_json', total_requests: 1, converted_actions: 1, assertions: 0, extractors: 0, correlations_detected: 0 },
      secret_binding_requirements: [{
        id: 'login-auth',
        path: '/actions/0/headers/Authorization',
        label: 'Login — Authorization header',
        context: 'Bearer credential',
        suggested_name: 'LOGIN_TOKEN',
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedScript,
      secret_binding_requirements: [],
    })

    await act(async () => {
      root.render(<SampleScriptImport />)
    })
    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    await act(async () => convertButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))
    expect(save?.disabled).toBe(false)
    await act(async () => save?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith(expect.objectContaining({
      bindings: [{ requirement_id: 'login-auth', secret_name: 'LOGIN_TOKEN' }],
    }))
    expect(container.textContent).toContain('{{secret.LOGIN_TOKEN}}')
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({
        headers: { Authorization: 'Bearer {{secret.LOGIN_TOKEN}}' },
      })],
    }), undefined, null)
  })

  it('keeps a newer conversion when an older credential application resolves late', async () => {
    const result = (name: string, requirements: unknown[]) => ({
      script: { name, version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }] },
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [], correlations: [],
      stats: { format: 'raw_http', total_requests: 1, converted_actions: 1, assertions: 0, extractors: 0, correlations_detected: 0 },
      secret_binding_requirements: requirements,
    })
    const requirement = { id: 'auth', path: '/actions/0/headers/Authorization', label: 'Authorization', context: 'Bearer credential', suggested_name: 'API_TOKEN' }
    apiMock.importSample
      .mockResolvedValueOnce(result('Generation A', [requirement]))
      .mockResolvedValueOnce(result('Generation B', []))
    let resolveApply: ((value: unknown) => void) | undefined
    apiMock.applySecretBindings.mockReturnValueOnce(new Promise(resolve => { resolveApply = resolve }))

    await act(async () => root.render(<SampleScriptImport />))
    const convertButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Convert Sample'))!
    await act(async () => convertButton.click())
    await flushPromises()
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save and Continue Importing with 1 secret to configure'))!
    act(() => saveButton.click())
    await flushPromises()
    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(1)

    await act(async () => convertButton.click())
    await flushPromises()
    await act(async () => {
      resolveApply?.({ script: result('Resolved A', []).script, secret_binding_requirements: [] })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Generation B')
    expect(container.textContent).not.toContain('Resolved A')
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('stores sample authentication options as governed secret references', async () => {
    await act(async () => {
      root.render(<SampleScriptImport />)
    })
    const authentication = Array.from(container.querySelectorAll('label'))
      .find(label => label.querySelector('span')?.textContent === 'Authentication')
      ?.querySelector('select')
    expect(authentication).not.toBeNull()
    await act(async () => setControlValue(authentication!, 'basic'))

    const bindingInput = container.querySelector<HTMLInputElement>('input[placeholder="LOGIN_PASSWORD"]')
    expect(bindingInput).not.toBeNull()
    await act(async () => setControlValue(bindingInput!, 'SAMPLE_LOGIN_PASSWORD'))

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    await act(async () => convertButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    await act(async () => saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      config: expect.objectContaining({
        http: expect.objectContaining({
          auth: expect.objectContaining({ password: '{{secret.SAMPLE_LOGIN_PASSWORD}}' }),
        }),
      }),
    }), undefined, null)
    expect(JSON.stringify(apiMock.createScript.mock.calls[0]?.[0])).not.toContain('"password":"SAMPLE_LOGIN_PASSWORD"')
  })

  it('rejects raw credentials in sensitive default headers', async () => {
    await act(async () => {
      root.render(<SampleScriptImport />)
    })
    const headersInput = container.querySelector<HTMLTextAreaElement>('textarea[placeholder*=x-tenant-id]')
    expect(headersInput).not.toBeNull()
    await act(async () => setControlValue(headersInput!, '{"Authorization":"Bearer pasted-token"}'))

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    await act(async () => convertButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))

    expect(apiMock.importSample).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Default header Authorization is sensitive')
    expect(container.textContent).toContain('{{secret.NAME}}')
  })

  it('sends the HAR static asset option and surfaces filtered asset counts', async () => {
    apiMock.importSample.mockResolvedValueOnce({
      script: {
        name: 'Imported_HAR_Script',
        protocol: 'http',
        config: { base_url: 'https://shop.example.com' },
        actions: [{ name: 'GET api orders', type: 'request', method: 'GET', target: '/api/orders' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'har',
        total_requests: 3,
        converted_actions: 1,
        filtered_assets: 2,
        assertions: 1,
        extractors: 0,
        correlations_detected: 0,
      },
    })

    await act(async () => {
      root.render(<SampleScriptImport />)
    })

    const formatSelect = container.querySelector('select') as HTMLSelectElement | null
    expect(formatSelect).not.toBeNull()
    if (!formatSelect) return

    await act(async () => {
      formatSelect.value = 'har'
      formatSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const includeStatic = Array.from(container.querySelectorAll('input[type="checkbox"]')).find(input => input.parentElement?.textContent?.includes('Include static assets')) as HTMLInputElement | undefined
    expect(includeStatic).toBeDefined()
    if (!includeStatic) return

    await act(async () => {
      includeStatic.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({
      format: 'har',
      options: expect.objectContaining({
        include_static_assets: true,
      }),
    }))
    expect(container.textContent).toContain('Filtered assets')
    expect(container.textContent).toContain('2')
  })

  it('uploads missing imported file assets before saving the generated script', async () => {
    apiMock.importSample.mockResolvedValueOnce({
      script: {
        name: 'Multipart Upload',
        protocol: 'http',
        config: { base_url: 'https://api.example.com' },
        actions: [{ name: 'POST upload', type: 'request', method: 'POST', target: '/upload', body: '{{file.upload_bin}}' }],
        data_sources: [{
          name: 'upload_bin',
          type: 'file',
          file_name: 'upload.bin',
          content_type: 'application/octet-stream',
          strategy: 'sequential',
        }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: {
        format: 'har',
        total_requests: 1,
        converted_actions: 1,
        assertions: 0,
        extractors: 0,
        correlations_detected: 0,
      },
      preflight: {
        valid: false,
        action_count: 1,
        expected_action_count: 1,
        data_source_count: 1,
        parameter_count: 0,
        warnings: ['Data source upload_bin needs an uploaded governed asset.'],
        checks: [{
          id: 'data_sources',
          label: 'Data source readiness',
          status: 'fail',
          detail: 'Data source upload_bin needs an uploaded governed asset.',
        }],
      },
    })

    await act(async () => {
      root.render(<SampleScriptImport projectId="project-1" />)
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Import Assets')
    expect(container.textContent).toContain('Missing asset')
    expect(container.textContent).toContain('0/1 ready')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing')) as HTMLButtonElement | undefined
    expect(saveButton?.disabled).toBe(true)

    const uploadInput = container.querySelector('input[aria-label="Upload asset for upload_bin"]') as HTMLInputElement | null
    expect(uploadInput).not.toBeNull()
    if (!uploadInput) return

    const file = new File(['abc'], 'upload.bin', { type: 'application/octet-stream' })
    Object.defineProperty(uploadInput, 'files', {
      configurable: true,
      value: [file],
    })
    await act(async () => {
      uploadInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.uploadImportAsset).toHaveBeenCalledWith('Multipart Upload', file, expect.objectContaining({
      project_id: 'project-1',
      name: 'upload_bin',
      content_type: 'application/octet-stream',
    }))
    expect(container.textContent).toContain('1/1 ready')
    expect(container.textContent).toContain('file:///tmp/upload.bin')
    expect(saveButton?.disabled).toBe(false)
  })

  it('promotes Java-over-HTTP sample imports into java_http drafts with bounded warnings', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport preset="java-http" onImported={onImported} />)
    })

    expect(container.textContent).toContain('Import Java over HTTP Samples')
    expect(container.textContent).toContain('base64-encoded serialized Java payloads')
    expect(container.textContent).not.toContain('REST JSON')
    // HAR is now an accepted Java-over-HTTP source format.
    expect(container.textContent).toContain('HAR / HAR Entry')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({
      project_id: undefined,
      format: 'raw_http',
    }))
    const warningDetails = container.querySelector<HTMLButtonElement>('button[aria-label^="View "][aria-label*=" import warning"]')
    expect(warningDetails).not.toBeNull()
    await act(async () => {
      warningDetails?.click()
    })
    expect(document.body.querySelector('[role="dialog"]')?.textContent)
      .toContain('Java-over-HTTP imports assume request and response payloads are already base64-encoded serialized Java bytes')
    await act(async () => {
      Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'))
        .find(button => button.textContent?.trim() === 'Close')
        ?.click()
    })

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      protocol: 'java_http',
      config: expect.objectContaining({
        java_http: expect.objectContaining({
          codec: 'java_serialized_object_base64',
          body_encoding: 'base64',
        }),
      }),
    }), undefined, null)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        protocol: 'java_http',
      })],
    }))
  })

  it('switches the payload profile to Java over HTTP in-component and promotes the saved draft', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<SampleScriptImport onImported={onImported} />)
    })

    const profileSelect = Array.from(container.querySelectorAll('select')).find(select => select.getAttribute('aria-label') === 'Payload profile') as HTMLSelectElement | undefined
    const formatSelect = Array.from(container.querySelectorAll('select')).find(select => select.getAttribute('aria-label') === 'Sample format') as HTMLSelectElement | undefined
    expect(profileSelect).toBeDefined()
    expect(formatSelect).toBeDefined()
    if (!profileSelect || !formatSelect) return

    // The Standard API profile offers REST JSON.
    expect(profileSelect.value).toBe('default')
    expect(Array.from(formatSelect.options).map(option => option.textContent)).toContain('REST JSON')

    // Switch the payload profile in-component (the merged Java-over-HTTP entry point).
    await act(async () => {
      profileSelect.value = 'java-http'
      profileSelect.dispatchEvent(new Event('change', { bubbles: true }))
    })

    // The format set narrows (no REST JSON), defaults to raw HTTP, and the notice explains java_http promotion.
    expect(Array.from(formatSelect.options).map(option => option.textContent)).not.toContain('REST JSON')
    expect(formatSelect.value).toBe('raw_http')
    expect(container.textContent).toContain('promoted to the `java_http` protocol')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({ format: 'raw_http' }))

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(saveButton).toBeDefined()
    if (!saveButton) return
    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    // Saving promotes the draft to the java_http protocol even though the profile was switched at runtime.
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      protocol: 'java_http',
      config: expect.objectContaining({
        java_http: expect.objectContaining({ codec: 'java_serialized_object_base64' }),
      }),
    }), undefined, null)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ protocol: 'java_http' })],
    }))
  })

  it('warns when a Java-over-HTTP body is base64 but not a serialized object stream', async () => {
    apiMock.importSample.mockResolvedValueOnce({
      script: {
        name: 'java_call',
        protocol: 'http',
        config: { base_url: 'https://api.example.com' },
        actions: [
          // Valid base64, but decodes to "hello world!" — not a Java serialized stream.
          { name: '01 - POST java session', body: 'aGVsbG8gd29ybGQh', extractors: [] },
        ],
      },
      warnings: [],
      correlations: [],
      stats: { format: 'raw_http', total_requests: 1, converted_actions: 1, assertions: 0, extractors: 0, correlations_detected: 0 },
    })

    await act(async () => {
      root.render(<SampleScriptImport preset="java-http" />)
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const warningDetails = container.querySelector<HTMLButtonElement>('button[aria-label^="View "][aria-label*=" import warning"]')
    expect(warningDetails).not.toBeNull()
    await act(async () => {
      warningDetails?.click()
    })
    expect(document.body.querySelector('[role="dialog"]')?.textContent)
      .toContain('do not decode to a Java serialized object stream')
  })

  it('passes the active project id into sample conversion previews', async () => {
    await act(async () => {
      root.render(<SampleScriptImport projectId="project-1" />)
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Sample'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importSample).toHaveBeenCalledWith(expect.objectContaining({ project_id: 'project-1' }))
  })

  it('clears the drop animation when the dropped file read is superseded', async () => {
    await act(async () => {
      root.render(<SampleScriptImport />)
    })

    // Hold the read open so the supersede lands while it is still in flight.
    let releaseRead: (text: string) => void = () => {}
    const pendingText = new Promise<string>(resolve => { releaseRead = resolve })
    const file = new File(['{}'], 'sample.json')
    Object.defineProperty(file, 'text', { value: () => pendingText })

    const field = container.querySelector<HTMLElement>('.sample-import-field')
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [file], types: ['Files'] } })

    await act(async () => {
      field?.dispatchEvent(drop)
    })
    expect(container.querySelector('[data-testid="drop-ingest-overlay"]')?.textContent).toContain('Reading file')

    // Typing in the sample textarea invalidates the in-flight read. The dropped
    // content never lands, so the spinner must clear rather than run forever.
    const textarea = container.querySelector('textarea')
    await act(async () => {
      if (textarea) setControlValue(textarea, 'edited by hand')
    })

    await act(async () => {
      releaseRead('{}')
    })
    await flushPromises()

    expect(container.querySelector('[data-testid="drop-ingest-overlay"]')).toBeNull()
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

function setControlValue(control: HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement, value: string) {
  const prototype = control instanceof HTMLSelectElement
    ? window.HTMLSelectElement.prototype
    : control instanceof HTMLTextAreaElement
      ? window.HTMLTextAreaElement.prototype
      : window.HTMLInputElement.prototype
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  setter?.call(control, value)
  control.dispatchEvent(new Event(control instanceof HTMLSelectElement ? 'change' : 'input', { bubbles: true }))
}
