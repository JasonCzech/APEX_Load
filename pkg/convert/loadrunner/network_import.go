package loadrunner

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var hostPortPattern = regexp.MustCompile(`(?i)([a-z0-9][a-z0-9.-]*|\d{1,3}(?:\.\d{1,3}){3}):(\d{1,5})`)

func ConvertNetwork(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	subprotocol := detected.CanonicalLoadRunnerProtocol
	if subprotocol == "" || subprotocol == "network" {
		subprotocol = detectNetworkSubprotocolFromActions(actions)
	}
	if subprotocol == "" {
		subprotocol = "tcp"
	}

	result := &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script: &dsl.Script{
			Name:          firstNonEmpty(opts.ScriptName, "Converted_LR_Network_Script"),
			Version:       "1.0.0",
			Protocol:      "network",
			GeneratedBy:   "apexload-loadrunner-converter",
			GeneratedFrom: "loadrunner",
			Config: dsl.ScriptConfig{
				Network: dsl.NetworkConfig{
					Subprotocol: subprotocol,
					Host:        "{{network.host}}",
					Address:     "{{network.address}}",
					Port:        defaultNetworkPort(subprotocol),
					TimeoutMs:   30000,
				},
			},
			Actions: make([]dsl.Action, 0),
		},
	}

	functions := flattenParsedFunctions(actions)
	result.Stats.TotalFunctions = len(functions)
	if len(functions) == 0 {
		return nil, fmt.Errorf("no LoadRunner functions found in action files")
	}

	for _, fn := range functions {
		action, ok := convertNetworkFunction(fn, subprotocol)
		if !ok {
			if fallback, mode, fallbackOK := convertLoadRunnerFunctionAction(fn); fallbackOK {
				appendLoadRunnerAction(result, fn, fallback, mode, loadRunnerSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable network mapping exists for this LoadRunner function yet."))
			continue
		}
		actionIndex := actionCountForPhase(result.Script, fn.Phase)
		appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(fn.Phase, actionIndex), action.Name, "action", "converted", "Converted to a generated APEX Load network action.", LoadRunnerSupportExecutable))
		appendActionForPhase(result.Script, fn.Phase, action)
		result.Stats.ConvertedActions++
		recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)
	}

	if len(result.Script.Init)+len(result.Script.Actions)+len(result.Script.Teardown) == 0 {
		result.Script = nil
		result.ReviewArtifact = NewReviewArtifact(markDetectedReviewOnly(detected, "No executable network commands were found in the network script."), nil, actions, map[string]any{
			"reason":      "no_network_actions",
			"subprotocol": subprotocol,
		})
		result.Warnings = append(result.Warnings, "No executable network commands were found; import was preserved as a review artifact.")
		return result, nil
	}

	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}
	result.Warnings = append(result.Warnings, "Network endpoints were imported with {{network.*}} placeholders; replace them with governed endpoint configuration before replay.")
	return result, nil
}

func convertNetworkFunction(fn ParsedFunction, defaultSubprotocol string) (dsl.Action, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	subprotocol := defaultSubprotocol
	if derived := networkSubprotocolFromFunctionName(name); derived != "" {
		subprotocol = derived
	}
	target := firstNetworkTarget(fn, subprotocol)
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "network_action")),
		Target:   target,
		Metadata: sourceFunctionMetadata(fn),
	}
	switch subprotocol {
	case "dns":
		action.Type = "dns_query"
		action.Params = map[string]any{"record_type": firstNonEmpty(networkArg(fn, "RecordType"), "A")}
	case "ftp":
		action.Type = "ftp_command"
		action.Value = firstNonEmpty(networkCommandValue(fn), "NOOP")
	case "smtp":
		action.Type = "smtp_send"
		action.Target = firstNonEmpty(target, "{{network.to}}")
		action.Body = firstNonEmpty(networkArg(fn, "Body"), networkCommandValue(fn), "Imported SMTP message body")
	case "pop3":
		action.Type = "pop3_command"
		action.Value = firstNonEmpty(networkCommandValue(fn), "NOOP")
	case "imap":
		action.Type = "imap_command"
		action.Value = firstNonEmpty(networkCommandValue(fn), "NOOP")
	case "ldap":
		if strings.Contains(name, "search") {
			action.Type = "ldap_search"
			action.Target = firstNonEmpty(target, networkArg(fn, "BaseDN"), "{{network.base_dn}}")
		} else {
			action.Type = "ldap_bind"
		}
	case "udp":
		action.Type = "udp_request"
		action.Body = firstNonEmpty(networkCommandValue(fn), fn.Body, "ping")
	case "winsock", "tcp":
		action.Type = "tcp_request"
		action.Body = firstNonEmpty(networkCommandValue(fn), fn.Body, "ping")
	default:
		return dsl.Action{}, false
	}
	if strings.TrimSpace(action.Target) == "" {
		switch action.Type {
		case "dns_query":
			action.Target = "{{network.host}}"
		case "ldap_bind":
			action.Target = ""
		default:
			action.Target = networkPlaceholderTarget(subprotocol)
		}
	}
	return action, true
}

