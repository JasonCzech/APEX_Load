import { useCallback, useState, type SetStateAction } from 'react'
import type { AccessView } from './accessDestinations'
import type { AccessTab, ActivitySubTab, ServiceAccountSubTab, UserSubTab } from './accessTypes'

export function useAccessWorkspace(
  initialTab: AccessTab,
  value?: AccessView,
  onChange?: (view: AccessView) => void,
) {
  const [local, setLocal] = useState<AccessView>(
    initialTab === 'service_accounts' ? 'accounts' : initialTab === 'activity' ? 'events' : 'users',
  )
  const current = value ?? local
  const activeTab: AccessTab =
    current === 'accounts' || current === 'create-account'
      ? 'service_accounts'
      : ['events', 'usage', 'review'].includes(current)
        ? 'activity'
        : 'users'
  const userSubTab: UserSubTab = current === 'onboard' ? 'onboard' : 'existing'
  const serviceAccountSubTab: ServiceAccountSubTab = current === 'create-account' ? 'generate' : 'existing'
  const activitySubTab: ActivitySubTab = current === 'usage' || current === 'review' ? current : 'events'
  const change = useCallback(
    (next: AccessView) => {
      if (next === current) return
      if (onChange) onChange(next)
      else setLocal(next)
    },
    [current, onChange],
  )
  const setActiveTab = useCallback(
    (next: SetStateAction<AccessTab>) => {
      const tab = typeof next === 'function' ? next(activeTab) : next
      if (tab !== activeTab)
        change(tab === 'service_accounts' ? 'accounts' : tab === 'activity' ? 'events' : 'users')
    },
    [activeTab, change],
  )
  const setUserSubTab = useCallback(
    (tab: UserSubTab) => change(tab === 'onboard' ? 'onboard' : 'users'),
    [change],
  )
  const setServiceAccountSubTab = useCallback(
    (tab: ServiceAccountSubTab) => change(tab === 'generate' ? 'create-account' : 'accounts'),
    [change],
  )
  const setActivitySubTab = useCallback((tab: ActivitySubTab) => change(tab), [change])
  return {
    view: current,
    setView: change,
    activeTab,
    userSubTab,
    serviceAccountSubTab,
    activitySubTab,
    setActiveTab,
    setUserSubTab,
    setServiceAccountSubTab,
    setActivitySubTab,
  }
}
