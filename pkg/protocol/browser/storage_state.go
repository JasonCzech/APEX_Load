package browser

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

const MaxStorageStateBytes = 1 << 20

// ParseStorageState accepts a bounded Playwright cookie/localStorage snapshot.
// Errors intentionally never include the input, which is a project secret.
// Unknown fields are rejected rather than silently losing IndexedDB/session state.
func ParseStorageState(raw string) (*playwright.OptionalStorageState, error) {
	if len(raw) == 0 || len(raw) > MaxStorageStateBytes || !json.Valid([]byte(raw)) || !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return nil, fmt.Errorf("browser storage state must be a JSON object of at most %d bytes", MaxStorageStateBytes)
	}
	var state playwright.OptionalStorageState
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("browser storage state must contain only supported Playwright cookies and localStorage origins")
	}
	if len(state.Cookies) > 1024 || len(state.Origins) > 128 {
		return nil, fmt.Errorf("browser storage state exceeds cookie or origin limits")
	}
	for _, cookie := range state.Cookies {
		if cookie.Name == "" || len(cookie.Name) > 1024 || len(cookie.Value) > 16384 || cookie.Domain == nil || *cookie.Domain == "" || cookie.Path == nil || !strings.HasPrefix(*cookie.Path, "/") || cookie.URL != nil {
			return nil, fmt.Errorf("browser storage state contains an invalid cookie; use domain and path")
		}
		domain := strings.TrimPrefix(*cookie.Domain, ".")
		if !validBrowserOrigin("https://"+domain, false) || strings.ContainsAny(domain, "/:?#@\\") {
			return nil, fmt.Errorf("browser storage state contains an invalid cookie domain")
		}
	}
	for _, origin := range state.Origins {
		if !validBrowserOrigin(origin.Origin, false) || len(origin.LocalStorage) > 256 {
			return nil, fmt.Errorf("browser storage state contains an invalid origin")
		}
		for _, item := range origin.LocalStorage {
			if len(item.Name) > 1024 || len(item.Value) > 16384 {
				return nil, fmt.Errorf("browser storage state localStorage entry exceeds limits")
			}
			if _, err := storageValueLeaves(item.Value); err != nil {
				return nil, err
			}
		}
	}
	return &state, nil
}

// StorageStateSecretValues registers individual credential values as well as
// the enclosing JSON; browser requests and console output can expose a leaf.
func StorageStateSecretValues(raw string) []string {
	state, err := ParseStorageState(raw)
	if err != nil {
		return nil
	}
	values := make([]string, 0, len(state.Cookies))
	for _, cookie := range state.Cookies {
		if cookie.Value != "" {
			values = append(values, cookie.Value)
		}
	}
	for _, origin := range state.Origins {
		for _, item := range origin.LocalStorage {
			if item.Value != "" {
				values = append(values, item.Value)
			}
			leaves, _ := storageValueLeaves(item.Value)
			values = append(values, leaves...)
		}
	}
	return values
}

func (b *Browser) configureStorageState(config map[string]any) error {
	raw := protocol.ConfigString(config, "browser_storage_state")
	if raw == "" {
		return nil
	}
	state, err := ParseStorageState(raw)
	if err != nil {
		return err
	}
	if len(b.allowedTargetHosts) > 0 {
		for _, cookie := range state.Cookies {
			if !protocol.TargetURLAllowed("https://"+strings.TrimPrefix(*cookie.Domain, "."), b.allowedTargetHosts) {
				return fmt.Errorf("browser storage state cookie is outside allowed target hosts")
			}
		}
		for _, origin := range state.Origins {
			if !protocol.TargetURLAllowed(origin.Origin, b.allowedTargetHosts) {
				return fmt.Errorf("browser storage state origin is outside allowed target hosts")
			}
		}
	}
	b.storageState = state
	return nil
}

// SPAs often store an auth object as a JSON string. Protect the nested token
// too, because the page can send it without the enclosing storage object.
func storageValueLeaves(raw string) ([]string, error) {
	if !json.Valid([]byte(raw)) {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, nil //nolint:nilerr // the whole value is already protected; a value that does not decode has no nested leaves
	}
	var values []string
	var walk func(any, int) error
	walk = func(node any, depth int) error {
		if depth > 32 || len(values) > 1024 {
			return fmt.Errorf("browser storage state localStorage JSON exceeds structural limits")
		}
		switch node := node.(type) {
		case string:
			if node != "" {
				values = append(values, node)
			}
		case json.Number:
			values = append(values, node.String())
		case map[string]any:
			for _, child := range node {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(value, 0); err != nil {
		return nil, err
	}
	if len(values) > 1024 {
		return nil, fmt.Errorf("browser storage state localStorage JSON exceeds structural limits")
	}
	return values, nil
}
