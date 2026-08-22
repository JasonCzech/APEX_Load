import { useMemo } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { xml } from '@codemirror/lang-xml'
import { EditorView } from '@codemirror/view'

import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { apexDslSemanticHighlighting } from './apexDslSemanticHighlighting'
import { detectSourceCodeLanguage } from './sourceCodeLanguage'
import { yamlSyntaxHighlighting } from './yamlSyntaxHighlighting'
import './SourceCodeViewer.css'

interface Props {
  ariaLabel?: string
  className?: string
  semanticMode?: 'apex-dsl'
  sourcePath: string
  value: string
}

export default function SourceCodeViewer({
  ariaLabel,
  className = '',
  semanticMode,
  sourcePath,
  value,
}: Props) {
  const language = detectSourceCodeLanguage(sourcePath, value)
  const extensions = useMemo(() => [
    apexCodeMirrorTheme,
    EditorView.editable.of(false),
    ...(language === 'json' ? [json()] : language === 'xml' ? [xml()] : [xml(), yamlSyntaxHighlighting]),
    ...(semanticMode === 'apex-dsl' ? [apexDslSemanticHighlighting()] : []),
  ], [language, semanticMode])

  const rootClassName = ['source-code-viewer', className].filter(Boolean).join(' ')

  return (
    <div className={rootClassName} role="region" aria-label={ariaLabel || `${language.toUpperCase()} source API`}>
      <CodeMirror
        value={value}
        extensions={extensions}
        editable={false}
        height="100%"
        maxHeight="100%"
        readOnly
        theme="none"
        basicSetup={{
          lineNumbers: true,
          foldGutter: true,
          highlightActiveLine: false,
          highlightActiveLineGutter: false,
          dropCursor: false,
          allowMultipleSelections: false,
          highlightSelectionMatches: false,
        }}
      />
    </div>
  )
}
