// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useExecutionScenarioPreviewModel } from './useExecutionScenarioPreviewModel'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

function PreviewHarness({ test, manifest, loading = false, error = '' }: { test: unknown; manifest: unknown; loading?: boolean; error?: string }) {
  const model = useExecutionScenarioPreviewModel(test as never, manifest as never, loading, error)

  return (
    <div>
      <span data-testid="source">{model.snapshotSource}</span>
      <span data-testid="loading">{model.snapshotLoading ? 'loading' : 'idle'}</span>
      <span data-testid="error">{model.snapshotError || 'none'}</span>
      <span data-testid="preview-total">{model.queuedScenarioPreview.totalSeconds}</span>
      <span data-testid="preview-vusers">{model.queuedScenarioPreview.totalVUsers}</span>
      <span data-testid="preview-delay">{model.queuedScenarioPreview.startDelaySeconds}</span>
      <span data-testid="rows">{model.queuedScenarioRows.length}</span>
      <span data-testid="row-vusers">{model.queuedScenarioRows[0]?.group.vusers ?? 0}</span>
      <span data-testid="actions">{model.queuedScenarioRows[0]?.actionCount ?? 'unavailable'}</span>
    </div>
  )
}

const retainedSummary = {
  id: 'test-1',
  name: 'Queued Checkout',
  status: 'QUEUED',
  total_vusers: 20,
  duration: '5m',
  start_delay: '45s',
  created_at: '2026-05-12T20:00:00Z',
  groups: [
    {
      name: 'Mutable Checkout',
      script_id: 'script-1',
      script_name: 'Current Checkout Script',
      protocol: 'http',
      vusers: 20,
    },
  ],
}

describe('useExecutionScenarioPreviewModel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  it('builds the preview from the immutable initial manifest configuration', async () => {
    await act(async () => {
      root.render(
        <PreviewHarness
          test={retainedSummary}
          manifest={{
            schema_version: '1.0',
            test_id: 'test-1',
            test_name: 'Queued Checkout',
            status: 'QUEUED',
            source: 'live',
            generated_at: '2026-05-12T20:01:00Z',
            identity: { created_at: '2026-05-12T20:00:00Z' },
            initial_configuration_provenance: {
              source: 'archive_provenance',
              immutable: true,
            },
            initial_configuration: {
              duration: '2m',
              total_vusers: 7,
              start_delay: '10s',
              groups: [{
                name: 'Queued Checkout Snapshot',
                script_id: 'script-1',
                script_name: 'Checkout Script v1',
                protocol: 'http',
                vusers: 7,
              }],
            },
            current_configuration: {},
            load_generators: {},
          }}
        />,
      )
    })

    expect(readTestId('source')).toBe('immutable_manifest')
    expect(readTestId('preview-total')).toBe('130')
    expect(readTestId('preview-vusers')).toBe('7')
    expect(readTestId('preview-delay')).toBe('10')
    expect(readTestId('rows')).toBe('1')
    expect(readTestId('row-vusers')).toBe('7')
    expect(readTestId('actions')).toBe('unavailable')
  })

  it('marks a mutable stored test record without claiming it is immutable', async () => {
    await act(async () => {
      root.render(
        <PreviewHarness
          test={retainedSummary}
          manifest={{
            schema_version: '1.0',
            test_id: 'test-1',
            test_name: 'Queued Checkout',
            status: 'QUEUED',
            source: 'live',
            generated_at: '2026-05-12T20:01:00Z',
            identity: {},
            initial_configuration_provenance: {
              source: 'stored_test_record',
              immutable: false,
            },
            initial_configuration: {},
            current_configuration: {},
            load_generators: {},
          }}
        />,
      )
    })

    expect(readTestId('source')).toBe('stored_manifest')
  })

  it('marks a synthesized manifest as retained state instead of an immutable snapshot', async () => {
    await act(async () => {
      root.render(
        <PreviewHarness
          test={retainedSummary}
          manifest={{
            schema_version: '1.0',
            test_id: 'test-1',
            test_name: 'Queued Checkout',
            status: 'QUEUED',
            source: 'live',
            generated_at: '2026-05-12T20:01:00Z',
            identity: { created_at: '2026-05-12T20:00:00Z' },
            initial_configuration_provenance: {
              source: 'retained_run_state',
              immutable: false,
            },
            initial_configuration: {
              duration: '4m',
              total_vusers: 13,
              start_delay: '20s',
              groups: [{
                name: 'Retained Checkout State',
                script_id: 'script-1',
                script_name: 'Checkout Script',
                protocol: 'http',
                vusers: 13,
              }],
            },
            current_configuration: {},
            load_generators: {},
          }}
        />,
      )
    })

    expect(readTestId('source')).toBe('retained_manifest')
    expect(readTestId('preview-total')).toBe('260')
    expect(readTestId('preview-vusers')).toBe('13')
    expect(readTestId('preview-delay')).toBe('20')
    expect(readTestId('row-vusers')).toBe('13')
  })

  it('treats legacy manifests without explicit provenance as retained state', async () => {
    await act(async () => {
      root.render(
        <PreviewHarness
          test={retainedSummary}
          manifest={{
            schema_version: '1.0',
            test_id: 'test-1',
            test_name: 'Queued Checkout',
            status: 'QUEUED',
            source: 'archive',
            generated_at: '2026-05-12T20:01:00Z',
            identity: {},
            initial_configuration: {},
            current_configuration: {},
            load_generators: {},
          }}
        />,
      )
    })

    expect(readTestId('source')).toBe('retained_manifest')
  })

  it('labels the retained-summary fallback when the immutable snapshot is unavailable', async () => {
    await act(async () => {
      root.render(<PreviewHarness test={retainedSummary} manifest={null} error="manifest not found" />)
    })

    expect(readTestId('source')).toBe('summary')
    expect(readTestId('loading')).toBe('idle')
    expect(readTestId('error')).toBe('manifest not found')
    expect(readTestId('preview-total')).toBe('345')
    expect(readTestId('preview-vusers')).toBe('20')
    expect(readTestId('row-vusers')).toBe('20')
    expect(readTestId('actions')).toBe('unavailable')
  })

  function readTestId(testId: string) {
    return container.querySelector(`[data-testid="${testId}"]`)?.textContent
  }
})
