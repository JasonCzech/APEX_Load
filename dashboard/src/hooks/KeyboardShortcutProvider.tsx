import { createContext, useContext, useEffect } from 'react'

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

export interface KeyboardShortcutContextValue {
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

export const SEQUENCE_TIMEOUT_MS = 900
export const KeyboardShortcutContext = createContext<KeyboardShortcutContextValue | null>(null)

export const BUILT_IN_HELP_SHORTCUT: RegisteredKeyboardShortcut = {
  id: 'global.help',
  label: 'Show keyboard shortcuts',
  keys: '?',
  group: 'Global',
  priority: 1000,
  sequence: 0,
  onTrigger: () => undefined,
}

export const BUILT_IN_PALETTE_SHORTCUT: RegisteredKeyboardShortcut = {
  id: 'global.command-palette',
  label: 'Open command palette',
  keys: ['mod+k', 'ctrl+shift+p'],
  group: 'Global',
  priority: 1001,
  sequence: 0,
  allowInEditable: true,
  onTrigger: () => undefined,
}

export const GROUP_ORDER = ['Global', 'Navigation', 'Current Page', 'Script Editor', 'Test Run', 'Workspace']

export function noopUnregister() {
  return undefined
}

export function isMacLike() {
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
  if (lower === 'arrowleft') return '←'
  if (lower === 'arrowright') return '→'
  if (lower === 'arrowup') return '↑'
  if (lower === 'arrowdown') return '↓'
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

const NON_TEXT_INPUT_TYPES = new Set(['button', 'checkbox', 'color', 'file', 'hidden', 'image', 'radio', 'range', 'reset', 'submit'])

// isEditableShortcutTarget reports whether keys typed at target belong to a
// field, so page shortcuts stay out of it. With textEditing, only a field the
// key can edit counts: not a select, nor a read-only or disabled field.
export function isEditableShortcutTarget(target: EventTarget | null, { textEditing = false }: { textEditing?: boolean } = {}): boolean {
  if (typeof HTMLElement === 'undefined' || !(target instanceof HTMLElement)) return false
  if (target.isContentEditable) return true
  if (target.closest('[contenteditable=""], [contenteditable="true"], [contenteditable="plaintext-only"]')) return true

  if (target instanceof HTMLSelectElement) return !textEditing
  const editable = !textEditing || !((target as HTMLInputElement).readOnly || (target as HTMLInputElement).disabled)
  if (target instanceof HTMLTextAreaElement) return editable
  if (!(target instanceof HTMLInputElement)) return false
  return editable && !NON_TEXT_INPUT_TYPES.has(target.type.toLowerCase())
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

export function shortcutKeys(shortcut: KeyboardShortcut): string[] {
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

export function sortShortcuts(left: RegisteredKeyboardShortcut, right: RegisteredKeyboardShortcut) {
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
