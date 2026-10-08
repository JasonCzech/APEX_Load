import { useEffect, useRef } from 'react'

import { ApiError, request } from '../hooks/apiClient'
import type { LiveMetrics } from '../types'

// Client render evidence (C5). While a run's project opts in, a dashboard tab
// viewing the live run measures each publication from receipt to the first
// paint after it rendered, and reports batches of those samples. The
// controller counts only a visible tab's renders toward the run's advisory
// browser_render stage; nothing here ever affects the run.

export const CLIENT_RENDER_FLUSH_MS = 3000
export const CLIENT_RENDER_MAX_SAMPLES = 64

export interface ClientRenderSample {
  receive_to_render_ms: number
  state: 'visible' | 'background'
  observed_at: string
}

// renderSample is one measured publication: receivedAtMs is when the tab
// received it (wall clock), renderedAtMs when it painted.
export function renderSample(receivedAtMs: number, renderedAtMs: number, visible: boolean): ClientRenderSample | null {
  if (!Number.isFinite(receivedAtMs) || !Number.isFinite(renderedAtMs)) return null
  return {
    receive_to_render_ms: Math.max(0, Math.round((renderedAtMs - receivedAtMs) * 1000) / 1000),
    state: visible ? 'visible' : 'background',
    observed_at: new Date(renderedAtMs).toISOString(),
  }
}

// enqueueRenderSample keeps the newest samples, at most one report's worth.
export function enqueueRenderSample(queue: ClientRenderSample[], sample: ClientRenderSample): ClientRenderSample[] {
  const next = [...queue, sample]
  return next.length > CLIENT_RENDER_MAX_SAMPLES ? next.slice(next.length - CLIENT_RENDER_MAX_SAMPLES) : next
}

function newClientID(): string {
  try {
    if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  } catch {
    // Fall through to a non-cryptographic tab identity: it only scopes the
    // controller's per-tab rate limit.
  }
  return `tab-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

function tabVisible(): boolean {
  return typeof document === 'undefined' || document.visibilityState === 'visible'
}

export function postClientRenderReport(testId: string, clientID: string, samples: ClientRenderSample[]) {
  return request<{ accepted: number; excluded?: Record<string, number> }>(
    `/tests/${encodeURIComponent(testId)}/telemetry/client-render`,
    { method: 'POST', body: JSON.stringify({ client_id: clientID, samples }) },
  )
}

// useClientRenderReporter measures and reports receive-to-render for each
// live publication of testId while enabled. A publication received while the
// tab was hidden is not measured (a hidden tab does not paint); one that
// became hidden before painting is reported as background, which the
// controller excludes. A 409 (the project turned the setting off, or the run
// ended) stops reporting for this run.
export function useClientRenderReporter(testId: string, enabled: boolean, latestMetrics?: LiveMetrics) {
  const clientIDRef = useRef<string>('')
  const queueRef = useRef<ClientRenderSample[]>([])
  const stoppedForRef = useRef<string | null>(null)
  const active = enabled && Boolean(testId) && stoppedForRef.current !== testId

  useEffect(() => {
    queueRef.current = []
  }, [testId])

  const receivedAt = latestMetrics?.telemetry?.client_received_at
  useEffect(() => {
    if (!active || !receivedAt || !tabVisible()) return
    const receivedAtMs = Date.parse(receivedAt)
    let timer: number | undefined
    const frame = requestAnimationFrame(() => {
      // The frame that rendered this publication paints after this callback;
      // a zero-delay task runs once it has.
      timer = window.setTimeout(() => {
        const sample = renderSample(receivedAtMs, Date.now(), tabVisible())
        if (sample) queueRef.current = enqueueRenderSample(queueRef.current, sample)
      }, 0)
    })
    return () => {
      cancelAnimationFrame(frame)
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [active, receivedAt])

  useEffect(() => {
    if (!active) return
    if (!clientIDRef.current) clientIDRef.current = newClientID()
    let inFlight = false
    const interval = window.setInterval(() => {
      if (inFlight || queueRef.current.length === 0) return
      const samples = queueRef.current
      queueRef.current = []
      inFlight = true
      postClientRenderReport(testId, clientIDRef.current, samples)
        .catch((error: unknown) => {
          if (error instanceof ApiError && error.status === 409) stoppedForRef.current = testId
          // Other failures drop the batch: render evidence is best-effort.
        })
        .finally(() => {
          inFlight = false
        })
    }, CLIENT_RENDER_FLUSH_MS)
    return () => window.clearInterval(interval)
  }, [active, testId])
}
