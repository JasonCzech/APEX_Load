import type { PartialRuntimeSettingsOverride } from '../types/runtimeSettings'
import { isSensitiveCredentialPreviewHeader, isSensitiveCredentialPreviewName } from './credentialPreviewPolicy'

/**
 * Execution plan entries store runtime_overrides as written: plans have no
 * protect/mask/reconcile pass (pkg/api/execution_plan_service.go). So every
 * secret-bearing field the run-side mask covers (pkg/api/runtime_mask.go) must
 * hold an exact {{secret.NAME}} reference to a project credential, never a
 * typed value and never a controller-reserved APEX_RUNTIME_OVERRIDE_* name.
 */
const SECRET_REFERENCE = /^\{\{\s*secret\.[A-Za-z0-9_.-]{1,249}\s*\}\}$/
const RESERVED_SECRET_PREFIX = /^\{\{\s*secret\.APEX_RUNTIME_OVERRIDE_/i
/** An Authorization-style value: a scheme word followed by the credential. */
const SCHEMED_VALUE = /^(bearer|basic|apikey|token|digest|negotiate|ntlm)\s+(.+)$/i
const CREDENTIAL_SHAPED_VALUE = /^(bearer|basic|apikey)\s|sharedaccesskey=|accountkey=|sig=|^(AKIA|ASIA)[A-Z0-9]{12,}/i

function isPlanSecretReference(value: string): boolean {
  const trimmed = value.trim()
  return SECRET_REFERENCE.test(trimmed) && !RESERVED_SECRET_PREFIX.test(trimmed)
}

/** An exact reference, or an Authorization scheme followed by one. */
function isSchemedSecretReference(value: string): boolean {
  if (isPlanSecretReference(value)) return true
  const schemed = SCHEMED_VALUE.exec(value.trim())
  return Boolean(schemed && isPlanSecretReference(schemed[2]))
}

/** Every cookie pair's value is a reference. */
function isCookieOfReferences(value: string): boolean {
  const pairs = value.split(';').map(part => part.trim()).filter(Boolean)
  return pairs.length > 0 && pairs.every(pair => {
    const separator = pair.indexOf('=')
    return separator > 0 && isPlanSecretReference(pair.slice(separator + 1))
  })
}

/** A URL that carries userinfo or a credential-named query value as plain text. */
function urlCarriesTypedCredential(raw: string): boolean {
  const value = raw.trim()
  if (!value) return false
  // Authority userinfo, with or without a scheme ("http://u:p@host", "u:p@host:8080").
  const authority = /^(?:[a-z][a-z0-9+.-]*:\/\/)?([^/?#\s]*)@[^/?#\s]/i.exec(value)
  if (authority && authority[1]) {
    const parts = authority[1].split(':')
    if (!parts.every(part => part === '' || isPlanSecretReference(decodeSafely(part)))) return true
  }
  const queryStart = value.search(/[?#]/)
  if (queryStart < 0) return false
  for (const pair of value.slice(queryStart + 1).split(/[&#;]/)) {
    const separator = pair.indexOf('=')
    if (separator <= 0) continue
    const name = decodeSafely(pair.slice(0, separator))
    const paramValue = decodeSafely(pair.slice(separator + 1))
    if (!paramValue || isPlanSecretReference(paramValue)) continue
    if (isSensitiveCredentialPreviewName(name) || /^(sig|key|code|access_token|api_key)$/i.test(name)) return true
  }
  return false
}

function decodeSafely(value: string): string {
  try { return decodeURIComponent(value.replace(/\+/g, '%20')) } catch { return value }
}

function headerNeedsReference(name: string, value: string): boolean {
  if (!value.trim()) return false
  if (/^cookie$/i.test(name.trim())) return !isCookieOfReferences(value)
  if (isSensitiveCredentialPreviewHeader(name) || CREDENTIAL_SHAPED_VALUE.test(value.trim())) return !isSchemedSecretReference(value)
  return false
}

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
    browser.client_certificates?.forEach((certificate, index) =>
      requireReference(`browser_config.client_certificates.${index}.passphrase`, certificate.passphrase))
    browser.session?.identities?.forEach((identity, index) =>
      requireReference(`browser_config.session.identities.${index}.password`, identity.password))
    requireReference('browser_config.session.jwt.private_key', browser.session?.jwt?.private_key)
  }

  overrides.java_vuser?.additional_vm_parameters?.forEach((parameter, index) => {
    if (vmParameterNeedsReference(parameter)) paths.push(`java_vuser.additional_vm_parameters.${index}`)
  })

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
