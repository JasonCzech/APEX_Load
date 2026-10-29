package engine

import (
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Browser diagnostic fields are bounded before applying any runtime-owned
// policy. Failure always discards the entire field, never a raw prefix.
const maxBoundedRuntimeDiagnosticTextBytes = 16 << 10

// snapshotBoundedRuntimeDiagnosticText gives a protocol a single immutable
// policy for all fields in an evidence batch. This form is for immutable
// pre-VUser providers; VUsers use the versioned publication fence below.
func snapshotBoundedRuntimeDiagnosticText(provider func() *RedactionConfig) (project protocol.BoundedTextRedactor, guard protocol.BoundedTextRedactorCommitGuard) {
	defer func() {
		if recover() != nil {
			project, guard = nil, nil
		}
	}()
	if provider == nil {
		return nil, nil
	}
	config := provider()
	project = func(value string) (string, bool) {
		return boundedRuntimeDiagnosticText(value, func() *RedactionConfig { return config })
	}
	guard = func(commit func() bool) (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		return commit != nil && provider() == config && commit()
	}
	return project, guard
}

// snapshotVersionedBoundedRuntimeDiagnosticText uses publication identity
// rather than configuration pointer identity, so a transient policy change
// followed by restoring the original configuration still invalidates a batch.
func snapshotVersionedBoundedRuntimeDiagnosticText(snapshot func() (*RedactionConfig, func(func() bool) bool)) (project protocol.BoundedTextRedactor, guard protocol.BoundedTextRedactorCommitGuard) {
	defer func() {
		if recover() != nil {
			project, guard = nil, nil
		}
	}()
	if snapshot == nil {
		return nil, nil
	}
	config, commitGuard := snapshot()
	if commitGuard == nil {
		return nil, nil
	}
	project = func(value string) (string, bool) {
		return boundedRuntimeDiagnosticText(value, func() *RedactionConfig { return config })
	}
	guard = func(commit func() bool) (ok bool) {
		defer func() {
			if recover() != nil {
				ok = false
			}
		}()
		return commitGuard(commit)
	}
	return project, guard
}

// boundedRuntimeDiagnosticText applies immutable execution matchers before and
// after each non-exact projection. It never calls a protocol-supplied callback.
// A panicking provider or malformed/unavailable policy must fail closed.
func boundedRuntimeDiagnosticText(value string, provider func() *RedactionConfig) (out string, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = "", false
		}
	}()
	if provider == nil || len(value) > maxBoundedRuntimeDiagnosticTextBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", false
	}
	config := provider()
	p := &replayRuntimeProjector{}
	if config != nil && (len(config.compiled) > MaxRedactionRules || len(config.Rules) != len(config.compiled)) {
		return "", false
	}
	for depth, layer := 0, config; layer != nil; depth, layer = depth+1, layer.runtimeParent {
		if depth >= maxRuntimeSecretRedactionLayers || layer.runtimePublicationUnavailable || len(layer.secretReplacers) > maxRuntimeSecretRedactionReplacers || (len(layer.runtimeSecretValues) > 0 && len(layer.secretReplacers) == 0) {
			return "", false
		}
		for _, replacer := range layer.secretReplacers {
			if replacer == nil {
				return "", false
			}
		}
		p.configs = append(p.configs, layer)
	}
	value, ok = p.exact(value)
	if !ok {
		return "", false
	}
	if config != nil {
		for _, pattern := range config.compiled {
			value, ok = boundedRuntimePattern(p, value, pattern)
			if !ok {
				return "", false
			}
		}
	}
	value, ok = p.exact(value)
	if !ok || !p.charge(128*(len(value)+1)) {
		return "", false
	}
	value = scrub.Value(value, scrub.Policy{})
	// The general scrubber can introduce a fresh occurrence of a known secret.
	// Reject an enlarged result before the final bounded exact-matcher pass.
	if len(value) > maxBoundedRuntimeDiagnosticTextBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", false
	}
	value, ok = p.exact(value)
	if !ok || len(value) > maxBoundedRuntimeDiagnosticTextBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return "", false
	}
	return strings.Clone(value), true
}

func boundedRuntimePattern(p *replayRuntimeProjector, value string, pattern payloadReplacement) (string, bool) {
	if pattern.re == nil || len(pattern.re.String()) > MaxRedactionPatternBytes || len(pattern.repl) > MaxRedactionReplacementBytes || !p.charge((len(value)+1)*(len(pattern.re.String())+1)) {
		return "", false
	}
	const maxMatches = 256
	matches := pattern.re.FindAllStringSubmatchIndex(value, maxMatches+1)
	if len(matches) > maxMatches {
		return "", false
	}
	var out strings.Builder
	last := 0
	for _, match := range matches {
		// ExpandString can multiply capture contents. Bound both work and its
		// potential allocation before asking regexp to perform the expansion.
		bound := len(pattern.repl)
		if strings.Contains(pattern.repl, "$") {
			// Every captured group is contained in the full match. Charge the
			// maximum possible expansion for that match, not the entire input;
			// ordinary short substitutions in a long URL remain usable.
			bound = (len(pattern.repl) + 1) * (match[1] - match[0] + 1)
		}
		if bound > maxBoundedRuntimeDiagnosticTextBytes || !p.charge(bound) {
			return "", false
		}
		replacement := pattern.re.ExpandString(nil, pattern.repl, value, match)
		if out.Len()+match[0]-last+len(replacement) > maxBoundedRuntimeDiagnosticTextBytes {
			return "", false
		}
		out.WriteString(value[last:match[0]])
		out.Write(replacement)
		last = match[1]
	}
	if out.Len()+len(value)-last > maxBoundedRuntimeDiagnosticTextBytes {
		return "", false
	}
	out.WriteString(value[last:])
	return out.String(), true
}
