import { describe, expect, it } from 'vitest'

import type { GitHubScriptStatus, StoredScript } from '../types'
import { GITHUB_STATUS_CONCURRENCY, loadGitHubScriptStatuses } from './scriptGitHubStatusModel'

describe('loadGitHubScriptStatuses', () => {
  it('loads beyond the former 100-script cap with bounded concurrency', async () => {
    const scripts = Array.from({ length: 125 }, (_, index) => ({ id: `script-${index}` })) as StoredScript[]
    let active = 0
    let maxActive = 0
    const result = await loadGitHubScriptStatuses(scripts, async scriptId => {
      active += 1
      maxActive = Math.max(maxActive, active)
      await Promise.resolve()
      active -= 1
      return { linked: true, status: 'linked', script_id: scriptId } as GitHubScriptStatus
    })

    expect(Object.keys(result)).toHaveLength(125)
    expect(result['script-124']).toMatchObject({ linked: true })
    expect(maxActive).toBeLessThanOrEqual(GITHUB_STATUS_CONCURRENCY)
  })

  it('keeps successful linked statuses when individual requests fail or are unlinked', async () => {
    const scripts = [{ id: 'linked' }, { id: 'missing' }, { id: 'failed' }] as StoredScript[]
    const result = await loadGitHubScriptStatuses(scripts, async scriptId => {
      if (scriptId === 'failed') throw new Error('unavailable')
      return { linked: scriptId === 'linked', status: scriptId === 'linked' ? 'linked' : 'unlinked', script_id: scriptId } as GitHubScriptStatus
    })

    expect(Object.keys(result)).toEqual(['linked'])
  })
})
