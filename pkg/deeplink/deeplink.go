// Package deeplink validates and expands provider link templates, such as a
// trace view URL with a {trace_id} placeholder. A template may point only at
// HTTPS on one of its connection's hosts, nothing but the four known
// placeholders is substituted, identifiers must be plain RFC 3986 unreserved
// text, and every substituted value is percent-encoded so it cannot change
// the URL's structure.
package deeplink

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// Placeholders a template may use.
const (
	TraceID  = "trace_id"
	EntityID = "entity_id"
	FromUTC  = "from_utc"
	ToUTC    = "to_utc"
)

// MaxTemplateBytes bounds a stored template and an expanded link.
const MaxTemplateBytes = 2048

// maxIdentifierBytes bounds a substituted trace or entity ID.
const maxIdentifierBytes = 256

var known = map[string]bool{TraceID: true, EntityID: true, FromUTC: true, ToUTC: true}

// Values fills a template. Times render as RFC 3339 UTC seconds.
type Values struct {
	TraceID  string
	EntityID string
	From     time.Time
	To       time.Time
}

func (v Values) lookup(name string) (string, error) {
	var value string
	var ok bool
	switch name {
	case TraceID:
		value, ok = v.TraceID, v.TraceID != ""
	case EntityID:
		value, ok = v.EntityID, v.EntityID != ""
	case FromUTC:
		value, ok = v.From.UTC().Format(time.RFC3339), !v.From.IsZero()
	case ToUTC:
		value, ok = v.To.UTC().Format(time.RFC3339), !v.To.IsZero()
	}
	if !ok {
		return "", errors.New("link template needs a value for {" + name + "}")
	}
	if (name == TraceID || name == EntityID) && !identifier(value) {
		return "", errors.New("link value for {" + name + "} must be an identifier")
	}
	return value, nil
}

// identifier accepts unreserved characters only, so a value means the same
// thing after the target product decodes it (a Kibana fragment is decoded
// before its rison state is parsed). A value of only dots is a path
// dot-segment, which browsers resolve even when percent-encoded.
func identifier(value string) bool {
	if len(value) > maxIdentifierBytes || strings.Trim(value, ".") == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if !unreserved(value[i]) {
			return false
		}
	}
	return true
}

func unreserved(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~'
}

// segment is literal text or a placeholder name.
type segment struct {
	text        string
	placeholder bool
}

func parse(template string) ([]segment, error) {
	if template == "" || len(template) > MaxTemplateBytes {
		return nil, errors.New("link template must be 1-2048 bytes")
	}
	if strings.ContainsAny(template, " \t\r\n\x00") {
		return nil, errors.New("link template must not contain whitespace or control characters")
	}
	var out []segment
	rest := template
	for rest != "" {
		open := strings.IndexByte(rest, '{')
		closing := strings.IndexByte(rest, '}')
		if open < 0 {
			if closing >= 0 {
				return nil, errors.New("link template has an unmatched }")
			}
			out = append(out, segment{text: rest})
			break
		}
		if closing >= 0 && closing < open {
			return nil, errors.New("link template has an unmatched }")
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return nil, errors.New("link template has an unmatched {")
		}
		name := rest[open+1 : open+end]
		if !known[name] {
			return nil, errors.New("link template may use only {trace_id}, {entity_id}, {from_utc} and {to_utc}")
		}
		if open > 0 {
			out = append(out, segment{text: rest[:open]})
		}
		out = append(out, segment{text: name, placeholder: true})
		rest = rest[open+end+1:]
	}
	return out, nil
}

// CheckTarget requires an absolute HTTPS URL of at most MaxTemplateBytes on
// one of hosts, compared case-insensitively with its port, and no user
// information.
func CheckTarget(raw string, hosts []string) error {
	if len(raw) > MaxTemplateBytes {
		return errors.New("link exceeds 2048 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return errors.New("link must be an absolute https URL without user information")
	}
	for _, host := range hosts {
		if host != "" && strings.EqualFold(u.Host, host) {
			return nil
		}
	}
	return errors.New("link must point at its connection's own host")
}

// Template is a validated template bound to the hosts it may target.
type Template struct {
	segments []segment
	hosts    []string
}

// Compile accepts a template whose scheme and host are literal, HTTPS and
// one of hosts, with only known placeholders after the host.
func Compile(template string, hosts []string) (*Template, error) {
	segments, err := parse(template)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 || segments[0].placeholder {
		return nil, errors.New("link template must start with a literal https://host/")
	}
	// A placeholder may never reach the scheme or authority: the literal
	// prefix must already end the host.
	rest, ok := strings.CutPrefix(segments[0].text, "https://")
	if !ok {
		return nil, errors.New("link template must use https")
	}
	if strings.IndexAny(rest, "/?#") < 0 && len(segments) > 1 {
		return nil, errors.New("link template must end its host before any placeholder")
	}
	t := &Template{segments: segments, hosts: append([]string(nil), hosts...)}
	sample := Values{TraceID: "0af7651916cd43dd8448eb211c80319c", EntityID: "SERVICE-1", From: time.Unix(0, 0), To: time.Unix(60, 0)}
	if _, err := t.Expand(sample); err != nil {
		return nil, err
	}
	return t, nil
}

// Validate reports whether Compile accepts the template.
func Validate(template string, hosts []string) error {
	_, err := Compile(template, hosts)
	return err
}

// Expand fills the template. A value is percent-encoded except for RFC 3986
// unreserved characters, so it can never add a path segment, query
// parameter, fragment or host.
func (t *Template) Expand(v Values) (string, error) {
	var b strings.Builder
	for _, s := range t.segments {
		if !s.placeholder {
			b.WriteString(s.text)
			continue
		}
		value, err := v.lookup(s.text)
		if err != nil {
			return "", err
		}
		b.WriteString(escape(value))
	}
	link := b.String()
	if err := CheckTarget(link, t.hosts); err != nil {
		return "", err
	}
	return link, nil
}

// Expand compiles and fills a template in one step.
func Expand(template string, v Values, hosts []string) (string, error) {
	t, err := Compile(template, hosts)
	if err != nil {
		return "", err
	}
	return t.Expand(v)
}

// HostOf returns the host[:port] of an https origin, or "" when it is not one.
func HostOf(origin string) string {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return ""
	}
	return u.Host
}

// OriginHosts returns the host[:port] of each parseable origin, whatever its
// scheme, for checking provider-built links against their connection.
func OriginHosts(origins ...string) []string {
	var hosts []string
	for _, origin := range origins {
		if u, err := url.Parse(strings.TrimSpace(origin)); err == nil && u.Host != "" {
			hosts = append(hosts, u.Host)
		}
	}
	return hosts
}

func escape(value string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if unreserved(c) {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}
