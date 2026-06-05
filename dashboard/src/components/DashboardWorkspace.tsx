import { useEffect, useMemo, useState, type DragEvent, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'

export interface DashboardWorkspaceCard {
  id: string
  title: string
  render: () => ReactNode
}

export interface DashboardWorkspaceTab {
  id: string
  label: string
  cardIds: string[]
}

export interface DashboardWorkspaceProfile {
  id: string
  name: string
  tabs: DashboardWorkspaceTab[]
  unassignedCardIds: string[]
  activeTabId?: string
  dismissedDefaultTabIds?: string[]
  matchEnabled?: boolean
  matchText?: string
  priority?: number
}

export interface DashboardWorkspaceContext {
  tokens: string[]
}

interface DashboardWorkspaceProps {
  activeStorageKey: string
  ariaLabel: string
  cards: DashboardWorkspaceCard[]
  context?: DashboardWorkspaceContext
  defaultProfile: DashboardWorkspaceProfile
  storageKey: string
}

interface WorkspaceActiveState {
  autoApply: boolean
  profileId: string
}

interface TabContextMenuState {
  tabId: string
  x: number
  y: number
}

type DragPayload =
  | { type: 'tab'; id: string }
  | { type: 'card'; id: string; sourceTabId: string }

const DEFAULT_ACTIVE_STATE: WorkspaceActiveState = {
  autoApply: true,
  profileId: 'default',
}

function safeReadJSON<T>(key: string, fallback: T): T {
  if (typeof window === 'undefined') return fallback
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return fallback
    return JSON.parse(raw) as T
  } catch {
    return fallback
  }
}

function safeWriteJSON(key: string, value: unknown) {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // localStorage can be unavailable in locked-down browser contexts.
  }
}

function slugify(value: string) {
  const slug = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  return slug || 'custom'
}

function uniqueId(prefix: string, existingIds: string[]) {
  const base = slugify(prefix)
  if (!existingIds.includes(base)) return base
  let index = 2
  while (existingIds.includes(`${base}-${index}`)) index += 1
  return `${base}-${index}`
}

function moveId(ids: string[], draggedId: string, targetId: string) {
  if (draggedId === targetId) return ids
  const next = ids.filter(id => id !== draggedId)
  const targetIndex = next.indexOf(targetId)
  if (targetIndex === -1) return ids
  next.splice(targetIndex, 0, draggedId)
  return next
}

function moveIdByOffset(ids: string[], id: string, offset: number) {
  const index = ids.indexOf(id)
  if (index < 0) return ids
  const targetIndex = Math.max(0, Math.min(ids.length - 1, index + offset))
  if (targetIndex === index) return ids
  const next = ids.filter(item => item !== id)
  next.splice(targetIndex, 0, id)
  return next
}

function moveIdToEdge(ids: string[], id: string, edge: 'start' | 'end') {
  if (!ids.includes(id)) return ids
  const next = ids.filter(item => item !== id)
  if (edge === 'start') return [id, ...next]
  return [...next, id]
}

function removeCardFromTabs(tabs: DashboardWorkspaceTab[], cardId: string) {
  return tabs.map(tab => ({ ...tab, cardIds: tab.cardIds.filter(id => id !== cardId) }))
}

