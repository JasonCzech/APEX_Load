// A single CodeMirror theme + syntax-highlight style wired to the dashboard's CSS
// variables. Because the theme rules reference `var(--…)` and the active theme is
// selected by `data-theme` on <html>, this one extension adapts to the dark, light,
// and solarized themes with no per-theme rebuild.

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
  '.apex-dsl-token': {
    borderRadius: '3px',
  },
  '.apex-dsl-section-key': {
    color: 'var(--accent-hover)',
    fontWeight: '800',
  },
  '.apex-dsl-action-name': {
    color: 'var(--info-text)',
    fontWeight: '800',
  },
  '.apex-dsl-action-method': {
    color: 'var(--warning-text)',
    fontWeight: '750',
  },
  '.apex-dsl-action-target': {
    color: 'var(--success-text)',
    fontWeight: '700',
  },
  '.apex-dsl-parameter-name, .apex-dsl-parameter-reference': {
    color: 'var(--success-text)',
    fontWeight: '800',
  },
  '.apex-dsl-parameter-reference': {
    backgroundColor: 'rgba(var(--success-rgb), 0.14)',
    boxShadow: 'inset 0 -1px 0 rgba(var(--success-rgb), 0.62)',
  },
  '.apex-dsl-data-source-name': {
    color: 'var(--info-text)',
    fontWeight: '750',
  },
  '.apex-dsl-transaction-metadata': {
    color: 'var(--accent-hover)',
    fontWeight: '800',
  },
  '.apex-dsl-supporting-key': {
    color: 'var(--text-secondary)',
    fontWeight: '650',
  },
  '.apex-dsl-config-key': {
    color: 'var(--text-muted)',
    fontWeight: '650',
  },
  '.apex-yaml-key': {
    color: 'var(--accent-hover)',
    fontWeight: '700',
  },
  '.apex-yaml-comment': {
    color: 'var(--text-muted)',
    fontStyle: 'italic',
  },
  '.apex-yaml-atom': {
    color: 'var(--warning-text)',
  },
  '.apex-embedded-xml-tag': {
    color: 'var(--accent-text)',
    fontWeight: '650',
  },
  '.apex-source-template-token': {
    color: 'var(--success-text)',
    fontWeight: '700',
  },
  '.cm-line.apex-dsl-section-block': {
    backgroundColor: 'rgba(var(--accent-rgb), 0.028)',
  },
  '.cm-line.apex-dsl-action-block': {
    backgroundColor: 'rgba(var(--info-rgb), 0.045)',
    boxShadow: 'inset 2px 0 0 rgba(var(--info-rgb), 0.34)',
  },
  '.cm-line.apex-dsl-parameter-block': {
    backgroundColor: 'rgba(var(--success-rgb), 0.045)',
    boxShadow: 'inset 2px 0 0 rgba(var(--success-rgb), 0.34)',
  },
  '.cm-line.apex-dsl-data-source-block': {
    backgroundColor: 'rgba(var(--accent-rgb), 0.04)',
    boxShadow: 'inset 2px 0 0 rgba(var(--accent-rgb), 0.3)',
  },
  '.cm-line.apex-dsl-extractor-block': {
    backgroundColor: 'rgba(var(--warning-rgb), 0.035)',
  },
  '.cm-line.apex-dsl-assertion-block': {
    backgroundColor: 'rgba(var(--danger-rgb), 0.03)',
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
  { tag: [t.keyword, t.modifier, t.controlKeyword, t.operatorKeyword], color: 'var(--accent-text)' },
  { tag: [t.string, t.special(t.string)], color: 'var(--success-text)' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--warning-text)' },
  { tag: [t.propertyName, t.definition(t.propertyName)], color: 'var(--text-primary)' },
  { tag: [t.comment, t.lineComment, t.blockComment], color: 'var(--text-muted)', fontStyle: 'italic' },
  { tag: [t.function(t.variableName), t.function(t.propertyName)], color: 'var(--accent-text)' },
  { tag: [t.typeName, t.className, t.tagName], color: 'var(--accent-text)' },
  { tag: [t.processingInstruction, t.meta], color: 'var(--text-muted)' },
  { tag: [t.operator, t.punctuation, t.separator, t.bracket], color: 'var(--text-secondary)' },
])

export const apexCodeMirrorTheme: Extension = [editorTheme, syntaxHighlighting(highlightStyle)]
