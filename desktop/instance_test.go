package main

import (
	"reflect"
	"testing"
)

func TestIsOpenableFile(t *testing.T) {
	cases := map[string]bool{
		"/x/order.json":      true,
		"/x/archive.zip":     true,
		"/x/capture.HAR":     true, // case-insensitive
		"/x/notes.txt":       false,
		"/x/script.exe":      false,
		"":                   false,
		"apexload://run/123": false,
	}
	for path, want := range cases {
		if got := isOpenableFile(path); got != want {
			t.Errorf("isOpenableFile(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsApexloadURL(t *testing.T) {
	cases := map[string]bool{
		"apexload://run/123":  true,
		"APEXLOAD://open?x=1": true, // scheme is case-insensitive
		"https://example.com": false,
		"/x/order.json":       false,
		"":                    false,
		"file:///etc/passwd":  false,
	}
	for raw, want := range cases {
		if got := isApexloadURL(raw); got != want {
			t.Errorf("isApexloadURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestClassifyOpenArgs(t *testing.T) {
	files, urls := classifyOpenArgs([]string{
		"/Applications/APEX Load.app/Contents/MacOS/apexload",
		"--flag",
		"/Users/x/order.json",
		"apexload://run/abc",
		"/Users/x/notes.txt",
		"/Users/x/archive.zip",
	})
	wantFiles := []string{"/Users/x/order.json", "/Users/x/archive.zip"}
	wantURLs := []string{"apexload://run/abc"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Errorf("files = %v, want %v", files, wantFiles)
	}
	if !reflect.DeepEqual(urls, wantURLs) {
		t.Errorf("urls = %v, want %v", urls, wantURLs)
	}
}

func TestHandleOpenFileWithoutCtxIsNoop(t *testing.T) {
	app := NewApp()
	// Should not panic without a runtime context.
	app.handleOpenFile("/x/order.json")
	app.handleOpenURL("apexload://run/1")
}
