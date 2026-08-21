import { useCallback, useRef, useState } from 'react'

/**
 * Owns the review generation counter every import wizard uses to invalidate a
 * conversion in flight.
 *
 * Each conversion, credential migration, or parameterization apply captures the
 * generation it started under. A late response compares its captured value
 * against the ref — not the state — so a stale request cannot overwrite a newer
 * review even when React has not re-rendered yet. That is why the ref is
 * returned alongside the state value rather than derived from it.
 *
 * The three wizards that need this carried a character-identical copy each.
 *
 * The ref is returned raw, not behind a getter, because callers also write to
 * it: an unmount or a completed persistence bumps `generationRef.current`
 * directly to fence a response in flight *without* triggering a re-render, which
 * `advance` would. Replacing it with a read-only accessor would silently break
 * that fencing.
 *
 * generationRef is a useRef result, so its identity is stable for the lifetime
 * of the component. Listing it in an effect's dependencies is inert; it is there
 * because the exhaustive-deps rule cannot see that a ref returned from a custom
 * hook is the same object every render.
 */
export function useImportReviewGeneration() {
  const generationRef = useRef(0)
  const [generation, setGeneration] = useState(0)
  const advance = useCallback(() => {
    const next = generationRef.current + 1
    generationRef.current = next
    setGeneration(next)
    return next
  }, [])
  return { generation, generationRef, advance }
}
