import { memo, useEffect, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react'
import { dominantPhase, isTimed, walk } from './analysis'
import { clampViewport, formatTickMs, niceTickStep, panViewport, zoomViewport, type WaterfallViewport } from './axis'
import type { WaterfallNode } from './types'

const MAX_MINIMAP_BARS = 800

type Drag = { mode: 'move'; originMs: number; start: WaterfallViewport } | { mode: 'brush'; anchorMs: number; currentMs: number }

/** Overview strip: every leaf at full-span scale, with a draggable window and brush-to-zoom. */
function WaterfallMinimapInner({
  roots,
  spanMs,
  viewport,
  onViewport,
  disabled = false,
}: {
  roots: readonly WaterfallNode<unknown>[]
  spanMs: number
  viewport: WaterfallViewport
  onViewport: (next: WaterfallViewport) => void
  /** Stacked mode has no time axis: the strip shows the run but does not zoom. */
  disabled?: boolean
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [drag, setDrag] = useState<Drag | null>(null)
  const total = Math.max(spanMs, 1)

  const bars = useMemo(() => {
    type Bar = { id: string; left: number; width: number; cls: string; failed: boolean; ms: number }
    const leaves: Bar[] = []
    walk(roots, n => {
      if (n.children.length > 0 || !isTimed(n) || n.kind === 'marker') return
      const dom = dominantPhase(n)
      leaves.push({
        id: n.id,
        left: (n.startMs / total) * 100,
        width: (n.durationMs / total) * 100,
        cls: n.status === 'failed' ? 'failed' : n.kind === 'think' ? 'think' : `phase-${dom?.key ?? 'total'}`,
        failed: n.status === 'failed',
        ms: n.durationMs,
      })
    })
    if (leaves.length <= MAX_MINIMAP_BARS) return leaves
    // Downsample across the whole span (not the first N): one bar per time bin,
    // preferring failures, then the longest leaf.
    const bins = new Map<number, Bar>()
    for (const leaf of leaves) {
      const bin = Math.min(MAX_MINIMAP_BARS - 1, Math.floor((leaf.left / 100) * MAX_MINIMAP_BARS))
      const kept = bins.get(bin)
      if (!kept || (leaf.failed && !kept.failed) || (leaf.failed === kept.failed && leaf.ms > kept.ms)) bins.set(bin, leaf)
    }
    return [...bins.entries()].sort((x, y) => x[0] - y[0]).map(([, v]) => v)
  }, [roots, total])

  const msAt = (clientX: number) => {
    const rect = ref.current?.getBoundingClientRect()
    if (!rect || rect.width <= 0) return 0
    return Math.min(total, Math.max(0, ((clientX - rect.left) / rect.width) * total))
  }

  const onPointerDown = (e: PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || disabled) return
    const ms = msAt(e.clientX)
    e.currentTarget.setPointerCapture?.(e.pointerId)
    if (ms >= viewport.startMs && ms <= viewport.endMs && !(viewport.startMs === 0 && viewport.endMs >= total)) setDrag({ mode: 'move', originMs: ms, start: viewport })
    else setDrag({ mode: 'brush', anchorMs: ms, currentMs: ms })
  }
  const onPointerMove = (e: PointerEvent<HTMLDivElement>) => {
    if (!drag) return
    const ms = msAt(e.clientX)
    if (drag.mode === 'move') onViewport(panViewport(drag.start, ms - drag.originMs, total))
    else setDrag({ ...drag, currentMs: ms })
  }
  const onPointerUp = (e: PointerEvent<HTMLDivElement>) => {
    if (!drag) return
    if (drag.mode === 'brush') {
      const a = Math.min(drag.anchorMs, drag.currentMs)
      const b = Math.max(drag.anchorMs, drag.currentMs)
      if (b - a > total * 0.005) onViewport(clampViewport({ startMs: a, endMs: b }, total))
      else {
        // A click recentres the current window on that point.
        const width = viewport.endMs - viewport.startMs
        onViewport(clampViewport({ startMs: a - width / 2, endMs: a + width / 2 }, total))
      }
    }
    e.currentTarget.releasePointerCapture?.(e.pointerId)
    setDrag(null)
  }

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const onWheel = (e: WheelEvent) => {
      // Sideways swipes belong to the page or dock, never a zoom.
      if (disabled || Math.abs(e.deltaX) >= Math.abs(e.deltaY)) return
      const rect = el.getBoundingClientRect()
      const ms = rect.width > 0 ? ((e.clientX - rect.left) / rect.width) * total : 0
      const width = viewport.endMs - viewport.startMs
      const anchor = width > 0 ? (ms - viewport.startMs) / width : 0.5
      const next = zoomViewport(viewport, e.deltaY > 0 ? 1.25 : 0.8, anchor, total)
      // At a zoom limit the wheel belongs to the page or dock scroll.
      if (Math.abs(next.startMs - viewport.startMs) < 1e-9 && Math.abs(next.endMs - viewport.endMs) < 1e-9) return
      e.preventDefault()
      onViewport(next)
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [onViewport, total, viewport, disabled])

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (disabled) return
    const width = viewport.endMs - viewport.startMs
    let next: WaterfallViewport | null = null
    if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') next = panViewport(viewport, -width * 0.1, total)
    else if (e.key === 'ArrowRight' || e.key === 'ArrowUp') next = panViewport(viewport, width * 0.1, total)
    else if (e.key === 'PageDown') next = panViewport(viewport, -width, total)
    else if (e.key === 'PageUp') next = panViewport(viewport, width, total)
    else if (e.key === 'Home') next = panViewport(viewport, -total, total)
    else if (e.key === 'End') next = panViewport(viewport, total, total)
    else if (e.key === '+' || e.key === '=') next = zoomViewport(viewport, 0.8, 0.5, total)
    else if (e.key === '-' || e.key === '_') next = zoomViewport(viewport, 1.25, 0.5, total)
    if (next) {
      e.preventDefault()
      onViewport(next)
    }
  }

  const brush = drag?.mode === 'brush' && Math.abs(drag.currentMs - drag.anchorMs) > 0 ? { a: Math.min(drag.anchorMs, drag.currentMs), b: Math.max(drag.anchorMs, drag.currentMs) } : null
  const step = niceTickStep(viewport.endMs - viewport.startMs)
  const valueText = `Showing ${formatTickMs(viewport.startMs, step)} to ${formatTickMs(viewport.endMs, step)} of ${formatTickMs(total, niceTickStep(total))}`
  const lanes = Math.max(1, Math.min(bars.length, 12))
  return (
    <div className="wf-minimap" ref={ref} onPointerDown={onPointerDown} onPointerMove={onPointerMove} onPointerUp={onPointerUp} onPointerCancel={() => setDrag(null)}>
      <div className="wf-minimap-bars" aria-hidden="true">
        {bars.map((b, i) => (
          <span key={b.id} className={`wf-minimap-bar ${b.cls}`} style={{ left: `${b.left}%`, width: `max(1px, ${b.width}%)`, top: `${((i % lanes) / lanes) * 100}%`, height: `${100 / lanes}%` }} />
        ))}
      </div>
      {brush && <span className="wf-minimap-brush" aria-hidden="true" style={{ left: `${(brush.a / total) * 100}%`, width: `${((brush.b - brush.a) / total) * 100}%` }} />}
      <div
        className="wf-minimap-window"
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-disabled={disabled || undefined}
        aria-label="Visible time range"
        aria-valuemin={0}
        aria-valuemax={Math.round(total)}
        aria-valuenow={Math.round(viewport.startMs)}
        aria-valuetext={valueText}
        onKeyDown={onKeyDown}
        // The 4px minimum width must stay inside the clipped track.
        style={{ left: `min(${(viewport.startMs / total) * 100}%, calc(100% - 4px))`, width: `${((viewport.endMs - viewport.startMs) / total) * 100}%` }}
      />
    </div>
  )
}

/** Memoized: hovering the rows re-renders the waterfall at pointer rate. */
export const WaterfallMinimap = memo(WaterfallMinimapInner)
