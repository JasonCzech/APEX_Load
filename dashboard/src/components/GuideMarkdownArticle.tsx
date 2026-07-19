import { useMemo } from 'react'

import MarkdownRenderer from './MarkdownRenderer'
import { parseMarkdown, type MarkdownBlock } from './repositoryGuideMarkdown'

type GuideMarkdownArticleProps = {
  source: string
  className?: string
}

// Renders an in-repository markdown guide with an auto-generated table of contents.
// The first level-1 heading is treated as the document title (not repeated in the
// body); level-2 headings become the on-page contents with smooth-scroll anchors.
// Shared by the API Guide's User Guide / Admin Guide tabs and the Repository Guide page.
export default function GuideMarkdownArticle({ source, className }: GuideMarkdownArticleProps) {
  const blocks = useMemo(() => parseMarkdown(source), [source])
  const title = blocks.find(block => block.kind === 'heading' && block.level === 1)
  const bodyBlocks = title ? blocks.filter(block => block !== title) : blocks
  const tableOfContents = useMemo(
    () =>
      blocks.filter(
        (block): block is Extract<MarkdownBlock, { kind: 'heading' }> => block.kind === 'heading' && block.level === 2,
      ),
    [blocks],
  )

  const containerClass = ['guide-markdown-article', className].filter(Boolean).join(' ')

  return (
    <div className={containerClass}>
      {tableOfContents.length > 0 && (
        <nav className="repository-guide-toc" aria-label="Guide contents">
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
        <MarkdownRenderer blocks={bodyBlocks} blockKeyPrefix="block" />
      </article>
    </div>
  )
}
