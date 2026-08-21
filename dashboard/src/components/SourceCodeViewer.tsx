import { useMemo } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { json } from '@codemirror/lang-json'
import { xml } from '@codemirror/lang-xml'
import { EditorView } from '@codemirror/view'

import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { detectSourceCodeLanguage } from './sourceCodeLanguage'
import { yamlSyntaxHighlighting } from './yamlSyntaxHighlighting'

export default function SourceCodeViewer({ sourcePath, value }: { sourcePath: string; value: string }) {
  const language = detectSourceCodeLanguage(sourcePath, value)
  const extensions = useMemo(() => [
    apexCodeMirrorTheme,
    EditorView.lineWrapping,
    EditorView.editable.of(false),
    ...(language === 'json' ? [json()] : language === 'xml' ? [xml()] : [xml(), yamlSyntaxHighlighting]),
  ], [language])

  return (
    <div className="bruno-native-source-viewer" role="region" aria-label={`${language.toUpperCase()} source API`}>
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
