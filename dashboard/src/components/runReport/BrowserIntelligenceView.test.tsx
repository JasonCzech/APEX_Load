// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import BrowserIntelligenceView from './BrowserIntelligenceView'
import { browserBaselineAbsence } from './browserBaselineAbsence'
import type { RunArchiveReport, TestSummary } from '../../types'
import type { RcaAnalysisResponse } from '../../types/rca'

vi.mock('./BrowserFindingsPanel', () => ({ default: ({ onInspectDocument, browserPolicyPinned }: { onInspectDocument: (id: string) => void; browserPolicyPinned?: boolean }) => <section data-testid="findings" data-pinned={String(browserPolicyPinned)}><button onClick={() => onInspectDocument('gen-document-12345678')}>(document …12345678)</button></section> }))
vi.mock('./BrowserInteractionsPanel', () => ({ default: () => <section data-testid="interactions" /> }))
vi.mock('./JourneyRiskPanel', () => ({ default: () => <section data-testid="journeys" /> }))
const inspectorMounts = vi.hoisted(() => ({ count: 0 }))
vi.mock('../ArchiveResourceInspectorDisclosure', async () => {
  const { useEffect } = await import('react')
  function InspectorDisclosure({ documentId }: { documentId?: string }) {
    useEffect(() => { inspectorMounts.count += 1 }, [])
    return <section data-testid="inspector">{documentId || 'no document'}</section>
  }
  return { default: InspectorDisclosure }
})
vi.mock('../rca/RcaBrowserBaseline', () => ({ default: () => <section data-testid="rca-baseline" /> }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container); inspectorMounts.count = 0 })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

const test = { id: 'run-1', project_id: 'p1' } as TestSummary
const report = { browser_documents: undefined } as unknown as RunArchiveReport
const noAnalysis = { response: null, loading: false, error: '' }

it('composes comparison, findings, baseline, interactions and journeys (BI-4)', async () => {
  const onOpenDiagnostics = vi.fn()
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate hasArchive test={test} report={report} rootCause={noAnalysis} onOpenDiagnostics={onOpenDiagnostics} />))
  for (const id of ['findings', 'interactions', 'journeys', 'inspector']) expect(container.querySelector(`[data-testid="${id}"]`), id).not.toBeNull()
  expect(container.textContent).toContain('This run kept no browser document evidence.')
  expect(container.textContent).toContain('Page timings and Web Vitals are recorded only when they are turned on')
  // The report holds no pinned browser policy.
  expect(container.querySelector('[data-testid="findings"]')?.getAttribute('data-pinned')).toBe('false')
  // Without a root-cause analysis the baseline section points to one.
  expect(container.querySelector('[data-testid="rca-baseline"]')).toBeNull()
  expect(container.textContent).toContain('has not been run for this run')
  await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Open root-cause analysis')!.click() })
  expect(onOpenDiagnostics).toHaveBeenCalled()
})

it('says the evidence is loading, never absent, before the report arrives', async () => {
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate hasArchive test={test} report={null} rootCause={{ response: null, loading: true, error: '' }} onOpenDiagnostics={vi.fn()} />))
  expect(container.textContent).toContain('Loading the run\'s document evidence…')
  expect(container.textContent).not.toContain('kept no browser document evidence')
  expect(container.textContent).toContain('Loading the run\'s root-cause analysis…')
  expect(container.querySelector('[data-testid="findings"]')?.getAttribute('data-pinned')).toBe('undefined')
})

it('shows the root-cause baseline when one exists', async () => {
  const response = { state: 'completed', presented: true, assessment: { browser_baseline: { version: 'browser_baseline.v1', status: 'complete' } } } as unknown as RcaAnalysisResponse
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate={false} hasArchive test={test} report={report} rootCause={{ response, loading: false, error: '' }} onOpenDiagnostics={vi.fn()} />))
  expect(container.querySelector('[data-testid="rca-baseline"]')).not.toBeNull()
})

it('says why the baseline is missing for each analysis state', () => {
  const at = (response: Partial<RcaAnalysisResponse> | null, loading = false, error = '') => browserBaselineAbsence({ response: response as RcaAnalysisResponse | null, loading, error })
  expect(at({ state: 'running', presented: true })).toContain('is running')
  expect(at({ state: 'completed', presented: true, assessment: {} as never })).toContain('made no browser baseline comparison')
  expect(at({ state: 'failed', presented: true })).toContain('did not complete')
  expect(at(null, false, 'boom')).toContain('could not be read')
  expect(at({ state: 'none', presented: false, analysis_enabled: false })).toContain('new analyses are not enabled for this project')
})

it('opens a finding\'s document in the resource inspector, again when chosen again', async () => {
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate hasArchive test={test} report={report} rootCause={noAnalysis} onOpenDiagnostics={vi.fn()} />))
  expect(container.querySelector('[data-testid="inspector"]')?.textContent).toBe('no document')
  const choose = async () => { await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === '(document …12345678)')!.click() }) }
  await choose()
  expect(container.querySelector('[data-testid="inspector"]')?.textContent).toBe('gen-document-12345678')
  expect(container.textContent).toContain('Opened from a finding: the retained resources of document …12345678.')
  const mounts = inspectorMounts.count
  await choose()
  expect(inspectorMounts.count).toBe(mounts + 1)
})
