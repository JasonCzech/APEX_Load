import type { LibraryAssignment } from '../types/storedContent'
export function assignmentSummary(value?: LibraryAssignment) {
  return value?.application ? `${value.application.name} · ${value.releases.map(release => `${release.name}${release.archived_at ? ' (archived)' : ''}`).join(', ') || 'No releases'}` : 'Unassigned'
}
export function assignmentMatches(value: LibraryAssignment | undefined, application: string, releases: string[]) {
  return (value?.application?.id || '') === application && [...(value?.releases || []).map(release => release.id)].sort().join('\0') === [...releases].sort().join('\0')
}
export function matchesAssignmentFilter(value: LibraryAssignment | undefined, application: string, release: string) {
  return (application === 'all' || (value?.application?.id || '') === application) && (release === 'all' || (release === '' ? !value?.releases.length : value?.releases.some(item => item.id === release)))
}
