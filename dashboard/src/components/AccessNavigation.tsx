import './Access.css'
import { settingsHref } from './settingsNavigation'
import { accessHref } from './accessDestinations'

/** Persistent navigation shared by identities and their policy destinations. */
export default function AccessNavigation({ currentSetting }: { currentSetting?: string }) {
  return (
    <nav className="access-navigation" aria-label="Access administration">
      {currentSetting && (
        <a className="btn btn-secondary btn-sm" href={accessHref()}>
          Users & service accounts
        </a>
      )}
      {(
        [
          ['authentication', 'Authentication'],
          ['features', 'Feature policy'],
          ['imports', 'Import policy'],
          ['redaction', 'Redaction'],
          ['secrets', 'Project secrets'],
          ['audit', 'Governance audit'],
        ] as const
      ).map(([id, label]) => (
        <a
          key={id}
          className="btn btn-secondary btn-sm"
          aria-current={currentSetting === id ? 'page' : undefined}
          href={settingsHref(id)}
        >
          {label}
        </a>
      ))}
      {currentSetting && (
        <a className="btn btn-secondary btn-sm" href={accessHref('review')}>
          Access Review
        </a>
      )}
    </nav>
  )
}
