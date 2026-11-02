import { describe, expect, it } from 'vitest'
import { isSupportedNativeImportFileName, resolveNativeImportRequest } from './nativeImportRouting'

const current = (id: number) => (candidate: number) => candidate === id

describe('resolveNativeImportRequest', () => {
  it('opens no importer for a file no importer reads', async () => {
    expect(isSupportedNativeImportFileName('diagram.png')).toBe(false)
    expect(isSupportedNativeImportFileName('capture-notes.docx')).toBe(false)
    await expect(resolveNativeImportRequest([new File(['x'], 'diagram.png')], 1, current(1))).resolves.toBeNull()
  })

  it('keeps an incidental file beside a supported one', async () => {
    const incidental = new File(['meta'], '.DS_Store')
    const spec = new File(['{"openapi":"3.0.0","paths":{}}'], 'api.json', { type: 'application/json' })
    await expect(resolveNativeImportRequest([incidental, spec], 2, current(2))).resolves.toMatchObject({ launcher: 'openapi', files: [incidental, spec] })
  })

  it('fills the HAR slots with HAR captures only', async () => {
    const primary = new File(['{}'], 'capture.har')
    const comparison = new File(['{}'], 'capture-2.har')
    const notes = new File(['notes'], 'notes.txt')
    await expect(resolveNativeImportRequest([primary, notes, comparison], 3, current(3))).resolves.toMatchObject({
      launcher: 'traffic_har',
      files: [primary, comparison],
    })
  })
})
