import { useEffect, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { type Folder } from '../types'
import { getErrorMessage } from '../utils/errorUtils'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'

interface Props {
  projectId?: string
  value: string
  onChange: (folderId: string) => void
}

/**
 * ImportDestinationFolder is the supplier for the folderId that the import,
 * recorder, and archive surfaces already accept.
 *
 * Those surfaces have taken a destination folder for a while, but nothing ever
 * passed one: ScriptsAuthoringShell did not supply it and FolderTree is mounted
 * nowhere, so every import and recording silently saved to the project root
 * regardless of the plumbing. This control is what makes that plumbing do
 * something.
 *
 * Unlike BrunoImport's own picker, which gates saving until a folder is chosen,
 * this one defaults to the project root. Requiring a choice here would block
 * formats that have always saved without one.
 */
export default function ImportDestinationFolder({ projectId, value, onChange }: Props) {
  const api = useApi()
  const [folders, setFolders] = useState<Folder[]>([])
  const [loadError, setLoadError] = useState('')

  useEffect(() => {
    if (!projectId) {
      setFolders([])
      setLoadError('')
      return
    }
    let cancelled = false
    void api.listFolders(projectId, 'scripts')
      .then(response => {
        if (cancelled) return
        setFolders(response.folders || [])
        setLoadError('')
      })
      .catch(cause => {
        if (cancelled) return
        setFolders([])
        setLoadError(getErrorMessage(cause, 'Unable to load destination folders.'))
      })
    return () => { cancelled = true }
  }, [api, projectId])

  // With no folders defined there is no choice to offer, and an empty dropdown
  // reads as something being broken rather than as an absent feature.
  if (!projectId || (folders.length === 0 && !loadError)) return null

  const options = brunoDestinationFolderOptions(folders)

  return (
    <div className="import-destination-folder">
      <label>
        <span>Destination folder</span>
        <select
          value={value}
          onChange={event => onChange(event.target.value)}
          aria-label="Destination folder for saved scripts"
        >
          <option value="">Project root</option>
          {options.map(({ folder, label }) => (
            <option key={folder.id} value={folder.id}>{label}</option>
          ))}
        </select>
      </label>
      {loadError && <p className="import-destination-folder-error" role="alert">{loadError}</p>}
    </div>
  )
}
