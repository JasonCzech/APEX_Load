// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'

import type { RunArchiveReport, TestSummary } from '../types'
import { libraryStatusTone } from './libraryFederationUtils'
import { rcaTone } from './rca/rcaModel'
import RunReportVerdictBanner from './runReport/RunReportVerdictBanner'
import StatusBadge from './StatusBadge'
import TestExecutionRunState from './TestExecutionRunState'

async function readComponentStylesheet(name: string): Promise<string> {
  // @ts-expect-error node:fs is available in the Vitest runtime.
  const { readFileSync } = await import('node:fs')
  // @ts-expect-error node:url is available in the Vitest runtime.
  const { fileURLToPath, URL: NodeURL } = await import('node:url')
  return readFileSync(fileURLToPath(new NodeURL(`./${name}`, import.meta.url)), 'utf8')
}

function ruleBody(css: string, selectorSnippet: string): string {
  for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const selectors = match[1].split(',').map(item => item.trim())
    if (selectors.includes(selectorSnippet)) return match[2]
  }
  return ''
}

describe('positive status pill styling across the application UX', () => {
  it('assigns status-complete to positive statuses and separates operator-stopped runs', () => {
    const host = document.createElement('div')
    const root = createRoot(host)

    for (const status of ['COMPLETE', 'completed', 'PASSED', 'pass', 'FINISHED', 'succeeded', 'success', 'ready', 'healthy']) {
      act(() => root.render(<StatusBadge status={status} />))
      const badge = host.querySelector('.status-badge')
      expect(badge?.classList.contains('status-complete'), status).toBe(true)
      expect(badge?.classList.contains('status-stopped'), status).toBe(false)
    }

    act(() => root.render(<StatusBadge status="COMPLETE" stoppedByOperator={true} />))
    expect(host.querySelector('.status-badge')?.classList.contains('status-stopped')).toBe(true)
    expect(host.querySelector('.status-badge')?.classList.contains('status-complete')).toBe(false)
    expect(host.textContent).toBe('Stopped by operator')

    act(() => root.render(<StatusBadge status="stopped" />))
    expect(host.querySelector('.status-badge')?.classList.contains('status-stopped')).toBe(true)
    expect(host.textContent).toBe('Stopped by operator')

    act(() => root.unmount())
  })

  it('maps positive statuses to healthy and success tones in shared status helpers', () => {
    for (const status of ['complete', 'completed', 'passed', 'pass', 'finished']) {
      expect(libraryStatusTone(status), `libraryStatusTone(${status})`).toBe('healthy')
      expect(rcaTone(status), `rcaTone(${status})`).toBe('success')
    }
  })

  it('renders a completed run state badge distinctly from an operator-stopped run', () => {
    const host = document.createElement('div')
    const root = createRoot(host)

    act(() =>
      root.render(
        <TestExecutionRunState
          test={{ status: 'COMPLETE', total_vusers: 10, groups: [] } as unknown as TestSummary}
          groupMetrics={[]}
          remainingSeconds={0}
        />,
      ),
    )
    const completeBadge = host.querySelector('.advanced-state-badge')
    expect(completeBadge?.classList.contains('complete')).toBe(true)
    expect(completeBadge?.textContent).toBe('Complete')

    act(() =>
      root.render(
        <TestExecutionRunState
          test={{ status: 'COMPLETE', stopped_by_subject: 'operator@example.com', total_vusers: 10, groups: [] } as unknown as TestSummary}
          groupMetrics={[]}
          remainingSeconds={0}
        />,
      ),
    )
    const stoppedBadge = host.querySelector('.advanced-state-badge')
    expect(stoppedBadge?.classList.contains('stopped')).toBe(true)
    expect(stoppedBadge?.textContent).toBe('Stopped by operator')

    act(() => root.unmount())
  })

  it('gives RunReportVerdictBanner the pass tone for positive acceptance verdicts', () => {
    const host = document.createElement('div')
    const root = createRoot(host)

    for (const verdictStatus of ['Pass', 'Passed', 'Complete', 'Completed', 'Finished']) {
      const report = {
        verdict: { status: verdictStatus, failed: false, explanation: 'All criteria met.' },
      } as unknown as RunArchiveReport
      act(() =>
        root.render(
          <RunReportVerdictBanner
            report={report}
            annotations={null}
            status=""
            explanation=""
            saving={false}
            onStatusChange={vi.fn()}
            onExplanationChange={vi.fn()}
            onSaveOverride={vi.fn(async () => true)}
          />,
        ),
      )
      expect(host.querySelector('.run-report-verdict')?.classList.contains('run-report-verdict-pass'), verdictStatus).toBe(true)
    }

    act(() =>
      root.render(
        <RunReportVerdictBanner
          report={{ verdict: { status: undefined as unknown as string, failed: false, explanation: '' } } as unknown as RunArchiveReport}
          annotations={null}
          status=""
          explanation=""
          saving={false}
          onStatusChange={vi.fn()}
          onExplanationChange={vi.fn()}
          onSaveOverride={vi.fn(async () => true)}
        />,
      ),
    )
    expect(host.querySelector('.run-report-verdict')?.classList.contains('run-report-verdict-conditional')).toBe(true)

    act(() => root.unmount())
  })

  it('uses green success background tokens for positive status pills across component stylesheets', async () => {
    const statusBadgeCss = await readComponentStylesheet('StatusBadge.css')
    for (const selector of ['.status-complete', '.status-badge.success', '.status-running']) {
      const body = ruleBody(statusBadgeCss, selector)
      expect(body, selector).toContain('background: rgba(var(--success-rgb), 0.15)')
      expect(body, selector).toContain('color: var(--success-text)')
    }

    const testExecutionCss = await readComponentStylesheet('TestExecution.css')
    for (const selector of ['.advanced-state-badge.running', '.advanced-state-badge.complete']) {
      const body = ruleBody(testExecutionCss, selector)
      expect(body, selector).toContain('background: var(--success-bg)')
      expect(body, selector).toContain('color: var(--success-text)')
    }

    const harCss = await readComponentStylesheet('HarCorrelationStudio.css')
    expect(ruleBody(harCss, '.har-status-pill--wired')).toContain('background: var(--success-bg)')
    expect(ruleBody(harCss, '.har-status-pill--wired')).toContain('color: var(--success-text)')

    const auraCss = await readComponentStylesheet('AuraEnvelopeEditor.css')
    expect(ruleBody(auraCss, '.aura-state-badge--success')).toContain('background: var(--success-bg)')
    expect(ruleBody(auraCss, '.aura-state-badge--success')).toContain('color: var(--success-text)')

    const sourceMapCss = await readComponentStylesheet('SourceMappingReview.css')
    expect(ruleBody(sourceMapCss, '.source-map-status.converted')).toContain('background: var(--success-bg)')
    expect(ruleBody(sourceMapCss, '.source-map-status.converted')).toContain('color: var(--success-text)')

    const sampleImportCss = await readComponentStylesheet('SampleScriptImport.css')
    expect(ruleBody(sampleImportCss, '.sample-import-asset-status.ready')).toContain('background: var(--success-bg)')
    expect(ruleBody(sampleImportCss, '.sample-import-asset-status.ready')).toContain('color: var(--success-text)')

    const debugSummaryCss = await readComponentStylesheet('DebugCompletionSummary.css')
    expect(ruleBody(debugSummaryCss, '.debug-transaction-status.passed')).toContain('background: var(--success-bg)')

    const pagesCss = await readComponentStylesheet('Pages.css')
    expect(ruleBody(pagesCss, '.transaction-sla-status-pass')).toContain('background: var(--success-bg)')
    expect(ruleBody(pagesCss, '.transaction-sla-status-pass')).toContain('color: var(--success-text)')
  })
})
