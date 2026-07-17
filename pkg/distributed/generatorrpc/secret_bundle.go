package generatorrpc

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
)

const (
	MaxAssignmentSecrets          = 256
	MaxAssignmentSecretNameBytes  = engine.MaxLogicalSecretNameBytes
	MaxAssignmentSecretValueBytes = 64 << 10
	MaxAssignmentSecretBytes      = 1 << 20
)

// ValidateSecretBundle applies the same admission limits on both controller and
// generator boundaries. Errors identify only the logical name or index and
// never include credential values, lengths, hashes, or excerpts.
func ValidateSecretBundle(bundle map[string]string) error {
	if len(bundle) > MaxAssignmentSecrets {
		return fmt.Errorf("secret bundle exceeds %d entries", MaxAssignmentSecrets)
	}
	total := 0
	for name, value := range bundle {
		name = strings.TrimSpace(name)
		if name == "" || len(name) > MaxAssignmentSecretNameBytes || !validSecretBundleName(name) {
			return fmt.Errorf("secret bundle contains an invalid logical name")
		}
		if value == "" || len(value) > MaxAssignmentSecretValueBytes {
			return fmt.Errorf("secret bundle value for %q is empty or exceeds the runtime limit", name)
		}
		total += len(name) + len(value)
		if total > MaxAssignmentSecretBytes {
			return fmt.Errorf("secret bundle exceeds %d bytes", MaxAssignmentSecretBytes)
		}
	}
	return nil
}

func validSecretBundleName(name string) bool {
	return engine.ValidLogicalSecretName(name)
}

func cloneSecretBundle(bundle map[string]string) map[string]string {
	if len(bundle) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(bundle))
	for name, value := range bundle {
		cloned[name] = value
	}
	return cloned
}

func secretBundleValues(bundle map[string]string) []string {
	if len(bundle) == 0 {
		return nil
	}
	values := make([]string, 0, len(bundle))
	for _, value := range bundle {
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func secretBundleResolver(bundle map[string]string) engine.SecretResolver {
	if len(bundle) == 0 {
		return nil
	}
	values := cloneSecretBundle(bundle)
	return engine.SecretResolverFunc(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
}

func validateAssignmentSecretReferences(referenced []string, bundle map[string]string) error {
	referenceSet := make(map[string]struct{}, len(referenced))
	for _, name := range referenced {
		referenceSet[name] = struct{}{}
		if _, ok := bundle[name]; !ok {
			return fmt.Errorf("secret bundle is missing referenced logical secret %q", name)
		}
	}
	for name := range bundle {
		if _, ok := referenceSet[name]; !ok {
			return fmt.Errorf("secret bundle contains an unreferenced logical secret")
		}
	}
	return nil
}
