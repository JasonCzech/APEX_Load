import { useMemo } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { xml } from '@codemirror/lang-xml'
import { EditorView } from '@codemirror/view'
import { apexCodeMirrorTheme } from './codeMirrorTheme'
import { yamlSyntaxHighlighting } from './yamlSyntaxHighlighting'
import { guideCodeHighlighting, guideCodeLanguage } from './guideCodeLanguage'

/** A Help code example in a read-only, highlighted CodeMirror view. */
export default function HelpCodeView({ code, language, label }: { code: string; language: string; label: string }) {
  const extensions = useMemo(() => [
    apexCodeMirrorTheme,
    guideCodeHighlighting,
    // CodeMirror's content is a read-only textbox, which needs a name of its own.
    EditorView.contentAttributes.of({ 'aria-label': label }),
    ...(language === 'json' || language === 'jsonc' ? [json()] :
      language === 'xml' || language === 'html' ? [xml()] :
        language === 'javascript' || language === 'js' || language === 'typescript' || language === 'ts' ? [javascript({ typescript: language === 'typescript' || language === 'ts' })] :
          language === 'yaml' || language === 'yml' ? [xml(), yamlSyntaxHighlighting] :
            [guideCodeLanguage(language)]),
  ], [label, language])
  return <CodeMirror
    value={code}
    extensions={extensions}
    editable={false}
    readOnly
    theme="none"
    basicSetup={false}
  />
}
