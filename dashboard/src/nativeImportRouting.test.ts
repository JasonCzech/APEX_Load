import { describe, expect, it } from 'vitest'
import { fileIsHARCapture, isSupportedNativeImportFileName, resolveNativeImportRequest } from './nativeImportRouting'
import { storedZipFixture } from './test/zipFixture'

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

  it('opens the HAR importer for zipped captures, as the capture guide asks for them', async () => {
    const named = new File([storedZipFixture(['capture.har'])], 'login-1.har.zip', { type: 'application/zip' })
    const inside = new File([storedZipFixture(['__MACOSX/', '__MACOSX/._capture.har', 'exports/', 'exports/capture.har', '.DS_Store'])], 'login-2.zip', { type: 'application/zip' })
    await expect(fileIsHARCapture(named)).resolves.toBe(true)
    await expect(fileIsHARCapture(inside)).resolves.toBe(true)
    await expect(resolveNativeImportRequest([named, inside], 4, current(4))).resolves.toMatchObject({
      launcher: 'traffic_har',
      files: [named, inside],
    })
  })

  it('keeps an archive that holds more than one capture with the archive importer', async () => {
    const script = new File([storedZipFixture(['Action.c', 'recording.har'])], 'vugen.zip', { type: 'application/zip' })
    const pair = new File([storedZipFixture(['a.har', 'b.har'])], 'pair.zip', { type: 'application/zip' })
    await expect(fileIsHARCapture(script)).resolves.toBe(false)
    await expect(fileIsHARCapture(pair)).resolves.toBe(false)
    await expect(resolveNativeImportRequest([script], 5, current(5))).resolves.toMatchObject({ launcher: 'enterprise_archive' })
  })
})