function normalizeProfile(
  profile: DashboardWorkspaceProfile,
  defaultProfile: DashboardWorkspaceProfile,
  knownCardIds: string[],
): DashboardWorkspaceProfile {
  const knownCards = new Set(knownCardIds)
  const seenCards = new Set<string>()
  const defaultTabIds = new Set(defaultProfile.tabs.map(tab => tab.id))
  const dismissedDefaultTabIds = (profile.dismissedDefaultTabIds || []).filter(tabId => defaultTabIds.has(tabId))
  const sourceTabs = Array.isArray(profile.tabs) && profile.tabs.length > 0 ? profile.tabs : defaultProfile.tabs
  let tabs = sourceTabs.map((tab, index) => {
    const id = tab.id?.trim() || `tab-${index + 1}`
    const label = tab.label?.trim() || `Tab ${index + 1}`
    const cardIds = (tab.cardIds || []).filter(cardId => {
      if (!knownCards.has(cardId) || seenCards.has(cardId)) return false
      seenCards.add(cardId)
      return true
    })
    return { id, label, cardIds }
  })

  if (tabs.length === 0) {
    tabs = [{ id: 'overview', label: 'Overview', cardIds: [] }]
  }

  const unassignedCardIds = (profile.unassignedCardIds || []).filter(cardId => {
    if (!knownCards.has(cardId) || seenCards.has(cardId)) return false
    const defaultTab = defaultProfile.tabs.find(tab => tab.cardIds.includes(cardId))
    const shouldRestoreDefaultTab = defaultTab && !tabs.some(tab => tab.id === defaultTab.id) && !dismissedDefaultTabIds.includes(defaultTab.id)
    if (shouldRestoreDefaultTab) return false
    seenCards.add(cardId)
    return true
  })

  for (const cardId of knownCardIds) {
    if (seenCards.has(cardId)) continue
    const defaultTab = defaultProfile.tabs.find(tab => tab.cardIds.includes(cardId))
    const matchingTab = defaultTab ? tabs.find(tab => tab.id === defaultTab.id) : null
    if (matchingTab) matchingTab.cardIds.push(cardId)
    else if (defaultTab && !dismissedDefaultTabIds.includes(defaultTab.id)) {
      const createdTab = { id: defaultTab.id, label: defaultTab.label, cardIds: [cardId] }
      tabs.push(createdTab)
    } else unassignedCardIds.push(cardId)
    seenCards.add(cardId)
  }

  const activeTabId = tabs.some(tab => tab.id === profile.activeTabId)
    ? profile.activeTabId
    : tabs[0]?.id

  return {
    ...profile,
    id: profile.id || defaultProfile.id,
    name: profile.name?.trim() || defaultProfile.name || 'Default',
    tabs,
    unassignedCardIds,
    activeTabId,
    dismissedDefaultTabIds,
    priority: Number.isFinite(profile.priority) ? profile.priority : 0,
  }
}

export function normalizeWorkspaceProfiles(
  profiles: DashboardWorkspaceProfile[],
  defaultProfile: DashboardWorkspaceProfile,
  knownCardIds: string[],
): DashboardWorkspaceProfile[] {
  const rawProfiles = Array.isArray(profiles) && profiles.length > 0 ? profiles : [defaultProfile]
  const seenProfileIds = new Set<string>()
  const normalized = rawProfiles.map((profile, index) => {
    const id = profile.id?.trim() || (index === 0 ? defaultProfile.id : `profile-${index + 1}`)
    const uniqueProfileId = seenProfileIds.has(id) ? uniqueId(id, Array.from(seenProfileIds)) : id
    seenProfileIds.add(uniqueProfileId)
    return normalizeProfile({ ...profile, id: uniqueProfileId }, defaultProfile, knownCardIds)
  })
  if (!normalized.some(profile => profile.id === defaultProfile.id)) {
    normalized.unshift(normalizeProfile(defaultProfile, defaultProfile, knownCardIds))
  }
  return normalized
}

function createProfileCopy(profile: DashboardWorkspaceProfile, existingIds: string[], name: string): DashboardWorkspaceProfile {
  return {
    ...profile,
    id: uniqueId(name, existingIds),
    name,
    tabs: profile.tabs.map(tab => ({ ...tab, cardIds: [...tab.cardIds] })),
    unassignedCardIds: [...profile.unassignedCardIds],
  }
}

function contextText(context?: DashboardWorkspaceContext) {
  return (context?.tokens || []).filter(Boolean).join(' ').toLowerCase()
}

function matchesProfile(profile: DashboardWorkspaceProfile, context?: DashboardWorkspaceContext) {
  const pattern = profile.matchText?.trim().toLowerCase()
  if (!profile.matchEnabled || !pattern) return false
  const haystack = contextText(context)
  if (!haystack) return false
  return pattern
    .split(',')
    .map(group => group.trim())
    .filter(Boolean)
    .some(group => group.split(/\s+/).every(term => haystack.includes(term)))
}

