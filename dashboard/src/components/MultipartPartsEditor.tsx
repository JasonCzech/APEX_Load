import type { ScriptMultipartPart } from '../types/scripts'

// MultipartPartsEditor edits a body_multipart: one row per form field. A
// file field reads a file data source ({{file.NAME}}) or keeps the inline
// bytes it was recorded or imported with; replay builds the body with a
// fresh boundary.

const FILE_REFERENCE = /^\{\{\s*file\.([\w.-]+)\s*\}\}$/

function base64ByteLength(value: string): number {
  const trimmed = value.replace(/\s/g, '')
  const padding = trimmed.endsWith('==') ? 2 : trimmed.endsWith('=') ? 1 : 0
  return Math.max(0, Math.floor(trimmed.length * 3 / 4) - padding)
}

function headersText(headers?: Record<string, string>): string {
  return Object.entries(headers || {}).map(([name, value]) => `${name}: ${value}`).join('\n')
}

function parseHeadersText(text: string): Record<string, string> | undefined {
  const headers: Record<string, string> = {}
  for (const line of text.split('\n')) {
    const separator = line.indexOf(':')
    if (separator <= 0) continue
    const name = line.slice(0, separator).trim()
    if (name) headers[name] = line.slice(separator + 1).trim()
  }
  return Object.keys(headers).length > 0 ? headers : undefined
}

export function MultipartPartsEditor({ parts, fileSources, onChange }: {
  parts: ScriptMultipartPart[]
  fileSources: string[]
  onChange: (parts: ScriptMultipartPart[]) => void
}) {
  const update = (index: number, patch: Partial<ScriptMultipartPart>) =>
    onChange(parts.map((part, i) => (i === index ? { ...part, ...patch } : part)))
  return (
    <div className="multipart-parts-editor" data-testid="multipart-parts-editor">
      <p>Replay builds this multipart/form-data body from its fields with a new boundary on every request and sets the Content-Type header to match. Fields expand {'{{…}}'} references.</p>
      <ol className="multipart-parts">
        {parts.map((part, index) => {
          const file = part.filename !== undefined && part.filename !== null
          const inline = part.value_encoding === 'base64'
          const source = FILE_REFERENCE.exec(part.value || '')?.[1] || ''
          const label = `Field ${index + 1}`
          return (
            <li key={index} className="multipart-part">
              <div className="multipart-part-row">
                <label>Name<input aria-label={`${label} name`} value={part.name} onChange={event => update(index, { name: event.target.value })} /></label>
                <label>Kind<select aria-label={`${label} kind`} value={file ? 'file' : 'text'} onChange={event => update(index, event.target.value === 'file' ? { filename: part.filename || '', value_encoding: inline ? part.value_encoding : undefined } : { filename: undefined, value: inline ? '' : part.value, value_encoding: undefined })}>
                  <option value="text">Text</option>
                  <option value="file">File</option>
                </select></label>
                <button type="button" className="btn btn-sm btn-secondary" aria-label={`Remove ${label}`} onClick={() => onChange(parts.filter((_, i) => i !== index))}>Remove</button>
              </div>
              {file ? (
                <div className="multipart-part-row">
                  <label>Filename<input aria-label={`${label} filename`} value={part.filename || ''} onChange={event => update(index, { filename: event.target.value })} /></label>
                  <label>Content type<input aria-label={`${label} content type`} value={part.content_type || ''} placeholder="application/octet-stream" onChange={event => update(index, { content_type: event.target.value || undefined })} /></label>
                  <label>Content<select aria-label={`${label} content`} value={inline ? '#inline' : source ? source : '#other'} onChange={event => {
                    const choice = event.target.value
                    if (choice === '#inline' || choice === '#other') return
                    update(index, { value: `{{file.${choice}}}`, value_encoding: undefined })
                  }}>
                    {inline && <option value="#inline">Inline bytes ({base64ByteLength(part.value || '')} bytes)</option>}
                    {!inline && !source && <option value="#other">{part.value ? 'Template value' : 'Choose a file data source'}</option>}
                    {fileSources.map(name => <option key={name} value={name}>File data source: {name}</option>)}
                  </select></label>
                </div>
              ) : (
                <label className="multipart-part-value">{part.value_encoding === 'windows-1252' ? 'Value (sent as Windows-1252)' : 'Value'}<textarea aria-label={`${label} value`} rows={2} value={inline ? '' : part.value || ''} placeholder={inline ? `Binary value (${base64ByteLength(part.value || '')} bytes)` : ''} disabled={inline} onChange={event => update(index, { value: event.target.value })} /></label>
              )}
              <details>
                <summary>Part headers ({Object.keys(part.headers || {}).length})</summary>
                {/* Edited as free text and saved on blur; keyed by the part's headers so
                    the text follows the part when rows shift. */}
                <textarea key={headersText(part.headers)} aria-label={`${label} headers`} rows={2} defaultValue={headersText(part.headers)} placeholder="Content-Transfer-Encoding: binary" onBlur={event => update(index, { headers: parseHeadersText(event.target.value) })} />
              </details>
            </li>
          )
        })}
      </ol>
      <div className="author-body-toolbar">
        <button type="button" className="btn btn-sm btn-secondary" onClick={() => onChange([...parts, { name: `field_${parts.length + 1}`, value: '' }])}>Add text field</button>
        <button type="button" className="btn btn-sm btn-secondary" onClick={() => onChange([...parts, { name: `file_${parts.length + 1}`, filename: '', value: fileSources[0] ? `{{file.${fileSources[0]}}}` : '' }])}>Add file field</button>
      </div>
    </div>
  )
}
