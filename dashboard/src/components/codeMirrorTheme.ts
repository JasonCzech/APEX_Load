// A single CodeMirror theme + syntax-highlight style wired to the dashboard's CSS
// variables. Because the theme rules reference `var(--…)` and the active theme is
// selected by `data-theme` on <html>, this one extension adapts to the dark, light,
// and sepia themes with no per-theme rebuild.

import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { type Extension } from '@codemirror/state'
import { EditorView } from '@codemirror/view'
import { tags as t } from '@lezer/highlight'

const editorTheme = EditorView.theme({
  '&': {
    color: 'var(--text-secondary)',
    backgroundColor: 'var(--bg-primary)',
    fontSize: '12px',
  },
  '.cm-content': {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
    caretColor: 'var(--accent)',
  },
  '.cm-scroller': {
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace',
    lineHeight: '1.5',
  },
  '&.cm-focused': { outline: 'none' },
  '.cm-gutters': {
    backgroundColor: 'var(--bg-card)',
    color: 'var(--text-muted)',
    border: 'none',
    borderRight: '1px solid var(--border)',
  },
  '.cm-activeLineGutter': { backgroundColor: 'transparent' },
  '.cm-lineNumbers .cm-gutterElement': {
    color: 'var(--text-muted)',
    padding: '0 6px 0 8px',
  },
  // The highlight for the line range linked from the other pane.
  '.cm-line.apex-linked-line': {
    backgroundColor: 'var(--accent-soft)',
    color: 'var(--text-primary)',
  },
  // Status gutter swatches (set as a CSS class on each gutter element).
  '.apex-status-gutter': {
    width: '4px',
    padding: '0',
  },
  '.apex-status-marker': {
    display: 'block',
    width: '4px',
    height: '100%',
    backgroundColor: 'transparent',
  },
  '.apex-status-marker.converted': { backgroundColor: 'var(--success)' },
  '.apex-status-marker.preserved': { backgroundColor: 'var(--accent)' },
  '.apex-status-marker.review': { backgroundColor: 'var(--warning)' },
  '.apex-status-marker.unsupported': { backgroundColor: 'var(--danger)' },
  '.apex-status-marker.no-origin': {
    backgroundColor: 'transparent',
    borderLeft: '2px dashed var(--warning)',
  },
})

const highlightStyle = HighlightStyle.define([
  { tag: [t.keyword, t.modifier, t.controlKeyword, t.operatorKeyword], color: 'var(--accent)' },
  { tag: [t.string, t.special(t.string)], color: 'var(--success)' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--warning)' },
  { tag: [t.propertyName, t.definition(t.propertyName)], color: 'var(--text-primary)' },
  { tag: [t.comment, t.lineComment, t.blockComment], color: 'var(--text-muted)', fontStyle: 'italic' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: 'var(--accent)' },
  { tag: [t.typeName, t.className, t.tagName], color: 'var(--accent)' },
  { tag: [t.processingInstruction, t.meta], color: 'var(--text-muted)' },
  { tag: [t.operator, t.punctuation, t.separator, t.bracket], color: 'var(--text-secondary)' },
])

export const apexCodeMirrorTheme: Extension = [editorTheme, syntaxHighlighting(highlightStyle)]
