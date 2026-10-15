package appium

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// webdriverElementKey is the W3C WebDriver element reference key; Appium also
// answers with the older JSON Wire key "ELEMENT".
const webdriverElementKey = "element-6066-11e4-a5d6-4a0f6f0ba3b9"

// maxWebDriverResponseBytes bounds a command's response (a screenshot of a
// large tablet is the biggest).
const maxWebDriverResponseBytes = 32 << 20

// errNoSuchElement is a locator that matched nothing (yet); errStaleElement
// an element the screen replaced after it was found.
var (
	errNoSuchElement = errors.New("no such element")
	errStaleElement  = errors.New("stale element reference")
)

// webdriverError is an error the server answered with.
type webdriverError struct {
	status  int
	code    string
	message string
}

func (e *webdriverError) Error() string {
	if e.message == "" {
		return fmt.Sprintf("appium %s (HTTP %d)", e.code, e.status)
	}
	return fmt.Sprintf("appium %s: %s", e.code, e.message)
}

func (e *webdriverError) Is(target error) bool {
	return (target == errNoSuchElement || target == errStaleElement) && e.code == target.Error()
}

// webdriver is a W3C WebDriver client for one Appium server: the local
// Appium of a simulator host, or a device cloud's hub (whose credentials ride
// in the URL's user info).
type webdriver struct {
	base   *url.URL
	client *http.Client
}

func newWebDriver(serverURL string) (*webdriver, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(serverURL), "/"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("appium server_url must be an http or https URL")
	}
	return &webdriver{base: parsed, client: &http.Client{}}, nil
}

// endpoint is the server URL with path appended.
func (d *webdriver) endpoint(path string) string {
	u := *d.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String()
}

// call sends one command and decodes the response's value into out (when
// not nil). A server error comes back as a *webdriverError.
func (d *webdriver) call(ctx context.Context, method, path string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.endpoint(path), payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	req.Header.Set("Accept", "application/json")
	if d.base.User != nil {
		password, _ := d.base.User.Password()
		req.SetBasicAuth(d.base.User.Username(), password)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return fmt.Errorf("appium %s %s: %w", method, path, redactUserInfo(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxWebDriverResponseBytes+1))
	if err != nil {
		return fmt.Errorf("appium %s %s: read response: %w", method, path, err)
	}
	if len(raw) > maxWebDriverResponseBytes {
		return fmt.Errorf("appium %s %s: response exceeds %d bytes", method, path, maxWebDriverResponseBytes)
	}
	var envelope struct {
		SessionID string          `json:"sessionId"`
		Value     json.RawMessage `json:"value"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return fmt.Errorf("appium %s %s: HTTP %d with a response that is not JSON", method, path, resp.StatusCode)
		}
	}
	if resp.StatusCode >= 400 || isWebDriverError(envelope.Value) {
		var failure struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(envelope.Value, &failure)
		if failure.Error == "" {
			failure.Error = "error"
		}
		return &webdriverError{status: resp.StatusCode, code: failure.Error, message: firstLine(failure.Message)}
	}
	if out == nil {
		return nil
	}
	if session, ok := out.(*newSessionValue); ok && envelope.SessionID != "" {
		// A JSON Wire server puts the session id beside the value.
		session.SessionID = envelope.SessionID
	}
	if len(envelope.Value) == 0 || string(envelope.Value) == "null" {
		return nil
	}
	return json.Unmarshal(envelope.Value, out)
}

func isWebDriverError(value json.RawMessage) bool {
	if len(value) == 0 || value[0] != '{' {
		return false
	}
	var probe struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(value, &probe) == nil && probe.Error != ""
}

func firstLine(message string) string {
	message = strings.TrimSpace(message)
	if cut := strings.IndexByte(message, '\n'); cut >= 0 {
		message = message[:cut]
	}
	const limit = 500
	if len(message) > limit {
		message = message[:limit] + "…"
	}
	return message
}

// redactUserInfo keeps a device cloud's credentials out of a transport error,
// which quotes the request URL.
func redactUserInfo(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil && parsed.User != nil {
			parsed.User = nil
			return &url.Error{Op: urlErr.Op, URL: parsed.String(), Err: urlErr.Err}
		}
	}
	return err
}

type newSessionValue struct {
	SessionID    string         `json:"sessionId"`
	Capabilities map[string]any `json:"capabilities"`
}

// newSession starts a session on the device the capabilities name.
func (d *webdriver) newSession(ctx context.Context, capabilities map[string]any) (string, error) {
	var value newSessionValue
	body := map[string]any{"capabilities": map[string]any{"alwaysMatch": capabilities, "firstMatch": []any{map[string]any{}}}}
	if err := d.call(ctx, http.MethodPost, "/session", body, &value); err != nil {
		return "", err
	}
	if value.SessionID == "" {
		return "", fmt.Errorf("appium did not return a session id")
	}
	return value.SessionID, nil
}

func (d *webdriver) deleteSession(ctx context.Context, session string) error {
	return d.call(ctx, http.MethodDelete, "/session/"+url.PathEscape(session), nil, nil)
}

func (d *webdriver) status(ctx context.Context) error {
	return d.call(ctx, http.MethodGet, "/status", nil, nil)
}

// findElement finds one element, once.
func (d *webdriver) findElement(ctx context.Context, session, using, value string) (string, error) {
	var element map[string]string
	if err := d.call(ctx, http.MethodPost, "/session/"+url.PathEscape(session)+"/element", map[string]string{"using": using, "value": value}, &element); err != nil {
		return "", err
	}
	if id := element[webdriverElementKey]; id != "" {
		return id, nil
	}
	if id := element["ELEMENT"]; id != "" {
		return id, nil
	}
	return "", fmt.Errorf("appium returned an element without a reference")
}

// waitForElement polls for an element until it appears or timeout passes.
func (d *webdriver) waitForElement(ctx context.Context, session, using, value string, timeout, poll time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		id, err := d.findElement(ctx, session, using, value)
		if err == nil || !errors.Is(err, errNoSuchElement) {
			return id, err
		}
		if time.Now().Add(poll).After(deadline) {
			return "", fmt.Errorf("no element matched %s %q within %s", using, value, timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (d *webdriver) elementCommand(ctx context.Context, session, element, command string, body any, out any) error {
	path := "/session/" + url.PathEscape(session) + "/element/" + url.PathEscape(element)
	if command != "" {
		path += "/" + command
	}
	method := http.MethodPost
	if body == nil {
		method = http.MethodGet
	}
	return d.call(ctx, method, path, body, out)
}

func (d *webdriver) execute(ctx context.Context, session, script string, args []any, out any) error {
	if args == nil {
		args = []any{}
	}
	return d.call(ctx, http.MethodPost, "/session/"+url.PathEscape(session)+"/execute/sync", map[string]any{"script": script, "args": args}, out)
}

func (d *webdriver) screenshot(ctx context.Context, session string) (string, error) {
	var encoded string
	err := d.call(ctx, http.MethodGet, "/session/"+url.PathEscape(session)+"/screenshot", nil, &encoded)
	return encoded, err
}
