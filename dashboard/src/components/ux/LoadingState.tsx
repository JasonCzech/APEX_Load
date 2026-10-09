/**
 * The one loading placeholder: announced politely (role="status") and worded
 * "Loading <what>…" with a single ellipsis character. Use it for Suspense
 * fallbacks and first loads; an empty result belongs in an empty state instead.
 */
export default function LoadingState({ what, className = 'page-loading', inline = false }: { what: string; className?: string; inline?: boolean }) {
  const text = `Loading ${what}…`
  return inline ? <p className={className} role="status">{text}</p> : <div className={className} role="status">{text}</div>
}
