import type { AppView } from '../appRouting'

// desktopActionView is where a desktop home quick action or File menu item
// opens. Import and Record open their own pickers, not the script library;
// actions that are a plain page change return null.
export function desktopActionView(action: string): AppView | null {
  switch (action) {
    case 'new-test':
      return { page: 'new-test' }
    case 'import':
      return { page: 'scripts', mode: 'create', workflow: 'source' }
    case 'record':
      return { page: 'scripts', mode: 'record' }
    default:
      return null
  }
}
