import { LibraryCatalogItem, LibraryRemote, LibraryTransferResponse } from '../types/library'

export function formatLibraryDateTime(value?: string) {
  if (!value) return 'Never'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleString()
}

export function shortLibraryHash(value?: string) {
  return value ? value.slice(0, 12) : '-'
}

export function libraryStatusTone(status?: string) {
  switch ((status || '').toLowerCase()) {
    case 'synced':
    case 'healthy':
      return 'healthy'
    case 'conflict':
    case 'unreachable':
    case 'missing credentials':
      return 'danger'
    case 'stale':
    case 'host only':
    case 'local only':
    case 'unknown':
      return 'warning'
    default:
      return ''
  }
}

export function remoteCredentialReady(remote?: LibraryRemote) {
  if (!remote) return false
  return remote.auth_method === 'none' || Boolean(remote.credential_configured)
}

export function normalizedRemoteBaseURL(remote?: LibraryRemote) {
  return (remote?.base_url || '').replace(/\/+$/, '')
}

export function hostLibraryHref(remote: LibraryRemote | undefined, item: LibraryCatalogItem) {
  const base = normalizedRemoteBaseURL(remote)
  if (!base) return ''
  const id = encodeURIComponent(item.remote_item_id || item.id)
  if (item.kind === 'script') {
    return `${base}/#/scripts?mode=edit&scriptId=${id}`
  }
  if (item.kind === 'result') {
    return `${base}/#/test/${id}`
  }
  return `${base}/#/tests`
}

export function libraryTransferSummary(action: 'pull' | 'push', response: LibraryTransferResponse) {
  const imported = response.import
  const conflicts = imported?.conflicts || []
  if (conflicts.length > 0) {
    return `${action === 'pull' ? 'Pull' : 'Push'} found ${conflicts.length} conflict${conflicts.length === 1 ? '' : 's'}: ${conflicts.map(conflict => conflict.reason).join('; ')}`
  }
  const count = imported?.items?.length || 0
  return `${action === 'pull' ? 'Pulled' : 'Pushed'} ${count} item${count === 1 ? '' : 's'}.`
}
