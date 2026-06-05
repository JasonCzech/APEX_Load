import { Fragment, useMemo, type ReactNode } from 'react'
import guideMarkdown from '../../../docs/API_USER_GUIDE.md?raw'

type MarkdownBlock =
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

    // Fenced code block
    const fenceMatch = line.match(/^```(\w*)\s*$/)
    if (fenceMatch) {
      const language = fenceMatch[1] || ''
      const codeLines: string[] = []
      index += 1
      while (index < lines.length && !/^```\s*$/.test(lines[index])) {
        codeLines.push(lines[index])
        index += 1
      }
      index += 1 // skip closing fence
      blocks.push({ kind: 'code', language, code: codeLines.join('\n') })
      continue
    }

    // Heading
    const headingMatch = line.match(/^(#{1,6})\s+(.*)$/)
    if (headingMatch) {
      const level = headingMatch[1].length
      const text = headingMatch[2].trim()
      blocks.push({ kind: 'heading', level, text, id: slugify(text) })
      index += 1
      continue
    }

    // Table
    if (line.trim().startsWith('|') && index + 1 < lines.length && isTableSeparator(lines[index + 1])) {
      const headers = splitTableRow(line)
      const rows: string[][] = []
      index += 2 // header + separator
      while (index < lines.length && lines[index].trim().startsWith('|')) {
        rows.push(splitTableRow(lines[index]))
        index += 1
      }
      blocks.push({ kind: 'table', headers, rows })
      continue
    }

    // Unordered list
    if (/^\s*[-*]\s+/.test(line)) {
      const items: string[] = []
      while (index < lines.length && /^\s*[-*]\s+/.test(lines[index])) {
        items.push(lines[index].replace(/^\s*[-*]\s+/, '').trim())
        index += 1
      }
      blocks.push({ kind: 'list', items })
      continue
    }

    // Paragraph (consume until blank line or next block starter)
    const paragraphLines: string[] = []
    while (index < lines.length && lines[index].trim() && !/^(#{1,6})\s+/.test(lines[index]) && !/^```/.test(lines[index]) && !/^\s*[-*]\s+/.test(lines[index]) && !lines[index].trim().startsWith('|')) {
      paragraphLines.push(lines[index].trim())
      index += 1
    }
    blocks.push({ kind: 'paragraph', text: paragraphLines.join(' ') })
  }

  return blocks
}

// Inline formatting: `code`, **bold**, [text](url)
function renderInline(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = []
  const pattern = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)]+\))/g
  let lastIndex = 0
  let match: RegExpExecArray | null
  let counter = 0

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index))
    }
    const token = match[0]
    const key = `${keyPrefix}-${counter}`
    counter += 1

    if (token.startsWith('`')) {
      nodes.push(<code key={key}>{token.slice(1, -1)}</code>)
    } else if (token.startsWith('**')) {
      nodes.push(<strong key={key}>{token.slice(2, -2)}</strong>)
    } else {
      const linkMatch = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/)
      if (linkMatch) {
        const href = linkMatch[2]
        const external = /^https?:\/\//.test(href)
        nodes.push(
          <a key={key} href={href} {...(external ? { target: '_blank', rel: 'noreferrer' } : {})}>
            {linkMatch[1]}
          </a>,
        )
      } else {
        nodes.push(token)
      }
    }
    lastIndex = pattern.lastIndex
  }

  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex))
  }

  return nodes
}

function renderBlock(block: MarkdownBlock, key: string): ReactNode {
  switch (block.kind) {
    case 'heading': {
      const Tag = `h${Math.min(block.level + 1, 6)}` as 'h2' | 'h3' | 'h4' | 'h5' | 'h6'
      return <Tag key={key} id={block.id}>{renderInline(block.text, key)}</Tag>
    }
    case 'paragraph':
      return <p key={key}>{renderInline(block.text, key)}</p>
    case 'code':
      return (
        <pre key={key} className="markdown-code-block">
          <code data-language={block.language || undefined}>{block.code}</code>
        </pre>
      )
    case 'list':
      return (
        <ul key={key} className="markdown-list">
          {block.items.map((item, itemIndex) => (
            <li key={`${key}-${itemIndex}`}>{renderInline(item, `${key}-${itemIndex}`)}</li>
          ))}
        </ul>
      )
    case 'table':
      return (
        <div key={key} className="markdown-table-wrap">
          <table>
            <thead>
              <tr>
                {block.headers.map((header, headerIndex) => (
                  <th key={`${key}-h-${headerIndex}`}>{renderInline(header, `${key}-h-${headerIndex}`)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, rowIndex) => (
                <tr key={`${key}-r-${rowIndex}`}>
                  {row.map((cell, cellIndex) => (
                    <td key={`${key}-r-${rowIndex}-c-${cellIndex}`}>{renderInline(cell, `${key}-r-${rowIndex}-c-${cellIndex}`)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
    default:
      return null
  }
}

export default function RepositoryGuidePage() {
  const blocks = useMemo(() => parseMarkdown(guideMarkdown), [])
  const title = blocks.find(block => block.kind === 'heading' && block.level === 1)
  const bodyBlocks = title ? blocks.filter(block => block !== title) : blocks
  const headingTitle = title && title.kind === 'heading' ? title.text : 'API User Guide'

  const tableOfContents = useMemo(
    () =>
      blocks.filter(
        (block): block is Extract<MarkdownBlock, { kind: 'heading' }> => block.kind === 'heading' && block.level === 2,
      ),
    [blocks],
  )

  return (
    <div className="page repository-guide-page">
      <div className="page-header">
        <div>
          <h2>Repository Guide</h2>
          <p className="page-subtitle">{headingTitle} — rendered from the in-repository documentation.</p>
        </div>
        <a className="btn btn-secondary btn-sm btn-accent-action" href="#/docs">API Guide</a>
      </div>

      {tableOfContents.length > 0 && (
        <nav className="repository-guide-toc" aria-label="Repository guide contents">
          <span className="api-guide-kicker">On this page</span>
          <ul>
            {tableOfContents.map(heading => (
              <li key={heading.id}>
                <button
                  type="button"
                  onClick={() => document.getElementById(heading.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}
                >
                  {heading.text}
                </button>
              </li>
            ))}
          </ul>
        </nav>
      )}

      <article className="repository-guide-content markdown-body">
        {bodyBlocks.map((block, blockIndex) => (
          <Fragment key={`block-${blockIndex}`}>{renderBlock(block, `block-${blockIndex}`)}</Fragment>
        ))}
      </article>
    </div>
  )
}
