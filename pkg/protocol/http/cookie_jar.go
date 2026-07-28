package http

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxHTTPCookiesPerVUser          = 256
	maxHTTPCookieBytes              = 8 << 10
	maxHTTPCookieJarBytes           = 64 << 10
	maxHTTPProcessCookieBytes int64 = 256 << 20
)

var processHTTPCookieBudget = responseCaptureBudget{limit: maxHTTPProcessCookieBytes}

// boundedCookieJar wraps the standard RFC-compliant jar with admission
// accounting. Accounting is intentionally conservative: replacements and
// deletions never reduce a key's high-water reservation until ClearCookies or
// teardown, so a jar can overcount but can never retain more than its bounds.
type boundedCookieJar struct {
	mu            sync.Mutex
	jar           http.CookieJar
	entries       map[string]int64
	currentValues map[string]string
	bytes         int64
	memory        *responseCaptureLease
}

func newBoundedCookieJar() (*boundedCookieJar, error) {
	return newBoundedCookieJarWithBudget(&processHTTPCookieBudget)
}

func newBoundedCookieJarWithBudget(budget *responseCaptureBudget) (*boundedCookieJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &boundedCookieJar{
		jar:           jar,
		entries:       make(map[string]int64),
		currentValues: make(map[string]string),
		memory:        budget.lease(),
	}, nil
}

func (j *boundedCookieJar) Cookies(target *url.URL) []*http.Cookie {
	if j == nil || target == nil {
		return nil
	}
	j.mu.Lock()
	cookies := j.jar.Cookies(target)
	j.mu.Unlock()
	return cookies
}

func (j *boundedCookieJar) SetCookies(target *url.URL, cookies []*http.Cookie) {
	if j == nil || target == nil || len(cookies) == 0 {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	accepted := make([]*http.Cookie, 0, min(len(cookies), maxHTTPCookiesPerVUser))
	now := time.Now()
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		key := boundedCookieKey(target, cookie)
		if key == "" {
			continue
		}
		// Deletions must reach the underlying jar. Keep their high-water
		// reservation conservatively; releasing it when the standard jar rejects
		// a malformed domain/path could under-account a still-live old cookie.
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(now)) {
			accepted = append(accepted, cookie)
			continue
		}
		size := boundedCookieSize(cookie)
		if size <= 0 || size > maxHTTPCookieBytes {
			continue
		}
		oldSize, exists := j.entries[key]
		if !exists && len(j.entries) >= maxHTTPCookiesPerVUser {
			continue
		}
		growth := size - oldSize
		if growth < 0 {
			growth = 0
		}
		if growth > 0 && j.bytes > maxHTTPCookieJarBytes-growth {
			continue
		}
		if growth > 0 && !j.memory.reserve(growth) {
			continue
		}
		if growth > 0 {
			j.entries[key] = oldSize + growth
			j.bytes += growth
		} else if !exists {
			j.entries[key] = size
		}
		if cookie.Value == "" {
			delete(j.currentValues, key)
		} else {
			j.currentValues[key] = cookie.Value
		}
		accepted = append(accepted, cookie)
	}
	if len(accepted) > 0 {
		j.jar.SetCookies(target, accepted)
	}
}

// runtimeCredentialValuesSnapshot returns the complete bounded set of
// protocol-managed values that may be sent by this jar. It does not accumulate
// every historical rotation; the engine overlaps consecutive snapshots while
// scrubbing the rotating response and retains only a bounded retirement window.
func (j *boundedCookieJar) runtimeCredentialValuesSnapshot() []string {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	unique := make(map[string]struct{}, len(j.currentValues))
	for _, value := range j.currentValues {
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	values := make([]string, 0, len(unique))
	for value := range unique {
		values = append(values, value)
	}
	j.mu.Unlock()
	sort.Strings(values)
	return values
}

func (j *boundedCookieJar) clear() error {
	if j == nil {
		return nil
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	j.mu.Lock()
	released := j.bytes
	j.jar = jar
	j.entries = make(map[string]int64)
	j.currentValues = make(map[string]string)
	j.bytes = 0
	j.memory.releaseBytes(released)
	j.mu.Unlock()
	return nil
}

func boundedCookieKey(target *url.URL, cookie *http.Cookie) string {
	if target == nil || cookie == nil || strings.TrimSpace(cookie.Name) == "" {
		return ""
	}
	domain := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(cookie.Domain), "."))
	if domain == "" {
		domain = strings.ToLower(strings.TrimSpace(target.Hostname()))
	}
	cookiePath := strings.TrimSpace(cookie.Path)
	if cookiePath == "" || cookiePath[0] != '/' {
		cookiePath = defaultCookiePath(target.Path)
	}
	return domain + "\x00" + cookiePath + "\x00" + cookie.Name
}

func defaultCookiePath(requestPath string) string {
	if requestPath == "" || requestPath[0] != '/' || requestPath == "/" {
		return "/"
	}
	directory := path.Dir(requestPath)
	if directory == "." || directory == "" {
		return "/"
	}
	return directory
}

func boundedCookieSize(cookie *http.Cookie) int64 {
	if cookie == nil {
		return 0
	}
	return int64(len(cookie.Name) + len(cookie.Value) + len(cookie.Path) + len(cookie.Domain) + len(cookie.Raw) + len(cookie.RawExpires) + 128)
}
