// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import EnterpriseArchiveImport from './EnterpriseArchiveImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getEnterpriseArchiveSupportMatrix: vi.fn(),
  importEnterpriseArchive: vi.fn(),
  validateEnterpriseArchiveImport: vi.fn(),
  createScript: vi.fn(),
  createAuthoringSession: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  getBrowserReplaySession: vi.fn(),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

// CodeMirror can't measure layout under jsdom; render the pane's text directly so
// the source/DSL content is assertable. The real editor is verified in-browser.
vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
}))

const sourceReviewFixture = {
  source_files: [
    {
      path: 'Action.c',
      language: 'c',
      content: 'Action()\n{\n\tweb_url("Homepage",\n\t\t"URL=https://example.com/app/home",\n\t\tLAST);\n}',
      original_bytes: 96,
      redacted: true,
    },
  ],
  mappings: [
    {
      id: 'action-homepage',
      source_file: 'Action.c',
      start_line: 3,
      end_line: 5,
      function: 'web_url',
      target_path: 'actions[0]',
      target_name: 'Homepage',
      target_kind: 'action',
      status: 'converted',
      notes: 'Converted to a generated APEX Load action.',
    },
  ],
}

function cssRule(css: string, selector: string) {
  const selectorIndex = css.indexOf(`${selector} {`)
  expect(selectorIndex).toBeGreaterThanOrEqual(0)
  const bodyStart = css.indexOf('{', selectorIndex) + 1
  const bodyEnd = css.indexOf('}', bodyStart)
  return css.slice(bodyStart, bodyEnd)
}

