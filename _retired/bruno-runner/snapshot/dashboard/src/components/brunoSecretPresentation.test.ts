import { describe, expect, it } from 'vitest'

import { type BrunoSecretRequirement } from '../types/scriptImports'
import { brunoSecretPresentation } from './brunoSecretPresentation'

const sensitiveHeaderExamples = [
  {
    suggestedName: 'CTA_COLLECTION_6A_CTA_INQUIREACCOUNTPROFILE_312_HEADER_X_CDF_AUTH_TOKEN_84921F7A1055',
    displayLabel: 'X-CDF auth token',
    headerName: 'X-CDF-AUTH-TOKEN',
    context: 'Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.',
    unavailableDescription: 'Sensitive Bruno request header "X-CDF-AUTH-TOKEN" references {{authToken}}, but the collection does not include a value for that source variable. A configured destination value is required when this request runs.',
  },
  {
    suggestedName: 'CTA_COLLECTION_6A_CTA_ACTIVATESUBSCRIBER_V302_HEADER_IDPCTX_IXP_DARK_MODE_SESSION_161E2747AD92',
    displayLabel: 'IDP context IXP dark mode session',
    headerName: 'idpctx-ixp-dark-mode-session',
    context: 'Sensitive Bruno request header "idpctx-ixp-dark-mode-session" contains an imported value that can be preserved for this request.',
    unavailableDescription: '',
  },
  {
    suggestedName: 'CTA_COLLECTION_6A_CTA_INQUIRECPNICONTACTINFORMATION_224_HEADER_IDPCTX_SESSION_ID',
    displayLabel: 'IDP context session ID',
    headerName: 'idpctx-session-id',
    context: 'Sensitive Bruno request header "idpctx-session-id" contains an imported value that can be preserved for this request.',
    unavailableDescription: '',
  },
] as const

describe('brunoSecretPresentation', () => {
  it.each(sensitiveHeaderExamples)(
    'leads with the authored $headerName header instead of its path-derived project-secret name',
    ({ suggestedName, displayLabel, headerName, context, unavailableDescription }) => {
      const requirement = {
        id: `requirement-${headerName}`,
        path: 'CTA_Collection 6a/CTA/request.yml',
        key: `native-header:/http/headers/${headerName}`,
        suggested_name: suggestedName,
        display_label: displayLabel,
        replacement: `{{secret.${suggestedName}}}`,
        context,
      } satisfies BrunoSecretRequirement

      const presentation = brunoSecretPresentation(requirement)

      expect(presentation.label).toBe(displayLabel)
      expect(presentation.label).not.toContain('CTA_COLLECTION_6A')
      expect(presentation.context).toBe(context)
      expect(presentation.origin).toBe('Bruno sensitive request header')
      expect(presentation.purpose)
        .toBe(`Supplies the credential value sent in the ${headerName} HTTP header when this request runs.`)
      expect(presentation.preservationUnavailableDescription).toBe(unavailableDescription)
      if (unavailableDescription) {
        expect(presentation.cardContext).not.toContain('does not include a value')
        expect(presentation.cardContext).toContain(`Origin: ${presentation.origin} "${headerName}".`)
      } else {
        expect(presentation.cardContext).toContain(context)
      }
      expect(presentation.cardContext).toContain(`Runtime purpose: ${presentation.purpose}`)
    },
  )

  it('keeps the former imperative context readable without treating it as current state', () => {
    const presentation = brunoSecretPresentation({
      id: 'legacy-imperative-header',
      path: 'CTA/request.yml',
      key: 'native-header:/http/headers/X-LEGACY-AUTH',
      suggested_name: 'CTA_REQUEST_HEADER_X_LEGACY_AUTH',
      display_label: 'X legacy auth',
      replacement: '{{secret.CTA_REQUEST_HEADER_X_LEGACY_AUTH}}',
      context: 'Sensitive Bruno request header "X-LEGACY-AUTH" has no recoverable imported value. Configure the generated project secret before running this request.',
    })

    expect(presentation.preservationUnavailableDescription)
      .toBe('Sensitive Bruno request header "X-LEGACY-AUTH" has no recoverable imported value.')
    expect(presentation.context)
      .toBe('Sensitive Bruno request header "X-LEGACY-AUTH" has no recoverable imported value.')
    expect(presentation.cardContext).not.toContain('Configure the generated project secret')
  })

  it('recognizes the legacy sensitive-header context without replacing its authored label', () => {
    const presentation = brunoSecretPresentation({
      id: 'legacy-header',
      path: 'CTA/request.yml',
      key: 'native-header:/http/headers/X-LEGACY-TOKEN',
      suggested_name: 'CTA_REQUEST_HEADER_X_LEGACY_TOKEN',
      replacement: '{{secret.CTA_REQUEST_HEADER_X_LEGACY_TOKEN}}',
      context: 'Bruno sensitive header X-LEGACY-TOKEN',
    })

    expect(presentation.label).toBe('X-LEGACY-TOKEN request header')
    expect(presentation.cardContext).toContain('Bruno sensitive header X-LEGACY-TOKEN')
    expect(presentation.cardContext).toContain('Runtime purpose:')
    expect(presentation.preservationUnavailableDescription).toBe('')
  })

  it('keeps explicit converter presentation metadata for non-header credentials', () => {
    const presentation = brunoSecretPresentation({
      id: 'partner-username',
      path: 'CTA/request.yml',
      key: 'partnerUserName',
      suggested_name: 'PARTNER_USERNAME',
      display_label: 'Partner username',
      replacement: '{{secret.PARTNER_USERNAME}}',
      context: 'XML username element <mh:userName>',
    })

    expect(presentation).toEqual({
      label: 'Partner username',
      context: 'XML username element <mh:userName>',
      cardContext: 'XML username element <mh:userName>',
      origin: '',
      purpose: '',
      preservationUnavailableDescription: '',
    })
  })
})
