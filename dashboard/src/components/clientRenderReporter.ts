import { useEffect, useRef } from 'react'

import { ApiError, request } from '../hooks/apiClient'
import type { LiveMetrics } from '../types'
import { clientTelemetryReceiptVisible, getClientTelemetryReceipt, type ClientTelemetryReceipt } from './clientTelemetryReceipt'

// Client render evidence (C5). A visible tab reports its monotonic interval
// from local receipt to a post-commit animation-frame/timer callback. That is
// an advisory paint opportunity, not proof of visible presentation or an
// end-to-end SLA. The wire report and existing advisory stage stay compatible.

export const CLIENT_RENDER_FLUSH_MS = 3000
export const CLIENT_RENDER_MAX_SAMPLES = 64

export interface ClientRenderSample {
  receive_to_render_ms: number
  state: 'visible' | 'background'
  observed_at: string
}

// Wall time labels the observation only; it never measures the interval.
// Invalid/regressed monotonic readings remain unavailable, not measured zero.
export function renderSample(receivedMonotonicMs: number, callbackMonotonicMs: number, visible: boolean, observedAtMs: number): ClientRenderSample | null {
  if (!Number.isFinite(receivedMonotonicMs) || !Number.isFinite(callbackMonotonicMs)
    || receivedMonotonicMs < 0 || callbackMonotonicMs < receivedMonotonicMs
    || !Number.isFinite(new Date(observedAtMs).getTime())) return null
  const elapsedMs = Math.round((callbackMonotonicMs - receivedMonotonicMs) * 1000) / 1000
  if (!Number.isFinite(elapsedMs)) return null
  return {
    receive_to_render_ms: elapsedMs,
    state: visible ? 'visible' : 'background',
    observed_at: new Date(observedAtMs).toISOString(),
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

export function postClientRenderReport(testId: string, clientID: string, samples: ClientRenderSample[]) {
  return request<{ accepted: number; excluded?: Record<string, number> }>(
    `/tests/${encodeURIComponent(testId)}/telemetry/client-render`,
    { method: 'POST', body: JSON.stringify({ client_id: clientID, samples }) },
  )
}

// Only a locally observed receipt for this run can be sampled. Hidden
// receipts, superseded callbacks and failed reports remain best-effort gaps;
// this reporter does not establish the complete eligible-publication population.
export function useClientRenderReporter(testId: string, enabled: boolean, latestMetrics?: LiveMetrics) {
  const clientIDRef = useRef<string>('')
  const queueRef = useRef<ClientRenderSample[]>([])
  const stoppedForRef = useRef<string | null>(null)
  const sampledReceiptsRef = useRef(new WeakSet<ClientTelemetryReceipt>())
  const active = enabled && Boolean(testId) && stoppedForRef.current !== testId

  useEffect(() => {
    queueRef.current = []
  }, [testId, active])

  const receipt = getClientTelemetryReceipt(latestMetrics?.telemetry)
  useEffect(() => {
    if (!active || !receipt || receipt.testId !== testId || !clientTelemetryReceiptVisible(receipt) || sampledReceiptsRef.current.has(receipt)) return
    let timer: number | undefined
    const frame = requestAnimationFrame(() => {
      // A timer after the frame is a paint opportunity only; browsers do
      // not guarantee that a visible frame was presented at this boundary.
      timer = window.setTimeout(() => {
        const sample = renderSample(receipt.monotonicMs, performance.now(), clientTelemetryReceiptVisible(receipt), Date.now())
        if (sample) {
          sampledReceiptsRef.current.add(receipt)
          queueRef.current = enqueueRenderSample(queueRef.current, sample)
        }
      }, 0)
    })
    return () => {
      cancelAnimationFrame(frame)
      if (timer !== undefined) window.clearTimeout(timer)
    }
  }, [active, receipt, testId])

  useEffect(() => {
    if (!active) return
    if (!clientIDRef.current) clientIDRef.current = newClientID()
    let inFlight = false
    let cancelled = false
    const interval = window.setInterval(() => {
      if (inFlight || stoppedForRef.current === testId || queueRef.current.length === 0) return
      const samples = queueRef.current
      queueRef.current = []
      inFlight = true
      postClientRenderReport(testId, clientIDRef.current, samples)
        .catch((error: unknown) => {
          if (!cancelled && error instanceof ApiError && error.status === 409) stoppedForRef.current = testId
          // Other failures drop the batch: render evidence is best-effort.
        })
        .finally(() => {
          inFlight = false
        })
    }, CLIENT_RENDER_FLUSH_MS)
    return () => {
      cancelled = true
      window.clearInterval(interval)
    }
  }, [active, testId])
}
