import { useEffect, useMemo, useRef } from 'react'
import MarkdownRenderer, { MarkdownLabel } from './MarkdownRenderer'
import { containsErDiagram, loadMermaidErDiagram } from './mermaidErDiagramLoader'
import { containsHighlightedCode } from './helpCodeViewLoader'
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

// Input that shows the reader has taken over the page from a deep link.
const READER_INTENTS = ['wheel', 'touchstart', 'keydown', 'pointerdown'] as const
// How long the destination stays aligned after the last layout change it
// followed, and at most.
export const FOLLOW_IDLE_MS = 1500
export const FOLLOW_MAX_MS = 10_000

// React may reveal lazily drawn diagrams a little after their chunk arrives,
// and each diagram lays out again once it measures its width. Keep the
// destination aligned as the article above it changes height, until focus
// leaves it, the page scrolls other than by this alignment (screen-reader
// browsing fires no key events), or the layout has settled.
function followDestination(article: HTMLElement, scroller: HTMLElement | null, target: HTMLElement): () => void {
  if (typeof ResizeObserver === 'undefined') return () => {}
  let alignedTop = scroller?.scrollTop
  let idle: ReturnType<typeof setTimeout> | undefined
  const stop = () => {
    observer.disconnect()
    scroller?.removeEventListener('scroll', onScroll)
    clearTimeout(idle)
    clearTimeout(cap)
  }
  const onScroll = () => { if (scroller && scroller.scrollTop !== alignedTop) stop() }
  const observer = new ResizeObserver(() => {
    if (article.ownerDocument.activeElement !== target) {
      stop()
      return
    }
    target.scrollIntoView?.({ block: 'start', behavior: 'instant' })
    alignedTop = scroller?.scrollTop
    clearTimeout(idle)
    idle = setTimeout(stop, FOLLOW_IDLE_MS)
  })
  const cap = setTimeout(stop, FOLLOW_MAX_MS)
  idle = setTimeout(stop, FOLLOW_IDLE_MS)
  observer.observe(article)
  scroller?.addEventListener('scroll', onScroll, { passive: true })
  return stop
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
  const waitsForDiagrams = Boolean(section) && containsErDiagram(bodyBlocks)
  // Code examples show their plain source in the highlighted view's box until
  // it loads, so a deep link does not wait for them; it follows them, in case
  // highlighting still changes the layout.
  const followsLazyBlocks = waitsForDiagrams || (Boolean(section) && containsHighlightedCode(bodyBlocks))
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
    let cancelled = false
    let frame = 0
    let stopFollowing = () => {}
    // While a deep link waits for its diagrams, and while it follows them or
    // code examples, the reader may scroll or move focus; from then on the
    // page is theirs.
    let readerActed = false
    const onIntent = () => {
      readerActed = true
      stopFollowing()
    }
    const document = container.ownerDocument
    if (followsLazyBlocks) for (const type of READER_INTENTS) document.addEventListener(type, onIntent, { capture: true, passive: true })
    const jump = () => {
      frame = requestAnimationFrame(() => {
        if (readerActed) return
        const target = section ? Array.from(container.querySelectorAll<HTMLElement>('[id]')).find(node => node.id === section) : undefined
        if (target) {
          target.scrollIntoView?.({ block: 'start', behavior: 'instant' })
          target.focus({ preventScroll: true })
          if (followsLazyBlocks) stopFollowing = followDestination(container, scroller, target)
        } else if (scroller) {
          scroller.scrollTop = 0
        }
      })
    }
    // ER diagrams show their shorter source until the renderer loads, so a
    // deep link into an article with diagrams jumps once it has (or failed).
    if (waitsForDiagrams) void loadMermaidErDiagram().catch(() => undefined).then(() => { if (!cancelled && !readerActed) jump() })
    else jump()
    return () => {
      cancelled = true
      cancelAnimationFrame(frame)
      observer?.disconnect()
      stopFollowing()
      for (const type of READER_INTENTS) document.removeEventListener(type, onIntent, true)
    }
  }, [source, section, providedBlocks, waitsForDiagrams, followsLazyBlocks])
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
