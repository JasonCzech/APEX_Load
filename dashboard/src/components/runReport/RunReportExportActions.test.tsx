// @vitest-environment jsdom

import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { downloadBlob } from '../../utils/fileUtils'
import RunReportExportActions from './RunReportExportActions'

vi.mock('../../utils/fileUtils', () => ({ downloadBlob: vi.fn().mockResolvedValue(undefined) }))

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

describe('RunReportExportActions', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => root.unmount())
    container.remove()
    vi.clearAllMocks()
  })

  it('exports the selected report lens as DOCX', async () => {
    const docx = new Blob(['PK'], { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' })
    const downloadHTML = vi.fn()
    const downloadFormat = vi.fn().mockResolvedValue(docx)
    await act(async () => {
      root.render(<RunReportExportActions testId="run-1" lens="audit" testName="Quarterly Test" downloadHTML={downloadHTML} downloadFormat={downloadFormat} />)
    })
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(candidate => candidate.textContent?.includes('Editable Word'))!
    await act(async () => button.click())

    expect(downloadFormat).toHaveBeenCalledWith('run-1', 'audit', 'docx')
    expect(downloadBlob).toHaveBeenCalledWith(docx, 'Quarterly-Test-audit.docx')
    expect(downloadHTML).not.toHaveBeenCalled()
  })

  it('explains each export format by the review task it supports', async () => {
    await act(async () => {
      root.render(<RunReportExportActions testId="run-1" lens="engineer" downloadHTML={vi.fn()} downloadFormat={vi.fn()} />)
    })
    for (const label of ['Navigable HTML', 'Print-ready PDF', 'Editable Word', 'Data CSV', 'Analysis tables']) {
      expect(container.textContent).toContain(label)
    }
    expect(container.textContent).toContain('Browse sections and charts offline.')
    expect(container.textContent).toContain('section and metric values')
  })

  it('downloads analysis tables as a ZIP bundle for the selected lens', async () => {
    const bundle = new Blob(['PK'], { type: 'application/zip' })
    const downloadFormat = vi.fn().mockResolvedValue(bundle)
    await act(async () => {
      root.render(<RunReportExportActions testId="run-1" lens="engineer" testName="Checkout" downloadHTML={vi.fn()} downloadFormat={downloadFormat} />)
    })
    const button = Array.from(container.querySelectorAll<HTMLButtonElement>('button')).find(candidate => candidate.textContent?.includes('Analysis tables'))!
    await act(async () => button.click())
    expect(downloadFormat).toHaveBeenCalledWith('run-1', 'engineer', 'analysis_zip')
    expect(downloadBlob).toHaveBeenCalledWith(bundle, 'Checkout-engineer.zip')
  })
})
