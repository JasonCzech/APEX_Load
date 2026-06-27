package main

import (
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Menu event names emitted to the dashboard over the Wails event bus. The
// dashboard subscribes via window.runtime.EventsOn and maps them to in-app
// actions, because several "native" affordances are actually owned by the React
// app rather than the OS shell: webview zoom is implemented with the
// --dashboard-zoom CSS variable (Wails v2 has no stable runtime zoom API), the
// About panel is an in-app dialog so it can show live build info on every
// platform, and File actions translate to dashboard navigation.
const (
	menuEventZoom   = "menu:zoom"   // payload: "in" | "out" | "reset"
	menuEventAbout  = "menu:about"  // open the in-app About dialog
	menuEventAction = "menu:action" // payload: action id ("new-test" | "open-script" | "import")
)

// buildAppMenu constructs the native application menu for the host OS.
func buildAppMenu(app *App) *menu.Menu {
	return buildAppMenuForOS(app, runtime.GOOS)
}

// buildAppMenuForOS builds the menu for an explicit GOOS so it is unit-testable
// off the host platform.
//
// Menu roles (AppMenu/EditMenu/WindowMenu) are honored only on macOS — the Wails
// frontend processes Role solely in the darwin backend. So on macOS we use the
// role-based Edit menu, which is what wires ⌘C/⌘V/⌘X/⌘A/⌘Z to the WKWebView
// page. The previous menu replaced the macOS default menu with a single
// empty-callback "Quit" item, which stripped the Edit menu and disabled
// copy/paste/undo in the script editor, JSON viewers, and inputs.
//
// On Windows (WebView2) and Linux (WebKitGTK) the webview natively handles those
// editing shortcuts, so we deliberately do NOT add accelerator-bearing Edit
// items there: a menu accelerator would intercept the keystroke before the
// webview and break copy/paste.
func buildAppMenuForOS(app *App, goos string) *menu.Menu {
	isMac := goos == "darwin"
	appMenu := menu.NewMenu()

	if isMac {
		// Standard macOS application menu: About, Services, Hide, Quit (⌘Q).
		appMenu.Append(menu.AppMenu())
	}

	fileMenu := appMenu.AddSubmenu("File")
	fileMenu.AddText("New Test", keys.CmdOrCtrl("n"), app.menuAction("new-test"))
	fileMenu.AddText("Open Script…", keys.CmdOrCtrl("o"), app.menuAction("open-script"))
	fileMenu.AddText("Import…", keys.Combo("i", keys.CmdOrCtrlKey, keys.ShiftKey), app.menuAction("import"))
	fileMenu.AddSeparator()
	fileMenu.AddText("Reveal Logs Folder", nil, func(_ *menu.CallbackData) { _ = app.OpenLogsFolder() })
	fileMenu.AddText("Reveal App Data Folder", nil, func(_ *menu.CallbackData) { _ = app.OpenAppDataFolder() })
	if !isMac {
		// macOS exposes Quit through the role-based App menu; elsewhere File owns it.
		fileMenu.AddSeparator()
		fileMenu.AddText("Quit", keys.CmdOrCtrl("q"), app.quit)
	}

	if isMac {
		appMenu.Append(menu.EditMenu())
	}

	viewMenu := appMenu.AddSubmenu("View")
	viewMenu.AddText("Reload", keys.CmdOrCtrl("r"), app.reload)
	viewMenu.AddSeparator()
	viewMenu.AddText("Zoom In", keys.CmdOrCtrl("="), app.menuZoom("in"))
	viewMenu.AddText("Zoom Out", keys.CmdOrCtrl("-"), app.menuZoom("out"))
	viewMenu.AddText("Actual Size", keys.CmdOrCtrl("0"), app.menuZoom("reset"))

	if isMac {
		appMenu.Append(menu.WindowMenu())
	}

	helpMenu := appMenu.AddSubmenu("Help")
	helpMenu.AddText("About APEX Load", nil, app.menuEmit(menuEventAbout))
	helpMenu.AddText("Open in System Browser", nil, func(_ *menu.CallbackData) { _ = app.OpenInBrowser() })

	return appMenu
}

// menuEmit returns a menu callback that emits a no-payload dashboard event.
func (a *App) menuEmit(event string) menu.Callback {
	return func(_ *menu.CallbackData) {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, event)
		}
	}
}

// menuAction emits a menu:action event carrying the action id.
func (a *App) menuAction(action string) menu.Callback {
	return func(_ *menu.CallbackData) {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, menuEventAction, action)
		}
	}
}

// menuZoom emits a menu:zoom event with the requested zoom action.
func (a *App) menuZoom(action string) menu.Callback {
	return func(_ *menu.CallbackData) {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, menuEventZoom, action)
		}
	}
}

func (a *App) reload(_ *menu.CallbackData) {
	if a.ctx != nil {
		wailsruntime.WindowReloadApp(a.ctx)
	}
}

func (a *App) quit(_ *menu.CallbackData) {
	if a.ctx != nil {
		wailsruntime.Quit(a.ctx)
	}
}
