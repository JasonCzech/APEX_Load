import type { MarkdownBlock } from './repositoryGuideMarkdown'

// A Mermaid diagram's first statement names its type; only an erDiagram can
// draw natively, so other Mermaid never loads the diagram renderer.
export function declaresErDiagram(code: string): boolean {
  return code.split('\n').map(line => line.trim()).find(line => line && !line.startsWith('%%')) === 'erDiagram'
}

/** Whether rendering these blocks loads the ER diagram renderer. */
export function containsErDiagram(blocks: MarkdownBlock[]): boolean {
  return blocks.some(block => block.kind === 'code' && block.language === 'mermaid' && declaresErDiagram(block.code))
}

/**
 * Loads the ER diagram renderer. The lazy renderer and anything that must wait
 * for the drawn diagrams (a Help deep link's anchor jump) share this module.
 */
export function loadMermaidErDiagram() {
  return import('./MermaidErDiagram')
}
