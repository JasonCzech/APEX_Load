/** Provider names for APM trace links, keyed by the controller's provider id. */
export const TRACE_LINK_PROVIDER_LABELS: Record<string, string> = {
  dynatrace: 'Dynatrace',
  elasticsearch: 'Kibana (ELK)',
}

export function traceLinkProviderLabel(provider: string): string {
  return TRACE_LINK_PROVIDER_LABELS[provider] ?? provider
}
