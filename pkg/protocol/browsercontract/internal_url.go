package browsercontract

import (
	"net/url"
	"strings"
)

// WindowPageKey names the page an http(s) URL shows: its origin and path, and
// a fragment only when it is a route (#/reports, #!/home), never a one-time
// value such as #access_token=. A switch to a window recorded with its full
// URL keeps the query of the recorded run (a sign-in popup's expiry, user and
// state), which no later run repeats; the page key still names that window.
// ok is false for anything that is not an http(s) URL with a host.
func WindowPageKey(raw string) (key string, ok bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	key = strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host) + path
	if fragment := parsed.Fragment; strings.HasPrefix(fragment, "/") || strings.HasPrefix(fragment, "!") {
		// A hash router's own query (#/callback?token=...) is per visit too.
		route, _, _ := strings.Cut(fragment, "?")
		key += "#" + route
	}
	return key, true
}

// IsBrowserInternalURL reports a URL of a page the browser draws for itself,
// not one a site serves: Chromium's error page for a load that failed
// (chrome-error://chromewebdata/), its settings and DevTools pages, extension
// pages. Nothing a site does can be replayed by loading one, and an
// interaction on one is with the browser, not the site. about:blank and
// about:srcdoc are not internal here: sites build working frames from them.
func IsBrowserInternalURL(raw string) bool {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok {
		return false
	}
	switch strings.ToLower(scheme) {
	case "chrome-error", "chrome", "chrome-untrusted", "chrome-search", "chrome-extension",
		"devtools", "edge", "brave", "opera", "vivaldi":
		// chrome://settings, not a host named edge at edge:8080/login.
		return strings.HasPrefix(rest, "//")
	case "view-source":
		return strings.HasPrefix(strings.ToLower(rest), "http") || strings.HasPrefix(rest, "//")
	}
	return false
}
