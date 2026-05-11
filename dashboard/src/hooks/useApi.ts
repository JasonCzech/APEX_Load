import { useMemo } from 'react'

import { ApiError } from './apiClient'
import { useRecorderApi } from './api/useRecorderApi'
import { useProjectApi } from './api/useProjectApi'
import { useScriptApi } from './api/useScriptApi'
import { useSystemApi } from './api/useSystemApi'
import { useTestApi } from './api/useTestApi'

export { ApiError }

export function useApi() {
  const tests = useTestApi()
  const projects = useProjectApi()
  const scripts = useScriptApi()
  const system = useSystemApi()
  const recorder = useRecorderApi()

  return useMemo(
    () => ({
      ...tests,
      ...projects,
      ...scripts,
      ...system,
      ...recorder,
    }),
    [tests, projects, scripts, system, recorder],
  )
}
