// The controller files runs that name no release under a per-project legacy
// bucket: application "legacy" / release "legacy-imported", suffixed with a
// short hash of the project outside the default project. They are a fallback,
// not a place to choose.
const LEGACY_APPLICATION = /^legacy(-[0-9a-f]{12})?$/
const LEGACY_RELEASE = /^legacy-imported(-[0-9a-f]{12})?$/

export function isLegacyApplicationId(id?: string): boolean {
  return Boolean(id && LEGACY_APPLICATION.test(id))
}

export function isLegacyReleaseId(id?: string): boolean {
  return Boolean(id && LEGACY_RELEASE.test(id))
}
