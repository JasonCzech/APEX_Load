import { useEffect, useId, useMemo, useRef, useState } from 'react'

import { useApi } from '../hooks/useApi'
import { ApiError } from '../hooks/apiClient'
import { type Folder } from '../types'
import { canCreateScriptFolders } from '../utils/authAccessUtils'
import { getErrorMessage } from '../utils/errorUtils'
import { brunoDestinationFolderOptions } from './brunoFolderOptions'
import './ImportDestinationFolder.css'

interface Props {
  projectId?: string
  value: string
  onChange: (folderId: string) => void
  /** Locks the destination while a conversion or save is using its value. */
  disabled?: boolean
  /** Lets the import surface fence script operations while a folder is created. */
  onOperationBusyChange?: (busy: boolean) => void
}

type FolderCreationAccess = 'checking' | 'allowed' | 'denied' | 'unavailable'

const FOLDER_CREATION_DENIED_MESSAGE = 'Your current role can import scripts but cannot create project folders. Select an existing script folder or Project root, or ask a Project Administrator to create one for this project.'

/**
 * Shared destination control for imports that may save to Project root.
 *
 * Folder membership is revalidated whenever the project changes so an ID from
 * one project can never be forwarded as the destination for another. Folder
 * creation is an administrative operation and is therefore exposed only after
 * effective access confirms manage_projects.
 */
