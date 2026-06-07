import { createContext, useContext } from 'react'

import {
  DevViewMode,
  readStoredDevViewMode,
  writeStoredDevViewMode,
} from './apiClient'

export type DevViewContextValue = {
  mode: DevViewMode
  setMode: (mode: DevViewMode) => void
}

export const DevViewContext = createContext<DevViewContextValue | null>(null)

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
