import { useEffect, useMemo, useRef } from 'react'
import MarkdownRenderer from './MarkdownRenderer'
import { parseMarkdown, type MarkdownBlock } from './repositoryGuideMarkdown'

type Props = {
  source: string
  blocks?: MarkdownBlock[]
  className?: string
  section?: string
  sectionHref?: (section: string) => string
  resolveLink?: (href: string) => string
}

export default function GuideMarkdownArticle({ source, blocks: providedBlocks, className, section, sectionHref, resolveLink }: Props) {
  const blocks = useMemo(() => providedBlocks ?? parseMarkdown(source), [providedBlocks, source])
  const bodyBlocks = blocks.filter(block => block.kind !== 'heading' || block.level !== 1)
  const headings = bodyBlocks.filter((block): block is Extract<MarkdownBlock, { kind: 'heading' }> => block.kind === 'heading' && block.level <= 3)
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
  return <div ref={ref} className={['guide-markdown-article', className].filter(Boolean).join(' ')} data-table-pagination="off">
    {headings.length > 1 && <nav className="help-article-toc" aria-label="On this page">
      <span>On this page</span>
      <ul>{headings.map(heading => <li key={heading.id} className={heading.level === 3 ? 'help-toc-nested' : undefined}>
        <a href={sectionHref?.(heading.id) || `#${heading.id}`} aria-current={section === heading.id ? 'location' : undefined}>{heading.text}</a>
      </li>)}</ul>
    </nav>}
    <article className="repository-guide-content markdown-body">
      <MarkdownRenderer blocks={bodyBlocks} blockKeyPrefix="block" resolveLink={resolveLink} copyExamples />
    </article>
  </div>
}
