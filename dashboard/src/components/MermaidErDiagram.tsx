import type { ReactNode } from 'react'
import EntityDiagram from './EntityDiagram'
import { parseErDiagram, type EntityDiagramSpec } from './entityDiagramModel'

// Parsed once per source so the diagram keeps its layout across re-renders.
// Bounded (least recently used first out): note previews re-render on every
// keystroke, and each edit is a new source.
const ER_DIAGRAM_CACHE_SIZE = 32
const erDiagrams = new Map<string, EntityDiagramSpec | null>()
function erDiagramFor(code: string): EntityDiagramSpec | null {
  const spec = erDiagrams.has(code) ? erDiagrams.get(code)! : parseErDiagram(code)
  erDiagrams.delete(code)
  erDiagrams.set(code, spec)
  if (erDiagrams.size > ER_DIAGRAM_CACHE_SIZE) erDiagrams.delete(erDiagrams.keys().next().value!)
  return spec
}

/** Draws a Mermaid erDiagram natively, or renders `fallback` (its source) when it does not parse. */
export default function MermaidErDiagram({ code, label, fallback }: { code: string; label?: string; fallback: ReactNode }) {
  const diagram = erDiagramFor(code)
  return diagram ? <EntityDiagram spec={diagram} label={label} /> : <>{fallback}</>
}