export default function ImportDestinationFolder({ projectId, value, onChange, disabled = false, onOperationBusyChange }: Props) {
  const api = useApi()
  const [folders, setFolders] = useState<Folder[]>([])
  const [foldersLoadedForProject, setFoldersLoadedForProject] = useState('')
  const [loadingFolders, setLoadingFolders] = useState(false)
  const [loadError, setLoadError] = useState('')
  const [reloadGeneration, setReloadGeneration] = useState(0)
  const [folderCreationAccess, setFolderCreationAccess] = useState<FolderCreationAccess>(projectId ? 'checking' : 'denied')
  const [newFolderName, setNewFolderName] = useState('')
  const [creatingFolder, setCreatingFolder] = useState(false)
  const [createError, setCreateError] = useState('')
  const [notice, setNotice] = useState('')
  const previousProjectIdRef = useRef(projectId)
  const projectScopeRef = useRef({ projectId })
  if (projectScopeRef.current.projectId !== projectId) {
    // Object identity prevents an A → B → A switch from reviving a create
    // request launched during the earlier visit to project A.
    projectScopeRef.current = { projectId }
  }
  const valueRef = useRef(value)
  valueRef.current = value
  const titleId = useId()
  const destinationHelpId = useId()

  useEffect(() => {
    const previousProjectId = previousProjectIdRef.current
    previousProjectIdRef.current = projectId
    if (previousProjectId !== projectId) {
      // A create request from the previous project is no longer allowed to
      // fence the new project's controls. Its continuation is already scoped
      // by previousProjectIdRef and cannot select the stale result.
      setCreatingFolder(false)
      onChange('')
    }

    setFolders([])
    setFoldersLoadedForProject('')
    setLoadError('')
    setNewFolderName('')
    setCreateError('')
    setNotice('')
    if (!projectId) {
      setLoadingFolders(false)
      return
    }

    let cancelled = false
    setLoadingFolders(true)
    void api.listFolders(projectId, 'scripts')
      .then(response => {
        if (cancelled) return
        setFolders(response.folders || [])
        setFoldersLoadedForProject(projectId)
        setLoadError('')
      })
      .catch(cause => {
        if (cancelled) return
        setFolders([])
        setFoldersLoadedForProject('')
        // A folder ID cannot remain trustworthy when its project membership
        // could not be loaded. Reset the controlled owner as well as the
        // visible select so a later save cannot retain a stale destination.
        if (valueRef.current) onChange('')
        setLoadError(getErrorMessage(cause, 'Unable to load destination folders.'))
      })
      .finally(() => {
        if (!cancelled) setLoadingFolders(false)
      })
    return () => { cancelled = true }
  }, [api, onChange, projectId, reloadGeneration])

  useEffect(() => {
    onOperationBusyChange?.(creatingFolder)
  }, [creatingFolder, onOperationBusyChange])

  useEffect(() => () => {
    onOperationBusyChange?.(false)
  }, [onOperationBusyChange])

  useEffect(() => {
    if (!projectId) {
      setFolderCreationAccess('denied')
      return
    }
    let cancelled = false
    setFolderCreationAccess('checking')
    void api.getAuthEffectiveAccess(projectId)
      .then(response => {
        if (cancelled) return
        setFolderCreationAccess(canCreateScriptFolders(response.resources || [])
          ? 'allowed'
          : 'denied')
      })
      .catch(() => {
        if (!cancelled) setFolderCreationAccess('unavailable')
      })
    return () => { cancelled = true }
  }, [api, projectId])

  useEffect(() => {
    if (
      !value
      || !projectId
      || foldersLoadedForProject !== projectId
      || folders.some(folder => folder.id === value)
    ) return
    onChange('')
    setNotice('The previously selected folder is not available in this project. Destination reset to Project root.')
  }, [folders, foldersLoadedForProject, onChange, projectId, value])

  const options = useMemo(() => brunoDestinationFolderOptions(folders), [folders])

  const createDestinationFolder = async () => {
    const folderName = newFolderName.trim()
    const sourceProjectScope = projectScopeRef.current
    const sourceProjectId = sourceProjectScope.projectId
    if (!sourceProjectId || !folderName || disabled || creatingFolder || folderCreationAccess !== 'allowed') return
    setCreatingFolder(true)
    setCreateError('')
    setNotice('')
    try {
      const created = await api.createFolder(sourceProjectId, {
        name: folderName,
        category: 'scripts',
      })
      if (projectScopeRef.current !== sourceProjectScope) return
      setFolders(previous => previous.some(folder => folder.id === created.id)
        ? previous
        : [...previous, created])
      setFoldersLoadedForProject(sourceProjectId)
      onChange(created.id)
      setNewFolderName('')
      setNotice(`Created and selected script folder “${created.name}”.`)
    } catch (cause) {
      if (projectScopeRef.current !== sourceProjectScope) return
      if (cause instanceof ApiError && cause.status === 403) {
        setFolderCreationAccess('denied')
        setCreateError(FOLDER_CREATION_DENIED_MESSAGE)
      } else {
        setCreateError(getErrorMessage(cause, 'Unable to create the destination folder.'))
      }
    } finally {
      if (projectScopeRef.current === sourceProjectScope) setCreatingFolder(false)
    }
  }

  return (
    <section className="import-destination-folder" aria-labelledby={titleId}>
      <div className="import-destination-folder-heading">
        <div>
          <h3 id={titleId}>Save destination</h3>
          <p>Choose where the imported scripts will appear in this project.</p>
        </div>
        <label>
          <span>Destination folder</span>
          <select
            value={value}
            disabled={!projectId || disabled || creatingFolder}
            onChange={event => {
              onChange(event.target.value)
              setNotice('')
            }}
            aria-label="Destination folder for saved scripts"
            aria-describedby={destinationHelpId}
          >
            <option value="">Project root</option>
            {options.map(({ folder, label }) => (
              <option key={folder.id} value={folder.id}>{label}</option>
            ))}
          </select>
        </label>
      </div>

      <p id={destinationHelpId} className="import-destination-folder-guidance" role="status">
        {!projectId
          ? 'Select a project to choose a script destination.'
          : creatingFolder
            ? 'Creating and selecting the folder. Script import actions and destination changes are locked until it finishes.'
          : disabled
            ? 'An import operation is in progress. The destination is locked until it finishes.'
          : loadingFolders
            ? 'Loading script folders… Project root remains the default destination.'
            : options.length === 0 && !loadError
              ? folderCreationAccess === 'allowed'
                ? 'No script folders exist in this project yet. Imports will save to Project root, or create a folder below.'
                : 'No script folders exist in this project. Imports will save to Project root.'
              : value
                ? 'Imported scripts will be saved in the selected script folder.'
                : 'Imported scripts will be saved at Project root.'}
      </p>

      {projectId && folderCreationAccess === 'allowed' && (
        <div className="import-destination-folder-create">
          <label>
            <span>New script folder name</span>
            <input
              className="settings-input"
              value={newFolderName}
              disabled={disabled || creatingFolder}
              onChange={event => {
                setNewFolderName(event.target.value)
                setCreateError('')
              }}
              placeholder="Imported API scripts"
            />
          </label>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={disabled || creatingFolder || newFolderName.trim() === ''}
            onClick={() => { void createDestinationFolder() }}
          >
            {creatingFolder ? 'Creating folder…' : 'Create and select folder'}
          </button>
        </div>
      )}

      {projectId && folderCreationAccess !== 'allowed' && (
        <p className="import-destination-folder-permission" role="status">
          {folderCreationAccess === 'checking'
            ? 'Checking whether this session can create project folders…'
            : folderCreationAccess === 'denied'
              ? FOLDER_CREATION_DENIED_MESSAGE
              : 'Folder-creation permission could not be verified. You can use Project root or an existing folder, or ask a Project Administrator to create one.'}
        </p>
      )}
      {loadError && (
        <p className="import-destination-folder-error" role="alert">
          {loadError}{' '}
          <button type="button" className="btn btn-ghost btn-sm" disabled={disabled} onClick={() => setReloadGeneration(current => current + 1)}>Retry</button>
        </p>
      )}
      {createError && <p className="import-destination-folder-error" role="alert">{createError}</p>}
      {notice && <p className="import-destination-folder-notice" role="status">{notice}</p>}
    </section>
  )
}
