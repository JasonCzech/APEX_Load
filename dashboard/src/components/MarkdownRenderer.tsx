import { Fragment, useMemo, type ReactNode } from 'react'
import { parseMarkdown, type MarkdownBlock } from './repositoryGuideMarkdown'

interface MarkdownRendererProps {
  source?: string
  blocks?: MarkdownBlock[]
  className?: string
  blockKeyPrefix?: string
}

function safeMarkdownHref(href: string) {
  const trimmed = href.trim()
  if (trimmed.startsWith('#')) return trimmed
  // Relative URLs are deliberately rejected. Authority-relative URLs inherit
  // the active scheme, while root-relative URLs resolve under the macOS
  // `wails://wails` origin and replace the embedded SPA.
  try {
    const parsed = new URL(trimmed)
    if (
      (parsed.protocol === 'http:' || parsed.protocol === 'https:')
      && parsed.hostname
      && !parsed.username
      && !parsed.password
    ) {
      return parsed.toString()
    }
    if (parsed.protocol === 'mailto:' && parsed.pathname) return parsed.toString()
  } catch {
    // Malformed and relative destinations render as inert Markdown text.
  }
  return ''
}

function renderMarkdownInline(text: string, keyPrefix: string): ReactNode[] {
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
      const href = linkMatch ? safeMarkdownHref(linkMatch[2]) : ''
      if (linkMatch && href) {
        const external = /^https?:\/\//i.test(href)
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

function renderMarkdownBlock(block: MarkdownBlock, key: string): ReactNode {
  switch (block.kind) {
    case 'heading': {
      const Tag = `h${Math.min(block.level + 1, 6)}` as 'h2' | 'h3' | 'h4' | 'h5' | 'h6'
      return <Tag key={key} id={block.id}>{renderMarkdownInline(block.text, key)}</Tag>
    }
    case 'paragraph':
      return <p key={key}>{renderMarkdownInline(block.text, key)}</p>
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
            <li key={`${key}-${itemIndex}`}>{renderMarkdownInline(item, `${key}-${itemIndex}`)}</li>
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
                  <th key={`${key}-h-${headerIndex}`}>{renderMarkdownInline(header, `${key}-h-${headerIndex}`)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, rowIndex) => (
                <tr key={`${key}-r-${rowIndex}`}>
                  {row.map((cell, cellIndex) => (
                    <td key={`${key}-r-${rowIndex}-c-${cellIndex}`}>{renderMarkdownInline(cell, `${key}-r-${rowIndex}-c-${cellIndex}`)}</td>
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

export default function MarkdownRenderer({
  source = '',
  blocks,
  className,
  blockKeyPrefix = 'markdown',
}: MarkdownRendererProps) {
  const parsedBlocks = useMemo(() => blocks ?? parseMarkdown(source), [blocks, source])
  const content = parsedBlocks.map((block, blockIndex) => (
    <Fragment key={`${blockKeyPrefix}-${blockIndex}`}>{renderMarkdownBlock(block, `${blockKeyPrefix}-${blockIndex}`)}</Fragment>
  ))

  if (className) {
    return <div className={className}>{content}</div>
  }
  return <>{content}</>
}
