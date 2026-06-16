import { useCallback, useMemo } from 'react'

import { EntitlementsResponse, EntitlementsUsageResponse } from '../../types'
import { request } from '../apiClient'

export function useEntitlementsApi() {
  const getEntitlements = useCallback(
    () => request<EntitlementsResponse>('/system/entitlements'),
    [],
  )

  const getEntitlementsUsage = useCallback(
    () => request<EntitlementsUsageResponse>('/system/entitlements/usage'),
    [],
  )

  return useMemo(
    () => ({ getEntitlements, getEntitlementsUsage }),
    [getEntitlements, getEntitlementsUsage],
  )
}
