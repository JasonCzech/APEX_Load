import { createContext, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import './KeyboardShortcutProvider.css'
import { useModalDialog } from './useModalDialog'
import { CommandPalette } from '../components/CommandPalette'

export type KeyboardShortcutGroup = 'Global' | 'Navigation' | 'Current Page' | 'Script Editor' | 'Test Run' | 'Workspace' | string

export interface KeyboardShortcut {
  id: string
  label: string
  keys: string | string[]
  group?: KeyboardShortcutGroup
  description?: string
  disabled?: boolean
  hidden?: boolean
  allowInEditable?: boolean
  preventDefault?: boolean
  priority?: number
  onTrigger: () => void
}

export interface RegisteredKeyboardShortcut extends KeyboardShortcut {
  sequence: number
}

interface KeyboardShortcutContextValue {
  shortcuts: RegisteredKeyboardShortcut[]
  openHelp: () => void
  registerShortcuts: (shortcuts: KeyboardShortcut[]) => () => void
}

export interface ParsedShortcutStep {
  key: string
  alt: boolean
  ctrl: boolean
  meta: boolean
  mod: boolean
  shift: boolean
  shiftExplicit: boolean
}

export interface ParsedShortcutSequence {
  display: string
  steps: ParsedShortcutStep[]
}

const SEQUENCE_TIMEOUT_MS = 900
const KeyboardShortcutContext = createContext<KeyboardShortcutContextValue | null>(null)

const BUILT_IN_HELP_SHORTCUT: RegisteredKeyboardShortcut = {
  id: 'global.help',
  label: 'Show keyboard shortcuts',
  keys: '?',
  group: 'Global',
  priority: 1000,
  sequence: 0,
  onTrigger: () => undefined,
}

const BUILT_IN_PALETTE_SHORTCUT: RegisteredKeyboardShortcut = {
  id: 'global.command-palette',
  label: 'Open command palette',
  keys: ['mod+k', 'ctrl+shift+p'],
  group: 'Global',
  priority: 1001,
  sequence: 0,
  allowInEditable: true,
  onTrigger: () => undefined,
}

const GROUP_ORDER = ['Global', 'Navigation', 'Current Page', 'Script Editor', 'Test Run', 'Workspace']

function noopUnregister() {
  return undefined
}

function isMacLike() {
  if (typeof navigator === 'undefined') return false
  return /mac|iphone|ipad|ipod/i.test(navigator.platform || navigator.userAgent)
}

function normalizeDisplayKey(part: string, macLike = isMacLike()) {
  const key = part.trim()
  const lower = key.toLowerCase()
  if (lower === 'cmd' || lower === 'meta') return macLike ? 'Cmd' : 'Meta'
  if (lower === 'ctrl' || lower === 'control') return 'Ctrl'
  if (lower === 'cmd/ctrl' || lower === 'ctrl/cmd' || lower === 'mod') return macLike ? 'Cmd' : 'Ctrl'
  if (lower === 'shift') return 'Shift'
  if (lower === 'alt' || lower === 'option') return macLike ? 'Option' : 'Alt'
  if (lower === 'plus') return '+'
  if (lower.length === 1) return lower.toUpperCase()
  if (lower === 'escape') return 'Esc'
  if (lower === 'backspace') return 'Backspace'
  if (lower === 'enter') return 'Enter'
  if (lower === 'space') return 'Space'
  return key.charAt(0).toUpperCase() + key.slice(1)
}

export function formatShortcutDisplay(keys: string | string[] | undefined, macLike = isMacLike()): string {
  const first = Array.isArray(keys) ? keys[0] : keys
  if (!first) return ''
  return first
    .trim()
    .split(/\s+/)
    .map(step => step.split('+').map(part => normalizeDisplayKey(part, macLike)).join('+'))
    .join(' ')
}

export function isEditableShortcutTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  if (target.closest('[contenteditable="true"]')) return true

  const tagName = target.tagName
  if (tagName === 'TEXTAREA' || tagName === 'SELECT') return true
  if (tagName !== 'INPUT') return false

  const type = (target as HTMLInputElement).type.toLowerCase()
  return !['button', 'checkbox', 'color', 'file', 'hidden', 'image', 'radio', 'range', 'reset', 'submit'].includes(type)
}

export function parseShortcutSequence(shortcut: string): ParsedShortcutSequence {
  return {
    display: shortcut,
    steps: shortcut.trim().split(/\s+/).filter(Boolean).map(parseShortcutStep),
  }
}