describe('EnterpriseArchiveImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getEnterpriseArchiveSupportMatrix.mockResolvedValue({
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
          status: 'executed',
        },
      ],
      notes: ['Streaming settings execute in the HTTP runtime for media-like responses.'],
      tested_examples: [
        {
          title: 'Extended Web HTTP/HTML runtime import',
          coverage: 'Parses transport, pacing, logging, browser emulation, and DFE settings into APEX Load config.',
          test: 'pkg/convert/enterprisearchive/converter_test.go::TestParseRuntimeCfgToScriptConfigMapsExtendedRuntimeSettings',
        },
      ],
      protocol_support_matrix: [
        {
          id: 'web_http_html',
          protocol: 'web_http_html',
          display_name: 'Web HTTP/HTML',
          status: 'supported',
        },
        {
          id: 'sap_gui',
          protocol: 'sap_gui',
          display_name: 'SAP GUI',
          status: 'external_runner',
          runner_requirement: 'External SAP GUI runner bridge',
        },
      ],
    })
    apiMock.importEnterpriseArchive.mockResolvedValue({
      script: {
        name: 'Demo Enterprise Script Archive',
        protocol: 'http',
        actions: [{ name: 'Homepage' }, { name: 'Submit Order' }, { name: 'Confirmation' }],
        metadata: {
          import: {
            source: 'enterprise_script_archive',
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
              source_review: sourceReviewFixture,
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
          title: 'Unsupported Source Elements',
          severity: 'blocker',
          summary: 'These source constructs were preserved for review but were not converted into executable APEX Load actions.',
          items: ['web_set_max_html_param_len'],
        },
        {
          key: 'preserved_metadata',
          title: 'Preserved Source Metadata',
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
          description: 'The source archive included source runtime metadata that may not all execute natively in APEX Load yet.',
          action: 'Compare the preserved runtime file and project settings against the generated APEX Load config before promoting the script.',
        },
      ],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions', 'General'],
        runtime_files: ['default.cfg'],
        source_review: sourceReviewFixture,
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
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue({
      script: {
        name: 'Demo Enterprise Script Archive',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
        metadata: {
          import: {
            source: 'enterprise_script_archive',
            preserved: {
              source_review: sourceReviewFixture,
            },
          },
        },
      },
      warnings: ['validation review'],
      unsupported_elements: ['web_set_max_html_param_len'],
      assumptions: [],
      review_sections: [
        {
          key: 'unsupported_elements',
          title: 'Unsupported Source Elements',
          severity: 'blocker',
          summary: 'These source constructs were preserved for review but were not converted into executable APEX Load actions.',
          items: ['web_set_max_html_param_len'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        mapped_runtime_file: 'default.cfg',
        project_sections: ['Actions', 'General'],
        runtime_files: ['default.cfg'],
        source_review: sourceReviewFixture,
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
    apiMock.createScript.mockImplementation(async (script: { name?: string; protocol?: string; version?: string }) => ({
      id: 'saved-script',
      name: script.name || 'Saved Script',
      protocol: script.protocol || 'http',
      version: script.version || '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
      script,
    }))
    apiMock.createAuthoringSession.mockResolvedValue({
      id: 'review-session',
      name: 'SAP GUI review artifact',
      source: 'source_review_artifact',
      status: 'reviewing',
      created_at: '2026-05-17T00:00:00Z',
      updated_at: '2026-05-17T00:00:00Z',
      last_accessed_at: '2026-05-17T00:00:00Z',
    })
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

  it('renders the import close action as a Back button', async () => {
    const onClose = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onClose={onClose} />)
    })

    const closeButtons = Array.from(container.querySelectorAll('button'))
      .filter(button => button.textContent?.trim() === 'Back')
    expect(closeButtons).toHaveLength(1)

    await act(async () => {
      closeButtons[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('layers import modal dialogs above the sticky app chrome', async () => {
    // @ts-expect-error Vitest runs in Node, but the dashboard app tsconfig intentionally omits Node types.
    const { readFileSync } = await import('node:fs')
    const cwd = (globalThis as typeof globalThis & { process: { cwd: () => string } }).process.cwd()
    const importCss = readFileSync(`${cwd}/src/components/EnterpriseArchiveImport.css`, 'utf8') as string
    const backdropRule = cssRule(importCss, '.lr-modal-backdrop')

    expect(backdropRule).toContain('z-index: 220;')
  })

  it('renders enterprise protocol import coverage with JMeter-style cards and full details', async () => {
    apiMock.getEnterpriseArchiveSupportMatrix.mockResolvedValueOnce({
      support_matrix: [],
      notes: [],
      tested_examples: [],
      protocol_support_matrix: [
        { id: 'web_http_html', protocol: 'web_http_html', display_name: 'Web HTTP/HTML', status: 'supported' },
        { id: 'database', protocol: 'database', display_name: 'SQL / ODBC-style Database', status: 'supported' },
        { id: 'dns', protocol: 'dns', display_name: 'DNS', status: 'supported' },
        { id: 'web_services', protocol: 'web_services', display_name: 'Web Services', status: 'supported' },
        { id: 'mapi', protocol: 'mapi', display_name: 'MAPI', status: 'review_only', notes: 'Preserved for review.' },
        { id: 'sap_gui', protocol: 'sap_gui', display_name: 'SAP GUI', status: 'external_runner', runner_requirement: 'External SAP GUI runner bridge' },
      ],
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })

    await flushPromises()

    const coverage = container.querySelector('[aria-label="enterprise protocol import coverage"]') as HTMLElement | null
    expect(coverage).not.toBeNull()
    expect(coverage?.querySelector('.lr-preserved-header h4')?.textContent).toBe('Import Coverage')
    expect(coverage?.querySelector('.lr-preserved-grid')).not.toBeNull()

    const executableCard = coverage?.querySelector('.lr-preserved-card.coverage-executable') as HTMLElement | null
    expect(executableCard).not.toBeNull()
    expect(executableCard?.textContent).toContain('4 protocols')
    expect(executableCard?.textContent).toContain('Web HTTP/HTML')
    expect(executableCard?.textContent).toContain('SQL / ODBC-style Database')
    expect(executableCard?.textContent).toContain('DNS')
    expect(executableCard?.textContent).toContain('Web Services')

    const executableDetails = coverage?.querySelector('details[data-coverage="executable"]') as HTMLDetailsElement | null
    expect(executableDetails).not.toBeNull()
    expect(executableDetails?.textContent).toContain('Web HTTP/HTML')
    expect(executableDetails?.textContent).toContain('SQL / ODBC-style Database')
    expect(executableDetails?.textContent).toContain('DNS')
    expect(executableDetails?.textContent).toContain('Web Services')

    const externalRunnerDetails = coverage?.querySelector('details[data-coverage="external_runner_required"]') as HTMLDetailsElement | null
    expect(externalRunnerDetails?.textContent).toContain('SAP GUI')
    expect(externalRunnerDetails?.textContent).toContain('Requires External SAP GUI runner bridge.')
  })

  it('imports a zip and saves the converted script', async () => {
    const onImported = vi.fn()
    apiMock.createScript.mockResolvedValueOnce({
      id: 'saved-script',
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
      version: '1.0.0',
      created_at: '2026-05-17T00:00:00Z',
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport onImported={onImported} />)
    })

    await flushPromises()

    expect(container.textContent).toContain('Protocol-aware detection uses .usr metadata plus source signatures')
    expect(container.textContent).toContain('Runtime Settings Coverage')
    expect(container.textContent).not.toContain('Protocol Import Coverage')
    expect(container.querySelector('.lr-conversion-row')).not.toBeNull()
    expect(container.querySelector('.lr-import-actions-card')).not.toBeNull()
    expect(container.querySelector('[aria-label="enterprise protocol import coverage"]')).not.toBeNull()
    expect(container.textContent).toContain('Executable')
    expect(container.textContent).toContain('External Runner')
    expect(container.textContent).toContain('Partial')
    expect(container.textContent).toContain('Compatibility Only')
    expect(container.querySelector('.lr-preserved-card.coverage-executable')).not.toBeNull()
    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(container.textContent).not.toContain('HTTP: Timeouts, redirects, and connection reuse')
    expect(container.textContent).not.toContain('Tested Examples')
    expect(container.textContent).not.toContain('Known Caveats')
    expect(container.textContent).not.toContain('Streaming settings execute in the HTTP runtime for media-like responses')

    const coverageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Runtime Settings Coverage'))
    expect(coverageButton).toBeDefined()
    if (!coverageButton) {
      return
    }

    await act(async () => {
      coverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[role="dialog"]')).not.toBeNull()
    expect(container.textContent).toContain('Coverage Details')
    expect(container.textContent).toContain('Executed')
    expect(container.textContent).toContain('HTTP: Timeouts, redirects, and connection reuse')
    expect(container.textContent).toContain('Tested Examples')
    expect(container.textContent).toContain('Known Caveats')
    expect(container.textContent).toContain('Streaming settings execute in the HTTP runtime for media-like responses')

    const closeCoverageButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Close'))
    expect(closeCoverageButton).toBeDefined()
    if (!closeCoverageButton) {
      return
    }

    await act(async () => {
      closeCoverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(container.textContent).not.toContain('HTTP: Timeouts, redirects, and connection reuse')

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

    const convertButton = findButton(container, 'Convert Enterprise Script Archive')
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }
    expect(convertButton.disabled).toBe(true)

    await clickImportAction(container, 'Validate Only')
    await flushPromises()

    expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: true,
      migration_block_statuses: ['blocked', 'partial', 'compatibility_only'],
    })
    expect(convertButton.disabled).toBe(false)
    expect(container.textContent).toContain('Validation Review')

    await clickButton(convertButton)

    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: true,
      migration_block_statuses: ['blocked', 'partial', 'compatibility_only'],
    })
    expect(container.textContent).toContain('Conversion Complete')
    expect(container.textContent).toContain('Demo Enterprise Script Archive')
    expect(container.textContent).toContain('manual review recommended')
    expect(container.textContent).toContain('Unsupported Source Functions (1)')
    expect(container.textContent).toContain('Import Review Sections (2)')
    expect(container.textContent).toContain('Repair Suggestions (2)')
    expect(container.querySelectorAll('.lr-review-row .lr-warnings')).toHaveLength(2)
    expect(container.textContent).toContain('Replace unsupported web_set_max_html_param_len')
    expect(container.textContent).toContain('Review preserved runtime settings')
    expect(container.textContent).toContain('Unsupported Source Elements')
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
    expect(container.querySelector('.lr-parameter-table')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('.lr-parameter-table th')).map(header => header.textContent)).toEqual([
      'File',
      'Section',
      'Mapped Data Source',
      'Mapping',
      'Action',
    ])
    expect(container.textContent).toContain('LoginUsers.prm')
    expect(container.textContent).toContain('LoginUser')
    expect(container.textContent).toContain('column username')
    expect(container.textContent).toContain('table Users.dat')
    expect(container.textContent).toContain('Generated Data Sources')
    expect(container.querySelector('.lr-data-source-table')).not.toBeNull()
    expect(Array.from(container.querySelectorAll('.lr-data-source-table th')).map(header => header.textContent)).toEqual([
      'Source',
      'Type',
      'Rows',
      'Columns',
      'Action',
    ])
    expect(container.textContent).toContain('users')
    expect(container.textContent).toContain('csv')
    expect(container.textContent).toContain('username')
    expect(container.textContent).toContain('Source Mapping')
    expect(container.textContent).toContain('Validate the conversion line by line')
    expect(container.textContent).toContain('Action.c:3-5')
    expect(container.textContent).toContain('web_url')
    expect(container.textContent).toContain('"name": "Homepage"')
    const review = container.querySelector('.script-import-review')
    const reviewChildren = Array.from(review?.children || [])
    const preview = reviewChildren.find(element => element.classList.contains('script-import-review-preview')) as HTMLDetailsElement | undefined
    const sourceComparison = reviewChildren.find(element => element.classList.contains('script-import-review-preview-aside'))
    expect(preview?.tagName).toBe('DETAILS')
    expect(preview?.open).toBe(true)
    expect(preview?.querySelector('summary h4')?.textContent).toBe('Generated Script Preview')
    expect(sourceComparison?.querySelector('.source-map-review')).not.toBeNull()
    expect(preview && sourceComparison ? reviewChildren.indexOf(preview) < reviewChildren.indexOf(sourceComparison) : false).toBe(true)
    expect(review?.querySelector('.script-import-review-preview-row')).toBeNull()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({
        name: 'Demo Enterprise Script Archive',
        protocol: 'http',
        script: expect.objectContaining({
          name: 'Demo Enterprise Script Archive',
          metadata: expect.objectContaining({
            import: expect.objectContaining({
              source: 'enterprise_script_archive',
            }),
          }),
        }),
      })],
    }))
  })

  it('surfaces TrueClient2 compatibility report from Enterprise Script Archive import responses', async () => {
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      script: {
        name: 'Checkout Web2',
        protocol: 'browser',
        actions: [
          {
            name: 'Custom JS',
            type: 'browser_evaluate_js',
            target: 'window.checkoutReady === true',
            metadata: {
              review_required: true,
              review_gate: 'javascript',
              promotion_blocker: true,
            },
          },
        ],
        metadata: {
          import: {
            source: 'enterprise_script_archive',
            source_script_name: 'CheckoutWeb2.zip',
            preserved: {},
          },
        },
      },
      warnings: ['TruClient 2.0 - Web import is review-only'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      trueclient2_compat_report: {
        source_script_type: 'TruClient 2.0 - Web',
        source_profile: 'truclient2_web',
        import_mode: 'review_only',
        promotion_status: 'blocked',
        promotion_allowed: false,
        imported_steps: 3,
        converted_actions: 2,
        skipped_review_steps: 1,
        supported_step_families: ['navigate', 'click'],
        review_only_step_families: ['evaluate_javascript'],
        unsupported_step_families: ['if_loop_mapping'],
        selector_confidence_pct: 86,
        selector_candidate_count: 5,
        runtime_parity_status: 'review_required',
        replay_status: 'required',
        javascript_review_gate: 'required',
        frame_window_blockers: 1,
        http_conversion_readiness: 'draft_required',
        fixture_contract_status: 'blocked',
        findings: ['Review-gated JavaScript is present.'],
        next_actions: ['Run draft browser replay.', 'Approve JavaScript before promotion.'],
      },
      summary: {
        project_file: 'CheckoutWeb2.usr',
        project_sections: ['Actions'],
        runtime_files: ['default.cfg'],
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 3,
        converted_actions: 2,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 1,
      },
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['zip-content'], 'web2.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('TruClient 2 Compatibility')
    expect(container.textContent).toContain('Blocked')
    expect(container.textContent).toContain('TruClient 2.0 - Web')
    expect(container.textContent).toContain('review_only')
    expect(container.textContent).toContain('2/3')
    expect(container.textContent).toContain('86%')
    expect(container.textContent).toContain('5 candidates')
    expect(container.textContent).toContain('1 frame/window blockers')
    expect(container.textContent).toContain('Review-only steps: evaluate_javascript')
    expect(container.textContent).toContain('Unsupported steps: if_loop_mapping')
    expect(container.textContent).toContain('Review-gated JavaScript is present.')
    expect(container.textContent).toContain('Approve JavaScript before promotion.')
  })

  it('copies selected source mapping snippets', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      value: undefined,
    })
    Object.defineProperty(window.navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) return

    const file = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    const copySource = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy Source'))
    const copyDsl = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy DSL'))
    expect(copySource).toBeDefined()
    expect(copyDsl).toBeDefined()
    if (!copySource || !copyDsl) return
    expect((copySource as HTMLButtonElement).disabled).toBe(false)
    expect((copyDsl as HTMLButtonElement).disabled).toBe(false)

    await act(async () => {
      copySource.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('web_url'))
    expect(container.textContent).toContain('Copied')

    await act(async () => {
      copyDsl.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining('"name": "Homepage"'))
  })

  it('queues multiple enterprise script archives, removes one, and converts the remaining files', async () => {
    const firstFile = new File(['alpha'], 'alpha.zip', { type: 'application/zip' })
    const removedFile = new File(['beta'], 'beta.zip', { type: 'application/zip' })
    const lastFile = new File(['gamma'], 'gamma.zip', { type: 'application/zip' })
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => ({
      script: {
        name: file.name.replace(/\.zip$/i, ''),
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: file.name,
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
    }))

    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }
    expect(fileInput.multiple).toBe(true)

    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [firstFile, removedFile, lastFile],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('3 ZIP files queued')
    expect(container.textContent).toContain('alpha.zip')
    expect(container.textContent).toContain('beta.zip')
    expect(container.textContent).toContain('gamma.zip')

    const removeBeta = container.querySelector('[aria-label="Remove beta.zip"]') as HTMLButtonElement | null
    expect(removeBeta).not.toBeNull()
    if (!removeBeta) {
      return
    }

    await act(async () => {
      removeBeta.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('2 ZIP files queued')
    expect(container.textContent).not.toContain('beta.zip')

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert 2 Enterprise Script Archives'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)
    expect(apiMock.importEnterpriseArchive.mock.calls[0][0]).toBe(firstFile)
    expect(apiMock.importEnterpriseArchive.mock.calls[1][0]).toBe(lastFile)
    expect(apiMock.importEnterpriseArchive.mock.calls.map(call => (call[0] as File).name)).toEqual(['alpha.zip', 'gamma.zip'])
    expect(container.textContent).toContain('Converted and saved 2 scripts to the library')
    expect(container.textContent).toContain('alpha')
  })

  it('converts every validated archive from the per-item review panel, not just the last-added one', async () => {
    const onImported = vi.fn()
    const firstFile = new File(['alpha'], 'alpha.zip', { type: 'application/zip' })
    const lastFile = new File(['gamma'], 'gamma.zip', { type: 'application/zip' })
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => ({
      script: {
        name: file.name.replace(/\.zip$/i, ''),
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: { project_file: file.name, unsupported_function_count: 0 },
      stats: { total_functions: 1, converted_actions: 1, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
    }))

    await act(async () => {
      root.render(<EnterpriseArchiveImport onImported={onImported} />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    // Add the archives one at a time so the queue selection lands on the LAST-added item,
    // reproducing the original "only the last script added is imported" report.
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [firstFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [lastFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('2 ZIP files queued')

    await clickImportAction(container, 'Validate Only')
    await flushPromises()

    // The per-item review panel renders the last-added (gamma) archive, but its save
    // button now converts every validated archive in the queue.
    const reviewSaveButton = container.querySelector('.script-import-review-actions .btn-primary') as HTMLButtonElement | null
    expect(reviewSaveButton).not.toBeNull()
    if (!reviewSaveButton) {
      return
    }
    expect(reviewSaveButton.textContent).toContain('Convert 2 Enterprise Script Archives')

    await clickButton(reviewSaveButton)
    await flushPromises()

    // Both archives are converted and saved — not just the last-added (gamma) one.
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)
    expect(apiMock.importEnterpriseArchive.mock.calls.map(call => (call[0] as File).name)).toEqual(['alpha.zip', 'gamma.zip'])
    expect(apiMock.createScript).toHaveBeenCalledTimes(2)
    expect(onImported).toHaveBeenCalledTimes(1)
    expect(onImported.mock.calls[0][0].scripts).toHaveLength(2)
  })

  it('announces import progress and failed queue item details', async () => {
    const firstFile = new File(['alpha'], 'alpha.zip', { type: 'application/zip' })
    const failedFile = new File(['gamma'], 'gamma.zip', { type: 'application/zip' })
    let resolveFirstImport: ((value: unknown) => void) | null = null
    let rejectSecondImport: ((error: unknown) => void) | null = null

    apiMock.importEnterpriseArchive
      .mockImplementationOnce(() => new Promise(resolve => {
        resolveFirstImport = resolve
      }))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => {
        rejectSecondImport = reject
      }))

    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }

    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [firstFile, failedFile],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert 2 Enterprise Script Archives'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('[role="status"]')?.textContent).toContain('Converting and saving 2 enterprise script archives')
    const convertingRows = Array.from(container.querySelectorAll('.lr-file-row.converting'))
    expect(convertingRows).toHaveLength(2)

    await act(async () => {
      resolveFirstImport?.({
        script: {
          name: 'alpha',
          protocol: 'http',
          actions: [{ name: 'Homepage' }],
        },
        warnings: [],
        unsupported_elements: [],
        assumptions: [],
        review_sections: [],
        repair_suggestions: [],
        summary: {
          project_file: 'alpha.zip',
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
    })
    await flushPromises()

    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)

    await act(async () => {
      rejectSecondImport?.(new Error('Unsupported archive layout'))
    })
    await flushPromises()

    expect(container.querySelector('[role="status"]')).toBeNull()
    expect(container.textContent).toContain('Converted and saved 1 script to the library; 1 archive failed')
    expect(container.textContent).toContain('1 enterprise script archive failed during import review')
    expect(container.textContent).toContain('Unsupported archive layout')

    const failedRow = Array.from(container.querySelectorAll('.lr-file-row')).find(row => row.textContent?.includes('gamma.zip'))
    expect(failedRow?.className).toContain('failed')
  })

  it('shows gated draft review and saves a review artifact for external-runner imports', async () => {
    const sapSourceReview = {
      source_files: [
        {
          path: 'Action.c',
          language: 'c',
          content: 'Action()\n{\n  sapgui_set_text("wnd[0]/usr/txtRSYST-BNAME", "APEXUSER", LAST);\n}',
        },
      ],
      mappings: [
        {
          id: 'sapgui-set-user',
          source_file: 'Action.c',
          start_line: 3,
          end_line: 3,
          function: 'sapgui_set_text',
          target_path: 'actions[1]',
          target_name: 'Set User',
          target_kind: 'action',
          status: 'review_only',
          support_mode: 'external_required',
          notes: 'Requires the SAP GUI external runner bridge.',
        },
      ],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      scripts: [
        {
          name: 'SAP GUI Draft',
          version: '1.0.0',
          protocol: 'sap_gui',
          config: {
            base_url: '',
            sapgui: {
              connection_string: 'ASHOST=sap.example.test SYSNR=00',
              client: '100',
              username: 'APEXUSER',
              password: '{{secret.sapgui_password}}',
              session_behavior: 'reuse',
            },
          },
          actions: [
            {
              name: 'Connect',
              type: 'sapgui_connect',
              target: 'ASHOST=sap.example.test SYSNR=00',
            },
            {
              name: 'Set User',
              type: 'sapgui_set',
              target: 'wnd[0]/usr/txtRSYST-BNAME',
              value: 'APEXUSER',
            },
          ],
          metadata: {
            import: {
              source: 'enterprise_script_archive',
              source_script_name: 'SAPGUI.usr',
              preserved: {
                source_review: sapSourceReview,
              },
            },
          },
        },
      ],
      script: {
        name: 'SAP GUI Draft',
        version: '1.0.0',
        protocol: 'sap_gui',
        config: {
          base_url: '',
          sapgui: {
            connection_string: 'ASHOST=sap.example.test SYSNR=00',
            client: '100',
            username: 'APEXUSER',
            password: '{{secret.sapgui_password}}',
            session_behavior: 'reuse',
          },
        },
        actions: [
          {
            name: 'Connect',
            type: 'sapgui_connect',
            target: 'ASHOST=sap.example.test SYSNR=00',
          },
          {
            name: 'Set User',
            type: 'sapgui_set',
            target: 'wnd[0]/usr/txtRSYST-BNAME',
            value: 'APEXUSER',
          },
        ],
        metadata: {
          import: {
            source: 'enterprise_script_archive',
            source_script_name: 'SAPGUI.usr',
            preserved: {
              source_review: sapSourceReview,
            },
          },
        },
      },
      detected_protocol: {
        canonical_source_protocol: 'sap_gui',
        display_name: 'SAP GUI',
        import_mode: 'external_runner_required',
        confidence: 'high',
        bundle: 'Foundation Entitlement',
        runner_requirement: 'windows_sap_gui_runner',
        required_evidence: ['External runner bridge approval'],
        signals: ['.usr [General] Type=SAP GUI'],
      },
      review_artifact: {
        id: 'lr-review-sap',
        title: 'SAP GUI review artifact',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'sap_gui',
          display_name: 'SAP GUI',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'windows_sap_gui_runner',
        },
        summary: 'SAP GUI requires an external SAP GUI runner bridge.',
        required_evidence: ['External runner bridge approval'],
        next_actions: ['Attach external runner approval.'],
      },
      warnings: ['Import was classified as external_runner_required; generated draft requires an approved external runner.'],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [
        {
          key: 'review_artifact',
          title: 'Protocol Review Artifact',
          severity: 'warning',
          summary: 'SAP GUI requires an external SAP GUI runner bridge.',
          items: ['Required evidence: External runner bridge approval'],
        },
      ],
      repair_suggestions: [],
      summary: {
        import_mode: 'external_runner_required',
        source_review: sapSourceReview,
        unsupported_function_count: 0,
      },
      stats: {
        total_functions: 1,
        converted_actions: 2,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
    })
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(fileInput).not.toBeNull()
    if (!fileInput) {
      return
    }
    const file = new File(['zip-content'], 'sap.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Conversion Complete')
    expect(container.textContent).toContain('SAP GUI')
    expect(container.textContent).toContain('External Runner Required')
    expect(container.textContent).toContain('external_runner_required')
    expect(container.textContent).toContain('Runner: windows_sap_gui_runner')
    expect(container.textContent).toContain('SAP GUI review artifact')
    expect(container.textContent).toContain('sapgui_set')
    expect(container.textContent).toContain('external required')
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save to Script Library'))).toBeUndefined()

    const saveArtifactButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Review Artifact'))
    expect(saveArtifactButton).toBeDefined()
    if (!saveArtifactButton) {
      return
    }
    await act(async () => {
      saveArtifactButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.createAuthoringSession).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      source: 'source_review_artifact',
      review_state: expect.objectContaining({
        review_artifact: expect.objectContaining({ id: 'lr-review-sap' }),
      }),
      draft: expect.objectContaining({
        protocol: 'sap_gui',
        actions: expect.arrayContaining([
          expect.objectContaining({ type: 'sapgui_set' }),
        ]),
      }),
    }))
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })


  it('runs validate-only review with preserve transaction option', async () => {
    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
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
    expect(checkboxes).toHaveLength(6)
    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      checkboxes[3].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Script(s)'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenCalledWith(file, {
      name: undefined,
      project_id: undefined,
      base_url: undefined,
      include_assertions: true,
      preserve_transactions: false,
      migration_block_statuses: ['blocked', 'compatibility_only'],
    })
    expect(container.textContent).toContain('Validation Review')
    expect(container.textContent).toContain('validation review')
    expect(container.textContent).toContain('Unsupported Source Functions (1)')
  })

  it('passes Salesforce HAR evidence and opens the native preview draft', async () => {
    const onEdit = vi.fn()
    const nativeScript = {
      name: 'Demo Salesforce Native Preview',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://acme.my.salesforce.com' },
      actions: [
        {
          name: 'Salesforce Login',
          type: 'request',
          method: 'POST',
          target: '/login',
        },
        {
          name: 'Open Account',
          type: 'request',
          method: 'POST',
          target: '/aura',
        },
      ],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      script: {
        name: 'Demo Enterprise Script Archive',
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: 'https://acme.my.salesforce.com' },
        actions: [
          {
            name: 'Homepage',
            type: 'request',
            method: 'GET',
            target: '/',
          },
          {
            name: 'Aura Action',
            type: 'request',
            method: 'POST',
            target: '/aura',
          },
        ],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [
        {
          key: 'salesforce_review',
          title: 'Salesforce Review',
          severity: 'info',
          summary: 'Detected Salesforce domains with HAR coverage.',
          items: ['Domain: acme.my.salesforce.com'],
        },
      ],
      repair_suggestions: [],
      summary: {
        project_file: 'Demo.usr',
        unsupported_function_count: 0,
        salesforce_review: {
          detected: true,
          domains: [{ host: 'acme.my.salesforce.com', kind: 'my_salesforce', count: 2 }],
          aura_endpoints: [{ method: 'POST', path: '/aura', host: 'acme.my.salesforce.com', count: 1 }],
          transactions: [{ name: 'Open Account', source: 'source_transaction', request_count: 2, aura_actions: 1 }],
          correlation_confidence: 'medium',
          har_match_coverage: {
            har_provided: true,
            imported_requests: 2,
            matched_requests: 2,
            coverage_pct: 100,
          },
          native_preview: {
            script: nativeScript,
            correlations: [
              {
                name: 'sid',
                source: 'har_response',
                expression: '$.sid',
                found_in_seq: 1,
                used_in_seqs: [2],
                confidence: 0.87,
              },
            ],
          },
        },
      },
      stats: {
        total_functions: 1539,
        converted_actions: 88,
        logical_actions: 88,
        raw_request_actions: 537,
        collapsed_requests: 449,
        transaction_markers: 946,
        extractors: 1,
        think_times: 0,
        transactions: 1,
        skipped: 0,
      },
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
    })

    const fileInputs = Array.from(container.querySelectorAll('input[type="file"]')) as HTMLInputElement[]
    const zipInput = fileInputs[0]
    const harInput = fileInputs[1]
    expect(zipInput).toBeDefined()
    expect(harInput).toBeDefined()
    if (!zipInput || !harInput) {
      return
    }

    const zipFile = new File(['zip-content'], 'demo.zip', { type: 'application/zip' })
    const harFile = new File(['{"log":{"entries":[]}}'], 'demo.har', { type: 'application/json' })
    await act(async () => {
      Object.defineProperty(zipInput, 'files', {
        configurable: true,
        value: [zipFile],
      })
      zipInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      Object.defineProperty(harInput, 'files', {
        configurable: true,
        value: [harFile],
      })
      harInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    expect(container.textContent).toContain('HAR: demo.har')
    expect(container.textContent).toContain('demo.har')

    const checkboxes = Array.from(container.querySelectorAll('input[type="checkbox"]')) as HTMLInputElement[]
    await act(async () => {
      checkboxes[2]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Script(s)'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenCalledWith(zipFile, expect.objectContaining({
      har_file: harFile,
      salesforce_native: true,
    }))
    expect(container.textContent).toContain('Exact Replay')
    expect(container.textContent).toContain('Salesforce Native Preview')
    expect(container.textContent).toContain('2/2 matched (100.0%)')
    expect(container.textContent).toContain('Draft Ready')
    expect(container.textContent).toContain('Salesforce Review')
    expect(container.textContent).toContain('Logical Actions')
    expect(container.textContent).toContain('Raw Requests')
    expect(container.textContent).toContain('Collapsed')
    expect(container.textContent).toContain('LR Markers')

    const openNativePreview = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Open Native Preview'))
    expect(openNativePreview).toBeDefined()
    if (!openNativePreview) {
      return
    }
    await act(async () => {
      openNativePreview.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).toHaveBeenCalledWith(nativeScript)
  })

  it('disables saving while migration readiness is blocked by the active policy', async () => {
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
      root.render(<EnterpriseArchiveImport />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens the imported script in the visual editor without an additional focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), undefined)
  })

  it('opens an unsupported function repair directly in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens imported runtime review in the visual editor with a runtime focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'runtime' })
  })

  it('opens a runtime repair suggestion in the visual editor with a runtime focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'runtime' })
  })

  it('opens a specific runtime area in the visual editor with a runtime area focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'runtime', targetRuntimeArea: 'Logging' })
  })

  it('opens a correlation source action in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens a correlation usage action in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetAction: 'Submit Order' })
  })

  it('opens the first correlation source summary jump in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetAction: 'Homepage' })
  })

  it('opens the first correlation usage summary jump in the visual editor with an action focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetAction: 'Submit Order' })
  })

  it('opens imported parameter data review in the visual editor with a parameter-data focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('opens parameter files from the summary card in the visual editor with a parameter-data focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('opens the first generated data source summary jump in the visual editor with a data source focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('opens a generated source in the visual editor with a data source focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('opens the first runtime setting summary jump in the visual editor with a runtime area focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'runtime', targetRuntimeArea: 'Run Logic' })
  })

  it('opens the first blocking runtime setting summary jump in the visual editor with a runtime area focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'runtime', targetRuntimeArea: 'Logging' })
  })

  it('relaxes a blocked runtime migration policy and reruns the current review', async () => {
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      script: {
        name: 'Blocked Runtime Script',
        protocol: 'http',
        actions: [{ name: 'Homepage' }],
        metadata: {
          import: {
            source: 'enterprise_script_archive',
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
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
      root.render(<EnterpriseArchiveImport />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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

    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)
    expect(apiMock.importEnterpriseArchive.mock.calls[1]).toEqual([
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
    expect(checkboxes[3]?.checked).toBe(false)
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

    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(3)
    expect(apiMock.importEnterpriseArchive.mock.calls[2]).toEqual([
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
    expect(checkboxes[3]?.checked).toBe(true)
    expect(container.textContent).toContain('Selected migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Conversion refreshed with migration block policy: Blocked, Partial, Compatibility Only.')
  })

  it('shows pending migration policy changes until the review is refreshed', async () => {
    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      checkboxes[3]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Selected migration block policy: Blocked, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Partial, Compatibility Only')
    expect(container.textContent).toContain('Refresh review to apply the selected migration block policy.')
  })

  it('opens a mapped parameter source in the visual editor with a mapping focus hint', async () => {
    const onEdit = vi.fn()

    await act(async () => {
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), { targetSection: 'parameter-data', targetDataSource: 'users' })
  })

  it('starts draft browser replay validation for converted browser imports before save', async () => {
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Draft Browser Replay Validation')
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
    expect(container.textContent).toContain('Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving it to the library.')
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
    expect(container.textContent).toContain('Draft browser replay started. Review the draft browser replay session before saving it to the library.')
    expect(container.textContent).toContain('Replay Session: draft-browser-validation')
    expect(container.textContent).toContain('Replay Status: Running')
    expect(container.textContent).toContain('Replay Group: default')
    expect(container.textContent).toContain('selector repair recommended')
    expect(container.textContent).toContain('Wait for the draft browser replay to finish before saving it to the library.')
    const inProgressReplayButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Draft Browser Replay In Progress')) as HTMLButtonElement | undefined
    expect(inProgressReplayButton).toBeDefined()
    expect(inProgressReplayButton?.disabled).toBe(true)
  })

  it('starts draft browser replay validation when saving a browser import for the first time', async () => {
    vi.useFakeTimers()

    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
          {
            name: 'Open Store',
            type: 'browser_navigate',
            url: '/store',
            metadata: {
              source_selector_fallbacks: [
                { kind: 'css', value: '#store-link', confidence: 81 },
                { kind: 'xpath', value: '//a[@href="/store"]', confidence: 68 },
              ],
            },
          },
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
        events: [
          {
            event_id: 'event-1',
            action_index: 0,
            event_index: 0,
            phase: 'actions',
            action_name: 'Open Store',
            action_type: 'browser_navigate',
            target: '/store',
            success: true,
            started_at: '2026-01-01T00:00:00Z',
            completed_at: '2026-01-01T00:00:01Z',
            duration_ms: 1000,
            source_step_id: 'step-open-store',
            source_transaction: 'Checkout',
            screenshot_base64: 'iVBORw0KGgo=',
            dom_summary: {
              title: 'Store',
              ready_state: 'complete',
              visible_text: ['Store'],
            },
            network_entries: [
              { method: 'GET', url: 'https://shop.example.test/store', status_code: 200 },
            ],
          },
          {
            event_id: 'event-2',
            action_index: 1,
            event_index: 1,
            phase: 'actions',
            action_name: 'Add To Cart',
            action_type: 'browser_click',
            target: '#add-to-cart',
            selector: '#add-to-cart',
            success: true,
            started_at: '2026-01-01T00:00:01Z',
            completed_at: '2026-01-01T00:00:02Z',
            duration_ms: 1000,
            source_step_id: 'step-add-to-cart',
            source_transaction: 'Checkout',
            html: '<button id="add-to-cart">Add</button>',
            console_entries: [
              { level: 'info', text: 'ready' },
            ],
            network_entries: [
              { method: 'POST', url: 'https://shop.example.test/cart', status_code: 200 },
            ],
          },
        ],
      },
      warnings: ['selector repair recommended'],
    })

    try {
      await act(async () => {
        root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

      await clickImportAction(container, 'Validate Only')

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      expect(container.textContent).toContain('Review the generated script, preserved metadata, and warnings. Wait for the draft browser replay to finish before saving it to the library.')
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
      expect(container.textContent).toContain('Draft browser replay started. Review the draft browser replay session before saving it to the library.')
      expect(container.textContent).toContain('Replay Session: draft-browser-validation')
      expect(container.textContent).toContain('Replay Status: Running')
      expect(container.textContent).toContain('Replay Group: default')
      expect(container.textContent).toContain('Draft Browser Replay In Progress')
      expect(container.textContent).toContain('Wait for the draft browser replay to finish before saving it to the library.')

      await act(async () => {
        vi.advanceTimersByTime(2000)
      })

      await flushPromises()

      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledWith('draft-browser-validation')
      expect(container.textContent).toContain('Replay Status: Completed')
      expect(container.textContent).toContain('The latest draft browser replay completed. Review the draft browser replay results before saving it to the library.')
      expect(container.textContent).toContain('Replay Evidence')
      expect(container.textContent).toContain('2 actions')
      expect(container.textContent).toContain('2 passed / 0 failed')
      expect(container.textContent).toContain('1 screenshot')
      expect(container.textContent).toContain('2 DOM/HTML captures')
      expect(container.textContent).toContain('1 console entry')
      expect(container.textContent).toContain('2 network entries')
      expect(container.textContent).toContain('2 source steps')
      expect(container.textContent).toContain('1 transaction')

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
      const savedScript = apiMock.createScript.mock.calls[0][0]
      const promotionReview = savedScript.metadata?.import?.preserved?.trueclient2_promotion_review
      expect(promotionReview).toMatchObject({
        schema_version: 1,
        status: 'replay_evidence_attached',
        source: 'trueclient_mobile_web',
        source_script_name: 'default.xml',
        gates: {
          review_required_actions: 0,
          javascript_review_actions: 0,
          selector_fallback_candidates: 2,
          replay_required: false,
          replay_warnings: ['selector repair recommended'],
        },
        replay_evidence: {
          session_id: 'draft-browser-validation',
          status: 'completed',
          events: 2,
          passed: 2,
          failed: 0,
          screenshots: 1,
          dom_html_captures: 2,
          console_entries: 1,
          network_entries: 2,
          source_steps: 2,
          source_transactions: 1,
        },
      })
      expect(promotionReview?.generated_at).toEqual(expect.any(String))
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a draft-specific replay refresh error when replay status polling fails', async () => {
    vi.useFakeTimers()

    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
        root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

      await clickImportAction(container, 'Validate Only')

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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
      expect(container.textContent).toContain('Replay Status: Running')
      expect(container.textContent).toContain('Draft Browser Replay In Progress')
    } finally {
      vi.useRealTimers()
    }
  })

  it('shows a start-specific replay error when the draft browser replay fails to start', async () => {
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
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

  it('shows the convert action for browser validation reviews', async () => {
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
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
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Script(s)'))
    expect(validateButton).toBeDefined()
    if (!validateButton) {
      return
    }

    await act(async () => {
      validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const convertValidationButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
    expect(convertValidationButton).toBeDefined()
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))).toBeUndefined()
    expect(container.textContent).toContain('Convert the validated archive to save the generated script.')
  })

  it('shows rerun copy after a browser validation replay fails', async () => {
    vi.useFakeTimers()

    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
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
    apiMock.importEnterpriseArchive.mockImplementationOnce(() => apiMock.validateEnterpriseArchiveImport.mock.results[0]?.value)
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
        root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

      const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Script(s)'))
      expect(validateButton).toBeDefined()
      if (!validateButton) {
        return
      }

      await act(async () => {
        validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
      expect(convertButton).toBeDefined()
      if (!convertButton) {
        return
      }

      await act(async () => {
        convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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
      expect(container.textContent).toContain('Replay Status: Failed')
      expect(container.textContent).toContain('Rerun Draft Browser Replay Before Saving')
      expect(container.textContent).toContain('Rerun a draft browser replay before saving it to the library.')
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
      expect(container.textContent).toContain('Draft browser replay restarted. Review the refreshed draft browser replay session before saving it to the library.')
      expect(container.textContent).toContain('Replay Session: draft-browser-validation-rerun')
      expect(container.textContent).toContain('Replay Status: Running')
    } finally {
      vi.useRealTimers()
    }
  })

  it('preserves rerun replay copy when a replay rerun fails to start', async () => {
    vi.useFakeTimers()
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
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
    apiMock.importEnterpriseArchive.mockImplementationOnce(() => apiMock.validateEnterpriseArchiveImport.mock.results[0]?.value)
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
        root.render(<EnterpriseArchiveImport projectId="project-1" />)
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

      const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Script(s)'))
      expect(validateButton).toBeDefined()
      if (!validateButton) {
        return
      }

      await act(async () => {
        validateButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      })

      await flushPromises()

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
      expect(convertButton).toBeDefined()
      if (!convertButton) {
        return
      }

      await act(async () => {
        convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
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
      expect(container.textContent).toContain('Replay Status: Failed')
      expect(container.textContent).toContain('Replay Session: draft-browser-validation')
      expect(container.textContent).toContain('Rerun Draft Browser Replay')
      expect(container.textContent).toContain('Rerun Draft Browser Replay Before Saving')
    } finally {
      vi.useRealTimers()
    }
  })

  it('opens a browser replay repair suggestion in the visual editor without an additional focus hint', async () => {
    const onEdit = vi.fn()

    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
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
      root.render(<EnterpriseArchiveImport onEdit={onEdit} />)
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

    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Convert Enterprise Script Archive'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(container.textContent).toContain('Run draft browser replay before saving it to the library')
    expect(container.textContent).toContain('Open the imported draft in the visual editor and run draft browser replay. Update selectors or waits for any failed action.')

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
    for (let index = 0; index < 8; index += 1) {
      await Promise.resolve()
    }
  })
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes(label)) as HTMLButtonElement | undefined
}

async function clickButton(button: HTMLButtonElement) {
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  await flushPromises()
}

async function clickImportAction(container: HTMLElement, label: string) {
  const button = findButton(container, label) || (label === 'Validate Only' ? findButton(container, 'Validate') : undefined)
  expect(button).toBeDefined()
  if (!button) return null
  await clickButton(button)
  return button
}
