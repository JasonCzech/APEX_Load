import { useCallback, useRef, useState, type CSSProperties, type DragEvent } from 'react'

import { isDesktopSurface } from '../desktop/desktopBridge'
import { canUseNativeDialogs, filesFromDroppedPaths, nativeFileReadErrorMessage, pickFilesViaBridge, shouldUseBrowserFileFallback } from '../desktop/desktopFiles'
import { useDesktopFileDrop } from '../desktop/useDesktopFileDrop'
import DropIngestOverlay from './DropIngestOverlay'
import { useDropIngestFeedback } from './dropIngestFeedback'
import './ImportDropzone.css'

interface Props {
  accept: string
  file: File | null
  // Returns false when the host did not take the file now (it refused it, or
  // asks first whether to discard the work the file would replace); the zone
  // then reports nothing added.
  onFile: (file: File | null) => boolean | void | Promise<unknown>
  // Optional 2-file handler (e.g. primary + comparison HAR captures dropped or picked together).
  onPairFiles?: (primary: File, secondary: File) => boolean | void
  dropHint: string
  // Optional validation: return an error message to reject the dropped file.
  validate?: (file: File) => string | null
  onError?: (message: string) => void
  disabled?: boolean
  // Set while the importer works on what the zone was given — validating,
  // inspecting or parsing it on the server — so the zone itself shows that it is
  // in progress (e.g. "Inspecting spec.yaml…", from describeWorking). The host
  // owns it: it sets the label when the work starts and clears it when it ends.
  // That includes importers that act on a drop at once and never hold the file.
  workingLabel?: string
}

