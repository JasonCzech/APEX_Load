import './Access.css'
import { useSectionListKeys } from './ux/useSectionListKeys'
import { SETTINGS_DESTINATIONS, settingsHref } from './settingsNavigation'
import { accessHref, type AccessView } from './accessDestinations'

export interface AccessNavigationCounts {
  /** Totals only, never filtered or loaded-page counts. */
  users?: number
  accounts?: number
  reviewAttention?: number
}

// Access review is a sub-tab of Activity, so it is not repeated here.
const PEOPLE_ITEMS: ReadonlyArray<{ id: 'users' | 'accounts' | 'activity'; label: string; view: AccessView }> = [
  { id: 'users', label: 'Users', view: 'users' },
  { id: 'accounts', label: 'Service accounts', view: 'accounts' },
  { id: 'activity', label: 'Activity', view: 'events' },
]

// The same names the Settings list and each page card use, defined once.
const POLICY_ITEMS = SETTINGS_DESTINATIONS.filter(item => item.group === 'Access & Security').map(item => [item.id, item.title] as const)

function peopleItemFor(view: AccessView | undefined): (typeof PEOPLE_ITEMS)[number]['id'] {
  if (view === 'accounts' || view === 'create-account') return 'accounts'
  if (view === 'events' || view === 'usage' || view === 'review') return 'activity'
  return 'users'
}

/**
 * Section list for the Access page, shared by its identity views and the
 * security-policy settings it owns. Entries and their order never change; the
 * current one is marked, the same way the Settings section list works.
 */
export default function AccessNavigation({ currentSetting, currentView, counts = {}, onSelectView }: {
  currentSetting?: string
  currentView?: AccessView
  counts?: AccessNavigationCounts
  /** On the Access page the identity views switch in place (keeping the unsaved-changes guard). */
  onSelectView?: (view: AccessView) => void
}) {
  const navRef = useSectionListKeys()
  const currentPeople = currentSetting ? undefined : peopleItemFor(currentView)
  const countFor = (id: (typeof PEOPLE_ITEMS)[number]['id']) => id === 'users' ? counts.users : id === 'accounts' ? counts.accounts : undefined
  return (
    <nav ref={navRef} className="section-nav access-navigation" aria-label="Access administration">
      <div>
        <h3>People</h3>
        {PEOPLE_ITEMS.map(item => {
          const count = countFor(item.id)
          const content = (
            <>
              <span>{item.label}</span>
              {count !== undefined && <strong>{count}</strong>}
              {item.id === 'activity' && Boolean(counts.reviewAttention) && (
                <span className="access-attention-badge" title={`${counts.reviewAttention} access review item${counts.reviewAttention === 1 ? '' : 's'} need attention (Activity › Access Review)`}>
                  <span aria-hidden="true">{counts.reviewAttention}</span>
                  <span className="sr-only">{counts.reviewAttention} access review item{counts.reviewAttention === 1 ? '' : 's'} need attention</span>
                </span>
              )}
            </>
          )
          const current = currentPeople === item.id ? 'page' : undefined
          return onSelectView
            ? <button key={item.id} type="button" aria-current={current} onClick={() => onSelectView(item.view)}>{content}</button>
            : <a key={item.id} href={accessHref(item.view)} aria-current={current}>{content}</a>
        })}
      </div>
      <div>
        <h3>Security policy</h3>
        {POLICY_ITEMS.map(([id, label]) => (
          <a key={id} href={settingsHref(id)} aria-current={currentSetting === id ? 'page' : undefined}>
            <span>{label}</span>
          </a>
        ))}
      </div>
    </nav>
  )
}
