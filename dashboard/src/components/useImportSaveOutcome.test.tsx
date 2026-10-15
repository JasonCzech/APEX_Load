// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { useImportSaveOutcome } from './useImportSaveOutcome'
import ImportSaveConfirmation from './ImportSaveConfirmation'
import { type ImportedSaveResult } from './useScriptsAuthoringState'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
const script = { id: 'stored-1', name: 'Imported One', protocol: 'http', version: '1.0', created_at: '2026-09-09' }
const result: ImportedSaveResult = { scripts: [script] }
const container = document.createElement('div')
let root = createRoot(container)
let outcome: ReturnType<typeof useImportSaveOutcome>
const notify = vi.fn()
const more = vi.fn()
function Harness({ scope = 'first' }: { scope?: string }) {
  outcome = useImportSaveOutcome(scope, notify)
  return <ImportSaveConfirmation result={outcome.savedResult} onImportMore={more} onOpen={outcome.openSaved} />
}
afterEach(async () => {
  await act(async () => root.unmount())
  root = createRoot(container)
  vi.clearAllMocks()
})

it('confirms saving without navigation, and opens only an explicitly selected saved script', async () => {
  await act(async () => root.render(<Harness />))
  await act(async () => outcome.publish(result))
  expect(notify).toHaveBeenLastCalledWith({ ...result, stayInImportReview: true })
  expect(container.textContent).toContain('Saved 1 script to the script library')
  const button = (name: string) => [...container.querySelectorAll('button')].find(item => item.textContent?.trim() === name)!
  await act(async () => button('Import More Files').click())
  expect(more).toHaveBeenCalledOnce()
  expect(notify).toHaveBeenCalledTimes(1)
  await act(async () => button('Open Imported One in Visual Editor').click())
  expect(notify).toHaveBeenLastCalledWith({ ...result, stayInImportReview: false })
  // Debug opens the same script, in the debugger.
  await act(async () => button('Debug Imported One').click())
  expect(notify).toHaveBeenLastCalledWith({ ...result, editorWorkspace: 'debug', stayInImportReview: false })
})

it('honors editor saves for one script but keeps batches and stale saves on the importer', async () => {
  await act(async () => root.render(<Harness />))
  await act(async () => outcome.publish(result, 'editor'))
  expect(notify).toHaveBeenLastCalledWith(result)
  const batch = { scripts: [script, { ...script, id: 'stored-2', name: 'Imported Two' }] }
  await act(async () => outcome.publish(batch, 'editor'))
  expect(notify).toHaveBeenLastCalledWith({ ...batch, stayInImportReview: true })
  await act(async () => outcome.publish({ ...result, stayInImportReview: true }, 'editor'))
  expect(notify).toHaveBeenLastCalledWith({ ...result, stayInImportReview: true })
})

it('does not show a prior save confirmation in a new review, including a late receipt', async () => {
  await act(async () => root.render(<Harness />))
  const previousPublish = outcome.publish
  await act(async () => root.render(<Harness scope="second" />))
  await act(async () => previousPublish({ ...result, stayInImportReview: true }))
  expect(outcome.savedResult).toBeNull()
  expect(container.textContent).toBe('')
  expect(notify).toHaveBeenLastCalledWith({ ...result, stayInImportReview: true })
})

it('keeps the current receipt when an older editor save finishes late', async () => {
  await act(async () => root.render(<Harness />))
  const previousPublish = outcome.publish
  await act(async () => root.render(<Harness scope="second" />))
  const currentResult = { scripts: [{ ...script, id: 'stored-2', name: 'Current Import' }] }
  await act(async () => outcome.publish(currentResult))
  await act(async () => previousPublish(result, 'editor'))
  expect(outcome.savedResult).toEqual(currentResult)
  expect(container.textContent).toContain('Open Current Import in Visual Editor')
  expect(notify).toHaveBeenLastCalledWith({ ...result, stayInImportReview: true })
})
