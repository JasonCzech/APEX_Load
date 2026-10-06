// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import JMeterImport from './JMeterImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getJMeterSupportMatrix: vi.fn(),
  validateJMeterImport: vi.fn(),
  importJMeter: vi.fn(),
  saveJMeterImport: vi.fn(),
  applySecretBindings: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
  applyImportParameterization: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
}))

describe('JMeterImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getJMeterSupportMatrix.mockResolvedValue({
      support_matrix: {
        supported: ['Thread Group', 'Regex Extractor', 'JSON Extractor'],
        partial: ['Variable substitution across URLs, headers, and bodies'],
        unsupported: ['JSR223 / BeanShell / Groovy scripting'],
        notes: ['Phase 1 targets HTTP-oriented JMX plans.'],
      },
    })
    apiMock.validateJMeterImport.mockResolvedValue({
      scripts: [
        {
          name: 'Auth_Users',
          actions: [
            {
              name: 'Create_Session',
              extractors: [
                { name: 'session_token', type: 'regex', expression: 'token=([^&]+)' },
                { name: 'access_token', type: 'jsonpath', expression: '$.access_token' },
              ],
            },
          ],
          data_sources: [],
        },
      ],
      warnings: [],
      unsupported_elements: [],
      assumptions: ['Flattened safely'],
      review_sections: [],
      repair_suggestions: [],
      source_review: {
        source_files: [{ path: 'extractors.jmx', language: 'xml', content: '<HTTPSamplerProxy testname="Create Session" />' }],
        mappings: [{ id: 'sampler', source_file: 'extractors.jmx', start_line: 1, function: 'HTTPSamplerProxy', target_path: 'actions[0]', target_name: 'Create_Session', target_kind: 'action', status: 'converted' }],
      },
      stats: {
        thread_groups: 1,
        samplers: 1,
        total_samplers: 2,
        skipped_samplers: 1,
        converted_actions: 1,
        headers: 0,
        variables: 0,
        csv_sources: 0,
        assertions: 0,
        timers: 0,
        unsupported: 0,
      },
      summary: { file_name: 'extractors.jmx' },
    })
    apiMock.importJMeter.mockReset()
    apiMock.saveJMeterImport.mockReset()
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
        replace_supported: true,
        source_secret_migration_policy_enabled: false,
      },
      secrets: [],
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
    apiMock.applyImportParameterization.mockImplementation(async ({ script }: { script: { actions?: Array<{ target?: string }> } }) => ({
      script: { ...script, actions: (script.actions || []).map(action => ({ ...action, target: `${action.target}?parameterized=true` })) },
      diffs: [],
      applied_count: 1,
    }))
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('retains the source and prevents upload when no project is selected', async () => {
    await act(async () => root.render(<JMeterImport initialFile={new File(['<jmeterTestPlan/>'], 'retained.jmx')} />))
    const inspect = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Inspect & review')!
    await act(async () => inspect.click())
    expect(apiMock.importJMeter).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Select a project in the workspace selector')
    expect(container.textContent).toContain('retained.jmx')
  })

  it('ignores a conversion response after an import option changes', async () => {
    let resolveImport: ((value: unknown) => void) | undefined
    apiMock.importJMeter.mockImplementationOnce(() => new Promise(resolve => { resolveImport = resolve }))
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['<jmeterTestPlan />'], 'race.jmx')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const convert = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Inspect & review'))
    await act(async () => {
      convert?.click()
      await Promise.resolve()
    })
    const includeAssertions = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))[0]
    await act(async () => includeAssertions?.click())
    await act(async () => {
      resolveImport?.({
        scripts: [{ name: 'Stale JMeter result', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [], data_sources: [] }],
        source_secret_session_id: 'stale-jmeter-session',
        warnings: [],
        unsupported_elements: [],
        assumptions: [],
        review_sections: [],
        repair_suggestions: [],
        stats: { thread_groups: 1, samplers: 0, total_samplers: 0, skipped_samplers: 0, converted_actions: 0, headers: 0, variables: 0, csv_sources: 0, assertions: 0, timers: 0, unsupported: 0 },
        summary: { file_name: 'race.jmx' },
      })
      await Promise.resolve()
    })

    expect(container.textContent).not.toContain('Stale JMeter result')
    expect(container.textContent).not.toContain('Review Converted Scripts')
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'stale-jmeter-session',
    })
  })

  it('locks source options until a deferred JMeter batch save settles', async () => {
    const onImported = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.saveJMeterImport.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" onImported={onImported} />)
    })
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['<jmeterTestPlan />'], 'save-race.jmx')],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Validate Only'))?.click()
    })
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => /^Save \d+ drafts?/.test(button.textContent || ''))?.click()
    })
    const includeAssertions = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))[0]
    expect(includeAssertions.disabled).toBe(true)
    const reviewedOption = includeAssertions.checked
    await act(async () => {
      includeAssertions?.click()
    })
    expect(includeAssertions.checked).toBe(reviewedOption)
    await act(async () => {
      resolveSave({
        count: 1,
        scripts: [{
          id: 'jmeter-saved-after-change',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        }],
      })
      await Promise.resolve()
    })

    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'jmeter-saved-after-change' })],
    }))
    expect(onImported.mock.calls[0]?.[0]).toHaveProperty('stayInImportReview', true)
  })

  it('reports a deferred batch save as busy until its persistence response settles', async () => {
    const onOperationBusyChange = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.saveJMeterImport.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" onOperationBusyChange={onOperationBusyChange} />)
    })
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['<jmeterTestPlan />'], 'deferred-save.jmx')],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Validate Only'))?.click()
    })
    await flushPromises()
    onOperationBusyChange.mockClear()

    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => /^Save \d+ drafts?/.test(button.textContent || ''))?.click()
      await Promise.resolve()
    })
    expect(onOperationBusyChange).toHaveBeenLastCalledWith(true)

    await act(async () => {
      resolveSave({
        count: 1,
        scripts: [{
          id: 'jmeter-deferred-save',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        }],
      })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(onOperationBusyChange).toHaveBeenLastCalledWith(false)
  })

  it('publishes a batch save completed after Back without navigating away from the next review', async () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    let resolveSave!: (value: unknown) => void
    apiMock.saveJMeterImport.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" onClose={onClose} onImported={onImported} />)
    })
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['<jmeterTestPlan />'], 'save-back-race.jmx')],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Validate Only'))?.click()
    })
    await flushPromises()
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => /^Save \d+ drafts?/.test(button.textContent || ''))?.click()
    })
    await act(async () => {
      Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.trim() === 'Back')?.click()
    })
    await act(async () => {
      resolveSave({
        count: 1,
        scripts: [{
          id: 'jmeter-saved-after-back',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        }],
      })
      await Promise.resolve()
    })

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'jmeter-saved-after-back' })],
      stayInImportReview: true,
    }))
  })

  it('renders the import close action as a Back button', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" onClose={onClose} />)
    })

    const closeButtons = Array.from(container.querySelectorAll('button'))
      .filter(button => button.textContent?.trim() === 'Back')
    expect(closeButtons).toHaveLength(1)

    await act(async () => {
      closeButtons[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('opens the file picker from the keyboard-accessible dropzone', async () => {
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })

    const dropzone = container.querySelector<HTMLElement>('.lr-dropzone')
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')
    expect(dropzone?.getAttribute('role')).toBe('button')
    expect(dropzone?.tabIndex).toBe(0)
    const clickSpy = fileInput ? vi.spyOn(fileInput, 'click') : null

    await act(async () => {
      dropzone?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    })

    expect(clickSpy).toHaveBeenCalledTimes(1)
  })

  it('renders inline coverage details and captured variables after validation', async () => {
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })

    await flushPromises()

    expect(container.textContent).toContain('HTTP plans convert most completely')
    expect(container.textContent).toContain('native database/network scripts')
    expect(container.textContent).toContain('JMS')

    const coverageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Import Coverage')
    expect(coverageButton).toBeDefined()
    if (!coverageButton) return
    await act(async () => {
      coverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Coverage Details')

    const coverageDetails = container.querySelector('details') as HTMLDetailsElement | null
    expect(coverageDetails).not.toBeNull()
    if (!coverageDetails) {
      return
    }

    coverageDetails.open = true
    coverageDetails.dispatchEvent(new Event('toggle'))

    expect(container.textContent).toContain('Regex Extractor')
    expect(container.textContent).toContain('JSON Extractor')

    const closeCoverageButton = container.querySelector('.import-coverage-modal-header button') as HTMLButtonElement | null
    if (closeCoverageButton) {
      await act(async () => {
        closeCoverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })
    }

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateJMeterImport).toHaveBeenCalledTimes(1)
    expect(apiMock.validateJMeterImport).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
    expect(container.textContent).toContain('Captured Variables (2)')
    expect(container.textContent).toContain('session_token')
    expect(container.textContent).toContain('access_token')
    expect(container.textContent).toContain('1 actions / 0 data sources / 2 extractors')
    expect(container.textContent).toContain('Validation Review')
    await act(async () => { container.querySelector<HTMLButtonElement>('[role=tab][aria-label="Source Mapping"]')?.click() })
    expect(container.textContent).toContain('Source Comparison')
    expect(container.textContent).toContain('HTTPSamplerProxy')
    expect(container.textContent).toContain('actions[0]')
    expect(container.textContent).toContain('Save 1 draft')
    const stats = Array.from(container.querySelectorAll('.import-evidence-facts > div')).map(item => item.textContent)
    expect(stats).toContain('Converted Samplers1')
    expect(stats).toContain('Total Samplers2')
    expect(stats).toContain('Skipped Samplers1')
  })

  it('renders structured coverage categories including adapter_required', async () => {
    apiMock.getJMeterSupportMatrix.mockResolvedValueOnce({
      support_matrix: {
        supported: ['HTTP Request samplers'],
        partial: [],
        unsupported: ['JMS Publisher / Subscriber / point-to-point samplers'],
      },
      support_matrix_entries: [
        { id: 'jmeter-http-sampler', area: 'Samplers', construct: 'HTTP Request samplers', status: 'supported', execution_status: 'executable' },
        { id: 'jmeter-jsr223', area: 'Scripting', construct: 'JSR223 / BeanShell / Groovy scripting', status: 'partial', execution_status: 'review_required', review_required: true },
        { id: 'jmeter-jdbc', area: 'Samplers', construct: 'JDBC Request samplers', status: 'supported', execution_status: 'executable' },
        { id: 'jmeter-jms', area: 'Samplers', construct: 'JMS Publisher / Subscriber / point-to-point samplers', status: 'review_only', execution_status: 'adapter_required', review_required: true, notes: 'Requires selecting a JMS provider before running.' },
      ],
      notes: ['adapter_required marks constructs that need an operator-supplied adapter or provider.'],
    })

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Import Coverage')
    const coverageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Import Coverage')
    expect(coverageButton).toBeDefined()
    if (!coverageButton) return
    await act(async () => {
      coverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(container.textContent).toContain('Mapped')
    expect(container.textContent).toContain('Review required')
    expect(container.textContent).toContain('Adapter required')

    const adapterDetails = container.querySelector('details[data-coverage="adapter_required"]') as HTMLDetailsElement | null
    expect(adapterDetails).not.toBeNull()
    expect(adapterDetails?.textContent).toContain('JMS Publisher / Subscriber / point-to-point samplers')
    expect(adapterDetails?.textContent).toContain('Requires selecting a JMS provider before running')
    // adapter_required must be a distinct category from generic review.
    expect(container.querySelector('details[data-coverage="mapped"]')).not.toBeNull()
    expect(container.querySelector('.lr-preserved-card.coverage-adapter_required')).not.toBeNull()
  })

  it('routes the reviewed script through the shared edit handoff', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const editButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Draft in Visual Editor'))
    expect(editButton).toBeDefined()
    if (!editButton) {
      return
    }

    await act(async () => {
      editButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Auth_Users',
      actions: expect.any(Array),
    }))
  })

  it('saves credential-preserving JMeter scripts before opening the persisted editor', async () => {
    const onImported = vi.fn()
    const onEdit = vi.fn()
    const importedScript = {
      name: 'Credentialed JMeter Login',
      version: '1.0.0',
      protocol: 'http',
      metadata: {
        import: {
          source: 'jmeter',
          preserved: {
            server_import_attestation_v1: 'protected:jmeter-login',
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
      data_sources: [],
    }
    apiMock.validateJMeterImport.mockResolvedValueOnce({
      scripts: [importedScript],
      script: importedScript,
      import_draft_expires_at: '2099-07-26T12:30:00Z',
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      stats: {
        thread_groups: 1,
        samplers: 1,
        total_samplers: 1,
        skipped_samplers: 0,
        converted_actions: 1,
        headers: 0,
        variables: 0,
        csv_sources: 0,
        assertions: 0,
        timers: 0,
        unsupported: 0,
      },
      summary: { file_name: 'credentialed.jmx' },
      secret_binding_requirements: [{
        id: 'login-password',
        path: '/actions/0/body',
        label: 'Login password',
        context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD',
        script_index: 0,
        script_name: importedScript.name,
        destination_value_preserved: true,
      }],
    })
    apiMock.saveJMeterImport.mockResolvedValueOnce({
      count: 1,
      scripts: [{
        id: 'jmeter-credentialed',
        name: importedScript.name,
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
      }],
    })

    await act(async () => root.render(
      <JMeterImport projectId="project-1" onImported={onImported} onEdit={onEdit} />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['<jmeterTestPlan />'], 'credentialed.jmx', { type: 'application/xml' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const validateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Validate Only'))!
    await act(async () => validateButton.click())
    await flushPromises()

    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.trim() === 'Save and Open in Visual Editor')
    expect(editButton).toBeDefined()
    await act(async () => editButton?.click())
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.saveJMeterImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        actions: [expect.objectContaining({ body: 'password=imported-runtime-password' })],
      })],
    }))
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'jmeter-credentialed' })],
    }))
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('resolves a redacted credential on the correct script before saving a multi-script import', async () => {
    const firstScript = {
      name: 'Orders HTTP',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'List Orders', type: 'request', method: 'GET', target: '/orders' }],
    }
    const redactedSecondScript = {
      name: 'Orders Database',
      version: '1.0.0',
      protocol: 'database',
      config: { base_url: '', database: { dsn: '[REDACTED]' } },
      actions: [{ name: 'Select Orders', type: 'db_query', target: 'SELECT * FROM orders' }],
    }
    const resolvedSecondScript = {
      ...redactedSecondScript,
      config: { base_url: '', database: { dsn: '{{secret.ORDERS_DSN}}' } },
    }
    apiMock.validateJMeterImport.mockResolvedValueOnce({
      scripts: [firstScript, redactedSecondScript],
      script: firstScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      stats: {
        thread_groups: 2,
        samplers: 2,
        total_samplers: 2,
        skipped_samplers: 0,
        converted_actions: 2,
        headers: 0,
        variables: 0,
        csv_sources: 0,
        assertions: 0,
        timers: 0,
        unsupported: 0,
      },
      summary: { file_name: 'orders.jmx' },
      secret_binding_requirements: [{
        id: 'database-dsn',
        path: '/config/database/dsn',
        label: 'Orders Database — DSN',
        context: 'Database connection credential',
        suggested_name: 'ORDERS_DSN',
        script_index: 1,
        script_name: 'Orders Database',
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedSecondScript,
      secret_binding_requirements: [],
    })
    apiMock.saveJMeterImport.mockResolvedValueOnce({
      count: 2,
      scripts: [
        { id: 'saved-http', name: 'Orders HTTP', protocol: 'http', version: '1.0.0', created_at: '2026-01-01T00:00:00Z' },
        { id: 'saved-db', name: 'Orders Database', protocol: 'database', version: '1.0.0', created_at: '2026-01-01T00:00:00Z' },
      ],
    })

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')
    expect(fileInput).not.toBeNull()
    const file = new File(['<jmeterTestPlan />'], 'orders.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput!, 'files', { configurable: true, value: [file] })
      fileInput!.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const validateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Validate Only'))
    await act(async () => validateButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => /^Save \d+ drafts?/.test(button.textContent || ''))
    expect(saveButton?.disabled).toBe(false)
    await act(async () => saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true })))
    await flushPromises()
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: redactedSecondScript,
      script_index: 1,
      bindings: [{ requirement_id: 'database-dsn', secret_name: 'ORDERS_DSN' }],
    })
    await act(async () => { container.querySelector<HTMLButtonElement>('[role=tab][aria-label="DSL Preview"]')?.click() })
    expect(container.textContent).toContain('{{secret.ORDERS_DSN}}')
    expect(apiMock.saveJMeterImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ name: 'Orders HTTP' }),
        expect.objectContaining({
          name: 'Orders Database',
          config: expect.objectContaining({ database: { dsn: '{{secret.ORDERS_DSN}}' } }),
        }),
      ],
    }))
  })

  it('reuses a configured project secret when an atomic JMeter script save is retried', async () => {
    const redactedScript = {
      name: 'JMeter Credential Retry',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'List Orders',
        type: 'request',
        method: 'GET',
        target: '/orders',
        headers: { Authorization: '[REDACTED]' },
      }],
    }
    const resolvedScript = {
      ...redactedScript,
      actions: [{
        ...redactedScript.actions[0],
        headers: { Authorization: 'Bearer {{secret.JMETER_API_TOKEN}}' },
      }],
    }
    apiMock.validateJMeterImport.mockResolvedValueOnce({
      scripts: [redactedScript],
      script: redactedScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      stats: {
        thread_groups: 1,
        samplers: 1,
        total_samplers: 1,
        skipped_samplers: 0,
        converted_actions: 1,
        headers: 1,
        variables: 0,
        csv_sources: 0,
        assertions: 0,
        timers: 0,
        unsupported: 0,
      },
      summary: { file_name: 'credential-retry.jmx' },
      secret_binding_requirements: [{
        id: 'jmeter-api-token',
        path: '/actions/0/headers/Authorization',
        label: 'List Orders — Authorization header',
        context: 'Bearer credential used by the imported JMeter request.',
        suggested_name: 'JMETER_API_TOKEN',
        script_index: 0,
        script_name: redactedScript.name,
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedScript,
      secret_binding_requirements: [],
    })
    apiMock.saveJMeterImport
      .mockRejectedValueOnce(new Error('atomic JMeter script save unavailable'))
      .mockResolvedValueOnce({
        count: 1,
        scripts: [{
          id: 'saved-jmeter-retry',
          name: resolvedScript.name,
          protocol: resolvedScript.protocol,
          version: resolvedScript.version,
          created_at: '2026-01-01T00:00:00Z',
          script: resolvedScript,
        }],
      })

    await act(async () => root.render(<JMeterImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['<jmeterTestPlan />'], 'credential-retry.jmx', { type: 'application/xml' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const validate = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Validate Only'))!
    await act(async () => validate.click())
    await flushPromises()

    const valueInput = container.querySelector<HTMLInputElement>('input[aria-label="Secret value for JMETER_API_TOKEN"]')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(valueInput, 'write-only-jmeter-token')
      valueInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const save = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await act(async () => save.click())
    await flushPromises()
    await flushPromises()

    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.saveJMeterImport).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('atomic JMeter script save unavailable')
    expect(container.textContent).toContain('remain in Project Secrets independently of script saving')
    expect(container.textContent).toContain('retrying this save reuses those configured values')
    expect(container.textContent).not.toContain('write-only-jmeter-token')

    const retry = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await act(async () => retry.click())
    await flushPromises()

    expect(apiMock.saveJMeterImport).toHaveBeenCalledTimes(2)
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
  })

  it('uses script indexes for duplicate legacy names when parameterizing a multi-script review', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined)
    const first = { name: 'Shared Override', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'First', type: 'request', method: 'GET', target: '/first' }] }
    const second = { name: 'Shared Override', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Second', type: 'request', method: 'GET', target: '/second' }] }
    apiMock.validateJMeterImport.mockResolvedValueOnce({
      scripts: [first, second], script: first,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [],
      stats: { thread_groups: 2, samplers: 2, total_samplers: 2, skipped_samplers: 0, converted_actions: 2, headers: 0, variables: 0, csv_sources: 0, assertions: 0, timers: 0, unsupported: 0 },
      summary: { file_name: 'duplicates.jmx' },
      parameterization: { suggestions: [{
        id: 'shared-param', kind: 'data_param', confidence: 0.9, reason: 'Repeated value',
        script_name: 'Shared Override', variable_name: 'customer', literal_value: '42', sample_value: '42',
        replacement: '{{var.customer}}', locations: ['/actions/0/target'], auto_apply: true,
      }] },
      secret_binding_requirements: [],
    })
    apiMock.saveJMeterImport.mockResolvedValue({ count: 2, scripts: [] })

    await act(async () => root.render(<JMeterImport projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['<jmeterTestPlan/>'], 'duplicates.jmx')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const validate = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Validate Only'))!
    await act(async () => validate.click())
    await flushPromises()

    const applyButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).filter(button => button.textContent?.includes('Apply Selected'))
    expect(applyButtons).toHaveLength(1)
    const nextScript = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Next script'))!
    await act(async () => nextScript.click())
    const secondApply = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Apply Selected'))!
    await act(async () => secondApply.click())
    await flushPromises()
    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => /^Save \d+ drafts?/.test(button.textContent || ''))!
    await act(async () => save.click())
    await flushPromises()

    expect(apiMock.saveJMeterImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ actions: [expect.objectContaining({ target: '/first' })] }),
        expect.objectContaining({ actions: [expect.objectContaining({ target: '/second?parameterized=true' })] }),
      ],
    }))
    expect(consoleError.mock.calls.some(call => String(call[0]).includes('same key'))).toBe(false)
    consoleError.mockRestore()
  })

  it('ignores a late credential result after a newer JMeter review', async () => {
    const buildResult = (name: string, requirements: unknown[]) => ({
      scripts: [{ name, version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }] }],
      script: { name, version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'GET /', type: 'request', method: 'GET', target: '/' }] },
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [],
      stats: { thread_groups: 1, samplers: 1, total_samplers: 1, skipped_samplers: 0, converted_actions: 1, headers: 0, variables: 0, csv_sources: 0, assertions: 0, timers: 0, unsupported: 0 },
      summary: { file_name: 'race.jmx' },
      secret_binding_requirements: requirements,
    })
    const requirement = { id: 'auth', path: '/actions/0/headers/Authorization', label: 'Authorization', context: 'Bearer credential', suggested_name: 'API_TOKEN', script_index: 0 }
    apiMock.validateJMeterImport
      .mockResolvedValueOnce(buildResult('Generation A', [requirement]))
      .mockResolvedValueOnce(buildResult('Generation B', []))
    let resolveApply: ((value: unknown) => void) | undefined
    apiMock.applySecretBindings.mockReturnValueOnce(new Promise(resolve => { resolveApply = resolve }))

    await act(async () => root.render(<JMeterImport projectId="project-1" />))
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['<jmeterTestPlan />'], 'race.jmx')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    const validateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Validate Only'))!
    await act(async () => validateButton.click())
    await flushPromises()
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.textContent?.includes('Save 1 draft'))!
    act(() => saveButton.click())
    await flushPromises()
    await act(async () => validateButton.click())
    await flushPromises()
    await act(async () => {
      resolveApply?.({ script: buildResult('Resolved A', []).script, secret_binding_requirements: [] })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Generation B')
    expect(container.textContent).not.toContain('Resolved A')
    expect(apiMock.saveJMeterImport).not.toHaveBeenCalled()
  })

  it('adds the active project and selected destination folder when saving reviewed scripts', async () => {
    apiMock.saveJMeterImport.mockResolvedValueOnce({
      count: 1,
      scripts: [
        {
          id: 'jmeter-script-1',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        },
      ],
    })

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" folderId="folder-load-tests" />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => /^Save \d+ drafts?/.test(button.textContent || ''))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.saveJMeterImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ project_id: 'project-1', folder_id: 'folder-load-tests' })],
    }))
  })

  it('accepts a multi-plan JMeter ZIP archive', async () => {
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })
    await flushPromises()

    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    expect(fileInput.accept).toContain('.zip')
    const archive = new File(['PK\u0003\u0004'], 'plans.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [archive] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('plans.zip')

    const validateButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Validate Only'))!
    await act(async () => validateButton.click())
    await flushPromises()

    expect(apiMock.validateJMeterImport).toHaveBeenCalledWith(
      archive,
      expect.objectContaining({ project_id: 'project-1' }),
    )
  })

  it('accepts a LoadRunner Enterprise JMeter USZ', async () => {
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })
    await flushPromises()

    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]')!
    expect(fileInput.accept).toContain('.usz')
    const archive = new File(['PK\u0003\u0004'], 'LoadPlan.usz')
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [archive] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).toContain('LoadPlan.usz')
    expect(container.textContent).not.toContain('Please upload a JMeter')
  })

  it('returns the first persisted script through the import save handoff', async () => {
    const onImported = vi.fn()
    apiMock.saveJMeterImport.mockResolvedValueOnce({
      count: 1,
      scripts: [
        {
          id: 'jmeter-script-1',
          name: 'Auth_Users',
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        },
      ],
    })

    await act(async () => {
      root.render(<JMeterImport projectId="project-1" onImported={onImported} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => /^Save \d+ drafts?/.test(button.textContent || ''))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        id: 'jmeter-script-1',
        name: 'Auth_Users',
        protocol: 'http',
        script: expect.objectContaining({
          name: 'Auth_Users',
          actions: expect.arrayContaining([
            expect.objectContaining({
              name: 'Create_Session',
            }),
          ]),
        }),
      })],
    }))
  })

  it('passes the active project id into JMeter preview conversion', async () => {
    await act(async () => {
      root.render(<JMeterImport projectId="project-1" />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['<jmeterTestPlan />'], 'extractors.jmx', { type: 'application/xml' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Only'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateJMeterImport).toHaveBeenCalledWith(file, expect.objectContaining({ project_id: 'project-1' }))
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}
