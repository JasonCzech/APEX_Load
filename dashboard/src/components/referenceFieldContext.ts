import { createContext, useContext } from 'react'

import type { ReferenceScope } from './referenceScopeModel'

/** A field's place in the selected step, as JSON path segments under the step ("target", ["headers", "Authorization"]). */
export type FieldPath = Array<string | number>

/**
 * What reference fields in the inspector share: the selected step's scope,
 * and the editor's handlers for the fixes they offer.
 */
export interface ReferenceFieldContextValue {
  scope: ReferenceScope | null
  /** Turns the field's value (or the selected part) into a parameter, reviewing other places with the same text. */
  onMakeParameter?: (request: { field: FieldPath; value: string; from: number; to: number }) => void
  /** Adds an extractor with this name to the nearest earlier step that has a response. */
  onAddExtractor?: (name: string) => void
  /** Adds an empty parameter for this name; returns the name it got (names are normalized), or null. */
  onCreateParameter?: (name: string) => string | null
  /** The last focused reference field registers how to insert into it, for "This step produces". */
  registerInsertTarget?: (insert: ((expression: string) => void) | null) => void
  /** A field going away releases its registration, if it is still the registered one. */
  releaseInsertTarget?: (insert: (expression: string) => void) => void
}

export const ReferenceFieldContext = createContext<ReferenceFieldContextValue>({ scope: null })

export function useReferenceFieldContext(): ReferenceFieldContextValue {
  return useContext(ReferenceFieldContext)
}
