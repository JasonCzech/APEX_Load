import { createContext, useContext, useEffect, type ReactNode } from 'react'

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
  icon?: ReactNode
  pressed?: boolean
  title?: string
  tone?: TopbarActionTone
}

export interface TopbarMetaChip {
  id: string
  label: string
  onClick?: () => void
  ariaLabel?: string
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

export const TopbarActiveContributionContext = createContext<TopbarContribution | null>(null)
export const TopbarContributionRegisterContext = createContext<TopbarContributionRegistrar>(() => () => undefined)
export const TopbarContributionProviderPresenceContext = createContext(false)

export function selectActiveTopbarContribution(contributions: RegisteredTopbarContribution[]): TopbarContribution | null {
  if (contributions.length === 0) return null
  return [...contributions].sort((left, right) => {
    const priorityDelta = (right.priority || 0) - (left.priority || 0)
    if (priorityDelta !== 0) return priorityDelta
    return right.sequence - left.sequence
  })[0]
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
