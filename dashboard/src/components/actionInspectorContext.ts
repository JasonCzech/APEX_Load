import { createContext } from 'react'

export const OpenActionBody = createContext<(() => void) | undefined>(undefined)
