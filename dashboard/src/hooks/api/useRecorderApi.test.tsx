// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { type RecorderImportResult } from '../../types/scriptImports'
import { type RecorderConversionOptions, useRecorderApi } from './useRecorderApi'

const { request } = vi.hoisted(() => ({ request: vi.fn().mockResolvedValue({}) }))
vi.mock('../apiClient', () => ({ request }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('useRecorderApi conversion contracts', () => {
  let api!: ReturnType<typeof useRecorderApi>
  let container: HTMLDivElement
  let root: Root

  function Harness() {
    api = useRecorderApi()
    return null
  }

  beforeEach(async () => {
    request.mockReset()
    request.mockResolvedValue({})
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    await act(async () => {
      root.render(<Harness />)
    })
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('preserves legacy (id, name, parameterize) serialization for convert and preview', async () => {
    await api.convertRecording('rec-1', 'Order Flow', true)
    expect(request).toHaveBeenLastCalledWith('/recorder/rec-1/convert', {
      method: 'POST',
      body: JSON.stringify({ name: 'Order Flow', parameterize: true }),
    })

    await api.previewRecordingConversion('rec-1', 'Order Flow', false)
    expect(request).toHaveBeenLastCalledWith('/recorder/rec-1/convert/preview', {
      method: 'POST',
      body: JSON.stringify({ name: 'Order Flow', parameterize: false }),
    })

    await api.convertRecording('rec-2')
    expect(request).toHaveBeenLastCalledWith('/recorder/rec-2/convert', {
      method: 'POST',
      body: '{}',
    })
  })

  it('serializes the full RecorderConversionOptions payload for object and 4-arg calls', async () => {
    const fullOptions: RecorderConversionOptions = {
      name: 'Unified Checkout',
      parameterize: true,
      conversion_policy: 'unified_v1',
      profile_id: 'prof-checkout',
      recording_profile_id: 'prof-checkout',
      resource_mode: 'in_step',
      async_scan: false,
      page_title_checks: true,
      http_version_mode: 'exact',
      disable_salesforce: true,
      agentforce_transform: false,
      parameterization_selections: ['param-user', 'param-sku'],
      secret_bindings: [{ requirement_id: 'req-api-key', secret_name: 'CHECKOUT_API_KEY' }],
    }

    await api.previewRecordingConversion('rec-unified', fullOptions)
    expect(request).toHaveBeenLastCalledWith('/recorder/rec-unified/convert/preview', {
      method: 'POST',
      body: JSON.stringify(fullOptions),
    })

    await api.convertRecording('rec-unified', fullOptions)
    expect(request).toHaveBeenLastCalledWith('/recorder/rec-unified/convert', {
      method: 'POST',
      body: JSON.stringify(fullOptions),
    })

    await api.previewRecordingConversion('rec-hybrid', 'Hybrid Flow', false, {
      conversion_policy: 'unified_v1',
      profile_id: 'prof-1',
      resource_mode: 'runtime',
      http_version_mode: 'upgrade',
      secret_bindings: [{ requirement_id: 'req-api-token', secret_name: 'API_TOKEN_SECRET' }],
    })
    expect(JSON.parse(request.mock.lastCall?.[1]?.body as string)).toEqual({
      name: 'Hybrid Flow',
      parameterize: false,
      conversion_policy: 'unified_v1',
      profile_id: 'prof-1',
      resource_mode: 'runtime',
      http_version_mode: 'upgrade',
      secret_bindings: [{ requirement_id: 'req-api-token', secret_name: 'API_TOKEN_SECRET' }],
    })

    // Options object as first argument with boolean parameterize override and extraOptions merge
    await api.convertRecording(
      'rec-override',
      {
        name: 'Override Flow',
        parameterize: false,
        conversion_policy: 'legacy',
      },
      true,
      {
        conversion_policy: 'unified_v1',
        correlation_mode: 'high_confidence',
        parameterization_selections: [{ script_index: 0, selected_ids: ['sug-1'] }],
      },
    )
    expect(JSON.parse(request.mock.lastCall?.[1]?.body as string)).toEqual({
      name: 'Override Flow',
      parameterize: true,
      conversion_policy: 'unified_v1',
      correlation_mode: 'high_confidence',
      parameterization_selections: [{ script_index: 0, selected_ids: ['sug-1'] }],
    })
  })

  it('aligns RecorderImportResult with server effective_settings and http_secret_binding_requirements fields', async () => {
    const mockResult: RecorderImportResult = {
      scripts: [],
      warnings: [],
      unsupported_elements: [],
      assumptions: [],
      review_sections: [],
      repair_suggestions: [],
      correlations: [],
      stats: { requests_recorded: 4, requests_excluded: 0 },
      summary: {},
      conversion_policy: 'unified_v1',
      effective_settings: {
        conversion_policy: 'unified_v1',
        correlation_mode: 'high_confidence',
        recording_profile_id: 'prof-default',
        recording_profile_name: 'Default Profile',
        resource_mode: 'runtime',
        async_scan: true,
        page_title_checks: true,
        http_version_mode: 'upgrade',
        high_confidence_only: true,
        salesforce_native: true,
        source_entry_point: 'recorder_convert',
        capability_notes: [],
      },
      http_parameterization: {
        suggestions: [],
        summary: {
          total: 0,
          by_kind: {},
          auto_apply_count: 0,
          review_count: 0,
          high_risk_count: 0,
          pre_applied_count: 0,
        },
      },
      http_preflight: {
        valid: true,
        action_count: 4,
        expected_action_count: 4,
        data_source_count: 0,
        parameter_count: 1,
      },
      http_secret_binding_requirements: [
        {
          id: 'req-http-1',
          path: 'actions[0].headers.Authorization',
          label: 'Authorization',
          context: 'Bearer token header',
          suggested_name: 'BEARER_TOKEN',
          script_index: 1,
        },
      ],
    }
    request.mockResolvedValueOnce(mockResult)

    const res = await api.previewRecordingConversion('rec-companion', {
      conversion_policy: 'unified_v1',
    })
    expect(res.conversion_policy).toBe('unified_v1')
    expect(res.effective_settings?.recording_profile_id).toBe('prof-default')
    expect(res.effective_settings?.high_confidence_only).toBe(true)
    expect(res.http_preflight?.valid).toBe(true)
    expect(res.http_secret_binding_requirements).toHaveLength(1)
  })
})
