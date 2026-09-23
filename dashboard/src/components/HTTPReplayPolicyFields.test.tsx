// @vitest-environment jsdom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { HTTPReplayPolicyFields } from './HTTPReplayPolicyFields'

;(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

it('edits each policy and preserves explicit zero and empty exclusion overrides', () => {
 const container = document.createElement('div'); document.body.appendChild(container)
 const root = createRoot(container); const onChange = vi.fn()
 try {
  act(() => root.render(<>{(['timeouts', 'connection', 'session'] as const).map(section => <HTTPReplayPolicyFields key={section} section={section} value={{ page_timeout_ms: 500000, cache_max_unused_iterations: 1, ip_version: 'auto', html_parse_mode: 'text', cache_excluded_mime_types: ['text/xml'] }} inherited={{ page_timeout_ms: 300000 }} onChange={onChange} />)}</>))
  const edit = (label: string, value: string) => {
   const element = [...container.querySelectorAll('label')].find(el => el.textContent?.startsWith(label))!.querySelector('input, select, textarea')!
   const prototype = element instanceof HTMLSelectElement ? HTMLSelectElement.prototype : element instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
   act(() => { Object.getOwnPropertyDescriptor(prototype, 'value')!.set!.call(element, value); element.dispatchEvent(new Event('change', { bubbles: true })) })
  }
  edit('Response read buffer', '12288'); expect(onChange).toHaveBeenLastCalledWith({ receive_buffer_bytes: 12288 })
  edit('Meta-refresh limit', '2'); expect(onChange).toHaveBeenLastCalledWith({ max_meta_refresh: 2 })
  edit('Content-check error match limit', '10'); expect(onChange).toHaveBeenLastCalledWith({ content_check_error_limit: 10 })
  edit('Whole-page timeout', '0'); expect(onChange).toHaveBeenLastCalledWith({ page_timeout_ms: 0 })
  edit('Whole-page timeout', ''); expect(onChange).toHaveBeenLastCalledWith({ page_timeout_ms: undefined })
  edit('Digest authentication delay', '0'); expect(onChange).toHaveBeenLastCalledWith({ challenge_delay_ms: 0 })
  for (const [label, key] of [['HTTP errors as warnings', 'http_errors_as_warnings'], ['Fail on non-critical resource errors', 'fail_noncritical_resources'], ['Resource timeouts as warnings', 'resource_timeout_as_warning'], ['Legacy HTML checks', 'legacy_html_checks'], ['Per-VUser DNS cache', 'dns_cache']]) {
   edit(label, 'false'); expect(onChange).toHaveBeenLastCalledWith({ [key]: false })
   edit(label, ''); expect(onChange).toHaveBeenLastCalledWith({ [key]: undefined })
  }
  edit('IP family', 'ipv6'); expect(onChange).toHaveBeenLastCalledWith({ ip_version: 'ipv6' })
  edit('Unused iterations', '2'); expect(onChange).toHaveBeenLastCalledWith({ cache_max_unused_iterations: 2 })
  edit('Discover HTML resources', 'any'); expect(onChange).toHaveBeenLastCalledWith({ html_parse_mode: 'any' })
  edit('Do not cache MIME types', ''); expect(onChange).toHaveBeenLastCalledWith({ cache_excluded_mime_types: [] })
  edit('Do not discover resources', 'text/xml\ntext/plain'); expect(onChange).toHaveBeenLastCalledWith({ html_excluded_mime_types: ['text/xml', 'text/plain'] })
 } finally { act(() => root.unmount()); container.remove() }
})
