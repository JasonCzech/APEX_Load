// Recent-items access for the desktop home. Backed by the Go bridge
// (recent.json persisted under the app data dir). No-ops in the browser.

import { getDesktopBridge } from './desktopBridge'

export interface RecentItem {
  kind: string
  id: string
  name: string
  route: string
  openedAt?: string
  pinned?: boolean
}

export async function getRecents(): Promise<RecentItem[]> {
  const bridge = getDesktopBridge()
  if (!bridge?.GetRecentItems) {
    return []
  }
  try {
    const raw = await bridge.GetRecentItems()
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? (parsed as RecentItem[]) : []
  } catch {
    return []
  }
}

export async function addRecent(item: RecentItem): Promise<void> {
  const bridge = getDesktopBridge()
  if (!bridge?.AddRecentItem || !item.id || !item.kind) {
    return
  }
  try {
    await bridge.AddRecentItem(JSON.stringify(item))
  } catch {
    // best-effort
  }
}

// retargetRecent points a listed Recent entry at where its item lives now (a
// scenario draft saved as a scenario). An item that is not listed stays out.
export async function retargetRecent(kind: string, id: string, next: Pick<RecentItem, 'name' | 'route'>): Promise<void> {
  const current = (await getRecents()).find(item => item.kind === kind && item.id === id)
  if (!current) return
  await addRecent({ kind, id, ...next })
}

export async function clearRecents(): Promise<void> {
  try {
    await getDesktopBridge()?.ClearRecentItems?.()
  } catch {
    // best-effort
  }
}

export async function toggleRecentPin(kind: string, id: string): Promise<void> {
  try {
    await getDesktopBridge()?.ToggleRecentPin?.(kind, id)
  } catch {
    // best-effort
  }
}
