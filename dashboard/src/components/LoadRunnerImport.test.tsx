// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import LoadRunnerImport from './LoadRunnerImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getLoadRunnerSupportMatrix: vi.fn(),
  importLoadRunner: vi.fn(),
  validateLoadRunnerImport: vi.fn(),
  createScript: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  getBrowserReplaySession: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

describe('LoadRunnerImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getLoadRunnerSupportMatrix.mockResolvedValue({
      support_matrix: [
        {
          id: 'http_transport',
          area: 'HTTP',
          concept: 'Timeouts, redirects, and connection reuse',
          status: 'executed',
        },
        {
          id: 'logging',
          area: 'Logging',
          concept: 'Log level, error-only, and extended logging options',
          status: 'partial',
        },
        {
          id: 'streaming',
          area: 'Streaming',
          concept: 'Adaptive or fixed playback settings',
          status: 'compatibility_only',
        },
      ],
      notes: ['Streaming and media playback settings remain compatibility-only.'],
      tested_examples: [
        {
          title: 'Extended Web HTTP/HTML runtime import',
          coverage: 'Parses transport, pacing, logging, browser emulation, and DFE settings into Stormrunner config.',
          test: 'pkg/convert/loadrunner/converter_test.go::TestParseRuntimeCfgToScriptConfigMapsExtendedRuntimeSettings',
        },
      ],
    })
    apiMock.importLoadRunner.mockResolvedValue({
      script: {
        name: 'Demo LoadRunner Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }, { name: 'Submit Order' }, { name: 'Confirmation' }],
        metadata: {
          import: {
            source: 'loadrunner',
            source_script_name: 'Demo.usr',
            preserved: {
              runtime_files: { 'default.cfg': {} },
              parameter_files: ['LoginUsers.prm'],
              parameter_file_sections: {
                'LoginUsers.prm': {
                  LoginUser: {
                    Type: 'File',
                    Table: 'Users.dat',
                    ColumnName: 'username',
                    SelectNextRow: 'Sequential',
                    UpdateValueOn: 'Each Iteration',
                  },
                },
              },
              correlation_suggestions: [
                {
                  name: 'SessionId',
                  source: 'web_reg_save_param',
                  expression: 'LB=sessionId= RB=&',
                  sample_value: 'abc123',
                  found_at: 'Step 1',
                  used_at: ['Step 2', 'Step 3'],
                },
              ],
              runtime_settings_migration_readiness: {
                score_pct: 72,
                blocked: true,
                summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial.',
                blocking_statuses: ['partial'],
                blocking_settings: [
                  {
                    id: 'logging',
                    area: 'Logging',
                    concept: 'Log level, error-only, and extended logging options',
                    status: 'partial',
                    runtime_files: ['default.cfg'],
                  },
                ],
              },
              runtime_settings_parity: [
                {
                  id: 'run_logic_iterations',
                  area: 'Run Logic',
                  concept: 'Iterations',
                  status: 'executed',
                  runtime_files: ['default.cfg'],
                },
                {
                  id: 'logging',
                  area: 'Logging',
                  concept: 'Log level, error-only, and extended logging options',
                  status: 'partial',
                  runtime_files: ['default.cfg'],
                },
              ],
              project_settings: { General: { Type: 'Web' } },
              unsupported_functions: ['web_set_max_html_param_len'],
            },
          },
        },
        data_sources: [
          {
            name: 'users',
            type: 'csv',
            first_row_header: true,
            inline_data: 'username\nalice\nbob',
          },
        ],
      },
      warnings: ['manual review recommended'],
      unsupported_elements: ['web_set_max_html_param_len'],
      assumptions: [],
      review_sections: [
        {
          key: 'unsupported_elements',
          title: 'Unsupported LoadRunner Elements',
          severity: 'blocker',
          summary: 'These source constructs were preserved for review but were not converted into executable Stormrunner actions.',
          items: ['web_set_max_html_param_len'],
        },
        {
          key: 'preserved_metadata',
          title: 'Preserved LoadRunner Metadata',
          severity: 'info',
          summary: 'Source project metadata was saved with the generated script for audit and repair workflows.',
          items: ['Project file: Demo.usr'],
        },
      ],
      repair_suggestions: [
        {
          id: 'unsupported:web-set-max-html-param-len',
          category: 'unsupported',
          severity: 'blocker',
          title: 'Replace unsupported web_set_max_html_param_len',
          description: 'Review the preserved source construct.',
          action: 'Open the generated script in the visual editor.',
          target_action: 'Homepage',
        },
        {
          id: 'runtime:review-preserved-settings',
          category: 'runtime',
          severity: 'info',
          title: 'Review preserved runtime settings',
          description: 'The source archive included LoadRunner runtime metadata that may not all execute natively in Stormrunner yet.',
          action: 'Compare the preserved runtime file and project settings against the generated Stormrunner config before promoting the script.',
        },
      ],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions', 'General'],
        runtime_files: ['default.cfg'],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Migration readiness score 100%. No imported runtime settings matched the selected block statuses: blocked, partial, compatibility_only.',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
        unsupported_function_count: 1,
      },
      stats: {
        total_functions: 12,
        converted_actions: 1,
        extractors: 1,
        think_times: 2,
        transactions: 1,
        skipped: 0,
      },
    })
    apiMock.validateLoadRunnerImport.mockResolvedValue({
      script: {
        name: 'Demo LoadRunner Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: ['validation review'],
      unsupported_elements: ['web_set_max_html_param_len'],
      assumptions: [],
      review_sections: [
        {
          key: 'unsupported_elements',
          title: 'Unsupported LoadRunner Elements',
          severity: 'blocker',
          summary: 'These source constructs were preserved for review but were not converted into executable Stormrunner actions.',
          items: ['web_set_max_html_param_len'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions', 'General'],
        runtime_files: ['default.cfg'],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Migration readiness score 100%. No imported runtime settings matched the selected block statuses: blocked, partial, compatibility_only.',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
        unsupported_function_count: 1,
      },
      stats: {
        total_functions: 12,
        converted_actions: 1,
        extractors: 1,
        think_times: 2,
        transactions: 1,
        skipped: 0,
      },
    })
    apiMock.createScript.mockResolvedValue({ id: 'saved-script' })
    apiMock.startDraftBrowserReplaySession.mockResolvedValue({
      script_id: '',
      replay_session_id: 'draft-browser-validation',
      session: {
        id: 'draft-browser-validation',
        status: 'running',
        group: 'default',
        protocol: 'browser',
        current_event_index: 0,
      },
      warnings: [],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('imports a zip and saves the converted script', async () => {
    const onImported = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onImported={onImported} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('SOAP-aware detection is enabled for LoadRunner request assets')
    expect(container.textContent).toContain('Java-over-HTTP import remains unavailable')
    expect(container.textContent).toContain('Runtime Settings Coverage')
    expect(container.textContent).toContain('Executed')
    expect(container.textContent).toContain('Partial')
    expect(container.textContent).toContain('Compatibility-only')
    expect(container.textContent).toContain('HTTP: Timeouts, redirects, and connection reuse')
    expect(container.textContent).toContain('Tested Examples')
    expect(container.textContent).toContain('Known Caveats')
    expect(container.textContent).toContain('Streaming and media playback settings remain compatibility-only')

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importLoadRunner).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: true,
      migration_block_statuses: ['blocked', 'partial', 'compatibility_only'],
    })
    expect(container.textContent).toContain('Conversion Complete')
    expect(container.textContent).toContain('Demo LoadRunner Script')
    expect(container.textContent).toContain('manual review recommended')
    expect(container.textContent).toContain('Unsupported LoadRunner Functions (1)')
    expect(container.textContent).toContain('Import Review Sections (2)')
    expect(container.textContent).toContain('Repair Suggestions (2)')
    expect(container.textContent).toContain('Replace unsupported web_set_max_html_param_len')
    expect(container.textContent).toContain('Review preserved runtime settings')
    expect(container.textContent).toContain('Unsupported LoadRunner Elements')
    expect(container.textContent).toContain('blocker')
    expect(container.textContent).toContain('web_set_max_html_param_len')
    expect(container.textContent).toContain('Demo.usr')
    expect(container.textContent).toContain('default.cfg')
    expect(container.textContent).toContain('Migration Readiness')
    expect(container.textContent).toContain('100%')
    expect(container.textContent).toContain('Migration Readiness Blocking Settings')
    expect(container.textContent).toContain('Partial: Logging')
    expect(container.textContent).toContain('Runtime Settings Parity')
    expect(container.textContent).toContain('Executed: Run Logic')
    expect(container.textContent).toContain('Correlation Suggestions')
    expect(container.textContent).toContain('SessionId')
    expect(container.textContent).toContain('Found in Step 1')
    expect(container.textContent).toContain('Used in Step 2, Step 3')
    expect(container.textContent).toContain('Parameter File Mappings')
    expect(container.textContent).toContain('LoginUsers.prm')
    expect(container.textContent).toContain('LoginUser')
    expect(container.textContent).toContain('column username')
    expect(container.textContent).toContain('table Users.dat')
    expect(container.textContent).toContain('Generated Data Sources')
    expect(container.textContent).toContain('users')
    expect(container.textContent).toContain('2 rows')

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save to Script Library'))
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }))
  })


  it('runs validate-only review with preserve transaction option', async () => {
    await act(async () => {
      root.render(<LoadRunnerImport />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes).toHaveLength(5)
    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      checkboxes[2].dispatchEvent(new MouseEvent('click', { bubbles: true }))
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

    expect(apiMock.validateLoadRunnerImport).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: false,
      migration_block_statuses: ['blocked', 'compatibility_only'],
    })
    expect(container.textContent).toContain('Validation Review')
    expect(container.textContent).toContain('validation review')
    expect(container.textContent).toContain('Unsupported LoadRunner Functions (1)')
  })

  it('disables saving while migration readiness is blocked by the active policy', async () => {
    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Blocked Runtime Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [
        {
          key: 'runtime_settings_migration_readiness',
          title: 'Migration Readiness',
          severity: 'warning',
          summary: 'Runtime settings currently block promotion under the active policy.',
          items: ['Logging'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions'],
        runtime_files: ['default.cfg'],
        migration_readiness: {
          score_pct: 72,
          blocked: true,
          summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial.',
          blocking_statuses: ['partial'],
          blocking_settings: [
            {
              id: 'logging',
              area: 'Logging',
              concept: 'Log level, error-only, and extended logging options',
              status: 'partial',
              runtime_files: ['default.cfg'],
            },
          ],
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })

    await act(async () => {
      root.render(<LoadRunnerImport />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save to Script Library')) as HTMLButtonElement | undefined
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    expect(saveButton.disabled).toBe(true)
    expect(container.textContent).toContain('Saving is disabled until the migration readiness blockers are resolved or the active policy is relaxed.')

    await act(async () => {
      saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('opens targeted repair suggestions in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openTargetButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Target Action In Editor'))
    expect(openTargetButton).toBeDefined()
    if (!openTargetButton) {
      return
    }

    await act(async () => {
      openTargetButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens the imported script in the visual editor without an additional focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openImportedScriptButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Imported Script In Editor'))
    expect(openImportedScriptButton).toBeDefined()
    if (!openImportedScriptButton) {
      return
    }

    await act(async () => {
      openImportedScriptButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), undefined)
  })

  it('opens an unsupported function repair directly in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openFixButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Fix In Editor'))
    expect(openFixButton).toBeDefined()
    if (!openFixButton) {
      return
    }

    await act(async () => {
      openFixButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens imported runtime review in the visual editor with a runtime focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openRuntimeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Runtime Settings In Editor'))
    expect(openRuntimeButton).toBeDefined()
    if (!openRuntimeButton) {
      return
    }

    await act(async () => {
      openRuntimeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'runtime' })
  })

  it('opens a runtime repair suggestion in the visual editor with a runtime focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openRuntimeSuggestionButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Runtime Review In Editor'))
    expect(openRuntimeSuggestionButton).toBeDefined()
    if (!openRuntimeSuggestionButton) {
      return
    }

    await act(async () => {
      openRuntimeSuggestionButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'runtime' })
  })

  it('opens a specific runtime area in the visual editor with a runtime area focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openLoggingButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Logging Settings In Editor'))
    expect(openLoggingButton).toBeDefined()
    if (!openLoggingButton) {
      return
    }

    await act(async () => {
      openLoggingButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'runtime', targetRuntimeArea: 'Logging' })
  })

  it('opens a correlation source action in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openSourceButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Source Action In Editor'))
    expect(openSourceButton).toBeDefined()
    if (!openSourceButton) {
      return
    }

    await act(async () => {
      openSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens a correlation usage action in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openUsageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Step 2 In Editor'))
    expect(openUsageButton).toBeDefined()
    if (!openUsageButton) {
      return
    }

    await act(async () => {
      openUsageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetAction: 'Submit Order' })
  })

  it('opens the first correlation source summary jump in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openSummarySourceButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open First Correlation Source In Editor'))
    expect(openSummarySourceButton).toBeDefined()
    if (!openSummarySourceButton) {
      return
    }

    await act(async () => {
      openSummarySourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens the first correlation usage summary jump in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openSummaryUsageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open First Correlation Use In Editor'))
    expect(openSummaryUsageButton).toBeDefined()
    if (!openSummaryUsageButton) {
      return
    }

    await act(async () => {
      openSummaryUsageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetAction: 'Submit Order' })
  })

  it('opens imported parameter data review in the visual editor with a parameter-data focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openParameterDataButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Parameter Data In Editor'))
    expect(openParameterDataButton).toBeDefined()
    if (!openParameterDataButton) {
      return
    }

    await act(async () => {
      openParameterDataButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'parameter-data' })
  })

  it('opens parameter files from the summary card in the visual editor with a parameter-data focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openParameterFilesButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Parameter Files In Editor'))
    expect(openParameterFilesButton).toBeDefined()
    if (!openParameterFilesButton) {
      return
    }

    await act(async () => {
      openParameterFilesButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'parameter-data' })
  })

  it('opens the first generated data source summary jump in the visual editor with a data source focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openFirstDataSourceButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open First Data Source In Editor'))
    expect(openFirstDataSourceButton).toBeDefined()
    if (!openFirstDataSourceButton) {
      return
    }

    await act(async () => {
      openFirstDataSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('opens a generated source in the visual editor with a data source focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openDataSourceButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Source In Editor'))
    expect(openDataSourceButton).toBeDefined()
    if (!openDataSourceButton) {
      return
    }

    await act(async () => {
      openDataSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('opens the first runtime setting summary jump in the visual editor with a runtime area focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openFirstRuntimeButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open First Runtime Setting In Editor'))
    expect(openFirstRuntimeButton).toBeDefined()
    if (!openFirstRuntimeButton) {
      return
    }

    await act(async () => {
      openFirstRuntimeButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'runtime', targetRuntimeArea: 'Run Logic' })
  })

  it('opens the first blocking runtime setting summary jump in the visual editor with a runtime area focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openBlockingSettingButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open First Blocking Setting In Editor'))
    expect(openBlockingSettingButton).toBeDefined()
    if (!openBlockingSettingButton) {
      return
    }

    await act(async () => {
      openBlockingSettingButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'runtime', targetRuntimeArea: 'Logging' })
  })

  it('relaxes a blocked runtime migration policy and reruns the current review', async () => {
    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Blocked Runtime Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
        metadata: {
          import: {
            source: 'loadrunner',
            source_script_name: 'Demo.usr',
            preserved: {
              runtime_settings_migration_readiness: {
                score_pct: 72,
                blocked: true,
                summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial.',
                blocking_statuses: ['partial'],
                blocking_settings: [
                  {
                    id: 'logging',
                    area: 'Logging',
                    concept: 'Log level, error-only, and extended logging options',
                    status: 'partial',
                    runtime_files: ['default.cfg'],
                  },
                ],
              },
            },
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [
        {
          key: 'runtime_settings_migration_readiness',
          title: 'Migration Readiness',
          severity: 'warning',
          summary: 'Runtime settings currently block promotion under the active policy.',
          items: ['Logging'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions'],
        runtime_files: ['default.cfg'],
        migration_readiness: {
          score_pct: 72,
          blocked: true,
          summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial.',
          blocking_statuses: ['partial'],
          blocking_settings: [
            {
              id: 'logging',
              area: 'Logging',
              concept: 'Log level, error-only, and extended logging options',
              status: 'partial',
              runtime_files: ['default.cfg'],
            },
          ],
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Blocked Runtime Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions'],
        runtime_files: ['default.cfg'],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Migration readiness score 100%. No imported runtime settings matched the selected block statuses: blocked, compatibility_only.',
          policy: { block_statuses: ['blocked', 'compatibility_only'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Blocked Runtime Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [
        {
          key: 'runtime_settings_migration_readiness',
          title: 'Migration Readiness',
          severity: 'warning',
          summary: 'Runtime settings currently block promotion under the active policy.',
          items: ['Logging'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions'],
        runtime_files: ['default.cfg'],
        migration_readiness: {
          score_pct: 72,
          blocked: true,
          summary: 'Migration readiness score 72%. Blocking runtime settings matched the selected block statuses: partial.',
          blocking_statuses: ['partial'],
          blocking_settings: [
            {
              id: 'logging',
              area: 'Logging',
              concept: 'Log level, error-only, and extended logging options',
              status: 'partial',
              runtime_files: ['default.cfg'],
            },
          ],
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })

    await act(async () => {
      root.render(<LoadRunnerImport />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Selected migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Partial, Compatibility Only')

    const relaxPolicyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Allow Partial Runtime Settings And Refresh Review'))
    expect(relaxPolicyButton).toBeDefined()
    if (!relaxPolicyButton) {
      return
    }

    await act(async () => {
      relaxPolicyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importLoadRunner).toHaveBeenCalledTimes(2)
    expect(apiMock.importLoadRunner.mock.calls[1]).toEqual([
      file,
      {
        name: undefined,
        project_id: undefined,
        base_url: undefined,
        include_assertions: true,
        preserve_transactions: true,
        migration_block_statuses: ['blocked', 'compatibility_only'],
      },
    ])

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    expect(checkboxes[2]?.checked).toBe(false)
    expect(container.textContent).toContain('Selected migration block policy: Blocked, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Compatibility Only')
    expect(container.textContent).toContain('Conversion refreshed with migration block policy: Blocked, Compatibility Only.')

    const restorePolicyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Restore Recommended Block Policy'))
    expect(restorePolicyButton).toBeDefined()
    if (!restorePolicyButton) {
      return
    }

    await act(async () => {
      restorePolicyButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.importLoadRunner).toHaveBeenCalledTimes(3)
    expect(apiMock.importLoadRunner.mock.calls[2]).toEqual([
      file,
      {
        name: undefined,
        project_id: undefined,
        base_url: undefined,
        include_assertions: true,
        preserve_transactions: true,
        migration_block_statuses: ['blocked', 'partial', 'compatibility_only'],
      },
    ])
    expect(checkboxes[2]?.checked).toBe(true)
    expect(container.textContent).toContain('Selected migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Conversion refreshed with migration block policy: Blocked, Partial, Compatibility Only.')
  })

  it('shows pending migration policy changes until the review is refreshed', async () => {
    await act(async () => {
      root.render(<LoadRunnerImport />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Selected migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Partial, Compatibility Only')

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    await act(async () => {
      checkboxes[2]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Selected migration block policy: Blocked, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Refresh review to apply the selected migration block policy.')
  })

  it('opens a mapped parameter source in the visual editor with a mapping focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const openMappedDataSourceButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Mapped Data Source In Editor'))
    expect(openMappedDataSourceButton).toBeDefined()
    if (!openMappedDataSourceButton) {
      return
    }

    await act(async () => {
      openMappedDataSourceButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo LoadRunner Script',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('starts draft browser replay validation for converted browser imports before save', async () => {
    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
        metadata: {
          import: {
            source: 'trueclient_mobile_web',
            source_script_name: 'default.xml',
            preserved: {},
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    let resolveReplayStart: ((value: {
      script_id: string
      replay_session_id: string
      session: {
        id: string
        status: string
        group: string
        protocol: string
        current_event_index: number
      }
      warnings: string[]
    }) => void) | null = null
    apiMock.startDraftBrowserReplaySession.mockReturnValueOnce(new Promise((resolve) => {
      resolveReplayStart = resolve
    }))

    await act(async () => {
      root.render(<LoadRunnerImport projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Browser Replay Validation')
    expect(container.textContent).toContain('Not Run')

    const validateReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay'))
    expect(validateReplayButton).toBeDefined()
    if (!validateReplayButton) {
      return
    }

    await act(async () => {
      validateReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Starting')
    expect(container.textContent).toContain('Wait for the draft browser replay to finish before saving it to the library.')
    expect(container.textContent).toContain('Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving.')
    const startingReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Starting Draft Browser Replay...')) as HTMLButtonElement | undefined
    expect(startingReplayButton).toBeDefined()
    expect(startingReplayButton?.disabled).toBe(true)
    const inProgressSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Draft Browser Replay In Progress')) as HTMLButtonElement | undefined
    expect(inProgressSaveButton).toBeDefined()
    expect(inProgressSaveButton?.disabled).toBe(true)

    await act(async () => {
      resolveReplayStart?.({
        script_id: '',
        replay_session_id: 'draft-browser-validation',
        session: {
          id: 'draft-browser-validation',
          status: 'running',
          group: 'default',
          protocol: 'browser',
          current_event_index: 0,
        },
        warnings: ['selector repair recommended'],
      })
    })

    await flushPromises()

    expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledWith(expect.objectContaining({
      ignore_think_time: true,
      browser: expect.objectContaining({
        engine: 'webkit',
        headless: true,
        device_profile: 'iphone_15_pro',
        touch_mode: true,
        orientation: 'portrait',
        capture_screenshots: 'per_action',
        viewport: { width: 393, height: 852 },
      }),
      script: expect.objectContaining({
        name: 'Imported TruClient Mobile Web',
        protocol: 'browser',
        project_id: 'project-1',
      }),
    }))
    expect(container.textContent).toContain('Draft browser replay started. Review the draft session before saving this import.')
    expect(container.textContent).toContain('Session: draft-browser-validation')
    expect(container.textContent).toContain('Status: Running')
    expect(container.textContent).toContain('selector repair recommended')
    expect(container.textContent).toContain('Wait for the draft browser replay to finish before saving it to the library.')
    const inProgressReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Draft Browser Replay In Progress')) as HTMLButtonElement | undefined
    expect(inProgressReplayButton).toBeDefined()
    expect(inProgressReplayButton?.disabled).toBe(true)
  })

  it('starts draft browser replay validation when saving a browser import for the first time', async () => {
    vi.useFakeTimers()

    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
        metadata: {
          import: {
            source: 'trueclient_mobile_web',
            source_script_name: 'default.xml',
            preserved: {},
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    let resolveReplayStart: ((value: {
      script_id: string
      replay_session_id: string
      session: {
        id: string
        status: string
        group: string
        protocol: string
        current_event_index: number
      }
      warnings: string[]
    }) => void) | null = null
    apiMock.startDraftBrowserReplaySession.mockReturnValueOnce(new Promise((resolve) => {
      resolveReplayStart = resolve
    }))
    apiMock.getBrowserReplaySession.mockResolvedValueOnce({
      session: {
        id: 'draft-browser-validation',
        status: 'completed',
        group: 'default',
        protocol: 'browser',
        current_event_index: 1,
      },
      warnings: ['selector repair recommended'],
    })

    try {
      await act(async () => {
        root.render(<LoadRunnerImport projectId="project-1" />)
      })

      const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
      expect(fileInput).not.toBeNull()
      if (!fileInput) {
        return
      }

      const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
      await act(async () => {
        Object.defineProperty(fileInput, 'files', {
          configurable: true,
          value: [file],
        })
        fileInput.dispatchEvent(new Event('change', { bubbles: true }))
      })

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
      expect(convertButton).toBeDefined()
      if (!convertButton) {
        return
      }

      await act(async () => {
        convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))
      expect(saveButton).toBeDefined()
      if (!saveButton) {
        return
      }

      await act(async () => {
        saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      expect(apiMock.createScript).not.toHaveBeenCalled()
      expect(container.textContent).toContain('Starting')
      expect(container.textContent).toContain('Draft Browser Replay In Progress')
      expect(container.textContent).toContain('Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving.')
      expect(container.textContent).toContain('Wait for the draft browser replay to finish before saving it to the library.')

      await act(async () => {
        resolveReplayStart?.({
          script_id: '',
          replay_session_id: 'draft-browser-validation',
          session: {
            id: 'draft-browser-validation',
            status: 'running',
            group: 'default',
            protocol: 'browser',
            current_event_index: 0,
          },
          warnings: ['selector repair recommended'],
        })
      })

      await flushPromises()

      expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledWith(expect.objectContaining({
        ignore_think_time: true,
        browser: expect.objectContaining({
          engine: 'webkit',
          headless: true,
          device_profile: 'iphone_15_pro',
          touch_mode: true,
          orientation: 'portrait',
          capture_screenshots: 'per_action',
          viewport: { width: 393, height: 852 },
        }),
        script: expect.objectContaining({
          name: 'Imported TruClient Mobile Web',
          protocol: 'browser',
          project_id: 'project-1',
        }),
      }))
      expect(container.textContent).toContain('Draft browser replay started. Review the draft session before saving this import.')
      expect(container.textContent).toContain('Session: draft-browser-validation')
      expect(container.textContent).toContain('Status: Running')
      expect(container.textContent).toContain('Draft Browser Replay In Progress')
      expect(container.textContent).toContain('Wait for the draft browser replay to finish before saving it to the library.')

      await act(async () => {
        vi.advanceTimersByTime(2000)
      })

      await flushPromises()

      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledWith('draft-browser-validation')
      expect(container.textContent).toContain('Status: Completed')
      expect(container.textContent).toContain('The latest draft browser replay completed. Review the replay results before saving it to the library.')

      const finalSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save to Script Library'))
      expect(finalSaveButton).toBeDefined()
      if (!finalSaveButton) {
        return
      }

      await act(async () => {
        finalSaveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
        name: 'Imported TruClient Mobile Web',
        protocol: 'browser',
      }), 'project-1')
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a draft-specific replay refresh error when replay status polling fails', async () => {
    vi.useFakeTimers()

    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
        metadata: {
          import: {
            source: 'trueclient_mobile_web',
            source_script_name: 'default.xml',
            preserved: {},
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
      script_id: '',
      replay_session_id: 'draft-browser-validation',
      session: {
        id: 'draft-browser-validation',
        status: 'running',
        group: 'default',
        protocol: 'browser',
        current_event_index: 0,
      },
      warnings: [],
    })
    apiMock.getBrowserReplaySession.mockRejectedValueOnce(undefined)

    try {
      await act(async () => {
        root.render(<LoadRunnerImport projectId="project-1" />)
      })

      const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
      expect(fileInput).not.toBeNull()
      if (!fileInput) {
        return
      }

      const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
      await act(async () => {
        Object.defineProperty(fileInput, 'files', {
          configurable: true,
          value: [file],
        })
        fileInput.dispatchEvent(new Event('change', { bubbles: true }))
      })

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
      expect(convertButton).toBeDefined()
      if (!convertButton) {
        return
      }

      await act(async () => {
        convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))
      expect(saveButton).toBeDefined()
      if (!saveButton) {
        return
      }

      await act(async () => {
        saveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      await act(async () => {
        vi.advanceTimersByTime(2000)
      })

      await flushPromises()

      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledWith('draft-browser-validation')
      expect(container.textContent).toContain('Draft browser replay status refresh failed')
      expect(container.textContent).toContain('Status: Running')
      expect(container.textContent).toContain('Draft Browser Replay In Progress')
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a start-specific replay error when the draft browser replay fails to start', async () => {
    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
        metadata: {
          import: {
            source: 'trueclient_mobile_web',
            source_script_name: 'default.xml',
            preserved: {},
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(undefined)

    await act(async () => {
      root.render(<LoadRunnerImport projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const startReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay'))
    expect(startReplayButton).toBeDefined()
    if (!startReplayButton) {
      return
    }

    await act(async () => {
      startReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Draft browser replay failed to start')
    expect(container.textContent).toContain('Start Draft Browser Replay')
    expect(container.textContent).toContain('Not Run')
  })

  it('shows the replay-first save label for browser validation reviews', async () => {
    apiMock.validateLoadRunnerImport.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
      },
      warnings: ['validation review'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })

    await act(async () => {
      root.render(<LoadRunnerImport projectId="project-1" />)
    })

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
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

    const replayFirstSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))
    expect(replayFirstSaveButton).toBeDefined()
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Reviewed Script'))).toBeUndefined()
    expect(container.textContent).toContain('Run a draft browser replay before saving the reviewed script.')
  })

  it('shows rerun copy after a browser validation replay fails', async () => {
    vi.useFakeTimers()

    apiMock.validateLoadRunnerImport.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
      },
      warnings: ['validation review'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
      script_id: '',
      replay_session_id: 'draft-browser-validation',
      session: {
        id: 'draft-browser-validation',
        status: 'running',
        group: 'default',
        protocol: 'browser',
        current_event_index: 0,
      },
      warnings: [],
    })
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
      script_id: '',
      replay_session_id: 'draft-browser-validation-rerun',
      session: {
        id: 'draft-browser-validation-rerun',
        status: 'running',
        group: 'default',
        protocol: 'browser',
        current_event_index: 0,
      },
      warnings: [],
    })
    apiMock.getBrowserReplaySession.mockResolvedValueOnce({
      session: {
        id: 'draft-browser-validation',
        status: 'failed',
        group: 'default',
        protocol: 'browser',
        current_event_index: 1,
      },
      warnings: [],
    })

    try {
      await act(async () => {
        root.render(<LoadRunnerImport projectId="project-1" />)
      })

      const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
      expect(fileInput).not.toBeNull()
      if (!fileInput) {
        return
      }

      const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
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

      const replayFirstSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))
      expect(replayFirstSaveButton).toBeDefined()
      if (!replayFirstSaveButton) {
        return
      }

      await act(async () => {
        replayFirstSaveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      await act(async () => {
        vi.advanceTimersByTime(2000)
      })

      await flushPromises()

      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledWith('draft-browser-validation')
      expect(container.textContent).toContain('Status: Failed')
      expect(container.textContent).toContain('Rerun Draft Browser Replay Before Saving')
      expect(container.textContent).toContain('Rerun a draft browser replay before saving the reviewed script.')
      expect(container.textContent).toContain('Rerun a draft browser replay against the converted browser script before saving it to the library.')

      const rerunReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Rerun Draft Browser Replay')
      expect(rerunReplayButton).toBeDefined()
      if (!rerunReplayButton) {
        return
      }

      await act(async () => {
        rerunReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledTimes(2)
      expect(container.textContent).toContain('Draft browser replay restarted. Review the refreshed draft session before saving this import.')
      expect(container.textContent).toContain('Session: draft-browser-validation-rerun')
      expect(container.textContent).toContain('Status: Running')
    } finally {
      vi.useRealTimers()
    }
  })

  it('preserves rerun replay copy when a replay rerun fails to start', async () => {
    vi.useFakeTimers()
    apiMock.validateLoadRunnerImport.mockResolvedValueOnce({
      script: {
        name: 'Validated Browser Script',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://example.com',
          browser: {
            engine: 'chromium',
          },
        },
        steps: [{
          id: 'step-1',
          name: 'Open page',
          action: { type: 'goto', url: 'https://example.com' },
        }],
      },
      warnings: [],
      review_sections: [],
      repair_suggestions: [],
      unsupported_elements: [],
      summary: {
        migration_readiness: {
          status_counts: {},
          blocked: false,
          warnings: [],
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })
    apiMock.startDraftBrowserReplaySession.mockResolvedValueOnce({
      script_id: '',
      replay_session_id: 'draft-browser-validation',
      session: {
        id: 'draft-browser-validation',
        status: 'running',
        group: 'default',
        protocol: 'browser',
        current_event_index: 0,
      },
      warnings: [],
    })
    apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(undefined)
    apiMock.getBrowserReplaySession.mockResolvedValueOnce({
      session: {
        id: 'draft-browser-validation',
        status: 'failed',
        group: 'default',
        protocol: 'browser',
        current_event_index: 1,
      },
      warnings: [],
    })

    try {
      await act(async () => {
        root.render(<LoadRunnerImport projectId="project-1" />)
      })

      const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
      expect(fileInput).not.toBeNull()
      if (!fileInput) {
        return
      }

      const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
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

      const replayFirstSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))
      expect(replayFirstSaveButton).toBeDefined()
      if (!replayFirstSaveButton) {
        return
      }

      await act(async () => {
        replayFirstSaveButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      await act(async () => {
        vi.advanceTimersByTime(2000)
      })

      await flushPromises()

      const rerunReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.trim() === 'Rerun Draft Browser Replay')
      expect(rerunReplayButton).toBeDefined()
      if (!rerunReplayButton) {
        return
      }

      await act(async () => {
        rerunReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledTimes(2)
      expect(container.textContent).toContain('Draft browser replay failed to start')
      expect(container.textContent).toContain('Status: Failed')
      expect(container.textContent).toContain('Session: draft-browser-validation')
      expect(container.textContent).toContain('Rerun Draft Browser Replay')
      expect(container.textContent).toContain('Rerun Draft Browser Replay Before Saving')
    } finally {
      vi.useRealTimers()
    }
  })

  it('opens a browser replay repair suggestion in the visual editor without an additional focus hint', async () => {
    const onEdit = vi.fn()

    apiMock.importLoadRunner.mockResolvedValueOnce({
      script: {
        name: 'Imported TruClient Mobile Web',
        version: '1.0',
        protocol: 'browser',
        config: {
          base_url: 'https://shop.example.test',
          browser: {
            engine: 'webkit',
            headless: true,
            device_profile: 'iphone_15_pro',
            touch_mode: true,
            orientation: 'portrait',
            viewport: { width: 393, height: 852 },
            capture_screenshots: 'on_failure',
          },
        },
        actions: [
          { name: 'Open Store', type: 'browser_navigate', url: '/store' },
        ],
        metadata: {
          import: {
            source: 'trueclient_mobile_web',
            source_script_name: 'default.xml',
            preserved: {},
          },
        },
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [
        {
          id: 'browser:replay-validation',
          category: 'replay',
          severity: 'warning',
          title: 'Run browser replay before saving as production-ready',
          description: 'Imported browser scripts depend on source selectors, waits, and device assumptions that should be verified against a live page.',
          action: 'Open the imported draft in the visual editor and run browser replay. Update selectors or waits for any failed action.',
        },
      ],
      summary: {
        project_file: 'default.xml',
        mapped_runtime_file: '',
        project_sections: [],
        runtime_files: [],
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked'] },
        },
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })

    await act(async () => {
      root.render(<LoadRunnerImport onEdit={onEdit} />)
    })

    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    const file = new File(['zip-content'], 'truclient.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert LoadRunner Script'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Run browser replay before saving as production-ready')

    const openImportedScriptButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Imported Script In Editor'))
    expect(openImportedScriptButton).toBeDefined()
    if (!openImportedScriptButton) {
      return
    }

    await act(async () => {
      openImportedScriptButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Imported TruClient Mobile Web',
      protocol: 'browser',
    }), undefined)
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}
