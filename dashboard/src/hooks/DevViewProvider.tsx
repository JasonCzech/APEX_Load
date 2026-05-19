import { ReactNode, createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'

import {
  DEV_VIEW_MODE_CHANGED_EVENT,
  DEV_VIEW_STORAGE_KEY,
  DevViewMode,
  readStoredDevViewMode,
  writeStoredDevViewMode,
} from './apiClient'

type DevViewContextValue = {
  mode: DevViewMode
  setMode: (mode: DevViewMode) => void
}

const DevViewContext = createContext<DevViewContextValue | null>(null)

export function DevViewProvider({ children }: { children: ReactNode }) {
  const [mode, setModeState] = useState<DevViewMode>(() => readStoredDevViewMode())

  const setMode = useCallback((nextMode: DevViewMode) => {
    writeStoredDevViewMode(nextMode)
    setModeState(readStoredDevViewMode())
  }, [])

  useEffect(() => {
    const syncMode = () => setModeState(readStoredDevViewMode())
    const handleStorage = (event: StorageEvent) => {
      if (event.key === DEV_VIEW_STORAGE_KEY) {
        syncMode()
      }
    }
    window.addEventListener(DEV_VIEW_MODE_CHANGED_EVENT, syncMode)
    window.addEventListener('storage', handleStorage)
    return () => {
      window.removeEventListener(DEV_VIEW_MODE_CHANGED_EVENT, syncMode)
      window.removeEventListener('storage', handleStorage)
    }
  }, [])

  const value = useMemo(() => ({ mode, setMode }), [mode, setMode])
  return <DevViewContext.Provider value={value}>{children}</DevViewContext.Provider>
}

export function useDevView(): DevViewContextValue {
  const value = useContext(DevViewContext)
  if (value) {
    return value
  }
  return {
    mode: readStoredDevViewMode(),
    setMode: writeStoredDevViewMode,
  }
}
