// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import GenerateReportModal from './GenerateReportModal'
import { buildReportTypeOptions } from './reportTypeOptions'
import type { RunArchiveReportProfile } from '../types'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('GenerateReportModal', () => {
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

  it('derives HTML report choices from backend profile metadata', () => {
    const options = buildReportTypeOptions(sampleProfiles())

    expect(options.map((option) => option.id)).toEqual(['executive', 'audit'])
    expect(options[1]).toMatchObject({
      label: 'Audit',
      bestFor: 'Evidence integrity and approval review.',
    })
  })

  it('allows selecting and generating the audit profile', async () => {
    const onGenerate = vi.fn()

    await act(async () => {
      root.render(
        <GenerateReportModal
          profiles={sampleProfiles()}
          onClose={vi.fn()}
          onGenerate={onGenerate}
        />,
      )
    })

    expect(container.textContent).toContain('Evidence integrity and approval review.')
    expect(container.textContent).not.toContain('Raw machine export only.')

    const auditButton = button('Audit')
    await act(async () => {
      auditButton.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    await act(async () => {
      button('Open Report').dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })

    expect(onGenerate).toHaveBeenCalledWith('audit')
  })

  function button(label: string): HTMLButtonElement {
    const match = Array.from(container.querySelectorAll('button')).find((item) => item.textContent?.includes(label))
    if (!match) throw new Error(`button not found: ${label}`)
    return match
  }
})

function sampleProfiles(): RunArchiveReportProfile[] {
  return [
    {
      id: 'executive',
      label: 'Executive Summary',
      description: 'Decision-focused summary.',
      best_for: 'Release sign-off.',
      export_formats: ['html', 'pdf'],
      included_sections: ['verdict'],
    },
    {
      id: 'raw',
      label: 'Raw Export',
      description: 'Raw machine export only.',
      export_formats: ['jsonl'],
      included_sections: ['raw_exports'],
    },
    {
      id: 'audit',
      label: 'Audit',
      description: 'Governance package.',
      best_for: 'Evidence integrity and approval review.',
      export_formats: ['html', 'pdf'],
      included_sections: ['audit_identity', 'artifact_integrity'],
    },
  ]
}
