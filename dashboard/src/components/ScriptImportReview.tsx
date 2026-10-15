import type { JMeterReviewSummary } from '../types/scriptImports'
import ImportCredentialPlacement from './ImportCredentialPlacement'
import CodeLineWrapToggle from './CodeLineWrapToggle'
import { useCodeLineWrapPreference } from './codeLineWrapPreference'
import './ImportReviewWorkspace.css'
import ImportRuntimeSettingsTable from './ImportRuntimeSettingsTable'
import ImportParameterInventory from './ImportParameterInventory'
import ImportSourceInventory from './ImportSourceInventory'
import { ImportSettingsProvider, ImportDataExtras, type SettingsResult } from './ImportSettingsContext'
import { type MutableRefObject, type ReactNode, useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'

import {
  type ImportReviewSection,
  type Parameterization,
  type ParameterizationDiff,
  type SecretBindingRequirement,
  type SecretBindingSelection,
  type SharedValueDiff,
  type SharedValues,
} from '../types/scriptImports'
import { type ScriptDefinition } from '../types/scripts'
import { isRecordedStateHeader, isSensitiveCredentialPreviewKey, normalizeCredentialPreviewName } from './credentialPreviewPolicy'
import { importActionEntries } from './importReviewIssues'
import ParameterizationReview from './ParameterizationReview'
import { ImportTransportPanel } from './ImportTransportPanel'
import ImportExistingScripts from './ImportExistingScripts'
import type { ImportSaveChoices } from './importReimport'
import SharedImportValues from './SharedImportValues'
import { importDraftExpiryStatus, protectedImportExpiryLabel } from './protectedImportSession'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import { requirementNeedsSecretBinding, requirementsPreserveDestinationValues } from './secretBindingUtils'
import type { IssueDetail } from './IssueDetailsDialog'
import ImportIssueWorkspace from './ImportIssueWorkspace'
import { groupImportWorkspaceIssues, importDiagnosticTitle, type GroupedImportWorkspaceIssue } from './importIssueWorkspaceModel'
import { ImportSourceIssueFocusContext, type ImportSourceIssueFocus } from './ImportSourceIssueFocus'
import LinkedCodePane from './LinkedCodePane'
import './ScriptImportReview.css'
import ImportReviewTabs, { type ImportReviewTab } from './ImportReviewTabs'
import { formatDateTime } from '../utils/dateTime'
import { useImportDestination } from './importDestinationContext'
import { countLabel } from '../utils/numberFormat'

const severityRank: Record<string, number> = {
  blocker: 0,
  warning: 1,
  info: 2,
}

const EMPTY_REQUIREMENTS: SecretBindingRequirement[] = []
const PREVIEW_MAX_CHARS = 120000
const PREVIEW_MAX_NODES = 5000
const PREVIEW_MAX_ARRAY_ITEMS = 1000
const PREVIEW_MAX_OBJECT_KEYS = 1000
const PREVIEW_MAX_DEPTH = 24
const PREVIEW_MAX_STRING_CHARS = 20000
const PROTECTED_PREVIEW_VALUE = '[protected imported value]'

const previewSemanticNameKeys = new Set([
  'key', 'name', 'field', 'fieldname', 'label', 'header', 'headername', 'attributename', 'property',
])
function isPreviewSemanticNameKey(key: string): boolean {
  return previewSemanticNameKeys.has(normalizeCredentialPreviewName(key))
}

function previewObjectHasSensitiveSemanticName(candidate: Record<string, unknown>, keys: string[]): boolean {
  for (const key of keys) {
    if (!isPreviewSemanticNameKey(key)) continue
    try {
      const value = candidate[key]
      if (typeof value === 'string' && isSensitiveCredentialPreviewKey(value)) return true
    } catch {
      // A throwing accessor cannot contribute a disclosure-safe semantic name.
    }
  }
  return false
}

// previewHeaderContainer reports whether the object at path is a header map
// (headers, default_headers) or one header_list entry.
function previewHeaderContainer(path: string): 'map' | 'entry' | null {
  const segments = path.split('/')
  const last = normalizeCredentialPreviewName(segments[segments.length - 1] || '')
  if (last === 'headers' || last === 'defaultheaders') return 'map'
  const parent = normalizeCredentialPreviewName(segments[segments.length - 2] || '')
  if ((parent === 'headerlist' || parent === 'defaultheaderlist') && /^\d+$/.test(segments[segments.length - 1] || '')) return 'entry'
  return null
}

function safeCredentialReference(value: string): boolean {
  const trimmed = value.trim()
  return trimmed === ''
    || /^\[redacted\]$/i.test(trimmed)
    || trimmed === PROTECTED_PREVIEW_VALUE
    || /^(?:bearer\s+|basic\s+)?\{\{secret\.[A-Za-z0-9_.-]{1,249}\}\}$/i.test(trimmed)
}

function decodeCredentialXMLName(value: string): string {
  const predefined: Record<string, string> = {
    amp: '&', apos: "'", gt: '>', lt: '<', quot: '"',
  }
  return value.replace(/&(?:#(x[0-9a-f]+|[0-9]+)|([a-z]+));/gi, (entity, numeric: string | undefined, named: string | undefined) => {
    if (named) return predefined[named.toLowerCase()] ?? entity
    if (!numeric) return entity
    const radix = numeric[0]?.toLowerCase() === 'x' ? 16 : 10
    const digits = radix === 16 ? numeric.slice(1) : numeric
    const codePoint = Number.parseInt(digits, radix)
    if (!Number.isSafeInteger(codePoint) || codePoint <= 0 || codePoint > 0x10ffff || (codePoint >= 0xd800 && codePoint <= 0xdfff)) {
      return entity
    }
    return String.fromCodePoint(codePoint)
  })
}

function redactPreviewHeaderLines(value: string): string {
  return value.replace(
    /^([\t ]*)([A-Za-z][A-Za-z0-9_.-]{0,255})([\t ]*:[\t ]*)([^\r\n]*)$/gm,
    (line: string, indent: string, name: string, delimiter: string, rawValue: string) => {
      if (!isSensitiveCredentialPreviewKey(name) || safeCredentialReference(rawValue)) return line
      return `${indent}${name}${delimiter}${PROTECTED_PREVIEW_VALUE}`
    },
  )
}

function neutralizePreviewXMLOpaqueMarkup(value: string): string {
  const characters = value.split('')
  const blank = (start: number, end: number) => {
    for (let index = start; index < end; index += 1) characters[index] = ' '
  }
  let cursor = 0
  while (cursor < value.length) {
    if (value.startsWith('<!--', cursor)) {
      const end = value.indexOf('-->', cursor + 4)
      const boundary = end < 0 ? value.length : end + 3
      blank(cursor, boundary)
      cursor = boundary
      continue
    }
    if (value.startsWith('<![CDATA[', cursor)) {
      const end = value.indexOf(']]>', cursor + 9)
      const boundary = end < 0 ? value.length : end + 3
      blank(cursor, boundary)
      cursor = boundary
      continue
    }
    if (value.startsWith('<?', cursor)) {
      const end = value.indexOf('?>', cursor + 2)
      const boundary = end < 0 ? value.length : end + 2
      blank(cursor, boundary)
      cursor = boundary
      continue
    }
    if (value.startsWith('<!', cursor)) {
      let quote = ''
      let subsetDepth = 0
      let end = cursor + 2
      while (end < value.length) {
        const character = value[end]
        if (quote) {
          if (character === quote) quote = ''
        } else if (character === '"' || character === "'") {
          quote = character
        } else if (character === '[') {
          subsetDepth += 1
        } else if (character === ']' && subsetDepth > 0) {
          subsetDepth -= 1
        } else if (character === '>' && subsetDepth === 0) {
          end += 1
          break
        }
        end += 1
      }
      blank(cursor, end)
      cursor = end
      continue
    }
    cursor += 1
  }
  return characters.join('')
}

function redactPreviewXMLText(value: string): string {
  const scanValue = neutralizePreviewXMLOpaqueMarkup(value)
  const tagPattern = /<\s*(\/?)\s*([A-Za-z_][\w.-]*(?::[A-Za-z_][\w.-]*)?)\b(?:[^>"']|"[^"]*"|'[^']*')*>/g
  const attributePattern = /\s+([A-Za-z_][\w.-]*(?::[A-Za-z_][\w.-]*)?)\s*=\s*("[^"]*"|'[^']*')/g
  const semanticNameAttributes = new Set(['name', 'key', 'field', 'property'])
  const semanticValueAttributes = new Set(['value', 'val', 'default', 'fixed'])
  const stack: Array<{ name: string; contentStart: number; sensitive: boolean }> = []
  const protectedRanges: Array<{ start: number; end: number }> = []
  let match: RegExpExecArray | null
  while ((match = tagPattern.exec(scanValue)) !== null) {
    const token = value.slice(match.index, tagPattern.lastIndex)
    const closing = match[1] === '/'
    const qualifiedName = match[2]
    if (!closing) {
      const localName = qualifiedName.includes(':') ? qualifiedName.slice(qualifiedName.lastIndexOf(':') + 1) : qualifiedName
      const attributes: Array<{ localName: string; value: string; start: number; end: number }> = []
      attributePattern.lastIndex = 0
      let attributeMatch: RegExpExecArray | null
      while ((attributeMatch = attributePattern.exec(token)) !== null) {
        const attributeName = attributeMatch[1]
        const attributeLocalName = attributeName.includes(':') ? attributeName.slice(attributeName.lastIndexOf(':') + 1) : attributeName
        const quotedValue = attributeMatch[2]
        const quotedOffset = attributeMatch[0].lastIndexOf(quotedValue)
        const start = match.index + attributeMatch.index + quotedOffset + 1
        attributes.push({
          localName: attributeLocalName,
          value: quotedValue.slice(1, -1),
          start,
          end: start + quotedValue.length - 2,
        })
      }
      const semanticCredential = attributes.some(attribute => (
        semanticNameAttributes.has(attribute.localName.toLowerCase())
        && isSensitiveCredentialPreviewKey(decodeCredentialXMLName(attribute.value))
      ))
      const elementSensitive = isSensitiveCredentialPreviewKey(localName) || semanticCredential
      for (const attribute of attributes) {
        const attributeSensitive = isSensitiveCredentialPreviewKey(attribute.localName)
          || (elementSensitive && semanticValueAttributes.has(attribute.localName.toLowerCase()))
        if (attributeSensitive && !safeCredentialReference(attribute.value)) {
          protectedRanges.push({ start: attribute.start, end: attribute.end })
        }
      }
      if (/\/\s*>$/.test(token)) continue
      stack.push({
        name: qualifiedName.toLowerCase(),
        contentStart: tagPattern.lastIndex,
        sensitive: elementSensitive,
      })
      continue
    }
    const normalizedName = qualifiedName.toLowerCase()
    let openIndex = stack.length - 1
    while (openIndex >= 0 && stack[openIndex].name !== normalizedName) openIndex -= 1
    if (openIndex < 0) continue
    for (let index = openIndex + 1; index < stack.length; index += 1) {
      const discarded = stack[index]
      if (discarded.sensitive) {
        protectedRanges.push({ start: discarded.contentStart, end: value.length })
      }
    }
    const open = stack[openIndex]
    stack.length = openIndex
    if (open.sensitive && match.index >= open.contentStart) {
      protectedRanges.push({ start: open.contentStart, end: match.index })
    }
  }
  for (const open of stack) {
    if (open.sensitive && open.contentStart <= value.length) {
      protectedRanges.push({ start: open.contentStart, end: value.length })
    }
  }
  const partialTagStart = scanValue.lastIndexOf('<')
  if (partialTagStart > scanValue.lastIndexOf('>')) {
    const partialTag = value.slice(partialTagStart)
    const nameMatch = /^<\s*([A-Za-z_][\w.-]*(?::[A-Za-z_][\w.-]*)?)/.exec(partialTag)
    if (nameMatch) {
      const qualifiedName = nameMatch[1]
      const localName = qualifiedName.includes(':') ? qualifiedName.slice(qualifiedName.lastIndexOf(':') + 1) : qualifiedName
      const semanticNamePattern = /\b(?:name|key|field|property)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/gi
      let semanticCredential = false
      let semanticMatch: RegExpExecArray | null
      while ((semanticMatch = semanticNamePattern.exec(partialTag)) !== null) {
        const semanticName = semanticMatch[1] ?? semanticMatch[2] ?? semanticMatch[3] ?? ''
        if (isSensitiveCredentialPreviewKey(decodeCredentialXMLName(semanticName))) {
          semanticCredential = true
          break
        }
      }
      if (isSensitiveCredentialPreviewKey(localName) || semanticCredential) {
        protectedRanges.push({ start: partialTagStart, end: value.length })
      }
    }
  }
  if (protectedRanges.length === 0) return value
  protectedRanges.sort((left, right) => left.start - right.start || right.end - left.end)
  const merged: Array<{ start: number; end: number }> = []
  for (const range of protectedRanges) {
    const previous = merged[merged.length - 1]
    if (previous && range.start <= previous.end) {
      previous.end = Math.max(previous.end, range.end)
    } else {
      merged.push({ ...range })
    }
  }
  let redacted = value
  for (let index = merged.length - 1; index >= 0; index -= 1) {
    const range = merged[index]
    const content = redacted.slice(range.start, range.end)
    if (safeCredentialReference(content)) continue
    redacted = `${redacted.slice(0, range.start)}${PROTECTED_PREVIEW_VALUE}${redacted.slice(range.end)}`
  }
  return redacted
}

function redactPreviewText(value: string): string {
  // Inspect XML semantics before generic assignment masking can obscure a
  // credential-name attribute such as key="client_secret".
  let safe = redactPreviewHeaderLines(redactPreviewXMLText(value))
  // URL userinfo has no credential field name. Imports without binding
  // descriptors still need protection in both the preview and download.
  safe = safe.replace(
    /((?:\b[a-z][a-z0-9+.-]*:)?\/\/)[^/\s?#]+@/gi,
    (_match, scheme: string) => `${scheme}${PROTECTED_PREVIEW_VALUE}@`,
  )
  safe = safe.replace(
    // Only start at a field boundary. Retrying an unbounded identifier from
    // every character of a long delimiter-free string makes this quadratic.
    // The boundary follows the optional quote so quoted fields remain matched
    // even when the preceding text ends in an identifier character.
    /(["']?)(?<![A-Za-z0-9_.%[\]-])([A-Za-z0-9_.%[\]-]+)\1\s*([:=])\s*(\{\{secret\.[A-Za-z0-9_.-]{1,249}\}\}|"[^"]*"|'[^']*'|(?:bearer|basic)\s+[^\s,}&]+|[^\s,}&]+)/gi,
    (match: string, quote: string, name: string, delimiter: string, rawValue: string) => {
      if (!isSensitiveCredentialPreviewKey(name)) return match
      const unquoted = rawValue.replace(/^(?:"([\s\S]*)"|'([\s\S]*)')$/, (_value, doubleQuoted, singleQuoted) => doubleQuoted ?? singleQuoted ?? '')
      if (safeCredentialReference(unquoted)) return match
      return `${quote}${name}${quote}${delimiter}${PROTECTED_PREVIEW_VALUE}`
    },
  )
  safe = safe.replace(
    // Preserve the fallback's conservative suffix matching (e.g. 123password)
    // while consuming each complete identifier only once.
    /(["']?)(?<![A-Za-z0-9_.%[\]-])([A-Za-z0-9_.%[\]-]*(?:authorization|password|passwd|passphrase|secret|client[_-]?secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|api[_-]?key|apikey|private[_-]?key|session(?:[_-]?(?:id|token))?|sid|csrf(?:[_-]?token)?|xsrf(?:[_-]?token)?|cookie|dsn))\1\s*[:=]\s*(?!(?:"|')?\{\{secret\.)("[^"]*"|'[^']*'|[^\s,}&]+)/gi,
    (_match, _quote: string, name: string) => `${name}=${PROTECTED_PREVIEW_VALUE}`,
  )
  safe = safe.replace(
    /\b(bearer|basic)\s+(?!\{\{secret\.)[^\s,}&]+/gi,
    (_match, scheme: string) => `${scheme} ${PROTECTED_PREVIEW_VALUE}`,
  )
  return safe
}

type CredentialAction = 'save' | 'edit'

interface CredentialActionRequest {
  id: number
  action: CredentialAction
  handler?: () => void
  sourceIdentity: string
  phase: 'committing' | 'applying' | 'awaiting-resolution'
}

function previewJSON(value: unknown, complete = false, protectedPaths: Set<string> = new Set()): { text: string; truncated: boolean } {
  // The explicit export has a separate, bounded budget. Never export a partial
  // document as a complete review, and use the same credential projection.
  const maxNodes = complete ? 2_000_000 : PREVIEW_MAX_NODES
  const maxDepth = complete ? 256 : PREVIEW_MAX_DEPTH
  const maxItems = complete ? 2_000_000 : PREVIEW_MAX_ARRAY_ITEMS
  const maxKeys = complete ? 2_000_000 : PREVIEW_MAX_OBJECT_KEYS
  const maxString = complete ? 64_000_000 : PREVIEW_MAX_STRING_CHARS
  const maxChars = complete ? 128_000_000 : PREVIEW_MAX_CHARS
  const state = {
    nodes: 0,
    truncated: false,
    seen: new WeakSet<object>(),
  }
  // recordedState marks the value of a cookie, session, anti-forgery or tracing
  // header: state the source recorded, which the server shows as recorded.
  const visit = (candidate: unknown, depth: number, sensitiveAncestor = false, path = '', recordedState = false): unknown => {
    if (state.nodes >= maxNodes || depth > maxDepth) {
      state.truncated = true
      return '… preview limit reached …'
    }
    state.nodes += 1
    sensitiveAncestor ||= protectedPaths.has(path)
    if (typeof candidate === 'string') {
      if (sensitiveAncestor && !safeCredentialReference(candidate)) return PROTECTED_PREVIEW_VALUE
      const truncated = candidate.length > maxString
      const boundedCandidate = truncated ? candidate.slice(0, maxString) : candidate
      const safeCandidate = recordedState && !sensitiveAncestor ? boundedCandidate : redactPreviewText(boundedCandidate)
      if (!truncated && safeCandidate.length <= maxString) return safeCandidate
      state.truncated = true
      return `${safeCandidate.slice(0, maxString)}…`
    }
    if (candidate === null) return candidate
    if (typeof candidate !== 'object') return sensitiveAncestor ? PROTECTED_PREVIEW_VALUE : candidate
    if (state.seen.has(candidate)) {
      state.truncated = true
      return '[Circular]'
    }
    state.seen.add(candidate)
    try {
      const toJSON = (candidate as { toJSON?: unknown }).toJSON
      if (typeof toJSON === 'function') {
        const projected = toJSON.call(candidate)
        if (projected !== candidate) return visit(projected, depth + 1, sensitiveAncestor, path)
      }
      if (Array.isArray(candidate)) {
        const itemCount = Math.min(candidate.length, maxItems)
        if (itemCount < candidate.length) state.truncated = true
        const result: unknown[] = []
        for (let index = 0; index < itemCount && state.nodes < maxNodes; index += 1) {
          result.push(visit(candidate[index], depth + 1, sensitiveAncestor, `${path}/${index}`))
        }
        if (result.length < candidate.length) { state.truncated = true; result.push(`… ${countLabel(candidate.length - result.length, 'more item')} omitted …`) }
        return result
      }
      const keys = Object.keys(candidate)
      const keyCount = Math.min(keys.length, maxKeys)
      if (keyCount < keys.length) state.truncated = true
      const result: Record<string, unknown> = {}
      const headerContainer = previewHeaderContainer(path)
      let recordedEntry = false
      if (headerContainer === 'entry') {
        try {
          const headerName = (candidate as Record<string, unknown>).name
          recordedEntry = typeof headerName === 'string' && isRecordedStateHeader(headerName)
        } catch {
          // A throwing accessor cannot prove the header is recorded state.
        }
      }
      const sensitiveSemanticName = !recordedEntry && previewObjectHasSensitiveSemanticName(candidate as Record<string, unknown>, keys)
      for (let index = 0; index < keyCount && state.nodes < maxNodes; index += 1) {
        const key = keys[index]
        if (['import_draft_token', 'source_secret_session_id', 'protected_import_session_id'].includes(key)) { result[key] = PROTECTED_PREVIEW_VALUE; continue }
        try {
          const child = (candidate as Record<string, unknown>)[key]
          const semanticNameKey = sensitiveSemanticName && isPreviewSemanticNameKey(key)
          const childRecordedState = (headerContainer === 'map' && isRecordedStateHeader(key)) ||
            (recordedEntry && normalizeCredentialPreviewName(key) === 'value')
          const childSensitive = (
            sensitiveAncestor
            // Opaque source snippets and positional arguments cannot be safely
            // classified by a browser name-based redactor. Preserve their shape
            // while hiding values in the downloadable review.
            || (complete && ['metadata', 'args', 'call_args'].includes(key))
            || (!semanticNameKey && !childRecordedState && isSensitiveCredentialPreviewKey(key))
            // A semantic credential record can use arbitrary adapter-specific
            // payload keys such as defaultValue, values, or schema.default. Protect
            // every non-name descendant instead of relying on a small allowlist.
            || (sensitiveSemanticName && !semanticNameKey)
          )
          result[key] = visit(child, depth + 1, childSensitive, `${path}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`, childRecordedState)
        } catch {
          result[key] = '[Unable to preview value]'
        }
      }
      if (Object.keys(result).length < keys.length) { state.truncated = true; result['…'] = `${countLabel(keys.length - Object.keys(result).length, 'more field')} omitted` }
      return result
    } finally {
      state.seen.delete(candidate)
    }
  }
  const text = JSON.stringify(visit(value, 0), null, 2) ?? String(value)
  if (text.length <= maxChars) {
    return { text, truncated: state.truncated }
  }
  return {
    text: `${text.slice(0, maxChars)}\n…`,
    truncated: true,
  }
}

interface Props {
  title: string
  reviewGeneration?: string | number
  subtitle?: ReactNode
  readinessMessage?: string
  tabbed?: boolean
  onSettingsApplied?: (result: SettingsResult) => void
  settingsDisabled?: boolean
  onSettingsBusyChange?: (busy: boolean) => void
  runtimeContent?: ReactNode
  dataContent?: ReactNode
  batchControls?: ReactNode
  /**
   * The scripts an earlier import already saved and what the save does to
   * each (update as a new version, or copy). Its choices also start each
   * unset input from how the updated scripts supplied it.
   */
  existingScripts?: { choices: ImportSaveChoices; labels: readonly string[] }
  compactReview?: boolean
  showOriginalDiagnostics?: boolean
  showCredentials?: boolean
  showDataReview?: boolean
  reviewNavigation?: ReactNode
  credentialTarget?: HTMLElement | null
  credentialIntroduction?: ReactNode
  script: ScriptDefinition
  previewData?: unknown
  warnings?: string[]
  warningsTitle?: string
  reviewSummary?: JMeterReviewSummary
  diagnostics?: {code?: string; source_ref?: string; element: string; detail: string; severity: 'blocker' | 'warning' | 'info'; location?: string}[]
  unsupportedElements?: string[]
  reviewSections?: ImportReviewSection[]
  // Collection-wide values, applied across every script of the import at once.
  // sharedValueScripts carries the whole batch: the panel is import-wide even
  // though the rest of this review is scoped to one script.
  sharedValues?: SharedValues
  sharedValueScripts?: ScriptDefinition[]
  onSharedValuesApplied?: (
    scripts: ScriptDefinition[],
    diffs: SharedValueDiff[],
    refreshed?: SharedValues,
    importDraftExpiresAt?: (string | null)[],
  ) => void
  parameterization?: Parameterization
  /**
   * Applies Transport panel choices (HTTP version per origin). The panel shows
   * for HTTP-family drafts only when this is set; the callback receives the
   * server-edited draft and its rotated import-draft expiry.
   */
  onTransportApplied?: (script: ScriptDefinition, importDraftExpiresAt?: string) => void
  /** Findings the Transport panel reads (origin_advertises_h3); defaults to diagnostics. */
  transportDiagnostics?: ReadonlyArray<{ code?: string; location?: string }>
  onParameterizationApplied?: (
    script: ScriptDefinition,
    diffs: ParameterizationDiff[],
    selectedIds: string[],
    importDraftExpiresAt?: string,
  ) => void
  secretBindingRequirements?: SecretBindingRequirement[]
  sourceMigrationRequirements?: SecretBindingRequirement[]
  preserveSourceSessionAfterApply?: boolean
  onSecretBindingsApply?: (bindings: SecretBindingSelection[]) => void | Promise<void>
  onSecretBindingsErrorChange?: (message: string) => void
  projectId?: string
  sourceSecretSessionId?: string
  sourceSecretSessionExpiresAt?: string
  sourceSecretMigrationAllowed?: boolean
  protectedImportSessionExpiresAt?: string
  importDraftExpiresAt?: string
  onSourceSecretSessionSettled?: (
    sourceSecretSessionId: string,
    migrationSelections: SecretBindingSelection[],
  ) => void
  credentialReviewRef?: MutableRefObject<SecretBindingReviewHandle | null>
  onCredentialsPreparedChange?: (prepared: PreparedSecretBindings | null) => void
  saving?: boolean
  saveDisabled?: boolean
  saveDisabledReason?: string
  reviewBusy?: boolean
  onReviewBusyChange?: (busy: boolean) => void
  onCredentialActionStart?: (action: CredentialAction) => void
  // The step a running save or save-and-open is on, shown in the header's
  // status line. action names the button that started it, once known.
  progress?: { label: string; action?: CredentialAction }
  saveLabel?: string
  editLabel?: string
  previewTitle?: string
  sourceFocusRequest?: ImportSourceIssueFocus | null
  previewAside?: ReactNode
  showPreview?: boolean
  previewInitiallyOpen?: boolean
  showPreviewAside?: boolean
  onSave?: () => void
  onEdit?: () => void
  // Disables the edit button, with this reason, while the page cannot open
  // the draft in the editor (a validation or save in progress).
  editDisabledReason?: string
  // Replaces the save button once the reviewed draft is already saved, so the
  // review offers the next step instead of explaining why saving is disabled.
  savedAction?: { label: string; onClick: () => void }
  children?: ReactNode
}

export default function ScriptImportReview({
  title,
  reviewGeneration = 0,
  subtitle,
  readinessMessage,
  showCredentials: requestedCredentials = true,
  showDataReview: requestedData = true,
  compactReview = false,
  showOriginalDiagnostics = true,
  tabbed = false,
  runtimeContent,
  onSettingsApplied,
  settingsDisabled = false,
  onSettingsBusyChange,
  dataContent,
  batchControls,
  existingScripts,
  credentialIntroduction,
  reviewNavigation,
  script,
  previewData,
  warnings = [],
  warningsTitle = 'Warnings',
  unsupportedElements = [],
  diagnostics,
  reviewSummary,
  reviewSections = [],
  sharedValues,
  sharedValueScripts,
  onSharedValuesApplied,
  parameterization,
  onTransportApplied,
  transportDiagnostics,
  onParameterizationApplied,
  secretBindingRequirements = EMPTY_REQUIREMENTS,
  sourceMigrationRequirements,
  preserveSourceSessionAfterApply = false,
  onSecretBindingsApply,
  onSecretBindingsErrorChange,
  projectId,
  sourceSecretSessionId,
  sourceSecretSessionExpiresAt,
  sourceSecretMigrationAllowed = false,
  protectedImportSessionExpiresAt,
  importDraftExpiresAt,
  onSourceSecretSessionSettled,
  credentialReviewRef,
  credentialTarget,
  onCredentialsPreparedChange,
  saving = false,
  saveDisabled = false,
  saveDisabledReason,
  reviewBusy = false,
  onReviewBusyChange,
  onCredentialActionStart,
  progress,
  saveLabel = 'Save to script library',
  editLabel = 'Open in Visual Editor',
  previewTitle = 'Generated Script Preview',
  previewAside,
  sourceFocusRequest,
  showPreview: requestedPreview = true,
  previewInitiallyOpen = false,
  showPreviewAside: requestedSource = true,
  onSave,
  onEdit,
  editDisabledReason,
  savedAction,
  children,
}: Props) {
  const [settingsBusy, setSettingsBusy] = useState(false)
  const settingsBusyRef = useRef(false)
  // Where the importer saves, offered here at the Save step.
  const importDestination = useImportDestination()
  const [activeReviewTab, setActiveReviewTab] = useState<ImportReviewTab>('review')
  const [sourceIssueFocus, setSourceIssueFocus] = useState<ImportSourceIssueFocus | null>(null)
  const sourceIssueFocusRevision = useRef(0)
  const focusIssueSource = (issue: GroupedImportWorkspaceIssue) => {
    const occurrence = issue.occurrences[0]
    sourceIssueFocusRevision.current += 1
    setSourceIssueFocus({
      revision: sourceIssueFocusRevision.current,
      title: issue.title,
      source: occurrence?.source || '',
      description: occurrence?.description || '',
    })
  }
  useEffect(() => {
    if (!sourceFocusRequest) return
    sourceIssueFocusRevision.current += 1
    setSourceIssueFocus({ ...sourceFocusRequest, revision: sourceIssueFocusRevision.current })
    setActiveReviewTab('source')
  }, [sourceFocusRequest])
  const showCredentials = requestedCredentials && (!tabbed || activeReviewTab === 'credentials')
  const showDataReview = requestedData && (!tabbed || activeReviewTab === 'data')
  const showPreview = requestedPreview && (!tabbed || activeReviewTab === 'dsl')
  const showPreviewAside = requestedSource && (!tabbed || activeReviewTab === 'source')
  const reviewSummaryRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    reviewSummaryRef.current?.focus()
    reviewSummaryRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
    setSourceIssueFocus(null)
  }, [reviewGeneration])
  const hasUnresolvedSecretBindings = secretBindingRequirements.length > 0
  const allRemainingRequirementsPreserveValues = requirementsPreserveDestinationValues(secretBindingRequirements)
  const internalBindingReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const bindingReviewRef = credentialReviewRef || internalBindingReviewRef
  const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [credentialEditing, setCredentialEditing] = useState(false)
  const credentialEditingRef = useRef(false)
  const [credentialAction, setCredentialAction] = useState<CredentialAction | null>(null)
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  const [sharedValuesApplying, setSharedValuesApplying] = useState(false)
  const [transportApplying, setTransportApplying] = useState(false)
  const [importDraftExpiryGeneration, setImportDraftExpiryGeneration] = useState(0)
  const [previewExportError, setPreviewExportError] = useState('')
  const [configuredCredentialReviewIdentity, setConfiguredCredentialReviewIdentity] = useState('')
  const saveDisabledReasonId = useId()
  const parameterizationApplyingRef = useRef(false)
  const sharedValuesApplyingRef = useRef(false)
  const transportApplyingRef = useRef(false)
  const credentialActionRequestRef = useRef<CredentialActionRequest | null>(null)
  const credentialActionRequestIdRef = useRef(0)
  const reviewIdentity = String(reviewGeneration)
  const projectSecretsConfiguredForReview = configuredCredentialReviewIdentity === reviewIdentity
  const currentReviewIdentityRef = useRef(reviewIdentity)
  currentReviewIdentityRef.current = reviewIdentity
  const currentHasUnresolvedSecretBindingsRef = useRef(hasUnresolvedSecretBindings)
  currentHasUnresolvedSecretBindingsRef.current = hasUnresolvedSecretBindings
  const currentRemainingRequirementsPreserveValuesRef = useRef(allRemainingRequirementsPreserveValues)
  currentRemainingRequirementsPreserveValuesRef.current = allRemainingRequirementsPreserveValues
  const visibleSaveDisabledReason = saveDisabled && !savedAction
    ? saveDisabledReason?.trim() || 'Saving is unavailable for this reviewed import. Correct the required choices shown on this page and try again.'
    : ''
  const defaultConfigureLaterCount = useMemo(
    () => new Set(secretBindingRequirements.filter(requirement => (
      requirementNeedsSecretBinding(requirement)
    )).map(requirement => {
      const name = requirement.suggested_name?.trim()
      if (!name) return ''
      const scope = typeof requirement.script_index === 'number'
        ? `index:${requirement.script_index}`
        : requirement.script_id
          ? `id:${requirement.script_id}`
          : requirement.script_name
            ? `name:${requirement.script_name}`
            : 'unscoped'
      return `${scope}:${name}`
    }).filter(Boolean)).size,
    [secretBindingRequirements],
  )
  const credentialsValid = !hasUnresolvedSecretBindings || (preparedSecrets?.valid ?? true)
  const credentialHandlerUnavailable = hasUnresolvedSecretBindings && !onSecretBindingsApply
  const configureLaterCount = preparedSecrets?.configureLaterCount ?? defaultConfigureLaterCount
  const selectedBindingCount = preparedSecrets?.selectedBindingCount
    ?? secretBindingRequirements.filter(requirementNeedsSecretBinding).length
  const deferredSaveLabel = /^Save to script library$/i.test(saveLabel) ? 'Save' : saveLabel
  const resolvedSaveLabel = hasUnresolvedSecretBindings && configureLaterCount > 0
    ? `${deferredSaveLabel} with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure`
    : saveLabel
  const resolvedEditLabel = hasUnresolvedSecretBindings && configureLaterCount > 0
    ? `${editLabel} with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure`
    : editLabel

  const reportInternalReviewBusy = useCallback((options?: { parameterization?: boolean; credentials?: boolean; sharedValues?: boolean; transport?: boolean }) => {
    const parameterizationBusy = options?.parameterization ?? parameterizationApplyingRef.current
    const credentialsBusy = options?.credentials ?? credentialActionRequestRef.current !== null
    const sharedValuesBusy = options?.sharedValues ?? sharedValuesApplyingRef.current
    const transportBusy = options?.transport ?? transportApplyingRef.current
    onReviewBusyChange?.(parameterizationBusy || credentialsBusy || sharedValuesBusy || transportBusy || settingsBusyRef.current || credentialEditingRef.current)
  }, [onReviewBusyChange])

  const handleSettingsBusyChange = useCallback((busy: boolean) => { settingsBusyRef.current = busy; setSettingsBusy(busy); onSettingsBusyChange?.(busy || credentialEditingRef.current); reportInternalReviewBusy() }, [reportInternalReviewBusy, onSettingsBusyChange])

  const handleCredentialEditingChange = useCallback((editing: boolean) => {
    credentialEditingRef.current = editing
    setCredentialEditing(editing)
    onSettingsBusyChange?.(editing || settingsBusyRef.current)
    reportInternalReviewBusy()
  }, [onSettingsBusyChange, reportInternalReviewBusy])

  const clearCredentialAction = useCallback(() => {
    credentialActionRequestRef.current = null
    setCredentialAction(null)
    reportInternalReviewBusy({ credentials: false })
  }, [reportInternalReviewBusy])

  const handleParameterizationApplyingChange = useCallback((applying: boolean) => {
    parameterizationApplyingRef.current = applying
    setParameterizationApplying(applying)
    reportInternalReviewBusy({ parameterization: applying })
  }, [reportInternalReviewBusy])

  const handleTransportApplyingChange = useCallback((applying: boolean) => {
    transportApplyingRef.current = applying
    setTransportApplying(applying)
    reportInternalReviewBusy({ transport: applying })
  }, [reportInternalReviewBusy])

  const handleSharedValuesApplyingChange = useCallback((applying: boolean) => {
    sharedValuesApplyingRef.current = applying
    setSharedValuesApplying(applying)
    reportInternalReviewBusy({ sharedValues: applying })
  }, [reportInternalReviewBusy])

  const handleProjectSecretsConfigured = useCallback((names: readonly string[]) => {
    if (names.length > 0) setConfiguredCredentialReviewIdentity(reviewIdentity)
  }, [reviewIdentity])

  useEffect(() => {
    const request = credentialActionRequestRef.current
    if (!request) return

    if (request.sourceIdentity !== reviewIdentity) {
      clearCredentialAction()
      return
    }

    if (
      request.phase !== 'awaiting-resolution'
      || (hasUnresolvedSecretBindings && !allRemainingRequirementsPreserveValues)
    ) return
    clearCredentialAction()
    request.handler?.()
  }, [allRemainingRequirementsPreserveValues, clearCredentialAction, hasUnresolvedSecretBindings, reviewIdentity])

  useEffect(() => {
    if (hasUnresolvedSecretBindings) return
    setPreparedSecrets(null)
    // A binding commit can render the resolved draft before its awaited
    // callback advances the queued action from `applying` to
    // `awaiting-resolution`. Keep that exact request alive; runAction will
    // dispatch it as soon as the commit promise resumes. Clearing it here
    // would resolve the references but silently drop the user's Save/Edit.
    if (!credentialActionRequestRef.current) clearCredentialAction()
  }, [clearCredentialAction, hasUnresolvedSecretBindings])

  useEffect(() => {
    handleParameterizationApplyingChange(false)
  }, [handleParameterizationApplyingChange, parameterization, reviewIdentity])

  useEffect(() => {
    handleSharedValuesApplyingChange(false)
  }, [handleSharedValuesApplyingChange, sharedValues, reviewIdentity])

  useEffect(() => {
    const expiry = Date.parse(importDraftExpiresAt || '')
    if (!Number.isFinite(expiry) || expiry <= Date.now()) return
    const timeout = window.setTimeout(
      () => setImportDraftExpiryGeneration(current => current + 1),
      Math.min(expiry - Date.now() + 10, 2_147_483_647),
    )
    return () => window.clearTimeout(timeout)
  }, [importDraftExpiresAt, importDraftExpiryGeneration])

  useEffect(() => () => {
    credentialActionRequestIdRef.current += 1
    credentialActionRequestRef.current = null
    parameterizationApplyingRef.current = false
    sharedValuesApplyingRef.current = false
    transportApplyingRef.current = false
    onReviewBusyChange?.(false)
  }, [onReviewBusyChange])

  useEffect(() => {
    onCredentialsPreparedChange?.(preparedSecrets)
  }, [onCredentialsPreparedChange, preparedSecrets])

  const applySecretBindings = useCallback(async (bindings: SecretBindingSelection[]) => {
    if (reviewIdentity !== currentReviewIdentityRef.current) {
      throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
    }
    const request = credentialActionRequestRef.current
    if (request) {
      if (request.phase !== 'committing' || request.sourceIdentity !== currentReviewIdentityRef.current) {
        throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
      }
      request.phase = 'applying'
    }
    await onSecretBindingsApply?.(bindings)
    if (request && credentialActionRequestRef.current === request) {
      if (request.sourceIdentity !== currentReviewIdentityRef.current) {
        credentialActionRequestRef.current = null
        setCredentialAction(null)
        reportInternalReviewBusy({ credentials: false })
        throw new Error('The import result changed while credentials were being prepared. Review the current draft and try again.')
      }
      request.phase = 'awaiting-resolution'
    }
  }, [onSecretBindingsApply, reportInternalReviewBusy, reviewIdentity])

  const runAction = async (action: CredentialAction) => {
    if (credentialEditingRef.current || settingsBusyRef.current || importDraftExpired || parameterizationApplyingRef.current || sharedValuesApplyingRef.current || transportApplyingRef.current || reviewBusy) return
    if (!hasUnresolvedSecretBindings) {
      if (action === 'save') onSave?.()
      if (action === 'edit') onEdit?.()
      return
    }
    if (!onSecretBindingsApply || !bindingReviewRef.current || !credentialsValid || credentialActionRequestRef.current) return
    // Give importers a synchronous boundary to freeze replace targets and
    // operation identity before credential preparation performs any awaits.
    onCredentialActionStart?.(action)
    const request: CredentialActionRequest = {
      id: ++credentialActionRequestIdRef.current,
      action,
      handler: action === 'save' ? onSave : onEdit,
      sourceIdentity: reviewIdentity,
      phase: 'committing',
    }
    credentialActionRequestRef.current = request
    setCredentialAction(action)
    reportInternalReviewBusy({ credentials: true })
    const committed = await bindingReviewRef.current.commit()
    if (credentialActionRequestRef.current !== request) return
    if (!committed) {
      clearCredentialAction()
      return
    }
    if (selectedBindingCount === 0) {
      clearCredentialAction()
      request.handler?.()
      return
    }
    // onApply normally moves the review to a requirements-free render. If it
    // completed synchronously without doing so, keep the action queued for the
    // exact same review generation instead of firing against stale props.
    if (request.phase === 'applying') request.phase = 'awaiting-resolution'
    if (
      request.phase === 'awaiting-resolution'
      && (
        !currentHasUnresolvedSecretBindingsRef.current
        || currentRemainingRequirementsPreserveValuesRef.current
      )
    ) {
      clearCredentialAction()
      request.handler?.()
    }
  }
  const sourceVisited = useRef(false)
  if (showPreviewAside) sourceVisited.current = true
  const [previewSection, setPreviewSection] = useState('all')
  const [previewSearch, setPreviewSearch] = useState('')
  const [previewAction, setPreviewAction] = useState('')
  const [previewWrap, setPreviewWrap] = useCodeLineWrapPreference()
  const [previewFocus, setPreviewFocus] = useState(false)
  const previewEntries = useMemo(() => importActionEntries(script), [script])
  const selectedPreviewAction = previewEntries.find(entry => entry.path === previewAction)
  const filteredPreviewEntries = previewEntries.filter(entry => `${entry.path} ${entry.action.name} ${entry.action.type}`.toLowerCase().includes(previewSearch.toLowerCase()))
  const credentialPreviewPaths = secretBindingRequirements.flatMap(requirement => [requirement.path, ...(requirement.affected_paths || [])].flatMap(path => [path, `/script${path}`, `/scripts/${requirement.script_index ?? 0}${path}`]))
  const preview = useMemo(() => {
    if (!showPreview) return null
    const paths = secretBindingRequirements.flatMap(requirement => [requirement.path, ...(requirement.affected_paths || [])])
    if ((compactReview || (tabbed && !previewData)) && selectedPreviewAction) {
      const prefix = '/' + selectedPreviewAction.path.replace(/\[(\d+)\]/g, '/$1').replace(/\./g, '/')
      return previewJSON(selectedPreviewAction.action, false, new Set(paths.filter(path => path === prefix || path.startsWith(`${prefix}/`)).map(path => path.slice(prefix.length))))
    }
    return previewJSON((compactReview || (tabbed && !previewData)) ? previewSection === 'all' ? { ...script, metadata: undefined } : { [previewSection]: script[previewSection as keyof ScriptDefinition] } : previewData ?? { script }, false, new Set(secretBindingRequirements.flatMap(requirement => [requirement.path, ...(requirement.affected_paths || [])].flatMap(path => [path, `/script${path}`, `/scripts/${requirement.script_index ?? 0}${path}`]))))
  }, [compactReview, tabbed, previewData, previewSection, script, secretBindingRequirements, selectedPreviewAction, showPreview])
  const previewPane = preview ? (
    <details className="script-import-review-preview" open={tabbed || previewInitiallyOpen}>
      <summary>
        <h4>{previewTitle}{compactReview || tabbed ? ' — redacted, read-only' : ''}</h4>
      </summary>
      {(compactReview || (tabbed && !previewData)) && <div className="lr-preview-toolbar import-review-toolbar"><label>Section<select value={previewSection} onChange={e => { setPreviewSection(e.target.value); setPreviewAction('') }}>{['all', 'config', 'parameters', 'data_sources', 'init', 'actions', 'teardown'].map(section => <option key={section} value={section}>{section}</option>)}</select></label><label>Find action<input type="search" value={previewSearch} onChange={e => { setPreviewSearch(e.target.value); setPreviewAction('') }} /></label><label>Jump to action<select value={previewAction} onChange={e => { setPreviewAction(e.target.value); if (e.target.value) setPreviewSection(e.target.value.split('[')[0]) }}><option value="">Section overview</option>{filteredPreviewEntries.slice(0, 200).map(entry => <option key={entry.path} value={entry.path}>{entry.path} · {entry.action.name || entry.action.type}</option>)}</select></label><span>{filteredPreviewEntries.length} matching actions{filteredPreviewEntries.length > 200 ? ' · refine search to see more' : ''}</span><CodeLineWrapToggle wrapLines={previewWrap} onChange={setPreviewWrap} /><button type="button" className="btn btn-secondary btn-sm" onClick={() => setPreviewFocus(v => !v)}>{previewFocus ? 'Restore preview' : 'Expand preview'}</button></div>}
      {preview.truncated && (
        <div className="script-import-review-preview-note" role="status">
          Preview truncated for browser performance. The on-screen view is partial. Select an individual action or download the complete protected review JSON. You can save a blocked draft without reviewing every action.
        </div>
      )}
      <button type="button" className="btn btn-secondary btn-sm" onClick={() => {
        setPreviewExportError('')
        try {
          const full = previewJSON(compactReview ? { script } : previewData ?? { script }, true, new Set(credentialPreviewPaths))
          if (full.truncated) throw new Error('The full review exceeds the export safety limit. No partial file was downloaded.')
          const url = URL.createObjectURL(new Blob([full.text], { type: 'application/json' }))
          const link = document.createElement('a')
          link.href = url
          link.download = `${(script.name || 'script').replace(/[^a-zA-Z0-9_.-]/g, '_')}.protected-review.json`
          link.click()
          const revokeURL = URL.revokeObjectURL.bind(URL)
          setTimeout(() => revokeURL(url), 1000)
        } catch (error) {
          setPreviewExportError(error instanceof Error ? error.message : 'Unable to export the protected review.')
        }
      }}>Download complete protected JSON</button>
      <p>The downloaded review JSON includes every generated action with sensitive values protected. It is not an executable export. The on-screen view shows the selected section or action.</p>
      {previewExportError && <p role="alert">{previewExportError}</p>}
      <LinkedCodePane
        value={preview.text}
        language="json"
        semanticMode="apex-dsl"
        ariaLabel={`${previewTitle} JSON`}
        maxHeight={previewFocus ? '75vh' : '560px'}
        wrapLines={previewWrap}
        searchQuery={previewSearch}
      />
    </details>
  ) : null
  const sortedReviewSections = [...reviewSections].sort((a, b) => {
    const left = severityRank[String(a.severity || '').toLowerCase()] ?? 3
    const right = severityRank[String(b.severity || '').toLowerCase()] ?? 3
    if (left !== right) return left - right
    return a.title.localeCompare(b.title)
  })
  const blockerDetails: IssueDetail[] = sortedReviewSections
    .filter(section => section.severity === 'blocker')
    .flatMap(section => section.items.length > 0
      ? section.items.map((item, index) => ({
          id: `${section.key}-blocker-${index}`,
          severity: 'blocker' as const,
          title: section.title,
          description: redactPreviewText(item),
          source: redactPreviewText(section.summary || 'Import review'),
        }))
      : [{
          id: `${section.key}-blocker`,
          severity: 'blocker' as const,
          title: section.title,
          description: redactPreviewText(section.summary),
          source: 'Import review',
        }])
  const warningDetails: IssueDetail[] = [
    ...sortedReviewSections
      .filter(section => section.severity === 'warning')
      .flatMap(section => section.items.length > 0
        ? section.items.map((item, index) => ({
            id: `${section.key}-warning-${index}`,
            severity: 'warning' as const,
            title: section.title,
            description: redactPreviewText(item),
            source: redactPreviewText(section.summary || 'Import review'),
          }))
        : [{
            id: `${section.key}-warning`,
            severity: 'warning' as const,
            title: section.title,
            description: redactPreviewText(section.summary),
            source: 'Import review',
          }]),
    ...warnings.map((warning, index) => ({
      id: `import-warning-${index}`,
      severity: 'warning' as const,
      title: warningsTitle.replace(/\s*\(\d+\)\s*$/, ''),
      description: redactPreviewText(warning),
      source: 'Import conversion',
    })),
  ]
  const infoDetails: IssueDetail[] = sortedReviewSections
    .filter(section => section.severity !== 'blocker' && section.severity !== 'warning')
    .flatMap(section => section.items.length > 0
      ? section.items.map((item, index) => ({
          id: `${section.key}-info-${index}`,
          severity: 'info' as const,
          title: section.title,
          description: redactPreviewText(item),
          source: redactPreviewText(section.summary || 'Import review'),
        }))
      : [{
          id: `${section.key}-info`,
          severity: 'info' as const,
          title: section.title,
          description: redactPreviewText(section.summary),
          source: 'Import review',
        }])
  const diagnosticStrings = diagnostics?.length ? diagnostics.map(item => `${item.element}${item.location ? ` at ${item.location}` : ''} [${item.severity}]: ${item.detail}`) : unsupportedElements
  const conversionNotes: IssueDetail[] = (reviewSummary && diagnostics != null ? [] : diagnosticStrings).filter(item => /\[info\]/i.test(item)).map((item, index) => ({ id: `conversion-note-${index}`, severity: 'info', title: 'Conversion note', description: redactPreviewText(item), source: 'Import conversion' }))
  const unsupportedDetails: IssueDetail[] = diagnosticStrings.filter(item => !/\[(info|blocker)\]/i.test(item)).map((item, index) => ({
    id: `unsupported-${index}`,
    severity: 'warning' as const,
    title: 'Unsupported element',
    description: redactPreviewText(item),
    source: 'Import conversion',
  }))
  for (const [index, item] of diagnosticStrings.filter(item => /\[blocker\]/i.test(item)).entries()) {
    if (!blockerDetails.some(existing => existing.description === redactPreviewText(item))) blockerDetails.push({id: `conversion-blocker-${index}`, severity: 'blocker', title: 'Blocking conversion failure', description: redactPreviewText(item), source: 'Import conversion'})
  }
  const normalizedDiagnostics = reviewSummary && diagnostics != null
  const actionableReviewIssues: IssueDetail[] = normalizedDiagnostics
    ? diagnostics.map((item, index) => ({
        id: `jmeter-${item.source_ref || item.location || index}-${item.code || index}`,
        identity: `jmeter-${item.source_ref || item.location || index}-${item.code || index}`,
        onSelect: () => setActiveReviewTab('source'),
        severity: item.severity,
        title: importDiagnosticTitle(item),
        description: redactPreviewText(item.detail),
        source: redactPreviewText([item.source_ref, item.location].filter(Boolean).join(' · ') || 'Import conversion'),
      }))
    : [...blockerDetails, ...warningDetails, ...infoDetails, ...unsupportedDetails]
  const groupedReviewIssues = normalizedDiagnostics ? [] : groupImportWorkspaceIssues(actionableReviewIssues)
  const groupedReviewIssueCount = normalizedDiagnostics
    ? reviewSummary.blocking_behaviors + reviewSummary.review_behaviors
    : groupedReviewIssues.length
  const warningIssueCount = groupedReviewIssues.filter(issue => issue.severity === 'warning').length
  const noteIssueCount = groupedReviewIssues.filter(issue => issue.severity === 'info').length
  const blockingIssueCount = groupedReviewIssues.filter(issue => issue.severity === 'blocker' || issue.severity === 'error').length
  const preservedCredentialCount = secretBindingRequirements.filter(requirement => requirement.destination_value_preserved === true).length
  const credentialSetupCount = secretBindingRequirements.filter(requirementNeedsSecretBinding).length
  // Neither kept as a usable value nor replaceable by a project secret.
  const keptAsRecordedCredentialCount = secretBindingRequirements.length - preservedCredentialCount - credentialSetupCount
  const editableSharedCandidates = onSharedValuesApplied ? (sharedValues?.candidates || []).filter(candidate => !candidate.credential_shaped) : []
  const requiredDataCount = editableSharedCandidates.filter(candidate => candidate.kind === 'source_variable').length
  const optionalDataCount = editableSharedCandidates.length - requiredDataCount + (onParameterizationApplied ? parameterization?.suggestions?.length || 0 : 0)
  const hasParameterizationReview = !!parameterization && (!!parameterization.aura_payload || (!!onParameterizationApplied && (parameterization.suggestions?.length ?? 0) > 0))
  const protectedSessionLabel = protectedImportExpiryLabel(protectedImportSessionExpiresAt)
  const importDraftExpiry = importDraftExpiryStatus(importDraftExpiresAt)
  const importDraftExpired = importDraftExpiry?.expired === true
  const compactHeader = compactReview || tabbed
  const showSubtitleInline = typeof subtitle === 'string' && /dry-run validation|nothing has been saved/i.test(subtitle)
  const hasReviewBlockers = normalizedDiagnostics
    ? reviewSummary.blocking_behaviors > 0
    : actionableReviewIssues.some(issue => issue.severity === 'blocker' || issue.severity === 'error')

  const content = (
    <div className={`script-import-review ${compactHeader ? 'script-import-review-compact' : ''}`}>
      <div className="script-import-review-header" tabIndex={-1} ref={reviewSummaryRef}>
        <p role="status">{progress ? <><span className="spinner" aria-hidden="true" /> {progress.label}</> : readinessMessage || (hasReviewBlockers ? 'Import review has blockers. Resolve them before replay.' : 'Import review ready. Review blockers and warnings before saving.')} {' '}
          {!compactHeader && <>{(script.init?.length || 0) + (script.actions?.length || 0) + (script.teardown?.length || 0)} total actions
          {' '}({script.init?.length || 0} initialization, {script.actions?.length || 0} main, {script.teardown?.length || 0} teardown).</>}
        </p>
        <div>
          <h3 className={compactHeader ? 'script-import-review-context' : undefined}>{title}</h3>{subtitle && (compactHeader && !showSubtitleInline ? <details><summary>Save & editor behavior</summary><p>{subtitle}</p></details> : <p>{subtitle}</p>)}
        </div>
        <div className="script-import-review-actions">
          {onEdit && (
            <button
              // In the importer, opening the draft is the step's one primary.
              className={compactReview ? 'btn btn-primary' : 'btn btn-secondary'}
              onClick={() => { void runAction('edit') }}
              disabled={Boolean(editDisabledReason) || credentialEditing || settingsBusy || importDraftExpired || parameterizationApplying || sharedValuesApplying || transportApplying || reviewBusy || !credentialsValid || credentialHandlerUnavailable || credentialAction !== null}
              title={editDisabledReason ? editDisabledReason : importDraftExpired
                ? 'Re-run the import to obtain a fresh credential-preserving draft before editing.'
                : !credentialsValid
                  ? 'Enter valid secret reference names before editing this draft.'
                  : undefined}
            >
              {credentialAction === 'edit' ? 'Preparing editor…' : progress?.action === 'edit' ? 'Saving and opening…' : compactReview ? editLabel : resolvedEditLabel}
            </button>
          )}
          {savedAction ? (
            <button type="button" className="btn btn-primary" onClick={savedAction.onClick}>{savedAction.label}</button>
          ) : onSave && (
            <button
              className={compactReview && onEdit ? 'btn btn-secondary' : 'btn btn-primary'}
              onClick={() => { void runAction('save') }}
              disabled={credentialEditing || settingsBusy || importDraftExpired || saving || saveDisabled || parameterizationApplying || sharedValuesApplying || transportApplying || reviewBusy || !credentialsValid || credentialHandlerUnavailable || credentialAction !== null}
              aria-describedby={visibleSaveDisabledReason ? saveDisabledReasonId : undefined}
              title={importDraftExpired
                ? 'Re-run the import to obtain a fresh credential-preserving draft before saving.'
                : visibleSaveDisabledReason || undefined}
            >
              {saving && progress?.action !== 'edit' ? 'Saving…' : credentialAction === 'save' ? 'Preparing credentials…' : (compactReview || tabbed) ? saveLabel : resolvedSaveLabel}
            </button>
          )}
        </div>
      </div>

      {importDestination && onSave && !savedAction && (
        <div className="script-import-review-destination">{importDestination}</div>
      )}

      {visibleSaveDisabledReason && (
        <div id={saveDisabledReasonId} className="script-import-review-save-disabled-reason" role="status">
          <strong>Why saving is unavailable:</strong> {visibleSaveDisabledReason}
        </div>
      )}

      {protectedSessionLabel && (
        <div className="script-import-review-session" role="status">
          {protectedSessionLabel} Review this draft before the session expires. If it expires, inspect the source again before saving.
        </div>
      )}

      {importDraftExpiry && (
        <div className="script-import-review-session" role={importDraftExpiry.expired ? 'alert' : 'status'}>
          {compactReview && !importDraftExpiry.expired ? `Draft repair session expires ${formatDateTime(importDraftExpiresAt!, { seconds: true })}.` : importDraftExpiry.label}
        </div>
      )}

      {projectSecretsConfiguredForReview && (
        <div className="script-import-review-session script-import-review-credential-recovery" role="status">
          Project-secret values configured during this review remain in Project Secrets independently of script saving.
          {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
        </div>
      )}

      {tabbed && <div className="import-review-readiness" role="status">
        {normalizedDiagnostics
          ? <span aria-label="JMeter review summary">{reviewSummary.blocking_behaviors} blocking behaviors · {reviewSummary.review_behaviors} behaviors requiring review · {reviewSummary.unsupported_behaviors} unsupported behaviors · {reviewSummary.warnings} warnings · {reviewSummary.informational} informational findings</span>
          : <span><strong>{groupedReviewIssueCount}</strong> finding{groupedReviewIssueCount === 1 ? '' : 's'} · {blockingIssueCount} blocking · {warningIssueCount} warning{warningIssueCount === 1 ? '' : 's'} · {noteIssueCount} note{noteIssueCount === 1 ? '' : 's'}</span>}
        {credentialSetupCount > 0 && <button type="button" onClick={() => setActiveReviewTab('credentials')}><strong>{credentialSetupCount}</strong> credential binding{credentialSetupCount === 1 ? '' : 's'} to configure</button>}
        {preservedCredentialCount > 0 && <button type="button" onClick={() => setActiveReviewTab('credentials')}><strong>{preservedCredentialCount}</strong> preserved credential{preservedCredentialCount === 1 ? '' : 's'} to review</button>}
        {keptAsRecordedCredentialCount > 0 && <button type="button" onClick={() => setActiveReviewTab('credentials')}><strong>{keptAsRecordedCredentialCount}</strong> credential{keptAsRecordedCredentialCount === 1 ? '' : 's'} kept as recorded, to edit before running</button>}
        {requiredDataCount > 0 && <button type="button" onClick={() => setActiveReviewTab('data')}><strong>{requiredDataCount}</strong> required data input{requiredDataCount === 1 ? '' : 's'}</button>}
        {optionalDataCount > 0 && <button type="button" onClick={() => setActiveReviewTab('data')}><strong>{optionalDataCount}</strong> optional data suggestion{optionalDataCount === 1 ? '' : 's'}</button>}
      </div>}
      {reviewNavigation}
      {!tabbed && onSettingsApplied && <div hidden={!showDataReview}><ImportParameterInventory script={script} /></div>}
      {showOriginalDiagnostics && actionableReviewIssues.length > 0 && <div hidden={tabbed && activeReviewTab !== 'review'}><ImportIssueWorkspace normalizedEvidence={Boolean(normalizedDiagnostics)} issues={actionableReviewIssues} onNavigate={(tab, issue) => { if (tab === 'source' && issue) focusIssueSource(issue); setActiveReviewTab(tab) }} onSourceSelect={focusIssueSource} /></div>}
      <div hidden={tabbed && activeReviewTab !== 'review'}>{children}</div>
      {tabbed && <div hidden={activeReviewTab !== 'runtime'}>{onTransportApplied && <ImportTransportPanel script={script} diagnostics={transportDiagnostics ?? diagnostics} disabled={credentialEditing || settingsBusy || importDraftExpired || saving || parameterizationApplying || sharedValuesApplying || reviewBusy || credentialAction !== null} onApplyingChange={handleTransportApplyingChange} onApplied={onTransportApplied} />}{runtimeContent}{onSettingsApplied && <ImportRuntimeSettingsTable script={script} />}{!runtimeContent && !onSettingsApplied && !onTransportApplied && <p className="import-review-empty">No editable runtime settings were supplied by this importer. Review effective settings in the saved script before debugging.</p>}</div>}
      {!tabbed && runtimeContent}
      {!tabbed && onTransportApplied && <ImportTransportPanel script={script} diagnostics={transportDiagnostics ?? diagnostics} disabled={credentialEditing || settingsBusy || importDraftExpired || saving || parameterizationApplying || sharedValuesApplying || reviewBusy || credentialAction !== null} onApplyingChange={handleTransportApplyingChange} onApplied={onTransportApplied} />}

      <ImportCredentialPlacement target={credentialTarget} visible={showCredentials}>
      {tabbed && !hasUnresolvedSecretBindings && <p className="import-review-empty">No unresolved credential bindings were reported for this draft. Runtime credentials are verified when debugging starts.</p>}
      {credentialIntroduction}
      {hasUnresolvedSecretBindings && onSecretBindingsApply && (
        <SecretBindingReview
          inlineEditing
          onEditingChange={handleCredentialEditingChange}
          ref={bindingReviewRef}
          requirements={secretBindingRequirements}
          sourceMigrationRequirements={sourceMigrationRequirements}
          preserveSourceSessionAfterApply={preserveSourceSessionAfterApply}
          reviewGeneration={reviewGeneration}
          projectId={projectId}
          sourceSecretSessionId={sourceSecretSessionId}
          sourceSecretSessionExpiresAt={sourceSecretSessionExpiresAt}
          sourceMigrationAllowed={sourceSecretMigrationAllowed}
          onSourceSessionSettled={onSourceSecretSessionSettled}
          onApply={applySecretBindings}
          onErrorChange={onSecretBindingsErrorChange}
          onPreparedChange={setPreparedSecrets}
          onProjectSecretsConfigured={handleProjectSecretsConfigured}
          disabled={settingsBusy || importDraftExpired || saving || parameterizationApplying || sharedValuesApplying || transportApplying || reviewBusy}
        />
      )}

      {hasUnresolvedSecretBindings && !onSecretBindingsApply && (
        <div className="secret-binding-review-unavailable" role="alert">
          This import contains redacted credentials, but this import source cannot prepare safe secret references.
        </div>
      )}

      </ImportCredentialPlacement>

      {showOriginalDiagnostics && conversionNotes.length > 0 && <details className="script-import-review-original-diagnostics" hidden={tabbed && activeReviewTab !== 'review'}><summary>Conversion reference ({conversionNotes.length})</summary><p>Technical converter notes are retained here for traceability. Actionable findings are grouped in Resolve import issues above.</p><ul>{conversionNotes.map(note => <li key={note.id}><strong>{note.title}:</strong> {note.description}</li>)}</ul></details>}

      <div hidden={!showDataReview}>
      {tabbed && onSettingsApplied && <ImportParameterInventory script={script} />}
      <ImportDataExtras>
      {dataContent}
      {tabbed && !onSettingsApplied && !dataContent && !sharedValues?.candidates?.length && !hasParameterizationReview && <p className="import-review-empty">No editable data substitutions were reported. Review unresolved variables in Review and configure their producers in the Visual Editor.</p>}
      {((onSharedValuesApplied && !!sharedValues?.candidates?.length) || hasParameterizationReview) && <details className="import-review-suggestions" open={tabbed || !compactReview}><summary>Suggested changes</summary>
      {sharedValues && onSharedValuesApplied && (sharedValues.candidates?.length ?? 0) > 0 && (
        <SharedImportValues
          guided={compactReview}
          groupRequirements={tabbed}
          sharedValues={sharedValues}
          scripts={sharedValueScripts && sharedValueScripts.length > 0 ? sharedValueScripts : [script]}
          reviewGeneration={reviewGeneration}
          disabled={credentialEditing || settingsBusy || importDraftExpired || saving || reviewBusy || credentialAction !== null}
          onApplyingChange={handleSharedValuesApplyingChange}
          previousInputs={existingScripts?.choices.previousInputs}
          onApplied={onSharedValuesApplied}
        />
      )}

      {parameterization && hasParameterizationReview && (
        <ParameterizationReview
          guided={compactReview || !!parameterization.aura_payload}
          parameterization={parameterization}
          script={script}
          scriptName={script.name}
          reviewGeneration={reviewGeneration}
          disabled={!onParameterizationApplied || credentialEditing || settingsBusy || importDraftExpired || saving || reviewBusy || credentialAction !== null}
          onApplyingChange={handleParameterizationApplyingChange}
          onApplied={(parameterizedScript, diffs, selectedIds, nextImportDraftExpiresAt) => {
            onParameterizationApplied?.(parameterizedScript, diffs, selectedIds, nextImportDraftExpiresAt)
          }}
        />
      )}

      </details>}
      </ImportDataExtras>
      </div>

      {showPreview && previewPane}
      {tabbed && activeReviewTab === 'source' && !previewAside && <p className="import-review-empty">This importer did not provide source mappings. Conversion does not establish source parity; retain the original source for manual comparison.</p>}
      {sourceVisited.current && previewAside && (
        <div className="script-import-review-preview-aside" hidden={!showPreviewAside}>
          {previewAside}
        </div>
      )}
      {showPreviewAside && onSettingsApplied && <ImportSourceInventory script={script} />}
    </div>
  )
  const reimportControl = existingScripts?.choices.hasMatches
    ? <ImportExistingScripts choices={existingScripts.choices} labels={existingScripts.labels} disabled={saving || reviewBusy || sharedValuesApplying || credentialAction !== null} />
    : null
  const reviewContent = tabbed ? <>{reimportControl}{batchControls}<ImportReviewTabs active={activeReviewTab} onChange={setActiveReviewTab} counts={{ review: groupedReviewIssueCount, credentials: secretBindingRequirements.length }}>{content}</ImportReviewTabs></> : <>{reimportControl}{content}</>
  return <ImportSourceIssueFocusContext.Provider value={sourceIssueFocus}><ImportSettingsProvider script={script} disabled={credentialEditing || settingsDisabled || saving || reviewBusy || importDraftExpired || parameterizationApplying || sharedValuesApplying || transportApplying || credentialAction !== null} onApplied={onSettingsApplied} onBusyChange={handleSettingsBusyChange}>{reviewContent}</ImportSettingsProvider></ImportSourceIssueFocusContext.Provider>
}
