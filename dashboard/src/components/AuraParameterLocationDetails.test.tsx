// @vitest-environment jsdom
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import AuraParameterLocationDetails from './AuraParameterLocationDetails'
import { auraEncodingLabel } from './auraParameterLocationModel'

describe('Aura parameter location details', () => {
  it('keeps repeated JSON string boundaries in their original order', () => {
    const html = renderToStaticMarkup(<AuraParameterLocationDetails location={{ action_path: 'teardown[0]', decoded_field_path: 'actions[0].params.details::<json>.child::<json>.recordId', encoding_layers: ['json', 'json_string', 'json_string'] }} />)
    expect(html).toContain('JSON envelope → JSON string → JSON string')
    expect(html).toContain('teardown[0]')
  })

  it('never renders unrecognized source encoding names', () => {
    for (const layer of ['private-source-label', '__proto__', 'constructor']) expect(auraEncodingLabel(layer)).toBe('Unrecognized encoding')
    const html = renderToStaticMarkup(<AuraParameterLocationDetails location={{ encoding_layers: ['private-source-label'] }} />)
    expect(html).toContain('Unrecognized encoding')
    expect(html).not.toContain('private-source-label')
  })
})
