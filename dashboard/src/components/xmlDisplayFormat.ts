type XMLToken = {
  kind: 'markup' | 'text'
  value: string
}

type ElementMarkup = {
  kind: 'opening' | 'closing' | 'self-closing'
  name: string
}

type Markup = ElementMarkup | {
  kind: 'comment' | 'cdata' | 'processing-instruction' | 'doctype'
}

type ElementFrame = {
  name: string
  hasCharacterContent: boolean
  hasElementChild: boolean
}

const MAX_FORMATTED_XML_INPUT_CHARS = 500_000
const MAX_FORMATTED_XML_OUTPUT_CHARS = 1_000_000
const MAX_FORMATTED_XML_DEPTH = 256
const xmlNameStartPattern = /[\p{L}_:]/u
const xmlNameCharacterPattern = /[\p{L}\p{N}_.:\-\u00B7]/u

function findMarkupEnd(value: string, start: number): number {
  if (value.startsWith('<!--', start)) {
    const end = value.indexOf('-->', start + 4)
    return end === -1 ? -1 : end + 3
  }
  if (value.startsWith('<![CDATA[', start)) {
    const end = value.indexOf(']]>', start + 9)
    return end === -1 ? -1 : end + 3
  }
  if (value.startsWith('<?', start)) {
    const end = value.indexOf('?>', start + 2)
    return end === -1 ? -1 : end + 2
  }

  let quote = ''
  let subsetDepth = 0
  for (let index = start + 1; index < value.length; index += 1) {
    const character = value[index]
    if (quote) {
      if (character === quote) quote = ''
      continue
    }
    if (character === '"' || character === "'") {
      quote = character
      continue
    }
    if (character === '[') subsetDepth += 1
    else if (character === ']') subsetDepth = Math.max(0, subsetDepth - 1)
    else if (character === '>' && subsetDepth === 0) return index + 1
  }
  return -1
}

function tokenizeXML(value: string): XMLToken[] | null {
  const tokens: XMLToken[] = []
  let cursor = 0
  while (cursor < value.length) {
    const markupStart = value.indexOf('<', cursor)
    if (markupStart === -1) {
      tokens.push({ kind: 'text', value: value.slice(cursor) })
      break
    }
    if (markupStart > cursor) tokens.push({ kind: 'text', value: value.slice(cursor, markupStart) })
    const markupEnd = findMarkupEnd(value, markupStart)
    if (markupEnd === -1) return null
    tokens.push({ kind: 'markup', value: value.slice(markupStart, markupEnd) })
    cursor = markupEnd
  }
  return tokens
}

function readXMLName(value: string, start: number): { name: string; next: number } | null {
  if (!xmlNameStartPattern.test(value[start] || '')) return null
  let next = start + 1
  while (next < value.length && xmlNameCharacterPattern.test(value[next])) next += 1
  return { name: value.slice(start, next), next }
}

function parseElementMarkup(value: string): ElementMarkup | null {
  if (value.startsWith('</')) {
    const name = readXMLName(value, 2)
    if (!name || !/^\s*>$/.test(value.slice(name.next))) return null
    return { kind: 'closing', name: name.name }
  }
  if (!value.startsWith('<') || value.startsWith('<!') || value.startsWith('<?')) return null

  const name = readXMLName(value, 1)
  if (!name) return null
  let cursor = name.next
  const attributeNames = new Set<string>()
  while (cursor < value.length) {
    const remainder = value.slice(cursor)
    if (/^\s*\/>$/.test(remainder)) return { kind: 'self-closing', name: name.name }
    if (/^\s*>$/.test(remainder)) return { kind: 'opening', name: name.name }
    if (!/\s/.test(value[cursor])) return null
    while (cursor < value.length && /\s/.test(value[cursor])) cursor += 1

    const attribute = readXMLName(value, cursor)
    if (!attribute || attributeNames.has(attribute.name)) return null
    attributeNames.add(attribute.name)
    cursor = attribute.next
    while (cursor < value.length && /\s/.test(value[cursor])) cursor += 1
    if (value[cursor] !== '=') return null
    cursor += 1
    while (cursor < value.length && /\s/.test(value[cursor])) cursor += 1
    const quote = value[cursor]
    if (quote !== '"' && quote !== "'") return null
    const endQuote = value.indexOf(quote, cursor + 1)
    if (endQuote === -1 || value.slice(cursor + 1, endQuote).includes('<')) return null
    cursor = endQuote + 1
  }
  return null
}

