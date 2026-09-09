/** APEX handoff v1: explicit link, bound to the exact execution, never a redirect. */
export function orchestrationReturn(search: string, testId: string): URL | null {
  const params = new URLSearchParams(search)
  if (params.get('orchestrationRun') !== testId) return null
  const value = params.get('orchestrationReturn')
  if (!value || value.length > 4096 || /[\s\\]/.test(value)) return null
  try {
    const url = new URL(value)
    const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
    if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) return null
    if (url.username || url.password || url.hash) return null
    if (!/^\/runs\/[a-zA-Z0-9_-]+$/.test(url.pathname) && url.pathname !== '/apex-load') return null
    if ([...url.searchParams.keys()].some(key => !['project', 'connection', 'run'].includes(key))) return null
    return url
  } catch { return null }
}
