import { useMemo, useState } from 'react'
import { Folder } from '../types'
import './FolderTree.css'

type FolderCategory = 'scripts' | 'scenarios' | 'results' | 'recordings'

interface TreeItem {
  id: string
  name: string
  folder_id?: string | null
  [key: string]: unknown
}

interface Props {
  title: string
  rootLabel?: string
  headerActions?: JSX.Element
  category: FolderCategory
  folders: Folder[]
  items: TreeItem[]
  selectedFolderId?: string | null
  onSelectFolder: (folderId?: string | null) => void
  selectedItemId?: string | null
  details?: JSX.Element
  onSelectItem?: (id: string) => void
  onCreateFolder: (parentId: string | null) => void
  onRenameFolder: (id: string, name: string) => void
  onDeleteFolder: (id: string) => void
  onMoveItem?: (itemId: string, folderId: string | null) => void
  renderItemExtra?: (item: TreeItem) => JSX.Element | null
}

function folderLabel(category: FolderCategory): string {
  if (category === 'scenarios') return 'scenario'
  if (category === 'results') return 'result'
  if (category === 'recordings') return 'recording'
  return 'script'
}

export default function FolderTree({
  title,
  rootLabel = 'Root',
  headerActions,
  category,
  folders,
  items,
  selectedFolderId,
  onSelectFolder,
  selectedItemId,
  details,
  onSelectItem,
  onCreateFolder,
  onRenameFolder,
  onDeleteFolder,
  onMoveItem,
  renderItemExtra,
}: Props) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  const childrenByParent = useMemo(() => {
    const map = new Map<string, Folder[]>()
    for (const folder of folders) {
      const key = folder.parent_id || 'root'
      if (!map.has(key)) map.set(key, [])
      map.get(key)!.push(folder)
    }
    for (const group of map.values()) {
      group.sort((a, b) => a.name.localeCompare(b.name))
    }
    return map
  }, [folders])

  const toggle = (id: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const renameFolder = (folder: Folder) => {
    const name = window.prompt('Folder name', folder.name)
    if (!name?.trim() || name.trim() === folder.name) return
    onRenameFolder(folder.id, name.trim())
  }

  const deleteFolder = (folder: Folder) => {
    if (!window.confirm(`Delete folder "${folder.name}"? The folder must be empty.`)) return
    onDeleteFolder(folder.id)
  }

  const renderInlineItems = (folderId: string | null, depth: number) => {
    const folderItems = items.filter((item) =>
      folderId === null ? !item.folder_id : item.folder_id === folderId,
    )
    return folderItems.map((item) => {
      const extra = renderItemExtra ? renderItemExtra(item) : null
      return (
        <div key={item.id} className={`folder-tree-item ${selectedItemId === item.id ? 'active' : ''}`} style={{ paddingLeft: 12 + (depth + 1) * 16, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <button className="folder-tree-item-main" onClick={() => onSelectItem?.(item.id)} style={{ flex: 1, minWidth: 0 }}>
            <span className="folder-tree-icon">◻</span>
            <span>{item.name}</span>
          </button>
          {extra && (
            <div style={{ display: 'flex', gap: 4, flexShrink: 0 }}>
              {extra}
            </div>
          )}
          {onMoveItem && (
            <select
              className="folder-tree-move"
              value={item.folder_id || ''}
              onChange={(event) => onMoveItem(item.id, event.target.value || null)}
              onClick={(event) => event.stopPropagation()}
            >
              <option value="">{rootLabel}</option>
              {folders.map((folder) => (
                <option key={folder.id} value={folder.id}>{folder.name}</option>
              ))}
            </select>
          )}
        </div>
      )
    })
  }

  const renderFolder = (folder: Folder, depth = 0): JSX.Element => {
    const childFolders = childrenByParent.get(folder.id) || []
    const childCount = childFolders.length
    const isExpanded = expanded.has(folder.id)
    const itemCount = items.filter((item) => item.folder_id === folder.id).length
    const hasChildren = childCount > 0 || itemCount > 0

    return (
      <div key={folder.id}>
        <div className={`folder-tree-row folder ${selectedFolderId === folder.id ? 'active' : ''}`} style={{ paddingLeft: 12 + depth * 16 }}>
          <button className="folder-tree-chevron" onClick={() => toggle(folder.id)} disabled={!hasChildren}>
            {hasChildren ? (isExpanded ? '⌄' : '›') : ''}
          </button>
          <button className="folder-tree-label" onClick={() => onSelectFolder(folder.id)}>
            <span className="folder-tree-icon">▰</span>
            <span>{folder.name}</span>
            <span className="folder-tree-count">{itemCount}</span>
          </button>
          <button className="folder-tree-action" title="New subfolder" onClick={() => onCreateFolder(folder.id)}>+</button>
          <button className="folder-tree-action" title="Rename folder" onClick={() => renameFolder(folder)}>Rename</button>
          <button className="folder-tree-action danger" title="Delete folder" onClick={() => deleteFolder(folder)}>Delete</button>
        </div>
        {isExpanded && (
          <>
            {childFolders.map((child) => renderFolder(child, depth + 1))}
            {renderInlineItems(folder.id, depth)}
          </>
        )}
      </div>
    )
  }

  const rootItemCount = items.filter((item) => !item.folder_id).length

  return (
    <div className="folder-tree">
      <div className="folder-tree-header">
        <div>
          <h3>{title}</h3>
          <span>{folders.length} folders · {items.length} {folderLabel(category)}{items.length === 1 ? '' : 's'}</span>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          {headerActions}
          <button className="btn btn-sm btn-secondary" onClick={() => onCreateFolder(selectedFolderId || null)}>
            + Folder
          </button>
        </div>
      </div>

      <div className={details ? 'folder-layout' : ''}>
        <div className="folder-tree-section">
          <button className={`folder-tree-row scope ${selectedFolderId === undefined ? 'active' : ''}`} onClick={() => onSelectFolder(undefined)}>
            <span className="folder-tree-icon">⌂</span>
            <span>{rootLabel}</span>
            <span className="folder-tree-count">{items.length}</span>
          </button>
          {childrenByParent.get('root')?.map((folder) => renderFolder(folder))}
          {rootItemCount === 0 && items.length === 0 && (
            <div className="folder-tree-empty">No {folderLabel(category)}s here yet.</div>
          )}
          {renderInlineItems(null, 0)}
        </div>

        {details && (
          <div className="folder-tree-details">
            {details}
          </div>
        )}
      </div>
    </div>
  )
}
