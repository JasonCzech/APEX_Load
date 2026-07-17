// @vitest-environment jsdom

import { act, useState } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { type Parameterization } from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import ScriptImportReview from './ScriptImportReview'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const apiMock = vi.hoisted(() => ({
  applyImportParameterization: vi.fn(),
  getProjectSecretStatuses: vi.fn(),
  createProjectSecrets: vi.fn(),
  migrateImportSecrets: vi.fn(),
  discardImportSecretSession: vi.fn(),
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
    vi.clearAllMocks()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    apiMock.getProjectSecretStatuses.mockResolvedValue({
      project_id: 'project-1',
      capabilities: {
        provider: 'database',
        writable: true,
        create_supported: true,
      },
      secrets: [],
    })
    apiMock.createProjectSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
    apiMock.migrateImportSecrets.mockResolvedValue({ project_id: 'project-1', secrets: [], count: 0 })
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
          reviewGeneration="render-unsupported"
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
          reviewGeneration="render-preview"
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
          reviewGeneration="large-preview"
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

  it('auto-applies safe references and allows values to be configured later', async () => {
    const onSave = vi.fn()
    const onEdit = vi.fn()
    const requirement = {
      id: 'auth-header',
      path: '/actions/0/headers/Authorization',
      label: 'Create Order — Authorization header',
      context: 'Bearer credential',
      suggested_name: 'ORDER_API_TOKEN',
      replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
    }
    function Harness() {
      const [requirements, setRequirements] = useState([requirement])
      return (
        <ScriptImportReview
          reviewGeneration="safe-references"
          title="Review Generated Script"
          script={script}
          onSave={onSave}
          onEdit={onEdit}
          secretBindingRequirements={requirements}
          onSecretBindingsApply={async () => setRequirements([])}
        />
      )
    }
    await act(async () => {
      root.render(<Harness />)
    })

    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')
    expect(saveButton?.disabled).toBe(false)
    const editButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Open In Visual Editor with 1 secret to configure')
    expect(editButton?.disabled).toBe(false)
    expect(container.textContent).toContain('Credentials (1)')
    await act(async () => {
      saveButton?.click()
      await Promise.resolve()
    })
    expect(onSave).toHaveBeenCalledTimes(1)
  })

  it('disables save, edit, and credential controls while parameterization is applying', async () => {
    apiMock.applyImportParameterization.mockImplementationOnce(() => new Promise(() => {}))
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'parameterize-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{var.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="parameterization-pending"
          title="Review Generated Script"
          script={script}
          parameterization={parameterization}
          onParameterizationApplied={() => {}}
          onSave={() => {}}
          onEdit={() => {}}
          projectId="project-1"
          secretBindingRequirements={[{
            id: 'auth',
            path: '/actions/0/headers/Authorization',
            label: 'Authorization',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={async () => {}}
        />,
      )
    })
    const apply = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Apply Selected'))
    await act(async () => {
      apply?.click()
      await Promise.resolve()
    })

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save with'))
    const edit = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Open In Visual Editor'))
    expect(save?.disabled).toBe(true)
    expect(edit?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')?.disabled).toBe(true)
  })

  it('disables parameterization while a credential-backed Save is in progress', async () => {
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'parameterize-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{var.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    const onSecretBindingsApply = vi.fn(() => new Promise<void>(() => {}))
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="save-pending"
          title="Review Generated Script"
          script={script}
          parameterization={parameterization}
          onParameterizationApplied={() => {}}
          onSave={vi.fn()}
          projectId="project-1"
          secretBindingRequirements={[{
            id: 'auth',
            path: '/actions/0/headers/Authorization',
            label: 'Authorization',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={onSecretBindingsApply}
        />,
      )
      await Promise.resolve()
    })

    const save = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Save with'))!
    await act(async () => {
      save.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onSecretBindingsApply).toHaveBeenCalledTimes(1)
    expect(container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.disabled).toBe(true)
    expect(container.querySelector<HTMLInputElement>('.paramize-review input[type="checkbox"]')?.disabled).toBe(true)
  })

  it('preserves edited credential names and write-only values after applying parameterization', async () => {
    const parameterization: Parameterization = {
      suggestions: [{
        id: 'parameterize-order-id',
        kind: 'data_param',
        var_name: 'ORDER_ID',
        replacement: '{{var.ORDER_ID}}',
        confidence: 0.95,
        rule_id: 'test',
        rationale: 'Repeated order id',
        auto_apply: true,
        high_risk: false,
      }],
      summary: { total: 1, by_kind: { data_param: 1 }, auto_apply_count: 1, review_count: 0, high_risk_count: 0, pre_applied_count: 0 },
    }
    apiMock.applyImportParameterization.mockResolvedValue({
      script: { ...script, config: { ...script.config, base_url: '{{param.BASE_URL}}' } },
      diffs: [{ kind: 'value_replaced', path: '/config/base_url', suggestion_id: 'parameterize-order-id' }],
      applied_count: 1,
    })

    function Harness() {
      const [generation, setGeneration] = useState(1)
      const [reviewedScript, setReviewedScript] = useState<ScriptDefinition>(script)
      return (
        <ScriptImportReview
          reviewGeneration={generation}
          title="Review Generated Script"
          script={reviewedScript}
          parameterization={parameterization}
          onParameterizationApplied={nextScript => {
            setReviewedScript(nextScript)
            setGeneration(current => current + 1)
          }}
          projectId="project-1"
          secretBindingRequirements={[{
            id: 'auth',
            path: '/actions/0/headers/Authorization',
            label: 'Authorization',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={vi.fn()}
        />
      )
    }

    await act(async () => {
      root.render(<Harness />)
      await Promise.resolve()
    })
    const nameInput = container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')!
    const valueInput = container.querySelector<HTMLInputElement>('.secret-binding-review input[type="password"]')!
    act(() => {
      const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')?.set
      setter?.call(nameInput, 'CUSTOM_ORDER_TOKEN')
      nameInput.dispatchEvent(new Event('input', { bubbles: true }))
      setter?.call(valueInput, 'write-only-value')
      valueInput.dispatchEvent(new Event('input', { bubbles: true }))
    })

    await act(async () => {
      container.querySelector<HTMLButtonElement>('.paramize-review button.btn-primary')?.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="text"]')?.value).toBe('CUSTOM_ORDER_TOKEN')
    expect(container.querySelector<HTMLInputElement>('.secret-binding-review input[type="password"]')?.value).toBe('write-only-value')
  })

  it('invalidates a pending credential action when a newer import result is rendered', async () => {
    const onSave = vi.fn()
    const oldApply = vi.fn().mockResolvedValue(undefined)
    let resolveCommitProbe: ((value: unknown) => void) | undefined
    const oldRequirement = {
      id: 'old-auth',
      path: '/actions/0/headers/Authorization',
      label: 'Old authorization',
      context: 'Old import credential',
      suggested_name: 'OLD_TOKEN',
      replacement: '{{secret.OLD_TOKEN}}',
    }
    const newRequirement = {
      ...oldRequirement,
      id: 'new-auth',
      label: 'New authorization',
      context: 'New import credential',
      suggested_name: 'NEW_TOKEN',
      replacement: '{{secret.NEW_TOKEN}}',
    }

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="old-import"
          title="Old import"
          script={{ ...script, name: 'Old draft' }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[oldRequirement]}
          onSecretBindingsApply={oldApply}
        />,
      )
      await Promise.resolve()
    })
    apiMock.getProjectSecretStatuses.mockReturnValueOnce(new Promise(resolve => {
      resolveCommitProbe = resolve
    }))
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')!
    act(() => saveButton.click())
    await act(async () => Promise.resolve())
    expect(oldApply).not.toHaveBeenCalled()

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="new-import"
          title="New import"
          script={{ ...script, name: 'New draft', config: { base_url: 'https://new.example.test' } }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[newRequirement]}
          onSecretBindingsApply={vi.fn()}
        />,
      )
    })
    await act(async () => {
      resolveCommitProbe?.({
        project_id: 'project-1',
        capabilities: { provider: 'database', writable: true, create_supported: true },
        secrets: [],
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(oldApply).not.toHaveBeenCalled()
    expect(onSave).not.toHaveBeenCalled()
    expect(container.textContent).toContain('New import')
    expect(container.textContent).toContain('{{secret.NEW_TOKEN}}')
    const currentSaveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')
    expect(currentSaveButton?.disabled).toBe(false)
  })

  it('does not carry an applying Save request into a newer requirements-free generation', async () => {
    const onSave = vi.fn()
    let resolveApply: (() => void) | undefined
    const oldApply = vi.fn(() => new Promise<void>(resolve => {
      resolveApply = resolve
    }))
    const requirement = {
      id: 'old-auth',
      path: '/actions/0/headers/Authorization',
      label: 'Old authorization',
      context: 'Old import credential',
      suggested_name: 'OLD_TOKEN',
      replacement: '{{secret.OLD_TOKEN}}',
    }

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="generation-a"
          title="Old import"
          script={{ ...script, name: 'Old draft' }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[requirement]}
          onSecretBindingsApply={oldApply}
        />,
      )
      await Promise.resolve()
    })
    const saveButton = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent === 'Save with 1 secret to configure')!
    act(() => saveButton.click())
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(oldApply).toHaveBeenCalledTimes(1)

    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="generation-b"
          title="New import"
          script={{ ...script, name: 'New safe draft' }}
          projectId="project-1"
          onSave={onSave}
          secretBindingRequirements={[]}
          onSecretBindingsApply={vi.fn()}
        />,
      )
      resolveApply?.()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(onSave).not.toHaveBeenCalled()
    expect(container.textContent).toContain('New safe draft')
  })

  it('preserves scoped action wording while reporting deferred credentials', async () => {
    const onCredentialsPreparedChange = vi.fn()
    await act(async () => {
      root.render(
        <ScriptImportReview
          reviewGeneration="scoped-wording"
          title="Review Generated Scripts"
          script={script}
          saveLabel="Save All 3 Scripts"
          editLabel="Edit Imported Scripts"
          onSave={vi.fn()}
          onEdit={vi.fn()}
          secretBindingRequirements={[{
            id: 'auth-header',
            path: '/actions/0/headers/Authorization',
            label: 'Create Order — Authorization header',
            context: 'Bearer credential',
            suggested_name: 'ORDER_API_TOKEN',
            replacement: 'Bearer {{secret.ORDER_API_TOKEN}}',
          }]}
          onSecretBindingsApply={vi.fn()}
          onCredentialsPreparedChange={onCredentialsPreparedChange}
        />,
      )
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Save All 3 Scripts with 1 secret to configure')
    expect(container.textContent).toContain('Edit Imported Scripts with 1 secret to configure')
    expect(onCredentialsPreparedChange).toHaveBeenLastCalledWith({
      configureLaterCount: 1,
      valid: true,
    })
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
          reviewGeneration="parameterization"
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
