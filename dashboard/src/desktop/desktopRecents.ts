// Recent-items access for the desktop home. Backed by the Go bridge
// (recent.json persisted under the app data dir). No-ops in the browser.

import { getDesktopBridge } from './desktopBridge'

export interface RecentItem {
  kind: string
  id: string
  name: string
  route: string
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

export async function clearRecents(): Promise<void> {
  try {
    await getDesktopBridge()?.ClearRecentItems?.()
  } catch {
    // best-effort
  }
}
