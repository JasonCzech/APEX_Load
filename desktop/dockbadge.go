package main

// SetDockBadge sets the dock/taskbar badge label (empty string clears it),
// driven by the dashboard's active-run count so the desktop icon reflects how
// many tests are running. Implemented on macOS (dock tile); a documented no-op
// on Windows/Linux for now (taskbar overlay icons via ITaskbarList3 / Unity
// LauncherEntry are a larger, separately-tracked effort — see the plan backlog).
func (a *App) SetDockBadge(label string) {
	setDockBadge(label)
}
