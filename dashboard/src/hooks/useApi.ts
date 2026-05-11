import { useMemo } from 'react'

import { ApiError } from './apiClient'
import { useApplicationApi } from './api/useApplicationApi'
import { useRecorderApi } from './api/useRecorderApi'
import { useProjectApi } from './api/useProjectApi'
import { useScriptApi } from './api/useScriptApi'
import { useSystemApi } from './api/useSystemApi'
import { useTestApi } from './api/useTestApi'

export { ApiError }

export function useApi() {
  const tests = useTestApi()
  const applications = useApplicationApi()
  const projects = useProjectApi()
  const scripts = useScriptApi()
  const system = useSystemApi()
  const recorder = useRecorderApi()

  return useMemo(
    () => ({
      ...tests,
      ...applications,
      ...projects,
      ...scripts,
      ...system,
      ...recorder,
    }),
    [tests, applications, projects, scripts, system, recorder],
  )
}
