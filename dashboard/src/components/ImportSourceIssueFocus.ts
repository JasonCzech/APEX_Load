import { createContext } from 'react'

export interface ImportSourceIssueFocus {
  revision: number
  title: string
  source: string
  description: string
}

export const ImportSourceIssueFocusContext = createContext<ImportSourceIssueFocus | null>(null)
