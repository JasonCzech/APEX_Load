import { createContext, useContext, useEffect, type ReactNode } from 'react'

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
  const name = importFolderNameFromSource(sourceName)
  useEffect(() => {
    suggest?.(name)
  }, [suggest, name])
}

/** "checkout_plan.jmx" → "checkout plan": a source file's name as a folder name. */
export function importFolderNameFromSource(sourceName: string | undefined): string {
  const base = (sourceName || '').split(/[\\/]/).filter(Boolean).pop() || ''
  return base
    .replace(/\.(?:tar\.gz|tgz)$/i, '')
    .replace(/\.[^.]+$/, '')
    // The suffix an exporter adds before the extension (Orders.postman_collection.json).
    .replace(/\.(?:postman_collection|postman_environment|openapi|swagger|asyncapi|bruno|har)$/i, '')
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
}
