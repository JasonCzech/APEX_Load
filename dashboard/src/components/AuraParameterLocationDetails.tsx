import type { ParameterizationLocation } from '../types/scriptImports'
import { auraEncodingLabel } from './auraParameterLocationModel'
import './AuraParameterLocationDetails.css'

type Location = Pick<ParameterizationLocation, 'action_path' | 'decoded_field_path' | 'encoding_layers'>

// Paths come from the server's validated structural route. Never reconstruct a
// DSL action path from HAR ordinals, display raw route keys, or expose payloads.
export default function AuraParameterLocationDetails({ location }: { location: Location }) {
  return <div className="aura-parameter-location-details" role="group" aria-label="Decoded Aura field">
    <p>Action: <code>{location.action_path || 'Action path unavailable'}</code></p>
    <p>Decoded field: <code>{location.decoded_field_path || 'Decoded field path unavailable'}</code></p>
    <p>Encoding: {location.encoding_layers?.length
      ? location.encoding_layers.slice(0, 16).map(auraEncodingLabel).join(' → ')
      : 'Encoding unavailable'}{(location.encoding_layers?.length || 0) > 16 && ' → Additional layers'}</p>
  </div>
}
