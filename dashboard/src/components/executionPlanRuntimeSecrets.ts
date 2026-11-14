import type { PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import { isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'
import {
  CREDENTIAL_SHAPED_TEXT as CREDENTIAL_SHAPED_VALUE,
  headerNeedsReference,
  isProjectSecretReference as isPlanSecretReference,
  isSchemedProjectSecretReference as isSchemedSecretReference,
  protocolPlanTypedCredentialPaths,
  urlCarriesTypedCredential,
} from './runtime-settings/protocolCredentialLeaves'

/**
 * Execution plan entries store runtime_overrides as written: plans have no
 * protect/mask/reconcile pass (pkg/api/execution_plan_service.go). So every
 * secret-bearing field the run-side mask covers (pkg/api/runtime_mask.go) must
 * hold an exact {{secret.NAME}} reference to a project credential, never a
 * typed value and never a controller-reserved APEX_RUNTIME_OVERRIDE_* name.
 * The reference, header, cookie and URL checks are the shared ones the native
 * protocol sections use (protocolCredentialLeaves), so both agree.
 */

/** A -Dname=value VM parameter whose name or value carries a credential. */
function vmParameterNeedsReference(parameter: string): boolean {
  const property = /^-D([^=]+)=(.*)$/i.exec(parameter.trim())
  if (property) {
    const [, name, value] = property
    if (!value.trim() || isSchemedSecretReference(value)) return false
    return isSensitiveCredentialPreviewName(name) || CREDENTIAL_SHAPED_VALUE.test(value.trim()) || urlCarriesTypedCredential(value)
  }
  return CREDENTIAL_SHAPED_VALUE.test(parameter.trim())
}

/**
 * The paths of a plan entry row that hold a typed credential instead of a
 * project credential reference. Empty means the row is safe to store.
 */
export function planEntryTypedCredentialPaths(overrides?: PartialRuntimeSettingsOverride | null): string[] {
  if (!overrides) return []
  const paths: string[] = []
  const requireReference = (path: string, value?: string) => {
    if (value && value.trim() && !isPlanSecretReference(value)) paths.push(path)
  }
  const requireCleanURL = (path: string, value?: string) => {
    if (value && urlCarriesTypedCredential(value)) paths.push(path)
  }

  requireReference('soap.ws_security.username_token.password', overrides.soap?.ws_security?.username_token?.password)

  const http = overrides.http
  if (http) {
    requireReference('http.auth.password', http.auth?.password)
    requireReference('http.auth.token', http.auth?.token)
    requireReference('http.proxy.password', http.proxy?.password)
    requireCleanURL('http.proxy.url', http.proxy?.url)
    requireCleanURL('http.proxy.pac_url', http.proxy?.pac_url)
    for (const [name, value] of Object.entries(http.default_headers ?? {})) {
      if (headerNeedsReference(name, value ?? '')) paths.push(`http.default_headers.${name}`)
    }
    for (const header of http.default_header_list ?? []) {
      if (headerNeedsReference(header.name ?? '', header.value ?? '')) paths.push(`http.default_header_list.${header.name}`)
    }
  }

  requireCleanURL('base_url', overrides.base_url)

  const browser = overrides.browser_config
  if (browser) {
    requireReference('browser_config.http_credentials.password', browser.http_credentials?.password)
    requireReference('browser_config.proxy.password', browser.proxy?.password)
    requireCleanURL('browser_config.proxy.server', browser.proxy?.server)
    // A Playwright cookies/localStorage snapshot is a session credential.
    requireReference('browser_config.storage_state', browser.storage_state)
    browser.client_certificates?.forEach((certificate, index) =>
      requireReference(`browser_config.client_certificates.${index}.passphrase`, certificate.passphrase))
    browser.session?.identities?.forEach((identity, index) => {
      requireReference(`browser_config.session.identities.${index}.password`, identity.password)
      requireReference(`browser_config.session.identities.${index}.storage_state`, identity.storage_state)
    })
    requireReference('browser_config.session.jwt.private_key', browser.session?.jwt?.private_key)
  }

  overrides.java_vuser?.additional_vm_parameters?.forEach((parameter, index) => {
    if (vmParameterNeedsReference(parameter)) paths.push(`java_vuser.additional_vm_parameters.${index}`)
  })

  // The native protocol sections' credential leaves (executionPlanProtocolCredentials).
  paths.push(...protocolPlanTypedCredentialPaths(overrides))

  for (const [key, value] of Object.entries(overrides.env ?? {})) {
    if (!value || !value.trim() || isSchemedSecretReference(value)) continue
    if (isSensitiveCredentialPreviewName(key) || CREDENTIAL_SHAPED_VALUE.test(value.trim()) || urlCarriesTypedCredential(value)) paths.push(`env.${key}`)
  }
  return paths
}

/** The error shown when a plan entry row would store a typed credential. */
export function planEntryRuntimeSecretError(overrides?: PartialRuntimeSettingsOverride | null): string {
  const paths = planEntryTypedCredentialPaths(overrides)
  if (paths.length === 0) return ''
  return `Use a {{secret.NAME}} reference to a project credential for ${paths.join(', ')}. Execution plans store runtime overrides as written, so typed passwords, tokens and URL credentials are not accepted.`
}
