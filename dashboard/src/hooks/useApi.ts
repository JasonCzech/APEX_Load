import { createContext, createElement, type PropsWithChildren, useContext, useMemo } from 'react'

import { ApiError } from './apiClient'
import { mergeApiServiceGroups } from './apiServiceRegistry'
import { useApplicationApi } from './api/useApplicationApi'
import { useBrowserRecorderApi } from './api/useBrowserRecorderApi'
import { useRecorderApi } from './api/useRecorderApi'
import { useProjectApi } from './api/useProjectApi'
import { useRepositoryApi } from './api/useRepositoryApi'
import { useScriptApi } from './api/useScriptApi'
import { useSystemApi } from './api/useSystemApi'
import { useTestApi } from './api/useTestApi'
import { useLibraryApi } from './api/useLibraryApi'
import { useCapacityApi } from './api/useCapacityApi'
import { useFleetApi } from './api/useFleetApi'
import { useExecutionPlansApi } from './api/useExecutionPlansApi'
import { useMonitoringCollectorsApi } from './api/useMonitoringCollectorsApi'
import { useObjectRepositoryApi } from './api/useObjectRepositoryApi'
import { useCorrelationRulesetApi } from './api/useCorrelationRulesetApi'
import { useEntitlementsApi } from './api/useEntitlementsApi'
import { useDashboardDefinitionsApi } from './api/useDashboardDefinitionsApi'
import { useELKConnectionsApi } from './api/useELKConnectionsApi'
import { useDynatraceConnectionsApi } from './api/useDynatraceConnectionsApi'

export { ApiError }

function useApiServices() {
  const tests = useTestApi()
  const applications = useApplicationApi()
  const projects = useProjectApi()
  const repository = useRepositoryApi()
  const scripts = useScriptApi()
  const system = useSystemApi()
  const recorder = useRecorderApi()
  const browserRecorder = useBrowserRecorderApi()
  const library = useLibraryApi()
  const capacity = useCapacityApi()
  const fleet = useFleetApi()
  const executionPlans = useExecutionPlansApi()
  const monitoringCollectors = useMonitoringCollectorsApi()
  const objectRepositories = useObjectRepositoryApi()
  const correlationRulesets = useCorrelationRulesetApi()
  const entitlements = useEntitlementsApi()
  const dashboardDefinitions = useDashboardDefinitionsApi()
  const elkConnections = useELKConnectionsApi()
  const dynatraceConnections = useDynatraceConnectionsApi()

  // Spread order is preserved by mergeApiServiceGroups; the group names are the
  // labels a collision report points at.
  return useMemo(
    () =>
      mergeApiServiceGroups({
        tests,
        applications,
        projects,
        repository,
        scripts,
        system,
        recorder,
        browserRecorder,
        library,
        capacity,
        fleet,
        executionPlans,
        monitoringCollectors,
        objectRepositories,
        correlationRulesets,
		entitlements,
		dashboardDefinitions,
		elkConnections,
		dynatraceConnections,
      }),
    [tests, applications, projects, repository, scripts, system, recorder, browserRecorder, library, capacity, fleet, executionPlans, monitoringCollectors, objectRepositories, correlationRulesets, entitlements, dashboardDefinitions, elkConnections, dynatraceConnections],
  )
}

type ApiServices = ReturnType<typeof useApiServices>

const ApiContext = createContext<ApiServices | null>(null)

export function ApiProvider({ children }: PropsWithChildren) {
  const services = useApiServices()
  return createElement(ApiContext.Provider, { value: services }, children)
}

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
