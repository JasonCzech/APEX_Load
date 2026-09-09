import { readStoredString } from './browserStorage'

function parseFlag(value: string | undefined): boolean | undefined {
  const normalized = value?.trim().toLowerCase()
  if (!normalized) return undefined
  if (['1', 'true', 'yes', 'on', 'enabled'].includes(normalized)) return true
  if (['0', 'false', 'no', 'off', 'disabled'].includes(normalized)) return false
  return undefined
}

export function isRunReportEnabled(): boolean {
  const stored = parseFlag(readStoredString('apexload.featureFlags.runReport', ''))
  if (stored !== undefined) return stored
  return parseFlag(import.meta.env?.VITE_APEXLOAD_RUN_REPORT) ?? true
}

// isJavaClasspathUploadEnabled gates the Java Vuser classpath JAR upload UI
// (attach dependencies during/after a LoadRunner Java import). The panel is
// shown by default and can be turned off per browser or at build time.
//
// Showing it is safe because the server side stays fail-closed: without
// APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED the upload endpoint answers 404
// and attached assets are ignored at run time. The resolver surfaces that as
// remediation naming the env var rather than a bare error.
export function isJavaClasspathUploadEnabled(): boolean {
  const stored = parseFlag(readStoredString('apexload.featureFlags.javaClasspathUpload', ''))
  if (stored !== undefined) return stored
  return parseFlag(import.meta.env?.VITE_APEXLOAD_JAVA_CLASSPATH_UPLOAD) ?? true
}
