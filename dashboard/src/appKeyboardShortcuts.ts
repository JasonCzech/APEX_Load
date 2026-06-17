import type { KeyboardShortcut } from './hooks/KeyboardShortcutProvider'
import type { NavPage } from './components/Sidebar'

interface BuildNavigationShortcutOptions {
  canUseAccessWorkspace: boolean
  isDevUserPreview: boolean
  onNavigate: (page: NavPage) => void
}

const NAVIGATION_SHORTCUTS: Array<{ id: string; label: string; keys: string; page: NavPage; hidden?: (options: BuildNavigationShortcutOptions) => boolean }> = [
  { id: 'dashboard', label: 'Go to Dashboard', keys: 'g d', page: 'dashboard' },
  { id: 'command-center', label: 'Go to Command Center', keys: 'g c', page: 'command-center' },
  { id: 'new-test', label: 'Go to New Test', keys: 'g n', page: 'new-test' },
  { id: 'scripts', label: 'Go to Scripts', keys: 'g s', page: 'scripts' },
  { id: 'results', label: 'Go to Results', keys: 'g r', page: 'results' },
  { id: 'monitoring', label: 'Go to Monitoring', keys: 'g m', page: 'monitoring' },
  { id: 'docs', label: 'Go to API Guide', keys: 'g h', page: 'docs' },
  { id: 'settings', label: 'Go to Settings', keys: 'g ,', page: 'settings', hidden: options => options.isDevUserPreview },
  { id: 'access', label: 'Go to Access', keys: 'g a', page: 'access', hidden: options => !options.canUseAccessWorkspace },
]

export function buildNavigationShortcuts(options: BuildNavigationShortcutOptions): KeyboardShortcut[] {
  return NAVIGATION_SHORTCUTS.map(shortcut => ({
    id: `navigation.${shortcut.id}`,
    label: shortcut.label,
    keys: shortcut.keys,
    group: 'Navigation',
    hidden: shortcut.hidden?.(options) || false,
    priority: 500,
    onTrigger: () => options.onNavigate(shortcut.page),
  }))
}

export function focusCurrentPageSearch(container: ParentNode = document): boolean {
  const selector = [
    'input[type="search"]',
    'input[aria-label*="Search" i]',
    'input[placeholder*="Search" i]',
    'input[aria-label*="Filter" i]',
    'input[placeholder*="Filter" i]',
  ].join(',')
  const candidates = Array.from(container.querySelectorAll<HTMLInputElement>(selector))
  const target = candidates.find(input => !input.disabled && isFocusableSearchTarget(input))
  if (!target) return false
  target.focus()
  target.select()
  return true
}

function isFocusableSearchTarget(input: HTMLInputElement) {
  const style = window.getComputedStyle(input)
  return style.display !== 'none' && style.visibility !== 'hidden'
}
