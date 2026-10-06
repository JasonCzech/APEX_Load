import { useContext } from 'react'

import { ApiContext } from './apiContext'
import type { ApiServices } from './ApiProvider'

export { ApiError } from './apiClient'

export function useApi(): ApiServices {
  const services = useContext(ApiContext)
  if (!services) {
    throw new Error('useApi must be used within ApiProvider')
  }
  return services
}

// Leaf surfaces that are also rendered in isolated component tests may opt out
// of API-backed enhancements when no provider is present. Core application
// routes still use useApi() and fail closed on a missing provider.
export function useOptionalApi(): ApiServices | null {
  return useContext(ApiContext)
}
