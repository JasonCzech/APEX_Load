import { useCallback, useMemo } from 'react'

import type {
  EntitlementsResponse,
  EntitlementsUsageResponse,
  ProjectEntitlementsUsageResponse,
} from '../../types/entitlements'
import { request } from '../apiClient'
import { withCallOptions, type ApiCallOptions } from './requestOptions'

export function useEntitlementsApi() {
  const getEntitlements = useCallback(
    (options?: ApiCallOptions) => request<EntitlementsResponse>('/system/entitlements', withCallOptions(undefined, options)),
    [],
  )

  const getEntitlementsUsage = useCallback(
    (options?: ApiCallOptions) => request<EntitlementsUsageResponse>('/system/entitlements/usage', withCallOptions(undefined, options)),
    [],
  )

  const getProjectEntitlementsUsage = useCallback(
    (projectId: string, options?: ApiCallOptions) =>
      request<ProjectEntitlementsUsageResponse>(`/projects/${projectId}/entitlements/usage`, withCallOptions(undefined, options)),
    [],
  )

  return useMemo(
    () => ({ getEntitlements, getEntitlementsUsage, getProjectEntitlementsUsage }),
    [getEntitlements, getEntitlementsUsage, getProjectEntitlementsUsage],
  )
}
