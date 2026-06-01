// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import ScriptImportReview from './ScriptImportReview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const script = {
  name: 'Imported API Script',
  version: '1.0.0',
  protocol: 'http',
  config: { base_url: 'https://api.example.test' },
  actions: [{ name: 'Create Order', type: 'request', method: 'POST', target: '/orders' }],
}

describe('ScriptImportReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => {
      root.unmount()
    })
    container.remove()
  })

  it('renders unsupported elements and diagnostic review sections', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          title="Review Generated Script"
          script={script}
          unsupportedElements={[
            'OpenAPI schema $ref at POST /orders [warning]: schema reference external.yaml could not be resolved',
          ]}
          reviewSections={[
            {
              key: 'import_diagnostics_warning',
              title: 'Import Diagnostics',
              severity: 'warning',
              summary: '1 import diagnostic requires review.',
              items: ['Multipart file upload needs a governed file value.'],
            },
          ]}
        />,
      )
    })

    expect(container.textContent).toContain('Unsupported Elements (1)')
    expect(container.textContent).toContain('schema reference external.yaml')
    expect(container.textContent).toContain('Import Review (1)')
    expect(container.textContent).toContain('Multipart file upload needs a governed file value.')
  })

  it('renders generated preview as a collapsible section before source comparison content', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          title="Review Generated Script"
          script={script}
          previewAside={<div className="test-source-comparison">Source Comparison</div>}
        />,
      )
    })

    const review = container.querySelector('.script-import-review')
    expect(review).not.toBeNull()
    const children = Array.from(review?.children || [])
    const preview = children.find(element => element.classList.contains('script-import-review-preview')) as HTMLDetailsElement | undefined
    const comparison = children.find(element => element.classList.contains('script-import-review-preview-aside'))
    expect(preview?.tagName).toBe('DETAILS')
    expect(preview?.open).toBe(true)
    expect(preview?.querySelector('summary h4')?.textContent).toBe('Generated Script Preview')
    expect(comparison?.textContent).toContain('Source Comparison')
    expect(preview && comparison ? children.indexOf(preview) < children.indexOf(comparison) : false).toBe(true)
    expect(container.querySelector('.script-import-review-preview-row')).toBeNull()
  })
})
