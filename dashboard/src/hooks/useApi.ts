import { useMemo } from 'react'

import { ApiError } from './apiClient'
import { useApplicationApi } from './api/useApplicationApi'
import { useRecorderApi } from './api/useRecorderApi'
import { useProjectApi } from './api/useProjectApi'
import { useRepositoryApi } from './api/useRepositoryApi'
import { useScriptApi } from './api/useScriptApi'
import { useSystemApi } from './api/useSystemApi'
import { useTestApi } from './api/useTestApi'
import { useLibraryApi } from './api/useLibraryApi'

export { ApiError }

export function useApi() {
  const tests = useTestApi()
  const applications = useApplicationApi()
  const projects = useProjectApi()
  const repository = useRepositoryApi()
  const scripts = useScriptApi()
  const system = useSystemApi()
  const recorder = useRecorderApi()
  const library = useLibraryApi()

  return useMemo(
    () => ({
      ...tests,
      ...applications,
      ...projects,
      ...repository,
      ...scripts,
      ...system,
      ...recorder,
      ...library,
    }),
    [tests, applications, projects, repository, scripts, system, recorder, library],
  )
}
