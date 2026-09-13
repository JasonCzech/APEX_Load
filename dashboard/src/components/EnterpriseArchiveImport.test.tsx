// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ApiError } from '../hooks/apiClient'
import EnterpriseArchiveImport from './EnterpriseArchiveImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  getEnterpriseArchiveSupportMatrix: vi.fn(),
  importEnterpriseArchive: vi.fn(),
  validateEnterpriseArchiveImport: vi.fn(),
  createScript: vi.fn(),
  saveEnterpriseArchiveImport: vi.fn(),
  deleteScript: vi.fn(),
  createAuthoringSession: vi.fn(),
  startDraftBrowserReplaySession: vi.fn(),
  getBrowserReplaySession: vi.fn(),
  applySecretBindings: vi.fn(),
  applyImportParameterization: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
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

function executableEnterpriseResult(script: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  return {
    scripts: [script],
    script,
    warnings: [],
    unsupported_elements: [],
    assumptions: [],
    review_sections: [],
    repair_suggestions: [],
    summary: {
      project_file: 'Login.usr',
      unsupported_function_count: 0,
      migration_readiness: {
        score_pct: 100,
        blocked: false,
        summary: 'No migration blockers.',
        policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
      },
    },
    stats: {
      total_functions: 1,
      converted_actions: 1,
      extractors: 0,
      think_times: 0,
      transactions: 0,
      skipped: 0,
    },
    ...extra,
  }
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
    apiMock.saveEnterpriseArchiveImport.mockImplementation(async (payload: {
      scripts: Array<{ name?: string; protocol?: string; version?: string; project_id?: string }>
      folder_id?: string | null
    }) => {
      const scripts = await Promise.all(payload.scripts.map(script => (
        apiMock.createScript(script, script.project_id, payload.folder_id ?? undefined)
      )))
      return { scripts, count: scripts.length }
    })
    apiMock.deleteScript.mockResolvedValue({ deleted: 'saved-script' })
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
    apiMock.applyImportParameterization.mockResolvedValue({
      script: {
        name: 'Parameterized Enterprise Script',
        version: '1.0.0',
        protocol: 'http',
        actions: [{ name: 'Get Order', type: 'request', method: 'GET', target: '/orders?customer_id={{var.customer_id}}' }],
      },
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_enterprise_customer' }],
      applied_count: 1,
    })
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: { provider: 'database', writable: true, create_supported: true, replace_supported: true },
      secrets: [],
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('saves validated archives despite a failed ZIP and retains saved receipts when adding more files', async () => {
    const onImported = vi.fn()
    apiMock.validateEnterpriseArchiveImport.mockImplementation(async (file: File) => {
      if (file.name === 'broken.zip') throw new Error('Invalid ZIP archive')
      return executableEnterpriseResult({ name: file.name, protocol: 'http', actions: [{ name: 'Home' }] })
    })
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => executableEnterpriseResult({
      name: file.name, protocol: 'http', actions: [{ name: 'Home' }],
    }))
    apiMock.saveEnterpriseArchiveImport.mockImplementation(async ({ scripts }: { scripts: Array<{ name: string }> }) => ({
      scripts: scripts.map(script => ({ id: script.name, name: script.name, protocol: 'http', version: '1.0', created_at: '2026-09-09', script })),
      count: scripts.length,
    }))
    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" folderId="folder-1" onImported={onImported} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[accept=".zip"]')!
    const add = async (names: string[]) => {
      await act(async () => {
        Object.defineProperty(fileInput, 'files', { configurable: true, value: names.map(name => new File(['zip'], name)) })
        fileInput.dispatchEvent(new Event('change', { bubbles: true }))
      })
    }
    await add(['one.zip', 'two.zip', 'broken.zip'])
    await clickImportAction(container, 'Validate Only')
    const save = findButton(container, 'Save Ready Scripts and Continue Importing')!
    expect(save.disabled).toBe(false)
    await clickButton(save)
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledWith(expect.objectContaining({ folder_id: 'folder-1', scripts: [
      expect.objectContaining({ name: 'one.zip', project_id: 'project-1' }),
      expect.objectContaining({ name: 'two.zip', project_id: 'project-1' }),
    ] }))
    expect(onImported).toHaveBeenLastCalledWith(expect.objectContaining({ stayInImportReview: true }))
    expect(container.textContent).toContain('2 scripts saved')
    expect(container.textContent).toContain('1 archive failed')
    expect(findButton(container, 'Import More Files')).toBeDefined()
    await add(['three.zip'])
    apiMock.validateEnterpriseArchiveImport.mockClear()
    await clickImportAction(container, 'Validate Only')
    expect(apiMock.validateEnterpriseArchiveImport.mock.calls.map(([file]) => file.name)).toEqual(['broken.zip', 'three.zip'])
    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(2)
    expect(apiMock.saveEnterpriseArchiveImport.mock.calls[1][0].scripts).toHaveLength(1)
    expect(container.textContent).toContain('3 scripts saved')
    await clickButton(findButton(container, 'Open one.zip in Visual Editor')!)
    expect(onImported).toHaveBeenLastCalledWith({ scripts: [expect.objectContaining({ id: 'one.zip' })] })
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

  it('persists a legitimate exact [REDACTED] literal when no structural credential requirement is present', async () => {
    const exactScript = {
      name: 'Literal Redacted Marker',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://example.test' },
      actions: [{
        name: 'Post marker',
        type: 'request',
        method: 'POST',
        target: '/markers',
        body: 'status=[REDACTED]',
      }],
    }
    const result = executableEnterpriseResult(exactScript)
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce(result)
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(result)

    await act(async () => {
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
    })
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip'], 'literal-marker.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({ body: 'status=[REDACTED]' })],
    }), 'project-1', undefined)
  })

  it('opens the archive picker from the keyboard-accessible dropzone', async () => {
    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })

    const dropzone = container.querySelector<HTMLElement>('.lr-dropzone')
    const archiveInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')
    expect(dropzone?.getAttribute('role')).toBe('button')
    expect(dropzone?.tabIndex).toBe(0)
    const clickSpy = archiveInput ? vi.spyOn(archiveInput, 'click') : null

    await act(async () => {
      dropzone?.dispatchEvent(new KeyboardEvent('keydown', { key: ' ', bubbles: true }))
    })

    expect(clickSpy).toHaveBeenCalledTimes(1)
  })

  it('paginates a high-cardinality archive queue with keyboard selection across pages', async () => {
    const files = Array.from({ length: 51 }, (_unused, index) => (
      new File(['zip'], `archive-${String(index + 1).padStart(2, '0')}.zip`, { type: 'application/zip' })
    ))
    await act(async () => root.render(<EnterpriseArchiveImport initialFiles={files} />))
    await flushPromises()

    expect(container.querySelectorAll('.lr-file-row')).toHaveLength(50)
    expect(container.textContent).toContain('Showing 1–50 of 51')
    const next = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next archives'))!
    await act(async () => next.click())
    expect(container.querySelectorAll('.lr-file-row')).toHaveLength(1)
    expect(container.textContent).toContain('archive-51.zip')
    expect(container.textContent).toContain('Showing 51–51 of 51')

    const selected = container.querySelector<HTMLButtonElement>('.lr-file-row-select')!
    await act(async () => {
      selected.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }))
    })
    expect(container.querySelectorAll('.lr-file-row')).toHaveLength(50)
    const active = Array.from(container.querySelectorAll<HTMLButtonElement>('.lr-file-row-select'))
      .find(button => button.getAttribute('aria-pressed') === 'true')!
    expect(active.textContent).toContain('archive-50.zip')
    expect(document.activeElement).toBe(active)
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
      java_import_surface_matrix: [
        {
          id: 'java-web-http',
          area: 'HTTP',
          construct: 'web.* / HttpURLConnection / HttpClient / Apache HttpClient',
          status: 'supported',
          execution_status: 'external_runner_required',
          fields: ['url', 'method', 'headers', 'body'],
          notes: 'Detected for source review; the complete archive remains in one source-order-preserving java_vuser draft.',
        },
        {
          id: 'java-jdbc',
          area: 'Database',
          construct: 'java.sql DriverManager/Connection/PreparedStatement',
          status: 'supported',
          execution_status: 'external_runner_required',
          fields: ['sql', 'query', 'exec'],
          notes: 'Detected for source review; the complete archive remains in one source-order-preserving java_vuser draft.',
        },
        {
          id: 'java-no-runtime-surfaces',
          area: 'Review',
          construct: 'RMI, EJB, CORBA, SAP GUI Java, Jacada, Oracle NCA, Tuxedo',
          status: 'supported',
          execution_status: 'external_runner_required',
          review_required: true,
          notes: 'Detected for source review; the complete archive remains in one source-order-preserving java_vuser draft.',
        },
      ],
      java_http_idiom_matrix: [
        { id: 'java-web-custom-request', area: 'HTTP', construct: 'web.customRequest', status: 'supported', execution_status: 'external_runner_required', fields: ['name', 'URL', 'Method', 'Body'], notes: 'Detected for source review; the complete archive remains combined.' },
      ],
      java_jms_idiom_matrix: [
        { id: 'java-jms-publish-consume', area: 'JMS', construct: 'producer.send / consumer.receive', status: 'supported', execution_status: 'external_runner_required', fields: ['jms_publish', 'jms_consume'], notes: 'Detected for source review; the complete archive remains combined.' },
      ],
      java_jdbc_idiom_matrix: [
        { id: 'java-jdbc-query', area: 'JDBC', construct: 'PreparedStatement.executeQuery', status: 'supported', execution_status: 'external_runner_required', fields: ['select_sql'], notes: 'Detected for source review; the complete archive remains combined.' },
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

    const javaCoverage = coverage?.querySelector('[aria-label="java vuser import coverage"]') as HTMLElement | null
    expect(javaCoverage).not.toBeNull()
    expect(javaCoverage?.textContent).toContain('Java Vuser Import Coverage')
    expect(javaCoverage?.textContent).toContain('HTTP: web.* / HttpURLConnection / HttpClient / Apache HttpClient')
    expect(javaCoverage?.textContent).toContain('Supported / External Runner Required')
    expect(javaCoverage?.textContent).toContain('Database: java.sql DriverManager/Connection/PreparedStatement')
    expect(javaCoverage?.textContent).toContain('Review: RMI, EJB, CORBA, SAP GUI Java, Jacada, Oracle NCA, Tuxedo')
    expect(javaCoverage?.textContent).toContain('Supported / External Runner Required / Review Required')
    expect(javaCoverage?.textContent).toContain('HTTP, JDBC, JMS, SOAP, and custom Java remain together')
    expect(javaCoverage?.textContent).toContain('java_vuser_persistent_lifecycle_v1')
    expect(javaCoverage?.textContent).toContain('java_vuser_session_frame_token_v1')

    const javaHTTPDetails = coverage?.querySelector('details[data-coverage="java_http"]') as HTMLDetailsElement | null
    const javaJMSDetails = coverage?.querySelector('details[data-coverage="java_jms"]') as HTMLDetailsElement | null
    const javaJDBCDetails = coverage?.querySelector('details[data-coverage="java_jdbc"]') as HTMLDetailsElement | null
    expect(javaHTTPDetails?.textContent).toContain('web.customRequest')
    expect(javaHTTPDetails?.textContent).toContain('complete archive remains combined')
    expect(javaJMSDetails?.textContent).toContain('producer.send / consumer.receive')
    expect(javaJDBCDetails?.textContent).toContain('PreparedStatement.executeQuery')
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
      root.render(<EnterpriseArchiveImport folderId="folder-9" onImported={onImported} />)
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
    expect(container.textContent).toContain('Selected migration block policy: Blocked')
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('.lr-advanced-policy input')).every(input => !input.checked)).toBe(true)
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

    coverageButton.focus()
    await act(async () => {
      coverageButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      await new Promise(resolve => window.setTimeout(resolve, 0))
    })

    const coverageDialog = container.querySelector<HTMLElement>('[role="dialog"]')
    expect(coverageDialog).not.toBeNull()
    expect(container.textContent).toContain('Coverage Details')
    expect(container.textContent).toContain('Executed')
    expect(container.textContent).toContain('HTTP: Timeouts, redirects, and connection reuse')
    expect(container.textContent).toContain('Tested Examples')
    expect(container.textContent).toContain('Known Caveats')
    expect(container.textContent).toContain('Streaming settings execute in the HTTP runtime for media-like responses')

    const closeCoverageButton = coverageDialog?.querySelector<HTMLButtonElement>('.lr-modal-header button')
    expect(closeCoverageButton).toBeDefined()
    expect(document.activeElement).toBe(closeCoverageButton)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true }))
    })
    expect(coverageDialog?.contains(document.activeElement)).toBe(true)

    await act(async () => {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    })

    expect(container.querySelector('[role="dialog"]')).toBeNull()
    expect(container.textContent).not.toContain('HTTP: Timeouts, redirects, and connection reuse')
    expect(document.activeElement).toBe(coverageButton)

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

    const convertButton = findButton(container, 'Save and Continue Importing')
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
      migration_block_statuses: ['blocked'],
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
      migration_block_statuses: ['blocked'],
    })
    expect(container.textContent).toContain('Review')
    expect(container.querySelector('[aria-label="enterprise protocol import coverage"]')).toBeNull()
    const conversionReviewTabs = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Script archive review sections"] [role="tab"]'))
    expect(conversionReviewTabs.map(tab => tab.textContent)).toEqual([
      'Review',
      'Preserved Source Settings',
      'DSL Preview',
      'Source Mapping',
    ])
    expect(conversionReviewTabs[0]?.getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Demo Enterprise Script Archive')
    const conversionWarningDetails = container.querySelector<HTMLButtonElement>('button[aria-label="View 1 import warning detail"]')
    expect(conversionWarningDetails).not.toBeNull()
    await clickButton(conversionWarningDetails!)
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('manual review recommended')
    await clickButton(Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'))
      .find(button => button.textContent?.trim() === 'Close')!)
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

    await clickButton(findButton(container, 'Source Mapping')!)
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 40)) })
    expect(conversionReviewTabs[3]?.getAttribute('aria-selected')).toBe('true')
    expect(container.textContent).toContain('Source Mapping')
    expect(container.textContent).toContain('Validate the conversion line by line')
    expect(container.textContent).toContain('Action.c:3-5')
    expect(container.textContent).toContain('web_url')
    expect(container.textContent).toContain('"name": "Homepage"')
    const sourceComparison = container.querySelector('.script-import-review-preview-aside')
    expect(sourceComparison?.querySelector('.source-map-review')).not.toBeNull()

    await clickButton(findButton(container, 'DSL Preview')!)
    expect(conversionReviewTabs[2]?.getAttribute('aria-selected')).toBe('true')
    const review = container.querySelector('.script-import-review')
    const reviewChildren = Array.from(review?.children || [])
    const preview = reviewChildren.find(element => element.classList.contains('script-import-review-preview')) as HTMLDetailsElement | undefined
    expect(preview?.tagName).toBe('DETAILS')
    expect(preview?.open).toBe(false)
    expect(preview?.querySelector('summary h4')?.textContent).toBe('DSL Preview')
    expect(review?.querySelector('.script-import-review-preview-aside')).toBeNull()
    expect(review?.querySelector('.script-import-review-preview-row')).toBeNull()

    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Demo Enterprise Script Archive',
      protocol: 'http',
    }), undefined, 'folder-9')
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

  it('stages parameterization for review and saves the server-applied enterprise script', async () => {
    const defaultResult = await apiMock.importEnterpriseArchive()
    apiMock.importEnterpriseArchive.mockClear()
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      ...defaultResult,
      script: {
        name: 'Parameterized Enterprise Script',
        version: '1.0.0',
        protocol: 'http',
        actions: [{ name: 'Get Order', type: 'request', method: 'GET', target: '/orders?customer_id=42' }],
      },
      scripts: undefined,
      parameterization: {
        suggestions: [{
          id: 'pz_enterprise_customer',
          kind: 'data_param',
          var_name: 'customer_id',
          replacement: '{{var.customer_id}}',
          sample_value: '42',
          confidence: 0.9,
          rule_id: 'test.enterprise.customer',
          rationale: 'Customer identifier should vary by virtual user',
          auto_apply: true,
          high_risk: false,
          locations: [{ section: 'actions', action_index: 0, action_name: 'Get Order', field: 'target' }],
        }],
        summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
      },
      secret_binding_requirements: [],
      summary: {
        ...defaultResult.summary,
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
    })

    await act(async () => {
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
    })
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    const file = new File(['zip-content'], 'parameterized.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await flushPromises()
    await clickButton(findButton(container, 'Save and Continue Importing')!)

    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Review parameterization suggestions before saving.')
    expect(container.textContent).toContain('Parameterization Review — Parameterized Enterprise Script (1)')
    await act(async () => {
      container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    await act(async () => {
      findButton(container, 'Save and Continue Importing')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.applyImportParameterization).toHaveBeenCalledWith(expect.objectContaining({
      selected_ids: ['pz_enterprise_customer'],
    }))
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Parameterized Enterprise Script',
      actions: [expect.objectContaining({ target: '/orders?customer_id={{var.customer_id}}' })],
    }), 'project-1', undefined)
  })

  it('stages a conversion with redacted credentials until its secret binding is applied', async () => {
    const redactedScript = {
      name: 'Redacted Enterprise Login',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: 'password=[REDACTED]',
      }],
    }
    const resolvedScript = {
      ...redactedScript,
      actions: [{
        ...redactedScript.actions[0],
        body: 'password={{secret.LOGIN_PASSWORD}}',
      }],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      scripts: [redactedScript],
      script: redactedScript,
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'Login.usr',
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'No migration blockers.',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
      secret_binding_requirements: [{
        id: 'login-password',
        path: '/actions/0/body',
        label: 'Login — Password',
        context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD',
        script_index: 0,
        script_name: 'Redacted Enterprise Login',
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedScript,
      secret_binding_requirements: [],
    })
    let resolveSave: ((value: unknown) => void) | undefined
    apiMock.createScript.mockReturnValueOnce(new Promise(resolve => { resolveSave = resolve }))

    await act(async () => {
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
    })
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')
    expect(fileInput).not.toBeNull()
    const file = new File(['zip-content'], 'login.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput!, 'files', { configurable: true, value: [file] })
      fileInput!.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')
    const convertButton = findButton(container, 'Save and Continue Importing')
    expect(convertButton).toBeDefined()
    await clickButton(convertButton!)

    expect(container.textContent).toContain('Resolve the redacted credential bindings before saving')
    expect(container.textContent).toContain('Credentials (1)')
    expect(apiMock.createScript).not.toHaveBeenCalled()
    const save = findButton(container, 'Save and Continue Importing with 1 secret to configure')
    expect(save?.disabled).toBe(false)
    act(() => {
      save?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      save?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(1)
    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: redactedScript,
      script_index: 0,
      bindings: [{ requirement_id: 'login-password', secret_name: 'LOGIN_PASSWORD' }],
    })
    await clickButton(findButton(container, 'DSL Preview')!)
    expect(container.textContent).toContain('{{secret.LOGIN_PASSWORD}}')
    expect(container.textContent).toContain('Secret bindings applied to Redacted Enterprise Login. Other generated scripts remain independent.')
    expect(container.textContent).not.toContain('Resolve every redacted credential binding before saving this script.')
    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({ body: 'password={{secret.LOGIN_PASSWORD}}' })],
    }), 'project-1', undefined)
    expect(findButton(container, 'Saving...')?.disabled).toBe(true)
    const assertionOption = Array.from(container.querySelectorAll<HTMLLabelElement>('.lr-checkbox'))
      .find(label => label.textContent?.includes('Add default status_code assertions'))
      ?.querySelector<HTMLInputElement>('input')
    expect(assertionOption?.disabled).toBe(true)
    const reviewedAssertionOption = assertionOption?.checked
    await act(async () => {
      assertionOption?.click()
    })
    expect(assertionOption?.checked).toBe(reviewedAssertionOption)
    await act(async () => {
      resolveSave?.({
        id: 'saved-redacted', name: resolvedScript.name, protocol: resolvedScript.protocol,
        version: resolvedScript.version, created_at: '2026-05-17T00:00:00Z', script: resolvedScript,
      })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.deleteScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Saved Redacted Enterprise Login to the script library')
  })

  it('reuses a configured project secret when an atomic enterprise script save is retried', async () => {
    const redactedScript = {
      name: 'Enterprise Credential Retry',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: 'password=[REDACTED]',
      }],
    }
    const resolvedScript = {
      ...redactedScript,
      actions: [{
        ...redactedScript.actions[0],
        body: 'password={{secret.ENTERPRISE_LOGIN_PASSWORD}}',
      }],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult(redactedScript, {
      secret_binding_requirements: [{
        id: 'enterprise-login-password',
        path: '/actions/0/body',
        label: 'Login — Password',
        context: 'Form credential used by the imported enterprise script.',
        suggested_name: 'ENTERPRISE_LOGIN_PASSWORD',
        script_index: 0,
        script_name: redactedScript.name,
      }],
    }))
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedScript,
      secret_binding_requirements: [],
    })
    apiMock.saveEnterpriseArchiveImport
      .mockRejectedValueOnce(new Error('atomic enterprise script save unavailable'))
      .mockResolvedValueOnce({
        count: 1,
        scripts: [{
          id: 'saved-enterprise-retry',
          name: resolvedScript.name,
          protocol: resolvedScript.protocol,
          version: resolvedScript.version,
          created_at: '2026-05-17T00:00:00Z',
          script: resolvedScript,
        }],
      })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip-content'], 'credential-retry.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)

    const valueInput = container.querySelector<HTMLInputElement>('input[aria-label="Secret value for ENTERPRISE_LOGIN_PASSWORD"]')!
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(valueInput, 'write-only-enterprise-password')
      valueInput.dispatchEvent(new Event('input', { bubbles: true }))
    })
    const firstSave = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await clickButton(firstSave)

    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('atomic enterprise script save unavailable')
    expect(container.textContent).toContain('remain in Project Secrets independently of script saving')
    expect(container.textContent).toContain('retrying this save reuses those configured values')
    expect(container.textContent).not.toContain('write-only-enterprise-password')
    const firstOperationID = apiMock.saveEnterpriseArchiveImport.mock.calls[0][0].operation_id

    const retry = container.querySelector<HTMLButtonElement>('.script-import-review-actions .btn-primary')!
    await clickButton(retry)

    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(2)
    expect(apiMock.saveEnterpriseArchiveImport.mock.calls[1][0].operation_id).toBe(firstOperationID)
    expect(apiMock.createProjectSecrets).toHaveBeenCalledTimes(1)
  })

  it('reports the complete atomic batch when Back is clicked during its save', async () => {
    const onClose = vi.fn()
    const onImported = vi.fn()
    const first = {
      name: 'First Converted Script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'First', type: 'request', method: 'GET', target: '/first' }],
    }
    const second = {
      name: 'Second Converted Script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'Second', type: 'request', method: 'GET', target: '/second' }],
    }
    const result = {
      ...executableEnterpriseResult(first),
      script: first,
      scripts: [first, second],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce(result)
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(result)
    let resolveBatchSave!: (value: unknown) => void
    apiMock.saveEnterpriseArchiveImport.mockReturnValueOnce(new Promise(resolve => {
      resolveBatchSave = resolve
    }))

    await act(async () => root.render(
      <EnterpriseArchiveImport
        projectId="project-1"
        onClose={onClose}
        onImported={onImported}
      />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['batch'], 'batch.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await act(async () => {
      findButton(container, 'Save and Continue Importing')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(1)
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ name: first.name }),
        expect.objectContaining({ name: second.name }),
      ],
    }))

    await clickButton(findButton(container, 'Back')!)
    expect(onClose).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveBatchSave({
        scripts: [
          {
            id: 'first-after-back',
            name: first.name,
            protocol: first.protocol,
            version: first.version,
            created_at: '2026-05-17T00:00:00Z',
            script: first,
          },
          {
            id: 'second-after-back',
            name: second.name,
            protocol: second.protocol,
            version: second.version,
            created_at: '2026-05-17T00:00:00Z',
            script: second,
          },
        ],
        count: 2,
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(1)
    expect(apiMock.deleteScript).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledWith({
      scripts: [
        expect.objectContaining({ id: 'first-after-back' }),
        expect.objectContaining({ id: 'second-after-back' }),
      ],
      stayInImportReview: true,
    })
  })

  it('publishes a confirmed batch before source-session cleanup and does so once across navigation', async () => {
    const onImported = vi.fn()
    const onClose = vi.fn()
    const script = {
      name: 'Committed Before Cleanup',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'Request', type: 'request', method: 'GET', target: '/' }],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce(executableEnterpriseResult(script))
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult(script, {
      source_secret_session_id: 'cleanup-after-commit',
      source_secret_session_expires_at: '2099-01-01T00:00:00Z',
    }))
    apiMock.saveEnterpriseArchiveImport.mockResolvedValueOnce({
      count: 1,
      scripts: [{
        id: 'committed-before-cleanup',
        name: script.name,
        protocol: script.protocol,
        version: script.version,
        created_at: '2026-05-17T00:00:00Z',
        script,
      }],
    })
    let resolveDiscard!: () => void
    apiMock.discardImportSecretSession.mockReturnValue(new Promise<void>(resolve => {
      resolveDiscard = resolve
    }))

    await act(async () => root.render(
      <EnterpriseArchiveImport projectId="project-1" onImported={onImported} onClose={onClose} />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip'], 'cleanup.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'cleanup-after-commit',
    })
    expect(onImported).toHaveBeenCalledTimes(1)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ id: 'committed-before-cleanup' })],
    }))

    await clickButton(findButton(container, 'Back')!)
    await act(async () => {
      root.render(<EnterpriseArchiveImport projectId="project-2" onImported={onImported} onClose={onClose} />)
      await Promise.resolve()
    })
    await act(async () => {
      resolveDiscard()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(onImported).toHaveBeenCalledTimes(1)
  })

  it('allocates case-fold-safe script names across archives before the atomic batch save', async () => {
    const files = [
      new File(['alpha'], 'alpha.zip', { type: 'application/zip' }),
      new File(['beta'], 'beta.zip', { type: 'application/zip' }),
    ]
    const resultFor = (file: File) => executableEnterpriseResult({
      name: file.name === 'alpha.zip' ? 'Shared Human Name' : 'shared human name',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'Request', type: 'request', method: 'GET', target: '/' }],
    })
    apiMock.validateEnterpriseArchiveImport.mockImplementation(async (file: File) => resultFor(file))
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => resultFor(file))

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: files })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate 2 Scripts')
    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    await flushPromises()

    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [
        expect.objectContaining({ name: 'Shared Human Name' }),
        expect.objectContaining({ name: 'shared human name_2' }),
      ],
    }))
  })

  it('does not validate another queued archive after Back fences a pending validation', async () => {
    const onClose = vi.fn()
    const script = {
      name: 'Validated Script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'Browse', type: 'request', method: 'GET', target: '/' }],
    }
    let resolveFirstValidation!: (value: unknown) => void
    apiMock.validateEnterpriseArchiveImport.mockImplementationOnce(() => new Promise(resolve => {
      resolveFirstValidation = resolve
    }))

    await act(async () => root.render(
      <EnterpriseArchiveImport projectId="project-1" onClose={onClose} />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['first'], 'first.zip', { type: 'application/zip' }),
          new File(['second'], 'second.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await act(async () => {
      findButton(container, 'Validate 2 Scripts')?.click()
      await Promise.resolve()
    })
    expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenCalledTimes(1)

    await clickButton(findButton(container, 'Back')!)
    await act(async () => {
      resolveFirstValidation(executableEnterpriseResult(script))
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenCalledTimes(1)
  })

  it('does not convert another queued archive after Back fences a pending conversion', async () => {
    const onClose = vi.fn()
    const script = {
      name: 'Converted Script',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{ name: 'Browse', type: 'request', method: 'GET', target: '/' }],
    }
    const result = executableEnterpriseResult(script)
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue(result)
    let resolveFirstConversion!: (value: unknown) => void
    apiMock.importEnterpriseArchive.mockImplementationOnce(() => new Promise(resolve => {
      resolveFirstConversion = resolve
    }))

    await act(async () => root.render(
      <EnterpriseArchiveImport projectId="project-1" onClose={onClose} />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['first'], 'first.zip', { type: 'application/zip' }),
          new File(['second'], 'second.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate 2 Scripts')
    await act(async () => {
      findButton(container, 'Save Ready Scripts and Continue Importing')?.click()
      await Promise.resolve()
    })
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(1)

    await clickButton(findButton(container, 'Back')!)
    await act(async () => {
      resolveFirstConversion(result)
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onClose).toHaveBeenCalledTimes(1)
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(1)
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('auto-saves a converted batch item when its imported credential remains runnable', async () => {
    const onImported = vi.fn()
    const importedScript = {
      name: 'Runnable Enterprise Login',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: 'password=imported-runtime-password',
      }],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult(importedScript, {
      source_secret_session_id: 'enterprise-auto-source-session',
      secret_binding_requirements: [{
        id: 'login-password',
        path: '/actions/0/body',
        label: 'Login — Password',
        context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD',
        script_index: 0,
        script_name: importedScript.name,
        destination_value_preserved: true,
      }],
    }))

    await act(async () => root.render(
      <EnterpriseArchiveImport projectId="project-1" onImported={onImported} />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip-content'], 'login.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'enterprise-auto-source-session',
    })
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({ body: 'password=imported-runtime-password' })],
    }), 'project-1', undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ name: importedScript.name })],
    }))
    expect(container.textContent).not.toContain('Resolve the redacted credential bindings before saving')
  })

  it('manually saves a reviewed conversion without replacing a preserved imported credential', async () => {
    const importedScript = {
      name: 'Reviewed Enterprise Login',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: 'password=imported-runtime-password&user=alice',
      }],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult(importedScript, {
      secret_binding_requirements: [{
        id: 'login-password',
        path: '/actions/0/body',
        label: 'Login — Password',
        context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD',
        script_index: 0,
        script_name: importedScript.name,
        destination_value_preserved: true,
      }],
      parameterization: {
        suggestions: [{
          id: 'parameterize-user',
          kind: 'data_param',
          var_name: 'LOGIN_USER',
          replacement: '{{var.LOGIN_USER}}',
          confidence: 0.9,
          rule_id: 'test',
          rationale: 'Imported username can be parameterized.',
          auto_apply: false,
          high_risk: false,
          pre_applied: false,
        }],
        summary: {
          total: 1,
          by_kind: { data_param: 1 },
          auto_apply_count: 0,
          review_count: 1,
          high_risk_count: 0,
          pre_applied_count: 0,
        },
      },
    }))

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip-content'], 'reviewed-login.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Review parameterization suggestions before saving')
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({
        body: 'password=imported-runtime-password&user=alice',
      })],
    }), 'project-1', undefined)
  })

  it('saves a validated credential-preserving draft before opening the persisted editor', async () => {
    const onEdit = vi.fn()
    const onImported = vi.fn()
    const importedScript = {
      name: 'Validated Enterprise Login',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://api.example.test' },
      actions: [{
        name: 'Login',
        type: 'request',
        method: 'POST',
        target: '/login',
        body: 'password=imported-runtime-password',
      }],
    }
    const importResult = executableEnterpriseResult(importedScript, {
      import_draft_expires_at: '2099-07-26T12:30:00Z',
      secret_binding_requirements: [{
        id: 'login-password',
        path: '/actions/0/body',
        label: 'Login — Password',
        context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD',
        script_index: 0,
        script_name: importedScript.name,
        destination_value_preserved: true,
      }],
    })
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce(importResult)
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(importResult)

    await act(async () => root.render(
      <EnterpriseArchiveImport
        projectId="project-1"
        onImported={onImported}
        onEdit={onEdit}
      />,
    ))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip-content'], 'validated-login.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await flushPromises()

    await clickButton(findButton(container, 'Save and Open in Visual Editor')!)
    await flushPromises()
    expect(apiMock.applySecretBindings).not.toHaveBeenCalled()
    expect(apiMock.createScript).toHaveBeenCalledWith(expect.objectContaining({
      actions: [expect.objectContaining({ body: 'password=imported-runtime-password' })],
    }), 'project-1', undefined)
    expect(onImported).toHaveBeenCalledWith(expect.objectContaining({
      scripts: [expect.objectContaining({ name: importedScript.name })],
    }))
    expect(onEdit).not.toHaveBeenCalled()
  })

  it('reviews bindings for the selected script while migrating the archive-wide source session', async () => {
    const first = {
      name: 'Archive Web', version: '1.0.0', protocol: 'http', config: { base_url: '' },
      actions: [{ name: 'Login', type: 'request', method: 'POST', target: '/login', body: 'token=[REDACTED]' }],
    }
    const second = {
      name: 'Archive Database', version: '1.0.0', protocol: 'database', config: { base_url: '', database: { dsn: '[REDACTED]' } },
      actions: [{ name: 'Query', type: 'db_query', target: 'SELECT 1' }],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      scripts: [first, second], script: first,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [],
      summary: { project_file: 'multi.usr', migration_readiness: { score_pct: 100, blocked: false, summary: 'Ready', policy: { block_statuses: [] } } },
      stats: { total_functions: 2, converted_actions: 2, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
      source_secret_session_id: 'archive-source-session',
      source_secret_session_expires_at: '2099-01-01T00:00:00Z',
      source_secret_migration_allowed: true,
      secret_binding_requirements: [
        { id: 'web-token', path: '/actions/0/body', label: 'Web token', context: 'Login token', suggested_name: 'CREDENTIAL', script_index: 0, script_name: first.name, source_value_available: true },
        { id: 'database-dsn', path: '/config/database/dsn', label: 'Database DSN', context: 'Database credential', suggested_name: 'CREDENTIAL', script_index: 1, script_name: second.name, source_value_available: true },
      ],
    })
    apiMock.applySecretBindings
      .mockResolvedValueOnce({
        script: { ...first, actions: [{ ...first.actions[0], body: 'token={{secret.CREDENTIAL}}' }] },
        secret_binding_requirements: [],
      })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['zip'], 'multi.zip', { type: 'application/zip' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)

    expect(container.textContent).toContain('Credentials (1)')
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="text"]')).map(input => input.value)).toEqual([
      'CREDENTIAL',
    ])
    const migrate = container.querySelector<HTMLInputElement>('.credentials-source-migration input')!
    await act(async () => migrate.click())
    const prepareFirstCredentialLane = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => (
      button.textContent?.includes('Prepare Credentials') || button.textContent?.includes('Save Remaining 2 Scripts Atomically')
    ))
    expect(prepareFirstCredentialLane).toBeDefined()
    await clickButton(prepareFirstCredentialLane!)
    await flushPromises()
    await flushPromises()

    expect(apiMock.migrateImportSecrets).toHaveBeenCalledTimes(1)
    expect(apiMock.migrateImportSecrets).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'archive-source-session',
      secrets: [
        { requirement_id: 'web-token', secret_name: 'CREDENTIAL' },
        { requirement_id: 'database-dsn', secret_name: 'CREDENTIAL_2' },
      ],
    })
    expect(apiMock.applySecretBindings).toHaveBeenNthCalledWith(1, {
      script: first,
      script_index: 0,
      bindings: [{ requirement_id: 'web-token', secret_name: 'CREDENTIAL' }],
    })
    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()

    const secondTab = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(
      button => button.getAttribute('aria-label') === `Review generated script ${second.name}`,
    )!
    await act(async () => secondTab.click())
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="text"]')).map(input => input.value)).toEqual([
      'CREDENTIAL_2',
    ])

    apiMock.applySecretBindings.mockRejectedValueOnce(new ApiError(
      'secret binding requirements changed',
      409,
      {
        code: 'secret_binding_requirements_changed',
        secret_binding_requirements: [{
          id: 'database-dsn',
          path: '/config/database/dsn',
          label: 'Database DSN',
          context: 'Database credential',
          suggested_name: 'CREDENTIAL',
          script_index: 1,
          script_name: second.name,
        }],
      },
    ))
    const prepareSecondCredentialLane = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => (
      button.textContent?.includes('Save Remaining 2 Scripts Atomically') || button.textContent?.includes('Prepare Credentials')
    ))
    expect(prepareSecondCredentialLane).toBeDefined()
    await clickButton(prepareSecondCredentialLane!)
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenNthCalledWith(2, {
      script: second,
      script_index: 1,
      bindings: [{ requirement_id: 'database-dsn', secret_name: 'CREDENTIAL_2' }],
    })
    expect(Array.from(container.querySelectorAll<HTMLInputElement>('.secret-binding-review input[type="text"]')).map(input => input.value)).toEqual([
      'CREDENTIAL_2',
    ])
  })

  it('prepares credentialed generated lanes before one atomic enterprise batch save', async () => {
    const first = {
      name: 'Credentialed Web', version: '1.0.0', protocol: 'http', config: { base_url: '' },
      actions: [{ name: 'Login', type: 'request', method: 'POST', target: '/login', body: 'token=[REDACTED]' }],
    }
    const second = {
      name: 'Credentialed Database', version: '1.0.0', protocol: 'database', config: { base_url: '', database: { dsn: '[REDACTED]' } },
      actions: [{ name: 'Query', type: 'db_query', target: 'SELECT 1' }],
    }
    const resolvedFirst = { ...first, actions: [{ ...first.actions[0], body: 'token={{secret.WEB_TOKEN}}' }] }
    const resolvedSecond = { ...second, config: { ...second.config, database: { dsn: '{{secret.DATABASE_DSN}}' } } }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      scripts: [first, second], script: first,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [],
      summary: { project_file: 'credentialed.usr', migration_readiness: { score_pct: 100, blocked: false, summary: 'Ready', policy: { block_statuses: [] } } },
      stats: { total_functions: 2, converted_actions: 2, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
      source_secret_session_id: 'credentialed-archive-session',
      source_secret_session_expires_at: '2099-01-01T00:00:00Z',
      source_secret_migration_allowed: false,
      secret_binding_requirements: [
        { id: 'web-token', path: '/actions/0/body', label: 'Web token', context: 'Login token', suggested_name: 'WEB_TOKEN', script_index: 0, script_name: first.name },
        { id: 'database-dsn', path: '/config/database/dsn', label: 'Database DSN', context: 'Database credential', suggested_name: 'DATABASE_DSN', script_index: 1, script_name: second.name },
      ],
    })
    apiMock.applySecretBindings
      .mockResolvedValueOnce({ script: resolvedFirst, secret_binding_requirements: [] })
      .mockResolvedValueOnce({ script: resolvedSecond, secret_binding_requirements: [] })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['zip'], 'credentialed.zip', { type: 'application/zip' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)

    const prepareFirst = findButton(container, 'Prepare Credentials for Script 1 of 2')
    expect(prepareFirst).toBeDefined()
    await clickButton(prepareFirst!)
    expect(apiMock.saveEnterpriseArchiveImport).not.toHaveBeenCalled()
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Credentialed Database')

    const atomicSave = findButton(container, 'Save Remaining 2 Scripts Atomically')
    expect(atomicSave).toBeDefined()
    await clickButton(atomicSave!)
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(2)
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(1)
    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledWith(expect.objectContaining({
      operation_id: expect.stringMatching(/^enterprise-archive-/),
      scripts: [
        expect.objectContaining({ name: resolvedFirst.name, project_id: 'project-1' }),
        expect.objectContaining({ name: resolvedSecond.name, project_id: 'project-1' }),
      ],
    }))
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'credentialed-archive-session',
    })
  })

  it('does not publish a late credential result after the selected generated script changes', async () => {
    const first = { name: 'Generation A', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Login', type: 'request', target: '/' }] }
    const second = { name: 'Generation B', version: '1.0.0', protocol: 'http', config: { base_url: '' }, actions: [{ name: 'Browse', type: 'request', target: '/browse' }] }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      scripts: [first, second], script: first,
      warnings: [], unsupported_elements: [], assumptions: [], review_sections: [], repair_suggestions: [],
      summary: { project_file: 'race.usr', migration_readiness: { score_pct: 100, blocked: false, summary: 'Ready', policy: { block_statuses: [] } } },
      stats: { total_functions: 2, converted_actions: 2, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
      secret_binding_requirements: [{ id: 'auth', path: '/actions/0/headers/Authorization', label: 'Authorization', context: 'Bearer credential', suggested_name: 'API_TOKEN', script_index: 0, script_name: 'Generation A' }],
    })
    let resolveApply: ((value: unknown) => void) | undefined
    apiMock.applySecretBindings.mockReturnValueOnce(new Promise(resolve => { resolveApply = resolve }))

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['zip'], 'race.zip', { type: 'application/zip' })] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    const save = findButton(container, 'Save Remaining 2 Scripts Atomically with 1 secret to configure')!
    act(() => save.click())
    await flushPromises()
    expect(apiMock.applySecretBindings).toHaveBeenCalledTimes(1)

    const secondTab = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(button => button.getAttribute('aria-label') === 'Review generated script Generation B')!
    await act(async () => secondTab.click())
    await act(async () => {
      resolveApply?.({ script: { ...first, name: 'Resolved A' }, secret_binding_requirements: [] })
      await Promise.resolve()
      await Promise.resolve()
    })

    await clickButton(findButton(container, 'DSL Preview')!)
    expect(container.querySelector('.script-import-review-preview pre')?.textContent).toContain('Generation B')
    expect(container.querySelector('.script-import-review-preview pre')?.textContent).not.toContain('Resolved A')
    expect(apiMock.createScript).not.toHaveBeenCalled()
  })

  it('shows validation credentials and queues focused editor links through safe binding preparation', async () => {
    const onEdit = vi.fn()
    const redactedScript = {
      name: 'Validated Login', version: '1.0.0', protocol: 'http', config: { base_url: '' },
      actions: [{ name: 'Login', type: 'request', method: 'POST', target: '/login', body: 'password=[REDACTED]' }],
    }
    const resolvedScript = {
      ...redactedScript,
      actions: [{ ...redactedScript.actions[0], body: 'password={{secret.LOGIN_PASSWORD}}' }],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      scripts: [redactedScript], script: redactedScript,
      warnings: [], unsupported_elements: ['web_custom_request'], assumptions: [],
      review_sections: [{
        key: 'unsupported_elements', title: 'Unsupported Source Elements', severity: 'blocker',
        summary: 'Review the unsupported source call.', items: ['web_custom_request'],
      }],
      repair_suggestions: [{
        id: 'unsupported-login', category: 'unsupported', severity: 'blocker', title: 'Review Login',
        description: 'Review the converted login action.', action: 'Open editor', target_action: 'Login',
      }],
      summary: {
        project_file: 'Login.usr', unsupported_function_count: 1,
        migration_readiness: { score_pct: 100, blocked: false, summary: 'Ready', policy: { block_statuses: [] } },
      },
      stats: { total_functions: 1, converted_actions: 1, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
      secret_binding_requirements: [{
        id: 'login-password', path: '/actions/0/body', label: 'Login password', context: 'Form credential',
        suggested_name: 'LOGIN_PASSWORD', script_index: 0, script_name: redactedScript.name,
      }],
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({ script: resolvedScript, secret_binding_requirements: [] })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" onEdit={onEdit} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [new File(['zip'], 'validation-login.zip')] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    expect(container.textContent).toContain('Validation Review')
    expect(container.textContent).toContain('Credentials (1)')
    expect(findButton(container, 'Open Draft in Visual Editor with 1 secret to configure')).toBeDefined()
    const focusedEditorLink = findButton(container, 'Open First Unsupported Action In Editor')
    expect(focusedEditorLink).toBeDefined()
    await clickButton(focusedEditorLink!)

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: redactedScript,
      script_index: 0,
      bindings: [{ requirement_id: 'login-password', secret_name: 'LOGIN_PASSWORD' }],
    })
    expect(onEdit).toHaveBeenCalledTimes(1)
    expect(onEdit).toHaveBeenCalledWith(resolvedScript, { targetAction: 'Login' })
    expect(JSON.stringify(onEdit.mock.calls[0]?.[0])).not.toContain('[REDACTED]')
  })

  it('shows and switches generated scripts for non-Java multi-script enterprise archive results', async () => {
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      scripts: [
        {
          name: 'HTTP Flow',
          protocol: 'http',
          actions: [{ name: 'Open Home', type: 'http_request' }],
        },
        {
          name: 'JMS Orders',
          protocol: 'jms-ibmmq',
          actions: [{ name: 'Publish Order', type: 'jms_publish' }],
        },
        {
          name: 'Database Checks',
          protocol: 'database',
          actions: [{ name: 'Select Orders', type: 'db_query' }],
        },
      ],
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        project_file: 'MixedProtocols.usr',
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: {
        total_functions: 3,
        converted_actions: 3,
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

    const file = new File(['zip-content'], 'mixed-protocols.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [file],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')

    const outputSelector = container.querySelector('[aria-label="Generated scripts"]') as HTMLElement | null
    expect(outputSelector).not.toBeNull()
    expect(outputSelector?.textContent).toContain('3 scripts')
    expect(outputSelector?.textContent).toContain('HTTP Flow')
    expect(outputSelector?.textContent).toContain('http · 1 actions')
    expect(outputSelector?.textContent).toContain('JMS Orders')
    expect(outputSelector?.textContent).toContain('jms-ibmmq · 1 actions')
    expect(outputSelector?.textContent).toContain('Database Checks')
    expect(outputSelector?.textContent).toContain('database · 1 actions')

    await clickButton(findButton(container, 'DSL Preview')!)
    const preview = () => container.querySelector('.script-import-review-preview pre')?.textContent || ''
    expect(preview()).toContain('"name": "HTTP Flow"')
    expect(preview()).toContain('"protocol": "http"')

    const jmsButton = container.querySelector('button[aria-label="Review generated script JMS Orders"]') as HTMLButtonElement | null
    expect(jmsButton).not.toBeNull()
    if (!jmsButton) {
      return
    }

    await clickButton(jmsButton)

    expect(jmsButton.getAttribute('aria-pressed')).toBe('true')
    expect(preview()).toContain('"name": "JMS Orders"')
    expect(preview()).toContain('"protocol": "jms-ibmmq"')

    const databaseButton = container.querySelector('button[aria-label="Review generated script Database Checks"]') as HTMLButtonElement | null
    expect(databaseButton).not.toBeNull()
    if (!databaseButton) {
      return
    }

    await clickButton(databaseButton)

    expect(databaseButton.getAttribute('aria-pressed')).toBe('true')
    expect(preview()).toContain('"name": "Database Checks"')
    expect(preview()).toContain('"protocol": "database"')
  })

  it('reviews one combined Java Vuser draft and saves it as one authoring session', async () => {
    const onEdit = vi.fn()
    const onImported = vi.fn()
    const combinedJavaDraft = {
      name: 'Combined Java Vuser',
      version: '1.0.0',
      protocol: 'java_vuser',
      init: [{ name: 'Java init', type: 'java_vuser_run', params: { method: 'init' } }],
      actions: [{ name: 'Java action', type: 'java_vuser_run', params: { method: 'action' } }],
      teardown: [{ name: 'Java end', type: 'java_vuser_run', params: { method: 'end' } }],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          source_script_name: 'CombinedJava.usr',
          preserved: {
            fidelity_report: { promotion_status: 'promotable', blockers: [] },
          },
        },
      },
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      script: combinedJavaDraft,
      scripts: [combinedJavaDraft],
      detected_protocol: {
        canonical_source_protocol: 'java_vuser',
        apexload_protocol: 'java_vuser',
        display_name: 'LoadRunner Java Vuser',
        import_mode: 'external_runner_required',
        confidence: 'high',
        runner_requirement: 'java_vuser_jvm_runner',
        required_evidence: ['Reviewed runner classpath'],
      },
      review_artifact: {
        id: 'java-combined-review',
        title: 'Combined Java Vuser review',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'java_vuser',
          apexload_protocol: 'java_vuser',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'java_vuser_jvm_runner',
        },
        summary: 'Review the combined Java source order before approval.',
        required_evidence: ['Reviewed runner classpath'],
      },
      fidelity_report: { promotion_status: 'promotable', blockers: [] },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        import_mode: 'external_runner_required',
        fidelity_report: { promotion_status: 'promotable', blockers: [] },
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready for review',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: {
        total_functions: 3,
        converted_actions: 3,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
    })

    await act(async () => root.render(
      <EnterpriseArchiveImport projectId="project-1" onEdit={onEdit} onImported={onImported} />,
    ))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const file = new File(['zip-content'], 'combined-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    expect(container.querySelector('[aria-label="Generated scripts"]')).toBeNull()
    expect(container.textContent).toContain('one combined init/action/end source-order draft')
    expect(container.textContent).toContain('java_vuser_persistent_lifecycle_v1')
    expect(container.textContent).toContain('java_vuser_session_frame_token_v1')
    expect(container.textContent).toContain('Each VUser keeps one JVM and Actions instance')
    expect(container.textContent).not.toContain('persistent per-VUser lifecycle support')
    expect(container.textContent).toContain('Runner: java_vuser_jvm_runner')
    expect(container.textContent).toContain('Save this combined draft as an authoring session, then resume that session to edit and approve it')
    expect(findButton(container, 'Open Draft in Visual Editor')).toBeUndefined()
    expect(container.textContent).not.toContain('Open Imported Script In Editor')
    expect(container.textContent).not.toContain('import_promotion_blocked')
    await clickButton(findButton(container, 'DSL Preview')!)
    const preview = container.querySelector('.script-import-review-preview pre')?.textContent || ''
    expect(preview).toContain('"protocol": "java_vuser"')
    expect(preview).toContain('"method": "init"')
    expect(preview).toContain('"method": "action"')
    expect(preview).toContain('"method": "end"')

    const saveButton = findButton(container, 'Save Combined Java Draft for Approval')
    expect(saveButton).toBeDefined()
    await clickButton(saveButton!)

    expect(apiMock.createAuthoringSession).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      source: 'source_review_artifact',
      idempotency_key: expect.stringMatching(/^enterprise-archive-review:lr-import-[^:]+:1:0$/),
      draft: combinedJavaDraft,
      review_state: expect.objectContaining({
        review_artifact: expect.objectContaining({ id: 'java-combined-review' }),
      }),
    }))
    const firstIdempotencyKey = apiMock.createAuthoringSession.mock.calls[0]?.[0]?.idempotency_key
    await clickButton(saveButton!)
    expect(apiMock.createAuthoringSession).toHaveBeenCalledTimes(2)
    expect(apiMock.createAuthoringSession.mock.calls[1]?.[0]?.idempotency_key).toBe(firstIdempotencyKey)
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(onEdit).not.toHaveBeenCalled()
    expect(onImported).toHaveBeenCalledTimes(2)
    expect(onImported).toHaveBeenLastCalledWith({
      scripts: [],
      stayInImportReview: true,
    })
    expect(container.textContent).toContain('Combined Java Vuser draft saved as authoring session')
  })

  it('saves the credential-bound combined Java draft instead of the stale redacted review', async () => {
    const redactedDraft = {
      name: 'Credentialed Combined Java Vuser',
      version: '1.0.0',
      protocol: 'java_vuser',
      config: { base_url: '' },
      actions: [{
        name: 'Java action',
        type: 'java_vuser_run',
        target: 'Actions',
        params: { method: 'action', password: '[REDACTED]' },
      }],
    }
    const resolvedDraft = {
      ...redactedDraft,
      actions: [{
        ...redactedDraft.actions[0],
        params: { method: 'action', password: '{{secret.JAVA_PASSWORD}}' },
      }],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      script: redactedDraft,
      scripts: [redactedDraft],
      detected_protocol: {
        canonical_source_protocol: 'java_vuser',
        apexload_protocol: 'java_vuser',
        display_name: 'LoadRunner Java Vuser',
        import_mode: 'external_runner_required',
        confidence: 'high',
        runner_requirement: 'java_vuser_jvm_runner',
      },
      review_artifact: {
        id: 'java-credential-review',
        title: 'Credentialed combined Java Vuser review',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'java_vuser',
          apexload_protocol: 'java_vuser',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'java_vuser_jvm_runner',
        },
        summary: 'Review and approve the combined Java draft.',
      },
      fidelity_report: { promotion_status: 'promotable', blockers: [] },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        import_mode: 'external_runner_required',
        fidelity_report: { promotion_status: 'promotable', blockers: [] },
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready for review',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: {
        total_functions: 1,
        converted_actions: 1,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 0,
      },
      secret_binding_requirements: [{
        id: 'java-password',
        path: '/actions/0/params/password',
        label: 'Java password',
        context: 'Java Vuser credential',
        suggested_name: 'JAVA_PASSWORD',
        script_index: 0,
        script_name: redactedDraft.name,
      }],
      source_secret_session_id: 'java-source-secrets',
      source_secret_session_expires_at: '2026-05-17T01:00:00Z',
      source_secret_migration_allowed: false,
    })
    apiMock.applySecretBindings.mockResolvedValueOnce({
      script: resolvedDraft,
      secret_binding_requirements: [],
    })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['zip-content'], 'credentialed-java.zip', { type: 'application/zip' }),
          new File(['unrelated'], 'unrelated.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    const removeUnrelated = container.querySelector('[aria-label="Remove unrelated.zip"]') as HTMLButtonElement
    await act(async () => {
      removeUnrelated.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(apiMock.discardImportSecretSession).not.toHaveBeenCalled()

    const saveButton = findButton(container, 'Save Combined Java Draft for Approval with 1 secret to configure')
    expect(saveButton).toBeDefined()
    await clickButton(saveButton!)
    await flushPromises()

    expect(apiMock.applySecretBindings).toHaveBeenCalledWith({
      script: redactedDraft,
      script_index: 0,
      bindings: [{ requirement_id: 'java-password', secret_name: 'JAVA_PASSWORD' }],
    })
    expect(apiMock.createAuthoringSession).toHaveBeenCalledTimes(1)
    expect(apiMock.createAuthoringSession).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      source: 'source_review_artifact',
      draft: resolvedDraft,
    }))
    expect(JSON.stringify(apiMock.createAuthoringSession.mock.calls[0]?.[0])).not.toContain('[REDACTED]')
  })

  it('keeps an active review-artifact save locked when an unrelated queued archive is removed', async () => {
    const onImported = vi.fn()
    const onClose = vi.fn()
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue({
      detected_protocol: {
        canonical_source_protocol: 'sap_gui',
        apexload_protocol: 'sap_gui',
        display_name: 'SAP GUI',
        import_mode: 'external_runner_required',
        confidence: 'high',
        runner_requirement: 'sap_gui_runner',
      },
      review_artifact: {
        id: 'sap-review',
        title: 'SAP GUI review artifact',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'sap_gui',
          apexload_protocol: 'sap_gui',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'sap_gui_runner',
        },
        summary: 'Review the external-runner requirements.',
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        import_mode: 'external_runner_required',
        migration_readiness: {
          score_pct: 100,
          blocked: false,
          summary: 'Ready for review',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: {
        total_functions: 1,
        converted_actions: 0,
        extractors: 0,
        think_times: 0,
        transactions: 0,
        skipped: 1,
      },
    })
    let resolveSave: ((session: {
      id: string
      name: string
      source: string
      status: string
      created_at: string
      updated_at: string
      last_accessed_at: string
    }) => void) | undefined
    apiMock.createAuthoringSession.mockReturnValueOnce(new Promise(resolve => {
      resolveSave = resolve
    }))

    await act(async () => root.render(
      <EnterpriseArchiveImport
        projectId="project-1"
        onImported={onImported}
        onClose={onClose}
      />,
    ))
    await flushPromises()
    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['alpha'], 'alpha.zip', { type: 'application/zip' }),
          new File(['beta'], 'beta.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    const saveButton = findButton(container, 'Save Review Artifact')
    expect(saveButton).toBeDefined()
    await act(async () => {
      saveButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(apiMock.createAuthoringSession).toHaveBeenCalledTimes(1)

    const betaRow = Array.from(container.querySelectorAll<HTMLElement>('.lr-file-row'))
      .find(row => row.textContent?.includes('beta.zip'))
    expect(betaRow).toBeDefined()
    const betaSelect = betaRow?.querySelector<HTMLButtonElement>('.lr-file-row-select')
    await act(async () => {
      betaSelect?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(findButton(container, 'Saving Review Artifact')).toBeUndefined()
    expect(findButton(container, 'Save Review Artifact')).toBeDefined()

    const alphaRow = Array.from(container.querySelectorAll<HTMLElement>('.lr-file-row'))
      .find(row => row.textContent?.includes('alpha.zip'))
    const alphaSelect = alphaRow?.querySelector<HTMLButtonElement>('.lr-file-row-select')
    await act(async () => {
      alphaSelect?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const removeUnrelated = container.querySelector('[aria-label="Remove beta.zip"]') as HTMLButtonElement
    await act(async () => {
      removeUnrelated.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const savingButton = findButton(container, 'Saving Review Artifact')
    expect(savingButton?.disabled).toBe(true)
    savingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(apiMock.createAuthoringSession).toHaveBeenCalledTimes(1)

    await clickButton(findButton(container, 'Back')!)
    expect(onClose).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveSave?.({
        id: 'review-session',
        name: 'SAP GUI review artifact',
        source: 'source_review_artifact',
        status: 'reviewing',
        created_at: '2026-05-17T00:00:00Z',
        updated_at: '2026-05-17T00:00:00Z',
        last_accessed_at: '2026-05-17T00:00:00Z',
      })
    })
    await flushPromises()
    expect(onImported).toHaveBeenCalledWith({
      scripts: [],
      stayInImportReview: true,
    })
    expect(container.textContent).not.toContain('Review artifact saved as authoring session')
  })

  it('keeps a converted-script save marked in flight when an unrelated queued archive is removed', async () => {
    const onImported = vi.fn()
    const draft = {
      name: 'Alpha Draft',
      version: '1.0.0',
      protocol: 'http',
      actions: [{ name: 'A', type: 'request', method: 'GET', target: '/a' }],
    }
    // A parameterization suggestion left for review keeps the batch conversion
    // from auto-saving, so the lane is saved through the review panel instead.
    apiMock.importEnterpriseArchive.mockResolvedValue(executableEnterpriseResult(draft, {
      parameterization: {
        suggestions: [{
          id: 'review-alpha',
          kind: 'data_param',
          var_name: 'ALPHA',
          replacement: '{{var.ALPHA}}',
          confidence: 0.8,
          rule_id: 'test',
          rationale: 'Review the generated parameter.',
          auto_apply: false,
          high_risk: false,
          pre_applied: false,
        }],
        summary: {
          total: 1,
          by_kind: { data_param: 1 },
          auto_apply_count: 0,
          review_count: 1,
          high_risk_count: 0,
          pre_applied_count: 0,
        },
      },
    }))
    let resolveCreate: ((script: Record<string, unknown>) => void) | undefined
    apiMock.createScript.mockImplementationOnce(() => new Promise(resolve => {
      resolveCreate = resolve as (script: Record<string, unknown>) => void
    }))

    await act(async () => root.render(<EnterpriseArchiveImport onImported={onImported} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['alpha'], 'alpha.zip', { type: 'application/zip' }),
          new File(['beta'], 'beta.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    await flushPromises()
    expect(apiMock.createScript).not.toHaveBeenCalled()

    await act(async () => {
      findButton(container, 'Save and Continue Importing')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    expect(findButton(container, 'Saving...')).toBeDefined()

    const removeUnrelated = container.querySelector('[aria-label="Remove beta.zip"]') as HTMLButtonElement
    await act(async () => {
      removeUnrelated.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // The POST for alpha is still running, so the save action must stay locked
    // instead of reverting to a clickable "Save and Continue Importing".
    const savingButton = findButton(container, 'Saving...')
    expect(savingButton?.disabled).toBe(true)
    savingButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    expect(apiMock.createScript).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveCreate?.({
        id: 'saved-script',
        name: draft.name,
        protocol: 'http',
        version: '1.0.0',
        created_at: '2026-05-17T00:00:00Z',
        script: draft,
      })
    })
    await flushPromises()
    expect(container.textContent).toContain('Saved Alpha Draft to the script library')
  })

  it('skips only the removed archive when a queued row is removed mid-conversion', async () => {
    const firstFile = new File(['alpha'], 'alpha.zip', { type: 'application/zip' })
    const removedFile = new File(['beta'], 'beta.zip', { type: 'application/zip' })
    const lastFile = new File(['gamma'], 'gamma.zip', { type: 'application/zip' })
    let resolveFirstImport: (() => void) | undefined
    apiMock.importEnterpriseArchive.mockImplementation((file: File) => {
      const conversion = {
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
      }
      if (file === firstFile) {
        return new Promise(resolve => {
          resolveFirstImport = () => resolve(conversion)
        })
      }
      return Promise.resolve(conversion)
    })

    await act(async () => root.render(<EnterpriseArchiveImport />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [firstFile, removedFile, lastFile],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    await act(async () => {
      findButton(container, 'Save Ready Scripts and Continue Importing')?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      await Promise.resolve()
    })
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(1)

    const removeBeta = container.querySelector('[aria-label="Remove beta.zip"]') as HTMLButtonElement
    await act(async () => {
      removeBeta.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    // Removing one queued row must not look like the batch ended: the run is
    // still in flight, so the queue actions stay locked.
    const queueActions = Array.from(container.querySelectorAll<HTMLButtonElement>('.lr-import-action-buttons button'))
    expect(queueActions[0]?.disabled).toBe(true)
    expect(queueActions[1]?.disabled).toBe(true)
    expect(container.textContent).toContain('Processing')

    await act(async () => {
      resolveFirstImport?.()
    })
    await flushPromises()
    await flushPromises()

    // gamma is queued behind the removed row and must still be converted.
    expect(apiMock.importEnterpriseArchive.mock.calls.map(call => (call[0] as File).name)).toEqual(['alpha.zip', 'gamma.zip'])
    expect(apiMock.createScript).toHaveBeenCalledTimes(2)
    expect(container.textContent).toContain('Converted and saved 2 scripts to the library; 1 archive skipped')
  })

  it('keeps a fidelity-blocked combined Java Vuser draft in review instead of offering approval', async () => {
    const blockedJavaDraft = {
      name: 'Blocked Combined Java Vuser',
      version: '1.0.0',
      protocol: 'java_vuser',
      actions: [{ name: 'Java action', type: 'java_vuser_run', params: { method: 'action' } }],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          preserved: {
            fidelity_report: {
              promotion_status: 'blocked',
              blockers: ['Selected Actions class has no public zero-argument action method.'],
            },
          },
        },
      },
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      script: blockedJavaDraft,
      scripts: [blockedJavaDraft],
      detected_protocol: {
        canonical_source_protocol: 'java_vuser',
        apexload_protocol: 'java_vuser',
        display_name: 'LoadRunner Java Vuser',
        import_mode: 'external_runner_required',
        confidence: 'high',
        runner_requirement: 'java_vuser_jvm_runner',
      },
      review_artifact: {
        id: 'java-blocked-review',
        title: 'Blocked combined Java Vuser review',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'java_vuser',
          apexload_protocol: 'java_vuser',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'java_vuser_jvm_runner',
        },
        summary: 'Resolve the selected lifecycle blocker.',
      },
      fidelity_report: {
        promotion_status: 'blocked',
        blockers: ['Selected Actions class has no public zero-argument action method.'],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [{
        id: 'protocol:java-vuser-fidelity',
        category: 'protocol',
        severity: 'blocker',
        title: 'Resolve combined Java Vuser fidelity blockers',
        description: 'The draft is not ready for authoring approval.',
        action: 'Resolve every named fidelity blocker.',
      }],
      summary: {
        import_mode: 'external_runner_required',
        fidelity_report: {
          promotion_status: 'blocked',
          blockers: ['Selected Actions class has no public zero-argument action method.'],
        },
        migration_readiness: {
          score_pct: 50,
          blocked: false,
          summary: 'Review required',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
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

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const file = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    expect(container.textContent).toContain('fidelity blockers must be resolved before authoring approval')
    expect(container.textContent).toContain('Fidelity blocker: Selected Actions class has no public zero-argument action method.')
    expect(findButton(container, 'Save Combined Java Draft for Approval')).toBeUndefined()
    const saveReview = findButton(container, 'Save Review Artifact Instead')
    expect(saveReview).toBeDefined()
    await clickButton(saveReview!)

    expect(apiMock.createAuthoringSession).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      source: 'source_review_artifact',
      draft: blockedJavaDraft,
    }))
    expect(container.textContent).toContain('Combined Java Vuser review artifact saved as authoring session')
  })

  // Shared by the required-files attachment tests below: a Java Vuser validation
  // response blocked on an unresolved superclass, which is what drives the
  // "Required Files To Resolve Fidelity Blockers" panel.
  const mockBlockedJavaSuperclassValidation = () => {
    const unresolvedSuperclassBlocker = 'Java runner class Action extends unresolved superclass BasicClient; upload the exact superclass source so construction and lifecycle semantics can be verified before promotion.'
    const blockedJavaDraft = {
      name: 'Blocked Combined Java Vuser',
      version: '1.0.0',
      protocol: 'java_vuser',
      actions: [{ name: 'Java action', type: 'java_vuser_run', params: { method: 'action' } }],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          preserved: {
            fidelity_report: {
              promotion_status: 'blocked',
              blockers: [unresolvedSuperclassBlocker],
            },
          },
        },
      },
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue({
      script: blockedJavaDraft,
      scripts: [blockedJavaDraft],
      detected_protocol: {
        canonical_source_protocol: 'java_vuser',
        apexload_protocol: 'java_vuser',
        display_name: 'LoadRunner Java Vuser',
        import_mode: 'external_runner_required',
        confidence: 'high',
        runner_requirement: 'java_vuser_jvm_runner',
      },
      review_artifact: {
        id: 'java-blocked-review',
        title: 'Blocked combined Java Vuser review',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'java_vuser',
          apexload_protocol: 'java_vuser',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'java_vuser_jvm_runner',
        },
        summary: 'Resolve the selected lifecycle blocker.',
      },
      fidelity_report: {
        promotion_status: 'blocked',
        blockers: [unresolvedSuperclassBlocker],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        import_mode: 'external_runner_required',
        fidelity_report: {
          promotion_status: 'blocked',
          blockers: [unresolvedSuperclassBlocker],
        },
        migration_readiness: {
          score_pct: 50,
          blocked: false,
          summary: 'Review required',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: { total_functions: 1, converted_actions: 1, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
    })
  }

  it('renames Java sources attached as .txt so they resolve the blocked superclass', async () => {
    // Corporate mail gateways strip .java attachments, so operators send the
    // superclass as BasicClient.java.txt. The archive importer performs the
    // authoritative rename server-side; the picker mirrors it so the required
    // files panel reflects the resolved name before re-validation.
    mockBlockedJavaSuperclassValidation()

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    expect(container.textContent).toContain('BasicClient (BasicClient.java) - missing')

    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    const textFile = new File(['public class BasicClient {}'], 'BasicClient.java.txt', { type: 'text/plain' })
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', { configurable: true, value: [textFile] })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    expect(validationCalls[validationCalls.length - 1]?.[1]).toEqual(expect.objectContaining({
      supplemental_files: [expect.objectContaining({ name: 'BasicClient.java' })],
    }))
    expect(container.textContent).toContain('BasicClient (BasicClient.java) - attached')
  })

  it('bounds how much of a .txt attachment it sniffs for Java', async () => {
    // The 256 KiB sniff cap is load-bearing, not cosmetic: the block-comment
    // masking pass is quadratic on input carrying many `/*` with no closing
    // `*/`, so an uncapped multi-megabyte attachment blocks the main thread for
    // tens of seconds. This pins the resulting boundary — a declaration sitting
    // past the cap is deliberately not detected — and matches the identical cap
    // in pkg/convert/enterprisearchive. Deleting the cap makes this fail.
    mockBlockedJavaSuperclassValidation()

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    const beyondCap = '/* license */\n'.repeat(25000) + 'public class BasicClient extends Object {}'
    expect(beyondCap.length).toBeGreaterThan(256 * 1024)
    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', {
        configurable: true,
        value: [new File([beyondCap], 'BasicClient.java.txt', { type: 'text/plain' })],
      })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    expect(validationCalls[validationCalls.length - 1]?.[1]).toEqual(expect.objectContaining({
      supplemental_files: [expect.objectContaining({ name: 'BasicClient.java.txt' })],
    }))
  })

  it.each([
    ['prose naming a class', 'notes.txt', 'Handover notes\nThe class Actions {see listing} uses lrapi helpers.'],
    ['a C enum', 'helper.txt', 'enum Status { OK, FAIL };'],
    ['a javascript class', 'app.txt', 'class Foo extends Bar {\n  run() {}\n}\n'],
    ['a css rule', 'style.txt', '.class Foo { color: red; }'],
    // Pins the client first-line gate: the quoted listing matches the
    // modifier-qualified declaration regex, so only the file-must-begin-like-
    // Java rule stops it. Because the client renames BEFORE upload and the
    // server trusts .java names without re-sniffing, a client-only miss here
    // smuggles prose past every server-side protection.
    ['a note quoting a class listing', 'handover.txt', 'Handover notes for the payments harness.\n\nThe runner class is shown below for reference:\n\npublic class Actions extends BasicClient {\n  public int action() { return 0; }\n}\n\nContact the perf team before changing it.'],
  ])('does not rename %s to .java', async (_label, fileName, content) => {
    // A bare `class <Ident> {` is not evidence of Java. Accepting it renamed
    // prose and data files, which on the backend polluted the immutable source
    // manifest the approval evidence binds to.
    mockBlockedJavaSuperclassValidation()

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', {
        configurable: true,
        value: [new File([content], fileName, { type: 'text/plain' })],
      })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    expect(validationCalls[validationCalls.length - 1]?.[1]).toEqual(expect.objectContaining({
      supplemental_files: [expect.objectContaining({ name: fileName })],
    }))
  })

  it('does not rename a .txt attachment on a non-Java import', async () => {
    // The rename must be scoped to Java imports, mirroring the server's
    // archiveHasJavaSourceSignal. Renaming unconditionally hands the server a
    // file already named .java, which by itself satisfies that server gate and
    // diverts a non-Java archive into the Java converter with its recorded
    // workload discarded.
    const httpDraft = {
      name: 'Recorded Web Flow',
      version: '1.0.0',
      protocol: 'http',
      actions: [{ name: 'GET home', type: 'http_request', target: 'https://example.com/' }],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue({
      script: httpDraft,
      scripts: [httpDraft],
      detected_protocol: {
        canonical_source_protocol: 'web_http_html',
        apexload_protocol: 'http',
        display_name: 'LoadRunner Web (HTTP/HTML)',
        import_mode: 'executable',
        confidence: 'high',
      },
      fidelity_report: { promotion_status: 'promotable', blockers: [] },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: { import_mode: 'executable' },
      stats: { total_functions: 1, converted_actions: 1, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
    })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'web-archive.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    // Content that WOULD be renamed inside a Java import.
    const note = new File(
      ['package com.acme;\n\npublic class Actions {\n  public int action() { return 0; }\n}\n'],
      'Actions.txt',
      { type: 'text/plain' },
    )
    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', { configurable: true, value: [note] })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    expect(validationCalls[validationCalls.length - 1]?.[1]).toEqual(expect.objectContaining({
      supplemental_files: [expect.objectContaining({ name: 'Actions.txt' })],
    }))
  })

  it('preserves the original bytes when renaming a .txt source', async () => {
    // The rename must change only the NAME. Rebuilding the upload from
    // file.text() re-encodes a UTF-8-with-replacement decode, silently
    // corrupting every non-UTF-8 byte — and windows-1252 was the default
    // encoding for Windows-authored LoadRunner Java sources (0xE9 for é).
    mockBlockedJavaSuperclassValidation()

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    // ASCII Java that passes the sniff, with one windows-1252 byte (0xE9, é)
    // inside a string literal.
    const ascii = 'public class BasicClient {\n  String label = "caf';
    const tail = '";\n}\n'
    const bytes = new Uint8Array(ascii.length + 1 + tail.length)
    for (let i = 0; i < ascii.length; i++) bytes[i] = ascii.charCodeAt(i)
    bytes[ascii.length] = 0xe9
    for (let i = 0; i < tail.length; i++) bytes[ascii.length + 1 + i] = tail.charCodeAt(i)

    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', {
        configurable: true,
        value: [new File([bytes], 'BasicClient.java.txt', { type: 'text/plain' })],
      })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    const sent = (validationCalls[validationCalls.length - 1]?.[1]?.supplemental_files as File[])?.[0]
    expect(sent?.name).toBe('BasicClient.java')
    const sentBytes = new Uint8Array(await sent.arrayBuffer())
    // The renamed upload must carry the raw 0xE9, not the UTF-8 replacement
    // sequence EF BF BD a text round-trip would produce.
    expect(sentBytes[ascii.length]).toBe(0xe9)
    expect(sentBytes.length).toBe(bytes.length)
  })

  it('still attaches the batch when one file cannot be read', async () => {
    // An unguarded Promise.all rejected the whole batch on a single unreadable
    // file: nothing was attached and no error was shown.
    mockBlockedJavaSuperclassValidation()

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    const unreadable = new File(['x'], 'Broken.java.txt', { type: 'text/plain' })
    Object.defineProperty(unreadable, 'text', {
      value: () => Promise.reject(new Error('NotReadableError')),
    })
    const good = new File(['public class BasicClient {}'], 'BasicClient.java', { type: 'text/plain' })

    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', { configurable: true, value: [unreadable, good] })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    const sent = validationCalls[validationCalls.length - 1]?.[1]?.supplemental_files as File[] | undefined
    // Both files survive; the unreadable one keeps its original name.
    expect(sent?.map(file => file.name).sort()).toEqual(['BasicClient.java', 'Broken.java.txt'])
  })

  it('leaves non-Java .txt attachments named as they were', async () => {
    mockBlockedJavaSuperclassValidation()

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    const parameterFile = new File(['account,region\n1001,east\n'], 'accounts.txt', { type: 'text/plain' })
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', { configurable: true, value: [parameterFile] })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    await clickImportAction(container, 'Validate Only')
    const validationCalls = apiMock.validateEnterpriseArchiveImport.mock.calls
    expect(validationCalls[validationCalls.length - 1]?.[1]).toEqual(expect.objectContaining({
      supplemental_files: [expect.objectContaining({ name: 'accounts.txt' })],
    }))
  })

  it('lets users attach required files from fidelity blockers and sends them on re-validation', async () => {
    const unresolvedSuperclassBlocker = 'Java runner class Action extends unresolved superclass BasicClient; upload the exact superclass source so construction and lifecycle semantics can be verified before promotion.'
    const blockedJavaDraft = {
      name: 'Blocked Combined Java Vuser',
      version: '1.0.0',
      protocol: 'java_vuser',
      actions: [{ name: 'Java action', type: 'java_vuser_run', params: { method: 'action' } }],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          preserved: {
            fidelity_report: {
              promotion_status: 'blocked',
              blockers: [unresolvedSuperclassBlocker],
            },
          },
        },
      },
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue({
      script: blockedJavaDraft,
      scripts: [blockedJavaDraft],
      detected_protocol: {
        canonical_source_protocol: 'java_vuser',
        apexload_protocol: 'java_vuser',
        display_name: 'LoadRunner Java Vuser',
        import_mode: 'external_runner_required',
        confidence: 'high',
        runner_requirement: 'java_vuser_jvm_runner',
      },
      review_artifact: {
        id: 'java-blocked-review',
        title: 'Blocked combined Java Vuser review',
        status: 'external_runner_required',
        protocol: {
          canonical_source_protocol: 'java_vuser',
          apexload_protocol: 'java_vuser',
          import_mode: 'external_runner_required',
          confidence: 'high',
          runner_requirement: 'java_vuser_jvm_runner',
        },
        summary: 'Resolve the selected lifecycle blocker.',
      },
      fidelity_report: {
        promotion_status: 'blocked',
        blockers: [unresolvedSuperclassBlocker],
      },
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      summary: {
        import_mode: 'external_runner_required',
        fidelity_report: {
          promotion_status: 'blocked',
          blockers: [unresolvedSuperclassBlocker],
        },
        migration_readiness: {
          score_pct: 50,
          blocked: false,
          summary: 'Review required',
          policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
        },
      },
      stats: { total_functions: 1, converted_actions: 1, extractors: 0, think_times: 0, transactions: 0, skipped: 0 },
    })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()

    const fileInput = container.querySelector('input[type="file"]') as HTMLInputElement
    const zipFile = new File(['zip-content'], 'blocked-java.zip', { type: 'application/zip' })
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: [zipFile] })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    expect(container.textContent).toContain('Required Files')
    expect(container.textContent).toContain('Required Files To Resolve Fidelity Blockers')
    expect(container.textContent).toContain('BasicClient (BasicClient.java) - missing')

    const supplementalInput = container.querySelector('input[accept*=".java"]') as HTMLInputElement
    const basicClientFile = new File(['public class BasicClient {}'], 'BasicClient.java', { type: 'text/plain' })
    await act(async () => {
      Object.defineProperty(supplementalInput, 'files', { configurable: true, value: [basicClientFile] })
      supplementalInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('BasicClient (BasicClient.java) - attached')
    expect(container.textContent).toContain('Review is stale; re-validate to refresh unresolved required files.')
    expect(container.textContent).not.toContain('No unresolved required files detected in the current review.')

    await clickImportAction(container, 'Validate Only')
    expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenLastCalledWith(zipFile, expect.objectContaining({
      supplemental_files: [basicClientFile],
    }))
    expect(container.textContent).toContain('BasicClient (BasicClient.java) - attached')
    expect(container.textContent).not.toContain('Review is stale; re-validate to refresh unresolved required files.')
  })

  it('shows lane-scoped replay preflight and falls back to the response envelope', async () => {
    const lanePreflight = {
      valid: false,
      action_count: 1,
      expected_action_count: 1,
      data_source_count: 0,
      parameter_count: 1,
      missing_variables: ['LANE_PARAMETER'],
      checks: [{ id: 'variables', label: 'Variables', status: 'fail', detail: 'LANE_PARAMETER' }],
    }
    const responsePreflight = {
      valid: false,
      action_count: 1,
      expected_action_count: 1,
      data_source_count: 0,
      parameter_count: 1,
      missing_variables: ['RESPONSE_PARAMETER'],
      checks: [{ id: 'variables', label: 'Variables', status: 'fail', detail: 'RESPONSE_PARAMETER' }],
    }
    const first = {
      name: 'HTTP lane',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: '' },
      actions: [{ name: 'Request', type: 'request', method: 'GET', target: '/' }],
      metadata: { import: { preserved: { replay_preflight: lanePreflight } } },
    }
    const second = {
      name: 'JMS lane',
      version: '1.0.0',
      protocol: 'jms',
      config: { base_url: '' },
      actions: [{ name: 'Publish', type: 'jms_publish', target: 'orders' }],
    }
    apiMock.validateEnterpriseArchiveImport.mockResolvedValueOnce({
      ...executableEnterpriseResult(first, { preflight: responsePreflight }),
      scripts: [first, second],
      script: first,
    })

    await act(async () => root.render(<EnterpriseArchiveImport />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['multi'], 'multi.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')
    expect(container.textContent).toContain('Missing variable: LANE_PARAMETER')
    expect(container.textContent).not.toContain('Missing variable: RESPONSE_PARAMETER')

    await clickButton(container.querySelector<HTMLButtonElement>('[aria-label="Review generated script JMS lane"]')!)
    expect(container.textContent).toContain('Missing variable: RESPONSE_PARAMETER')
    expect(container.textContent).not.toContain('Missing variable: LANE_PARAMETER')
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
        fixture_contract_status: { web2_spa_checkout: 'blocked', web2_runtime_parameters: 'supported' },
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('TruClient 2 Compatibility')
    expect(container.textContent).toContain('web2 spa checkout: Blocked')
    expect(container.textContent).toContain('web2 runtime parameters: Supported')
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(convertButton).toBeDefined()
    if (!convertButton) return

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    await clickButton(findButton(container, 'Source Mapping')!)
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 40)) })
    const copySource = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy mapped source'))
    const copyDsl = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Copy mapped destination'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Ready Scripts and Continue Importing'))
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
    expect(reviewSaveButton.textContent).toContain('Save Ready Scripts and Continue Importing')

    await clickButton(reviewSaveButton)
    await flushPromises()

    // Both archives are converted and saved — not just the last-added (gamma) one.
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)
    expect(apiMock.importEnterpriseArchive.mock.calls.map(call => (call[0] as File).name)).toEqual(['alpha.zip', 'gamma.zip'])
    expect(apiMock.createScript).toHaveBeenCalledTimes(2)
    expect(onImported).toHaveBeenCalledTimes(1)
    expect(onImported.mock.calls[0][0].scripts).toHaveLength(2)
  })

  it('reconciles an ambiguous atomic batch save with the same operation id', async () => {
    const files = [
      new File(['alpha'], 'alpha.zip', { type: 'application/zip' }),
      new File(['gamma'], 'gamma.zip', { type: 'application/zip' }),
    ]
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => executableEnterpriseResult({
      name: file.name.replace(/\.zip$/i, ''),
      version: '1.0.0',
      protocol: 'http',
      actions: [{ name: 'Homepage', type: 'request', method: 'GET', target: '/' }],
      metadata: {
        import: {
          source: 'enterprise_script_archive',
          preserved: { server_import_attestation_v1: `attestation-${file.name}` },
        },
      },
    }))
    apiMock.saveEnterpriseArchiveImport
      .mockRejectedValueOnce(new ApiError('response interrupted', 500))
      .mockRejectedValueOnce(new ApiError('receipt still finalizing', 500))
      .mockResolvedValueOnce({
        scripts: files.map((file, index) => ({
          id: `saved-${index}`,
          name: file.name.replace(/\.zip$/i, ''),
          protocol: 'http',
          version: '1.0.0',
          created_at: '2026-05-17T00:00:00Z',
        })),
        count: 2,
      })

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', { configurable: true, value: files })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    await flushPromises()

    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(2)
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)
    const firstOperationID = apiMock.saveEnterpriseArchiveImport.mock.calls[0][0].operation_id
    expect(firstOperationID).toMatch(/^enterprise-archive-/)
    expect(apiMock.saveEnterpriseArchiveImport.mock.calls[1][0].operation_id).toBe(firstOperationID)
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Choose Convert again to reconcile the same operation')

    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    await flushPromises()

    expect(apiMock.saveEnterpriseArchiveImport).toHaveBeenCalledTimes(3)
    expect(apiMock.saveEnterpriseArchiveImport.mock.calls[2][0].operation_id).toBe(firstOperationID)
    expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(2)
    expect(apiMock.saveEnterpriseArchiveImport.mock.calls[2][0].scripts)
      .toBe(apiMock.saveEnterpriseArchiveImport.mock.calls[0][0].scripts)
    expect(container.textContent).toContain('Converted and saved 2 scripts to the library')
  })

  it('keeps a mixed ready-and-review queue mounted while refreshing saved ready scripts', async () => {
    const onImported = vi.fn()
    const readyScript = {
      name: 'Ready Lane',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://ready.example.test' },
      actions: [{ name: 'Ready', type: 'request', method: 'GET', target: '/' }],
    }
    const reviewedScript = {
      name: 'Reviewed Lane',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://review.example.test' },
      actions: [{ name: 'Review', type: 'request', method: 'POST', target: '/review' }],
    }
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => (
      file.name === 'ready.zip'
        ? executableEnterpriseResult(readyScript)
        : executableEnterpriseResult(reviewedScript, {
            parameterization: {
              suggestions: [{
                id: 'review-user',
                kind: 'data_param',
                var_name: 'USER',
                replacement: '{{var.USER}}',
                confidence: 0.8,
                rule_id: 'test',
                rationale: 'Review this parameter.',
                auto_apply: false,
                high_risk: false,
                pre_applied: false,
              }],
              summary: {
                total: 1,
                by_kind: { data_param: 1 },
                auto_apply_count: 0,
                review_count: 1,
                high_risk_count: 0,
                pre_applied_count: 0,
              },
            },
          })
    ))

    await act(async () => root.render(<EnterpriseArchiveImport onImported={onImported} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['ready'], 'ready.zip', { type: 'application/zip' }),
          new File(['review'], 'review.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledTimes(1)
    expect(onImported).toHaveBeenNthCalledWith(1, expect.objectContaining({
      scripts: [expect.objectContaining({ name: readyScript.name })],
      stayInImportReview: true,
    }))
    expect(container.textContent).toContain('2 ZIP files queued')
    await clickButton(findButton(container, 'DSL Preview')!)
    expect(container.querySelector('.script-import-review-preview pre')?.textContent).toContain(reviewedScript.name)
    expect(findButton(container, 'Save and Continue Importing')).toBeDefined()

    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledTimes(2)
    expect(onImported).toHaveBeenCalledTimes(2)
    expect(onImported.mock.calls[1][0]).toEqual(expect.objectContaining({
      scripts: [
        expect.objectContaining({ name: readyScript.name }),
        expect.objectContaining({ name: reviewedScript.name }),
      ],
    }))
    expect(onImported.mock.calls[1][0].stayInImportReview).toBe(true)
  })

  it.each(['none', 'validate', 'policy', 'converted', 'failed-refresh', 'changed-lanes'])('tracks saved lanes without duplicates after %s refresh', async (refresh) => {
    const onImported = vi.fn()
    const first = {
      name: 'Generated Lane A',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://multi.example.test' },
      actions: [{ name: 'A', type: 'request', method: 'GET', target: '/a' }],
    }
    const second = {
      name: 'Generated Lane B',
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: 'https://multi.example.test' },
      actions: [{ name: 'B', type: 'request', method: 'GET', target: '/b' }],
    }
    apiMock.importEnterpriseArchive.mockResolvedValueOnce({
      ...executableEnterpriseResult(first, {
        parameterization: {
          suggestions: [{
            id: 'review-lanes',
            kind: 'data_param',
            var_name: 'LANE',
            replacement: '{{var.LANE}}',
            confidence: 0.8,
            rule_id: 'test',
            rationale: 'Review the generated lanes.',
            auto_apply: false,
            high_risk: false,
            pre_applied: false,
          }],
          summary: {
            total: 1,
            by_kind: { data_param: 1 },
            auto_apply_count: 0,
            review_count: 1,
            high_risk_count: 0,
            pre_applied_count: 0,
          },
        },
      }),
      script: first,
      scripts: [first, second],
    })

    await act(async () => root.render(<EnterpriseArchiveImport onImported={onImported} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['multi'], 'multi.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    await clickImportAction(container, 'Validate Only')
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    if (refresh === 'converted') {
      await clickImportAction(container, 'Validate Only')
      expect(apiMock.validateEnterpriseArchiveImport).toHaveBeenCalledTimes(2)
      apiMock.importEnterpriseArchive.mockResolvedValueOnce(await apiMock.importEnterpriseArchive.mock.results[0].value)
      await clickButton(findButton(container, 'Save and Continue Importing')!)
    }
    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(onImported).toHaveBeenNthCalledWith(1, expect.objectContaining({
      scripts: [expect.objectContaining({ name: first.name })],
      stayInImportReview: true,
    }))
    await clickButton(findButton(container, 'DSL Preview')!)
    expect(container.querySelector('.script-import-review-preview pre')?.textContent).toContain(second.name)
    expect(container.textContent).toContain('Generated Lane A')
    expect(container.textContent).toContain('Saved')

    if (refresh !== 'none' && refresh !== 'converted') {
      if (refresh === 'failed-refresh' || refresh === 'changed-lanes') {
        if (refresh === 'failed-refresh') apiMock.importEnterpriseArchive.mockRejectedValueOnce(new Error('Refresh temporarily unavailable'))
        else apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult(first))
        await clickImportAction(container, 'Validate Only')
        expect(container.textContent).toContain(refresh === 'failed-refresh' ? 'Refresh temporarily unavailable' : 'The generated script lanes changed')
        expect(apiMock.createScript).toHaveBeenCalledTimes(1)
      }
      apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult(first, { scripts: [first, second] }))
      if (refresh === 'policy') {
        await act(async () => container.querySelector<HTMLInputElement>('.lr-advanced-policy input')!.click())
      }
      await clickImportAction(container, 'Validate Only')
      await flushPromises()
      expect(apiMock.importEnterpriseArchive).toHaveBeenCalledTimes(refresh === 'failed-refresh' || refresh === 'changed-lanes' ? 3 : 2)
      expect(apiMock.createScript).toHaveBeenCalledTimes(1)
      expect(container.textContent).toContain('Generated Lane A')
    }

    await clickButton(findButton(container, 'Save and Continue Importing')!)
    await flushPromises()

    expect(apiMock.createScript).toHaveBeenCalledTimes(2)
    expect(onImported.mock.calls[1][0].scripts).toEqual([
      expect.objectContaining({ name: first.name }),
      expect.objectContaining({ name: second.name }),
    ])
    expect(onImported.mock.calls[1][0].stayInImportReview).toBe(true)
  })

  it('discards a protected source session when its nonselected queue item is removed', async () => {
    const firstFile = new File(['alpha'], 'alpha.zip', { type: 'application/zip' })
    const secondFile = new File(['beta'], 'beta.zip', { type: 'application/zip' })
    apiMock.validateEnterpriseArchiveImport.mockImplementation(async (file: File) => (
      executableEnterpriseResult({
        name: file.name,
        version: '1.0.0',
        protocol: 'http',
        config: { base_url: '' },
        actions: [{ name: 'Request', type: 'request', method: 'GET', target: '/' }],
      }, {
        source_secret_session_id: `${file.name}-source-session`,
        source_secret_session_expires_at: '2099-01-01T00:00:00Z',
      })
    ))

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [firstFile, secondFile],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate Only')

    expect(container.querySelector('.lr-file-row.active')?.textContent).toContain(firstFile.name)
    await clickButton(container.querySelector<HTMLButtonElement>('[aria-label="Remove beta.zip"]')!)
    await flushPromises()

    expect(apiMock.discardImportSecretSession).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'beta.zip-source-session',
    })
    expect(container.textContent).toContain(firstFile.name)
    expect(container.textContent).not.toContain(secondFile.name)
  })

  it('discards only the selected archive source session when opening its draft in the editor', async () => {
    const onEdit = vi.fn()
    const resultFor = (file: File) => executableEnterpriseResult({
      name: file.name,
      version: '1.0.0',
      protocol: 'http',
      config: { base_url: '' },
      actions: [{ name: 'Request', type: 'request', method: 'GET', target: '/' }],
    }, {
      source_secret_session_id: `${file.name}-source-session`,
      source_secret_session_expires_at: '2099-01-01T00:00:00Z',
    })
    apiMock.validateEnterpriseArchiveImport.mockImplementation(async (file: File) => resultFor(file))
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => resultFor(file))

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" onEdit={onEdit} />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['alpha'], 'alpha.zip', { type: 'application/zip' }),
          new File(['beta'], 'beta.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate 2 Scripts')
    await flushPromises()
    apiMock.discardImportSecretSession.mockClear()

    await clickButton(findButton(container, 'Open Draft in Visual Editor')!)
    await flushPromises()

    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({ name: 'alpha.zip' }), undefined)
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledTimes(1)
    expect(apiMock.discardImportSecretSession).toHaveBeenCalledWith({
      project_id: 'project-1',
      source_secret_session_id: 'alpha.zip-source-session',
    })
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save Ready Scripts and Continue Importing'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.querySelector('.lr-import-progress')?.textContent).toContain('Processing 2 reviewed archives')
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

    expect(container.querySelector('.script-import-review-save-disabled-reason')?.textContent).toContain('must pass validation')
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
              password: 'imported-sap-password',
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
            password: 'imported-sap-password',
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
      secret_binding_requirements: [{
        id: 'sapgui-password',
        path: '/config/sapgui/password',
        label: 'SAP GUI password',
        context: 'External-runner credential',
        suggested_name: 'SAPGUI_PASSWORD',
        script_index: 0,
        script_name: 'SAP GUI Draft',
        destination_value_preserved: true,
      }],
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }
    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()
    await flushPromises()

    expect(container.textContent).toContain('Review')
    expect(container.textContent).toContain('SAP GUI')
    expect(container.textContent).toContain('External Runner Required')
    expect(container.textContent).toContain('external_runner_required')
    expect(container.textContent).toContain('Runner: windows_sap_gui_runner')
    expect(container.textContent).toContain('SAP GUI review artifact')
    expect(container.textContent).toContain('sapgui_set')
    await clickButton(findButton(container, 'Source Mapping')!)
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 40)) })
    expect(container.textContent).toContain('external required')
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))).toBeUndefined()

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
        config: expect.objectContaining({
          sapgui: expect.objectContaining({ password: 'imported-sap-password' }),
        }),
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

    const checkboxes = [
      'Add default status_code assertions', 'Preserve transaction names in generated actions',
      'Build Salesforce native preview', 'Treat partial runtime settings as blockers',
      'Treat preserved-only runtime settings as blockers', 'Treat non-applicable runtime settings as blockers',
    ].map(text => Array.from(container.querySelectorAll('label')).find(label => label.textContent?.trim() === text)?.querySelector('input') as HTMLInputElement)
    expect(checkboxes).toHaveLength(6)
    await act(async () => {
      checkboxes[1].dispatchEvent(new MouseEvent('click', { bubbles: true }))
      checkboxes[3].dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    const validateButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Validate Script(s)'))
    expect(validateButton).toBeDefined()
    expect(container.querySelector('[aria-label="enterprise protocol import coverage"]')).not.toBeNull()
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
      migration_block_statuses: ['blocked', 'partial'],
    })
    expect(container.textContent).toContain('Validation Review')
    const validationWarningDetails = container.querySelector<HTMLButtonElement>('button[aria-label="View 1 import warning detail"]')
    expect(validationWarningDetails).not.toBeNull()
    await clickButton(validationWarningDetails!)
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('validation review')
    await clickButton(Array.from(document.body.querySelectorAll<HTMLButtonElement>('[role="dialog"] button'))
      .find(button => button.textContent?.trim() === 'Close')!)
    expect(container.textContent).toContain('Unsupported Source Functions (1)')
    expect(container.querySelector('[aria-label="enterprise protocol import coverage"]')).toBeNull()

    const reviewTabs = Array.from(container.querySelectorAll<HTMLButtonElement>('[role="tablist"][aria-label="Script archive review sections"] [role="tab"]'))
    expect(reviewTabs.map(tab => tab.textContent)).toEqual([
      'Review',
      'Preserved Source Settings',
      'DSL Preview',
      'Source Mapping',
    ])
    expect(reviewTabs[0]?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector<HTMLElement>('.lr-validation-review-overview')?.hidden).toBe(false)
    expect(container.querySelector<HTMLElement>('.lr-validation-review-preserved')?.hidden).toBe(true)
    expect(container.querySelector('.script-import-review-preview')).toBeNull()
    expect(container.querySelector('.script-import-review-preview-aside')).toBeNull()

    await clickButton(findButton(container, 'Preserved Source Settings')!)
    expect(reviewTabs[1]?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector<HTMLElement>('.lr-validation-review-overview')?.hidden).toBe(true)
    expect(container.querySelector<HTMLElement>('.lr-validation-review-preserved')?.hidden).toBe(false)
    expect(container.textContent).toContain('Preserved Source Settings')

    await clickButton(findButton(container, 'DSL Preview')!)
    expect(reviewTabs[2]?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.script-import-review-preview summary h4')?.textContent).toBe('DSL Preview')
    expect(container.querySelector('.script-import-review-preview-aside')).toBeNull()

    await clickButton(findButton(container, 'Source Mapping')!)
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 40)) })
    expect(reviewTabs[3]?.getAttribute('aria-selected')).toBe('true')
    expect(container.querySelector('.script-import-review-preview')).toBeNull()
    expect(container.querySelector('.script-import-review-preview-aside .source-map-review')).not.toBeNull()
  })

  it('keeps the nested Salesforce native preview review-only until it is separately attested', async () => {
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
          body: 'password=[REDACTED]',
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

    const checkboxes = [
      'Add default status_code assertions', 'Preserve transaction names in generated actions',
      'Build Salesforce native preview', 'Treat partial runtime settings as blockers',
      'Treat preserved-only runtime settings as blockers', 'Treat non-applicable runtime settings as blockers',
    ].map(text => Array.from(container.querySelectorAll('label')).find(label => label.textContent?.trim() === text)?.querySelector('input') as HTMLInputElement)
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
    expect(container.textContent).toContain('Review Only')
    expect(container.textContent).toContain('Salesforce Review')
    expect(container.textContent).toContain('Logical Actions')
    expect(container.textContent).toContain('Raw Requests')
    expect(container.textContent).toContain('Collapsed')
    expect(container.textContent).toContain('LR Markers')

    const openNativePreview = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Review Native Preview Limitation'))
    expect(openNativePreview).toBeDefined()
    if (!openNativePreview) {
      return
    }
    await act(async () => {
      openNativePreview.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onEdit).not.toHaveBeenCalled()
    expect(container.textContent).toContain('separately attested import candidate')
    expect(container.textContent).toContain('without re-entry')
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }

    await act(async () => {
      convertButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    const saveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing')) as HTMLButtonElement | undefined
    expect(saveButton).toBeDefined()
    if (!saveButton) {
      return
    }

    expect(saveButton.disabled).toBe(true)
    expect(container.textContent).toContain('Saving is disabled until the migration readiness blockers are resolved or the Migration Block Policy in Conversion Options is relaxed.')
    expect(container.textContent).toContain('Migration Block Policy')
    expect(container.textContent).toContain('These settings control which migration-readiness results prevent saving.')

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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    await act(async () => {
      container.querySelectorAll<HTMLInputElement>('.lr-advanced-policy input')[0].click()
      container.querySelectorAll<HTMLInputElement>('.lr-advanced-policy input')[1].click()
    })
    await clickImportAction(container, 'Validate Only')

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const checkboxes = [
      'Add default status_code assertions', 'Preserve transaction names in generated actions',
      'Build Salesforce native preview', 'Treat partial runtime settings as blockers',
      'Treat preserved-only runtime settings as blockers', 'Treat non-applicable runtime settings as blockers',
    ].map(text => Array.from(container.querySelectorAll('label')).find(label => label.textContent?.trim() === text)?.querySelector('input') as HTMLInputElement)
    expect(checkboxes[3]?.checked).toBe(false)
    expect(container.textContent).toContain('Selected migration block policy: Blocked, Compatibility Only')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked, Compatibility Only')
    expect(container.textContent).toContain('Conversion refreshed with migration block policy: Blocked, Compatibility Only.')

    const restorePolicyButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Restore Default Policy'))
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
        migration_block_statuses: ['blocked'],
      },
    ])
    expect(checkboxes[3]?.checked).toBe(false)
    expect(container.textContent).toContain('Selected migration block policy: Blocked')
    expect(container.textContent).toContain('Reviewed migration block policy: Blocked')
    expect(container.textContent).toContain('Conversion refreshed with migration block policy: Blocked.')
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

    expect(container.textContent).toContain('Selected migration block policy: Blocked')

    const checkboxes = [
      'Add default status_code assertions', 'Preserve transaction names in generated actions',
      'Build Salesforce native preview', 'Treat partial runtime settings as blockers',
      'Treat preserved-only runtime settings as blockers', 'Treat non-applicable runtime settings as blockers',
    ].map(text => Array.from(container.querySelectorAll('label')).find(label => label.textContent?.trim() === text)?.querySelector('input') as HTMLInputElement)
    await act(async () => {
      checkboxes[3]?.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(container.textContent).toContain('Selected migration block policy: Blocked, Partial')
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

  it('never reaches the batch save loop with a browser import', async () => {
    apiMock.importEnterpriseArchive.mockResolvedValueOnce(executableEnterpriseResult({
      name: 'Imported TruClient Web',
      version: '1.0',
      protocol: 'browser',
      config: {
        base_url: 'https://shop.example.test',
        browser: { engine: 'chromium', headless: true },
      },
      actions: [{ name: 'Open Store', type: 'browser_navigate', url: '/store' }],
      metadata: {
        import: {
          source: 'trueclient_web',
          source_script_name: 'default.xml',
          preserved: {},
        },
      },
    }))

    await act(async () => {
      root.render(<EnterpriseArchiveImport projectId="project-1" />)
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
    await flushPromises()

    const convertButton = findButton(container, 'Save and Continue Importing')
    expect(convertButton).toBeDefined()
    if (!convertButton) {
      return
    }
    await clickButton(convertButton)
    await flushPromises()

    // The browser-replay gate holds the archive in review, so the conversion
    // batch's save loop is never entered for a browser-protocol script. Any
    // browser-only handling inside that loop would be dead code.
    expect(apiMock.createScript).not.toHaveBeenCalled()
    expect(container.textContent).toContain('Run draft browser replay before saving this browser script.')
    expect(container.textContent).toContain('Draft Browser Replay Validation')
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const assertionOption = Array.from(container.querySelectorAll('label')).find(label => label.textContent?.includes('Add default status_code assertions'))
      ?.querySelector('input') as HTMLInputElement | undefined
    expect(assertionOption).toBeDefined()
    if (!assertionOption) {
      return
    }
    await act(async () => {
      assertionOption.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      validateReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(validateReplayButton.disabled).toBe(true)
    expect(apiMock.startDraftBrowserReplaySession).not.toHaveBeenCalled()

    await act(async () => {
      assertionOption.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(validateReplayButton.disabled).toBe(false)

    act(() => {
      validateReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
      validateReplayButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    await flushPromises()

    expect(apiMock.startDraftBrowserReplaySession).toHaveBeenCalledTimes(1)
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

  it('clears replay-start loading on its source row after the operator switches rows', async () => {
    const resultFor = (file: File) => executableEnterpriseResult({
      name: file.name,
      version: '1.0.0',
      protocol: 'browser',
      config: { base_url: 'https://shop.example.test', browser: { engine: 'chromium', headless: true } },
      actions: [{ name: 'Open', type: 'browser_navigate', target: '/' }],
    })
    apiMock.validateEnterpriseArchiveImport.mockImplementation(async (file: File) => resultFor(file))
    apiMock.importEnterpriseArchive.mockImplementation(async (file: File) => resultFor(file))
    let resolveStart!: (value: unknown) => void
    apiMock.startDraftBrowserReplaySession.mockReturnValueOnce(new Promise(resolve => { resolveStart = resolve }))

    await act(async () => root.render(<EnterpriseArchiveImport projectId="project-1" />))
    await flushPromises()
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [
          new File(['alpha'], 'alpha-browser.zip', { type: 'application/zip' }),
          new File(['beta'], 'beta-browser.zip', { type: 'application/zip' }),
        ],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })
    await clickImportAction(container, 'Validate 2 Scripts')
    await clickButton(findButton(container, 'Save Ready Scripts and Continue Importing')!)
    await clickButton(findButton(container, 'Start Draft Browser Replay')!)
    expect(findButton(container, 'Starting Draft Browser Replay...')).toBeDefined()

    const betaRow = Array.from(container.querySelectorAll<HTMLElement>('.lr-file-row'))
      .find(row => row.textContent?.includes('beta-browser.zip'))!
    await act(async () => betaRow.click())
    await act(async () => {
      resolveStart({
        script_id: '',
        replay_session_id: 'alpha-replay',
        session: { id: 'alpha-replay', status: 'running', group: 'alpha', protocol: 'browser', current_event_index: 0 },
        warnings: [],
      })
      await Promise.resolve()
    })

    const alphaRow = Array.from(container.querySelectorAll<HTMLElement>('.lr-file-row'))
      .find(row => row.textContent?.includes('alpha-browser.zip'))!
    await act(async () => alphaRow.click())
    await flushPromises()

    expect(findButton(container, 'Starting Draft Browser Replay...')).toBeUndefined()
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

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

      const finalSaveButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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
      }), 'project-1', undefined)
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

  it('fails a permanently missing draft replay after a bounded reconciliation window', async () => {
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
    apiMock.getBrowserReplaySession.mockRejectedValue(new ApiError('not found', 404))

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

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

      for (let attempt = 0; attempt < 5; attempt += 1) {
        await act(async () => {
          vi.advanceTimersByTime(2000)
        })
        await flushPromises()
      }

      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledTimes(5)
      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledWith('draft-browser-validation')
      expect(container.textContent).toContain('Replay session could not be confirmed')
      expect(container.textContent).toContain('Replay Status: Failed')
      expect(container.textContent).toContain('Rerun Draft Browser Replay Before Saving')
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
    apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(
      Object.assign(new Error('Draft browser replay failed to start'), { status: 422 }),
    )

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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    vi.useFakeTimers()
    try {
      apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(new Error('connection reset'))
      apiMock.getBrowserReplaySession.mockImplementationOnce(async (replaySessionId: string) => ({
        session: {
          id: replaySessionId,
          status: 'completed',
          group: 'Imported TruClient Mobile Web-replay',
          protocol: 'browser',
          current_event_index: 0,
          completed_at: '2026-07-23T12:00:00Z',
          events: [],
        },
        warnings: [],
      }))

      const retryButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
        .find(button => button.textContent?.includes('Start Draft Browser Replay'))
      expect(retryButton).toBeDefined()
      await act(async () => {
        retryButton?.click()
      })
      await flushPromises()

      const replayCalls = apiMock.startDraftBrowserReplaySession.mock.calls
      const payload = replayCalls[replayCalls.length - 1]?.[0]
      expect(payload.replay_session_id).toMatch(/^[0-9a-f-]{36}$/)
      expect(container.textContent).toContain(`Replay Session: ${payload.replay_session_id}`)
      expect(container.textContent).toContain('Confirming the accepted replay session')

      await act(async () => {
        vi.advanceTimersByTime(2000)
      })
      await flushPromises()

      expect(apiMock.getBrowserReplaySession).toHaveBeenCalledWith(payload.replay_session_id)
      expect(container.textContent).toContain('Replay Status: Completed')
    } finally {
      vi.useRealTimers()
    }
  })

  it('identifies required browser replay in the validation action', async () => {
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

    const convertValidationButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Continue to browser replay'))
    expect(convertValidationButton).toBeDefined()
    expect(Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Start Draft Browser Replay Before Saving'))).toBeUndefined()
    expect(container.textContent).toContain('Continue to conversion and required draft browser replay before saving.')
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

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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
    apiMock.startDraftBrowserReplaySession.mockRejectedValueOnce(
      Object.assign(new Error('Draft browser replay failed to start'), { status: 422 }),
    )
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

      const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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

    const convertButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Save and Continue Importing'))
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