// ImportDropzone is the shared drag-drop file picker for the import wizards,
// matching the JMeter/Enterprise Archive dropzone interaction: drop or click
// to browse, with file name + size and a change button once selected.
//
// In the desktop shell it prefers the native open dialog and accepts native file
// drops (which carry real absolute paths via Wails OnFileDrop), reconstructing a
// File for the existing upload path. In the browser it uses the standard
// <input type=file> + HTML5 drag-drop.
export default function ImportDropzone({ accept, file, onFile, onPairFiles, dropHint, validate, onError, disabled = false, workingLabel }: Props) {
  const inputRef = useRef<HTMLInputElement>(null)
  const zoneRef = useRef<HTMLDivElement>(null)
  const disabledRef = useRef(disabled)
  disabledRef.current = disabled
  const [dragOver, setDragOver] = useState(false)
  const [browserFallback, setBrowserFallback] = useState(false)
  // Stable callbacks — destructured so the native drop handler keeps its identity
  // (it is a useDesktopFileDrop dependency and would re-register every render).
  const { phase: ingestPhase, label: ingestLabel, hostClassName, begin: beginIngest, succeed: succeedIngest, fail: failIngest } = useDropIngestFeedback({ workingLabel })

  const desktop = isDesktopSurface()
  const nativeDialogs = desktop && canUseNativeDialogs() && !browserFallback

  const acceptFile = useCallback((next?: File | null): boolean => {
    // A native picker can resolve after a parent save has started. Read the
    // current lock instead of the callback's render-time `disabled` value.
    if (disabledRef.current) return false
    if (!next) return false
    if (validate) {
      const error = validate(next)
      if (error) {
        onError?.(error)
        return false
      }
    }
    onError?.('')
    return onFile(next) !== false
  }, [validate, onError, onFile])

  const acceptFiles = useCallback((incoming: ArrayLike<File> | null | undefined): string | null => {
    if (disabledRef.current || !incoming || incoming.length === 0) return null
    if (incoming.length > 1 && onPairFiles) {
      if (incoming.length > 2) {
        onError?.('Drop at most two HAR captures (primary and comparison).')
        return null
      }
      const primary = incoming[0]
      const secondary = incoming[1]
      if (!primary || !secondary) return null
      if (validate) {
        const primaryError = validate(primary)
        if (primaryError) {
          onError?.(primaryError)
          return null
        }
        const secondaryError = validate(secondary)
        if (secondaryError) {
          onError?.(secondaryError)
          return null
        }
      }
      onError?.('')
      if (onPairFiles(primary, secondary) === false) return null
      return `Added ${primary.name} + ${secondary.name}`
    }
    if (acceptFile(incoming[0])) {
      return `Added ${incoming[0].name}`
    }
    return null
  }, [acceptFile, onError, onPairFiles, validate])

  // Native drop (desktop): Wails delivers absolute paths; reconstruct a File.
  const handleNativePaths = useCallback((paths: string[]) => {
    // A busy or locked zone refuses the drop, as the HTML5 path does, so its work chip stays.
    if (disabledRef.current) {
      setDragOver(false)
      return
    }
    beginIngest()
    void filesFromDroppedPaths(paths).then(files => {
      setDragOver(false)
      const label = acceptFiles(files)
      if (label) succeedIngest(label)
      else failIngest()
    }).catch(error => {
      setDragOver(false)
      failIngest()
      onError?.(nativeFileReadErrorMessage(error))
      if (shouldUseBrowserFileFallback(error)) setBrowserFallback(true)
    })
  }, [acceptFiles, onError, beginIngest, succeedIngest, failIngest])
  useDesktopFileDrop(zoneRef, handleNativePaths, desktop)

  const handleBrowse = useCallback(() => {
    if (disabledRef.current) return
    if (nativeDialogs) {
      const picked = onPairFiles ? pickFilesViaBridge(accept, true) : pickFilesViaBridge(accept)
      void picked.then(files => {
        if (files.length > 0) {
          acceptFiles(files)
        }
      }).catch(error => {
        onError?.(nativeFileReadErrorMessage(error))
        if (shouldUseBrowserFileFallback(error)) {
          setBrowserFallback(true)
          inputRef.current?.click()
        }
      })
      return
    }
    inputRef.current?.click()
  }, [nativeDialogs, accept, acceptFiles, onError, onPairFiles])

  const handleDrop = (event: DragEvent) => {
    event.preventDefault()
    setDragOver(false)
    if (disabledRef.current) return
    // In the desktop shell, file drops arrive via the native OnFileDrop router
    // (dataTransfer carries no usable path); ignore the HTML5 drop there.
    if (desktop) return
    const droppedList = event.dataTransfer.files
    if (!droppedList || droppedList.length === 0) return
    // The File is available synchronously here, so the feedback hook holds the
    // "reading" state briefly — otherwise a drop lands with no visible change.
    beginIngest()
    const label = acceptFiles(droppedList)
    if (label) succeedIngest(label)
    else failIngest()
  }

  // The CSS marker registers this element as a Wails native drop target.
  const dropTargetStyle = desktop ? ({ '--wails-drop-target': 'drop' } as CSSProperties) : undefined

  return (
    <div
      ref={zoneRef}
      className={`import-dropzone ${hostClassName} ${dragOver ? 'drag-over' : ''} ${file ? 'has-file' : ''} ${disabled ? 'disabled' : ''}`}
      style={dropTargetStyle}
      onDragOver={event => { event.preventDefault(); if (!disabled) setDragOver(true) }}
      onDragLeave={() => setDragOver(false)}
      onDrop={handleDrop}
      onClick={file ? undefined : handleBrowse}
      role={file ? 'group' : 'button'}
      tabIndex={disabled || file ? -1 : 0}
      aria-disabled={disabled}
      // Named explicitly (as the JMeter/archive dropzones are) so the transient
      // drop-status text inside the zone cannot leak into its accessible name.
      aria-label={file ? `Selected file ${file.name}` : dropHint}
      onKeyDown={event => {
        if (disabled || file || event.target !== event.currentTarget) return
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          handleBrowse()
        }
      }}
    >
      <DropIngestOverlay phase={ingestPhase} label={ingestLabel} />
      <input
        ref={inputRef}
        type="file"
        accept={accept}
        multiple={Boolean(onPairFiles)}
        disabled={disabled}
        onClick={event => event.stopPropagation()}
        onChange={event => {
          acceptFiles(event.target.files)
          event.target.value = ''
        }}
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
            disabled={disabled}
            onClick={event => {
              event.stopPropagation()
              handleBrowse()
            }}
          >
            Change
          </button>
          <button
            type="button"
            className="btn btn-sm btn-ghost"
            disabled={disabled}
            onClick={() => {
              if (disabledRef.current) return
              onError?.('')
              onFile(null)
              if (inputRef.current) inputRef.current.value = ''
            }}
          >
            Remove
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
          <span className="import-dropzone-hint">{nativeDialogs ? 'or click to choose a file' : 'or click to browse'}</span>
        </div>
      )}
    </div>
  )
}
