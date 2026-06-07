import type { ReactNode } from 'react'

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

export const CARD_WIDTH_SEQUENCE: DashboardWorkspaceCardWidth[] = ['full', 'half', 'natural']

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

export function slugifyWorkspaceId(value: string) {
  const slug = value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  return slug || 'custom'
}

export function uniqueId(prefix: string, existingIds: string[]) {
  const base = slugifyWorkspaceId(prefix)
  if (!existingIds.includes(base)) return base
  let index = 2
  while (existingIds.includes(`${base}-${index}`)) index += 1
  return `${base}-${index}`
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

export function normalizeProfile(
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
