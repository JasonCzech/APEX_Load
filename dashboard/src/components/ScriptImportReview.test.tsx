// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { type Parameterization } from '../types/scriptImports'
import ScriptImportReview from './ScriptImportReview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  applyImportParameterization: vi.fn(),
}))

vi.mock('../hooks/useApi', () => ({
  useApi: () => apiMock,
}))

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

  it('caps very large generated previews', () => {
    act(() => {
      root.render(
        <ScriptImportReview
          title="Review Generated Script"
          script={{
            ...script,
            actions: Array.from({ length: 5000 }, (_, index) => ({
              name: `Action ${index}`,
              type: 'request',
              method: 'GET',
              target: `/orders/${index}`,
            })),
          }}
        />,
      )
    })

    expect(container.textContent).toContain('Preview truncated for browser performance')
    const preview = container.querySelector('.script-import-review-preview pre')
    expect(preview?.textContent?.length).toBeLessThan(121000)
  })

  it('blocks saving until all redacted credentials are bound', () => {
    const onSave = vi.fn()
    const onEdit = vi.fn()
    const onSecretBindingsApply = vi.fn()
    act(() => {
      root.render(
        <ScriptImportReview
          title="Review Generated Script"
          script={script}
          onSave={onSave}
          onEdit={onEdit}
          secretBindingRequirements={[
            {
              id: 'auth-header',
              path: '/actions/0/headers/Authorization',
              label: 'Create Order — Authorization header',
              context: 'Bearer credential',
              suggested_name: 'ORDER_API_TOKEN',
              replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
            },
          ]}
          onSecretBindingsApply={onSecretBindingsApply}
        />,
      )
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save To Script Library')
    expect(saveButton?.disabled).toBe(true)
    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Open In Visual Editor')
    expect(editButton?.disabled).toBe(true)
    expect(container.textContent).toContain('Resolve Redacted Credentials (1)')
    act(() => saveButton!.click())
    act(() => editButton!.click())
    expect(onSave).not.toHaveBeenCalled()
    expect(onEdit).not.toHaveBeenCalled()

    act(() => {
      root.render(
        <ScriptImportReview
          title="Review Generated Script"
          script={script}
          onSave={onSave}
          secretBindingRequirements={[]}
          onSecretBindingsApply={onSecretBindingsApply}
        />,
      )
    })
    const enabledSaveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save To Script Library')
    expect(enabledSaveButton?.disabled).toBe(false)
  })

  it('filters scoped parameterization suggestions to the active script', () => {
    const parameterization: Parameterization = {
      suggestions: [
        {
          id: 'pz_kafka',
          kind: 'data_param',
          script_name: 'Orders Kafka',
          var_name: 'kafka_token',
          replacement: '{{param.kafka_token}}',
          literal_value: 'kafka-123',
          sample_value: 'kafka-123',
          confidence: 0.8,
          rule_id: 'test.kafka',
          rationale: 'Kafka script value',
          auto_apply: false,
          high_risk: false,
        },
        {
          id: 'pz_jms',
          kind: 'data_param',
          script_name: 'Orders JMS',
          var_name: 'jms_token',
          replacement: '{{param.jms_token}}',
          literal_value: 'jms-123',
          sample_value: 'jms-123',
          confidence: 0.8,
          rule_id: 'test.jms',
          rationale: 'JMS script value',
          auto_apply: false,
          high_risk: false,
        },
      ],
      summary: {
        total: 2,
        by_kind: { data_param: 2 },
        auto_apply_count: 0,
        review_count: 2,
        high_risk_count: 0,
        pre_applied_count: 0,
      },
    }

    act(() => {
      root.render(
        <ScriptImportReview
          title="Review Generated Script"
          script={{ ...script, name: 'Orders JMS' }}
          parameterization={parameterization}
          onParameterizationApplied={vi.fn()}
        />,
      )
    })

    expect(container.textContent).toContain('jms_token')
    expect(container.textContent).not.toContain('kafka_token')
  })
})
