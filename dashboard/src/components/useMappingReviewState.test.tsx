// @vitest-environment jsdom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import type { ScriptDefinition, SourceReview } from '../types/scripts'
import { useMappingReviewState } from './useMappingReviewState'

describe('source domain mapping hook', () => {
  let root: Root | undefined, container: HTMLDivElement | undefined
  afterEach(() => { act(() => root?.unmount()); container?.remove() })
  it('clears a verified current binding when review switches to historical or unknown domains', async () => {
    let latest: ReturnType<typeof useMappingReviewState> | undefined
    const script: ScriptDefinition = { name: 'Native', version: '1', protocol: 'http', config: { base_url: 'https://example.test' }, actions: [{ type: 'GET', name: 'Unrelated', target: '/unrelated' }] }
    const review: SourceReview = { mappings: [{ id: 'same-index', source_file: 'capture.har', start_line: 1, target_path: 'actions[0]', status: 'converted' }] }
    function Harness({ source }: { source: SourceReview }) { latest = useMappingReviewState(script, script, source); return null }
    container = document.createElement('div'); document.body.appendChild(container); root = createRoot(container)
    await act(async () => { root?.render(<Harness source={review} />) })
    for (let attempt = 0; attempt < 20 && latest?.checking; attempt++) await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)) })
    expect(latest?.resolved['same-index']?.status).toBe('matched')
    for (const domain of ['original_import', 'future_domain']) {
      await act(async () => { root?.render(<Harness source={{ ...review, target_domain: domain }} />) })
      expect(latest).toEqual({ state: undefined, resolved: {}, checking: false, error: '' })
    }
  })
})
