import { useMemo } from 'react'
import guideMarkdown from '../../../docs/API_USER_GUIDE.md?raw'
import { useApiGuideTopbarContribution } from './useApiGuideTopbarContribution'
import MarkdownRenderer from './MarkdownRenderer'
import { parseMarkdown, type MarkdownBlock } from './repositoryGuideMarkdown'

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
        <MarkdownRenderer blocks={bodyBlocks} blockKeyPrefix="block" />
      </article>
    </div>
  )
}
