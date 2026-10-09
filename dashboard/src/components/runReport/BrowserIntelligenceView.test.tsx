// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import BrowserIntelligenceView from './BrowserIntelligenceView'
import type { RunArchiveReport, TestSummary } from '../../types'

vi.mock('./BrowserFindingsPanel', () => ({ default: ({ onInspectDocument }: { onInspectDocument: (id: string) => void }) => <section data-testid="findings"><button onClick={() => onInspectDocument('gen-document-12345678')}>(document …12345678)</button></section> }))
vi.mock('./BrowserInteractionsPanel', () => ({ default: () => <section data-testid="interactions" /> }))
vi.mock('./JourneyRiskPanel', () => ({ default: () => <section data-testid="journeys" /> }))
vi.mock('../ArchiveResourceInspectorDisclosure', () => ({ default: ({ documentId }: { documentId?: string }) => <section data-testid="inspector">{documentId || 'no document'}</section> }))
vi.mock('../rca/RcaBrowserBaseline', () => ({ default: () => <section data-testid="rca-baseline" /> }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

let container: HTMLDivElement
let root: Root
beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
afterEach(async () => { await act(async () => root.unmount()); container.remove() })

const test = { id: 'run-1', project_id: 'p1' } as TestSummary
const report = { browser_documents: undefined } as unknown as RunArchiveReport

it('composes comparison, findings, baseline, interactions and journeys (BI-4)', async () => {
  const onOpenDiagnostics = vi.fn()
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate hasArchive test={test} report={report} onOpenDiagnostics={onOpenDiagnostics} />))
  for (const id of ['findings', 'interactions', 'journeys', 'inspector']) expect(container.querySelector(`[data-testid="${id}"]`), id).not.toBeNull()
  expect(container.textContent).toContain('This run kept no browser document evidence.')
  // Without a root-cause analysis the baseline section points to one.
  expect(container.querySelector('[data-testid="rca-baseline"]')).toBeNull()
  await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === 'Open root-cause analysis')!.click() })
  expect(onOpenDiagnostics).toHaveBeenCalled()
})

it('shows the root-cause baseline when one exists', async () => {
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate={false} hasArchive test={test} report={report} baseline={{ version: 'browser_baseline.v1', status: 'complete' } as never} onOpenDiagnostics={vi.fn()} />))
  expect(container.querySelector('[data-testid="rca-baseline"]')).not.toBeNull()
})

it('opens a finding\'s document in the resource inspector', async () => {
  await act(async () => root.render(<BrowserIntelligenceView testId="run-1" projectId="p1" canEvaluate hasArchive test={test} report={report} onOpenDiagnostics={vi.fn()} />))
  expect(container.querySelector('[data-testid="inspector"]')?.textContent).toBe('no document')
  await act(async () => { Array.from(container.querySelectorAll('button')).find(button => button.textContent === '(document …12345678)')!.click() })
  expect(container.querySelector('[data-testid="inspector"]')?.textContent).toBe('gen-document-12345678')
  expect(container.textContent).toContain('Showing the retained resources of document …12345678.')
})
