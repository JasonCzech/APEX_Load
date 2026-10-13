import type { AuthoringComponent } from '../types'

/** A block a user saved (it says when), as opposed to a built-in catalog entry. */
export function isSavedBlock(component: AuthoringComponent): boolean {
  // Saved blocks carry a server-generated "block-" id, and saved_at from controllers that report it.
  return Boolean(component.saved_at) || (component.id || '').startsWith('block-')
}
