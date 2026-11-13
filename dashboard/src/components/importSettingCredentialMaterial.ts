import { isCredentialLocatorName, isSensitiveCredentialPreviewName, normalizeCredentialPreviewName } from './credentialPreviewPolicy'

// Mirror of the import settings credential guard in
// pkg/api/handlers_import_settings.go (importSettingTextCredentialFragments):
// the import POST refuses a value that adds or changes credential material
// (a literal URL userinfo password or lone userinfo part, or the value of a
// credential-named or Azure SAS sig assignment or query parameter) relative
// to the draft's value at that place. Material already in the draft value is
// script-sourced and passes.

const EXACT_REFERENCE = /^\s*\{\{\s*secret\.[^}]+\}\}\s*$/
const URL_SCHEME = /[A-Za-z][A-Za-z0-9+.-]*:\/\//g
const ASSIGNMENT = /([A-Za-z0-9_.-]+)\s*=\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)'|([^\s;,&"']+))/g
const NAME_CHAR = /[A-Za-z0-9_.-]/

function isExactReference(value: string): boolean {
 if (EXACT_REFERENCE.test(value)) return true
 try {
  const decoded = decodeURIComponent(value)
  return decoded !== value && EXACT_REFERENCE.test(decoded)
 } catch {
  return false
 }
}

function userinfoPartIsMaterial(part: string): boolean {
 return part !== '' && !isExactReference(part)
}

// Keep in parity with pkg/credential.PasswordLikeName.
function passwordLikeName(name: string): boolean {
 const normalized = normalizeCredentialPreviewName(name)
 if (normalized === 'pw') return true
 if (['password', 'passwd', 'passcode', 'passphrase', 'secret'].some(word => normalized.includes(word))) return true
 return ['pwd', 'pass', 'pin', 'otp'].some(word => normalized.endsWith(word))
}

function pairIsCredential(name: string, value: string): boolean {
 const trimmed = value.trim()
 if (!trimmed || isExactReference(trimmed)) return false
 const matchable = trimmed.length >= 4 || passwordLikeName(name)
 if (name.toLowerCase() === 'sig') return matchable
 return isSensitiveCredentialPreviewName(name) && !isCredentialLocatorName(name) && matchable
}

