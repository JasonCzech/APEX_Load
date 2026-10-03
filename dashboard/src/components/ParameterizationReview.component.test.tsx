// @vitest-environment jsdom

import { act, useState } from 'react'
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

  it('shows incomplete Aura inspection and unsupported fields when no selectable candidates remain', () => {
    render({ ...buildParameterization([]), aura_payload: {
      complete: false, recognized_bodies: 5, inspected_bodies: 3, rejected_bodies: 1, omitted_bodies: 1,
      string_fields: 12, unsupported_number_fields: 2, unsupported_other_fields: 3, unsafe_path_fields: 4,
      candidate_fields: 6, retained_candidates: 0, omitted_candidates: 6,
    } })
    const coverage = container.querySelector('[aria-label="Aura payload inspection"]')!
    expect(coverage.textContent).toContain('3 of 5 recognized request bodies inspected. Inspection incomplete.')
    expect(coverage.textContent).toContain('12 string fields checked; 6 candidate fields, 0 retained candidates.')
    expect(coverage.textContent).toContain('1 request bodies rejected; 1 request bodies not fully inspected.')
    expect(coverage.textContent).not.toContain('omitted by inspection limits')
    expect(coverage.textContent).toContain('2 numeric fields and 3 boolean, null, or other fields remain unchanged.')
    expect(coverage.textContent).toContain('4 fields excluded because a safe structural path could not be published.')
    expect(coverage.textContent).toContain('6 candidate fields omitted by review limits.')
    expect(container.textContent).toContain('No selectable parameter suggestions were produced.')
    expect(container.querySelector('button')).toBeNull()
  })

  it('reports detection omissions even when no body was classified as Aura', () => {
    render({ ...buildParameterization([]), aura_payload: {
      complete: false, recognized_bodies: 0, inspected_bodies: 0, rejected_bodies: 0, omitted_bodies: 2,
      string_fields: 0, unsupported_number_fields: 0, unsupported_other_fields: 0, unsafe_path_fields: 0,
      candidate_fields: 0, retained_candidates: 0, omitted_candidates: 0,
    } })
    const coverage = container.querySelector('[aria-label="Aura payload inspection"]')!
    expect(coverage.textContent).toContain('0 of 0 recognized request bodies inspected. Inspection incomplete.')
    expect(coverage.textContent).toContain('0 request bodies rejected; 2 request bodies not fully inspected.')
    expect(coverage.textContent).not.toContain('omitted by inspection limits')
    expect(container.querySelector('button')).toBeNull()
  })

  it('shows complete Aura coverage without implying that unsupported scalar fields were parameterized', () => {
    render({ ...buildParameterization([]), aura_payload: {
      complete: true, recognized_bodies: 1, inspected_bodies: 1, rejected_bodies: 0, omitted_bodies: 0,
      string_fields: 0, unsupported_number_fields: 1, unsupported_other_fields: 0, unsafe_path_fields: 0,
      candidate_fields: 0, retained_candidates: 0, omitted_candidates: 0,
    } })
    expect(container.textContent).toContain('Inspection complete.')
    expect(container.textContent).toContain('1 numeric fields and 0 boolean, null, or other fields remain unchanged.')
    expect(container.textContent).not.toContain('could not be fully inspected')
    expect(container.querySelector('button')).toBeNull()
  })

  it('previews the authoritative nested Aura location without captured values and preserves its route on apply', async () => {
    const location = {
      section: 'actions', action_index: 987, action_name: 'Captured action label', field: 'body',
      action_path: 'actions[0].control.steps[1].params.requests[2]',
      decoded_field_path: 'actions[0].params.details::<json>.recordId',
      aura_route: [{ kind: 'key' as const, key: 'actions' }, { kind: 'index' as const, index: 0 }, { kind: 'key' as const, key: 'params' }, { kind: 'key' as const, key: 'details' }, { kind: 'json_string' as const }, { kind: 'key' as const, key: 'recordId' }],
      encoding_layers: ['base64', 'url_form', 'json', 'json_string'], payload_shape: 'private-shape-digest',
      search_text: 'private-search-payload', replace_text: 'private-replace-payload',
    }
    const parameterization = buildParameterization([suggestion({
      id: 'pz_aura', locations: [location], literal_value: 'private-record-value', sample_value: 'private-record-value',
      rationale: 'private-rationale-value', auto_apply: true,
    })])
    applyMock.mockResolvedValue({ script, applied_count: 1, diffs: [
      { suggestion_id: 'pz_aura', path: '/actions/0/control/steps/1/params/requests/2/body', kind: 'value_replaced', before: 'private-full-body-before', after: 'private-full-body-after {{var.user_id}}' },
      { path: '/data_sources/0', kind: 'data_source_added', before: 'private-unattributed-before', after: 'private-unattributed-after' },
      { suggestion_id: 'pz_aura', path: '/parameters/user_id', kind: 'parameter_added', after: 'private-parameter-configuration' },
    ] })
    const onApplied = vi.fn()
    act(() => root.render(<ParameterizationReview guided parameterization={parameterization} script={script} onApplied={onApplied} />))
    expect(container.textContent).toContain(location.action_path)
    expect(container.textContent).toContain(location.decoded_field_path)
    expect(container.textContent).toContain('Base64 body → URL form message → JSON envelope → JSON string')
    expect(container.textContent).not.toContain('987')
    expect(container.textContent).not.toContain('Captured action label')
    await act(async () => { [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Preview parameter changes')!.click() })
    const preview = container.querySelector('[aria-label="Parameter suggestion preview"]')!
    expect(preview.textContent).toContain(location.decoded_field_path)
    expect(preview.textContent).toContain('Captured value hidden')
    expect(preview.textContent).toContain('1 selected suggestion.')
    expect(preview.textContent).not.toContain('1 selected suggestions.')
    await act(async () => { [...container.querySelectorAll<HTMLButtonElement>('button')].find(button => button.textContent === 'Apply reviewed parameter changes')!.click() })
    expect(applyMock.mock.calls[0][0].suggestions[0].locations).toEqual([location])
    expect(onApplied).toHaveBeenCalledTimes(1)
    expect(container.textContent).toContain('Decoded field updated')
    expect(container.textContent).toContain('Data source created')
    expect(container.textContent).toContain('Parameter binding created')
    expect([...container.querySelectorAll('.paramize-diffs .paramize-after')].map(item => item.textContent)).toEqual(['Decoded field updated', 'Data source created', 'Parameter binding created'])
    for (const value of ['private-record-value', 'private-search-payload', 'private-replace-payload', 'private-shape-digest', 'private-rationale-value', 'private-full-body-before', 'private-full-body-after', 'private-unattributed-before', 'private-unattributed-after', 'private-parameter-configuration']) {
      expect(container.innerHTML).not.toContain(value)
    }
  })

  it('keeps missing Aura path metadata explicit and never guesses from an import ordinal', () => {
    render(buildParameterization([suggestion({ locations: [{ section: 'actions', action_index: 987, action_name: 'Captured action label', field: 'body', aura_route: [], replace_text: '' }] })]))
    expect(container.textContent).toContain('Action path unavailable')
    expect(container.textContent).toContain('Decoded field path unavailable')
    expect(container.textContent).toContain('Encoding unavailable')
    expect(container.textContent).not.toContain('actions[987]')
    expect(container.textContent).not.toContain('12345')
  })

  it('bounds Aura location rendering without dropping occurrences from the selected request', async () => {
    const locations = Array.from({ length: 21 }, (_, index) => ({ section: 'actions', action_index: index, field: 'body', replace_text: '',
      action_path: `actions[${index}]`, decoded_field_path: `actions[${index}].params.recordId`, aura_route: [], encoding_layers: ['json'] }))
    render(buildParameterization([suggestion({ id: 'pz_aura_many', locations, auto_apply: true })]))
    expect(container.querySelectorAll('[aria-label="Decoded Aura field"]')).toHaveLength(20)
    expect(container.textContent).toContain('1 more occurrences')
    applyMock.mockResolvedValue({ script, diffs: [], applied_count: 21 })
    await act(async () => { container.querySelector<HTMLButtonElement>('button.btn-primary')!.click() })
    expect(applyMock.mock.calls[0][0].suggestions[0].locations).toHaveLength(21)
  })

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
    expect(onApplied).toHaveBeenCalledWith(parameterizedScript, expect.any(Array), ['pz_auto'], undefined)
    expect(container.textContent).toContain('Applied 1 change(s)')
  })

  it('locks selection and apply while the surrounding workflow is mutating', async () => {
    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={buildParameterization([
            suggestion({ id: 'pz_auto', auto_apply: true }),
            suggestion({ id: 'pz_review', auto_apply: false }),
          ])}
          script={script}
          disabled
          onApplied={vi.fn()}
        />,
      )
    })

    const checkboxes = Array.from(container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]'))
    const applyButton = container.querySelector<HTMLButtonElement>('button.btn-primary')!
    expect(checkboxes.every(checkbox => checkbox.disabled)).toBe(true)
    expect(applyButton.disabled).toBe(true)

    await act(async () => {
      checkboxes[1].click()
      applyButton.click()
      await Promise.resolve()
    })

    expect(checkboxes[1].checked).toBe(false)
    expect(applyMock).not.toHaveBeenCalled()
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

  it('preserves applied state when the parent advances its stale-request generation after apply', async () => {
    const parameterization = buildParameterization([
      suggestion({ id: 'pz_auto', auto_apply: true }),
    ])
    applyMock.mockResolvedValue({
      script: { ...script, name: 'Parameterized' },
      diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_auto' }],
      applied_count: 1,
    })

    function Harness() {
      const [generation, setGeneration] = useState(1)
      const [currentScript, setCurrentScript] = useState(script)
      return (
        <ParameterizationReview
          parameterization={parameterization}
          script={currentScript}
          reviewGeneration={generation}
          onApplied={nextScript => {
            setGeneration(current => current + 1)
            setCurrentScript(nextScript)
          }}
        />
      )
    }

    act(() => root.render(<Harness />))
    await act(async () => {
      container.querySelector<HTMLButtonElement>('button.btn-primary')?.click()
      await Promise.resolve()
    })

    expect(container.textContent).toContain('Applied 1 change(s)')
    expect(container.querySelector<HTMLButtonElement>('button.btn-primary')?.textContent).toContain('Reapply Selected (1)')
  })

  it('drops a late apply response after the review generation changes', async () => {
    let resolveApply: ((value: unknown) => void) | undefined
    applyMock.mockImplementationOnce(() => new Promise(resolve => { resolveApply = resolve }))
    const parameterization = buildParameterization([suggestion({ id: 'pz_auto', auto_apply: true })])
    const onApplied = vi.fn()
    const onApplyingChange = vi.fn()

    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={parameterization}
          script={script}
          reviewGeneration="review-1"
          onApplyingChange={onApplyingChange}
          onApplied={onApplied}
        />,
      )
    })
    await act(async () => {
      container.querySelector<HTMLButtonElement>('button.btn-primary')?.click()
      await Promise.resolve()
    })
    expect(onApplyingChange).toHaveBeenCalledWith(true)

    act(() => {
      root.render(
        <ParameterizationReview
          parameterization={parameterization}
          script={script}
          reviewGeneration="review-2"
          onApplyingChange={onApplyingChange}
          onApplied={onApplied}
        />,
      )
    })
    expect(onApplyingChange).toHaveBeenLastCalledWith(false)

    await act(async () => {
      resolveApply?.({
        script: { ...script, name: 'Late parameterized script' },
        diffs: [{ kind: 'value_replaced', path: '/actions/0/target', suggestion_id: 'pz_auto' }],
        applied_count: 1,
      })
      await Promise.resolve()
    })

    expect(onApplied).not.toHaveBeenCalled()
    expect(container.textContent).not.toContain('Applied 1 change(s)')
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

  it('bounds high-cardinality suggestions and exposes keyboard-operable page controls', async () => {
    render(buildParameterization(Array.from({ length: 125 }, (_unused, index) => suggestion({
      id: `pz_${index}`,
      var_name: `parameter_${index}`,
    }))))

    expect(container.querySelectorAll('.paramize-item')).toHaveLength(50)
    expect(container.textContent).toContain('Showing 1–50 of 125')
    expect(container.textContent).toContain('parameter_0')
    expect(container.textContent).not.toContain('parameter_50')
    const next = Array.from(container.querySelectorAll<HTMLButtonElement>('button'))
      .find(button => button.textContent?.includes('Next suggestions'))!
    await act(async () => {
      next.focus()
      next.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
      next.click()
    })

    expect(container.querySelectorAll('.paramize-item')).toHaveLength(50)
    expect(container.textContent).toContain('Showing 51–100 of 125')
    expect(container.textContent).toContain('parameter_50')
    expect(container.textContent).not.toContain('parameter_0')
  })
})

