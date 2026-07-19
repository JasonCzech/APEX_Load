// DOM-only handoff used for native menu intents returned by RendererReady.
// Live Wails zoom events are consumed directly from window.runtime; queued
// events arrive in the readiness response and are replayed through this event
// after every React listener has been installed.
export const DESKTOP_QUEUED_MENU_ZOOM_EVENT =
  "apexload:desktop-queued-menu-zoom";
