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
    expect(container.textContent).toContain('Oracle E-Business Signals')
    expect(container.textContent).toContain('Confirm Oracle E-Business runtime settings before saving reusable assets.')

    // Review-only surface: there must be no save / save-to-library control.
    const saveButton = Array.from(container.querySelectorAll('button')).find(button => /save/i.test(button.textContent || ''))
    expect(saveButton).toBeUndefined()
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
