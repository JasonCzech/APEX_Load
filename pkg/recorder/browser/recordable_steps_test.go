package browser

import (
	"strings"
	"testing"
)

// The server decides whether hovers are recorded: the page's own config can
// be rewritten by the site.
func TestHandleRecordBindingDropsHoversUnlessEnabled(t *testing.T) {
	hover := `{"kind":"hover","ts":1700000000000,"url":"https://app.example/","element":{"tag":"button","id":"menu","css_path":"#menu"}}`
	off := New(Options{StartURL: "https://app.example/"})
	off.handleRecordBinding(nil, hover)
	if got := len(off.Snapshot().Steps); got != 0 {
		t.Fatalf("steps = %d with hovers off, want 0", got)
	}
	on := New(Options{StartURL: "https://app.example/", CaptureHovers: true})
	on.handleRecordBinding(nil, hover)
	if got := len(on.Snapshot().Steps); got != 1 {
		t.Fatalf("steps = %d with hovers on, want 1", got)
	}
}

// What happens on the browser's own error page is not recorded, the recording
// says so once, and a verification pick made there still ends pick mode.
func TestHandleRecordBindingDropsBrowserErrorPage(t *testing.T) {
	var warnings []string
	r := New(Options{StartURL: "https://app.example/", OnEvent: func(e Event) {
		if e.Type == EventWarning {
			warnings = append(warnings, e.Warning)
		}
	}})
	click := `{"kind":"click","ts":1700000000000,"url":"chrome-error://chromewebdata/","element":{"tag":"div","id":"sub-frame-error","css_path":"#sub-frame-error"}}`
	r.handleRecordBinding(nil, click)
	r.handleRecordBinding(nil, click)
	snapshot := r.Snapshot()
	if len(snapshot.Steps) != 0 {
		t.Fatalf("steps = %+v, want none from the error page", snapshot.Steps)
	}
	recorded := 0
	for _, warning := range snapshot.Warnings {
		if strings.Contains(warning, "error page") {
			recorded++
		}
	}
	if recorded != 1 || len(warnings) != 1 {
		t.Fatalf("session warnings %v, emitted %v; want the error page reported once", snapshot.Warnings, warnings)
	}

	r.SetAssertMode(true)
	r.handleRecordBinding(nil, `{"kind":"assert","ts":1700000001000,"url":"chrome-error://chromewebdata/","element":{"tag":"div","id":"x","css_path":"#x"}}`)
	r.mu.Lock()
	picking := r.assertMode
	r.mu.Unlock()
	if picking {
		t.Fatal("pick mode still on after a pick on the error page")
	}
}
