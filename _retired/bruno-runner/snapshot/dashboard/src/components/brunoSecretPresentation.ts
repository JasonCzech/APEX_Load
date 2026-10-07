import { type BrunoSecretRequirement } from '../types/scriptImports'

const SENSITIVE_HEADER_CONTEXT = /^Sensitive Bruno request header\s+"([^"]+)"/i
const LEGACY_SENSITIVE_HEADER_CONTEXT = /^Bruno sensitive header\s+(.+)$/i
const CONFIGURE_HEADER_SECRET_ACTION = /\s+Configure the generated project secret before running this request\.\s*$/i
const CONFIGURED_DESTINATION_REQUIRED = /\s+A configured destination value is required when this request runs\.\s*$/i

export interface BrunoSecretPresentation {
  label: string
  context: string
  cardContext: string
  origin: string
  purpose: string
  preservationUnavailableDescription: string
}

export function brunoSecretPresentation(
  requirement: BrunoSecretRequirement,
): BrunoSecretPresentation {
  const context = requirement.context?.trim() || ''
  const currentHeaderMatch = SENSITIVE_HEADER_CONTEXT.exec(context)
  const legacyHeaderMatch = LEGACY_SENSITIVE_HEADER_CONTEXT.exec(context)
  const headerName = (currentHeaderMatch?.[1] || legacyHeaderMatch?.[1] || '').trim()
  if (!headerName) {
    return {
      label: requirement.display_label || requirement.suggested_name,
      context,
      cardContext: context,
      origin: '',
      purpose: '',
      preservationUnavailableDescription: '',
    }
  }

  const origin = 'Bruno sensitive request header'
  const purpose = `Supplies the credential value sent in the ${headerName} HTTP header when this request runs.`
  const stateIndependentContext = context.replace(CONFIGURE_HEADER_SECRET_ACTION, '').trim()
  const hasStateIndependentDestinationRequirement = CONFIGURED_DESTINATION_REQUIRED.test(context)
  const preservationUnavailableDescription = !currentHeaderMatch
    ? ''
    : stateIndependentContext !== context
      ? stateIndependentContext
      : hasStateIndependentDestinationRequirement
        ? context
        : ''
  const cardContext = preservationUnavailableDescription
    ? `Origin: ${origin} "${headerName}". Runtime purpose: ${purpose}`
    : `${stateIndependentContext} Runtime purpose: ${purpose}`
  return {
    label: requirement.display_label || `${headerName} request header`,
    context: stateIndependentContext,
    cardContext,
    origin,
    purpose,
    preservationUnavailableDescription,
  }
}
