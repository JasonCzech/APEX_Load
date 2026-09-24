// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import OracleEBusinessReviewImport from './OracleEBusinessReviewImport'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = {
  reviewOracleEBusiness: vi.fn(),
  getOracleEBusinessSupportMatrix: vi.fn().mockResolvedValue({ notes: [] }),
}

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

describe('OracleEBusinessReviewImport', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getOracleEBusinessSupportMatrix.mockResolvedValue({
      notes: ['Oracle E-Business and Siebel imports execute over HTTP and require enterprise correlation review.'],
    })
    apiMock.reviewOracleEBusiness.mockResolvedValue({
      name: 'Checkout Review',
      package: 'oracle_web',
      status: 'review_required',
      source_summary: { files: 1, bytes: 1200 },
      execution_posture: { posture: 'review_required' },
      review_sections: [
        { key: 'oracle_signals', title: 'Oracle E-Business Signals', severity: 'warning', summary: 'ICX session identifiers detected.', items: ['icx_ticket correlation candidate'] },
      ],
      next_actions: ['Confirm Oracle E-Business runtime settings before saving reusable assets.'],
      repair_suggestions: [],
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    vi.clearAllMocks()
  })

  it('runs a review-only pass and never offers a save-to-library action', async () => {
    await act(async () => {
      root.render(<OracleEBusinessReviewImport projectId="project-1" />)
    })
    await flushPromises()

    expect(container.textContent).toContain('Oracle E-Business / Siebel Review')

    const textarea = container.querySelector('textarea') as HTMLTextAreaElement | null
    expect(textarea).not.toBeNull()
    if (!textarea) return
    await act(async () => {
      setTextareaValue(textarea, 'GET /OA_HTML/OA.jsp?page=checkout')
    })

    const runButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Run Review'))
    expect(runButton).toBeDefined()
    if (!runButton) return
    await act(async () => {
      runButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(apiMock.reviewOracleEBusiness).toHaveBeenCalledWith(expect.objectContaining({
      project_id: 'project-1',
      source_text: 'GET /OA_HTML/OA.jsp?page=checkout',
    }))
    expect(container.textContent).toContain('Checkout Review')
    expect(container.querySelector('.shared-import-issue-workspace')?.textContent).toContain('Oracle E-Business Signals')
    expect(container.querySelector('.shared-import-issue-workspace')?.textContent).toContain('ICX session identifiers detected.')
    expect(container.querySelector('.shared-import-issue-workspace')?.textContent).toContain('icx_ticket correlation candidate')
    expect(container.textContent).toContain('Confirm Oracle E-Business runtime settings before saving reusable assets.')
    expect(container.querySelector<HTMLDetailsElement>('.import-source-setup')?.open).toBe(false)
    expect(Array.from(container.querySelectorAll('[role="tab"]')).map(tab => tab.getAttribute('aria-label')))
      .toEqual(['Review', 'Data & parameters', 'Runtime settings', 'Source Mapping'])

    // Review-only surface: there must be no save / save-to-library control.
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => /save/i.test(button.textContent || ''))
    expect(saveButton).toBeUndefined()
  })

  it('requires source and invalidates findings whenever the review inputs change', async () => {
    await act(async () => root.render(<OracleEBusinessReviewImport projectId="project-1" />))
    const run = () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Run Review')!
    expect(run().disabled).toBe(true)
    const source = container.querySelector('textarea')!
    await act(async () => setTextareaValue(source, 'Oracle source'))
    await act(async () => run().click())
    expect(container.textContent).toContain('Checkout Review')
    await act(async () => setTextareaValue(source, 'Different source'))
    expect(container.textContent).not.toContain('Checkout Review')
    await act(async () => run().click())
    const pkg = container.querySelector('select')!
    await act(async () => {
      pkg.value = 'siebel_web'
      pkg.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(container.textContent).not.toContain('Checkout Review')
    await act(async () => run().click())
    const name = container.querySelector('input')!
    await act(async () => {
      Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!.call(name, 'Changed name')
      name.dispatchEvent(new Event('input', { bubbles: true }))
    })
    expect(container.textContent).not.toContain('Checkout Review')
  })

  it('locks inputs while reviewing and ignores a result from the previous project', async () => {
    let resolve!: (value: unknown) => void
    apiMock.reviewOracleEBusiness.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await act(async () => root.render(<OracleEBusinessReviewImport projectId="project-1" />))
    await act(async () => setTextareaValue(container.querySelector('textarea')!, 'Source A'))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Run Review')!.click())
    expect(container.querySelector('fieldset')?.disabled).toBe(true)
    await act(async () => root.render(<OracleEBusinessReviewImport projectId="project-2" />))
    await act(async () => resolve({ name: 'Stale project result', status: 'review' }))
    expect(container.textContent).not.toContain('Stale project result')
    expect(container.querySelector('textarea')?.value).toBe('')
    expect(container.querySelector('fieldset')?.disabled).toBe(false)
  })

  it('announces review failures for recovery', async () => {
    apiMock.reviewOracleEBusiness.mockRejectedValueOnce(new Error('Review unavailable'))
    await act(async () => root.render(<OracleEBusinessReviewImport />))
    await act(async () => setTextareaValue(container.querySelector('textarea')!, 'Source'))
    await act(async () => Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Run Review')!.click())
    expect(container.querySelector('[role="alert"]')?.textContent).toContain('Review unavailable')
  })

  it('renders a sparse review payload without crashing', async () => {
    apiMock.reviewOracleEBusiness.mockResolvedValueOnce({ status: 'review' })
    await act(async () => {
      root.render(<OracleEBusinessReviewImport />)
    })
    await flushPromises()

    const textarea = container.querySelector('textarea') as HTMLTextAreaElement | null
    expect(textarea).not.toBeNull()
    if (!textarea) return
    await act(async () => {
      setTextareaValue(textarea, 'notes only')
    })
    const runButton = Array.from(container.querySelectorAll('button')).find(button => button.textContent?.includes('Run Review'))
    expect(runButton).toBeDefined()
    if (!runButton) return
    await act(async () => {
      runButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await flushPromises()

    expect(container.textContent).toContain('Review Pack')
  })

  it('allows the same source file to be reselected by clearing the file input', async () => {
    await act(async () => {
      root.render(<OracleEBusinessReviewImport />)
    })
    await flushPromises()

    const input = container.querySelector('input[type="file"]') as HTMLInputElement | null
    expect(input).not.toBeNull()
    if (!input) return

    const makeFiles = (name: string, content: string) => {
      const file = new File([content], name)
      const files = {
        length: 1,
        item: (index: number) => index === 0 ? file : null,
        0: file,
        [Symbol.iterator]: function* () {
          yield file
        },
      } as unknown as FileList
      return files
    }

    Object.defineProperty(input, 'files', { value: makeFiles('notes.txt', 'oracle'), configurable: true })

    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(input.value).toBe('')

    Object.defineProperty(input, 'files', { value: makeFiles('notes.txt', 'oracle-2'), configurable: true })
    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(input.value).toBe('')
  })
})

async function flushPromises() {
  await act(async () => {
    await Promise.resolve()
  })
}

function setTextareaValue(input: HTMLTextAreaElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')?.set
  setter?.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}
