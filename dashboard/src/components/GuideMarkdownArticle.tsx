import { useEffect, useMemo, useRef } from 'react'
import MarkdownRenderer, { MarkdownLabel } from './MarkdownRenderer'
import { markdownPlainText, parseMarkdown, type MarkdownBlock } from './repositoryGuideMarkdown'

type Props = {
  source: string
  blocks?: MarkdownBlock[]
  className?: string
  section?: string
  sectionHref?: (section: string) => string
  resolveLink?: (href: string) => string
}

type Heading = Extract<MarkdownBlock, { kind: 'heading' }>
type OutlineEntry = { heading: Heading; children: Heading[] }

// Past this many entries a single "On this page" list stops being scannable
// (the DSL action reference has 160), so it lists sections only and each
// section opens with an index of its own subsections.
const LONG_OUTLINE = 15
const SECTION_INDEX_MIN = 3

function buildOutline(headings: Heading[]): OutlineEntry[] {
  const outline: OutlineEntry[] = []
  for (const heading of headings) {
    const parent = outline[outline.length - 1]
    if (heading.level === 3 && parent?.heading.level === 2) parent.children.push(heading)
    else outline.push({ heading, children: [] })
  }
  return outline
}

export default function GuideMarkdownArticle({ source, blocks: providedBlocks, className, section, sectionHref, resolveLink }: Props) {
  const blocks = useMemo(() => providedBlocks ?? parseMarkdown(source), [providedBlocks, source])
  const bodyBlocks = blocks.filter(block => block.kind !== 'heading' || block.level !== 1)
  const headings = bodyBlocks.filter((block): block is Heading => block.kind === 'heading' && block.level <= 3)
  const outline = buildOutline(headings)
  const sectioned = headings.length > LONG_OUTLINE && outline.length > 1 && outline.some(entry => entry.children.length >= SECTION_INDEX_MIN)
  const sectionIndexes = new Map<string, Heading[]>(sectioned ? outline.filter(entry => entry.children.length >= SECTION_INDEX_MIN).map(entry => [entry.heading.id, entry.children]) : [])
  const href = (id: string) => sectionHref?.(id) || `#${id}`
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const container = ref.current
    if (!container) return
    // Use the actual toolbar height, including wrapping/zoom, rather than a fixed offset.
    const scroller = container.closest<HTMLElement>('.main-content')
    const toolbar = scroller?.querySelector<HTMLElement>('.main-topbar')
    const updateOffset = () => {
      container.style.setProperty('--help-scroll-offset', `${(toolbar?.offsetHeight || 80) + 32}px`)
      // Access and deployment controls may arrive after the first anchor jump.
      // Re-align the focused destination when that makes the toolbar taller.
      const focused = container.ownerDocument.activeElement as HTMLElement | null
      if (section && focused?.id === section && toolbar && focused.getBoundingClientRect().top < toolbar.getBoundingClientRect().bottom + 24) {
        focused.scrollIntoView?.({ block: 'start', behavior: 'instant' })
      }
    }
    updateOffset()
    const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(updateOffset)
    if (toolbar) observer?.observe(toolbar)
    const frame = requestAnimationFrame(() => {
      const target = section ? Array.from(container.querySelectorAll<HTMLElement>('[id]')).find(node => node.id === section) : undefined
      if (target) {
        target.scrollIntoView?.({ block: 'start', behavior: 'instant' })
        target.focus({ preventScroll: true })
      } else if (scroller) {
        scroller.scrollTop = 0
      }
    })
    return () => { cancelAnimationFrame(frame); observer?.disconnect() }
  }, [source, section, providedBlocks])
  const link = (heading: Heading) => <a href={href(heading.id)} aria-current={section === heading.id ? 'location' : undefined}><MarkdownLabel text={heading.text} /></a>
  return <div ref={ref} className={['guide-markdown-article', className].filter(Boolean).join(' ')} data-table-pagination="off">
    {headings.length > 1 && <nav className="help-article-toc" aria-label="On this page">
      <span>On this page</span>
      <ul>{sectioned
        ? outline.map(({ heading, children }) => <li key={heading.id}>
          {link(heading)}
          {children.length > 0 && <> <span className="help-toc-count">{children.length}<span className="sr-only"> subsections</span></span></>}
        </li>)
        : headings.map(heading => <li key={heading.id} className={heading.level === 3 ? 'help-toc-nested' : undefined}>{link(heading)}</li>)}
      </ul>
    </nav>}
    <article className="repository-guide-content markdown-body">
      <MarkdownRenderer blocks={bodyBlocks} blockKeyPrefix="block" resolveLink={resolveLink} copyExamples renderAfterHeading={heading => {
        const children = sectionIndexes.get(heading.id)
        return children && <nav className="help-section-index" aria-label={`In this section: ${markdownPlainText(heading.text)}`}>
          <ul>{children.map(child => <li key={child.id}>{link(child)}</li>)}</ul>
        </nav>
      }} />
    </article>
  </div>
}
