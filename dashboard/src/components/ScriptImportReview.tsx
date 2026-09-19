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
import { isSensitiveCredentialPreviewKey, normalizeCredentialPreviewName } from './credentialPreviewPolicy'
import ParameterizationReview from './ParameterizationReview'
import SharedImportValues from './SharedImportValues'
import { importDraftExpiryStatus, protectedImportExpiryLabel } from './protectedImportSession'
import SecretBindingReview, {
  type PreparedSecretBindings,
  type SecretBindingReviewHandle,
} from './SecretBindingReview'
import { requirementsPreserveDestinationValues } from './secretBindingUtils'
import IssueDetailsDialog, { type IssueDetail } from './IssueDetailsDialog'
import './ScriptImportReview.css'

const severityRank: Record<string, number> = {
  blocker: 0,
  warning: 1,
  info: 2,
}

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
    /(["']?)([A-Za-z][A-Za-z0-9_.%[\]-]*)\1\s*([:=])\s*(\{\{secret\.[A-Za-z0-9_.-]{1,249}\}\}|"[^"]*"|'[^']*'|(?:bearer|basic)\s+[^\s,}&]+|[^\s,}&]+)/gi,
    (match: string, quote: string, name: string, delimiter: string, rawValue: string) => {
      if (!isSensitiveCredentialPreviewKey(name)) return match
      const unquoted = rawValue.replace(/^(?:"([\s\S]*)"|'([\s\S]*)')$/, (_value, doubleQuoted, singleQuoted) => doubleQuoted ?? singleQuoted ?? '')
      if (safeCredentialReference(unquoted)) return match
      return `${quote}${name}${quote}${delimiter}${PROTECTED_PREVIEW_VALUE}`
    },
  )
  safe = safe.replace(
    /(["']?)((?:[a-z0-9]+[_-])*(?:authorization|password|passwd|passphrase|secret|client[_-]?secret|token|access[_-]?token|refresh[_-]?token|id[_-]?token|auth[_-]?token|api[_-]?key|apikey|private[_-]?key|session(?:[_-]?(?:id|token))?|sid|csrf(?:[_-]?token)?|xsrf(?:[_-]?token)?|cookie|dsn))\1\s*[:=]\s*(?!(?:"|')?\{\{secret\.)("[^"]*"|'[^']*'|[^\s,}&]+)/gi,
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
  const visit = (candidate: unknown, depth: number, sensitiveAncestor = false, path = ''): unknown => {
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
      const safeCandidate = redactPreviewText(boundedCandidate)
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
        if (result.length < candidate.length) { state.truncated = true; result.push(`… ${candidate.length - result.length} more item(s) omitted …`) }
        return result
      }
      const keys = Object.keys(candidate)
      const keyCount = Math.min(keys.length, maxKeys)
      if (keyCount < keys.length) state.truncated = true
      const result: Record<string, unknown> = {}
      const sensitiveSemanticName = previewObjectHasSensitiveSemanticName(candidate as Record<string, unknown>, keys)
      for (let index = 0; index < keyCount && state.nodes < maxNodes; index += 1) {
        const key = keys[index]
        if (['import_draft_token', 'source_secret_session_id', 'protected_import_session_id'].includes(key)) { result[key] = PROTECTED_PREVIEW_VALUE; continue }
        try {
          const child = (candidate as Record<string, unknown>)[key]
          const semanticNameKey = sensitiveSemanticName && isPreviewSemanticNameKey(key)
          const childSensitive = (
            sensitiveAncestor
            // Opaque source snippets and positional arguments cannot be safely
            // classified by a browser name-based redactor. Preserve their shape
            // while hiding values in the downloadable review.
            || (complete && ['metadata', 'args', 'call_args'].includes(key))
            || (!semanticNameKey && isSensitiveCredentialPreviewKey(key))
            // A semantic credential record can use arbitrary adapter-specific
            // payload keys such as defaultValue, values, or schema.default. Protect
            // every non-name descendant instead of relying on a small allowlist.
            || (sensitiveSemanticName && !semanticNameKey)
          )
          result[key] = visit(child, depth + 1, childSensitive, `${path}/${key.replace(/~/g, '~0').replace(/\//g, '~1')}`)
        } catch {
          result[key] = '[Unable to preview value]'
        }
      }
      if (Object.keys(result).length < keys.length) { state.truncated = true; result['…'] = `${keys.length - Object.keys(result).length} more field(s) omitted` }
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
  showCredentials?: boolean
  showDataReview?: boolean
  credentialIntroduction?: ReactNode
  script: ScriptDefinition
  previewData?: unknown
  warnings?: string[]
  warningsTitle?: string
  diagnostics?: {element: string; detail: string; severity: 'blocker' | 'warning' | 'info'; location?: string}[]
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
  saveLabel?: string
  editLabel?: string
  previewTitle?: string
  previewAside?: ReactNode
  showPreview?: boolean
  previewInitiallyOpen?: boolean
  showPreviewAside?: boolean
  onSave?: () => void
  onEdit?: () => void
  children?: ReactNode
}

export default function ScriptImportReview({
  title,
  reviewGeneration = 0,
  subtitle,
  readinessMessage,
  showCredentials = true,
  showDataReview = true,
  credentialIntroduction,
  script,
  previewData,
  warnings = [],
  warningsTitle = 'Warnings',
  unsupportedElements = [],
  diagnostics,
  reviewSections = [],
  sharedValues,
  sharedValueScripts,
  onSharedValuesApplied,
  parameterization,
  onParameterizationApplied,
  secretBindingRequirements = [],
  sourceMigrationRequirements,
  preserveSourceSessionAfterApply = false,
  onSecretBindingsApply,
  projectId,
  sourceSecretSessionId,
  sourceSecretSessionExpiresAt,
  sourceSecretMigrationAllowed = false,
  protectedImportSessionExpiresAt,
  importDraftExpiresAt,
  onSourceSecretSessionSettled,
  credentialReviewRef,
  onCredentialsPreparedChange,
  saving = false,
  saveDisabled = false,
  saveDisabledReason,
  reviewBusy = false,
  onReviewBusyChange,
  onCredentialActionStart,
  saveLabel = 'Save To Script Library',
  editLabel = 'Open In Visual Editor',
  previewTitle = 'Generated Script Preview',
  previewAside,
  showPreview = true,
  previewInitiallyOpen = false,
  showPreviewAside = true,
  onSave,
  onEdit,
  children,
}: Props) {
  const reviewSummaryRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    reviewSummaryRef.current?.focus()
    reviewSummaryRef.current?.scrollIntoView?.({ block: 'start', behavior: 'smooth' })
  }, [reviewGeneration])
  const hasUnresolvedSecretBindings = secretBindingRequirements.length > 0
  const allRemainingRequirementsPreserveValues = requirementsPreserveDestinationValues(secretBindingRequirements)
  const internalBindingReviewRef = useRef<SecretBindingReviewHandle | null>(null)
  const bindingReviewRef = credentialReviewRef || internalBindingReviewRef
  const [preparedSecrets, setPreparedSecrets] = useState<PreparedSecretBindings | null>(null)
  const [credentialAction, setCredentialAction] = useState<CredentialAction | null>(null)
  const [parameterizationApplying, setParameterizationApplying] = useState(false)
  const [sharedValuesApplying, setSharedValuesApplying] = useState(false)
  const [importDraftExpiryGeneration, setImportDraftExpiryGeneration] = useState(0)
  const [previewExportError, setPreviewExportError] = useState('')
  const [issueDialog, setIssueDialog] = useState<{ title: string; issues: IssueDetail[] } | null>(null)
  const [configuredCredentialReviewIdentity, setConfiguredCredentialReviewIdentity] = useState('')
  const saveDisabledReasonId = useId()
  const parameterizationApplyingRef = useRef(false)
  const sharedValuesApplyingRef = useRef(false)
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
  const visibleSaveDisabledReason = saveDisabled
    ? saveDisabledReason?.trim() || 'Saving is unavailable for this reviewed import. Correct the required choices shown on this page and try again.'
    : ''
  const defaultConfigureLaterCount = useMemo(
    () => new Set(secretBindingRequirements.filter(requirement => (
      requirement.destination_value_preserved !== true
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
    ?? secretBindingRequirements.filter(requirement => requirement.destination_value_preserved !== true).length
  const deferredSaveLabel = /^Save to Script Library$/i.test(saveLabel) ? 'Save' : saveLabel
  const resolvedSaveLabel = hasUnresolvedSecretBindings && configureLaterCount > 0
    ? `${deferredSaveLabel} with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure`
    : saveLabel
  const resolvedEditLabel = hasUnresolvedSecretBindings && configureLaterCount > 0
    ? `${editLabel} with ${configureLaterCount} secret${configureLaterCount === 1 ? '' : 's'} to configure`
    : editLabel

  const reportInternalReviewBusy = useCallback((options?: { parameterization?: boolean; credentials?: boolean; sharedValues?: boolean }) => {
    const parameterizationBusy = options?.parameterization ?? parameterizationApplyingRef.current
    const credentialsBusy = options?.credentials ?? credentialActionRequestRef.current !== null
    const sharedValuesBusy = options?.sharedValues ?? sharedValuesApplyingRef.current
    onReviewBusyChange?.(parameterizationBusy || credentialsBusy || sharedValuesBusy)
  }, [onReviewBusyChange])

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
    setIssueDialog(null)
  }, [reviewIdentity])

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
    if (importDraftExpired || parameterizationApplyingRef.current || sharedValuesApplyingRef.current || reviewBusy) return
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
  const preview = useMemo(
    () => showPreview ? previewJSON(previewData ?? { script }) : null,
    [previewData, script, showPreview],
  )
  const previewPane = preview ? (
    <details className="script-import-review-preview" open={previewInitiallyOpen}>
      <summary>
        <h4>{previewTitle}</h4>
      </summary>
      {preview.truncated && (
        <div className="script-import-review-preview-note" role="status">
          Preview truncated for browser performance. Download the complete protected JSON to inspect every generated action before saving.
        </div>
      )}
      <button type="button" className="btn btn-secondary btn-sm" onClick={() => {
        setPreviewExportError('')
        try {
          const full = previewJSON({ script }, true, new Set(secretBindingRequirements.flatMap(requirement => [requirement.path, ...(requirement.affected_paths || [])].map(path => `/script${path}`))))
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
      <p>Credential values, source metadata, and opaque positional arguments are protected. Every generated action is included; this review copy is not an executable export.</p>
      {previewExportError && <p role="alert">{previewExportError}</p>}
      <pre>{preview.text}</pre>
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
  const conversionNotes: IssueDetail[] = diagnosticStrings.filter(item => /\[info\]/i.test(item)).map((item, index) => ({ id: `conversion-note-${index}`, severity: 'info', title: 'Conversion note', description: redactPreviewText(item), source: 'Import conversion' }))
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
  const protectedSessionLabel = protectedImportExpiryLabel(protectedImportSessionExpiresAt)
  const importDraftExpiry = importDraftExpiryStatus(importDraftExpiresAt)
  const importDraftExpired = importDraftExpiry?.expired === true

  return (
    <div className="script-import-review">
      <div className="script-import-review-header" tabIndex={-1} ref={reviewSummaryRef}>
        <p role="status">{readinessMessage || 'Import review ready. Review blockers and warnings before saving.'} {' '}
          {(script.init?.length || 0) + (script.actions?.length || 0) + (script.teardown?.length || 0)} total actions
          {' '}({script.init?.length || 0} initialization, {script.actions?.length || 0} main, {script.teardown?.length || 0} teardown).
        </p>
        <div>
          <h3>{title}</h3>
          {subtitle && <p>{subtitle}</p>}
        </div>
        <div className="script-import-review-actions">
          {onEdit && (
            <button
              className="btn btn-secondary"
              onClick={() => { void runAction('edit') }}
              disabled={importDraftExpired || parameterizationApplying || sharedValuesApplying || reviewBusy || !credentialsValid || credentialHandlerUnavailable || credentialAction !== null}
              title={importDraftExpired
                ? 'Re-run the import to obtain a fresh credential-preserving draft before editing.'
                : !credentialsValid
                  ? 'Enter valid secret reference names before editing this draft.'
                  : undefined}
            >
              {credentialAction === 'edit' ? 'Preparing editor...' : resolvedEditLabel}
            </button>
          )}
          {onSave && (
            <button
              className="btn btn-primary"
              onClick={() => { void runAction('save') }}
              disabled={importDraftExpired || saving || saveDisabled || parameterizationApplying || sharedValuesApplying || reviewBusy || !credentialsValid || credentialHandlerUnavailable || credentialAction !== null}
              aria-describedby={visibleSaveDisabledReason ? saveDisabledReasonId : undefined}
              title={importDraftExpired
                ? 'Re-run the import to obtain a fresh credential-preserving draft before saving.'
                : visibleSaveDisabledReason || undefined}
            >
              {saving ? 'Saving...' : credentialAction === 'save' ? 'Preparing credentials...' : resolvedSaveLabel}
            </button>
          )}
        </div>
      </div>

      {visibleSaveDisabledReason && (
        <div id={saveDisabledReasonId} className="script-import-review-save-disabled-reason" role="status">
          <strong>Why saving is unavailable:</strong> {visibleSaveDisabledReason}
        </div>
      )}

      {protectedSessionLabel && (
        <div className="script-import-review-session" role="status">
          {protectedSessionLabel} Credential bindings and parameterization selections remain protected; other draft edits switch to the compatibility commit path.
        </div>
      )}

      {importDraftExpiry && (
        <div className="script-import-review-session" role={importDraftExpiry.expired ? 'alert' : 'status'}>
          {importDraftExpiry.label}
        </div>
      )}

      {projectSecretsConfiguredForReview && (
        <div className="script-import-review-session script-import-review-credential-recovery" role="status">
          Project-secret values configured during this review remain in Project Secrets independently of script saving.
          {' '}If script saving fails, retrying this save reuses those configured values. Any atomic-save promise on this page applies to scripts, not external secret-store writes.
        </div>
      )}

      {children}

      <div hidden={!showCredentials}>
      {credentialIntroduction}
      {hasUnresolvedSecretBindings && onSecretBindingsApply && (
        <SecretBindingReview
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
          onPreparedChange={setPreparedSecrets}
          onProjectSecretsConfigured={handleProjectSecretsConfigured}
          disabled={importDraftExpired || parameterizationApplying || sharedValuesApplying || reviewBusy}
        />
      )}

      {hasUnresolvedSecretBindings && !onSecretBindingsApply && (
        <div className="secret-binding-review-unavailable" role="alert">
          This import contains redacted credentials, but this import source cannot prepare safe secret references.
        </div>
      )}

      </div>

      {conversionNotes.length > 0 && <button type="button" className="script-health-token issue-pill-button" onClick={() => setIssueDialog({ title: 'Conversion notes', issues: conversionNotes })}>{conversionNotes.length} conversion note{conversionNotes.length === 1 ? '' : 's'}</button>}
      {(blockerDetails.length > 0 || warningDetails.length > 0 || infoDetails.length > 0 || unsupportedDetails.length > 0) && (
        <div className="script-import-review-issue-summary" aria-label="Import issue summary">
          {blockerDetails.length > 0 && (
            <button
              type="button"
              className="script-health-token danger issue-pill-button"
              aria-label={`View ${blockerDetails.length} import blocker ${blockerDetails.length === 1 ? 'detail' : 'details'}`}
              onClick={() => setIssueDialog({ title: 'Import blockers', issues: blockerDetails })}
            >
              {blockerDetails.length} blocker{blockerDetails.length === 1 ? '' : 's'}
            </button>
          )}
          {warningDetails.length > 0 && (
            <button
              type="button"
              className="script-health-token warning issue-pill-button"
              aria-label={`View ${warningDetails.length} import warning ${warningDetails.length === 1 ? 'detail' : 'details'}`}
              onClick={() => setIssueDialog({ title: 'Import warnings', issues: warningDetails })}
            >
              {warningDetails.length} warning{warningDetails.length === 1 ? '' : 's'}
            </button>
          )}
          {infoDetails.length > 0 && (
            <button
              type="button"
              className="script-health-token issue-pill-button"
              aria-label={`View ${infoDetails.length} import review ${infoDetails.length === 1 ? 'detail' : 'details'}`}
              onClick={() => setIssueDialog({ title: 'Import review details', issues: infoDetails })}
            >
              {infoDetails.length} review detail{infoDetails.length === 1 ? '' : 's'}
            </button>
          )}
          {unsupportedDetails.length > 0 && (
            <button
              type="button"
              className="script-health-token warning issue-pill-button"
              aria-label={`View ${unsupportedDetails.length} unsupported import ${unsupportedDetails.length === 1 ? 'element' : 'elements'}`}
              onClick={() => setIssueDialog({ title: 'Unsupported elements', issues: unsupportedDetails })}
            >
              {unsupportedDetails.length} unsupported
            </button>
          )}
        </div>
      )}

      <div hidden={!showDataReview}>
      {sharedValues && onSharedValuesApplied && (sharedValues.candidates?.length ?? 0) > 0 && (
        <SharedImportValues
          sharedValues={sharedValues}
          scripts={sharedValueScripts && sharedValueScripts.length > 0 ? sharedValueScripts : [script]}
          reviewGeneration={reviewGeneration}
          disabled={importDraftExpired || saving || reviewBusy || credentialAction !== null}
          onApplyingChange={handleSharedValuesApplyingChange}
          onApplied={onSharedValuesApplied}
        />
      )}

      {parameterization && onParameterizationApplied && (parameterization.suggestions?.length ?? 0) > 0 && (
        <ParameterizationReview
          parameterization={parameterization}
          script={script}
          scriptName={script.name}
          reviewGeneration={reviewGeneration}
          disabled={importDraftExpired || saving || reviewBusy || credentialAction !== null}
          onApplyingChange={handleParameterizationApplyingChange}
          onApplied={(parameterizedScript, diffs, selectedIds, nextImportDraftExpiresAt) => {
            onParameterizationApplied(parameterizedScript, diffs, selectedIds, nextImportDraftExpiresAt)
          }}
        />
      )}

      </div>

      {showPreview && previewPane}
      {showPreviewAside && previewAside && (
        <div className="script-import-review-preview-aside">
          {previewAside}
        </div>
      )}
      {issueDialog && (
        <IssueDetailsDialog
          title={issueDialog.title}
          issues={issueDialog.issues}
          onClose={() => setIssueDialog(null)}
        />
      )}
    </div>
  )
}
