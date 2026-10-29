import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { PreviewText } from './ScriptReplayDebuggerView'

it('explains that resolved secret values can appear in debugger evidence', () => {
  const html = renderToStaticMarkup(<PreviewText segments={[{ kind: 'secret', text: '{{secret.token}}' }]} />)
  expect(html).toContain('resolved when the step runs')
  expect(html).toContain('Debugger evidence can contain the resolved value.')
  expect(html).not.toContain('never shown')
})
