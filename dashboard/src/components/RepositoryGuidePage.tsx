import { Fragment, useMemo, type ReactNode } from 'react'
import guideMarkdown from '../../../docs/API_USER_GUIDE.md?raw'
import { useApiGuideTopbarContribution } from './ApiGuideTopbarToggle'
import { parseMarkdown, type MarkdownBlock } from './repositoryGuideMarkdown'

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
  useApiGuideTopbarContribution('repo-guide', 'Repository Guide')

  return (
    <div className="page repository-guide-page">
      <div className="page-header">
        <div>
          <h2>Repository Guide</h2>
          <p className="page-subtitle">{headingTitle} — rendered from the in-repository documentation.</p>
        </div>
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
