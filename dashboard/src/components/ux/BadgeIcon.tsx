export type BadgeIconKind = 'check' | 'cross' | 'ban' | 'alert' | 'info' | 'key' | 'pending' | 'unknown'

/** Small stroke glyphs for compact status badges; decorative (the badge carries the words). */
export default function BadgeIcon({ kind }: { kind: BadgeIconKind }) {
  const props = {
    className: 'ux-badge-icon',
    width: 12,
    height: 12,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 2.6,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
    focusable: 'false',
  } as const
  switch (kind) {
    case 'check':
      return <svg {...props}><path d="M5 12.5l4.5 4.5L19 7.5" /></svg>
    case 'cross':
      return <svg {...props}><path d="M6 6l12 12M18 6L6 18" /></svg>
    case 'alert':
      return <svg {...props}><path d="M12 4l9 16H3z" /><path d="M12 10v4" /><path d="M12 17.5v.01" /></svg>
    case 'info':
      return <svg {...props}><circle cx="12" cy="12" r="9" /><path d="M12 11v6" /><path d="M12 7.5v.01" /></svg>
    case 'ban':
      return <svg {...props}><circle cx="12" cy="12" r="9" /><path d="M5.7 5.7l12.6 12.6" /></svg>
    case 'key':
      return <svg {...props}><circle cx="7.5" cy="16.5" r="3.5" /><path d="M10 14L20 4" /><path d="M17 7l2.5 2.5" /><path d="M14 10l2 2" /></svg>
    case 'pending':
      return <svg {...props}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>
    case 'unknown':
      return <svg {...props}><path d="M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.7.3-1 .9-1 1.7v.5" /><path d="M12 17.5v.01" /></svg>
  }
}