func detectNetworkSubprotocolFromActions(actions []*ParsedAction) string {
	counts := map[string]int{}
	for _, fn := range flattenParsedFunctions(actions) {
		counts[strings.ToLower(fn.Name)]++
	}
	if subprotocol, ok := networkSubprotocolFromFunctions(counts); ok {
		return subprotocol
	}
	return ""
}

func networkSubprotocolFromFunctionName(name string) string {
	switch {
	case strings.HasPrefix(name, "dns_"):
		return "dns"
	case strings.HasPrefix(name, "ftp_"):
		return "ftp"
	case strings.HasPrefix(name, "smtp_"):
		return "smtp"
	case strings.HasPrefix(name, "pop3_"):
		return "pop3"
	case strings.HasPrefix(name, "imap_"):
		return "imap"
	case strings.HasPrefix(name, "ldap_"):
		return "ldap"
	case strings.HasPrefix(name, "lrs_"), strings.HasPrefix(name, "winsock_"):
		return "winsock"
	default:
		return ""
	}
}

func firstNetworkTarget(fn ParsedFunction, subprotocol string) string {
	for _, key := range []string{"URL", "Host", "Server", "Address", "Target", "Domain", "DN", "BaseDN"} {
		if value := networkArg(fn, key); value != "" {
			if subprotocol == "winsock" || subprotocol == "tcp" || subprotocol == "udp" {
				return normalizeSocketTarget(value)
			}
			return value
		}
	}
	for _, raw := range fn.RawArgs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if subprotocol == "dns" && looksLikeDomain(raw) {
			return raw
		}
		if subprotocol == "winsock" || subprotocol == "tcp" || subprotocol == "udp" {
			if target := normalizeSocketTarget(raw); target != "" {
				return target
			}
		}
		if strings.Contains(strings.ToLower(raw), "host=") {
			parts := strings.SplitN(raw, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return ""
}

func networkArg(fn ParsedFunction, key string) string {
	for argKey, value := range fn.Args {
		if strings.EqualFold(argKey, key) {
			return strings.TrimSpace(value)
		}
	}
	prefix := strings.ToLower(key) + "="
	for _, raw := range fn.RawArgs {
		if strings.HasPrefix(strings.ToLower(raw), prefix) {
			return strings.TrimSpace(raw[len(prefix):])
		}
	}
	return ""
}

func networkCommandValue(fn ParsedFunction) string {
	for _, key := range []string{"Command", "Data", "Payload", "Message", "Body"} {
		if value := networkArg(fn, key); value != "" {
			return value
		}
	}
	for _, raw := range fn.RawArgs {
		lower := strings.ToLower(strings.TrimSpace(raw))
		if lower == "" || strings.Contains(lower, "host=") || strings.Contains(lower, "server=") || strings.Contains(lower, "url=") {
			continue
		}
		return raw
	}
	return ""
}

func normalizeSocketTarget(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		value = strings.TrimPrefix(value, "tcp://")
		value = strings.TrimPrefix(value, "udp://")
	}
	if host, port, err := net.SplitHostPort(value); err == nil && host != "" && port != "" {
		return net.JoinHostPort(host, port)
	}
	if match := hostPortPattern.FindStringSubmatch(value); len(match) == 3 {
		return net.JoinHostPort(match[1], match[2])
	}
	return ""
}

func looksLikeDomain(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, ".") && !strings.ContainsAny(value, " /\\")
}

func networkPlaceholderTarget(subprotocol string) string {
	port := defaultNetworkPort(subprotocol)
	if port > 0 {
		return "{{network.address}}:" + strconv.Itoa(port)
	}
	return "{{network.address}}"
}

func defaultNetworkPort(subprotocol string) int {
	switch strings.ToLower(strings.TrimSpace(subprotocol)) {
	case "dns":
		return 53
	case "ftp":
		return 21
	case "smtp":
		return 25
	case "pop3":
		return 110
	case "imap":
		return 143
	case "ldap":
		return 389
	case "udp", "tcp", "winsock":
		return 0
	default:
		return 0
	}
}
