package main

import "github.com/wailsapp/wails/v2/pkg/options/windows"

// apexloadWindowsMessages brands the WebView2 prerequisite prompts Wails shows
// when the Evergreen WebView2 runtime is missing or outdated. Wails already
// displays a native dialog (and can silently install the runtime) instead of a
// blank window — this just makes the copy clear and product-specific. Packaging
// docs should additionally recommend bundling the Evergreen/Fixed-Version
// runtime for offline installs.
func apexloadWindowsMessages() *windows.Messages {
	m := windows.DefaultMessages()
	m.Webview2NotInstalled = "Microsoft Edge WebView2 runtime is required to run APEX Load."
	m.InstallationRequired = "APEX Load needs the Microsoft Edge WebView2 runtime. Press OK to download and install it (this happens silently — please wait)."
	m.ContactAdmin = "APEX Load requires the Microsoft Edge WebView2 runtime. Please contact your system administrator to install it."
	m.DownloadPage = "APEX Load requires the Microsoft Edge WebView2 runtime. Press OK to open the download page. Minimum version required: "
	return m
}
