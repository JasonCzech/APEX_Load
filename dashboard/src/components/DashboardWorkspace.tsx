import { useEffect, useMemo, useState, type DragEvent, type KeyboardEvent, type MouseEvent, type ReactNode } from 'react'

export type DashboardWorkspaceCardWidth = 'natural' | 'half' | 'full'

export interface DashboardWorkspaceCard {
  id: string
  title: string
  category?: string
  description?: string
  defaultWidth?: DashboardWorkspaceCardWidth
  render: () => ReactNode
}

export interface DashboardWorkspaceTab {
  id: string
  label: string
  cardIds: string[]
  cardWidths?: Record<string, DashboardWorkspaceCardWidth>
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

export interface DashboardWorkspaceDefaultCardInsertion {
  key: string
  profileId: string
  tabId: string
  cardId: string
  afterCardId?: string
}

interface DashboardWorkspaceProps {
  activeStorageKey: string
  ariaLabel: string
  cardLibraryStorageKey: string
  cards: DashboardWorkspaceCard[]
  context?: DashboardWorkspaceContext
  defaultCardInsertions?: DashboardWorkspaceDefaultCardInsertion[]
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
  | { type: 'library-card'; id: string }

const DEFAULT_ACTIVE_STATE: WorkspaceActiveState = {
  autoApply: true,
  profileId: 'default',
}

const CARD_WIDTH_SEQUENCE: DashboardWorkspaceCardWidth[] = ['full', 'half', 'natural']

const LEGACY_CARD_ID_REPLACEMENTS: Record<string, string[]> = {
  'load-impact-metrics': ['load-impact', 'runtime-graphs'],
  'metrics-under-load': ['runtime-graphs'],
}

function replacementCardIds(cardId: string, tabId?: string) {
  if (cardId === 'load-impact-metrics' && tabId === 'load-impact') return ['load-impact']
  if (cardId === 'metrics-under-load' && tabId === 'metrics-under-load') return []
  return LEGACY_CARD_ID_REPLACEMENTS[cardId] || [cardId]
}

function normalizeLegacyTabLabel(id: string, label: string) {
  if (id === 'load-impact' && label === 'Load Impact + Metrics Under Load') return 'Load Impact'
  return label
}

function isRemovedLegacyTab(tab: DashboardWorkspaceTab) {
  return tab.id === 'metrics-under-load' && tab.cardIds.length === 0
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

function uniqueKnownCardIds(cardIds: string[], knownCards: Set<string>, tabId?: string) {
  const seen = new Set<string>()
  return (cardIds || []).flatMap(cardId => replacementCardIds(cardId, tabId)).filter(cardId => {
    if (!knownCards.has(cardId) || seen.has(cardId)) return false
    seen.add(cardId)
    return true
  })
}

function normalizeCardWidths(cardWidths: DashboardWorkspaceTab['cardWidths'], cardIds: string[]) {
  if (!cardWidths) return undefined
  const validCardIds = new Set(cardIds)
  const normalized = Object.entries(cardWidths).reduce<Record<string, DashboardWorkspaceCardWidth>>((acc, [cardId, width]) => {
    const replacementIds = LEGACY_CARD_ID_REPLACEMENTS[cardId] || [cardId]
    for (const replacementId of replacementIds) {
      if (validCardIds.has(replacementId) && CARD_WIDTH_SEQUENCE.includes(width)) {
        acc[replacementId] = width
      }
    }
    return acc
  }, {})
  return Object.keys(normalized).length > 0 ? normalized : undefined
}

function insertCardId(cardIds: string[], cardId: string, beforeCardId?: string) {
  if (cardIds.includes(cardId)) return cardIds
  if (!beforeCardId) return [...cardIds, cardId]
  const targetIndex = cardIds.indexOf(beforeCardId)
  if (targetIndex === -1) return [...cardIds, cardId]
  const next = [...cardIds]
  next.splice(targetIndex, 0, cardId)
  return next
}

function insertCardIdAfter(cardIds: string[], cardId: string, afterCardId?: string) {
  if (cardIds.includes(cardId)) return cardIds
  if (!afterCardId) return [...cardIds, cardId]
  const targetIndex = cardIds.indexOf(afterCardId)
  if (targetIndex === -1) return [...cardIds, cardId]
  const next = [...cardIds]
  next.splice(targetIndex + 1, 0, cardId)
  return next
}

function removeCardFromTab(tabs: DashboardWorkspaceTab[], cardId: string, sourceTabId: string) {
  return tabs.map(tab => (tab.id === sourceTabId ? { ...tab, cardIds: tab.cardIds.filter(id => id !== cardId) } : tab))
}

function normalizeProfile(
  profile: DashboardWorkspaceProfile,
  defaultProfile: DashboardWorkspaceProfile,
  knownCardIds: string[],
): DashboardWorkspaceProfile {
  const knownCards = new Set(knownCardIds)
  const defaultTabIds = new Set(defaultProfile.tabs.map(tab => tab.id))
  const dismissedDefaultTabIds = (profile.dismissedDefaultTabIds || []).filter(tabId => defaultTabIds.has(tabId))
  const sourceTabs = Array.isArray(profile.tabs) && profile.tabs.length > 0 ? profile.tabs : defaultProfile.tabs
  let tabs = sourceTabs.map((tab, index) => {
    const id = tab.id?.trim() || `tab-${index + 1}`
    const label = normalizeLegacyTabLabel(id, tab.label?.trim() || `Tab ${index + 1}`)
    const cardIds = uniqueKnownCardIds(tab.cardIds || [], knownCards, id)
    const cardWidths = normalizeCardWidths(tab.cardWidths, cardIds)
    return { id, label, cardIds, ...(cardWidths ? { cardWidths } : {}) }
  }).filter(tab => !isRemovedLegacyTab(tab))

  if (tabs.length === 0) {
    tabs = [{ id: 'overview', label: 'Overview', cardIds: [] }]
  }

  const unassignedCardIds = uniqueKnownCardIds(profile.unassignedCardIds || [], knownCards)

  for (const defaultTab of defaultProfile.tabs) {
    if (tabs.some(tab => tab.id === defaultTab.id) || dismissedDefaultTabIds.includes(defaultTab.id)) continue
    tabs.push({
      id: defaultTab.id,
      label: defaultTab.label,
      cardIds: uniqueKnownCardIds(defaultTab.cardIds || [], knownCards),
      cardWidths: normalizeCardWidths(defaultTab.cardWidths, defaultTab.cardIds || []),
    })
  }

  const requestedActiveTabId = profile.activeTabId === 'metrics-under-load' ? 'graphs' : profile.activeTabId
  const activeTabId = tabs.some(tab => tab.id === requestedActiveTabId)
    ? requestedActiveTabId
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

function profileContainsCard(profile: DashboardWorkspaceProfile, cardId: string) {
  if (profile.unassignedCardIds.includes(cardId)) return true
  return profile.tabs.some(tab => tab.cardIds.includes(cardId))
}

function applyDefaultCardInsertions(
  profiles: DashboardWorkspaceProfile[],
  insertions?: DashboardWorkspaceDefaultCardInsertion[],
): DashboardWorkspaceProfile[] {
  if (!insertions || insertions.length === 0 || typeof window === 'undefined') return profiles
  let nextProfiles = profiles
  for (const insertion of insertions) {
    if (!insertion.key || safeReadJSON<boolean>(insertion.key, false)) {
      continue
    }
    nextProfiles = nextProfiles.map(profile => {
      if (profile.id !== insertion.profileId || profileContainsCard(profile, insertion.cardId)) {
        return profile
      }
      return {
        ...profile,
        tabs: profile.tabs.map(tab => (
          tab.id === insertion.tabId
            ? { ...tab, cardIds: insertCardIdAfter(tab.cardIds, insertion.cardId, insertion.afterCardId) }
            : tab
        )),
      }
    })
    safeWriteJSON(insertion.key, true)
  }
  return nextProfiles
}

function createProfileCopy(profile: DashboardWorkspaceProfile, existingIds: string[], name: string): DashboardWorkspaceProfile {
  return {
    ...profile,
    id: uniqueId(name, existingIds),
    name,
    tabs: profile.tabs.map(tab => ({ ...tab, cardIds: [...tab.cardIds], cardWidths: tab.cardWidths ? { ...tab.cardWidths } : undefined })),
    unassignedCardIds: [...profile.unassignedCardIds],
  }
}

function defaultCardWidth(card: DashboardWorkspaceCard): DashboardWorkspaceCardWidth {
  return card.defaultWidth || 'full'
}

function resolvedCardWidth(tab: DashboardWorkspaceTab, card: DashboardWorkspaceCard): DashboardWorkspaceCardWidth {
  return tab.cardWidths?.[card.id] || defaultCardWidth(card)
}

function cardWidthLabel(width: DashboardWorkspaceCardWidth) {
  if (width === 'natural') return 'Natural'
  if (width === 'half') return 'Half'
  return 'Full'
}

function nextCardWidth(width: DashboardWorkspaceCardWidth): DashboardWorkspaceCardWidth {
  const index = CARD_WIDTH_SEQUENCE.indexOf(width)
  return CARD_WIDTH_SEQUENCE[(index + 1) % CARD_WIDTH_SEQUENCE.length]
}

function withoutCardWidth(cardWidths: DashboardWorkspaceTab['cardWidths'], cardId: string) {
  if (!cardWidths) return undefined
  const next = { ...cardWidths }
  delete next[cardId]
  return Object.keys(next).length > 0 ? next : undefined
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
    if (payload?.type === 'tab' || payload?.type === 'card' || payload?.type === 'library-card') return payload
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
  cardLibraryStorageKey,
  cards,
  context,
  defaultCardInsertions,
  defaultProfile,
  storageKey,
}: DashboardWorkspaceProps) {
  const cardIds = useMemo(() => cards.map(card => card.id), [cards])
  const cardIdSignature = useMemo(() => cardIds.join('|'), [cardIds])
  const defaultCardInsertionSignature = useMemo(
    () => (defaultCardInsertions || []).map(insertion => `${insertion.key}:${insertion.profileId}:${insertion.tabId}:${insertion.cardId}:${insertion.afterCardId || ''}`).join('|'),
    [defaultCardInsertions],
  )
  const cardMap = useMemo(() => new Map(cards.map(card => [card.id, card])), [cards])
  const [profiles, setProfiles] = useState<DashboardWorkspaceProfile[]>(() =>
    applyDefaultCardInsertions(
      normalizeWorkspaceProfiles(safeReadJSON<DashboardWorkspaceProfile[]>(storageKey, [defaultProfile]), defaultProfile, cardIds),
      defaultCardInsertions,
    ),
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
  const [cardLibraryCollapsed, setCardLibraryCollapsed] = useState(() => safeReadJSON<boolean>(cardLibraryStorageKey, false))
  const [collapsedLibraryCategories, setCollapsedLibraryCategories] = useState<Record<string, boolean>>({})
  const [newTabName, setNewTabName] = useState('')
  const [tabContextMenu, setTabContextMenu] = useState<TabContextMenuState | null>(null)

  useEffect(() => {
    setProfiles(previous => applyDefaultCardInsertions(normalizeWorkspaceProfiles(previous, defaultProfile, cardIds), defaultCardInsertions))
  }, [cardIdSignature, defaultCardInsertionSignature, defaultCardInsertions, defaultProfile])

  useEffect(() => {
    safeWriteJSON(storageKey, profiles)
  }, [profiles, storageKey])

  useEffect(() => {
    safeWriteJSON(activeStorageKey, activeState)
  }, [activeState, activeStorageKey])

  useEffect(() => {
    safeWriteJSON(cardLibraryStorageKey, cardLibraryCollapsed)
  }, [cardLibraryCollapsed, cardLibraryStorageKey])

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
  const activeCards = activeTab.cardIds.map(cardId => cardMap.get(cardId)).filter((card): card is DashboardWorkspaceCard => Boolean(card))
  const cardLibraryGroups = useMemo(() => {
    const groupMap = new Map<string, DashboardWorkspaceCard[]>()
    for (const card of cards) {
      const category = card.category?.trim() || 'General'
      const group = groupMap.get(category) || []
      group.push(card)
      groupMap.set(category, group)
    }
    return Array.from(groupMap.entries()).map(([category, groupedCards]) => ({ category, cards: groupedCards }))
  }, [cards])
  const toggleLibraryCategory = (category: string) => {
    setCollapsedLibraryCategories(current => ({ ...current, [category]: !current[category] }))
  }

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

  const copyCardToTab = (cardId: string, targetTabId: string, targetCardId?: string) => {
    if (!cardMap.has(cardId)) return
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      tabs: profile.tabs.map(tab => (tab.id === targetTabId
        ? { ...tab, cardIds: insertCardId(tab.cardIds, cardId, targetCardId) }
        : tab)),
    }))
  }

