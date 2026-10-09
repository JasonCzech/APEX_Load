import { useMemo, useRef } from 'react'
import PersonalSettings, { type PersonalSection, type PersonalSettingsProps } from './PersonalSettings'
import { useTopbarContribution } from '../hooks/TopbarContributionProvider'
import { useSectionListKeys } from './ux/useSectionListKeys'

const PERSONAL_SECTIONS: { id: PersonalSection; title: string }[] = [
  { id: 'appearance', title: 'Appearance' },
  { id: 'workspace', title: 'Tables & editor' },
  { id: 'notifications', title: 'Notifications' },
  { id: 'account', title: 'Account' },
]

type Props = PersonalSettingsProps & {
  destinationId?: string
  onDestinationChange: (id: string) => void
}

export default function UserSettingsPage({ destinationId, onDestinationChange, ...props }: Props) {
  const headingRef = useRef<HTMLHeadingElement>(null)
  const navRef = useSectionListKeys()
  const requested = destinationId?.replace(/^personal-/, '')
  const administrativeDestination = !!destinationId && !destinationId.startsWith('personal-') && destinationId !== 'preferences'
  const requestedReturn = new URLSearchParams(window.location.hash.split('?')[1] || '').get('returnTo') || ''
  const returnTo = /^#\/(monitoring|fleet|dashboard|schedules|command-center|tests|test\/[^?#]+)(\?|$)/.test(requestedReturn) ? requestedReturn : ''
  const destination = PERSONAL_SECTIONS.find(item => item.id === requested) || PERSONAL_SECTIONS[0]
  const contribution = useMemo(() => ({
    id: 'personal-settings',
    title: 'Settings',
    controls: <span className="ux-save-status">Applies immediately</span>,
    priority: 10,
  }), [])
  useTopbarContribution(contribution)

  return <div className="page settings-page personal-settings-page">
    {administrativeDestination && <section className="settings-subsection" role="note"><h2>Administrator settings</h2><p>Your account can manage personal preferences here. The requested setting requires administrator access.</p>{returnTo && <a className="btn btn-secondary" href={returnTo}>Return to previous page</a>}</section>}
    {/* The same always-visible section list as administrator Settings. */}
    <div className="settings-tabbed-layout">
      <nav ref={navRef} id="personal-settings-navigation" className="settings-task-navigation is-open" aria-label="Personal settings sections">
        <h3>Personal</h3>
        {PERSONAL_SECTIONS.map(item => <button
          key={item.id}
          type="button"
          className={destination.id === item.id ? 'active' : ''}
          aria-current={destination.id === item.id ? 'page' : undefined}
          onClick={() => {
            onDestinationChange('personal-' + item.id)
            // Stacked layouts put the section below the list; move focus to it.
            if (window.innerWidth <= 1100) headingRef.current?.focus()
          }}
        >{item.title}</button>)}
      </nav>
      <div className="settings-main">
        <header className="settings-task-heading">
          <p>Personal</p>
          <h2 ref={headingRef} tabIndex={-1}>{destination.title}</h2>
          <p>Applies to: This browser only</p>
          <p className="setting-desc">Preferences apply immediately and are remembered in this browser when local storage is available. They do not change team or deployment settings.</p>
        </header>
        <PersonalSettings {...props} section={destination.id} />
      </div>
    </div>
  </div>
}
