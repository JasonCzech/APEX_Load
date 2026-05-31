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
		action, mode, ok := convertNetworkFunction(fn, subprotocol)
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
		appendConvertedProtocolAction(result, fn, action, mode, "Converted to a generated APEX Load network action.")
	}

	if len(result.Script.Actions) == 0 {
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

func convertNetworkFunction(fn ParsedFunction, defaultSubprotocol string) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	subprotocol := defaultSubprotocol
	if derived := networkSubprotocolFromFunctionName(name); derived != "" {
		subprotocol = derived
	}
	if strings.HasPrefix(name, "lrs_") {
		return convertWinsockFunction(fn, subprotocol)
	}
	if strings.HasPrefix(name, "ftp_") {
		return convertFTPFunction(fn)
	}
	if strings.HasPrefix(name, "smtp_") {
		return convertSMTPFunction(fn)
	}
	if strings.HasPrefix(name, "pop3_") {
		return convertMailCommandFunction(fn, "pop3_command", "STAT")
	}
	if strings.HasPrefix(name, "imap_") {
		return convertMailCommandFunction(fn, "imap_command", "CAPABILITY")
	}
	if strings.HasPrefix(name, "ldap_") {
		return convertLDAPFunction(fn)
	}
	if strings.HasPrefix(name, "dns_") {
		return convertDNSFunction(fn)
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
		return dsl.Action{}, "", false
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
	return action, LoadRunnerSupportExecutable, true
}

func convertWinsockFunction(fn ParsedFunction, subprotocol string) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	networkName := "tcp"
	if subprotocol == "udp" {
		networkName = "udp"
	}
	target := firstNonEmpty(firstNetworkTarget(fn, networkName), networkPlaceholderTarget(networkName))
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "winsock_action")),
		Target:   target,
		Metadata: sourceFunctionMetadata(fn),
		Params:   map[string]any{"function": fn.Name, "subprotocol": networkName},
	}
	switch name {
	case "lrs_create_socket":
		action.Type = networkName + "_request"
		action.Params["connect_only"] = true
	case "lrs_send":
		action.Type = networkName + "_send"
		action.Body = firstNonEmpty(networkCommandValue(fn), fn.Body)
	case "lrs_receive", "lrs_receive_ex", "lrs_length_receive":
		action.Type = networkName + "_request"
		action.Params["read"] = true
		if readBytes := networkReadBytes(fn); readBytes > 0 {
			action.Params["read_bytes"] = readBytes
		}
	case "lrs_save_param", "lrs_save_param_ex", "lrs_save_searched_string":
		return networkNativeAction(fn, "loadrunner_network"), supportModeForFunction(fn, LoadRunnerSupportExecutable), true
	case "lrs_close_socket", "lrs_cleanup", "lrs_startup", "lrs_get_socket_handler", "lrs_set_socket_handler", "lrs_set_send_buffer", "lrs_set_receive_option", "lrs_disable_socket", "lrs_enable_socket":
		return networkNativeAction(fn, "loadrunner_network"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	default:
		return dsl.Action{}, "", false
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertDNSFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "dns_query")),
		Type:     "dns_query",
		Target:   firstNonEmpty(firstNetworkTarget(fn, "dns"), networkArg(fn, "Domain"), "{{network.host}}"),
		Params:   map[string]any{"record_type": firstNonEmpty(networkArg(fn, "RecordType"), networkArg(fn, "Type"), "A")},
		Metadata: sourceFunctionMetadata(fn),
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertFTPFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	target := firstNonEmpty(firstNetworkTarget(fn, "ftp"), networkPlaceholderTarget("ftp"))
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "ftp_action")),
		Type:     "ftp_command",
		Target:   target,
		Metadata: sourceFunctionMetadata(fn),
		Params:   credentialParams(fn),
	}
	switch name {
	case "ftp_logon", "ftp_login", "ftp_connect":
		action.Type = "ftp_login"
		action.Value = "NOOP"
	case "ftp_logout", "ftp_disconnect":
		action.Value = "QUIT"
	case "ftp_noop":
		action.Type = "ftp_noop"
		action.Value = "NOOP"
	default:
		action.Value = firstNonEmpty(networkCommandValue(fn), ftpCommandFromFunction(fn))
	}
	if action.Value == "" {
		action.Value = "NOOP"
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertSMTPFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	if name == "smtp_logon" || name == "smtp_logout" || name == "smtp_connect" || name == "smtp_disconnect" {
		return networkNativeAction(fn, "loadrunner_network"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	}
	params := credentialParams(fn)
	if from := firstNonEmpty(networkArg(fn, "From"), networkArg(fn, "MailFrom")); from != "" {
		params["from"] = from
	}
	if to := firstNonEmpty(networkArg(fn, "To"), networkArg(fn, "RcptTo"), networkArg(fn, "Recipients")); to != "" {
		params["to"] = splitNetworkList(to)
	}
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "smtp_send")),
		Type:     "smtp_send",
		Target:   firstNonEmpty(firstNetworkTarget(fn, "smtp"), networkArg(fn, "To"), "{{network.to}}"),
		Body:     firstNonEmpty(networkArg(fn, "Body"), networkArg(fn, "Message"), networkCommandValue(fn), "Imported SMTP message body"),
		Params:   params,
		Metadata: sourceFunctionMetadata(fn),
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertMailCommandFunction(fn ParsedFunction, actionType string, fallbackCommand string) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	if strings.Contains(name, "logon") || strings.Contains(name, "login") || strings.Contains(name, "logout") || strings.Contains(name, "connect") || strings.Contains(name, "disconnect") {
		return networkNativeAction(fn, "loadrunner_network"), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	}
	subprotocol := strings.TrimSuffix(strings.SplitN(actionType, "_", 2)[0], "_")
	params := credentialParams(fn)
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, actionType)),
		Type:     actionType,
		Target:   firstNonEmpty(firstNetworkTarget(fn, subprotocol), networkPlaceholderTarget(subprotocol)),
		Value:    firstNonEmpty(networkCommandValue(fn), mailCommandFromFunction(fn), fallbackCommand),
		Params:   params,
		Metadata: sourceFunctionMetadata(fn),
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertLDAPFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	params := credentialParams(fn)
	if filter := networkArg(fn, "Filter"); filter != "" {
		params["filter"] = filter
	}
	if attrs := networkArg(fn, "Attributes"); attrs != "" {
		params["attributes"] = splitNetworkList(attrs)
	}
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "ldap_action")),
		Target:   firstNonEmpty(firstNetworkTarget(fn, "ldap"), networkArg(fn, "BaseDN"), "{{network.base_dn}}"),
		Params:   params,
		Metadata: sourceFunctionMetadata(fn),
	}
	if strings.Contains(name, "search") || strings.Contains(name, "query") {
		action.Type = "ldap_search"
	} else if strings.Contains(name, "bind") || strings.Contains(name, "logon") || strings.Contains(name, "login") {
		action.Type = "ldap_bind"
		action.Target = firstNetworkTarget(fn, "ldap")
	} else {
		return networkNativeAction(fn, "loadrunner_network"), supportModeForFunction(fn, LoadRunnerSupportReviewOnly), true
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func networkNativeAction(fn ParsedFunction, actionType string) dsl.Action {
	def, ok := lookupLoadRunnerFunction(fn.Name)
	if !ok {
		def = LoadRunnerFunctionDefinition{Name: strings.ToLower(fn.Name), Family: "network", SupportMode: LoadRunnerSupportNativeNoop, Reason: defaultLoadRunnerReason("network", LoadRunnerSupportNativeNoop)}
	}
	return convertGenericLoadRunnerAction(fn, actionType, def)
}

func networkReadBytes(fn ParsedFunction) int {
	for _, key := range []string{"Length", "Bytes", "ReadBytes", "Size"} {
		if value := networkArg(fn, key); value != "" {
			if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				return parsed
			}
		}
	}
	for _, arg := range loadRunnerControlArgs(fn) {
		if parsed, err := strconv.Atoi(strings.TrimSpace(arg)); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

func credentialParams(fn ParsedFunction) map[string]any {
	params := map[string]any{}
	if username := firstNonEmpty(networkArg(fn, "User"), networkArg(fn, "Username"), networkArg(fn, "UserName")); username != "" {
		params["username"] = username
	}
	if password := networkArg(fn, "Password"); password != "" {
		params["password"] = password
	}
	return params
}

func ftpCommandFromFunction(fn ParsedFunction) string {
	name := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fn.Name)), "ftp_")
	args := loadRunnerControlArgs(fn)
	firstArg := ""
	if len(args) > 0 {
		firstArg = args[0]
	}
	secondArg := ""
	if len(args) > 1 {
		secondArg = args[1]
	}
	switch name {
	case "get", "retrieve":
		return strings.TrimSpace("RETR " + firstArg)
	case "put", "store", "upload":
		return strings.TrimSpace("STOR " + firstArg)
	case "delete", "del":
		return strings.TrimSpace("DELE " + firstArg)
	case "mkdir":
		return strings.TrimSpace("MKD " + firstArg)
	case "rmdir":
		return strings.TrimSpace("RMD " + firstArg)
	case "cd", "cwd":
		return strings.TrimSpace("CWD " + firstArg)
	case "pwd":
		return "PWD"
	case "dir", "list", "ls":
		return strings.TrimSpace(firstNonEmpty("LIST "+firstArg, "LIST"))
	case "rename":
		return strings.TrimSpace("RNFR " + firstArg + "\r\nRNTO " + secondArg)
	case "ascii":
		return "TYPE A"
	case "binary":
		return "TYPE I"
	case "site":
		return strings.TrimSpace("SITE " + firstArg)
	default:
		return strings.ToUpper(strings.TrimSpace(networkCommandValue(fn)))
	}
}

