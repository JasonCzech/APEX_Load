import { Fragment, lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { copyTextToClipboard } from '../utils/clipboardUtils'
import { reportClientError } from '../utils/clientErrorReporting'
import { markdownPlainText, parseMarkdown, type MarkdownBlock, type MarkdownListBlock } from './repositoryGuideMarkdown'
import { declaresErDiagram, loadMermaidErDiagram } from './mermaidErDiagramLoader'

// The ER diagram renderer and its parser load only for a document that has
// an erDiagram block. Until then, and if the chunk cannot load (reported like
// any other lazy surface), the block shows its source: the same output as a
// diagram that does not parse.
const MermaidErDiagram = lazy(() => loadMermaidErDiagram().catch((error: unknown) => {
  reportClientError('render', error)
  return { default: MermaidSource }
}))
function MermaidSource({ fallback }: { fallback: ReactNode }) {
  return <>{fallback}</>
}

type MarkdownHeadingBlock = Extract<MarkdownBlock, { kind: 'heading' }>

interface MarkdownRendererProps {
  source?: string
  blocks?: MarkdownBlock[]
  className?: string
  blockKeyPrefix?: string
  resolveLink?: (href: string) => string
  copyExamples?: boolean
  /** Extra content rendered directly below a heading, such as a section index. */
  renderAfterHeading?: (heading: MarkdownHeadingBlock) => ReactNode
}

type InlineOptions = Pick<MarkdownRendererProps, 'resolveLink'> & { identifierBreaks?: boolean }
type BlockOptions = Pick<MarkdownRendererProps, 'resolveLink' | 'copyExamples'> & { sectionTitle?: string }

// Table cells let runs without spaces wrap at their separators rather than
// mid-word, so a column of them can stay narrow: after each `/` and `|`
// (browsers do not reliably break there), and inside a long name
// (`config.eventhubs.start_position`) after `_ . = -`. Shorter names stay whole.
function withIdentifierBreaks(text: string, keyPrefix: string): ReactNode {
  if (!/[/|]|\S{25,}/.test(text)) return text
  return text.split(/(\s+)/).map((token, tokenIndex) => {
    const pieces = token.split(/(?<=[/|])/).flatMap(piece => piece.length < 25 ? [piece] : piece.split(/(?<=[_.=-])(?=[^_.=-])/))
    // A trailing separator may break before the next inline element: `a`/`b`.
    if (/[/|]$/.test(token)) pieces.push('')
    return pieces.length === 1 ? token : <Fragment key={`${keyPrefix}-w${tokenIndex}`}>
      {pieces.map((piece, pieceIndex) => <Fragment key={pieceIndex}>{pieceIndex > 0 && <wbr />}{piece}</Fragment>)}
    </Fragment>
  })
}

type PlainText = (value: string, key: string) => ReactNode

// Link and heading labels keep their inline code; any other syntax is dropped.
function renderLabel(text: string, keyPrefix: string, plain: PlainText = value => value): ReactNode[] {
  return text.split(/(`[^`]+`)/).map((part, index) => index % 2
    ? <code key={`${keyPrefix}-${index}`}>{plain(part.slice(1, -1), `${keyPrefix}-${index}`)}</code>
    : plain(markdownPlainText(part), `${keyPrefix}-${index}`))
}

/** A heading's inline Markdown for navigation lists. */
export function MarkdownLabel({ text }: { text: string }) {
  return <>{renderLabel(text, 'label')}</>
}

// Auto table layout sizes columns from their content. It cannot tell short
// values that must not wrap (kinds, statuses, yes) from prose that needs a
// readable measure, so the renderer marks those two shapes.
function tableColumnClasses(headers: string[], rows: string[][]): (string | undefined)[] {
  return headers.map((_, column) => {
    const lengths = rows.map(row => markdownPlainText(row[column] ?? '').trim().length).filter(length => length > 0)
    if (lengths.length === 0 || Math.max(...lengths) <= 16) return 'markdown-col-compact'
    if (lengths.reduce((sum, length) => sum + length, 0) / lengths.length >= 40) return 'markdown-col-prose'
    return undefined
  })
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

function renderMarkdownInline(text: string, keyPrefix: string, options: InlineOptions): ReactNode[] {
  const nodes: ReactNode[] = []
  const pattern = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\[[^\]]+\]\([^)]+\))/g
  const plain: PlainText = (value, key) => options.identifierBreaks ? withIdentifierBreaks(value, key) : value
  let lastIndex = 0
  let match: RegExpExecArray | null
  let counter = 0

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(plain(text.slice(lastIndex, match.index), `${keyPrefix}-t${counter}`))
    }
    const token = match[0]
    const key = `${keyPrefix}-${counter}`
    counter += 1

    if (token.startsWith('`')) {
      nodes.push(<code key={key}>{plain(token.slice(1, -1), key)}</code>)
    } else if (token.startsWith('**')) {
      nodes.push(<strong key={key}>{token.slice(2, -2)}</strong>)
    } else {
      const linkMatch = token.match(/^\[([^\]]+)\]\(([^)]+)\)$/)
      const href = linkMatch ? safeMarkdownHref(options.resolveLink ? options.resolveLink(linkMatch[2]) : linkMatch[2]) : ''
      if (linkMatch && href) {
        const external = /^https?:\/\//i.test(href)
        nodes.push(
          <a key={key} href={href} {...(external ? { target: '_blank', rel: 'noopener noreferrer' } : {})}>
            {renderLabel(linkMatch[1], key, plain)}
          </a>,
        )
      } else {
        nodes.push(options.resolveLink && linkMatch ? <Fragment key={key}>{renderLabel(linkMatch[1], key, plain)}</Fragment> : token)
      }
    }
    lastIndex = pattern.lastIndex
  }

  if (lastIndex < text.length) {
    nodes.push(plain(text.slice(lastIndex), `${keyPrefix}-t${counter}`))
  }

  return nodes
}

