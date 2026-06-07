export type MarkdownBlock =
  | { kind: 'heading'; level: number; text: string; id: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'code'; language: string; code: string }
  | { kind: 'list'; items: string[] }
  | { kind: 'table'; headers: string[]; rows: string[][] }

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
    .split('|')
    .map(cell => cell.trim())
}

function isTableSeparator(line: string) {
  return /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(line)
}

export function parseMarkdown(source: string): MarkdownBlock[] {
  const lines = source.replace(/\r\n/g, '\n').split('\n')
  const blocks: MarkdownBlock[] = []
  let index = 0

  while (index < lines.length) {
    const line = lines[index]

    if (!line.trim()) {
      index += 1
      continue
    }

    const fenceMatch = line.match(/^```(\w*)\s*$/)
    if (fenceMatch) {
      const language = fenceMatch[1] || ''
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
      blocks.push({ kind: 'heading', level, text, id: slugify(text) })
      index += 1
      continue
    }

    if (line.trim().startsWith('|') && index + 1 < lines.length && isTableSeparator(lines[index + 1])) {
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

    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = []
      while (index < lines.length && /^\s*[-*]\s+/.test(lines[index])) {
        items.push(lines[index].replace(/^\s*[-*]\s+/, '').trim())
        index += 1
      }
      blocks.push({ kind: 'list', items })
      continue
    }

    const paragraphLines: string[] = []
    while (index < lines.length && lines[index].trim() && !/^(#{1,6})\s+/.test(lines[index]) && !/^```/.test(lines[index]) && !/^\s*[-*]\s+/.test(lines[index]) && !lines[index].trim().startsWith('|')) {
      paragraphLines.push(lines[index].trim())
      index += 1
    }
    blocks.push({ kind: 'paragraph', text: paragraphLines.join(' ') })
  }

  return blocks
}
