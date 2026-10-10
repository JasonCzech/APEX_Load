package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// OptionalHTTPVersion preserves absence separately from an authored JSON null or
// empty string. Its zero value inherits the session policy. Explicit policies
// are strict scalars: auto, 1.1, 2, auto+h3, or 3. Values are never
// interpolated. The HTTP/3 policies are valid vocabulary on every
// installation; whether one can execute is decided at admission and by the
// runtime, not here.
type OptionalHTTPVersion struct {
	value   string
	present bool
	null    bool
}

func NewHTTPVersionOverride(value string) (OptionalHTTPVersion, error) {
	result := OptionalHTTPVersion{value: value, present: true}
	return result, result.Validate()
}

func (value OptionalHTTPVersion) IsZero() bool    { return !value.present }
func (value OptionalHTTPVersion) IsPresent() bool { return value.present }
func (value OptionalHTTPVersion) Value() string   { return value.value }

func (value OptionalHTTPVersion) Validate() error {
	if !value.present {
		return nil
	}
	if value.null {
		return fmt.Errorf("http_version must be a non-null string: %s", httpVersionPolicyList)
	}
	if !validHTTPVersionPolicy(value.value) {
		return fmt.Errorf("http_version must be %s", httpVersionPolicyList)
	}
	return nil
}

// Resolve returns the effective transport policy. The inherited policy has
// already been normalized by session initialization; empty means auto.
func (value OptionalHTTPVersion) Resolve(inherited string) (string, error) {
	if err := value.Validate(); err != nil {
		return "", err
	}
	if value.present {
		return value.value, nil
	}
	if inherited == "" {
		return "auto", nil
	}
	if !validHTTPVersionPolicy(inherited) {
		return "", fmt.Errorf("inherited HTTP version is unsupported")
	}
	return inherited, nil
}

// httpVersionPolicyList names the accepted explicit policies for messages.
const httpVersionPolicyList = "auto, 1.1, 2, auto+h3, or 3"

func validHTTPVersionPolicy(value string) bool {
	switch value {
	case "auto", "1.1", "2", "auto+h3", "3":
		return true
	}
	return false
}

func (value OptionalHTTPVersion) MarshalJSON() ([]byte, error) {
	if !value.present || value.null {
		return []byte("null"), nil
	}
	return json.Marshal(value.value)
}

func (value *OptionalHTTPVersion) UnmarshalJSON(data []byte) error {
	if value == nil {
		return fmt.Errorf("http_version destination is nil")
	}
	*value = OptionalHTTPVersion{present: true}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.null = true
		return nil
	}
	if err := json.Unmarshal(data, &value.value); err != nil {
		return fmt.Errorf("http_version must be a string: %s", httpVersionPolicyList)
	}
	return nil
}
