import { createContext, useContext, useEffect, type ReactNode } from 'react'

import { importSourceLabel } from './importSourceLabel'

// The save destination an import surface offers. The import review shows it
// at its Save step, so the page does not ask where to save before there is
// anything to save.
export const ImportDestinationContext = createContext<ReactNode>(null)

export function useImportDestination(): ReactNode {
  return useContext(ImportDestinationContext)
}

// Lets the active importer offer its source's name (a test plan's or
// collection's file, an archive) for the folder the destination can create.
export const ImportFolderSuggestionContext = createContext<((name: string) => void) | null>(null)

/** Offers the folder name sourceName suggests; no source clears the offer. */
export function useSuggestImportFolderName(sourceName: string | undefined) {
  const suggest = useContext(ImportFolderSuggestionContext)
  const name = importSourceLabel(sourceName)
  useEffect(() => {
    suggest?.(name)
  }, [suggest, name])
}