it('requires a fresh guided preview after selection or draft changes and masks secret values', async () => {
  const container = document.createElement('div'); const root = createRoot(container)
  const parameterization = buildParameterization([
    suggestion({ id: 'auto', auto_apply: true }),
    suggestion({ id: 'secret', kind: 'secret', var_name: 'PASSWORD', literal_value: 'never-show-this', sample_value: 'never-show-this', high_risk: true }),
  ])
  const onApplied = vi.fn()
  const render = (draft = script) => root.render(<ParameterizationReview guided parameterization={parameterization} script={draft} onApplied={onApplied} />)
  const button = (name: string) => Array.from(container.querySelectorAll('button')).find(b => b.textContent === name)!
  applyMock.mockReset()
  try {
    act(() => render())
    act(() => button('Preview parameter changes').click())
    expect(container.querySelector('[aria-label="Parameter suggestion preview"]')).not.toBeNull()
    expect(applyMock).not.toHaveBeenCalled()
    act(() => container.querySelectorAll<HTMLInputElement>('input[type="checkbox"]')[1].click())
    expect(container.querySelector('[aria-label="Parameter suggestion preview"]')).toBeNull()
    act(() => button('Preview parameter changes').click())
    expect(container.textContent).not.toContain('never-show-this')
    expect(container.textContent).toContain('High risk: explicitly selected')
    expect(container.querySelector('[aria-label="Parameter suggestion preview"]')!.textContent).toContain('2 selected suggestions.')
    act(() => render({ ...script, name: 'Changed draft' }))
    expect(container.querySelector('[aria-label="Parameter suggestion preview"]')).toBeNull()
    act(() => button('Preview parameter changes').click())
    applyMock.mockResolvedValueOnce({ script, diffs: [], applied_count: 2 })
    await act(async () => button('Apply reviewed parameter changes').click())
    expect(applyMock).toHaveBeenCalledOnce()
    expect(onApplied).toHaveBeenCalledOnce()
  } finally { act(() => root.unmount()) }
})
