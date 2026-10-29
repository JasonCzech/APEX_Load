import { useCallback, useEffect, useRef, useState } from 'react'

// 'ingesting' and 'accepted' are the drop animation, driven by begin/succeed/fail.
// 'working' is different: the host owns it. It lasts exactly as long as the host
// reports work on the file the zone holds (see DropIngestOptions.workingLabel).
export type DropIngestPhase = 'idle' | 'ingesting' | 'working' | 'accepted'

// A browser drop hands over the File object synchronously, so without a floor
// the spinner would flash for a single frame and read as "nothing happened".
const MIN_INGEST_MS = 450
// How long the confirmation lingers before the zone returns to its normal state.
const ACCEPTED_MS = 1300
// Safety net: if a drop handler never settles (native bridge dropped the reply,
// unexpected throw), never leave the zone spinning forever.
const WATCHDOG_MS = 20000

export interface DropIngestOptions {
  // While non-empty the zone shows that the host is working on its file — a
  // validation or inspection request started from the dropped source. The host
  // supplies the label when the request starts and clears it when it settles;
  // there is deliberately no timer here, so the zone can never say "working"
  // after the host's own busy state (and its button spinner) has ended.
  workingLabel?: string
}

export interface DropIngestFeedback {
  phase: DropIngestPhase
  label: string
  // Class names for the dropzone element itself (adds positioning + state).
  hostClassName: string
  // begin marks the drop as being read; call it the moment a drop lands.
  begin: (message?: string) => void
  // succeed shows the confirmation pulse, then fades back to idle.
  succeed: (message?: string) => void
  // fail clears the animation immediately — the caller surfaces the error.
  fail: () => void
}

// describeReading builds the "working on it" label for a dropped file set.
export function describeReading(count: number): string {
  return count > 1 ? `Reading ${count} files…` : 'Reading file…'
}

// describeAccepted builds the confirmation label for a dropped file set.
export function describeAccepted(count: number): string {
  return count > 1 ? `${count} files added` : 'File added'
}

// describeWorking builds the in-progress label for the host's own work on the
// zone's file, e.g. describeWorking('Validating', 'plan.jmx') → "Validating plan.jmx…".
export function describeWorking(verb: string, subject: string): string {
  return `${verb} ${subject}…`
}

// useDropIngestFeedback drives the drop → "reading" → "added" animation shared by
// every import dropzone, so a drag-drop is always visibly acknowledged even when
// the file is accepted instantly, and — through options.workingLabel — the
// "working" state that follows while the host validates what was dropped. The
// returned begin/succeed/fail callbacks are referentially stable, so they are
// safe as effect dependencies (the native drop-target registration depends on
// its handler's identity).
export function useDropIngestFeedback({ workingLabel = '' }: DropIngestOptions = {}): DropIngestFeedback {
  const [dropPhase, setPhase] = useState<DropIngestPhase>('idle')
  const [dropLabel, setLabel] = useState('')
  const startedAtRef = useRef(0)
  const timersRef = useRef<number[]>([])
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      timersRef.current.forEach(id => window.clearTimeout(id))
      timersRef.current = []
    }
  }, [])

  const clearTimers = useCallback(() => {
    timersRef.current.forEach(id => window.clearTimeout(id))
    timersRef.current = []
  }, [])

  const schedule = useCallback((fn: () => void, delay: number) => {
    const id = window.setTimeout(() => {
      timersRef.current = timersRef.current.filter(timer => timer !== id)
      if (mountedRef.current) fn()
    }, delay)
    timersRef.current.push(id)
  }, [])

  const reset = useCallback(() => {
    clearTimers()
    setPhase('idle')
    setLabel('')
  }, [clearTimers])

  const begin = useCallback((message?: string) => {
    clearTimers()
    startedAtRef.current = Date.now()
    setLabel(message || describeReading(1))
    setPhase('ingesting')
    schedule(() => {
      setPhase(current => (current === 'ingesting' ? 'idle' : current))
    }, WATCHDOG_MS)
  }, [clearTimers, schedule])

  const succeed = useCallback((message?: string) => {
    const settle = () => {
      setPhase('accepted')
      setLabel(message || describeAccepted(1))
      schedule(reset, ACCEPTED_MS)
    }
    const elapsed = Date.now() - startedAtRef.current
    const remaining = Math.max(0, MIN_INGEST_MS - elapsed)
    clearTimers()
    if (remaining === 0) settle()
    else schedule(settle, remaining)
  }, [clearTimers, reset, schedule])

  // A drop being read wins over the host's work: it replaces the source, and the
  // host abandons its work on a source change anyway. Otherwise the host's work
  // wins, including over the "added" pill that would share its corner.
  const working = Boolean(workingLabel) && dropPhase !== 'ingesting'
  const phase: DropIngestPhase = working ? 'working' : dropPhase
  const label = working ? workingLabel : dropLabel

  const hostClassName = [
    'drop-ingest-host',
    phase === 'ingesting' ? 'is-ingesting' : '',
    phase === 'working' ? 'is-working' : '',
    phase === 'accepted' ? 'is-accepted' : '',
  ].filter(Boolean).join(' ')

  return { phase, label, hostClassName, begin, succeed, fail: reset }
}
