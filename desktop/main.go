package main

import (
	"io/fs"
	"log"

	"github.com/jasonczech/apexload/pkg/api"
	"github.com/jasonczech/apexload/pkg/buildinfo"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// Extract the "static" subdirectory from the embedded filesystem
	staticSub, err := fs.Sub(api.StaticFS, "static")
	if err != nil {
		log.Fatal("failed to load embedded static assets:", err)
	}

	// Build the native application menu. On macOS this restores the role-based
	// Edit menu (copy/paste/undo/select-all) that a custom menu would otherwise
	// strip from WKWebView. See desktop/menu.go.
	AppMenu := buildAppMenu(app)

	// Create application with options
	err = wails.Run(&options.App{
		Title:     "APEX Load",
		Width:     1280,
		Height:    800,
		MinWidth:  minWindowWidth,
		MinHeight: minWindowHeight,
		AssetServer: &assetserver.Options{
			Assets: staticSub,
		},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 255}, // Match --bg-primary
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Menu:             AppMenu,
		// Only one instance may run: the first owns the local SQLite handle, and a
		// second launch raises the existing window and forwards any open-with
		// file/URL args to it (see desktop/instance.go).
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               singleInstanceUniqueID,
			OnSecondInstanceLaunch: app.onSecondInstanceLaunch,
		},
		// Surface dropped-file absolute paths to the dashboard (OnFileDrop). Drop
		// targets opt in via the `--wails-drop-target: drop` CSS marker.
		// DisableWebViewDrop stops the webview from navigating to an accidental
		// file drop outside a registered target.
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  true,
				HideTitleBar:               false,
				FullSizeContent:            true,
				UseToolbar:                 false,
				HideToolbarSeparator:       true,
			},
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "APEX Load",
				Message: buildAboutMessage(buildinfo.Get()),
			},
			// Open-with handoff for file associations + the apexload:// scheme
			// declared in wails.json (see desktop/instance.go).
			OnFileOpen: app.handleOpenFile,
			OnUrlOpen:  app.handleOpenURL,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			BackdropType:         windows.Mica,
			DisableWindowIcon:    false,
			// Branded WebView2 prerequisite prompts (Wails shows a native dialog +
			// can auto-install when the runtime is missing). See desktop/windows.go.
			Messages: apexloadWindowsMessages(),
		},
	})

	if err != nil {
		log.Fatal(err)
	}
}
