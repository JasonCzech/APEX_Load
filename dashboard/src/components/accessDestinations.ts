export const ACCESS_VIEWS = [
  'users',
  'onboard',
  'accounts',
  'create-account',
  'events',
  'usage',
  'review',
] as const
export type AccessView = (typeof ACCESS_VIEWS)[number]
export function isAccessView(value: string | null | undefined): value is AccessView {
  return ACCESS_VIEWS.includes(value as AccessView)
}
export function accessHref(view: AccessView = 'users', hash = window.location.hash): string {
  const current = new URLSearchParams(hash.split('?')[1] || '')
  const params = new URLSearchParams({ accessView: view })
  for (const key of ['projectId', 'applicationId', 'releaseId']) {
    const value = current.get(key)
    if (value) params.set(key, value)
  }
  return `#/access?${params}`
}
