import { type RuntimeSettingsSectionId } from './scriptEditorRuntimeSettingsModel'

/**
 * Per-section glyph for the Runtime Settings nav and section headers. Lives in
 * its own module so the view and the extracted protocol sections can both use it
 * without importing each other.
 */
export function RuntimeSectionIcon({ section }: { section: RuntimeSettingsSectionId }) {
  const iconProps = {
    className: 'runtime-section-icon-svg',
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.8,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    focusable: 'false',
  } as const

  const icon = (() => {
    switch (section) {
      case 'script_config':
        return (
          <>
            <rect x="4" y="4" width="16" height="16" rx="2" />
            <path d="M8 9h8" />
            <path d="M8 13h5" />
            <path d="M8 17h7" />
          </>
        )
      case 'think':
        return (
          <>
            <path d="M6 3h12" />
            <path d="M6 21h12" />
            <path d="M8 3v4.5a4 4 0 0 0 1.4 3l2.6 2.1 2.6-2.1a4 4 0 0 0 1.4-3V3" />
            <path d="M8 21v-4.5a4 4 0 0 1 1.4-3l2.6-2.1 2.6 2.1a4 4 0 0 1 1.4 3V21" />
          </>
        )
      case 'pacing':
        return (
          <>
            <path d="M9 3h6" />
            <circle cx="12" cy="13" r="7" />
            <path d="M12 13l3-3" />
            <path d="M12 6v2" />
          </>
        )
      case 'runlogic':
        return (
          <>
            <path d="M6 7h8a4 4 0 0 1 4 4v1" />
            <path d="m15 4 3 3-3 3" />
            <path d="M18 17h-8a4 4 0 0 1-4-4v-1" />
            <path d="m9 20-3-3 3-3" />
          </>
        )
      case 'errors':
        return (
          <>
            <path d="M12 3 2.5 20h19L12 3z" />
            <path d="M12 9v5" />
            <path d="M12 17h.01" />
          </>
        )
      case 'jms':
        return (
          <>
            <path d="M5 7h10a3 3 0 0 1 3 3v2a3 3 0 0 1-3 3H9l-4 4v-4a3 3 0 0 1-3-3v-2a3 3 0 0 1 3-3z" />
            <path d="M8 10h7" />
            <path d="M8 13h4" />
          </>
        )
      case 'eventhubs':
        return (
          <>
            <path d="M4 8h11a4 4 0 0 1 0 8H4" />
            <path d="m12 5 3 3-3 3" />
            <circle cx="19" cy="12" r="1" />
          </>
        )
      case 'websocket':
        return (
          <>
            <path d="M8 7V5a3 3 0 0 1 3-3h2a3 3 0 0 1 3 3v2" />
            <path d="M8 17v2a3 3 0 0 0 3 3h2a3 3 0 0 0 3-3v-2" />
            <path d="M5 10h14" />
            <path d="M5 14h14" />
            <path d="m17 10 2 2-2 2" />
            <path d="m7 10-2 2 2 2" />
          </>
        )
      case 'grpc':
        return (
          <>
            <circle cx="6" cy="12" r="2.5" />
            <circle cx="18" cy="6" r="2.5" />
            <circle cx="18" cy="18" r="2.5" />
            <path d="M8.4 11.1 15.8 7" />
            <path d="M8.4 12.9 15.8 17" />
          </>
        )
      case 'database':
        return (
          <>
            <ellipse cx="12" cy="5" rx="7" ry="3" />
            <path d="M5 5v10c0 1.7 3.1 3 7 3s7-1.3 7-3V5" />
            <path d="M5 10c0 1.7 3.1 3 7 3s7-1.3 7-3" />
          </>
        )
      case 'network':
        return (
          <>
            <circle cx="12" cy="12" r="2.4" />
            <circle cx="5" cy="6" r="2" />
            <circle cx="19" cy="6" r="2" />
            <circle cx="19" cy="18" r="2" />
            <path d="M7 7.2 10.2 10" />
            <path d="m13.8 10 3.2-2.8" />
            <path d="m13.8 14 3.4 2.8" />
          </>
        )
      case 'http':
        return (
          <>
            <circle cx="12" cy="12" r="9" />
            <path d="M3 12h18" />
            <path d="M12 3a13 13 0 0 1 0 18" />
            <path d="M12 3a13 13 0 0 0 0 18" />
          </>
        )
      case 'soap':
        return (
          <>
            <circle cx="9" cy="15" r="4" />
            <circle cx="15.5" cy="9" r="3.5" />
            <circle cx="17.5" cy="17.5" r="1.8" />
            <path d="M6.5 5.5h.01" />
          </>
        )
      case 'java_http':
        return (
          <>
            <path d="M8 8h8v6a4 4 0 0 1-4 4h0a4 4 0 0 1-4-4V8z" />
            <path d="M16 10h1.5a2.5 2.5 0 0 1 0 5H16" />
            <path d="M9 3c1 1 1 2 0 3" />
            <path d="M13 3c1 1 1 2 0 3" />
            <path d="M7 21h10" />
          </>
        )
      case 'java_vuser':
        return (
          <>
            <path d="M7 8h9v6a4 4 0 0 1-4 4h-1a4 4 0 0 1-4-4V8z" />
            <path d="M16 10h1.5a2.5 2.5 0 0 1 0 5H16" />
            <path d="M8 3c1 1 1 2 0 3" />
            <path d="M12 3c1 1 1 2 0 3" />
            <path d="M6 21h12" />
          </>
        )
      case 'browser_emulation':
        return (
          <>
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M3 8h18" />
            <circle cx="12" cy="14" r="3.2" />
            <path d="M8.8 14h6.4" />
            <path d="M12 10.8a5.5 5.5 0 0 1 0 6.4" />
          </>
        )
      case 'web_runtime':
        return (
          <>
            <rect x="3" y="4" width="18" height="16" rx="2" />
            <path d="M3 9h18" />
            <path d="M8 13h8" />
            <path d="M8 17h4" />
            <path d="M17 12v6" />
          </>
        )
      case 'log':
        return (
          <>
            <path d="M9 4h6l1 2h3v15H5V6h3l1-2z" />
            <path d="M9 11h6" />
            <path d="M9 15h6" />
            <path d="M9 19h4" />
          </>
        )
      case 'imported_source':
        return (
          <>
            <path d="M14 3H6a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V8l-5-5z" />
            <path d="M14 3v5h5" />
            <path d="M9 13h6" />
            <path d="M9 17h4" />
          </>
        )
    }
  })()

  return (
    <span className="section-icon runtime-section-icon" aria-hidden="true">
      <svg {...iconProps} data-runtime-section-icon={section}>
        {icon}
      </svg>
    </span>
  )
}
