// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { DraftJSONReview, ImportReviewCommitPanel, SourceMappingReviewDocument } from './ScriptImportReviewView'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('DraftJSONReview', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  const baseline: ScriptDefinition = {
    name: 'Baseline',
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: 'https://example.test' },
    actions: [],
  }
  const current: ScriptDefinition = {
    name: 'Current',
    version: '1.0.0',
    protocol: 'http',
    config: { base_url: 'https://example.test' },
    actions: [],
  }

  async function renderReview(jsonTextLength = 100) {
    await act(async () => {
      root.render(<DraftJSONReview reviewBaselineScript={baseline} script={current} jsonTextLength={jsonTextLength} />)
    })
  }

  async function expandReview() {
    const details = container.querySelector('details') as HTMLDetailsElement
    await act(async () => {
      details.open = true
      details.dispatchEvent(new Event('toggle'))
    })
  }

  it('does not render either JSON preview while collapsed', async () => {
    await renderReview()

    expect(container.querySelectorAll('pre')).toHaveLength(0)
    expect(container.textContent).not.toContain('Baseline')
    expect(container.textContent).not.toContain('Current')
  })

  it('renders both JSON previews only after expansion', async () => {
    await renderReview()
    await expandReview()

    const previews = Array.from(container.querySelectorAll('pre')).map(element => element.textContent)
    expect(previews).toHaveLength(2)
    expect(previews[0]).toContain('Baseline')
    expect(previews[1]).toContain('Current')
  })

  it('keeps oversized scripts out of the DOM after expansion', async () => {
    await renderReview(250_001)
    await expandReview()

    expect(container.querySelectorAll('pre')).toHaveLength(0)
    expect(container.textContent).toContain('Script too large to preview inline.')
  })
})

const script: ScriptDefinition = { name: 'Recorded HTTP output', version: '1', protocol: 'http', config: { base_url: '' }, actions: [], metadata: { import: { source: 'har', preserved: { recording_environment: { version: 1, observation_source: 'agent_reported', engine: 'chromium', emulated: false, physical_device_qualification: 'unverified' } } } } }

