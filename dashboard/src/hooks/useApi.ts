import { useMemo } from 'react'

import { ApiError } from './apiClient'
import { useRecorderApi } from './api/useRecorderApi'
import { useScriptApi } from './api/useScriptApi'
import { useSystemApi } from './api/useSystemApi'
import { useTestApi } from './api/useTestApi'

export { ApiError }

export function useApi() {
  const tests = useTestApi()
  const scripts = useScriptApi()
  const system = useSystemApi()
  const recorder = useRecorderApi()

  return useMemo(
    () => ({
      ...tests,
      ...scripts,
      ...system,
      ...recorder,
    }),
    [tests, scripts, system, recorder],
  )
}

