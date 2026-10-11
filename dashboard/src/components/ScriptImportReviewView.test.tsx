// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import type { ScriptDefinition } from '../types/scripts'
import { DraftJSONReview, SourceMappingReviewDocument } from './ScriptImportReviewView'

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
})
