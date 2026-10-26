import type { MarkdownBlock } from './repositoryGuideMarkdown'

// A copyable example draws in the highlighted code view, except Mermaid,
// which stays readable source (or an ER diagram).
export function highlightsCode(language: string): boolean {
  return language !== 'mermaid'
}

/** Whether rendering these blocks as copyable examples loads the code view. */
export function containsHighlightedCode(blocks: MarkdownBlock[]): boolean {
  return blocks.some(block => block.kind === 'code' && highlightsCode(block.language))
}

/**
 * Loads the highlighted code view, which brings CodeMirror. The lazy view and
 * anything that must wait for it (a Help deep link's anchor jump) share this
 * module, so pages that only render Markdown never load the editor.
 */
export function loadHelpCodeView() {
  return import('./HelpCodeView')
}
