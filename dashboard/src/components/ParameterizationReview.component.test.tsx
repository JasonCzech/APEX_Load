// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import ParameterizationReview from './ParameterizationReview'
import {
  type Parameterization,
  type ParameterizationSuggestion,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'

const applyMock = vi.fn()

vi.mock('../hooks/useApi', () => ({
  useApi: () => ({
    applyImportParameterization: applyMock,
  }),
}))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const script: ScriptDefinition = {
  name: 'Test Script',
  version: '1.0',
  protocol: 'http',
  config: { base_url: 'https://api.example.com' },
  actions: [],
}

function suggestion(overrides: Partial<ParameterizationSuggestion>): ParameterizationSuggestion {
  return {
    id: 'pz_test',
    kind: 'data_param',
    var_name: 'user_id',
    replacement: '{{var.user_id}}',
    literal_value: '12345',
    sample_value: '12345',
    confidence: 0.6,
    rule_id: 'data_param.name_heuristic',
    rationale: 'looks like test data',
    auto_apply: false,
    high_risk: false,
    ...overrides,
  }
}

function buildParameterization(suggestions: ParameterizationSuggestion[]): Parameterization {
  return {
    suggestions,
    summary: {
      total: suggestions.length,
      by_kind: {},
      auto_apply_count: suggestions.filter(s => s.auto_apply).length,
      review_count: suggestions.filter(s => !s.auto_apply && !s.pre_applied).length,
      high_risk_count: suggestions.filter(s => s.high_risk).length,
      pre_applied_count: suggestions.filter(s => s.pre_applied).length,
    },
  }
}

describe('ParameterizationReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    applyMock.mockReset()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  function render(parameterization: Parameterization, onApplied = vi.fn()) {
    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={parameterization}
          script={script}
          onApplied={onApplied}
        />,
      )
    })
    return onApplied
  }

  it('pre-checks only auto-apply suggestions', () => {
    render(buildParameterization([
      suggestion({ id: 'pz_auto', kind: 'correlation', var_name: 'corr_token', confidence: 0.9, auto_apply: true }),
      suggestion({ id: 'pz_review', var_name: 'user_id', auto_apply: false }),
      suggestion({ id: 'pz_secret', kind: 'secret', var_name: 'PASSWORD', confidence: 0.9, auto_apply: false, high_risk: true }),
    ]))

    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    expect(checkboxes).toHaveLength(3)
    const checkedCount = checkboxes.filter(box => box.checked).length
    expect(checkedCount).toBe(1)
    expect(container.textContent).toContain('corr_token')
    expect(container.textContent).toContain('high risk')
    const applyButton = container.querySelector('button.btn-primary')
    expect(applyButton?.textContent).toContain('Apply Selected (1)')
  })

  it('renders pre-applied suggestions without checkboxes', () => {
    render(buildParameterization([
      suggestion({ id: 'pz_pre', kind: 'correlation', var_name: 'corr_csrf', confidence: 1, pre_applied: true }),
    ]))

    expect(container.querySelectorAll('input[type="checkbox"]')).toHaveLength(0)
    expect(container.textContent).toContain('corr_csrf')
    expect(container.querySelector('.paramize-preapplied-mark')).not.toBeNull()
    // Nothing selectable, so no apply button is rendered enabled.
    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    expect(applyButton?.disabled).toBe(true)
  })

  it('applies the selection and lifts the parameterized script', async () => {
    const parameterizedScript = { ...script, name: 'Parameterized' }
    applyMock.mockResolvedValue({
      script: parameterizedScript,
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_auto' }],
      applied_count: 1,
    })
    const onApplied = render(buildParameterization([
      suggestion({ id: 'pz_auto', kind: 'correlation', var_name: 'corr_token', confidence: 0.9, auto_apply: true }),
    ]))

    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    expect(applyButton).not.toBeNull()
    await act(async () => {
      applyButton!.click()
    })

    expect(applyMock).toHaveBeenCalledWith(expect.objectContaining({
      selected_ids: ['pz_auto'],
      confirm_high_risk: false,
    }))
    expect(onApplied).toHaveBeenCalledWith(parameterizedScript, expect.any(Array))
    expect(container.textContent).toContain('Applied 1 change(s)')
  })

  it('allows the selection to be applied again after the user changes it', async () => {
    applyMock
      .mockResolvedValueOnce({ script, diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_auto' }], applied_count: 1 })
      .mockResolvedValueOnce({ script, diffs: [{ kind: 'value_replaced', path: '/actions/1/target', suggestion_id: 'pz_review' }], applied_count: 1 })
    render(buildParameterization([
      suggestion({ id: 'pz_auto', kind: 'correlation', var_name: 'corr_token', confidence: 0.9, auto_apply: true }),
      suggestion({ id: 'pz_review', var_name: 'user_id', auto_apply: false }),
    ]))

    let applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    await act(async () => {
      applyButton!.click()
    })
    expect(container.textContent).toContain('Applied 1 change(s)')

    const reviewBox = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')).find(box => !box.checked)
    await act(async () => {
      reviewBox!.click()
    })
    expect(container.textContent).not.toContain('Applied 1 change(s)')
    applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    expect(applyButton?.textContent).toContain('Apply Selected (2)')
    await act(async () => {
      applyButton!.click()
    })

    expect(applyMock).toHaveBeenLastCalledWith(expect.objectContaining({
      selected_ids: ['pz_auto', 'pz_review'],
    }))
  })

  it('keeps applied state on an apply-driven script change but resets on a new import', async () => {
    applyMock.mockResolvedValue({ script: { ...script, name: 'Parameterized' }, diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_auto' }], applied_count: 1 })
    const parameterization = buildParameterization([
      suggestion({ id: 'pz_auto', auto_apply: true }),
    ])
    render(parameterization)

    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    await act(async () => {
      applyButton!.click()
    })
    expect(container.textContent).toContain('Applied 1 change(s)')

    // Re-rendering with only a new script object — which every call site does on
    // a successful apply, lifting the parameterized script back into the prop —
    // must NOT wipe the applied badge/diffs.
    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={parameterization}
          script={{ ...script, name: 'Changed Script' }}
          onApplied={vi.fn()}
        />,
      )
    })
    expect(container.textContent).toContain('Applied 1 change(s)')

    // A genuinely new import (different suggestion set) DOES reset the state.
    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={buildParameterization([suggestion({ id: 'pz_other', auto_apply: true })])}
          script={script}
          onApplied={vi.fn()}
        />,
      )
    })
    expect(container.textContent).not.toContain('Applied 1 change(s)')
    expect(container.querySelector<HTMLButtonElement>('button.btn-primary')?.textContent).toContain('Apply Selected (1)')
  })

  it('passes confirm_high_risk when a secret row is selected', async () => {
    applyMock.mockResolvedValue({ script, diffs: [], applied_count: 1 })
    render(buildParameterization([
      suggestion({ id: 'pz_secret', kind: 'secret', var_name: 'PASSWORD', confidence: 0.9, auto_apply: false, high_risk: true }),
    ]))

    const checkbox = container.querySelector<HTMLInputElement>('input[type="checkbox"]')
    await act(async () => {
      checkbox!.click()
    })
    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    await act(async () => {
      applyButton!.click()
    })

    expect(applyMock).toHaveBeenCalledWith(expect.objectContaining({
      selected_ids: ['pz_secret'],
      confirm_high_risk: true,
    }))
  })

  it('masks high-risk sample values in the review list', () => {
    render(buildParameterization([
      suggestion({ id: 'pz_secret', kind: 'secret', var_name: 'PASSWORD', literal_value: 'super-secret', sample_value: 'super-secret', confidence: 0.9, high_risk: true }),
    ]))

    expect(container.textContent).toContain('••••••')
    expect(container.textContent).not.toContain('super-secret')
  })

  it('masks secret values in applied diffs', async () => {
    applyMock.mockResolvedValue({
      script,
      diffs: [{ kind: 'value_replaced', path: '/actions/0/body', suggestion_id: 'pz_secret', before: 'super-secret', after: '{{secret.PASSWORD}}' }],
      applied_count: 1,
    })
    render(buildParameterization([
      suggestion({ id: 'pz_secret', kind: 'secret', var_name: 'PASSWORD', literal_value: 'super-secret', sample_value: 'super-secret', confidence: 0.9, high_risk: true }),
    ]))

    const checkbox = container.querySelector<HTMLInputElement>('input[type="checkbox"]')
    await act(async () => {
      checkbox!.click()
    })
    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    await act(async () => {
      applyButton!.click()
    })

    expect(container.textContent).toContain('••••••')
    expect(container.textContent).toContain('{{secret.PASSWORD}}')
    expect(container.textContent).not.toContain('super-secret')
  })

  it('shows the blocked reason without lifting state', async () => {
    applyMock.mockResolvedValue({ script, diffs: [], applied_count: 0, blocked: true, blocked_reason: 'needs confirmation' })
    const onApplied = render(buildParameterization([
      suggestion({ id: 'pz_auto', auto_apply: true }),
    ]))

    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')
    await act(async () => {
      applyButton!.click()
    })

    expect(onApplied).not.toHaveBeenCalled()
    expect(container.textContent).toContain('needs confirmation')
  })

  it('filters suggestions by script name for multi-script imports', () => {
    render(buildParameterization([
      suggestion({ id: 'pz_a', var_name: 'group_a_var', script_name: 'Group A' }),
      suggestion({ id: 'pz_b', var_name: 'group_b_var', script_name: 'Group B' }),
    ]))
    expect(container.textContent).toContain('group_a_var')
    expect(container.textContent).toContain('group_b_var')

    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={buildParameterization([
            suggestion({ id: 'pz_a', var_name: 'group_a_var', script_name: 'Group A' }),
            suggestion({ id: 'pz_b', var_name: 'group_b_var', script_name: 'Group B' }),
          ])}
          script={script}
          scriptName="Group A"
          onApplied={vi.fn()}
        />,
      )
    })
    expect(container.textContent).toContain('group_a_var')
    expect(container.textContent).not.toContain('group_b_var')
  })
})
