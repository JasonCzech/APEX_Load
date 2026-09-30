import type { LibraryAssignment } from '../types/storedContent'
interface Props {
  items: Array<{ library_assignment?: LibraryAssignment }>
  application: string
  release: string
  onApplication: (value: string) => void
  onRelease: (value: string) => void
}
export default function LibraryAssignmentFilters({ items, application, release, onApplication, onRelease }: Props) {
  const applications = new Map(items.flatMap(item => item.library_assignment?.application ? [[item.library_assignment.application.id, item.library_assignment.application.name] as const] : []))
  const releases = new Map(items.flatMap(item => item.library_assignment?.releases.map(value => [value.id, `${value.name}${value.archived_at ? ' (archived)' : ''}`] as const) || []))
  return <>
    <label className="project-redaction-rule-field"><span>Assigned application</span><select aria-label="Assigned application" className="settings-input" value={application} onChange={event => onApplication(event.target.value)}><option value="all">All assigned applications</option><option value="">Unassigned</option>{application !== 'all' && application !== '' && !applications.has(application) && <option value={application}>Selected application (no matching items)</option>}{[...applications].map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
    <label className="project-redaction-rule-field"><span>Assigned release</span><select aria-label="Assigned release" className="settings-input" value={release} onChange={event => onRelease(event.target.value)}><option value="all">All assigned releases</option><option value="">No assigned releases</option>{release !== 'all' && release !== '' && !releases.has(release) && <option value={release}>Selected release (no matching items)</option>}{[...releases].map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
  </>
}