describe('saved recording environment review', () => {
  let container: HTMLDivElement, root: Root
  beforeEach(() => { container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container) })
  afterEach(() => { act(() => root.unmount()); container.remove() })
  it('shows the HTTP recording receipt independently of source-review existence and preserves importer provenance', () => {
    const controller = { importMetadata: script.metadata?.import, sourceReview: null, trueClientPromotionReview: null, importSummary: { runtimeSettingsParity: [], correlationSuggestions: [] }, importedCorrelationFocusTargets: [], focusSourceMapping: () => {} } as unknown as Parameters<typeof SourceMappingReviewDocument>[0]['controller']
    act(() => root.render(<SourceMappingReviewDocument controller={controller} script={script} onSelectAction={() => {}} />))
    const panel = container.querySelector('[aria-label="Recording environment"]')!
    expect(panel).not.toBeNull()
    expect(panel.textContent).toContain('Chromium')
    expect(panel.textContent).toContain('Recording agent reported')
    expect(panel.textContent).toContain('Not applicable (HTTP output)')
    expect(panel.textContent).toContain('Unverified')
    expect(script.metadata?.import?.source).toBe('har')
  })

  it('renders concise nowrap correlation action buttons with WCAG 2.5.3 aria-labels in source and commit panels', async () => {
    const onSelectAction = vi.fn()
    const longZeroSpaceLocation = `Action_${'CheckoutAuthorizationTransactionStep_'.repeat(6)}001`
    expect(longZeroSpaceLocation.length).toBeGreaterThan(220)

    const correlation = {
      name: 'csrf_token',
      source: 'header',
      expression: 'x-csrf-token',
      found_at: longZeroSpaceLocation,
      used_at: [`Use_${longZeroSpaceLocation}_A`, `Use_${longZeroSpaceLocation}_B`, `Use_${longZeroSpaceLocation}_C`],
      sample_value: 'tok_live_123',
    }
    const controller = {
      importMetadata: script.metadata?.import,
      sourceReview: null,
      trueClientPromotionReview: null,
      draftDiffSummary: null,
      reviewBaselineScript: null,
      importSummary: {
        runtimeSettingsParity: [],
        correlationSuggestions: [correlation],
      },
      importedCorrelationFocusTargets: [{
        correlation,
        foundAtActionIndex: 1,
        foundAtLabel: `Step 2 · ${longZeroSpaceLocation}`,
        usedAtLocations: [
          { location: correlation.used_at[0], actionIndex: 3, label: `Step 4 · ${correlation.used_at[0]}` },
          { location: correlation.used_at[1], actionIndex: 5, label: `Step 6 · ${correlation.used_at[1]}` },
          { location: correlation.used_at[2], actionIndex: 7, label: `Step 8 · ${correlation.used_at[2]}` },
        ],
        usedAtTargets: [
          { location: correlation.used_at[0], actionIndex: 3, label: `Step 4 · ${correlation.used_at[0]}` },
          { location: correlation.used_at[1], actionIndex: 5, label: `Step 6 · ${correlation.used_at[1]}` },
          { location: correlation.used_at[2], actionIndex: 7, label: `Step 8 · ${correlation.used_at[2]}` },
        ],
      }],
      focusSourceMapping: () => {},
    } as unknown as Parameters<typeof SourceMappingReviewDocument>[0]['controller']

    await act(async () => {
      root.render(
        <div>
          <SourceMappingReviewDocument controller={controller} script={script} onSelectAction={onSelectAction} />
          <ImportReviewCommitPanel
            assistantChangeLog={[]}
            controller={controller}
            editorHistoryPastCount={0}
            jsonTextLength={120}
            migrationSuggestions={[]}
            onApplyMigrationSuggestion={() => {}}
            onSelectAction={onSelectAction}
            script={script}
          />
        </div>,
      )
    })

    const sourceButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.import-correlation-card button'))
    expect(sourceButtons.map(button => button.textContent)).toEqual(['Open source', 'Open use', 'Open use', 'Open use'])
    for (const button of sourceButtons) {
      expect(button.style.whiteSpace).toBe('nowrap')
      const visibleText = button.textContent || ''
      const ariaLabel = button.getAttribute('aria-label') || ''
      expect(ariaLabel.startsWith(visibleText)).toBe(true)
      expect(ariaLabel).toContain('for csrf_token')
      expect(ariaLabel).toContain(longZeroSpaceLocation)
    }

    await act(async () => {
      sourceButtons[0].click()
      sourceButtons[2].click()
    })
    expect(onSelectAction).toHaveBeenNthCalledWith(1, 1)
    expect(onSelectAction).toHaveBeenNthCalledWith(2, 5)

    const inspectorButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.utility-inline-actions button'))
    expect(inspectorButtons.map(button => button.textContent)).toEqual(['Open source', 'Open use', 'Open use', '+1 more use'])
    for (const button of inspectorButtons) {
      expect(button.style.whiteSpace).toBe('nowrap')
    }
    expect(inspectorButtons[0].getAttribute('aria-label')).toBe(`Open source Step 2 · ${longZeroSpaceLocation} for csrf_token`)
    expect(inspectorButtons[1].getAttribute('aria-label')).toBe(`Open use Step 4 · ${correlation.used_at[0]} for csrf_token`)
  })

  it('falls back to step and correlation nouns in aria-label when labels or names are whitespace-only', async () => {
    const whitespaceCorrelation = {
      name: '   ',
      source: 'body',
      expression: '$.token',
      found_at: '   ',
      used_at: ['   '],
    }
    const controller = {
      importMetadata: script.metadata?.import,
      sourceReview: null,
      trueClientPromotionReview: null,
      draftDiffSummary: null,
      reviewBaselineScript: null,
      importSummary: {
        runtimeSettingsParity: [],
        correlationSuggestions: [whitespaceCorrelation],
      },
      importedCorrelationFocusTargets: [{
        correlation: whitespaceCorrelation,
        foundAtActionIndex: 0,
        foundAtLabel: '   ',
        usedAtLocations: [{ location: '   ', actionIndex: 1, label: '   ' }],
        usedAtTargets: [{ location: '   ', actionIndex: 1, label: '   ' }],
      }],
      focusSourceMapping: () => {},
    } as unknown as Parameters<typeof SourceMappingReviewDocument>[0]['controller']

    await act(async () => {
      root.render(
        <div>
          <SourceMappingReviewDocument controller={controller} script={script} onSelectAction={() => {}} />
          <ImportReviewCommitPanel
            assistantChangeLog={[]}
            controller={controller}
            editorHistoryPastCount={0}
            jsonTextLength={120}
            migrationSuggestions={[]}
            onApplyMigrationSuggestion={() => {}}
            onSelectAction={() => {}}
            script={script}
          />
        </div>,
      )
    })

    const sourceButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.import-correlation-card button'))
    expect(sourceButtons.map(button => button.getAttribute('aria-label'))).toEqual([
      'Open source step for correlation',
      'Open use step for correlation',
    ])
    const inspectorButtons = Array.from(container.querySelectorAll<HTMLButtonElement>('.utility-inline-actions button'))
    expect(inspectorButtons.map(button => button.getAttribute('aria-label'))).toEqual([
      'Open source step for correlation',
      'Open use step for correlation',
    ])
  })
})
