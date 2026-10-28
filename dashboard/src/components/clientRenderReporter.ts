import { useEffect, useRef } from 'react'

import { ApiError, request } from '../hooks/apiClient'
import type { LiveMetrics } from '../types'
import { clientTelemetryReceiptResolved, clientTelemetryReceiptVisible, discardLoggedReceipts, getClientTelemetryReceipt, takeSupersededReceipts, type ClientTelemetryPublication, type ClientTelemetryReceipt } from './clientTelemetryReceipt'

// Client render evidence (C5). A visible tab reports its monotonic interval
// from local receipt to a post-commit animation-frame/timer callback. That is
// an advisory paint opportunity, not proof of visible presentation or an
// end-to-end SLA. The wire report and existing advisory stage stay compatible.
// When the controller names its publications (TEL-A), the publications a
// reporting tab receives are reported by identity, best effort: rendered,
// superseded (a newer one was rendered first) or background (received or
// painted while hidden). The run accounts every eligible publication for one
// reference tab; any that tab does not report is unreported there.

export const CLIENT_RENDER_FLUSH_MS = 3000
export const CLIENT_RENDER_MAX_SAMPLES = 64
export const CLIENT_RENDER_CALIBRATION_INTERVAL_MS = 30000

export interface ClientRenderClockCalibration {
  client_send_mono_ms: number
  client_recv_mono_ms: number
  controller_recv_unix_ms: number
  controller_send_unix_ms: number
}

export interface ClientRenderSample {
  receive_to_render_ms: number
  state: 'visible' | 'background' | 'superseded'
  observed_at: string
  publication?: ClientTelemetryPublication
  received_mono_ms?: number
  rendered_mono_ms?: number
  delivery_signal?: 'post_commit_paint_opportunity' | 'verified_frame_presentation'
}

// Wall time labels the observation only; it never measures the interval.
// Invalid/regressed monotonic readings remain unavailable, not measured zero.
export function renderSample(receivedMonotonicMs: number, callbackMonotonicMs: number, visible: boolean, observedAtMs: number, publication?: ClientTelemetryPublication): ClientRenderSample | null {
  if (!Number.isFinite(receivedMonotonicMs) || !Number.isFinite(callbackMonotonicMs)
    || receivedMonotonicMs < 0 || callbackMonotonicMs < receivedMonotonicMs
    || !Number.isFinite(new Date(observedAtMs).getTime())) return null
  const elapsedMs = Math.round((callbackMonotonicMs - receivedMonotonicMs) * 1000) / 1000
  if (!Number.isFinite(elapsedMs)) return null
  return {
    receive_to_render_ms: elapsedMs,
    state: visible ? 'visible' : 'background',
    observed_at: new Date(observedAtMs).toISOString(),
    ...(publication
      ? {
          publication,
          received_mono_ms: Math.round(receivedMonotonicMs * 1000) / 1000,
          rendered_mono_ms: Math.round(callbackMonotonicMs * 1000) / 1000,
          delivery_signal: 'post_commit_paint_opportunity' as const,
        }
      : {}),
  }
}

