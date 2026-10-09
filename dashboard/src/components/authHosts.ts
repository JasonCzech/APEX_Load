// parseAuthHosts turns the comma-separated hosts field into
// config.http.auth.hosts entries.
export function parseAuthHosts(value: string): string[] {
  return value.split(',').map(host => host.trim()).filter(host => host.length > 0)
}
