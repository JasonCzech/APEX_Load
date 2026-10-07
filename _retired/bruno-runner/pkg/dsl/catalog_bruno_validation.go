package dsl

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var brunoSecretReferencePattern = regexp.MustCompile(`^\{\{\s*secret\.[A-Za-z0-9_][A-Za-z0-9_.:/@-]{0,248}\s*\}\}$`)

func validateBrunoConfigIssues(cfg BrunoConfig) []string {
	var issues []string
	if strings.TrimSpace(cfg.BundleRef) == "" {
		issues = append(issues, "config.bruno.bundle_ref is required")
	}
	if digest := strings.TrimSpace(cfg.BundleSHA256); digest == "" {
		issues = append(issues, "config.bruno.bundle_sha256 is required")
	} else if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(strings.ToLower(digest)) {
		issues = append(issues, "config.bruno.bundle_sha256 must be a lowercase SHA-256 digest")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Sandbox)) {
	case "", "safe", "developer":
	default:
		issues = append(issues, "config.bruno.sandbox must be safe or developer")
	}
	if cfg.Limits.MaxJumps < 0 || cfg.Limits.MaxJumps > 1_000_000 {
		issues = append(issues, "config.bruno.limits.max_jumps must be between 0 and 1000000")
	}
	if cfg.Limits.MaxRunRequestDepth < 0 || cfg.Limits.MaxRunRequestDepth > 1024 {
		issues = append(issues, "config.bruno.limits.max_run_request_depth must be between 0 and 1024")
	}
	if cfg.Limits.MaxParallelRequests < 0 || cfg.Limits.MaxParallelRequests > 100_000 {
		issues = append(issues, "config.bruno.limits.max_parallel_requests must be between 0 and 100000")
	}
	if cfg.Limits.MaxOutputBytes < 0 || cfg.Limits.MaxOutputBytes > 1_073_741_824 {
		issues = append(issues, "config.bruno.limits.max_output_bytes must be between 0 and 1073741824")
	}
	if cfg.Limits.DefaultStreamTimeoutMs < 0 || cfg.Limits.DefaultStreamTimeoutMs > 86_400_000 {
		issues = append(issues, "config.bruno.limits.default_stream_timeout_ms must be between 0 and 86400000")
	}
	bindingNames := make([]string, 0, len(cfg.SecretBindings))
	for name := range cfg.SecretBindings {
		bindingNames = append(bindingNames, name)
	}
	sort.Strings(bindingNames)
	for _, name := range bindingNames {
		if !brunoSecretReferencePattern.MatchString(strings.TrimSpace(cfg.SecretBindings[name])) {
			issues = append(issues, fmt.Sprintf(
				"config.bruno.secret_bindings[%q] must be an exact {{secret.NAME}} reference",
				name,
			))
		}
	}
	return issues
}