function renderMarkdownList(block: MarkdownListBlock, key: string, options: BlockOptions): ReactNode {
  const ListTag = block.ordered ? 'ol' : 'ul'
  const className = block.ordered ? 'markdown-list markdown-ordered-list' : 'markdown-list'
  return (
    <ListTag key={key} className={className} {...(block.ordered && block.start ? { start: block.start } : {})}>
      {block.items.map((item, itemIndex) => (
        <li key={`${key}-${itemIndex}`}>
          {renderMarkdownInline(item.text, `${key}-${itemIndex}`, options)}
          {item.children && renderMarkdownList(item.children, `${key}-${itemIndex}-sub`, options)}
        </li>
      ))}
    </ListTag>
  )
}

function renderMarkdownBlock(block: MarkdownBlock, key: string, options: BlockOptions): ReactNode {
  switch (block.kind) {
    case 'heading': {
      const Tag = `h${Math.min(block.level + 1, 6)}` as 'h2' | 'h3' | 'h4' | 'h5' | 'h6'
      return <Tag key={key} id={block.id} tabIndex={options.copyExamples ? -1 : undefined}>{renderMarkdownInline(block.text, key, options)}</Tag>
    }
    case 'paragraph':
      return <p key={key}>{renderMarkdownInline(block.text, key, options)}</p>
    case 'code': {
      const source = options.copyExamples
        ? <CopyCodeBlock code={block.code} language={block.language} />
        : (
          <pre className="markdown-code-block">
            <code data-language={block.language || undefined}>{block.code}</code>
          </pre>
        )
      // Mermaid ER diagrams draw natively; other Mermaid stays readable source.
      if (block.language === 'mermaid' && declaresErDiagram(block.code)) {
        return (
          <Suspense key={key} fallback={source}>
            <MermaidErDiagram code={block.code} label={options.sectionTitle} fallback={source} />
          </Suspense>
        )
      }
      return <Fragment key={key}>{source}</Fragment>
    }
    case 'list':
      return renderMarkdownList(block, key, options)
    case 'table': {
      const columnClasses = tableColumnClasses(block.headers, block.rows)
      const cellOptions = { ...options, identifierBreaks: true }
      const label = `${options.sectionTitle || 'Table'}: ${block.headers.map(markdownPlainText).join(', ')}`
      return (
        <MarkdownTableWrap key={key} label={label}>
          <table>
            <thead>
              <tr>
                {block.headers.map((header, headerIndex) => (
                  <th key={`${key}-h-${headerIndex}`} className={columnClasses[headerIndex]}>{renderMarkdownInline(header, `${key}-h-${headerIndex}`, cellOptions)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, rowIndex) => (
                <tr key={`${key}-r-${rowIndex}`}>
                  {row.map((cell, cellIndex) => (
                    <td key={`${key}-r-${rowIndex}-c-${cellIndex}`} className={columnClasses[cellIndex]}>{renderMarkdownInline(cell, `${key}-r-${rowIndex}-c-${cellIndex}`, cellOptions)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </MarkdownTableWrap>
      )
    }
    default:
      return null
  }
}

export default function MarkdownRenderer({
  source = '',
  blocks,
  className,
  blockKeyPrefix = 'markdown',
  resolveLink,
  copyExamples,
  renderAfterHeading,
}: MarkdownRendererProps) {
  const parsedBlocks = useMemo(() => blocks ?? parseMarkdown(source), [blocks, source])
  let sectionTitle: string | undefined
  const content = parsedBlocks.map((block, blockIndex) => {
    if (block.kind === 'heading') sectionTitle = markdownPlainText(block.text)
    return <Fragment key={`${blockKeyPrefix}-${blockIndex}`}>
      {renderMarkdownBlock(block, `${blockKeyPrefix}-${blockIndex}`, { resolveLink, copyExamples, sectionTitle })}
      {block.kind === 'heading' && renderAfterHeading?.(block)}
    </Fragment>
  })

  if (className) {
    return <div className={className}>{content}</div>
  }
  return <>{content}</>
}

// A table wider than its column scrolls sideways. Only then is the wrapper a
// named, focusable region, so keyboard users can scroll it without every
// table on a long page becoming a tab stop.
function MarkdownTableWrap({ label, children }: { label: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const [scrollable, setScrollable] = useState(false)
  useEffect(() => {
    const wrap = ref.current
    if (!wrap || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(() => setScrollable(wrap.scrollWidth > wrap.clientWidth + 1))
    observer.observe(wrap)
    if (wrap.firstElementChild) observer.observe(wrap.firstElementChild)
    return () => observer.disconnect()
  }, [])
  return <div ref={ref} className="markdown-table-wrap" {...(scrollable ? { tabIndex: 0, role: 'region', 'aria-label': label } : {})}>{children}</div>
}

function CopyCodeBlock({ code, language }: { code: string; language: string }) {
  const [status, setStatus] = useState('')
  return <div className="help-code-example">
    <div className="help-code-toolbar"><span>{language || 'Example'}</span><button className="btn btn-secondary btn-sm" type="button" onClick={async () => {
      const copied = await copyTextToClipboard(code)
      setStatus(copied ? 'Copied' : 'Copy unavailable. Select the example and copy manually.')
    }}>Copy example</button></div>
    <pre className="markdown-code-block" tabIndex={0} role="region" aria-label={language ? `${language} code example` : 'Code example'}><code data-language={language || undefined}>{code}</code></pre>
    <span role="status">{status}</span>
  </div>
}
