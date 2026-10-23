import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { DIAGRAM, layoutErDiagram, type EntityDiagramSpec } from './entityDiagramModel'
import './EntityDiagram.css'

// Used for layout before the container is measured (and in tests).
const DEFAULT_WIDTH = 880
// Below this the text would shrink past legibility, so the figure scrolls.
const MIN_DRAWN_WIDTH = 300

/** Draws a parsed Mermaid erDiagram with the dashboard theme. */
export default function EntityDiagram({ spec, label }: { spec: EntityDiagramSpec; label?: string }) {
  const ref = useRef<HTMLElement>(null)
  const [width, setWidth] = useState(DEFAULT_WIDTH)
  const [active, setActive] = useState<string | null>(null)
  const markerID = `entity-diagram-arrow-${useId().replace(/[^\w-]/g, '')}`

  useEffect(() => {
    const figure = ref.current
    if (!figure || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(([entry]) => {
      const measured = Math.floor(entry.contentRect.width)
      if (measured > 0) setWidth(measured)
    })
    observer.observe(figure)
    return () => observer.disconnect()
  }, [])

  const layout = useMemo(() => layoutErDiagram(spec, width), [spec, width])
  const references = useMemo(() => {
    const byColumn = new Map<string, string>()
    for (const relationship of spec.relationships) byColumn.set(`${relationship.child}.${relationship.label}`, relationship.parent)
    return byColumn
  }, [spec])
  const tables = spec.entities.filter(entity => !entity.stub).length
  const summary = `${label ? `${label}: ` : ''}entity relationship diagram of ${tables} ${tables === 1 ? 'table' : 'tables'} and ${spec.relationships.length} ${spec.relationships.length === 1 ? 'relationship' : 'relationships'}`
  const touches = (parent: string, child: string) => active !== null && (parent === active || child === active)
  const minWidth = Math.min(layout.width, MIN_DRAWN_WIDTH)
  // Like Markdown tables, the figure is a focusable region only while it scrolls.
  const scrollable = width < minWidth

  return (
    <figure ref={ref} className="entity-diagram" {...(scrollable ? { tabIndex: 0, role: 'region', 'aria-label': summary } : {})}>
      <svg
        role="img"
        aria-label={summary}
        viewBox={`0 0 ${layout.width} ${layout.height}`}
        style={{ maxWidth: layout.width, minWidth }}
        className={active ? 'has-active' : undefined}
      >
        <defs>
          {/* One marker per line state, each filled from a theme token. */}
          {['', '-active'].map(suffix => (
            <marker key={suffix} id={`${markerID}${suffix}`} className={suffix ? 'is-active' : undefined} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" />
            </marker>
          ))}
        </defs>
        <g className="entity-diagram-edges">
          {layout.edges.map(({ relationship, path }, index) => {
            const highlighted = touches(relationship.parent, relationship.child)
            return (
            <path
              key={index}
              d={path}
              markerEnd={`url(#${markerID}${highlighted ? '-active' : ''})`}
              className={[relationship.logical ? 'is-logical' : '', highlighted ? 'is-active' : ''].filter(Boolean).join(' ') || undefined}
            >
              <title>{`${relationship.child}.${relationship.label} → ${relationship.parent}${relationship.logical ? ' (no constraint)' : ''}`}</title>
            </path>
            )
          })}
        </g>
        {layout.nodes.map(({ entity, x, y, width: boxWidth, height }) => (
          <g
            key={entity.name}
            className={`entity-diagram-node${entity.stub ? ' is-stub' : ''}${active === entity.name ? ' is-active' : ''}`}
            transform={`translate(${x},${y})`}
            onMouseEnter={() => setActive(entity.name)}
            onMouseLeave={() => setActive(current => current === entity.name ? null : current)}
          >
            <rect className="entity-diagram-box" width={boxWidth} height={height} rx="5" />
            {!entity.stub && (height > DIAGRAM.headerHeight
              ? <path className="entity-diagram-header" d={`M0,5 a5,5 0 0 1 5,-5 h${boxWidth - 10} a5,5 0 0 1 5,5 v${DIAGRAM.headerHeight - 5} h${-boxWidth} z`} />
              : <rect className="entity-diagram-header" width={boxWidth} height={height} rx="5" />)}
            <text className="entity-diagram-name" x={boxWidth / 2} y={entity.stub ? DIAGRAM.stubHeight / 2 + 4 : 17} textAnchor="middle">{entity.name}</text>
            {entity.attributes.map((attribute, index) => {
              const rowY = DIAGRAM.headerHeight + index * DIAGRAM.rowHeight + 13
              const target = references.get(`${entity.name}.${attribute.name}`)
              return (
                <g key={attribute.name}>
                  <text className="entity-diagram-column" x={DIAGRAM.paddingX} y={rowY}>
                    {attribute.name}
                    <title>{`${attribute.name} (${attribute.type})${target ? ` → ${target}` : ''}`}</title>
                  </text>
                  {attribute.keys.length > 0
                    ? <text className="entity-diagram-key" x={boxWidth - DIAGRAM.paddingX} y={rowY} textAnchor="end">{attribute.keys.join(' ')}</text>
                    : attribute.comment && <text className="entity-diagram-note" x={boxWidth - DIAGRAM.paddingX} y={rowY} textAnchor="end">{attribute.comment}</text>}
                </g>
              )
            })}
            {entity.hiddenColumns !== undefined && (
              <text className="entity-diagram-note" x={DIAGRAM.paddingX} y={DIAGRAM.headerHeight + entity.attributes.length * DIAGRAM.rowHeight + 13}>
                {`+${entity.hiddenColumns} more ${entity.hiddenColumns === 1 ? 'column' : 'columns'}`}
              </text>
            )}
          </g>
        ))}
      </svg>
    </figure>
  )
}
