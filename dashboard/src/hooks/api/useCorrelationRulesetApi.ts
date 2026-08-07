import { useCallback, useMemo } from 'react'

import type {
  CorrelationRuleset,
  CorrelationRulesetApplyResponse,
  CorrelationRulesetListResponse,
} from '../../types/correlationRulesets'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

function buildQuery(params: Record<string, string | number | undefined>) {
  const query = new URLSearchParams()
  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== '') {
      query.set(key, String(value))
    }
  })
  const suffix = query.toString()
  return suffix ? `?${suffix}` : ''
}

export function useCorrelationRulesetApi() {
  const listCorrelationRulesets = useCallback(
    (params?: { project_id?: string; limit?: number; offset?: number }, options?: ApiCallOptions) =>
      request<CorrelationRulesetListResponse>(`/correlation-rulesets${buildQuery({ ...params })}`, withCallOptions(undefined, options)),
    [],
  )

  const getCorrelationRuleset = useCallback(
    (id: string, options?: ApiCallOptions) => request<CorrelationRuleset>(`/correlation-rulesets/${id}`, withCallOptions(undefined, options)),
    [],
  )

  const createCorrelationRuleset = useCallback(
    (body: Partial<CorrelationRuleset>, options?: ApiCallOptions) =>
      request<CorrelationRuleset>('/correlation-rulesets', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const updateCorrelationRuleset = useCallback(
    (id: string, body: Partial<CorrelationRuleset> & { expected_updated_at: string }, options?: ApiCallOptions) =>
      request<CorrelationRuleset>(`/correlation-rulesets/${id}`, withCallOptions({ method: 'PUT', body: JSON.stringify(body) }, options)),
    [],
  )

  const deleteCorrelationRuleset = useCallback(
    (id: string, options?: ApiCallOptions) => request<{ deleted: string }>(`/correlation-rulesets/${id}`, withCallOptions({ method: 'DELETE' }, options)),
    [],
  )

  const discoverCorrelationRulesetFromScript = useCallback(
    (body: { script_id: string; name?: string }, options?: ApiCallOptions) =>
      request<CorrelationRuleset>('/correlation-rulesets/discover-from-script', withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  const applyCorrelationRulesetToScript = useCallback(
    (id: string, body: { script_id: string; apply?: boolean; preview_token?: string }, options?: ApiCallOptions) =>
      request<CorrelationRulesetApplyResponse>(`/correlation-rulesets/${id}/apply-to-script`, withCallOptions({ method: 'POST', body: JSON.stringify(body) }, options)),
    [],
  )

  return useMemo(
    () => ({
      listCorrelationRulesets,
      getCorrelationRuleset,
      createCorrelationRuleset,
      updateCorrelationRuleset,
      deleteCorrelationRuleset,
      discoverCorrelationRulesetFromScript,
      applyCorrelationRulesetToScript,
    }),
    [listCorrelationRulesets, getCorrelationRuleset, createCorrelationRuleset, updateCorrelationRuleset, deleteCorrelationRuleset, discoverCorrelationRulesetFromScript, applyCorrelationRulesetToScript],
  )
}
