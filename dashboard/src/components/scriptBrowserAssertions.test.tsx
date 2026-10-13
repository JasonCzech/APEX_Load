// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, it, expect } from 'vitest'
import ActionChecks from './ActionChecks'
import type { ScriptActionDefinition } from '../types/scripts'
import { defaultAssertionForProtocol } from './scriptEditorHelpers'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
describe('browser assertions', () => {
  it('edits the expected value while preserving the selector and unrelated action fields', async () => {
    let result: ScriptActionDefinition = { name: 'Greeting', type: 'browser_wait', target: '#result', metadata: { source: 'recording' }, assertions: [{ type: 'element_text_contains', value: '#result', expected: 'Welcome' }] }
    function Harness() {
      const [action, setAction] = useState(result)
      return <ActionChecks action={action} index={0} protocol="browser" isBrowserScript onAddExtractor={() => {}} onUpdateExtractor={() => {}} onRemoveExtractor={() => {}} onAddAssertion={() => {}} onRemoveAssertion={() => {}} onUpdateAssertion={(_i, ai, field, value) => { result = { ...action, assertions: action.assertions!.map((a, i) => i === ai ? { ...a, [field]: value } : a) }; setAction(result) }} />
    }
    const host = document.createElement('div'); document.body.append(host); const root = createRoot(host)
    try {
      await act(async () => root.render(<Harness />))
      expect(host.querySelector<HTMLInputElement>('[aria-label="Check 1 selector"]')!.value).toBe('#result')
      const input = host.querySelector<HTMLInputElement>('[aria-label="Check 1 text"]')!
      expect(input.value).toBe('Welcome')
      await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'Hello'); input.dispatchEvent(new Event('input', { bubbles: true })) })
      expect(result.assertions).toEqual([{ type: 'element_text_contains', value: '#result', expected: 'Hello' }])
      expect(result.metadata).toEqual({ source: 'recording' })
      expect(host.textContent).not.toContain('Status is')
      expect(host.textContent).not.toContain('Saved values')
      expect(defaultAssertionForProtocol('browser')).toEqual({ type: 'element_text_contains', value: '', expected: '' })
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
