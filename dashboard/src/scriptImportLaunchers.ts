// Script import launchers (AUTH-3). A launcher descriptor (launcher and
// optional mode) is the one internal name of an import surface: the create
// catalog, native file routing and the Scripts route all open an importer by
// descriptor, and the surface that renders it is derived here. Old addresses
// are translated at the route boundary (appRouting), never inside the page.

/** Every launcher the Scripts page can open. */
export const SCRIPT_IMPORT_LAUNCHERS = [
  'enterprise_archive',
  'jmeter',
  'bruno',
  'sample',
  'oracle_ebusiness_review',
  'openapi',
  'postman',
  'wsdl',
  'asyncapi',
  'raml',
  'traffic_har',
  'recorder_export',
  'devweb',
  'k6',
  'cloudtest',
  'eventhubs_capture',
  'broker_config',
  // A YAML file whose format is decided on its own surface (native import).
  'yaml_chooser',
] as const

export type ScriptImportLauncher = (typeof SCRIPT_IMPORT_LAUNCHERS)[number]

/** The Scripts page sub-views that render an importer. */
export type ScriptImportSurface = 'import' | 'jmeter-import' | 'bruno-import' | 'sample' | 'oracle-review' | 'protocol-import'

export interface ScriptImportLauncherDescriptor {
  launcher: string
  mode: string | null
}

/**
 * The launcher an old `#/scripts?mode=import` address (written before routes
 * named their launcher) opens: the LoadRunner script archive importer.
 */
export const LEGACY_IMPORT_ROUTE_LAUNCHER: ScriptImportLauncher = 'enterprise_archive'

const DEDICATED_SURFACES: Partial<Record<ScriptImportLauncher, ScriptImportSurface>> = {
  enterprise_archive: 'import',
  jmeter: 'jmeter-import',
  bruno: 'bruno-import',
  sample: 'sample',
  oracle_ebusiness_review: 'oracle-review',
}

export function isScriptImportLauncher(value: string | null | undefined): value is ScriptImportLauncher {
  return SCRIPT_IMPORT_LAUNCHERS.includes(value as ScriptImportLauncher)
}

/** A launcher mode as a route may carry it: a short lower-case token. */
export function isScriptImportMode(value: string | null | undefined): value is string {
  return typeof value === 'string' && /^[a-z][a-z0-9_]{0,39}$/.test(value)
}

/** The sub-view that renders a launcher; every other launcher is a protocol import. */
export function importSurfaceForLauncher(launcher: string): ScriptImportSurface {
  return DEDICATED_SURFACES[launcher as ScriptImportLauncher] || 'protocol-import'
}

/** The launcher a dedicated import sub-view renders (none for protocol-import, which renders many). */
export function launcherForImportSurface(subView: string | undefined): ScriptImportLauncher | undefined {
  const entry = (Object.entries(DEDICATED_SURFACES) as [ScriptImportLauncher, ScriptImportSurface][]).find(([, surface]) => surface === subView)
  return entry?.[0]
}

/** A descriptor with its launcher and mode trimmed (a blank mode is none). */
export function scriptImportLauncherDescriptor(launcher: string, mode?: string | null): ScriptImportLauncherDescriptor {
  return { launcher: launcher.trim(), mode: mode?.trim() || null }
}
