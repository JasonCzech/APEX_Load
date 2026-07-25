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
  return parseFlag(import.meta.env?.VITE_APEXLOAD_RUN_REPORT) ?? false
}

// isJavaClasspathUploadEnabled gates the Java Vuser classpath JAR upload UI
// (attach dependencies during/after a LoadRunner Java import). The runner must
// also be opted in server-side (APEXLOAD_JAVA_VUSER_CLASSPATH_UPLOAD_ENABLED)
// for uploads to take effect at run time.
export function isJavaClasspathUploadEnabled(): boolean {
  const stored = parseFlag(readStoredString('apexload.featureFlags.javaClasspathUpload', ''))
  if (stored !== undefined) return stored
  return parseFlag(import.meta.env?.VITE_APEXLOAD_JAVA_CLASSPATH_UPLOAD) ?? false
}