  const removeCard = (cardId: string, tabId: string) => {
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      tabs: profile.tabs.map(tab => {
        if (tab.id !== tabId) return tab
        return {
          ...tab,
          cardIds: tab.cardIds.filter(id => id !== cardId),
          cardWidths: withoutCardWidth(tab.cardWidths, cardId),
        }
      }),
      unassignedCardIds: profile.unassignedCardIds.includes(cardId) ? profile.unassignedCardIds : [...profile.unassignedCardIds, cardId],
    }))
  }

  const toggleCardWidth = (cardId: string, tabId: string) => {
    const card = cardMap.get(cardId)
    if (!card) return
    updateProfile(selectedProfile.id, profile => ({
      ...profile,
      tabs: profile.tabs.map(tab => {
        if (tab.id !== tabId) return tab
        const currentWidth = tab.cardWidths?.[cardId] || defaultCardWidth(card)
        return {
          ...tab,
          cardWidths: {
            ...(tab.cardWidths || {}),
            [cardId]: nextCardWidth(currentWidth),
          },
        }
      }),
    }))
  }

  const moveTab = (draggedId: string, targetId: string) => {
    updateProfile(selectedProfile.id, profile => {
      const order = moveId(profile.tabs.map(tab => tab.id), draggedId, targetId)
      return { ...profile, tabs: order.map(tabId => profile.tabs.find(tab => tab.id === tabId)!).filter(Boolean) }
    })
  }

  const moveCard = (cardId: string, sourceTabId: string, targetTabId: string, targetCardId?: string) => {
    updateProfile(selectedProfile.id, profile => {
      const card = cardMap.get(cardId)
      const sourceTab = profile.tabs.find(tab => tab.id === sourceTabId)
      const sourceWidth = sourceTab?.cardWidths?.[cardId] || (card ? defaultCardWidth(card) : undefined)
      const strippedTabs = removeCardFromTab(profile.tabs, cardId, sourceTabId).map(tab => {
        if (tab.id !== sourceTabId) return tab
        return { ...tab, cardWidths: withoutCardWidth(tab.cardWidths, cardId) }
      })
      const tabs = strippedTabs.map(tab => {
        if (tab.id !== targetTabId) return tab
        if (tab.cardIds.includes(cardId)) return tab
        return {
          ...tab,
          cardIds: insertCardId(tab.cardIds, cardId, targetCardId),
          cardWidths: sourceWidth ? { ...(tab.cardWidths || {}), [cardId]: sourceWidth } : tab.cardWidths,
        }
      })
      return {
        ...profile,
        tabs,
      }
    })
  }

  const handleTabKeyDown = (event: KeyboardEvent<HTMLButtonElement>, tabId: string) => {
    const order = selectedProfile.tabs.map(tab => tab.id)
    const index = order.indexOf(tabId)
    if (index < 0) return
    if (event.shiftKey) {
      let nextOrder = order
      if (event.key === 'ArrowLeft') nextOrder = moveIdByOffset(order, tabId, -1)
      else if (event.key === 'ArrowRight') nextOrder = moveIdByOffset(order, tabId, 1)
      else if (event.key === 'Home') nextOrder = moveIdToEdge(order, tabId, 'start')
      else if (event.key === 'End') nextOrder = moveIdToEdge(order, tabId, 'end')
      else return
      event.preventDefault()
      updateProfile(selectedProfile.id, profile => ({ ...profile, tabs: nextOrder.map(id => profile.tabs.find(tab => tab.id === id)!).filter(Boolean) }))
      return
    }
    let targetTab: DashboardWorkspaceTab | undefined
    if (event.key === 'ArrowLeft') targetTab = selectedProfile.tabs[Math.max(0, index - 1)]
    else if (event.key === 'ArrowRight') targetTab = selectedProfile.tabs[Math.min(selectedProfile.tabs.length - 1, index + 1)]
    else if (event.key === 'Home') targetTab = selectedProfile.tabs[0]
    else if (event.key === 'End') targetTab = selectedProfile.tabs[selectedProfile.tabs.length - 1]
    else return
    event.preventDefault()
    if (targetTab) selectTab(targetTab.id)
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
      moveCard(cardId, tabId, targetTab.id)
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
    event.dataTransfer.effectAllowed = payload.type === 'library-card' ? 'copy' : 'move'
    if (payload.type === 'tab') setDraggedTabId(payload.id)
    if (payload.type === 'card' || payload.type === 'library-card') setDraggedCardId(payload.id)
  }

  const handleDropOnTab = (event: DragEvent<HTMLElement>, targetTabId: string) => {
    event.preventDefault()
    const payload = parseDragPayload(event.dataTransfer.getData('application/json'))
    if (!payload) return
    if (payload.type === 'tab') moveTab(payload.id, targetTabId)
    if (payload.type === 'card') moveCard(payload.id, payload.sourceTabId, targetTabId)
    if (payload.type === 'library-card') copyCardToTab(payload.id, targetTabId)
  }

  const handleDropOnCard = (event: DragEvent<HTMLElement>, targetTabId: string, targetCardId: string) => {
    event.preventDefault()
    const payload = parseDragPayload(event.dataTransfer.getData('application/json'))
    if (payload?.type === 'card') moveCard(payload.id, payload.sourceTabId, targetTabId, targetCardId)
    if (payload?.type === 'library-card') copyCardToTab(payload.id, targetTabId, targetCardId)
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
                title="Drag to reorder. Use Shift+Arrow to reorder with the keyboard. Right-click for tab actions."
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

      <div className={`dashboard-workspace-body ${cardLibraryCollapsed ? 'library-collapsed' : ''}`}>
        <div
          className="dashboard-workspace-panel"
          role="tabpanel"
          aria-label={`${activeTab.label} tab`}
          onDragOver={event => event.preventDefault()}
          onDrop={event => handleDropOnTab(event, activeTab.id)}
        >
          {activeCards.length === 0 ? (
            <div className="dashboard-workspace-empty">
              No cards are assigned to {activeTab.label}.
            </div>
          ) : activeCards.map(card => {
            const cardWidth = resolvedCardWidth(activeTab, card)
            const nextWidth = nextCardWidth(cardWidth)
            const nextWidthLabel = cardWidthLabel(nextWidth).toLowerCase()
            return (
            <section
              key={card.id}
              className={`dashboard-workspace-card width-${cardWidth} ${draggedCardId === card.id ? 'dragging' : ''}`}
              data-card-id={card.id}
              data-card-width={cardWidth}
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
                  className="dashboard-workspace-width-toggle"
                  aria-label={`Set card ${card.title} to ${nextWidthLabel} width`}
                  title={`Set ${nextWidthLabel} width`}
                  onClick={() => toggleCardWidth(card.id, activeTab.id)}
                >
                  {cardWidthLabel(cardWidth)}
                </button>
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
            )
          })}
        </div>

        <aside className={`dashboard-workspace-card-library ${cardLibraryCollapsed ? 'collapsed' : ''}`} aria-label={`${ariaLabel} card library`}>
          {cardLibraryCollapsed ? (
            <button
              type="button"
              className="dashboard-workspace-library-restore"
              aria-label="Show card library"
              onClick={() => setCardLibraryCollapsed(false)}
            >
              Cards
            </button>
          ) : (
            <>
              <div className="dashboard-workspace-library-header">
                <h3>Card Library</h3>
                <button
                  type="button"
                  className="btn btn-sm btn-ghost"
                  aria-label="Collapse card library"
                  onClick={() => setCardLibraryCollapsed(true)}
                >
                  Collapse
                </button>
              </div>
              <div className="dashboard-workspace-library-groups">
                {cardLibraryGroups.map(group => (
                  <section key={group.category} className="dashboard-workspace-library-group">
                    <button
                      type="button"
                      className="dashboard-workspace-library-group-header"
                      aria-expanded={!collapsedLibraryCategories[group.category]}
                      aria-controls={`dashboard-workspace-library-group-${slugify(group.category)}`}
                      onClick={() => toggleLibraryCategory(group.category)}
                    >
                      <span className="dashboard-workspace-library-group-title">{group.category}</span>
                      <span className="dashboard-workspace-library-group-meta">
                        {group.cards.filter(card => activeTab.cardIds.includes(card.id)).length}/{group.cards.length}
                      </span>
                      <span className="dashboard-workspace-library-group-chevron" aria-hidden="true">⌄</span>
                    </button>
                    <div
                      id={`dashboard-workspace-library-group-${slugify(group.category)}`}
                      className="dashboard-workspace-library-list"
                      hidden={Boolean(collapsedLibraryCategories[group.category])}
                    >
                      {group.cards.map(card => {
                        const alreadyOnActiveTab = activeTab.cardIds.includes(card.id)
                        return (
                          <div
                            key={card.id}
                            className={`dashboard-workspace-library-item ${alreadyOnActiveTab ? 'placed' : ''}`}
                            data-library-card-id={card.id}
                            draggable
                            onDragStart={event => handleDragStart(event, { type: 'library-card', id: card.id })}
                            onDragEnd={() => setDraggedCardId(null)}
                          >
                            <div>
                              <strong>{card.title}</strong>
                              {card.description && <p>{card.description}</p>}
                            </div>
                            <button
                              type="button"
                              className="btn btn-sm btn-secondary"
                              disabled={alreadyOnActiveTab}
                              onClick={() => copyCardToTab(card.id, activeTab.id)}
                            >
                              {alreadyOnActiveTab ? 'Added' : 'Add'}
                            </button>
                          </div>
                        )
                      })}
                    </div>
                  </section>
                ))}
              </div>
            </>
          )}
        </aside>
      </div>
    </section>
  )
}
