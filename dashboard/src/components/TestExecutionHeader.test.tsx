// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { TestSummary } from '../types'
import TestExecutionHeader from './TestExecutionHeader'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('TestExecutionHeader', () => {
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

  it('keeps run-control actions out of the header for active tests', async () => {
    await renderHeader(buildTest('RUNNING'))

    expect(buttonLabels()).toEqual(['Back'])

    await renderHeader(buildTest('PAUSED'))

    expect(buttonLabels()).toEqual(['Back'])
  })

  async function renderHeader(test: TestSummary) {
    await act(async () => {
      root.render(
        <TestExecutionHeader
          test={test}
          isMultiGroup={false}
          onBack={vi.fn()}
          onStart={vi.fn()}
          onSelectTest={vi.fn()}
          isStopping={test.status === 'STOPPING'}
          showArchiveDownload={false}
          onDownloadArchive={vi.fn()}
        />,
      )
    })
  }

  function buttonLabels() {
    return Array.from(container.querySelectorAll('button')).map((button) => button.textContent || '')
  }
})

function buildTest(status: TestSummary['status']): TestSummary {
  return {
    id: 'test-1',
    name: 'Checkout Journey',
    status,
    total_vusers: 12,
    duration: '10m',
    created_at: '2026-05-12T20:00:00Z',
    groups: [
      {
        name: 'Checkout',
        script_id: 'script-1',
        script_name: 'Checkout Script',
        protocol: 'http',
        vusers: 12,
      },
    ],
  }
}
