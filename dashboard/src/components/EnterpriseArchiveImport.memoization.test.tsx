// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import EnterpriseArchiveImport from './EnterpriseArchiveImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const derivationCounts = vi.hoisted(() => ({ importSummary: 0, inlineData: 0 }))

vi.mock('./scriptEditorImportModel', async () => {
  const actual = await vi.importActual<typeof import('./scriptEditorImportModel')>('./scriptEditorImportModel')
  return {
    ...actual,
    getImportSummary: (...args: Parameters<typeof actual.getImportSummary>) => {
      derivationCounts.importSummary += 1
      return actual.getImportSummary(...args)
    },
  }
})

vi.mock('./scriptEditorUtils', async () => {
  const actual = await vi.importActual<typeof import('./scriptEditorUtils')>('./scriptEditorUtils')
  return {
    ...actual,
    summarizeInlineData: (...args: Parameters<typeof actual.summarizeInlineData>) => {
      derivationCounts.inlineData += 1
      return actual.summarizeInlineData(...args)
    },
  }
})

const apiMock = vi.hoisted(() => ({
  getEnterpriseArchiveSupportMatrix: vi.fn(),
  importEnterpriseArchive: vi.fn(),
  validateEnterpriseArchiveImport: vi.fn(),
  createScript: vi.fn(),
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
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

// CodeMirror can't measure layout under jsdom; the real editor is verified in-browser.
vi.mock('./LinkedCodePane', () => ({
  default: ({ value, ariaLabel }: { value: string; ariaLabel?: string }) => (
    <pre aria-label={ariaLabel}>{value}</pre>
  ),
}))

const generatedScript = {
  name: 'Memoized Enterprise Script Archive',
  version: '1.0.0',
  protocol: 'http',
  config: { base_url: 'https://example.test' },
  actions: [
    { name: 'Homepage', type: 'request', method: 'GET', target: '/' },
    { name: 'Submit Order', type: 'request', method: 'POST', target: '/orders' },
  ],
  data_sources: [
    { name: 'users', type: 'csv', first_row_header: true, inline_data: 'username\nalice\nbob' },
    { name: 'orders', type: 'csv', first_row_header: true, inline_data: 'order_id\n1\n2' },
  ],
  metadata: {
    import: {
      source: 'enterprise_script_archive',
      source_script_name: 'Memo.usr',
      preserved: {
        runtime_files: { 'default.cfg': {} },
        parameter_files: ['LoginUsers.prm'],
        parameter_file_sections: {
          'LoginUsers.prm': {
            LoginUser: {
              Type: 'File',
              Table: 'users',
              ColumnName: 'username',
            },
          },
        },
        correlation_suggestions: [
          { name: 'SessionId', source: 'web_reg_save_param', found_at: 'Step 1', used_at: ['Step 2'] },
          { name: 'OrderId', source: 'web_reg_save_param', found_at: 'Step 2', used_at: ['Step 2'] },
        ],
        project_settings: { General: { Type: 'Web' } },
        unsupported_functions: [],
      },
    },
  },
}

function enterpriseResult() {
  return {
    scripts: [generatedScript],
    script: generatedScript,
    warnings: [],
    unsupported_elements: [],
    assumptions: [],
    review_sections: [],
    repair_suggestions: [],
    summary: {
      project_file: 'Memo.usr',
      unsupported_function_count: 0,
      migration_readiness: {
        score_pct: 100,
        blocked: false,
        summary: 'No migration blockers.',
        policy: { block_statuses: ['blocked', 'partial', 'compatibility_only'] },
      },
    },
    stats: {
      total_functions: 2,
      converted_actions: 2,
      extractors: 0,
      think_times: 0,
      transactions: 0,
      skipped: 0,
    },
  }
}

async function flushPromises() {
  await act(async () => {
    for (let index = 0; index < 8; index += 1) {
      await Promise.resolve()
    }
  })
}

function findButton(root: HTMLElement, label: string): HTMLButtonElement | undefined {
  return Array.from(root.querySelectorAll('button')).find(button => button.textContent?.includes(label)) as HTMLButtonElement | undefined
}

async function clickButton(button: HTMLButtonElement) {
  await act(async () => {
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }))
  })
  await flushPromises()
}

describe('EnterpriseArchiveImport review derivations', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    vi.clearAllMocks()
    derivationCounts.importSummary = 0
    derivationCounts.inlineData = 0
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)

    apiMock.getEnterpriseArchiveSupportMatrix.mockResolvedValue({
      support_matrix: [],
      notes: [],
      tested_examples: [],
      protocol_support_matrix: [],
    })
    apiMock.validateEnterpriseArchiveImport.mockResolvedValue(enterpriseResult())
    apiMock.importEnterpriseArchive.mockResolvedValue(enterpriseResult())
    apiMock.discardImportSecretSession.mockResolvedValue(undefined)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('does not re-normalize the import summary or re-parse inline data on an unrelated re-render', async () => {
    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })
    const fileInput = container.querySelector<HTMLInputElement>('input[type="file"][accept=".zip"]')!
    await act(async () => {
      Object.defineProperty(fileInput, 'files', {
        configurable: true,
        value: [new File(['zip'], 'memoized.zip', { type: 'application/zip' })],
      })
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const validateButton = findButton(container, 'Validate Only') || findButton(container, 'Validate')
    expect(validateButton).toBeDefined()
    await clickButton(validateButton!)
    await clickButton(findButton(container, 'Save and Continue Importing')!)

    expect(container.textContent).toContain('Review')
    const summaryCallsAfterConversion = derivationCounts.importSummary
    const inlineDataCallsAfterConversion = derivationCounts.inlineData
    expect(summaryCallsAfterConversion).toBeGreaterThan(0)
    expect(inlineDataCallsAfterConversion).toBeGreaterThan(0)

    // A render that leaves the imported script alone (a keystroke elsewhere in the
    // review surface looks exactly like this to the component body) must reuse the
    // derived summary and data-source rows instead of rebuilding them.
    await act(async () => {
      root.render(<EnterpriseArchiveImport />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Review')
    expect(derivationCounts.importSummary).toBe(summaryCallsAfterConversion)
    expect(derivationCounts.inlineData).toBe(inlineDataCallsAfterConversion)
  })
})