function trimToken(token: string): string {
 let out = token.replace(/^["'(),;[\]]+|["'(),;[\]]+$/g, '')
 let open = (out.match(/\{/g) || []).length
 let closing = (out.match(/\}/g) || []).length
 while (closing > open && out.endsWith('}')) { out = out.slice(0, -1); closing-- }
 while (open > closing && out.startsWith('{')) { out = out.slice(1); open-- }
 return out.replace(/^["'(),;[\]]+|["'(),;[\]]+$/g, '')
}

function authorityUserinfo(rest: string): { user: string; password: string; hasPassword: boolean } | null {
 const end = rest.search(/[/?#]/)
 const authority = end >= 0 ? rest.slice(0, end) : rest
 const at = authority.lastIndexOf('@')
 if (at <= 0) return null
 const userinfo = authority.slice(0, at)
 const colon = userinfo.indexOf(':')
 return colon < 0 ? { user: userinfo, password: '', hasPassword: false } : { user: userinfo.slice(0, colon), password: userinfo.slice(colon + 1), hasPassword: true }
}

function tokenUserinfoMaterial(token: string): string[] {
 const schemes = Array.from(token.matchAll(URL_SCHEME))
 if (schemes.length === 0) {
  if (token.toLowerCase().startsWith('mailto:')) return []
  const info = authorityUserinfo(token)
  return info && info.hasPassword && userinfoPartIsMaterial(info.password) ? [info.password] : []
 }
 const material: string[] = []
 for (const match of schemes) {
  const info = authorityUserinfo(token.slice((match.index ?? 0) + match[0].length))
  if (!info) continue
  // A user name beside a password is not secret; a lone part is the credential.
  if (info.hasPassword && userinfoPartIsMaterial(info.password)) material.push(info.password)
  else if (!info.hasPassword && userinfoPartIsMaterial(info.user)) material.push(info.user)
 }
 return material
}

interface CredentialFragment { userinfo: boolean; name: string; value: string }

const fragmentKey = (fragment: CredentialFragment) => fragment.userinfo ? `userinfo\0\0${fragment.value}` : `pair\0${fragment.name.toLowerCase()}\0${fragment.value}`

/** The credential material the import guard finds in text, keyed by kind, name and value. */
export function importCredentialFragments(text: string): Set<string> {
 return new Set(credentialFragmentList(text).keys())
}

function credentialFragmentList(text: string): Map<string, CredentialFragment> {
 const out = new Map<string, CredentialFragment>()
 const add = (fragment: CredentialFragment) => { out.set(fragmentKey(fragment), fragment) }
 for (const token of text.split(/\s+/).filter(Boolean)) {
  for (const part of tokenUserinfoMaterial(trimToken(token))) add({ userinfo: true, name: '', value: part })
 }
 const addPair = (name: string, value: string) => {
  if (pairIsCredential(name, value)) add({ userinfo: false, name, value })
 }
 for (const match of text.matchAll(ASSIGNMENT)) addPair(match[1], (match[2] ?? '') + (match[3] ?? '') + (match[4] ?? ''))
 for (const segment of text.split(/[?#&;,"'\s]+/).filter(Boolean)) {
  for (let i = 0; i < segment.length; i++) {
   if (segment[i] !== '=') continue
   let start = i
   while (start > 0 && NAME_CHAR.test(segment[start - 1])) start--
   const name = segment.slice(start, i)
   if (name) addPair(name, trimToken(segment.slice(i + 1)))
  }
 }
 return out
}

/**
 * The parts of a URL-shaped value the import's masking projection hides
 * (approximating maskURLSecrets in pkg/api/runtime_mask.go): the whole URL
 * userinfo (svc:pw, svc:, TOKEN), and the value of each query or fragment
 * parameter with a credential-like name, whatever its length, quoting or
 * name encoding (token=ab, access%5Ftoken=..., token="..."). The detector in
 * importCredentialFragments does not count all of these, but the import still
 * refuses one that is not already in the draft value. Like the import, the
 * parts are compared by the hidden text alone.
 */
export function importMaskedParts(text: string): Set<string> {
 const out = new Set<string>()
 for (const match of text.matchAll(URL_SCHEME)) {
  const info = authorityUserinfo(text.slice((match.index ?? 0) + match[0].length))
  if (info) out.add(info.hasPassword ? `${info.user}:${info.password}` : info.user)
 }
 const start = text.search(/[?#]/)
 if (start >= 0) {
  for (const part of text.slice(start + 1).split(/[?#&;]/)) {
   const eq = part.indexOf('=')
   if (eq <= 0) continue
   const name = part.slice(0, eq).trim()
   // The masker leaves an exact reference in sig= alone (it still hides one
   // under any other credential-like name).
   if (name.toLowerCase() === 'sig' && isExactReference(part.slice(eq + 1))) continue
   if (name.toLowerCase() === 'sig' || passwordLikeName(name) || (isSensitiveCredentialPreviewName(name) && !isCredentialLocatorName(name))) {
    out.add(part.slice(eq + 1))
   }
  }
 }
 return out
}

/**
 * The settings the import's masking projection URL-masks (maskURLSecrets in
 * pkg/api/runtime_mask.go). Elsewhere only the detector applies.
 */
const MASKED_URL_SETTINGS = new Set(['config.base_url', 'config.http.proxy.url', 'config.browser.proxy.server'])

function coveredBy(next: Set<string>, known: Set<string>): boolean {
 for (const item of next) if (!known.has(item)) return false
 return true
}

// Mirror of importSettingKeptMaterialProbe and importSettingStripFragments:
// the import swaps each credential pair value for a neutral reference and
// drops all URL userinfo when a userinfo fragment is present.
const KEPT_MATERIAL_PROBE = '{{secret.import_kept_material}}'
const SCHEME_USERINFO = /([A-Za-z][A-Za-z0-9+.-]*:\/\/)[^/?#\s]*@/g
const SCHEMELESS_CREDS = /(^|\s)[^\s/?#@:]*:[^\s/?#]*@/g

function stripUserinfo(text: string): string {
 return text.replace(SCHEME_USERINFO, '$1').replace(SCHEMELESS_CREDS, '$1')
}

function stripFragments(text: string, fragments: Iterable<CredentialFragment>): string {
 let userinfo = false
 for (const fragment of fragments) {
  if (fragment.userinfo) { userinfo = true; continue }
  for (const quote of ['', '"', "'"]) {
   text = text.split(`${fragment.name}=${quote}${fragment.value}${quote}`).join(`${fragment.name}=${quote}${KEPT_MATERIAL_PROBE}${quote}`)
  }
 }
 return userinfo ? stripUserinfo(text) : text
}

// Mirror of importSettingStripReferenceUserinfo: text with its URL userinfo
// taken out when every userinfo in it is reference-only (an exact reference
// password beside any user name, or a lone reference part), else null.
function stripReferenceUserinfo(text: string): string | null {
 let found = false
 for (const raw of text.split(/\s+/).filter(Boolean)) {
  const token = trimToken(raw)
  const schemes = Array.from(token.matchAll(URL_SCHEME))
  if (schemes.length === 0) {
   if (token.toLowerCase().startsWith('mailto:')) continue
   const info = authorityUserinfo(token)
   if (info && info.hasPassword) {
    if (!isExactReference(info.password)) return null
    found = true
   }
   continue
  }
  for (const match of schemes) {
   const info = authorityUserinfo(token.slice((match.index ?? 0) + match[0].length))
   if (!info) continue
   if (!isExactReference(info.hasPassword ? info.password : info.user)) return null
   found = true
  }
 }
 return found ? stripUserinfo(text) : null
}

/**
 * Whether the import accepts `text` on a URL setting it masks, over the
 * draft's `currentText` (importSettingLeafKeepsImportedMaterial): nothing for
 * its projections to hide; or detector material all in the draft with no
 * masked part left once that material is taken out; or masked parts all in
 * the draft. Reference-only userinfo is judged with that userinfo taken out.
 */
function maskedSettingAccepts(text: string, currentText: string): boolean {
 const accepts = (value: string) => {
  const fragments = credentialFragmentList(value)
  const masked = importMaskedParts(value)
  if (fragments.size === 0 && masked.size === 0) return true
  if (fragments.size > 0 && coveredBy(new Set(fragments.keys()), importCredentialFragments(currentText))
   && importMaskedParts(stripFragments(value, fragments.values())).size === 0) return true
  return masked.size > 0 && coveredBy(masked, importMaskedParts(currentText))
 }
 if (accepts(text)) return true
 const stripped = stripReferenceUserinfo(text)
 return stripped !== null && accepts(stripped)
}

/**
 * True when the import POST would refuse sending `target` over the draft's
 * `current` value: a typed literal on a Credentials-reference setting, or
 * credential material in `target` that `current` does not already carry.
 * On a URL setting the import masks (`path`), the import's rule is mirrored
 * exactly: every part its masking projection hides must be accounted for by
 * detector material the draft carries, or all be parts the draft carries
 * too; URL userinfo that only holds a {{secret.*}} reference is not material.
 */
export function importRefusesRestoring(target: unknown, current: unknown, credentialReference: boolean, path?: string): boolean {
 if (typeof target !== 'string' || !target.trim() || target === current) return false
 if (credentialReference) return !isExactReference(target)
 const currentText = typeof current === 'string' ? current : ''
 if (path !== undefined && MASKED_URL_SETTINGS.has(path)) return !maskedSettingAccepts(target, currentText)
 return !coveredBy(importCredentialFragments(target), importCredentialFragments(currentText))
}
