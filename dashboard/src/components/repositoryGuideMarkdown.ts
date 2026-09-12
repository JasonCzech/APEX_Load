export type MarkdownListItem = { text: string; children?: MarkdownListBlock }

export type MarkdownListBlock = { kind: 'list'; ordered: boolean; start?: number; items: MarkdownListItem[] }

export type MarkdownBlock =
  | { kind: 'heading'; level: number; text: string; id: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'code'; language: string; code: string }
  | MarkdownListBlock
  | { kind: 'table'; headers: string[]; rows: string[][] }

const BULLET_MARKER = /^(\s*)[-*]\s+(.*)$/
const ORDERED_MARKER = /^(\s*)(\d+)[.)]\s+(.*)$/

type ListMarker = { indent: number; ordered: boolean; number?: number; text: string }

function slugify(text: string) {
  return text
    .toLowerCase()
    .replace(/`/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function splitTableRow(line: string) {
  return line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split(/(?<!\\)\|/)
    .map(cell => cell.trim().replace(/\\\|/g, '|'))
}

function isTableSeparator(line: string) {
  return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line)
}

function isTableStart(lines: string[], index: number) {
  return lines[index]?.trim().startsWith('|') && index + 1 < lines.length && isTableSeparator(lines[index + 1])
}

function matchCodeFence(line: string) {
  return line.match(/^```([^`]*)\s*$/)
}

// A bullet line and a table separator can look alike (`- - -`), and the table
// branch runs first, so only lines that fail `isTableStart` reach this.
function matchListMarker(line: string): ListMarker | null {
  const bullet = line.match(BULLET_MARKER)
  if (bullet) return { indent: bullet[1].length, ordered: false, text: bullet[2].trim() }
  const ordered = line.match(ORDERED_MARKER)
  if (ordered) {
    return { indent: ordered[1].length, ordered: true, number: Number(ordered[2]), text: ordered[3].trim() }
  }
  return null
}

// Lines that end the current list item's lazy continuation. Anything else that is
// non-blank and not a new marker is a wrapped line belonging to the current item.
function endsListContinuation(lines: string[], index: number) {
  const line = lines[index]
  return (
    !line.trim()
    || /^(#{1,6})\s+/.test(line)
    || matchCodeFence(line) !== null
    || isTableStart(lines, index)
  )
}

// Parses one list at `baseIndent`. Items keep wrapped continuation lines, and lines
// indented further than the list's own marker open a nested child list.
function parseList(lines: string[], startIndex: number, baseIndent: number): { block: MarkdownListBlock; nextIndex: number } {
  const first = matchListMarker(lines[startIndex])!
  const block: MarkdownListBlock = { kind: 'list', ordered: first.ordered, items: [] }
  if (first.ordered && first.number !== undefined && first.number !== 1) block.start = first.number

  let index = startIndex
  while (index < lines.length) {
    const marker = isTableStart(lines, index) ? null : matchListMarker(lines[index])
    if (!marker) break
    if (marker.indent < baseIndent) break

    if (marker.indent > baseIndent) {
      // Deeper marker without a preceding sibling at this level: treat as a nested
      // list under the item we just added, or as this list's own content if there
      // is none yet.
      const parent = block.items[block.items.length - 1]
      if (!parent) break
      const nested = parseList(lines, index, marker.indent)
      parent.children = nested.block
      index = nested.nextIndex
      continue
    }

    if (marker.ordered !== block.ordered) break

    const item: MarkdownListItem = { text: marker.text }
    block.items.push(item)
    index += 1

    while (index < lines.length && !endsListContinuation(lines, index)) {
      const nextMarker = matchListMarker(lines[index])
      if (nextMarker) {
        if (nextMarker.indent <= baseIndent) break
        const nested = parseList(lines, index, nextMarker.indent)
        item.children = nested.block
        index = nested.nextIndex
        continue
      }
      item.text = `${item.text} ${lines[index].trim()}`.trim()
      index += 1
    }
  }

  return { block, nextIndex: index }
}

export function parseMarkdown(source: string): MarkdownBlock[] {
  const lines = source.replace(/\r\n/g, '\n').split('\n')
  const blocks: MarkdownBlock[] = []
  const headingIds = new Set<string>()
  let index = 0

  while (index < lines.length) {
    const line = lines[index]

    if (!line.trim()) {
      index += 1
      continue
    }

    const fenceMatch = matchCodeFence(line)
    if (fenceMatch) {
      const language = fenceMatch[1].trim().split(/\s+/, 1)[0] || ''
      const codeLines: string[] = []
      index += 1
      while (index < lines.length && !/^```\s*$/.test(lines[index])) {
        codeLines.push(lines[index])
        index += 1
      }
      index += 1
      blocks.push({ kind: 'code', language, code: codeLines.join('\n') })
      continue
    }

    const headingMatch = line.match(/^(#{1,6})\s+(.*)$/)
    if (headingMatch) {
      const level = headingMatch[1].length
      const text = headingMatch[2].trim()
      const baseId = slugify(text) || 'section'
      let id = baseId
      let suffix = 1
      while (headingIds.has(id)) id = `${baseId}-${suffix++}`
      headingIds.add(id)
      blocks.push({ kind: 'heading', level, text, id })
      index += 1
      continue
    }

    if (isTableStart(lines, index)) {
      const headers = splitTableRow(line)
      const rows: string[][] = []
      index += 2
      while (index < lines.length && lines[index].trim().startsWith('|')) {
        rows.push(splitTableRow(lines[index]))
        index += 1
      }
      blocks.push({ kind: 'table', headers, rows })
      continue
    }

    const listMarker = matchListMarker(line)
    if (listMarker) {
      const { block, nextIndex } = parseList(lines, index, listMarker.indent)
      blocks.push(block)
      index = nextIndex
      continue
    }

    const paragraphLines: string[] = []
    while (
      index < lines.length
      && lines[index].trim()
      && !/^(#{1,6})\s+/.test(lines[index])
      && !matchCodeFence(lines[index])
      && !matchListMarker(lines[index])
      && !isTableStart(lines, index)
    ) {
      paragraphLines.push(lines[index].trim())
      index += 1
    }
    blocks.push({ kind: 'paragraph', text: paragraphLines.join(' ') })
  }

  return blocks
}
