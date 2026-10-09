/** Asks the sidebar to open its project picker (for example from a scope-blocked route). */
export const OPEN_PROJECT_PICKER_EVENT = 'apexload:open-project-picker'

export function requestProjectPicker() {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(OPEN_PROJECT_PICKER_EVENT))
}
