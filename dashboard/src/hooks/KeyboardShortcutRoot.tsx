import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'

import { CommandPalette } from '../components/CommandPalette'
import { dialogBackdropProps } from '../components/dialogBackdropProps'
import {
  BUILT_IN_HELP_SHORTCUT,
  BUILT_IN_PALETTE_SHORTCUT,
  GROUP_ORDER,
  KeyboardShortcutContext,
  SEQUENCE_TIMEOUT_MS,
  formatShortcutDisplay,
  matchingShortcutsForEvent,
  noopUnregister,
  parseShortcutSequence,
  shortcutKeys,
  sortShortcuts,
  type KeyboardShortcut,
  type KeyboardShortcutContextValue,
  type ParsedShortcutStep,
  type RegisteredKeyboardShortcut,
} from './KeyboardShortcutProvider'
import { useModalDialog } from './useModalDialog'
import './KeyboardShortcutProvider.css'

export function KeyboardShortcutProvider({ children }: { children: ReactNode }) {
  const [registry, setRegistry] = useState<Record<string, RegisteredKeyboardShortcut>>({})
  const [helpOpen, setHelpOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const sequenceRef = useRef(0)
  const pendingStepsRef = useRef<ParsedShortcutStep[]>([])
  const timeoutRef = useRef<number | null>(null)
  const registeredShortcuts = useMemo(() => Object.values(registry).sort(sortShortcuts), [registry])
  const allShortcuts = useMemo<RegisteredKeyboardShortcut[]>(() => [
    { ...BUILT_IN_HELP_SHORTCUT, onTrigger: () => setHelpOpen(true) },
    { ...BUILT_IN_PALETTE_SHORTCUT, onTrigger: () => setPaletteOpen(open => !open) },
    ...registeredShortcuts,
  ], [registeredShortcuts])
  const clearPendingSequence = () => {
    pendingStepsRef.current = []
    if (timeoutRef.current !== null) { window.clearTimeout(timeoutRef.current); timeoutRef.current = null }
  }
  const registerShortcuts = useMemo(() => ((shortcuts: KeyboardShortcut[]) => {
    if (shortcuts.length === 0) return noopUnregister
    const registrationId = `shortcut-${++sequenceRef.current}`
    setRegistry(current => {
      const next = { ...current }
      shortcuts.forEach((shortcut, index) => { next[`${registrationId}-${shortcut.id}`] = { ...shortcut, sequence: sequenceRef.current * 1000 + index } })
      return next
    })
    return () => setRegistry(current => {
      const next = { ...current }
      for (const key of Object.keys(next)) if (key.startsWith(`${registrationId}-`)) delete next[key]
      return next
    })
  }), [])
  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return
      const matches = matchingShortcutsForEvent(allShortcuts, event, pendingStepsRef.current)
      const selected = matches.exact[0]
      if (selected) {
        if (selected.preventDefault !== false) event.preventDefault()
        clearPendingSequence(); selected.onTrigger(); return
      }
      if (matches.prefixes.length > 0) {
        const firstKeys = shortcutKeys(matches.prefixes[0])[0]
        const nextStep = parseShortcutSequence(firstKeys).steps[pendingStepsRef.current.length]
        pendingStepsRef.current = [...pendingStepsRef.current, nextStep]
        if (timeoutRef.current !== null) window.clearTimeout(timeoutRef.current)
        timeoutRef.current = window.setTimeout(clearPendingSequence, SEQUENCE_TIMEOUT_MS)
        if (matches.prefixes[0].preventDefault !== false) event.preventDefault()
        return
      }
      clearPendingSequence()
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => { window.removeEventListener('keydown', handleKeyDown); clearPendingSequence() }
  }, [allShortcuts])
  const value = useMemo<KeyboardShortcutContextValue>(() => ({ shortcuts: allShortcuts, openHelp: () => setHelpOpen(true), registerShortcuts }), [allShortcuts, registerShortcuts])
  return (
    <KeyboardShortcutContext.Provider value={value}>
      {children}
      {helpOpen && <ShortcutHelpDialog shortcuts={allShortcuts} onClose={() => setHelpOpen(false)} />}
      {paletteOpen && <CommandPalette shortcuts={allShortcuts} onClose={() => setPaletteOpen(false)} />}
    </KeyboardShortcutContext.Provider>
  )
}

function ShortcutHelpDialog({ shortcuts, onClose }: { shortcuts: RegisteredKeyboardShortcut[]; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose })
  const grouped = groupShortcuts(shortcuts.filter(shortcut => !shortcut.hidden))
  return (
    <div className="keyboard-shortcut-backdrop" {...dialogBackdropProps(onClose)}>
      <div ref={dialogRef} className="keyboard-shortcut-dialog" role="dialog" aria-modal="true" aria-labelledby="keyboard-shortcut-title">
        <div className="keyboard-shortcut-header"><div><h2 id="keyboard-shortcut-title">Keyboard Shortcuts</h2><p>Power controls for navigating APEX Load without leaving the keyboard.</p></div><button type="button" className="keyboard-shortcut-close" onClick={onClose} aria-label="Close keyboard shortcuts">Close</button></div>
        <div className="keyboard-shortcut-groups">{grouped.map(([group, entries]) => <section key={group} className="keyboard-shortcut-group" aria-label={`${group} shortcuts`}><h3>{group}</h3><dl>{entries.map(shortcut => <div key={`${shortcut.id}-${formatShortcutDisplay(shortcut.keys)}`} className={shortcut.disabled ? 'disabled' : ''}><dt>{shortcut.label}</dt><dd><kbd>{formatShortcutDisplay(shortcut.keys)}</kbd></dd></div>)}</dl></section>)}</div>
      </div>
    </div>
  )
}

function groupShortcuts(shortcuts: RegisteredKeyboardShortcut[]): Array<[string, RegisteredKeyboardShortcut[]]> {
  const groups = new Map<string, RegisteredKeyboardShortcut[]>()
  for (const shortcut of shortcuts) groups.set(shortcut.group || 'Current Page', [...(groups.get(shortcut.group || 'Current Page') || []), shortcut])
  return Array.from(groups.entries()).sort(([left], [right]) => {
    const leftIndex = GROUP_ORDER.indexOf(left); const rightIndex = GROUP_ORDER.indexOf(right)
    if (leftIndex >= 0 || rightIndex >= 0) return (leftIndex >= 0 ? leftIndex : GROUP_ORDER.length) - (rightIndex >= 0 ? rightIndex : GROUP_ORDER.length)
    return left.localeCompare(right)
  }).map(([group, entries]) => [group, entries.sort(sortShortcuts)])
}