function parseShortcutStep(step: string): ParsedShortcutStep {
  const parsed: ParsedShortcutStep = {
    key: '',
    alt: false,
    ctrl: false,
    meta: false,
    mod: false,
    shift: false,
    shiftExplicit: false,
  }

  for (const rawPart of step.split('+')) {
    const part = rawPart.trim()
    const lower = part.toLowerCase()
    if (!lower) continue
    if (lower === 'cmd' || lower === 'meta') {
      parsed.meta = true
    } else if (lower === 'ctrl' || lower === 'control') {
      parsed.ctrl = true
    } else if (lower === 'cmd/ctrl' || lower === 'ctrl/cmd' || lower === 'mod') {
      parsed.mod = true
    } else if (lower === 'shift') {
      parsed.shift = true
      parsed.shiftExplicit = true
    } else if (lower === 'alt' || lower === 'option') {
      parsed.alt = true
    } else if (lower === 'space') {
      parsed.key = ' '
    } else if (lower === 'plus') {
      parsed.key = '+'
    } else {
      parsed.key = lower
    }
  }

  if (!parsed.key) {
    parsed.key = step.trim().toLowerCase()
  }
  return parsed
}

function shortcutKeys(shortcut: KeyboardShortcut): string[] {
  return (Array.isArray(shortcut.keys) ? shortcut.keys : [shortcut.keys]).filter(Boolean)
}

function eventMatchesStep(event: KeyboardEvent, step: ParsedShortcutStep): boolean {
  const eventKey = normalizeEventKey(event.key)
  if (eventKey !== step.key) return false
  if (step.mod) {
    if (!(event.ctrlKey || event.metaKey)) return false
  } else {
    if (event.ctrlKey !== step.ctrl) return false
    if (event.metaKey !== step.meta) return false
  }
  if (event.altKey !== step.alt) return false
  if (step.shiftExplicit) {
    return event.shiftKey === step.shift
  }
  if (step.key.length === 1 && /^[a-z0-9]$/.test(step.key)) {
    return event.shiftKey === false
  }
  return true
}

function normalizeEventKey(key: string) {
  if (key === 'Esc') return 'escape'
  if (key === ' ') return ' '
  return key.toLowerCase()
}

function compareSteps(left: ParsedShortcutStep, right: ParsedShortcutStep) {
  return left.key === right.key &&
    left.alt === right.alt &&
    left.ctrl === right.ctrl &&
    left.meta === right.meta &&
    left.mod === right.mod &&
    left.shift === right.shift &&
    left.shiftExplicit === right.shiftExplicit
}

function isShortcutEnabled(shortcut: RegisteredKeyboardShortcut, event: KeyboardEvent) {
  if (shortcut.hidden || shortcut.disabled) return false
  if (!shortcut.allowInEditable && isEditableShortcutTarget(event.target)) return false
  return true
}

function sortShortcuts(left: RegisteredKeyboardShortcut, right: RegisteredKeyboardShortcut) {
  const priorityDelta = (right.priority || 0) - (left.priority || 0)
  if (priorityDelta !== 0) return priorityDelta
  return right.sequence - left.sequence
}

export function matchingShortcutsForEvent(
  shortcuts: RegisteredKeyboardShortcut[],
  event: KeyboardEvent,
  pendingSteps: ParsedShortcutStep[] = [],
): { exact: RegisteredKeyboardShortcut[]; prefixes: RegisteredKeyboardShortcut[] } {
  const exact: RegisteredKeyboardShortcut[] = []
  const prefixes: RegisteredKeyboardShortcut[] = []
  const stepsSoFar = pendingSteps

  for (const shortcut of shortcuts) {
    if (!isShortcutEnabled(shortcut, event)) continue

    for (const keys of shortcutKeys(shortcut)) {
      const parsed = parseShortcutSequence(keys)
      if (parsed.steps.length <= stepsSoFar.length) continue
      if (!stepsSoFar.every((step, index) => compareSteps(step, parsed.steps[index]))) continue
      if (!eventMatchesStep(event, parsed.steps[stepsSoFar.length])) continue

      if (parsed.steps.length === stepsSoFar.length + 1) {
        exact.push(shortcut)
      } else {
        prefixes.push(shortcut)
      }
    }
  }

  return {
    exact: exact.sort(sortShortcuts),
    prefixes: prefixes.sort(sortShortcuts),
  }
}

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
    if (timeoutRef.current !== null) {
      window.clearTimeout(timeoutRef.current)
      timeoutRef.current = null
    }
  }

  const registerShortcuts = useMemo(() => (
    (shortcuts: KeyboardShortcut[]) => {
      if (shortcuts.length === 0) return noopUnregister
      const registrationId = `shortcut-${++sequenceRef.current}`
      setRegistry(current => {
        const next = { ...current }
        shortcuts.forEach((shortcut, index) => {
          next[`${registrationId}-${shortcut.id}`] = {
            ...shortcut,
            sequence: sequenceRef.current * 1000 + index,
          }
        })
        return next
      })
      return () => {
        setRegistry(current => {
          const next = { ...current }
          for (const key of Object.keys(next)) {
            if (key.startsWith(`${registrationId}-`)) {
              delete next[key]
            }
          }
          return next
        })
      }
    }
  ), [])

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented) return

      const matches = matchingShortcutsForEvent(allShortcuts, event, pendingStepsRef.current)
      const selected = matches.exact[0]
      if (selected) {
        if (selected.preventDefault !== false) event.preventDefault()
        clearPendingSequence()
        selected.onTrigger()
        return
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
    return () => {
      window.removeEventListener('keydown', handleKeyDown)
      clearPendingSequence()
    }
  }, [allShortcuts])

  const value = useMemo<KeyboardShortcutContextValue>(() => ({
    shortcuts: allShortcuts,
    openHelp: () => setHelpOpen(true),
    registerShortcuts,
  }), [allShortcuts, registerShortcuts])

  return (
    <KeyboardShortcutContext.Provider value={value}>
      {children}
      {helpOpen && <ShortcutHelpDialog shortcuts={allShortcuts} onClose={() => setHelpOpen(false)} />}
      {paletteOpen && <CommandPalette shortcuts={allShortcuts} onClose={() => setPaletteOpen(false)} />}
    </KeyboardShortcutContext.Provider>
  )
}

