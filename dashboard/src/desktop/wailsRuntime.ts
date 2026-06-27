// Minimal typing for the Wails v2 runtime that the desktop shell injects as
// `window.runtime`. Importing the generated `wailsjs/runtime` module would pull
// Wails-specific code into the hosted-web bundle, so the dashboard talks to the
// injected global directly and degrades to undefined in the browser.

export type WailsEventCancel = () => void

export interface WailsEventRuntime {
  EventsOn?: (eventName: string, callback: (...data: unknown[]) => void) => WailsEventCancel
  EventsEmit?: (eventName: string, ...data: unknown[]) => void
}

export interface WailsEventRuntimeWindow extends Window {
  runtime?: WailsEventRuntime
}

// getWailsEventRuntime returns the injected Wails runtime, or undefined when not
// running inside the desktop shell (hosted web, SSR, tests).
export function getWailsEventRuntime(): WailsEventRuntime | undefined {
  if (typeof window === 'undefined') {
    return undefined
  }
  return (window as WailsEventRuntimeWindow).runtime
}
