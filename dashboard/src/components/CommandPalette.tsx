import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { useModalDialog } from '../hooks/useModalDialog'
import { formatShortcutDisplay, type RegisteredKeyboardShortcut } from '../hooks/KeyboardShortcutProvider'
import './CommandPalette.css'

interface CommandPaletteProps {
  shortcuts: RegisteredKeyboardShortcut[]
  onClose: () => void
}

// Commands are surfaced from the keyboard-shortcut registry so every registered
// verb (Run, Step, navigation, authoring) is reachable from one keyboard-first
// search. Results are sorted by stable label — not fuzzy rank — so positions stay
// learnable across sessions (matching VS Code's command-palette behaviour).
export function CommandPalette({ shortcuts, onClose }: CommandPaletteProps) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  const inputRef = useRef<HTMLInputElement | null>(null)
  useModalDialog({ dialogRef, onClose })
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)

  const commands = useMemo(() => {
    const available = shortcuts
      .filter(shortcut => !shortcut.hidden && !shortcut.disabled && shortcut.id !== 'global.command-palette')
      .sort((left, right) => left.label.localeCompare(right.label))
    const needle = query.trim().toLowerCase()
    if (!needle) return available
    return available.filter(shortcut =>
      shortcut.label.toLowerCase().includes(needle)
      || (shortcut.group || '').toLowerCase().includes(needle)
      || (shortcut.description || '').toLowerCase().includes(needle),
    )
  }, [shortcuts, query])

  useEffect(() => { setActiveIndex(0) }, [query])
  useEffect(() => { inputRef.current?.focus() }, [])

  const runCommand = (command?: RegisteredKeyboardShortcut) => {
    if (!command) return
    onClose()
    // Defer so the palette unmounts before the action runs — the action may open
    // another dialog or move focus, which should not fight the closing palette.
    window.setTimeout(() => command.onTrigger(), 0)
  }

  const handleKeyDown = (event: ReactKeyboardEvent) => {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setActiveIndex(index => commands.length === 0 ? 0 : Math.min(index + 1, commands.length - 1))
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setActiveIndex(index => Math.max(index - 1, 0))
    } else if (event.key === 'Enter') {
      event.preventDefault()
      runCommand(commands[activeIndex])
    }
  }

  return (
    <div className="command-palette-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        ref={dialogRef}
        className="command-palette"
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        onMouseDown={event => event.stopPropagation()}
      >
        <input
          ref={inputRef}
          className="command-palette-input"
          type="text"
          placeholder="Type a command…"
          value={query}
          onChange={event => setQuery(event.target.value)}
          onKeyDown={handleKeyDown}
          role="combobox"
          aria-autocomplete="list"
          aria-expanded="true"
          aria-label="Command search"
          aria-controls="command-palette-list"
          aria-activedescendant={commands[activeIndex] ? `command-palette-option-${activeIndex}` : undefined}
        />
        <ul className="command-palette-list" id="command-palette-list" role="listbox">
          {commands.length === 0 ? (
            <li className="command-palette-empty" role="presentation"><span role="status">No matching commands.</span></li>
          ) : (
            commands.map((command, index) => (
              <li
                key={`${command.id}-${command.sequence}`}
                id={`command-palette-option-${index}`}
                role="option"
                aria-selected={index === activeIndex}
                className={`command-palette-item ${index === activeIndex ? 'active' : ''}`}
                onMouseEnter={() => setActiveIndex(index)}
                onMouseDown={event => { event.preventDefault(); runCommand(command) }}
              >
                <span className="command-palette-item-main">
                  <span className="command-palette-item-label">{command.label}</span>
                  {command.group && <span className="command-palette-item-group">{command.group}</span>}
                </span>
                {command.keys && <kbd>{formatShortcutDisplay(command.keys)}</kbd>}
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  )
}
