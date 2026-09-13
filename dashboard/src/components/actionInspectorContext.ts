import { createContext } from 'react'

export const ManagedActionChecks = createContext(false)
export const OpenActionBody = createContext<(() => void) | undefined>(undefined)
