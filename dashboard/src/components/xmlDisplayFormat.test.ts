import { describe, expect, it } from 'vitest'

import { formatXMLForDisplay } from './xmlDisplayFormat'

describe('formatXMLForDisplay', () => {
  it('indents compact SOAP XML without changing source tokens', () => {
    const source = '<SOAP-ENV:Envelope xmlns:SOAP-ENV="urn:soap"><SOAP-ENV:Body><Check><Username>{{secret.USERNAME}}</Username><!--keep--><Enabled>true</Enabled></Check></SOAP-ENV:Body></SOAP-ENV:Envelope>'
    const formatted = formatXMLForDisplay(source)

    expect(formatted).toBe([
      '<SOAP-ENV:Envelope xmlns:SOAP-ENV="urn:soap">',
      '  <SOAP-ENV:Body>',
      '    <Check>',
      '      <Username>{{secret.USERNAME}}</Username>',
      '      <!--keep-->',
      '      <Enabled>true</Enabled>',
      '    </Check>',
      '  </SOAP-ENV:Body>',
      '</SOAP-ENV:Envelope>',
    ].join('\n'))
    expect(formatted?.replace(/\n\s*/g, '')).toBe(source)
  })

  it('preserves entities, CDATA, declarations, and greater-than signs in attributes', () => {
    const source = '<?xml version="1.0"?><root test="a > b"><value>A &amp; B</value><raw><![CDATA[<not-a-tag>]]></raw></root>'
    const formatted = formatXMLForDisplay(source)

    expect(formatted).toContain('<value>A &amp; B</value>')
    expect(formatted).toContain('    <![CDATA[<not-a-tag>]]>')
    expect(formatted?.replace(/\n\s*/g, '')).toBe(source)
  })

  it('preserves significant whitespace in text-only elements', () => {
    const source = '<root><value> A &amp; B </value></root>'

    expect(formatXMLForDisplay(source)).toBe([
      '<root>',
      '  <value> A &amp; B </value>',
      '</root>',
    ].join('\n'))
  })

  it('declines mixed content instead of changing its visible whitespace', () => {
    expect(formatXMLForDisplay('<p>Hello <b>world</b> again</p>')).toBeNull()
    expect(formatXMLForDisplay('<p><![CDATA[Hello ]]><b>world</b></p>')).toBeNull()
  })

  it('declines malformed or non-XML bodies instead of inventing a projection', () => {
    expect(formatXMLForDisplay('{"not":"xml"}')).toBeNull()
    expect(formatXMLForDisplay('<root><child></root>')).toBeNull()
    expect(formatXMLForDisplay('<root')).toBeNull()
    expect(formatXMLForDisplay('<first/><second/>')).toBeNull()
    expect(formatXMLForDisplay('<>')).toBeNull()
    expect(formatXMLForDisplay('<root duplicate="one" duplicate="two"/>')).toBeNull()
    expect(formatXMLForDisplay('<root invalid=unquoted/>')).toBeNull()
    expect(formatXMLForDisplay('<root/>trailing text')).toBeNull()
  })

  it('declines documents that exceed safe nesting or formatted-output limits', () => {
    const tooDeep = `${'<node>'.repeat(257)}${'</node>'.repeat(257)}`
    const outputAmplifier = `${'<node>'.repeat(256)}${'<leaf/>'.repeat(2_000)}${'</node>'.repeat(256)}`

    expect(formatXMLForDisplay(tooDeep)).toBeNull()
    expect(formatXMLForDisplay(outputAmplifier)).toBeNull()
  })
})
