/**
 * What the project secret an Event Hubs credential binding names must hold:
 * the connection string, in the format the API states on the requirement
 * (secret_binding_requirements[].context) or, without it, the form's own
 * wording. Shared by the import's batch repair and a saved script's repair.
 *
 * The API's text already names what the value is ("Event Hubs connection
 * string: ..."), and the converter's text names the hub, so it is shown as
 * written: the hub sentence is added only when the text does not name it.
 */
export function EventHubsConnectionFormat({ id, context, eventHub }: { id: string; context?: string; eventHub?: string }) {
  const format = context?.trim()
  const hub = eventHub?.trim()
  return format
    ? <p id={id}>{format}{hub && !format.includes(`"${hub}"`) ? <> This script's event hub is <code>{hub}</code>.</> : null}</p>
    : <p id={id}>The secret's value is the Event Hubs connection string, in the form <code>Endpoint=sb://&lt;namespace&gt;/;SharedAccessKeyName=&lt;name&gt;;SharedAccessKey=&lt;key&gt;</code>, as the Azure portal shows it under Shared access policies. It may end with <code>;EntityPath=&lt;hub&gt;</code> only when that names this script's event hub{hub ? <> (<code>{hub}</code>)</> : null}. Copy the key without surrounding spaces.</p>
}
