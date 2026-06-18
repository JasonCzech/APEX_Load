import { useMemo } from 'react'

import { ApiError } from './apiClient'
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

export { ApiError }

export function useApi() {
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

  return useMemo(
    () => ({
      ...tests,
      ...applications,
      ...projects,
      ...repository,
      ...scripts,
      ...system,
      ...recorder,
      ...browserRecorder,
      ...library,
      ...capacity,
      ...fleet,
      ...executionPlans,
      ...monitoringCollectors,
      ...objectRepositories,
      ...correlationRulesets,
      ...entitlements,
    }),
    [tests, applications, projects, repository, scripts, system, recorder, browserRecorder, library, capacity, fleet, executionPlans, monitoringCollectors, objectRepositories, correlationRulesets, entitlements],
  )
}