function classifyMarkup(value: string): Markup | null {
  if (value.startsWith('<!--')) {
    return value.endsWith('-->') && !value.slice(4, -3).includes('--') ? { kind: 'comment' } : null
  }
  if (value.startsWith('<![CDATA[')) return value.endsWith(']]>') ? { kind: 'cdata' } : null
  if (value.startsWith('<?')) {
    const target = readXMLName(value, 2)
    return target && target.next <= value.length - 2 && value.endsWith('?>')
      ? { kind: 'processing-instruction' }
      : null
  }
  if (/^<!DOCTYPE(?:\s|>)/.test(value) && value.endsWith('>')) return { kind: 'doctype' }
  return parseElementMarkup(value)
}

function validateXMLStructure(tokens: XMLToken[]): boolean {
  const stack: ElementFrame[] = []
  let rootCount = 0
  let doctypeSeen = false

  for (const token of tokens) {
    if (token.kind === 'text') {
      if (!token.value.trim()) continue
      if (stack.length === 0) return false
      stack[stack.length - 1].hasCharacterContent = true
      continue
    }

    const markup = classifyMarkup(token.value.trim())
    if (!markup) return false
    if (markup.kind === 'comment' || markup.kind === 'processing-instruction') continue
    if (markup.kind === 'doctype') {
      if (doctypeSeen || rootCount > 0 || stack.length > 0) return false
      doctypeSeen = true
      continue
    }
    if (markup.kind === 'cdata') {
      if (stack.length === 0) return false
      if (token.value.slice(9, -3).length > 0) stack[stack.length - 1].hasCharacterContent = true
      continue
    }
    if (markup.kind === 'closing') {
      const frame = stack.pop()
      if (!frame || frame.name !== markup.name || (frame.hasCharacterContent && frame.hasElementChild)) return false
      continue
    }

    if (stack.length === 0) {
      rootCount += 1
      if (rootCount > 1) return false
    } else {
      stack[stack.length - 1].hasElementChild = true
    }
    if (markup.kind === 'opening') {
      if (stack.length >= MAX_FORMATTED_XML_DEPTH) return false
      stack.push({ name: markup.name, hasCharacterContent: false, hasElementChild: false })
    }
  }

  return rootCount === 1 && stack.length === 0
}

/**
 * Builds an indented, source-fidelity XML projection for read-only display.
 * The returned string is never written back to the DSL body; authored bytes
 * remain authoritative for execution.
 */
export function formatXMLForDisplay(value: string): string | null {
  if (!value || value.length > MAX_FORMATTED_XML_INPUT_CHARS) return null
  const trimmed = value.trim()
  if (!trimmed.startsWith('<')) return null
  const tokens = tokenizeXML(trimmed)
  if (!tokens || !validateXMLStructure(tokens)) return null

  const lines: string[] = []
  const openTags: string[] = []
  let outputLength = 0
  const appendLine = (line: string): boolean => {
    outputLength += line.length + (lines.length > 0 ? 1 : 0)
    if (outputLength > MAX_FORMATTED_XML_OUTPUT_CHARS) return false
    lines.push(line)
    return true
  }
  const indent = () => '  '.repeat(openTags.length)

  for (let index = 0; index < tokens.length; index += 1) {
    const token = tokens[index]
    if (token.kind === 'text') {
      if (!token.value.trim()) continue
      // Character content is emitted with its opening and closing tags below.
      // Any other placement is intentionally left to the exact raw view.
      return null
    }

    const markupValue = token.value.trim()
    const markup = classifyMarkup(markupValue)
    if (!markup) return null
    if (markup.kind === 'closing') {
      openTags.pop()
      if (!appendLine(`${indent()}${markupValue}`)) return null
      continue
    }

    if (markup.kind !== 'opening') {
      if (!appendLine(`${indent()}${markupValue}`)) return null
      continue
    }

    const contentToken = tokens[index + 1]
    const closeToken = tokens[index + 2]
    const parsedClose = closeToken?.kind === 'markup' ? classifyMarkup(closeToken.value.trim()) : null
    if (
      contentToken?.kind === 'text'
      && parsedClose?.kind === 'closing'
      && parsedClose.name === markup.name
    ) {
      if (!appendLine(`${indent()}${markupValue}${contentToken.value}${closeToken.value.trim()}`)) return null
      index += 2
      continue
    }

    if (!appendLine(`${indent()}${markupValue}`)) return null
    openTags.push(markup.name)
  }

  return lines.length > 0 ? lines.join('\n') : null
}