func mailCommandFromFunction(fn ParsedFunction) string {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	command := strings.TrimPrefix(strings.TrimPrefix(name, "pop3_"), "imap_")
	args := loadRunnerControlArgs(fn)
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch command {
	case "retrieve", "fetch", "read":
		if strings.HasPrefix(name, "imap_") {
			return strings.TrimSpace("FETCH " + firstNonEmpty(arg, "1") + " BODY[]")
		}
		return strings.TrimSpace("RETR " + firstNonEmpty(arg, "1"))
	case "delete", "del":
		if strings.HasPrefix(name, "imap_") {
			return strings.TrimSpace("STORE " + firstNonEmpty(arg, "1") + " +FLAGS (\\Deleted)")
		}
		return strings.TrimSpace("DELE " + firstNonEmpty(arg, "1"))
	case "list", "dir":
		if strings.HasPrefix(name, "imap_") {
			return "LIST \"\" *"
		}
		return "LIST"
	case "select":
		return strings.TrimSpace("SELECT " + firstNonEmpty(arg, "INBOX"))
	case "search":
		return strings.TrimSpace("SEARCH " + firstNonEmpty(arg, "ALL"))
	default:
		return strings.ToUpper(strings.TrimSpace(networkCommandValue(fn)))
	}
}

func splitNetworkList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	if len(out) == 0 && strings.TrimSpace(value) != "" {
		return []string{strings.TrimSpace(value)}
	}
	return out
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
	for _, arg := range fn.CallArgs {
		if strings.EqualFold(arg.Name, key) {
			return strings.TrimSpace(arg.Value)
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
