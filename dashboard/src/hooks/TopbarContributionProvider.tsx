import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

export type TopbarActionTone = 'primary' | 'secondary' | 'success' | 'danger' | 'ghost'
export type TopbarMetaTone = 'default' | 'success' | 'warning' | 'danger' | 'info' | 'accent'

export interface TopbarAction {
  id: string
  label: string
  onClick: () => void
  ariaLabel?: string
  className?: string
  compactLabel?: string
  disabled?: boolean
  hidden?: boolean
  pressed?: boolean
  shortcut?: string | string[]
  shortcutGroup?: string
  allowShortcutInEditable?: boolean
  title?: string
  tone?: TopbarActionTone
}

export interface TopbarMetaChip {
  id: string
  label: string
  hidden?: boolean
  title?: string
  tone?: TopbarMetaTone
}

export interface TopbarContribution {
  id: string
  title?: string
  titleNode?: ReactNode
  controls?: ReactNode
  feedback?: ReactNode
  meta?: TopbarMetaChip[]
  primaryActions?: TopbarAction[]
  secondaryActions?: TopbarAction[]
  priority?: number
}

export interface RegisteredTopbarContribution extends TopbarContribution {
  sequence: number
}

type TopbarContributionRegistrar = (contribution: TopbarContribution) => () => void

const TopbarActiveContributionContext = createContext<TopbarContribution | null>(null)
const TopbarContributionRegisterContext = createContext<TopbarContributionRegistrar>(() => () => undefined)
const TopbarContributionProviderPresenceContext = createContext(false)

export function selectActiveTopbarContribution(contributions: RegisteredTopbarContribution[]): TopbarContribution | null {
  if (contributions.length === 0) return null
  return [...contributions].sort((left, right) => {
    const priorityDelta = (right.priority || 0) - (left.priority || 0)
    if (priorityDelta !== 0) return priorityDelta
    return right.sequence - left.sequence
  })[0]
}

export function TopbarContributionProvider({ children }: { children: ReactNode }) {
  const sequenceRef = useRef(0)
  const [contributions, setContributions] = useState<Record<string, RegisteredTopbarContribution>>({})

  const registerContribution = useMemo(() => (
    (contribution: TopbarContribution) => {
      const sequence = ++sequenceRef.current
      setContributions((current) => ({
        ...current,
        [contribution.id]: { ...contribution, sequence },
      }))
      return () => {
        setContributions((current) => {
          if (!current[contribution.id]) return current
          const next = { ...current }
          delete next[contribution.id]
          return next
        })
      }
    }
  ), [])

  const activeContribution = useMemo(
    () => selectActiveTopbarContribution(Object.values(contributions)),
    [contributions],
  )

  return (
    <TopbarContributionProviderPresenceContext.Provider value={true}>
      <TopbarContributionRegisterContext.Provider value={registerContribution}>
        <TopbarActiveContributionContext.Provider value={activeContribution}>
          {children}
        </TopbarActiveContributionContext.Provider>
      </TopbarContributionRegisterContext.Provider>
    </TopbarContributionProviderPresenceContext.Provider>
  )
}

export function useActiveTopbarContribution() {
  return useContext(TopbarActiveContributionContext)
}

export function useTopbarContribution(contribution: TopbarContribution | null | undefined) {
  const registerContribution = useContext(TopbarContributionRegisterContext)

  useEffect(() => {
    if (!contribution) return undefined
    return registerContribution(contribution)
  }, [contribution, registerContribution])
}

export function useHasTopbarContributionProvider() {
  return useContext(TopbarContributionProviderPresenceContext)
}
