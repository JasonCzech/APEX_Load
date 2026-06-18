import { useCallback, useMemo } from 'react'

import {
  CorrelationRuleset,
  CorrelationRulesetApplyResponse,
  CorrelationRulesetListResponse,
} from '../../types'
import { request } from '../apiClient'

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
    (params?: { project_id?: string; limit?: number; offset?: number }) =>
      request<CorrelationRulesetListResponse>(`/correlation-rulesets${buildQuery({ ...params })}`),
    [],
  )

  const getCorrelationRuleset = useCallback(
    (id: string) => request<CorrelationRuleset>(`/correlation-rulesets/${id}`),
    [],
  )

  const createCorrelationRuleset = useCallback(
    (body: Partial<CorrelationRuleset>) =>
      request<CorrelationRuleset>('/correlation-rulesets', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const updateCorrelationRuleset = useCallback(
    (id: string, body: Partial<CorrelationRuleset>) =>
      request<CorrelationRuleset>(`/correlation-rulesets/${id}`, { method: 'PUT', body: JSON.stringify(body) }),
    [],
  )

  const deleteCorrelationRuleset = useCallback(
    (id: string) => request<{ deleted: string }>(`/correlation-rulesets/${id}`, { method: 'DELETE' }),
    [],
  )

  const discoverCorrelationRulesetFromScript = useCallback(
    (body: { script_id: string; name?: string }) =>
      request<CorrelationRuleset>('/correlation-rulesets/discover-from-script', { method: 'POST', body: JSON.stringify(body) }),
    [],
  )

  const applyCorrelationRulesetToScript = useCallback(
    (id: string, body: { script_id: string; apply?: boolean }) =>
      request<CorrelationRulesetApplyResponse>(`/correlation-rulesets/${id}/apply-to-script`, { method: 'POST', body: JSON.stringify(body) }),
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
