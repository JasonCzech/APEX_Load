import { useMemo, useRef, useState, type ReactNode } from 'react'

import {
  TopbarActiveContributionContext,
  TopbarContributionProviderPresenceContext,
  TopbarContributionRegisterContext,
  selectActiveTopbarContribution,
  type RegisteredTopbarContribution,
  type TopbarContribution,
} from './TopbarContributionProvider'

export function TopbarContributionProvider({ children }: { children: ReactNode }) {
  const sequenceRef = useRef(0)
  const [contributions, setContributions] = useState<Record<string, RegisteredTopbarContribution>>({})

  const registerContribution = useMemo(() => (
    (contribution: TopbarContribution) => {
      const sequence = ++sequenceRef.current
      setContributions(current => ({ ...current, [contribution.id]: { ...contribution, sequence } }))
      return () => {
        setContributions(current => {
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
