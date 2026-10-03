import type { ParameterizationLocation } from '../types/scriptImports'

const encodingLabels = new Map<string, string>([
  ['base64', 'Base64 body'],
  ['url_form', 'URL form message'],
  ['json', 'JSON envelope'],
  ['json_string', 'JSON string'],
])

export function auraEncodingLabel(layer: string): string {
  return encodingLabels.get(layer) || 'Unrecognized encoding'
}

export function isAuraParameterLocation(location: ParameterizationLocation): boolean {
  return location.aura_route !== undefined || location.decoded_field_path !== undefined
    || location.encoding_layers !== undefined || location.payload_shape !== undefined
}
