import type { GitHubScriptStatus, StoredScript } from '../types'
import { isAddressableScriptId } from './scriptIdentity'

export const GITHUB_STATUS_CONCURRENCY = 8

/** Load every addressable script status without an unbounded request burst. */
export async function loadGitHubScriptStatuses(
  scripts: readonly StoredScript[],
  loadStatus: (scriptId: string) => Promise<GitHubScriptStatus>,
  concurrency = GITHUB_STATUS_CONCURRENCY,
): Promise<Record<string, GitHubScriptStatus>> {
  const ids = scripts.filter(script => isAddressableScriptId(script.id)).map(script => script.id)
  const statuses: Record<string, GitHubScriptStatus> = {}
  let nextIndex = 0

  const worker = async () => {
    while (nextIndex < ids.length) {
      const id = ids[nextIndex]
      nextIndex += 1
      try {
        const status = await loadStatus(id)
        if (status.linked) statuses[id] = status
      } catch {
        // One missing/unauthorized remote link should not hide other statuses.
      }
    }
  }

  const workerCount = Math.min(ids.length, Math.max(1, Math.floor(concurrency)))
  await Promise.all(Array.from({ length: workerCount }, () => worker()))
  return statuses
}
