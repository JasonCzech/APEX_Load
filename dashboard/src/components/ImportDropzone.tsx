import { useRef, useState, type DragEvent } from 'react'

import './ImportDropzone.css'

interface Props {
  accept: string
  file: File | null
  onFile: (file: File | null) => void
  dropHint: string
  // Optional validation: return an error message to reject the dropped file.
  validate?: (file: File) => string | null
  onError?: (message: string) => void
}

// ImportDropzone is the shared drag-drop file picker for the import wizards,
// matching the JMeter/Enterprise Archive dropzone interaction: drop or click
// to browse, with file name + size and a change button once selected.
export default function ImportDropzone({ accept, file, onFile, dropHint, validate, onError }: Props) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragOver, setDragOver] = useState(false)

  const acceptFile = (next?: File | null) => {
    if (!next) return
    if (validate) {
      const error = validate(next)
      if (error) {
        onError?.(error)
        return
      }
    }
    onFile(next)
  }

  const handleDrop = (event: DragEvent) => {
    event.preventDefault()
    setDragOver(false)
    acceptFile(event.dataTransfer.files?.[0])
  }

  return (
    <div
      className={`import-dropzone ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''}`}
      onDragOver={event => { event.preventDefault(); setDragOver(true) }}
      onDragLeave={() => setDragOver(false)}
      onDrop={handleDrop}
      onClick={() => inputRef.current?.click()}
      role="button"
      tabIndex={0}
      onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') inputRef.current?.click() }}
    >
      <input
        ref={inputRef}
        type="file"
        accept={accept}
        onChange={event => acceptFile(event.target.files?.[0])}
        hidden
      />
      {file ? (
        <div className="import-dropzone-file">
          <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="var(--success)" strokeWidth="2" aria-hidden>
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
            <polyline points="14 2 14 8 20 8" />
          </svg>
          <div>
            <span className="import-dropzone-name">{file.name}</span>
            <span className="import-dropzone-size">{(file.size / 1024).toFixed(1)} KB</span>
          </div>
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            onClick={event => { event.stopPropagation(); onFile(null) }}
          >
            Change
          </button>
        </div>
      ) : (
        <div className="import-dropzone-empty">
          <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.5" aria-hidden>
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="17 8 12 3 7 8" />
            <line x1="12" y1="3" x2="12" y2="15" />
          </svg>
          <p>{dropHint}</p>
          <span className="import-dropzone-hint">or click to browse</span>
        </div>
      )}
    </div>
  )
}
