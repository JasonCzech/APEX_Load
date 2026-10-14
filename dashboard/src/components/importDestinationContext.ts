import { createContext, useContext, type ReactNode } from 'react'

// The save destination an import surface offers. The import review shows it
// at its Save step, so the page does not ask where to save before there is
// anything to save.
export const ImportDestinationContext = createContext<ReactNode>(null)

export function useImportDestination(): ReactNode {
  return useContext(ImportDestinationContext)
}
