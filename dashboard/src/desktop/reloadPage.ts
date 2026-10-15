// reloadPage reloads the dashboard. A module of its own so tests can stand in
// for it (jsdom cannot replace window.location.reload).
export function reloadPage() {
  window.location.reload()
}