export function useKeyboardShortcuts(shortcuts: KeyboardShortcut[] | null | undefined) {
  const context = useContext(KeyboardShortcutContext)
  const registerShortcuts = context?.registerShortcuts

  useEffect(() => {
    if (!registerShortcuts || !shortcuts || shortcuts.length === 0) return undefined
    return registerShortcuts(shortcuts)
  }, [registerShortcuts, shortcuts])
}

export function useKeyboardShortcutHelp() {
  const context = useContext(KeyboardShortcutContext)
  return context?.openHelp || (() => undefined)
}

function ShortcutHelpDialog({ shortcuts, onClose }: { shortcuts: RegisteredKeyboardShortcut[]; onClose: () => void }) {
  const dialogRef = useRef<HTMLDivElement | null>(null)
  useModalDialog({ dialogRef, onClose })
  const visibleShortcuts = shortcuts.filter(shortcut => !shortcut.hidden)
  const grouped = groupShortcuts(visibleShortcuts)

  return (
    <div className="keyboard-shortcut-backdrop" role="presentation" onMouseDown={onClose}>
      <div
        ref={dialogRef}
        className="keyboard-shortcut-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="keyboard-shortcut-title"
        onMouseDown={event => event.stopPropagation()}
      >
        <div className="keyboard-shortcut-header">
          <div>
            <h2 id="keyboard-shortcut-title">Keyboard Shortcuts</h2>
            <p>Power controls for navigating APEX Load without leaving the keyboard.</p>
          </div>
          <button type="button" className="keyboard-shortcut-close" onClick={onClose} aria-label="Close keyboard shortcuts">
            Close
          </button>
        </div>
        <div className="keyboard-shortcut-groups">
          {grouped.map(([group, groupShortcuts]) => (
            <section key={group} className="keyboard-shortcut-group" aria-label={`${group} shortcuts`}>
              <h3>{group}</h3>
              <dl>
                {groupShortcuts.map(shortcut => (
                  <div key={`${shortcut.id}-${formatShortcutDisplay(shortcut.keys)}`} className={shortcut.disabled ? 'disabled' : ''}>
                    <dt>{shortcut.label}</dt>
                    <dd>
                      <kbd>{formatShortcutDisplay(shortcut.keys)}</kbd>
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </div>
    </div>
  )
}

function groupShortcuts(shortcuts: RegisteredKeyboardShortcut[]): Array<[string, RegisteredKeyboardShortcut[]]> {
  const groups = new Map<string, RegisteredKeyboardShortcut[]>()
  for (const shortcut of shortcuts) {
    const group = shortcut.group || 'Current Page'
    const current = groups.get(group) || []
    current.push(shortcut)
    groups.set(group, current)
  }

  return Array.from(groups.entries())
    .sort(([left], [right]) => {
      const leftIndex = GROUP_ORDER.indexOf(left)
      const rightIndex = GROUP_ORDER.indexOf(right)
      if (leftIndex >= 0 || rightIndex >= 0) {
        return (leftIndex >= 0 ? leftIndex : GROUP_ORDER.length) - (rightIndex >= 0 ? rightIndex : GROUP_ORDER.length)
      }
      return left.localeCompare(right)
    })
    .map(([group, groupShortcuts]) => [group, groupShortcuts.sort(sortShortcuts)])
}