// publicationOutcomes reports, without a measurement, the publications that
// were never rendered on their own: superseded ones (background when received
// while hidden), and the receipt itself when it was received while hidden.
export function publicationOutcomes(superseded: ClientTelemetryReceipt[], observedAtMs: number, hidden?: ClientTelemetryReceipt): ClientRenderSample[] {
  const observedAt = new Date(observedAtMs).toISOString()
  const outcomes: ClientRenderSample[] = superseded
    .filter(receipt => receipt.publication)
    .map(receipt => ({ receive_to_render_ms: 0, state: clientTelemetryReceiptVisible(receipt) ? 'superseded' : 'background', observed_at: observedAt, publication: receipt.publication }))
  if (hidden?.publication) outcomes.push({ receive_to_render_ms: 0, state: 'background', observed_at: observedAt, publication: hidden.publication })
  return outcomes
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

export function postClientRenderReport(
  testId: string,
  clientID: string,
  samples: ClientRenderSample[],
  options?: { clientSendMonoMs?: number; calibration?: ClientRenderClockCalibration },
) {
  return request<{ accepted: number; excluded?: Record<string, number>; clock_calibration?: [number, number, number] }>(
    `/tests/${encodeURIComponent(testId)}/telemetry/client-render`,
    {
      method: 'POST',
      body: JSON.stringify({
        client_id: clientID,
        samples,
        ...(options?.clientSendMonoMs !== undefined ? { client_send_mono_ms: options.clientSendMonoMs } : {}),
        ...(options?.calibration ? { calibration: options.calibration } : {}),
      }),
    },
  )
}

// Only a locally observed receipt for this run can be sampled. A tab reports
// named publications' unmeasured outcomes only once it has measured a visible
// render, the condition for being the run's reference tab. Queue limits, a
// refused (409, 429) or failed report and receipts that arrived while reporting
// was off remain gaps, accounted as unreported for the reference tab.
export function useClientRenderReporter(testId: string, enabled: boolean, latestMetrics?: LiveMetrics) {
  const clientIDRef = useRef<string>('')
  const queueRef = useRef<ClientRenderSample[]>([])
  const stoppedForRef = useRef<string | null>(null)
  const sampledReceiptsRef = useRef(new WeakSet<ClientTelemetryReceipt>())
  const sessionRef = useRef<string | null>(null)
  const measuredRef = useRef(false)
  const lastCalibrationMonoRef = useRef<number | null>(null)
  const latestCalibrationRef = useRef<ClientRenderClockCalibration | undefined>(undefined)
  const active = enabled && Boolean(testId) && stoppedForRef.current !== testId
  const receipt = getClientTelemetryReceipt(latestMetrics?.telemetry)

  useEffect(() => {
    // A new reporting session (another run, or reporting turned on or off):
    // StrictMode's repeated effect for the same session changes nothing.
    const session = active ? testId : null
    if (sessionRef.current === session) return
    sessionRef.current = session
    queueRef.current = []
    measuredRef.current = false
    lastCalibrationMonoRef.current = null
    latestCalibrationRef.current = undefined
    if (session) discardLoggedReceipts(session, receipt)
  }, [testId, active, receipt])
  useEffect(() => {
    if (!active || !receipt || receipt.testId !== testId || clientTelemetryReceiptResolved(receipt) || sampledReceiptsRef.current.has(receipt)) return
    if (!clientTelemetryReceiptVisible(receipt)) {
      // A hidden receipt is not measured. One naming its publication is
      // reported as background, with the publications it superseded, once
      // this tab has measured a visible render.
      if (receipt.publication && measuredRef.current) {
        sampledReceiptsRef.current.add(receipt)
        for (const outcome of publicationOutcomes(takeSupersededReceipts(receipt), Date.now(), receipt)) {
          queueRef.current = enqueueRenderSample(queueRef.current, outcome)
        }
      }
      return
    }
    let timer: number | undefined
    const frame = requestAnimationFrame(() => {
      // A timer after the frame is a paint opportunity only; browsers do
      // not guarantee that a visible frame was presented at this boundary.
      timer = window.setTimeout(() => {
        const sample = renderSample(receipt.monotonicMs, performance.now(), clientTelemetryReceiptVisible(receipt), Date.now(), receipt.publication)
        if (sample && sample.state !== 'visible' && receipt.publication && !measuredRef.current) {
          // Hidden before it painted, in a tab that has measured nothing: left
          // logged, so a later measurement reports it as background.
          sampledReceiptsRef.current.add(receipt)
          return
        }
        if (sample) {
          sampledReceiptsRef.current.add(receipt)
          for (const outcome of publicationOutcomes(takeSupersededReceipts(receipt), Date.now())) {
            queueRef.current = enqueueRenderSample(queueRef.current, outcome)
          }
          queueRef.current = enqueueRenderSample(queueRef.current, sample)
          if (sample.state === 'visible') measuredRef.current = true
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
      const hasNamedSample = samples.some(sample => Boolean(sample.publication))
      const nowMono = Math.round(performance.now() * 1000) / 1000
      const canCalibrate = hasNamedSample && (
        lastCalibrationMonoRef.current === null
        || nowMono - lastCalibrationMonoRef.current >= CLIENT_RENDER_CALIBRATION_INTERVAL_MS
      )
      if (canCalibrate) lastCalibrationMonoRef.current = nowMono
      const pendingRefinement = !canCalibrate ? latestCalibrationRef.current : undefined
      if (pendingRefinement) latestCalibrationRef.current = undefined
      postClientRenderReport(
        testId,
        clientIDRef.current,
        samples,
        hasNamedSample
          ? {
              ...(canCalibrate ? { clientSendMonoMs: nowMono } : {}),
              ...(pendingRefinement ? { calibration: pendingRefinement } : {}),
            }
          : undefined,
      )
        .then(res => {
          const echo = res?.clock_calibration
          if (Array.isArray(echo) && echo.length === 3 && echo.every(v => typeof v === 'number' && Number.isFinite(v))) {
            latestCalibrationRef.current = {
              client_send_mono_ms: echo[0],
              client_recv_mono_ms: Math.round(performance.now() * 1000) / 1000,
              controller_recv_unix_ms: echo[1],
              controller_send_unix_ms: echo[2],
            }
          }
        })
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