function chooseAutoProfile(profiles: DashboardWorkspaceProfile[], context?: DashboardWorkspaceContext) {
  return profiles
    .filter(profile => matchesProfile(profile, context))
    .sort((left, right) => (right.priority || 0) - (left.priority || 0))[0]
}

function parseDragPayload(value: string): DragPayload | null {
  try {
    const payload = JSON.parse(value) as DragPayload
    if (payload?.type === 'tab' || payload?.type === 'card') return payload
  } catch {
    return null
  }
  return null
}

export function buildWorkspaceContextTokens(...values: unknown[]): string[] {
  const tokens: string[] = []
  const visit = (value: unknown) => {
    if (value === null || value === undefined) return
    if (typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean') {
      tokens.push(String(value))
      return
    }
    if (Array.isArray(value)) {
      value.forEach(visit)
      return
    }
    if (typeof value === 'object') {
      Object.values(value as Record<string, unknown>).forEach(visit)
    }
  }
  values.forEach(visit)
  return tokens
}

export default function DashboardWorkspace({
  activeStorageKey,
  ariaLabel,
  cards,
  context,
  defaultProfile,
  storageKey,
}: DashboardWorkspaceProps) {
  const cardIds = useMemo(() => cards.map(card => card.id), [cards])
  const cardIdSignature = useMemo(() => cardIds.join('|'), [cardIds])
  const cardMap = useMemo(() => new Map(cards.map(card => [card.id, card])), [cards])
  const [profiles, setProfiles] = useState<DashboardWorkspaceProfile[]>(() =>
    normalizeWorkspaceProfiles(safeReadJSON<DashboardWorkspaceProfile[]>(storageKey, [defaultProfile]), defaultProfile, cardIds),
  )
  const [activeState, setActiveState] = useState<WorkspaceActiveState>(() => {
    const stored = safeReadJSON<WorkspaceActiveState>(activeStorageKey, DEFAULT_ACTIVE_STATE)
    return {
      autoApply: stored.autoApply ?? true,
      profileId: stored.profileId || defaultProfile.id,
    }
  })
  const [draggedTabId, setDraggedTabId] = useState<string | null>(null)
  const [draggedCardId, setDraggedCardId] = useState<string | null>(null)
  const [newTabName, setNewTabName] = useState('')
  const [selectedCardId, setSelectedCardId] = useState('')
  const [tabContextMenu, setTabContextMenu] = useState<TabContextMenuState | null>(null)

  useEffect(() => {
    setProfiles(previous => normalizeWorkspaceProfiles(previous, defaultProfile, cardIds))
  }, [cardIdSignature, defaultProfile])

  useEffect(() => {
    safeWriteJSON(storageKey, profiles)
  }, [profiles, storageKey])

  useEffect(() => {
    safeWriteJSON(activeStorageKey, activeState)
  }, [activeState, activeStorageKey])

  useEffect(() => {
    if (!tabContextMenu) return undefined
    const closeMenu = () => setTabContextMenu(null)
    const closeOnEscape = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') closeMenu()
    }
    window.addEventListener('click', closeMenu)
    window.addEventListener('keydown', closeOnEscape)
    return () => {
      window.removeEventListener('click', closeMenu)
      window.removeEventListener('keydown', closeOnEscape)
    }
  }, [tabContextMenu])

  const selectedProfile = useMemo(() => {
    if (activeState.autoApply) {
      const matched = chooseAutoProfile(profiles, context)
      if (matched) return matched
    }
    return profiles.find(profile => profile.id === activeState.profileId)
      || profiles.find(profile => profile.id === defaultProfile.id)
      || profiles[0]
  }, [activeState.autoApply, activeState.profileId, context, defaultProfile.id, profiles])

  const activeTab = selectedProfile.tabs.find(tab => tab.id === selectedProfile.activeTabId) || selectedProfile.tabs[0]
  const contextMenuTab = tabContextMenu
    ? selectedProfile.tabs.find(tab => tab.id === tabContextMenu.tabId)
    : null
  const availableCards = selectedProfile.unassignedCardIds.map(cardId => cardMap.get(cardId)).filter((card): card is DashboardWorkspaceCard => Boolean(card))
  const activeCards = activeTab.cardIds.map(cardId => cardMap.get(cardId)).filter((card): card is DashboardWorkspaceCard => Boolean(card))

  const updateProfile = (profileId: string, updater: (profile: DashboardWorkspaceProfile) => DashboardWorkspaceProfile) => {
    setProfiles(previous => previous.map(profile => (profile.id === profileId ? normalizeProfile(updater(profile), defaultProfile, cardIds) : profile)))
  }

  const selectProfile = (profileId: string) => {
    setActiveState({ profileId, autoApply: false })
  }

  const selectTab = (tabId: string) => {
    updateProfile(selectedProfile.id, profile => ({ ...profile, activeTabId: tabId }))
  }

  const addProfile = () => {
    const name = `Layout ${profiles.length + 1}`
    const next = createProfileCopy(defaultProfile, profiles.map(profile => profile.id), name)
    setProfiles(previous => [...previous, next])
    setActiveState({ profileId: next.id, autoApply: false })
  }

  const duplicateProfile = () => {
    const name = `${selectedProfile.name} Copy`
    const next = createProfileCopy(selectedProfile, profiles.map(profile => profile.id), name)
    setProfiles(previous => [...previous, next])
    setActiveState({ profileId: next.id, autoApply: false })
  }

  const deleteProfile = () => {
    if (profiles.length <= 1) return
    const nextProfiles = profiles.filter(profile => profile.id !== selectedProfile.id)
    setProfiles(nextProfiles)
    setActiveState({ profileId: nextProfiles[0]?.id || defaultProfile.id, autoApply: false })
  }

  const resetActiveProfile = () => {
    updateProfile(selectedProfile.id, profile => ({
      ...defaultProfile,
      id: profile.id,
      name: profile.name,
      matchEnabled: profile.matchEnabled,
      matchText: profile.matchText,
      priority: profile.priority,
    }))
  }

  const restoreDefaults = () => {
    const next = normalizeWorkspaceProfiles([defaultProfile], defaultProfile, cardIds)
    setProfiles(next)
    setActiveState({ profileId: defaultProfile.id, autoApply: true })
  }

  const addTab = () => {
    const label = newTabName.trim() || `Custom ${selectedProfile.tabs.length + 1}`
    const id = uniqueId(label, selectedProfile.tabs.map(tab => tab.id))
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      activeTabId: id,
      tabs: [...profile.tabs, { id, label, cardIds: [] }],
    }))
    setNewTabName('')
  }

  const removeTab = (tabId: string) => {
    if (selectedProfile.tabs.length <= 1) return
    setTabContextMenu(null)
    updateProfile(selectedProfile.id, profile => {
      const removedTab = profile.tabs.find(tab => tab.id === tabId)
      const tabs = profile.tabs.filter(tab => tab.id !== tabId)
      return {
        ...profile,
        tabs,
        activeTabId: profile.activeTabId === tabId ? tabs[0]?.id : profile.activeTabId,
        unassignedCardIds: Array.from(new Set([...profile.unassignedCardIds, ...(removedTab?.cardIds || [])])),
        dismissedDefaultTabIds: defaultProfile.tabs.some(tab => tab.id === tabId)
          ? Array.from(new Set([...(profile.dismissedDefaultTabIds || []), tabId]))
          : profile.dismissedDefaultTabIds,
      }
    })
  }

  const addCardToActiveTab = () => {
    if (!selectedCardId || !activeTab) return
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      tabs: profile.tabs.map(tab => tab.id === activeTab.id ? { ...tab, cardIds: [...tab.cardIds, selectedCardId] } : tab),
      unassignedCardIds: profile.unassignedCardIds.filter(cardId => cardId !== selectedCardId),
    }))
    setSelectedCardId('')
  }

  const removeCard = (cardId: string, tabId: string) => {
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      tabs: profile.tabs.map(tab => tab.id === tabId ? { ...tab, cardIds: tab.cardIds.filter(id => id !== cardId) } : tab),
      unassignedCardIds: profile.unassignedCardIds.includes(cardId) ? profile.unassignedCardIds : [...profile.unassignedCardIds, cardId],
    }))
  }

  const moveTab = (draggedId: string, targetId: string) => {
    updateProfile(selectedProfile.id, profile => {
      const order = moveId(profile.tabs.map(tab => tab.id), draggedId, targetId)
      return { ...profile, tabs: order.map(tabId => profile.tabs.find(tab => tab.id === tabId)!).filter(Boolean) }
    })
  }

  const moveCard = (cardId: string, targetTabId: string, targetCardId?: string) => {
    updateProfile(selectedProfile.id, profile => {
      const strippedTabs = removeCardFromTabs(profile.tabs, cardId)
      const tabs = strippedTabs.map(tab => {
        if (tab.id !== targetTabId) return tab
        const cardIds = targetCardId ? moveId([...tab.cardIds, cardId], cardId, targetCardId) : [...tab.cardIds, cardId]
        return { ...tab, cardIds }
      })
      return {
        ...profile,
        tabs,
        unassignedCardIds: profile.unassignedCardIds.filter(id => id !== cardId),
      }
    })
  }

  const handleTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>, tabId: string) => {
    const order = selectedProfile.tabs.map(tab => tab.id)
    let nextOrder = order
    if (event.key === 'ArrowLeft') nextOrder = moveIdByOffset(order, tabId, -1)
    else if (event.key === 'ArrowRight') nextOrder = moveIdByOffset(order, tabId, 1)
    else if (event.key === 'Home') nextOrder = moveIdToEdge(order, tabId, 'start')
    else if (event.key === 'End') nextOrder = moveIdToEdge(order, tabId, 'end')
    else return
    event.preventDefault()
    updateProfile(selectedProfile.id, profile => ({ ...profile, tabs: nextOrder.map(id => profile.tabs.find(tab => tab.id === id)!).filter(Boolean) }))
  }

  const handleCardKeyDown = (event: KeyboardEvent<HTMLButtonElement>, cardId: string, tabId: string) => {
    const tab = selectedProfile.tabs.find(item => item.id === tabId)
    if (!tab) return
    let nextIds = tab.cardIds
    if (event.key === 'ArrowUp') nextIds = moveIdByOffset(tab.cardIds, cardId, -1)
    else if (event.key === 'ArrowDown') nextIds = moveIdByOffset(tab.cardIds, cardId, 1)
    else if (event.key === 'Home') nextIds = moveIdToEdge(tab.cardIds, cardId, 'start')
    else if (event.key === 'End') nextIds = moveIdToEdge(tab.cardIds, cardId, 'end')
    else if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') {
      const tabIndex = selectedProfile.tabs.findIndex(item => item.id === tabId)
      const targetTab = selectedProfile.tabs[tabIndex + (event.key === 'ArrowLeft' ? -1 : 1)]
      if (!targetTab) return
      event.preventDefault()
      moveCard(cardId, targetTab.id)
      return
    } else return
    event.preventDefault()
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      tabs: profile.tabs.map(item => item.id === tabId ? { ...item, cardIds: nextIds } : item),
    }))
  }

  const handleDragStart = (event: DragEvent<HTMLElement>, payload: DragPayload) => {
    event.dataTransfer.setData('application/json', JSON.stringify(payload))
    event.dataTransfer.effectAllowed = 'move'
    if (payload.type === 'tab') setDraggedTabId(payload.id)
    if (payload.type === 'card') setDraggedCardId(payload.id)
  }

  const handleDropOnTab = (event: DragEvent<HTMLElement>, targetTabId: string) => {
    event.preventDefault()
    const payload = parseDragPayload(event.dataTransfer.getData('application/json'))
    if (!payload) return
    if (payload.type === 'tab') moveTab(payload.id, targetTabId)
    if (payload.type === 'card') moveCard(payload.id, targetTabId)
  }

  const handleDropOnCard = (event: DragEvent<HTMLElement>, targetTabId: string, targetCardId: string) => {
    event.preventDefault()
    const payload = parseDragPayload(event.dataTransfer.getData('application/json'))
    if (payload?.type === 'card') moveCard(payload.id, targetTabId, targetCardId)
  }

  const handleTabContextMenu = (event: MouseEvent<HTMLElement>, tabId: string) => {
    event.preventDefault()
    event.stopPropagation()
    selectTab(tabId)
    setTabContextMenu({ tabId, x: event.clientX, y: event.clientY })
  }

  return (
    <section className="dashboard-workspace" aria-label={ariaLabel}>
      <div className="dashboard-workspace-toolbar">
        <label>
          <span>Layout</span>
          <select aria-label={`${ariaLabel} layout profile`} value={selectedProfile.id} onChange={event => selectProfile(event.target.value)}>
            {profiles.map(profile => (
              <option key={profile.id} value={profile.id}>{profile.name}</option>
            ))}
          </select>
        </label>
        <label>
          <span>Profile Name</span>
          <input
            aria-label={`${ariaLabel} profile name`}
            value={selectedProfile.name}
            onChange={event => updateProfile(selectedProfile.id, profile => ({ ...profile, name: event.target.value }))}
          />
        </label>
        <label>
          <span>Auto Match</span>
          <input
            aria-label={`${ariaLabel} auto match terms`}
            value={selectedProfile.matchText || ''}
            placeholder="scenario tag, metadata, protocol"
            onChange={event => updateProfile(selectedProfile.id, profile => ({ ...profile, matchText: event.target.value }))}
          />
        </label>
        <label>
          <span>Priority</span>
          <input
            aria-label={`${ariaLabel} auto match priority`}
            type="number"
            value={selectedProfile.priority ?? 0}
            onChange={event => updateProfile(selectedProfile.id, profile => ({ ...profile, priority: Number(event.target.value) || 0 }))}
          />
        </label>
        <label className="dashboard-workspace-check">
          <input
            aria-label={`${ariaLabel} enable profile match`}
            type="checkbox"
            checked={Boolean(selectedProfile.matchEnabled)}
            onChange={event => updateProfile(selectedProfile.id, profile => ({ ...profile, matchEnabled: event.target.checked }))}
          />
          <span>Use for auto</span>
        </label>
        <label className="dashboard-workspace-check">
          <input
            aria-label={`${ariaLabel} auto apply layouts`}
            type="checkbox"
            checked={activeState.autoApply}
            onChange={event => setActiveState({ profileId: selectedProfile.id, autoApply: event.target.checked })}
          />
          <span>Auto apply</span>
        </label>
        <div className="dashboard-workspace-actions">
          <button type="button" className="btn btn-sm btn-secondary" onClick={addProfile}>New Profile</button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={duplicateProfile}>Duplicate</button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={deleteProfile} disabled={profiles.length <= 1}>Delete Profile</button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={resetActiveProfile}>Reset Layout</button>
          <button type="button" className="btn btn-sm btn-ghost" onClick={restoreDefaults}>Restore Defaults</button>
        </div>
      </div>

      <div className="dashboard-workspace-tab-tools">
        <div className="dashboard-workspace-tabs" role="tablist" aria-label={`${ariaLabel} tabs`}>
          {selectedProfile.tabs.map(tab => (
            <div
              key={tab.id}
              className={`dashboard-workspace-tab-shell ${draggedTabId === tab.id ? 'dragging' : ''}`}
              draggable
              onDragStart={event => handleDragStart(event, { type: 'tab', id: tab.id })}
              onDragOver={event => event.preventDefault()}
              onDrop={event => handleDropOnTab(event, tab.id)}
              onDragEnd={() => setDraggedTabId(null)}
              onContextMenu={event => handleTabContextMenu(event, tab.id)}
            >
              <button
                type="button"
                className={`dashboard-workspace-tab ${activeTab.id === tab.id ? 'active' : ''}`}
                role="tab"
                aria-selected={activeTab.id === tab.id}
                onClick={() => selectTab(tab.id)}
                onKeyDown={event => handleTabKeyDown(event, tab.id)}
                title="Drag to reorder. Right-click for tab actions."
              >
                <span>{tab.label}</span>
                <small>{tab.cardIds.length}</small>
              </button>
            </div>
          ))}
        </div>
        <div className="dashboard-workspace-add-tab">
          <input
            aria-label={`${ariaLabel} new tab name`}
            value={newTabName}
            onChange={event => setNewTabName(event.target.value)}
            placeholder="New tab name"
          />
          <button type="button" className="btn btn-sm btn-secondary" onClick={addTab}>Add Tab</button>
        </div>
      </div>

      {contextMenuTab && tabContextMenu && (
        <div
          className="dashboard-workspace-tab-menu"
          role="menu"
          aria-label={`${contextMenuTab.label} tab actions`}
          style={{ left: tabContextMenu.x, top: tabContextMenu.y }}
          onClick={event => event.stopPropagation()}
        >
          <button
            type="button"
            role="menuitem"
            disabled={selectedProfile.tabs.length <= 1}
            onClick={() => removeTab(contextMenuTab.id)}
          >
            Delete Tab
          </button>
        </div>
      )}

      <div className="dashboard-workspace-card-tools">
        {availableCards.length > 0 ? (
          <>
            <select
              aria-label={`Add card to ${activeTab.label}`}
              value={selectedCardId}
              onChange={event => setSelectedCardId(event.target.value)}
            >
              <option value="">Add card to {activeTab.label}</option>
              {availableCards.map(card => (
                <option key={card.id} value={card.id}>{card.title}</option>
              ))}
            </select>
            <button type="button" className="btn btn-sm btn-secondary" disabled={!selectedCardId} onClick={addCardToActiveTab}>Add Card</button>
          </>
        ) : (
          <span>All cards are placed in this profile.</span>
        )}
      </div>

      <div
        className="dashboard-workspace-panel"
        role="tabpanel"
        aria-label={`${activeTab.label} tab`}
        onDragOver={event => event.preventDefault()}
        onDrop={event => handleDropOnTab(event, activeTab.id)}
      >
        {activeCards.length === 0 ? (
          <div className="dashboard-workspace-empty">
            No cards are assigned to {activeTab.label}. Add a card from the pool or restore defaults.
          </div>
        ) : activeCards.map(card => (
          <section
            key={card.id}
            className={`dashboard-workspace-card ${draggedCardId === card.id ? 'dragging' : ''}`}
            data-card-id={card.id}
            draggable
            onDragStart={event => handleDragStart(event, { type: 'card', id: card.id, sourceTabId: activeTab.id })}
            onDragOver={event => event.preventDefault()}
            onDrop={event => handleDropOnCard(event, activeTab.id, card.id)}
            onDragEnd={() => setDraggedCardId(null)}
          >
            <div className="dashboard-workspace-card-header">
              <button
                type="button"
                className="dashboard-workspace-grabber"
                aria-label={`Reorder card ${card.title}`}
                onKeyDown={event => handleCardKeyDown(event, card.id, activeTab.id)}
              >
                ⋮⋮
              </button>
              <strong>{card.title}</strong>
              <button
                type="button"
                className="dashboard-workspace-remove"
                aria-label={`Remove card ${card.title}`}
                onClick={() => removeCard(card.id, activeTab.id)}
              >
                ×
              </button>
            </div>
            <div className="dashboard-workspace-card-body">
              {card.render()}
            </div>
          </section>
        ))}
      </div>
    </section>
  )
}
