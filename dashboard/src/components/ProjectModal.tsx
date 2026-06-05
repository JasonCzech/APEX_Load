import { useEffect, useState } from 'react'
import './ProjectModal.css'

interface Props {
  title?: string
  initialName?: string
  onClose: () => void
  onSubmit: (name: string) => Promise<void> | void
}

export default function ProjectModal({ title = 'New Project', initialName = '', onClose, onSubmit }: Props) {
  const [name, setName] = useState(initialName)
  const [saving, setSaving] = useState(false)

  useEffect(() => setName(initialName), [initialName])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose()
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!name.trim()) return
    setSaving(true)
    try {
      await onSubmit(name.trim())
      onClose()
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="project-modal-backdrop" role="presentation" onMouseDown={onClose}>
      <form className="project-modal" onSubmit={submit} onMouseDown={(event) => event.stopPropagation()}>
        <div className="project-modal-header">
          <h3>{title}</h3>
          <button type="button" className="project-modal-close" onClick={onClose}>×</button>
        </div>
        <label className="project-modal-field">
          <span>Name</span>
          <input autoFocus value={name} onChange={(event) => setName(event.target.value)} placeholder="Application or service name" />
        </label>
        <div className="project-modal-actions">
          <button type="button" className="btn btn-secondary" onClick={onClose}>Cancel</button>
          <button type="submit" className="btn btn-primary" disabled={!name.trim() || saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </form>
    </div>
  )
}
