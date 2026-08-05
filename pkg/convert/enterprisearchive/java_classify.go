package enterprisearchive

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	// A bare .send() or .execute() is shared by many non-HTTP APIs. These
	// expressions are therefore used only after the receiver has been proven to
	// be an HTTP client by a declaration or a recognized client factory.
	javaHTTPSendCallRe              = regexp.MustCompile(`(?i)^send(?:async)?$`)
	javaHTTPExecuteCallRe           = regexp.MustCompile(`(?i)^execute$`)
	javaHTTPBuiltClientAssignmentRe = regexp.MustCompile(
		`(?is)\b([a-z_$][a-z0-9_$]*)\s*=\s*(?:(?:java\s*\.\s*net\s*\.\s*http\s*\.\s*)?httpclient\s*\.\s*newbuilder|httpclients\s*\.\s*custom|httpclientbuilder\s*\.\s*create)\s*\([^;{}]*?\)(?:\s*\.\s*[a-z_$][a-z0-9_$]*\s*\([^;{}]*?\))*\s*\.\s*build\s*\(`,
	)
	javaHTTPChainedSendRe = regexp.MustCompile(
		`(?is)(?:(?:(?:java\s*\.\s*net\s*\.\s*http\s*\.\s*)?httpclient\s*\.\s*newbuilder|httpclients\s*\.\s*custom|httpclientbuilder\s*\.\s*create)\s*\([^;{}]*?\)(?:\s*\.\s*[a-z_$][a-z0-9_$]*\s*\([^;{}]*?\))*\s*\.\s*build\s*\([^;{}]*?\)|(?:java\s*\.\s*net\s*\.\s*http\s*\.\s*)?httpclient\s*\.\s*newhttpclient\s*\([^;{}]*?\)|httpclients\s*\.\s*create[a-z0-9_]*\s*\([^;{}]*?\))\s*\.\s*(?:send(?:async)?|execute)\s*\(`,
	)
	javaHTTPFactoryReferenceRe   = regexp.MustCompile(`(?i)\b(?:httpclient|httpclients|httpclientbuilder)\b`)
	javaHTTPURLConstructorCallRe = regexp.MustCompile(
		`(?is)\bnew\s+([a-z_$][a-z0-9_$.]*)\s*\([^;{}]*?\)\s*\.\s*(?:openconnection|openstream)\s*\(`,
	)

	javaProtocolVariableDeclRe = regexp.MustCompile(
		`(?i)\b(?:(?:public|private|protected|static|final|volatile|transient)\s+)*([a-z_$][a-z0-9_$.]*(?:\s*<[^;={}()]+>)?(?:\s*\[\])*)\s+([a-z_$][a-z0-9_$]*)\s*(?:=|;|,|\)|:)`,
	)
	javaProtocolAssignmentCallRe = regexp.MustCompile(
		`(?i)\b([a-z_$][a-z0-9_$]*)\s*=\s*([a-z_$][a-z0-9_$.]*)\s*\.\s*([a-z_$][a-z0-9_$]*)\s*\(`,
	)
	javaProtocolReceiverCallRe = regexp.MustCompile(
		`(?i)\b([a-z_$][a-z0-9_$.]*)\s*\.\s*([a-z_$][a-z0-9_$]*)\s*\(`,
	)
	javaProtocolTypeBindingRe = regexp.MustCompile(
		`(?i)__apexload_protocol_type_([a-z_$][a-z0-9_$]*)_([a-z]+)__`,
	)
	javaProtocolReservedMarkerRe = regexp.MustCompile(
		`(?i)\b(?:__apexload_protocol_scope_boundary__|__apexload_protocol_local_scope_boundary__|__apexload_protocol_block_start__|__apexload_protocol_block_end__)\b`,
	)
	javaProtocolReservedSuperFieldRe = regexp.MustCompile(
		`(?i)\b__apexload_protocol_super_field_[a-z_$][a-z0-9_$]*__\b`,
	)
	javaImplicitLambdaParametersRe = regexp.MustCompile(
		`(?is)(?:\(\s*([a-z_$][a-z0-9_$]*(?:\s*,\s*[a-z_$][a-z0-9_$]*)*)\s*\)|([a-z_$][a-z0-9_$]*))\s*$`,
	)
	javaThisConstructorCallRe       = regexp.MustCompile(`(?i)\bthis\s*\(`)
	javaSuperConstructorCallRe      = regexp.MustCompile(`(?i)\bsuper\s*\(`)
	javaObjectConstructionRe        = regexp.MustCompile(`(?i)\bnew\s+([a-z_$][a-z0-9_$.]*)\s*\(`)
	javaAnonymousClassInitializerRe = regexp.MustCompile(
		`(?is)\bnew\s+[a-z_$][a-z0-9_$.]*(?:\s*<[^;{}]+>)?\s*\([^;{}]*\)\s*\{`,
	)
	javaFinalMethodModifierRe    = regexp.MustCompile(`(?i)\bfinal\b`)
	javaFunctionalAssignedNameRe = regexp.MustCompile(
		`(?i)([a-z_$][a-z0-9_$]*)\s*$`,
	)
	javaFunctionalMethodHeaderRe = regexp.MustCompile(
		`(?is)\b([a-z_$][a-z0-9_$]*)\s*\(([^()]*)\)\s*(?:throws\s+[a-z0-9_$.,\s]+)?$`,
	)
	javaMethodReferenceRe = regexp.MustCompile(
		`(?i)^\s*([a-z_$][a-z0-9_$.]*)\s*::\s*([a-z_$][a-z0-9_$]*|new)\s*$`,
	)
	javaCastReceiverCallRe = regexp.MustCompile(
		`(?i)\(\s*\(\s*([a-z_$][a-z0-9_$.]*(?:\s*<[^;{}()]+>)?(?:\s*\[\])*)\s*\)\s*[a-z_$][a-z0-9_$]*\s*\)\s*\.\s*([a-z_$][a-z0-9_$]*)\s*\(`,
	)
	javaArrayReceiverCallRe = regexp.MustCompile(
		`(?i)\b([a-z_$][a-z0-9_$]*)\s*\[[^;{}\[\]]+\]\s*\.\s*([a-z_$][a-z0-9_$]*)\s*\(`,
	)
	javaParenthesizedReceiverCallRe = regexp.MustCompile(
		`(?i)\(\s*([a-z_$][a-z0-9_$]*)\s*\)\s*\.\s*([a-z_$][a-z0-9_$]*)\s*\(`,
	)
	javaTrailingCastReceiverTypeRe = regexp.MustCompile(
		`(?i)\(\s*\(\s*([a-z_$][a-z0-9_$.]*(?:\s*<[^;{}()]+>)?(?:\s*\[\])*)\s*\)\s*$`,
	)
	javaReturnedReceiverCastTypeRe = regexp.MustCompile(
		`(?i)^[a-z_$][a-z0-9_$.]*(?:\s*<[^;{}()]+>)?(?:\s*\[\s*\])*$`,
	)
)

type javaProtocolReceiverKind uint8

const (
	javaProtocolReceiverUnknown javaProtocolReceiverKind = iota
	javaProtocolReceiverHTTPClient
	javaProtocolReceiverHTTPBuilder
	javaProtocolReceiverJDBCDataSource
	javaProtocolReceiverJDBCConnection
	javaProtocolReceiverJDBCStatement
	javaProtocolReceiverLoadRunnerWeb
	javaProtocolReceiverJMS
	javaProtocolReceiverSOAP
	javaProtocolReceiverHTTPURL
)

const (
	javaProtocolScopeBoundary      = "__apexload_protocol_scope_boundary__;"
	javaProtocolLocalScopeBoundary = "__apexload_protocol_local_scope_boundary__;"
	javaProtocolBlockStart         = "__apexload_protocol_block_start__;"
	javaProtocolBlockEnd           = "__apexload_protocol_block_end__;"
	javaProtocolSuperFieldPrefix   = "__apexload_protocol_super_field_"
	// Expression analysis is deliberately bounded independently of source size.
	// Generated Java can otherwise place millions of receiver hops in one
	// retained statement and evade statement/overload-count limits.
	maxJavaReceiverChainLinks    = 256
	maxJavaMemberReceiverLookups = 4096
	maxJavaTotalMemberLookups    = 1_000_000
	// Static wildcard imports are expanded lazily per distinct unqualified
	// method name. Keep a defensive runtime-analysis ceiling in addition to the
	// overload preflight charge so direct analysis callers cannot perform an
	// imports-by-methods amplification.
	maxJavaStaticImportOwnerLookups = 2_000_000
)

type javaSurface string

const (
	javaSurfaceLR       javaSurface = "lr"
	javaSurfaceHTTP     javaSurface = "http"
	javaSurfaceJMS      javaSurface = "jms"
	javaSurfaceDatabase javaSurface = "database"
	javaSurfaceSOAP     javaSurface = "soap"
	javaSurfaceReview   javaSurface = "review"
)

type JavaProfile struct {
	Surfaces        map[javaSurface]bool
	Primary         javaSurface
	JMSProvider     string
	JMSProtocol     string
	DestinationType string
	DatabaseDriver  string
	ReviewFamilies  []string
}

func classifyJavaArchive(program javaProgram, projectConfig *ProjectConfig, files map[string]string) JavaProfile {
	return classifyJavaArchiveWithReachability(
		program,
		projectConfig,
		files,
		buildJavaVuserRuntimeReachability(program),
	)
}

// classifyJavaArchiveWithReachability keeps live conversion to one full
// lifecycle-reachability pass. Standalone classifier callers retain the wrapper
// above, while ConvertJava threads the same result into classification, source
// mapping, transaction accounting, and promotion gating.
func classifyJavaArchiveWithReachability(
	program javaProgram,
	projectConfig *ProjectConfig,
	files map[string]string,
	reachability javaVuserRuntimeReachability,
) JavaProfile {
	text, contextText := javaExecutableEvidence(program)
	// Literal values and imports may identify a provider or driver, but they
	// must never establish that a protocol was executed. Consult only source
	// owned by the reachable lifecycle graph after executable evidence has
	// selected a surface; unrelated backup classes must not select a provider.
	contextText = strings.ToLower(contextText)
	profile := JavaProfile{
		Surfaces:        map[javaSurface]bool{},
		JMSProvider:     "solace",
		JMSProtocol:     "jms-solace",
		DestinationType: "queue",
	}
	if strings.Contains(text, "lrapi") || strings.Contains(text, "lr.") {
		profile.Surfaces[javaSurfaceLR] = true
	}
	if javaLooksLikeHTTP(text) {
		profile.Surfaces[javaSurfaceHTTP] = true
	}
	if javaLooksLikeJMS(text) {
		profile.Surfaces[javaSurfaceJMS] = true
		profile.JMSProvider, profile.JMSProtocol = inferJavaJMSProvider(contextText)
		profile.DestinationType = inferJavaJMSDestinationType(contextText)
	}
	if javaLooksLikeDatabase(text) {
		profile.Surfaces[javaSurfaceDatabase] = true
		profile.DatabaseDriver = inferJavaJDBCDriver(contextText)
	}
	if javaLooksLikeSOAP(text) {
		profile.Surfaces[javaSurfaceSOAP] = true
	}
	profile.ReviewFamilies = inferJavaReviewFamilies(text)
	if len(program.Unparsed) > 0 {
		profile.ReviewFamilies = appendUnique(profile.ReviewFamilies, "unparsed_java_region")
	}
	if javaReachabilityHasUnknownLR(reachability) {
		profile.ReviewFamilies = appendUnique(profile.ReviewFamilies, "unrecognized_lr_method")
	}
	if len(profile.ReviewFamilies) > 0 {
		profile.Surfaces[javaSurfaceReview] = true
	}

	switch {
	case profile.Surfaces[javaSurfaceHTTP]:
		profile.Primary = javaSurfaceHTTP
	case profile.Surfaces[javaSurfaceJMS]:
		profile.Primary = javaSurfaceJMS
	case profile.Surfaces[javaSurfaceDatabase]:
		profile.Primary = javaSurfaceDatabase
	case profile.Surfaces[javaSurfaceSOAP]:
		profile.Primary = javaSurfaceSOAP
	case profile.Surfaces[javaSurfaceReview]:
		profile.Primary = javaSurfaceReview
	default:
		profile.Primary = javaSurfaceLR
	}
	if projectConfig != nil {
		if profileForProject, _, ok := profileForProjectConfig(projectConfig); ok && profileForProject.Canonical == "java_jms_solace" {
			profile.Surfaces[javaSurfaceJMS] = true
			if !javaHasNonSolaceJMSProviderEvidence(contextText) {
				profile.JMSProvider = "solace"
				profile.JMSProtocol = "jms-solace"
			}
			profile.Primary = javaSurfaceJMS
		}
	}
	return profile
}

func javaProgramHasUnknownLR(program javaProgram) bool {
	return javaReachabilityHasUnknownLR(buildJavaVuserRuntimeReachability(program))
}

func javaReachabilityHasUnknownLR(reachability javaVuserRuntimeReachability) bool {
	for _, reachable := range reachability.statements {
		if len(reachable.phases) == 0 {
			if javaStatementHasUnknownLR(reachable.statement, "action") {
				return true
			}
			continue
		}
		for phase := range reachable.phases {
			if javaStatementHasUnknownLR(reachable.statement, phase) {
				return true
			}
		}
	}
	return false
}

func javaStatementHasUnknownLR(statement javaStatement, phase string) bool {
	if statement.Kind == "block" {
		for _, child := range statement.Children {
			if javaStatementHasUnknownLR(child, phase) {
				return true
			}
		}
		return false
	}
	fn, ok := javaLRToParsedFunction(statement, phase)
	if !ok {
		return false
	}
	_, known := lookupSourceFunction(fn.Name)
	return !known
}

func javaLooksLikeHTTP(text string) bool {
	code := javaCodeOnly(text)
	directCode := javaHTTPDirectEvidenceCode(code)
	if javaHTTPChainedSendRe.MatchString(directCode) ||
		javaHTTPURLConstructorCall(code) {
		return true
	}

	return javaHasProtocolReceiverCall(
		code,
		inferJavaHTTPReceiverKinds,
		func(kind javaProtocolReceiverKind, method string) bool {
			if kind == javaProtocolReceiverLoadRunnerWeb {
				switch method {
				case "url", "custom_request", "customrequest", "submit_data", "submitdata",
					"submit_form", "submitform", "add_cookie", "addcookie",
					"add_header", "addheader", "add_auto_header", "addautoheader",
					"remove_auto_header", "removeautoheader", "reg_find", "regfind",
					"reg_save_param", "regsaveparam", "set_sockets_option", "setsocketsoption",
					"set_user", "setuser", "cleanup_cookies", "cleanupcookies",
					"cache_cleanup", "cachecleanup", "convert_param", "convertparam":
					return true
				default:
					return false
				}
			}
			if kind == javaProtocolReceiverHTTPURL {
				switch method {
				case "openconnection", "openstream", "connect", "getinputstream", "getoutputstream":
					return true
				default:
					return false
				}
			}
			return kind == javaProtocolReceiverHTTPClient &&
				(javaHTTPSendCallRe.MatchString(method) || javaHTTPExecuteCallRe.MatchString(method))
		},
	)
}

func javaHTTPURLConstructorCall(code string) bool {
	for _, scope := range strings.Split(code, javaProtocolScopeBoundary) {
		overrides := javaProtocolTypeOverrides(scope)
		for _, match := range javaHTTPURLConstructorCallRe.FindAllStringSubmatch(scope, -1) {
			if len(match) == 2 &&
				javaProtocolKindForType(match[1], overrides) == javaProtocolReceiverHTTPURL {
				return true
			}
		}
	}
	return false
}

func javaHTTPDirectEvidenceCode(code string) string {
	scopes := strings.Split(code, javaProtocolScopeBoundary)
	for index, scope := range scopes {
		scopes[index] = javaMaskNonStandardHTTPFactoryReferences(scope, javaProtocolTypeOverrides(scope))
	}
	return strings.Join(scopes, javaProtocolScopeBoundary)
}

func javaMaskNonStandardHTTPFactoryReferences(
	code string,
	typeOverrides map[string]javaProtocolReceiverKind,
) string {
	matches := javaHTTPFactoryReferenceRe.FindAllStringIndex(code, -1)
	if len(matches) == 0 {
		return code
	}
	masked := []byte(code)
	for _, match := range matches {
		start := match[0]
		qualifiedStart := start
		for qualifiedStart > 0 {
			previous := code[qualifiedStart-1]
			if isJavaIdentifierByte(previous) || previous == '.' {
				qualifiedStart--
				continue
			}
			break
		}
		if javaHTTPFactoryOwner(code[qualifiedStart:match[1]], typeOverrides) != "" {
			continue
		}
		for index := match[0]; index < match[1]; index++ {
			masked[index] = '_'
		}
	}
	return string(masked)
}

func javaLooksLikeJMS(text string) bool {
	return javaHasProtocolReceiverCall(
		javaCodeOnly(text),
		inferJavaJMSReceiverKinds,
		func(kind javaProtocolReceiverKind, method string) bool {
			if kind != javaProtocolReceiverJMS {
				return false
			}
			switch method {
			case "createcontext", "createconnection", "createqueue", "createtopic", "createproducer", "createconsumer",
				"createtextmessage", "createmessage", "createsession",
				"receive", "receivenowait", "acknowledge", "send", "publish",
				"settext", "setstringproperty":
				return true
			default:
				return false
			}
		},
	)
}

func javaLooksLikeDatabase(text string) bool {
	code := javaCodeOnly(text)
	return javaHasProtocolReceiverCall(
		code,
		inferJavaJDBCReceiverKinds,
		func(kind javaProtocolReceiverKind, method string) bool {
			switch kind {
			case javaProtocolReceiverJDBCDataSource:
				return method == "getconnection"
			case javaProtocolReceiverJDBCConnection:
				switch method {
				case "preparestatement", "createstatement", "preparecall", "setautocommit", "commit", "rollback":
					return true
				}
			case javaProtocolReceiverJDBCStatement:
				switch method {
				case "execute", "executequery", "executeupdate", "executelargeupdate", "executebatch":
					return true
				}
			}
			return false
		},
	)
}

type javaProtocolReceiverCall struct {
	receiver string
	method   string
}

type javaProtocolReceiverInfer func(
	string,
	map[string]javaProtocolReceiverKind,
	map[string]javaProtocolReceiverKind,
	map[string]javaProtocolReceiverKind,
)

type javaProtocolReceiverMatch func(javaProtocolReceiverKind, string) bool

// javaHasProtocolReceiverCall evaluates declarations, factory assignments, and
// calls in source order. Each lifecycle/constructor scope starts with the
// selected Actions fields, but locals can shadow those fields without leaking
// into another method. This prevents an unused protocol-typed field from
// lending its type to a same-named non-protocol local receiver.
func javaHasProtocolReceiverCall(code string, infer javaProtocolReceiverInfer, matches javaProtocolReceiverMatch) bool {
	scopes := strings.Split(code, javaProtocolScopeBoundary)
	if len(scopes) == 1 && !strings.Contains(scopes[0], javaProtocolLocalScopeBoundary) {
		kinds := make(map[string]javaProtocolReceiverKind)
		return javaProtocolScopeHasReceiverCall(scopes[0], kinds, kinds, infer, matches)
	}

	for _, scope := range scopes {
		sections := strings.SplitN(scope, javaProtocolLocalScopeBoundary, 2)
		fieldKinds := make(map[string]javaProtocolReceiverKind)
		if javaProtocolScopeHasReceiverCall(sections[0], fieldKinds, fieldKinds, infer, matches) {
			return true
		}
		if len(sections) == 1 {
			continue
		}
		localKinds := cloneJavaProtocolReceiverKinds(fieldKinds)
		if javaProtocolScopeHasReceiverCall(sections[1], localKinds, fieldKinds, infer, matches) {
			return true
		}
	}
	return false
}

func javaProtocolScopeHasReceiverCall(
	code string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	infer javaProtocolReceiverInfer,
	matches javaProtocolReceiverMatch,
) bool {
	typeOverrides := javaProtocolTypeOverrides(code)
	currentKinds := kinds
	outerKinds := make([]map[string]javaProtocolReceiverKind, 0, 4)
	for _, statement := range strings.Split(code, ";") {
		token := strings.TrimSpace(statement)
		switch token {
		case strings.TrimSuffix(javaProtocolBlockStart, ";"):
			outerKinds = append(outerKinds, currentKinds)
			currentKinds = cloneJavaProtocolReceiverKinds(currentKinds)
			continue
		case strings.TrimSuffix(javaProtocolBlockEnd, ";"):
			if len(outerKinds) > 0 {
				currentKinds = outerKinds[len(outerKinds)-1]
				outerKinds = outerKinds[:len(outerKinds)-1]
			}
			continue
		}
		// strings.Split removes the declaration terminator used by the
		// declaration recognizer, so restore it for each ordered fragment.
		fragment := statement + ";"
		if strings.Contains(fragment, "->") {
			if javaProtocolLambdaHasReceiverCall(fragment, currentKinds, fieldKinds, typeOverrides, infer, matches) {
				return true
			}
			continue
		}
		applyJavaProtocolReceiverDeclarations(fragment, currentKinds, typeOverrides)
		infer(fragment, currentKinds, fieldKinds, typeOverrides)
		for _, call := range javaProtocolReceiverCalls(fragment) {
			if matches(javaProtocolReceiverKindForValue(call.receiver, currentKinds, fieldKinds, typeOverrides), call.method) {
				return true
			}
		}
	}
	return false
}

func applyJavaProtocolReceiverDeclarations(
	code string,
	kinds map[string]javaProtocolReceiverKind,
	typeOverrides map[string]javaProtocolReceiverKind,
) {
	for _, match := range javaProtocolVariableDeclRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(match[2]))
		kinds[name] = javaProtocolKindForType(match[1], typeOverrides)
	}
}

func javaProtocolTypeOverrides(code string) map[string]javaProtocolReceiverKind {
	overrides := make(map[string]javaProtocolReceiverKind)
	for _, match := range javaProtocolTypeBindingRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		kind, ok := javaProtocolReceiverKindForLabel(strings.ToLower(match[2]))
		if ok {
			overrides[strings.ToLower(match[1])] = kind
		}
	}
	return overrides
}

func javaProtocolReceiverKindForLabel(label string) (javaProtocolReceiverKind, bool) {
	switch label {
	case "custom":
		return javaProtocolReceiverUnknown, true
	case "http":
		return javaProtocolReceiverHTTPClient, true
	case "httpbuilder":
		return javaProtocolReceiverHTTPBuilder, true
	case "jdbcsource":
		return javaProtocolReceiverJDBCDataSource, true
	case "jdbcconnection":
		return javaProtocolReceiverJDBCConnection, true
	case "jdbcstatement":
		return javaProtocolReceiverJDBCStatement, true
	case "jms":
		return javaProtocolReceiverJMS, true
	case "soap":
		return javaProtocolReceiverSOAP, true
	case "web":
		return javaProtocolReceiverLoadRunnerWeb, true
	case "httpurl":
		return javaProtocolReceiverHTTPURL, true
	default:
		return javaProtocolReceiverUnknown, false
	}
}

func javaProtocolLambdaHasReceiverCall(
	fragment string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	typeOverrides map[string]javaProtocolReceiverKind,
	infer javaProtocolReceiverInfer,
	matches javaProtocolReceiverMatch,
) bool {
	arrow := strings.Index(fragment, "->")
	if arrow < 0 {
		infer(fragment, kinds, fieldKinds, typeOverrides)
		for _, call := range javaProtocolReceiverCalls(fragment) {
			if matches(javaProtocolReceiverKindForValue(call.receiver, kinds, fieldKinds, typeOverrides), call.method) {
				return true
			}
		}
		return false
	}

	prefix := fragment[:arrow]
	lambdaParameters := javaLambdaParameterKinds(prefix, typeOverrides)
	type savedKind struct {
		kind   javaProtocolReceiverKind
		exists bool
	}
	savedParameters := make(map[string]savedKind, len(lambdaParameters))
	for name := range lambdaParameters {
		kind, exists := kinds[name]
		savedParameters[name] = savedKind{kind: kind, exists: exists}
	}
	applyJavaProtocolReceiverDeclarations(prefix+";", kinds, typeOverrides)
	for name, saved := range savedParameters {
		if saved.exists {
			kinds[name] = saved.kind
		} else {
			delete(kinds, name)
		}
	}
	infer(prefix, kinds, fieldKinds, typeOverrides)
	for _, call := range javaProtocolReceiverCalls(prefix) {
		if matches(javaProtocolReceiverKindForValue(call.receiver, kinds, fieldKinds, typeOverrides), call.method) {
			return true
		}
	}

	lambdaKinds := cloneJavaProtocolReceiverKinds(kinds)
	for name, kind := range lambdaParameters {
		lambdaKinds[name] = kind
	}
	return javaProtocolLambdaHasReceiverCall(
		fragment[arrow+2:],
		lambdaKinds,
		fieldKinds,
		typeOverrides,
		infer,
		matches,
	)
}

func javaLambdaParameterKinds(
	prefix string,
	typeOverrides map[string]javaProtocolReceiverKind,
) map[string]javaProtocolReceiverKind {
	parameters := make(map[string]javaProtocolReceiverKind)
	for _, raw := range javaLambdaParameterSources(prefix) {
		parts := strings.Fields(raw)
		if len(parts) == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(parts[len(parts)-1]))
		name = strings.Trim(name, "[]")
		if name == "" {
			continue
		}
		kind := javaProtocolReceiverUnknown
		if len(parts) > 1 {
			typeName := strings.Join(parts[:len(parts)-1], " ")
			typeName = strings.TrimSpace(strings.TrimPrefix(typeName, "final "))
			kind = javaProtocolKindForType(typeName, typeOverrides)
		}
		parameters[name] = kind
	}
	return parameters
}

func javaLambdaParameterSources(prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	parameterText := ""
	if strings.HasSuffix(prefix, ")") {
		depth := 0
		for index := len(prefix) - 1; index >= 0; index-- {
			switch prefix[index] {
			case ')':
				depth++
			case '(':
				depth--
				if depth == 0 {
					parameterText = prefix[index+1 : len(prefix)-1]
					index = -1
				}
			}
		}
	} else if match := javaImplicitLambdaParametersRe.FindStringSubmatch(prefix); len(match) == 3 {
		if match[1] != "" {
			parameterText = match[1]
		} else {
			parameterText = match[2]
		}
	}
	parameters := make([]string, 0)
	for _, raw := range splitJavaArgs(parameterText) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parameters = append(parameters, raw)
	}
	return parameters
}

func cloneJavaProtocolReceiverKinds(source map[string]javaProtocolReceiverKind) map[string]javaProtocolReceiverKind {
	cloned := make(map[string]javaProtocolReceiverKind, len(source))
	for name, kind := range source {
		cloned[name] = kind
	}
	return cloned
}

func javaProtocolReceiverKindForValue(
	value string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	typeOverrides map[string]javaProtocolReceiverKind,
) javaProtocolReceiverKind {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	if strings.HasPrefix(normalized, "this.") {
		fieldName := strings.TrimPrefix(normalized, "this.")
		if !strings.Contains(fieldName, ".") {
			return fieldKinds[fieldName]
		}
		return javaProtocolReceiverUnknown
	}
	if strings.HasPrefix(normalized, "super.") {
		fieldName := strings.TrimPrefix(normalized, "super.")
		if !strings.Contains(fieldName, ".") {
			return fieldKinds[javaProtocolSuperFieldEvidenceName(fieldName)]
		}
		return javaProtocolReceiverUnknown
	}
	if strings.Contains(normalized, ".") {
		base := javaProtocolReceiverName(normalized)
		if kind, ok := javaKnownQualifiedProtocolKind(normalized, base); ok {
			return kind
		}
		// Do not treat holder.client as the same receiver as a local or field
		// named client. Its declared member type is not available here.
		return javaProtocolReceiverUnknown
	}
	return kinds[normalized]
}

func javaProtocolKindForType(
	typeName string,
	typeOverrides map[string]javaProtocolReceiverKind,
) javaProtocolReceiverKind {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	if generic := strings.IndexByte(normalized, '<'); generic >= 0 {
		normalized = normalized[:generic]
	}
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.TrimSuffix(normalized, "[]")
	if normalized == "httpclient.builder" {
		if kind, ok := typeOverrides["httpclient"]; ok && kind == javaProtocolReceiverUnknown {
			return javaProtocolReceiverUnknown
		}
		return javaProtocolReceiverHTTPBuilder
	}
	base := normalized
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 {
		base = base[dot+1:]
	}
	if strings.Contains(normalized, ".") {
		if kind, ok := javaKnownQualifiedProtocolKind(normalized, base); ok {
			return kind
		}
		return javaProtocolReceiverUnknown
	}
	if kind, ok := typeOverrides[base]; ok {
		return kind
	}
	switch {
	case strings.HasSuffix(normalized, "httpclient.builder"),
		base == "httpclientbuilder",
		base == "httpasyncclientbuilder":
		return javaProtocolReceiverHTTPBuilder
	case base == "httpclient",
		base == "closeablehttpclient",
		base == "httpasyncclient",
		base == "closeablehttpasyncclient":
		return javaProtocolReceiverHTTPClient
	case base == "url",
		base == "urlconnection",
		base == "httpurlconnection":
		return javaProtocolReceiverHTTPURL
	case base == "datasource":
		return javaProtocolReceiverJDBCDataSource
	case base == "connection":
		return javaProtocolReceiverJDBCConnection
	case base == "statement",
		base == "preparedstatement",
		base == "callablestatement":
		return javaProtocolReceiverJDBCStatement
	case base == "jmscontext",
		base == "connectionfactory",
		base == "queueconnectionfactory",
		base == "topicconnectionfactory",
		base == "session",
		base == "queuesession",
		base == "topicsession",
		base == "jmsproducer",
		base == "jmsconsumer",
		base == "messageproducer",
		base == "messageconsumer",
		base == "queuebrowser",
		base == "message",
		base == "textmessage",
		base == "bytesmessage",
		base == "mapmessage",
		base == "objectmessage",
		base == "streammessage":
		return javaProtocolReceiverJMS
	case base == "soapmessage",
		base == "soapconnection",
		base == "soapconnectionfactory",
		base == "messagefactory",
		base == "dispatch",
		base == "service":
		return javaProtocolReceiverSOAP
	default:
		return javaProtocolReceiverUnknown
	}
}

func javaKnownQualifiedProtocolKind(normalized, base string) (javaProtocolReceiverKind, bool) {
	switch {
	case normalized == "java.net.http.httpclient":
		return javaProtocolReceiverHTTPClient, true
	case normalized == "java.net.http.httpclient.builder":
		return javaProtocolReceiverHTTPBuilder, true
	case normalized == "java.net.url",
		normalized == "java.net.urlconnection",
		normalized == "java.net.httpurlconnection":
		return javaProtocolReceiverHTTPURL, true
	case strings.HasPrefix(normalized, "org.apache.http."),
		strings.HasPrefix(normalized, "org.apache.hc."):
		switch base {
		case "httpclient", "closeablehttpclient", "httpasyncclient", "closeablehttpasyncclient", "httpclients":
			return javaProtocolReceiverHTTPClient, true
		case "httpclientbuilder", "httpasyncclientbuilder":
			return javaProtocolReceiverHTTPBuilder, true
		}
	case strings.HasPrefix(normalized, "java.sql."):
		switch base {
		case "connection":
			return javaProtocolReceiverJDBCConnection, true
		case "statement", "preparedstatement", "callablestatement":
			return javaProtocolReceiverJDBCStatement, true
		case "drivermanager":
			return javaProtocolReceiverJDBCDataSource, true
		}
	case normalized == "javax.sql.datasource":
		return javaProtocolReceiverJDBCDataSource, true
	case strings.HasPrefix(normalized, "javax.jms."),
		strings.HasPrefix(normalized, "jakarta.jms."):
		return javaProtocolReceiverJMS, true
	case strings.HasPrefix(normalized, "javax.xml.soap."),
		strings.HasPrefix(normalized, "jakarta.xml.soap."),
		strings.HasPrefix(normalized, "javax.xml.ws."),
		strings.HasPrefix(normalized, "jakarta.xml.ws."):
		return javaProtocolReceiverSOAP, true
	case normalized == "lrapi.web":
		return javaProtocolReceiverLoadRunnerWeb, true
	}
	return javaProtocolReceiverUnknown, false
}

func inferJavaHTTPReceiverKinds(
	code string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	typeOverrides map[string]javaProtocolReceiverKind,
) {
	if _, exists := kinds["web"]; !exists {
		if kind, overridden := typeOverrides["web"]; overridden {
			kinds["web"] = kind
		} else {
			kinds["web"] = javaProtocolReceiverLoadRunnerWeb
		}
	}
	// A builder may be assigned first and built later, including when local type
	// inference ("var") hides both concrete types. Iterate because the build
	// assignment can depend on a factory assignment earlier in this same source.
	for pass := 0; pass < 2; pass++ {
		for _, match := range javaProtocolAssignmentCallRe.FindAllStringSubmatch(code, -1) {
			if len(match) != 4 {
				continue
			}
			target := strings.ToLower(strings.TrimSpace(match[1]))
			qualifier := strings.ToLower(strings.TrimSpace(match[2]))
			method := strings.ToLower(strings.TrimSpace(match[3]))
			factory := javaHTTPFactoryOwner(qualifier, typeOverrides)
			switch {
			case factory == "httpclient" && method == "newhttpclient":
				kinds[target] = javaProtocolReceiverHTTPClient
			case factory == "httpclient" && method == "newbuilder":
				kinds[target] = javaProtocolReceiverHTTPBuilder
			case factory == "httpclients" && strings.HasPrefix(method, "create"):
				kinds[target] = javaProtocolReceiverHTTPClient
			case factory == "httpclients" && method == "custom":
				kinds[target] = javaProtocolReceiverHTTPBuilder
			case factory == "httpclientbuilder" && method == "create":
				kinds[target] = javaProtocolReceiverHTTPBuilder
			case javaProtocolReceiverKindForValue(qualifier, kinds, fieldKinds, typeOverrides) == javaProtocolReceiverHTTPBuilder && method == "build":
				kinds[target] = javaProtocolReceiverHTTPClient
			}
		}
	}
	// A direct newBuilder().build() assignment is seen by the simpler assignment
	// scanner as newBuilder(). Apply this more-specific result last.
	factoryCode := javaMaskNonStandardHTTPFactoryReferences(code, typeOverrides)
	for _, match := range javaHTTPBuiltClientAssignmentRe.FindAllStringSubmatch(factoryCode, -1) {
		if len(match) == 2 {
			kinds[strings.ToLower(strings.TrimSpace(match[1]))] = javaProtocolReceiverHTTPClient
		}
	}
}

func javaHTTPFactoryOwner(
	value string,
	typeOverrides map[string]javaProtocolReceiverKind,
) string {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	base := javaProtocolStaticType(normalized)
	if kind, overridden := typeOverrides[base]; overridden && kind == javaProtocolReceiverUnknown {
		return ""
	}
	if strings.Contains(normalized, ".") {
		switch base {
		case "httpclient":
			if normalized != "java.net.http.httpclient" {
				return ""
			}
		case "httpclients", "httpclientbuilder":
			if !strings.HasPrefix(normalized, "org.apache.http.") &&
				!strings.HasPrefix(normalized, "org.apache.hc.") {
				return ""
			}
		default:
			return ""
		}
	}
	switch base {
	case "httpclient", "httpclients", "httpclientbuilder":
		return base
	default:
		return ""
	}
}

func inferJavaJDBCReceiverKinds(
	code string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	typeOverrides map[string]javaProtocolReceiverKind,
) {
	if _, exists := kinds["drivermanager"]; !exists {
		if kind, overridden := typeOverrides["drivermanager"]; overridden {
			kinds["drivermanager"] = kind
		} else {
			kinds["drivermanager"] = javaProtocolReceiverJDBCDataSource
		}
	}
	// Resolve common factory chains so Java 10+ "var" declarations still carry
	// enough receiver evidence for a later execute call.
	for pass := 0; pass < 2; pass++ {
		for _, match := range javaProtocolAssignmentCallRe.FindAllStringSubmatch(code, -1) {
			if len(match) != 4 {
				continue
			}
			target := strings.ToLower(strings.TrimSpace(match[1]))
			qualifier := strings.ToLower(strings.TrimSpace(match[2]))
			method := strings.ToLower(strings.TrimSpace(match[3]))
			qualifierKind := javaProtocolReceiverKindForValue(qualifier, kinds, fieldKinds, typeOverrides)
			switch {
			case javaJDBCDriverManagerOwner(qualifier, typeOverrides) && method == "getconnection":
				kinds[target] = javaProtocolReceiverJDBCConnection
			case qualifierKind == javaProtocolReceiverJDBCDataSource && method == "getconnection":
				kinds[target] = javaProtocolReceiverJDBCConnection
			case qualifierKind == javaProtocolReceiverJDBCConnection && method == "createstatement":
				kinds[target] = javaProtocolReceiverJDBCStatement
			case qualifierKind == javaProtocolReceiverJDBCConnection &&
				(method == "preparestatement" || method == "preparecall"):
				kinds[target] = javaProtocolReceiverJDBCStatement
			}
		}
	}
}

func javaJDBCDriverManagerOwner(
	value string,
	typeOverrides map[string]javaProtocolReceiverKind,
) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	if kind, overridden := typeOverrides["drivermanager"]; overridden && kind == javaProtocolReceiverUnknown {
		return false
	}
	return normalized == "drivermanager" || normalized == "java.sql.drivermanager"
}

func inferJavaJMSReceiverKinds(
	code string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	typeOverrides map[string]javaProtocolReceiverKind,
) {
	for receiver, typeName := range map[string]string{
		"session":  "session",
		"producer": "messageproducer",
		"consumer": "messageconsumer",
	} {
		if _, exists := kinds[receiver]; exists {
			continue
		}
		if typeOverrides[typeName] == javaProtocolReceiverJMS {
			kinds[receiver] = javaProtocolReceiverJMS
		}
	}
	for _, match := range javaProtocolAssignmentCallRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 4 {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(match[1]))
		qualifier := strings.ToLower(strings.TrimSpace(match[2]))
		method := strings.ToLower(strings.TrimSpace(match[3]))
		if javaProtocolReceiverKindForValue(qualifier, kinds, fieldKinds, typeOverrides) != javaProtocolReceiverJMS {
			continue
		}
		switch method {
		case "createcontext", "createconnection", "createsession", "createqueue",
			"createtopic", "createproducer", "createconsumer", "createtextmessage",
			"createmessage":
			kinds[target] = javaProtocolReceiverJMS
		}
	}
}

func inferJavaSOAPReceiverKinds(
	code string,
	kinds map[string]javaProtocolReceiverKind,
	fieldKinds map[string]javaProtocolReceiverKind,
	_ map[string]javaProtocolReceiverKind,
) {
	for _, match := range javaProtocolAssignmentCallRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 4 {
			continue
		}
		target := strings.ToLower(strings.TrimSpace(match[1]))
		qualifier := strings.ToLower(strings.TrimSpace(match[2]))
		method := strings.ToLower(strings.TrimSpace(match[3]))
		if javaProtocolReceiverKindForValue(qualifier, kinds, fieldKinds, nil) != javaProtocolReceiverSOAP {
			continue
		}
		switch method {
		case "newinstance", "createsoapmessage", "createsoapconnection",
			"createconnection", "createmessage", "createdispatch":
			kinds[target] = javaProtocolReceiverSOAP
		}
	}
}

func javaProtocolReceiverCalls(code string) []javaProtocolReceiverCall {
	matches := javaProtocolReceiverCallRe.FindAllStringSubmatch(code, -1)
	calls := make([]javaProtocolReceiverCall, 0, len(matches))
	for _, match := range matches {
		if len(match) != 3 {
			continue
		}
		calls = append(calls, javaProtocolReceiverCall{
			receiver: strings.ToLower(strings.TrimSpace(match[1])),
			method:   strings.ToLower(strings.TrimSpace(match[2])),
		})
	}
	return calls
}

func javaProtocolReceiverName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		value = value[dot+1:]
	}
	return value
}

func javaProtocolStaticType(value string) string {
	return javaProtocolReceiverName(strings.ReplaceAll(value, " ", ""))
}

func javaLooksLikeSOAP(text string) bool {
	return javaHasProtocolReceiverCall(
		javaCodeOnly(text),
		inferJavaSOAPReceiverKinds,
		func(kind javaProtocolReceiverKind, method string) bool {
			if kind != javaProtocolReceiverSOAP {
				return false
			}
			switch method {
			case "createsoapmessage", "createsoapconnection", "createdispatch",
				"createmessage", "createconnection", "getport", "call", "writeto",
				"invoke", "invokeasync", "invokeoneway":
				return true
			default:
				return false
			}
		},
	)
}

// javaExecutableEvidenceText is intentionally narrower than the archived raw
// source. Protocol review evidence must describe code reached from the selected
// VUser lifecycle, not unused imports, comments, or string literals that merely
// contain API-looking text.
func javaExecutableEvidenceText(program javaProgram) string {
	text, _ := javaExecutableEvidence(program)
	return text
}

func javaExecutableEvidence(program javaProgram) (string, string) {
	class, ok := javaVuserClass(program)
	if !ok {
		return "", ""
	}
	index := newJavaEvidenceIndex(program)
	reachableMethods := javaExecutableEvidenceMethods(index, class)
	scopes := make([]string, 0, 8)
	context := make([]string, 0, len(reachableMethods)*2)
	contextOwners := make(map[string]bool)
	for _, reachable := range reachableMethods {
		bindings := javaProtocolTypeBindingEvidence(index, reachable.class)
		parts := append([]string(nil), bindings...)
		for _, field := range javaExecutableEvidenceFields(index, reachable.class, reachable.method.Static) {
			parts = append(parts, javaFieldDeclarationEvidenceForOwner(index, field.class, field.statement.Text))
			if !javaSameEvidenceClass(field.class, reachable.class) {
				parts = append(parts, javaSuperFieldDeclarationEvidenceForOwner(index, field.class, field.statement.Text))
			}
		}
		parts = append(parts, javaProtocolLocalScopeBoundary)
		parts = append(parts, bindings...)
		for _, parameter := range reachable.method.Parameters {
			if parameter.Type != "" && parameter.Name != "" {
				parts = append(parts, javaEvidenceCode(parameter.Type+" "+parameter.Name+";"))
			}
		}
		executedStatements := javaImmediatelyExecutedStatements(reachable.method.Statements)
		appendJavaStatementEvidence(
			&parts,
			executedStatements,
		)
		scopes = append(scopes, strings.Join(parts, "\n"))

		for _, contextOwner := range index.classHierarchy(reachable.class) {
			ownerKey := javaEvidenceOwnerKey(contextOwner)
			if !contextOwners[ownerKey] {
				contextOwners[ownerKey] = true
				for _, imported := range contextOwner.Imports {
					context = append(context, "import "+imported+";")
				}
			}
		}
		appendJavaStatementContext(&context, executedStatements)
	}
	return strings.ToLower(strings.Join(scopes, "\n"+javaProtocolScopeBoundary+"\n")),
		strings.Join(context, "\n")
}

func appendJavaStatementContext(parts *[]string, statements []javaStatement) {
	for _, statement := range statements {
		if statement.Kind == "block" {
			if strings.TrimSpace(statement.Header) != "" {
				*parts = append(*parts, statement.Header)
			}
			appendJavaStatementContext(parts, statement.Children)
			continue
		}
		if strings.TrimSpace(statement.Text) != "" {
			*parts = append(*parts, statement.Text)
		}
		if len(statement.Children) > 0 {
			appendJavaStatementContext(parts, statement.Children)
		}
	}
}

type javaExecutableEvidenceMethod struct {
	class  javaClass
	method javaMethod
}

// javaEvidenceIndex resolves archived Java types without repeatedly scanning
// every class for every reachable method and protocol type. Enterprise archives
// permit thousands of entries, so classification must remain linear in the
// number of classes plus the reachable call graph.
type javaEvidenceIndex struct {
	program                 javaProgram
	qualified               map[string][]javaClass
	packageSimple           map[string][]javaClass
	nestedTypes             map[string]map[string]bool
	directFieldTypes        map[string]map[string]string
	hierarchyFieldTypes     map[string]map[string]string
	fieldLookupCache        map[string]javaEvidenceFieldLookup
	totalMemberLookups      int
	simpleTypes             map[string]bool
	bindingCache            map[string][]string
	importScopes            map[string]*javaEvidenceImportScope
	staticMethodCache       map[string][]javaResolvedHelperMethod
	staticImportLookups     int
	staticImportLimitHit    bool
	directSubtypes          map[string][]int
	directClassSubtypes     map[int][]int
	classIndices            map[string]int
	classIntervals          map[int]javaEvidenceClassInterval
	methodClasses           map[string][]int
	subtypeCache            map[string][]int
	functionalBindings      map[string]javaStoredFunctionalBindingTimeline
	persistentFunctional    map[string]javaStoredFunctionalBindingSet
	functionalBindingBuilds int
}

type javaEvidenceClassInterval struct {
	start int
	end   int
}

type javaEvidenceFieldLookup struct {
	typeName string
	found    bool
}

type javaEvidenceImportScope struct {
	explicitTypes    map[string][]string
	wildcardTypes    map[string][]javaClass
	wildcardPackages []string
	staticExact      map[string][]string
	staticWildcard   []string
	lrSimpleReady    bool
	lrSimpleBinding  bool
	lrStaticReady    bool
	lrStaticImports  javaLRStaticImportSet
}

const javaEvidenceSubtypeCacheLimit = 32

func newJavaEvidenceIndex(program javaProgram) *javaEvidenceIndex {
	index := &javaEvidenceIndex{
		program:             program,
		qualified:           make(map[string][]javaClass, len(program.Classes)),
		packageSimple:       make(map[string][]javaClass, len(program.Classes)),
		nestedTypes:         make(map[string]map[string]bool, len(program.Classes)),
		directFieldTypes:    make(map[string]map[string]string, len(program.Classes)),
		hierarchyFieldTypes: make(map[string]map[string]string, len(program.Classes)),
		fieldLookupCache:    make(map[string]javaEvidenceFieldLookup),
		simpleTypes:         make(map[string]bool, len(program.Classes)*2),
		bindingCache:        make(map[string][]string),
		importScopes:        make(map[string]*javaEvidenceImportScope),
		staticMethodCache:   make(map[string][]javaResolvedHelperMethod),
		directSubtypes:      make(map[string][]int),
		directClassSubtypes: make(map[int][]int),
		classIndices:        make(map[string]int, len(program.Classes)),
		classIntervals:      make(map[int]javaEvidenceClassInterval, len(program.Classes)),
		methodClasses:       make(map[string][]int),
		subtypeCache:        make(map[string][]int),
		functionalBindings:  make(map[string]javaStoredFunctionalBindingTimeline),
		persistentFunctional: make(
			map[string]javaStoredFunctionalBindingSet,
		),
	}
	for classIndex, class := range program.Classes {
		qualified := strings.ToLower(strings.TrimSpace(javaQualifiedClassName(class)))
		index.qualified[qualified] = append(index.qualified[qualified], class)
		key := javaEvidencePackageTypeKey(class.Package, class.Name)
		index.packageSimple[key] = append(index.packageSimple[key], class)
		index.simpleTypes[javaComparableType(class.Name)] = true
		index.simpleTypes[javaComparableType(javaQualifiedClassName(class))] = true
		index.classIndices[javaEvidenceOwnerKey(class)] = classIndex
		for _, declaration := range javaTypeDeclarationRe.FindAllStringSubmatch(
			javaCodeOnly(class.Source),
			maxJavaAnalysisTypeCount+1,
		) {
			if len(declaration) != 3 {
				continue
			}
			ownerKey := javaEvidenceOwnerKey(class)
			if index.nestedTypes[ownerKey] == nil {
				index.nestedTypes[ownerKey] = make(map[string]bool)
			}
			index.nestedTypes[ownerKey][strings.ToLower(strings.TrimSpace(declaration[2]))] = true
		}
		methodNames := make(map[string]bool)
		for methodName := range class.MethodOverloads {
			methodNames[strings.ToLower(strings.TrimSpace(methodName))] = true
		}
		for methodName := range class.Methods {
			methodNames[strings.ToLower(strings.TrimSpace(methodName))] = true
		}
		for methodName := range methodNames {
			index.methodClasses[methodName] = append(index.methodClasses[methodName], classIndex)
		}
	}
	for _, class := range program.Classes {
		ownerKey := javaEvidenceOwnerKey(class)
		fieldTypes := make(map[string]string)
		for _, field := range class.Fields {
			collectJavaDeclaredVariableTypes(fieldTypes, field.Text)
		}
		if len(fieldTypes) == 0 {
			continue
		}
		canonical := make(map[string]string, len(fieldTypes))
		for name, typeName := range fieldTypes {
			canonical[name] = javaCanonicalArchivedType(index, class, typeName)
		}
		index.directFieldTypes[ownerKey] = canonical
	}
	for classIndex, class := range program.Classes {
		for _, supertype := range index.directSupertypes(class) {
			key := javaEvidenceOwnerKey(supertype)
			index.directSubtypes[key] = append(index.directSubtypes[key], classIndex)
		}
		if superClass, ok := index.superclass(class); ok {
			if superIndex, found := index.classIndices[javaEvidenceOwnerKey(superClass)]; found {
				index.directClassSubtypes[superIndex] = append(
					index.directClassSubtypes[superIndex],
					classIndex,
				)
			}
		}
	}
	index.buildClassIntervals()
	return index
}

func (index *javaEvidenceIndex) buildClassIntervals() {
	if index == nil {
		return
	}
	children := make(map[int]bool)
	for _, childIndices := range index.directClassSubtypes {
		for _, childIndex := range childIndices {
			children[childIndex] = true
		}
	}
	visited := make(map[int]bool, len(index.program.Classes))
	next := 0
	var visit func(int)
	visit = func(classIndex int) {
		if classIndex < 0 || classIndex >= len(index.program.Classes) ||
			visited[classIndex] {
			return
		}
		visited[classIndex] = true
		start := next
		next++
		for _, childIndex := range index.directClassSubtypes[classIndex] {
			visit(childIndex)
		}
		index.classIntervals[classIndex] = javaEvidenceClassInterval{
			start: start,
			end:   next,
		}
	}
	for classIndex, class := range index.program.Classes {
		if strings.EqualFold(strings.TrimSpace(class.Kind), "interface") ||
			children[classIndex] {
			continue
		}
		visit(classIndex)
	}
	for classIndex := range index.program.Classes {
		visit(classIndex)
	}
}

func (index *javaEvidenceIndex) directSupertypes(class javaClass) []javaClass {
	if index == nil {
		return nil
	}
	typeNames := make([]string, 0, len(class.Interfaces)+1)
	if strings.TrimSpace(class.SuperClass) != "" {
		typeNames = append(typeNames, class.SuperClass)
	}
	typeNames = append(typeNames, class.Interfaces...)
	resolved := make([]javaClass, 0, len(typeNames))
	seen := make(map[string]bool)
	for _, typeName := range typeNames {
		supertype, ok := index.classForType(class, typeName)
		if !ok {
			continue
		}
		key := javaEvidenceOwnerKey(supertype)
		if seen[key] {
			continue
		}
		seen[key] = true
		resolved = append(resolved, supertype)
	}
	return resolved
}

// defaultMethodSuperinterfaces returns the interfaces Java initializes before a
// class because they declare default methods. Parent interfaces are ordered
// before children; interfaces that contribute no default are traversed for
// descendants but are not initialized themselves.
func (index *javaEvidenceIndex) defaultMethodSuperinterfaces(class javaClass) []javaClass {
	if index == nil {
		return nil
	}
	resolved := make([]javaClass, 0)
	seen := make(map[string]bool)
	var visit func(javaClass)
	visit = func(current javaClass) {
		key := javaEvidenceOwnerKey(current)
		if seen[key] {
			return
		}
		seen[key] = true
		for _, parentName := range current.Interfaces {
			if parent, ok := index.classForType(current, parentName); ok {
				visit(parent)
			}
		}
		if javaClassDeclaresDefaultMethod(current) {
			resolved = append(resolved, current)
		}
	}
	for _, interfaceName := range class.Interfaces {
		if interfaceClass, ok := index.classForType(class, interfaceName); ok {
			visit(interfaceClass)
		}
	}
	return resolved
}

func javaClassDeclaresDefaultMethod(class javaClass) bool {
	for _, overloads := range class.MethodOverloads {
		for _, method := range overloads {
			if method.Default && !method.Abstract {
				return true
			}
		}
	}
	return false
}

func (index *javaEvidenceIndex) subtypeIndicesFor(class javaClass) []int {
	if index == nil {
		return nil
	}
	rootKey := javaEvidenceOwnerKey(class)
	if cached, ok := index.subtypeCache[rootKey]; ok {
		return cached
	}
	direct := index.directSubtypes[rootKey]
	if len(direct) == 0 {
		if len(index.subtypeCache) < javaEvidenceSubtypeCacheLimit {
			index.subtypeCache[rootKey] = nil
		}
		return nil
	}
	seen := make(map[int]bool, len(direct))
	stack := append([]int(nil), direct...)
	resolved := make([]int, 0, len(direct))
	for len(stack) > 0 {
		last := len(stack) - 1
		classIndex := stack[last]
		stack = stack[:last]
		if classIndex < 0 || classIndex >= len(index.program.Classes) ||
			seen[classIndex] {
			continue
		}
		seen[classIndex] = true
		resolved = append(resolved, classIndex)
		subtype := index.program.Classes[classIndex]
		stack = append(stack, index.directSubtypes[javaEvidenceOwnerKey(subtype)]...)
	}
	if len(index.subtypeCache) < javaEvidenceSubtypeCacheLimit {
		index.subtypeCache[rootKey] = resolved
	}
	return resolved
}

func (index *javaEvidenceIndex) isSubtype(candidate, base javaClass) bool {
	if index == nil || javaSameEvidenceClass(candidate, base) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(base.Kind), "interface") &&
		!strings.EqualFold(strings.TrimSpace(candidate.Kind), "interface") {
		candidateIndex, candidateOK := index.classIndices[javaEvidenceOwnerKey(candidate)]
		baseIndex, baseOK := index.classIndices[javaEvidenceOwnerKey(base)]
		candidateInterval, candidateIntervalOK := index.classIntervals[candidateIndex]
		baseInterval, baseIntervalOK := index.classIntervals[baseIndex]
		if candidateOK && baseOK && candidateIntervalOK && baseIntervalOK {
			return candidateInterval.start >= baseInterval.start &&
				candidateInterval.start < baseInterval.end
		}
	}
	candidateKey := javaEvidenceOwnerKey(candidate)
	for _, classIndex := range index.subtypeIndicesFor(base) {
		if javaEvidenceOwnerKey(index.program.Classes[classIndex]) == candidateKey {
			return true
		}
	}
	return false
}

func javaEvidencePackageTypeKey(packageName, simpleName string) string {
	return strings.ToLower(strings.TrimSpace(packageName)) + "\x00" +
		strings.ToLower(strings.TrimSpace(simpleName))
}

func javaEvidenceOwnerKey(class javaClass) string {
	return strings.ToLower(strings.TrimSpace(class.File)) + "\x00" +
		strings.ToLower(strings.TrimSpace(javaQualifiedClassName(class)))
}

func (index *javaEvidenceIndex) classesInPackage(packageName, simpleName string) []javaClass {
	if index == nil {
		return nil
	}
	return index.packageSimple[javaEvidencePackageTypeKey(packageName, simpleName)]
}

func (index *javaEvidenceIndex) qualifiedClass(typeName string) (javaClass, bool) {
	if index == nil {
		return javaClass{}, false
	}
	matches := index.qualified[strings.ToLower(strings.TrimSpace(typeName))]
	if len(matches) != 1 {
		return javaClass{}, false
	}
	return matches[0], true
}

func (index *javaEvidenceIndex) superclass(class javaClass) (javaClass, bool) {
	superName := strings.TrimSpace(class.SuperClass)
	if superName == "" ||
		strings.EqualFold(superName, "Object") ||
		strings.EqualFold(superName, "java.lang.Object") {
		return javaClass{}, false
	}
	return index.classForType(class, superName)
}

// classHierarchy returns archived classes from the eldest resolved superclass
// through class. Root-first ordering makes ordinary Java field shadowing work
// when declarations are replayed into an evidence scope.
func (index *javaEvidenceIndex) classHierarchy(class javaClass) []javaClass {
	hierarchy := make([]javaClass, 0, 4)
	seen := make(map[string]bool)
	var appendClass func(javaClass)
	appendClass = func(current javaClass) {
		key := javaEvidenceOwnerKey(current)
		if seen[key] {
			return
		}
		seen[key] = true
		if superClass, ok := index.superclass(current); ok {
			appendClass(superClass)
		}
		hierarchy = append(hierarchy, current)
	}
	appendClass(class)
	return hierarchy
}

type javaExecutableEvidenceField struct {
	class     javaClass
	statement javaStatement
}

func javaExecutableEvidenceFields(
	index *javaEvidenceIndex,
	class javaClass,
	staticOnly bool,
) []javaExecutableEvidenceField {
	var fields []javaExecutableEvidenceField
	for _, owner := range index.classHierarchy(class) {
		for _, field := range owner.Fields {
			if staticOnly && !javaFieldIsStaticInClass(owner, field) {
				continue
			}
			fields = append(fields, javaExecutableEvidenceField{
				class:     owner,
				statement: field,
			})
		}
	}
	return fields
}

// javaExecutableEvidenceMethods returns only constructors, lifecycle methods,
// and helpers reachable from them. Each method remains a separate evidence
// scope: inlining helper statements into the caller would let helper locals
// shadow or manufacture receiver types for caller statements after the call.
func javaExecutableEvidenceMethods(index *javaEvidenceIndex, class javaClass) []javaExecutableEvidenceMethod {
	methods := make([]javaExecutableEvidenceMethod, 0, 8)
	seenMethods := make(map[string]bool)
	staticInitialized := make(map[string]bool)
	instanceInitialized := make(map[string]bool)
	var add func(javaClass, javaMethod)
	var initializeStatic func(javaClass)
	var initializeInstance func(javaClass)

	initializeStatic = func(owner javaClass) {
		classKey := javaEvidenceOwnerKey(owner)
		if staticInitialized[classKey] {
			return
		}
		staticInitialized[classKey] = true

		if superClass, ok := index.superclass(owner); ok {
			initializeStatic(superClass)
		}
		if !strings.EqualFold(strings.TrimSpace(owner.Kind), "interface") {
			for _, interfaceClass := range index.defaultMethodSuperinterfaces(owner) {
				initializeStatic(interfaceClass)
			}
		}

		staticFields := make([]javaStatement, 0, len(owner.Fields))
		for _, field := range owner.Fields {
			if javaFieldIsStaticInClass(owner, field) {
				staticFields = append(staticFields, field)
			}
		}
		if fields := javaExecutedFieldStatements(staticFields); len(fields) > 0 {
			add(owner, javaMethod{
				Name:       "<static_field_initializers>",
				File:       owner.File,
				StartLine:  owner.BodyStartLine,
				EndLine:    owner.BodyStartLine,
				Static:     true,
				Statements: fields,
			})
		}
		for initializerIndex, initializer := range javaClassInitializerStatements(owner) {
			if !strings.EqualFold(strings.TrimSpace(javaCodeOnly(initializer.Header)), "static") {
				continue
			}
			add(owner, javaMethod{
				Name:       "<static_initializer_" + strconv.Itoa(initializerIndex) + ">",
				File:       owner.File,
				StartLine:  initializer.StartLine,
				EndLine:    initializer.EndLine,
				Static:     true,
				Statements: initializer.Children,
			})
		}
	}

	initializeInstance = func(owner javaClass) {
		initializeStatic(owner)
		classKey := javaEvidenceOwnerKey(owner)
		if instanceInitialized[classKey] {
			return
		}
		instanceInitialized[classKey] = true

		if superClass, ok := index.superclass(owner); ok {
			initializeInstance(superClass)
		}

		instanceFields := make([]javaStatement, 0, len(owner.Fields))
		for _, field := range owner.Fields {
			if !javaFieldIsStaticInClass(owner, field) {
				instanceFields = append(instanceFields, field)
			}
		}
		if fields := javaExecutedFieldStatements(instanceFields); len(fields) > 0 {
			add(owner, javaMethod{
				Name:       "<instance_field_initializers>",
				File:       owner.File,
				StartLine:  owner.BodyStartLine,
				EndLine:    owner.BodyStartLine,
				Statements: fields,
			})
		}
		for initializerIndex, initializer := range javaClassInitializerStatements(owner) {
			if strings.EqualFold(strings.TrimSpace(javaCodeOnly(initializer.Header)), "static") {
				continue
			}
			add(owner, javaMethod{
				Name:       "<instance_initializer_" + strconv.Itoa(initializerIndex) + ">",
				File:       owner.File,
				StartLine:  initializer.StartLine,
				EndLine:    initializer.EndLine,
				Statements: initializer.Children,
			})
		}
	}

	add = func(owner javaClass, method javaMethod) {
		if method.Name == owner.Name {
			initializeInstance(owner)
		} else {
			initializeStatic(owner)
		}
		key := javaHelperVisitKey(owner, method)
		if seenMethods[key] {
			return
		}
		seenMethods[key] = true
		methods = append(methods, javaExecutableEvidenceMethod{class: owner, method: method})
		collectJavaExecutableEvidenceHelpers(
			javaImmediatelyExecutedStatements(method.Statements),
			owner,
			method,
			index,
			javaDeclaredVariableTypesWithHierarchy(index, owner, method),
			add,
			initializeInstance,
		)
		if method.Name == owner.Name {
			constructorCode := javaCodeOnly(method.Body)
			if !javaThisConstructorCallRe.MatchString(constructorCode) &&
				!javaSuperConstructorCallRe.MatchString(constructorCode) {
				addJavaImplicitSuperConstructorEvidence(index, owner, add)
			}
		}
	}

	// Harness.getDeclaredConstructor().newInstance() constructs one Actions
	// object even when its archived class declares no explicit constructor.
	initializeInstance(class)

	// The governed harness invokes only the public zero-argument constructor.
	hasExplicitConstructor := false
	for _, constructor := range class.Constructors {
		if javaMethodIsPublicZeroArg(constructor) {
			hasExplicitConstructor = true
			add(class, constructor)
		}
	}
	if !hasExplicitConstructor && len(class.Constructors) == 0 {
		addJavaImplicitSuperConstructorEvidence(index, class, add)
	}
	for _, lifecycle := range []string{"init", "action", "teardown"} {
		if method, ok := javaVuserLifecycleMethod(index.program, lifecycle); ok {
			add(class, method)
		}
	}
	return methods
}

func addJavaImplicitSuperConstructorEvidence(
	index *javaEvidenceIndex,
	class javaClass,
	add func(javaClass, javaMethod),
) {
	superClass, ok := index.superclass(class)
	if !ok {
		return
	}
	for _, constructor := range superClass.Constructors {
		if constructor.ParameterCount == 0 {
			add(superClass, constructor)
			return
		}
	}
	if len(superClass.Constructors) == 0 {
		addJavaImplicitSuperConstructorEvidence(index, superClass, add)
	}
}

func javaExecutedFieldStatements(fields []javaStatement) []javaStatement {
	executed := make([]javaStatement, 0, len(fields))
	for _, field := range fields {
		// Field declarations containing lambdas or anonymous classes were
		// flattened by the class scanner. Parse the declaration again here so
		// eager anonymous-class initializers can be distinguished from deferred
		// method/lambda bodies.
		parsed := parseJavaStatements(field.File, field.Text, field.StartLine)
		executed = append(executed, javaImmediatelyExecutedStatements(parsed)...)
	}
	return executed
}

// javaImmediatelyExecutedStatements removes bodies that are only stored for a
// later callback. It intentionally retains lambdas passed directly to an
// invocation (forEach, retry callbacks, and similar APIs), because those may
// execute during the reachable statement and remain conservative evidence.
func javaImmediatelyExecutedStatements(statements []javaStatement) []javaStatement {
	executed := make([]javaStatement, 0, len(statements))
	for _, statement := range statements {
		copy := statement
		if statement.Kind != "block" {
			copy.Text = javaMaskStoredExpressionLambda(statement.Text)
			if len(statement.Children) > 0 {
				copy.Children = javaImmediatelyExecutedStatements(statement.Children)
			}
			executed = append(executed, copy)
			continue
		}

		headerCode := javaCodeOnly(statement.Header)
		if arrow := strings.Index(headerCode, "->"); arrow >= 0 &&
			javaLambdaIsDefinitelyDeferred(headerCode[:arrow]) {
			// The lambda object is created now; its body is not.
			copy.Children = nil
			executed = append(executed, copy)
			continue
		}
		if javaTypeDeclarationRe.MatchString(headerCode) {
			// Declaring a named member/local type does not execute its method
			// bodies. The runtime reachability builder fails closed if a
			// reachable construction or receiver cannot bind that nested type.
			copy.Children = nil
			executed = append(executed, copy)
			continue
		}
		if javaAnonymousClassInitializerRe.MatchString(statement.Header + "{") {
			// Constructing an anonymous class executes its field and
			// initializer blocks, but not its declared methods.
			copy.Children = javaAnonymousClassEagerStatements(statement.Children)
			executed = append(executed, copy)
			continue
		}
		copy.Children = javaImmediatelyExecutedStatements(statement.Children)
		executed = append(executed, copy)
	}
	return executed
}

func javaAnonymousClassEagerStatements(members []javaStatement) []javaStatement {
	executed := make([]javaStatement, 0, len(members))
	for _, member := range members {
		header := strings.ToLower(strings.TrimSpace(javaCodeOnly(member.Header)))
		switch {
		case len(member.Children) > 0 && (header == "" || header == "static"):
			// Parser blocks with an empty header are represented as statements.
			executed = append(executed, javaImmediatelyExecutedStatements(member.Children)...)
		case member.Kind == "statement" && len(member.Children) == 0:
			// Direct statements at anonymous-class scope are field
			// declarations. Their initializer expressions execute eagerly.
			copy := member
			copy.Text = javaMaskStoredExpressionLambda(member.Text)
			executed = append(executed, copy)
		case member.Kind == "block" && javaClassMemberBlockIsField(member):
			copy := member
			copy.Children = javaAnonymousClassEagerStatements(member.Children)
			executed = append(executed, copy)
		}
	}
	return executed
}

func javaMaskStoredExpressionLambda(source string) string {
	code := javaEvidenceCode(source)
	arrow := strings.Index(code, "->")
	if arrow < 0 || !javaLambdaIsDefinitelyDeferred(code[:arrow]) {
		return source
	}
	masked := []byte(source)
	end := len(masked)
	for index := arrow + 2; index < len(code); index++ {
		if code[index] == ';' {
			end = index
			break
		}
	}
	for index := arrow; index < end; index++ {
		if masked[index] != '\n' && masked[index] != '\r' {
			masked[index] = ' '
		}
	}
	return string(masked)
}

func javaLambdaIsDefinitelyDeferred(prefix string) bool {
	if javaHasTopLevelAssignment(prefix) {
		return true
	}
	prefix = strings.TrimSpace(prefix)
	for _, keyword := range []string{"return", "yield"} {
		if !strings.HasPrefix(prefix, keyword) {
			continue
		}
		if len(prefix) == len(keyword) || !isJavaIdentifierByte(prefix[len(keyword)]) {
			return true
		}
	}
	return false
}

func javaHasTopLevelAssignment(code string) bool {
	parenDepth := 0
	bracketDepth := 0
	for index := 0; index < len(code); index++ {
		switch code[index] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '=':
			if parenDepth != 0 || bracketDepth != 0 {
				continue
			}
			previousEquals := index > 0 && strings.ContainsRune("=!<>", rune(code[index-1]))
			nextEquals := index+1 < len(code) && code[index+1] == '='
			if !previousEquals && !nextEquals {
				return true
			}
		}
	}
	return false
}

func javaFieldDeclarationEvidenceForOwner(
	index *javaEvidenceIndex,
	owner javaClass,
	source string,
) string {
	return javaFieldDeclarationEvidenceForOwnerWithName(index, owner, source, func(name string) string {
		return name
	})
}

func javaSuperFieldDeclarationEvidenceForOwner(
	index *javaEvidenceIndex,
	owner javaClass,
	source string,
) string {
	return javaFieldDeclarationEvidenceForOwnerWithName(index, owner, source, javaProtocolSuperFieldEvidenceName)
}

func javaFieldDeclarationEvidenceForOwnerWithName(
	index *javaEvidenceIndex,
	owner javaClass,
	source string,
	evidenceName func(string) string,
) string {
	code := javaEvidenceCode(source)
	typeOverrides := javaProtocolTypeOverrides(
		strings.Join(javaProtocolTypeBindingEvidence(index, owner), "\n"),
	)
	declarations := make([]string, 0, 1)
	for _, match := range javaProtocolVariableDeclRe.FindAllStringSubmatch(code, -1) {
		if len(match) != 3 {
			continue
		}
		kind := javaProtocolKindForType(strings.TrimSpace(match[1]), typeOverrides)
		declarations = append(declarations,
			javaCanonicalEvidenceType(kind)+" "+evidenceName(strings.TrimSpace(match[2]))+";",
		)
	}
	return strings.Join(declarations, "\n")
}

func javaProtocolSuperFieldEvidenceName(name string) string {
	return javaProtocolSuperFieldPrefix + strings.ToLower(strings.TrimSpace(name)) + "__"
}

func javaCanonicalEvidenceType(kind javaProtocolReceiverKind) string {
	switch kind {
	case javaProtocolReceiverHTTPClient:
		return "java.net.http.HttpClient"
	case javaProtocolReceiverHTTPBuilder:
		return "java.net.http.HttpClient.Builder"
	case javaProtocolReceiverJDBCDataSource:
		return "javax.sql.DataSource"
	case javaProtocolReceiverJDBCConnection:
		return "java.sql.Connection"
	case javaProtocolReceiverJDBCStatement:
		return "java.sql.Statement"
	case javaProtocolReceiverLoadRunnerWeb:
		return "lrapi.web"
	case javaProtocolReceiverJMS:
		return "javax.jms.MessageConsumer"
	case javaProtocolReceiverSOAP:
		return "javax.xml.ws.Dispatch"
	case javaProtocolReceiverHTTPURL:
		return "java.net.URL"
	default:
		return "java.lang.Object"
	}
}

func collectJavaExecutableEvidenceHelpers(
	statements []javaStatement,
	owner javaClass,
	current javaMethod,
	index *javaEvidenceIndex,
	inheritedTypes map[string]string,
	add func(javaClass, javaMethod),
	initializeInstance func(javaClass),
) {
	variableTypes := cloneJavaVariableTypes(inheritedTypes)
	for _, statement := range statements {
		callSource := statement.Text
		callVariableTypes := variableTypes
		if statement.Kind == "block" {
			callSource = statement.Header
			childTypes := cloneJavaVariableTypes(variableTypes)
			collectJavaDeclaredVariableTypes(childTypes, callSource)
			callVariableTypes = childTypes
			collectJavaExecutableEvidenceHelpers(
				statement.Children,
				owner,
				current,
				index,
				childTypes,
				add,
				initializeInstance,
			)
		}
		for _, construction := range javaResolvedEvidenceConstructions(
			callSource,
			owner,
			index,
			callVariableTypes,
		) {
			initializeInstance(construction.class)
			if construction.constructor.Name != "" {
				add(construction.class, construction.constructor)
			} else if len(construction.class.Constructors) == 0 {
				addJavaImplicitSuperConstructorEvidence(
					index,
					construction.class,
					add,
				)
			}
		}
		for _, helper := range javaResolvedEvidenceHelperCalls(callSource, owner, current, index, callVariableTypes) {
			add(helper.class, helper.method)
		}
		if statement.Kind != "block" {
			collectJavaDeclaredVariableTypes(variableTypes, statement.Text)
		}
	}
}

func javaClassInitializerStatements(class javaClass) []javaStatement {
	baseLine := class.BodyStartLine
	if baseLine <= 0 {
		baseLine = 1
	}
	members := parseJavaStatements(class.File, class.Source, baseLine)
	initializers := make([]javaStatement, 0)
	for _, member := range members {
		if len(member.Children) == 0 || javaClassMemberBlockIsField(member) {
			continue
		}
		header := strings.ToLower(strings.TrimSpace(javaCodeOnly(member.Header)))
		if header != "" && header != "static" {
			continue
		}
		member.Kind = "block"
		initializers = append(initializers, member)
	}
	return initializers
}

type javaResolvedEvidenceConstruction struct {
	class       javaClass
	constructor javaMethod
}

const javaCanonicalEvidenceTypesOwnerKey = "\x00apexload:canonical-evidence-owner"

func (index *javaEvidenceIndex) canonicalHierarchyFieldTypes(
	owner javaClass,
) map[string]string {
	key := javaEvidenceOwnerKey(owner)
	if cached, ok := index.hierarchyFieldTypes[key]; ok {
		return cached
	}
	types := make(map[string]string)
	for _, fieldOwner := range index.classHierarchy(owner) {
		for name, typeName := range index.directFieldTypes[javaEvidenceOwnerKey(fieldOwner)] {
			types[name] = typeName
		}
	}
	types[javaCanonicalEvidenceTypesOwnerKey] = key
	index.hierarchyFieldTypes[key] = types
	return types
}

func javaDeclaredVariableTypesWithHierarchy(
	index *javaEvidenceIndex,
	owner javaClass,
	current javaMethod,
) map[string]string {
	base := index.canonicalHierarchyFieldTypes(owner)
	types := make(map[string]string, len(base)+len(current.Parameters))
	for name, typeName := range base {
		if name != javaCanonicalEvidenceTypesOwnerKey {
			types[name] = typeName
		}
	}
	for _, parameter := range current.Parameters {
		if parameter.Name != "" && parameter.Type != "" {
			types[parameter.Name] = javaCanonicalArchivedType(index, owner, parameter.Type)
		}
	}
	return types
}

func javaEffectiveEvidenceVariableTypes(
	index *javaEvidenceIndex,
	owner javaClass,
	variableTypes map[string]string,
) map[string]string {
	ownerKey := javaEvidenceOwnerKey(owner)
	if variableTypes[javaCanonicalEvidenceTypesOwnerKey] == ownerKey {
		return variableTypes
	}
	effective := javaDeclaredVariableTypesWithHierarchy(index, owner, javaMethod{})
	for name, typeName := range variableTypes {
		effective[name] = javaCanonicalArchivedType(index, owner, typeName)
	}
	effective[javaCanonicalEvidenceTypesOwnerKey] = ownerKey
	return effective
}

func javaCanonicalArchivedType(index *javaEvidenceIndex, owner javaClass, typeName string) string {
	if class, ok := index.classForType(owner, typeName); ok {
		return javaQualifiedClassName(class)
	}
	return javaSimpleTypeName(typeName)
}

func javaResolvedEvidenceConstructions(
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []javaResolvedEvidenceConstruction {
	variableTypes = javaEffectiveEvidenceVariableTypes(index, owner, variableTypes)
	code := javaCodeOnly(source)
	resolved := make([]javaResolvedEvidenceConstruction, 0)
	seen := make(map[string]bool)
	for _, match := range javaObjectConstructionRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		class, ok := index.classForType(owner, code[match[2]:match[3]])
		if !ok {
			continue
		}
		args, argsOK := javaCallArguments(source, code, match[3])
		if !argsOK {
			continue
		}
		constructors := javaHelperOverloadsForArguments(class.Constructors, args, variableTypes)
		if len(constructors) == 0 {
			key := javaEvidenceOwnerKey(class) + "\x00<implicit>"
			if !seen[key] {
				seen[key] = true
				resolved = append(resolved, javaResolvedEvidenceConstruction{class: class})
			}
			continue
		}
		for _, constructor := range constructors {
			key := javaHelperVisitKey(class, constructor)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, javaResolvedEvidenceConstruction{
				class:       class,
				constructor: constructor,
			})
		}
	}
	return resolved
}

type javaStoredFunctionalBinding struct {
	name       string
	typeName   string
	owner      javaClass
	ordinal    int
	startLine  int
	endLine    int
	fallback   []javaMethod
	named      map[string][]javaMethod
	reference  string
	unresolved bool
}

type javaStoredFunctionalBindingSet map[string][]javaStoredFunctionalBinding

type javaStoredFunctionalBindingTimeline map[string]javaStoredFunctionalBindingSet

func javaStoredFunctionalBindings(
	current javaMethod,
	owner javaClass,
	index *javaEvidenceIndex,
	invocationSource string,
) javaStoredFunctionalBindingSet {
	if index == nil {
		return nil
	}
	cacheKey := javaEvidenceOwnerKey(owner) + "\x00" + javaEvidenceMethodIdentity(current)
	timeline, cached := index.functionalBindings[cacheKey]
	if !cached {
		timeline = javaBuildStoredFunctionalBindingTimeline(current, owner, index)
		index.functionalBindings[cacheKey] = timeline
		index.functionalBindingBuilds++
	}
	return timeline[javaFunctionalSourceKey(invocationSource)]
}

func javaBuildStoredFunctionalBindingTimeline(
	current javaMethod,
	owner javaClass,
	index *javaEvidenceIndex,
) javaStoredFunctionalBindingTimeline {
	bindings := make(javaStoredFunctionalBindingSet)
	timeline := make(javaStoredFunctionalBindingTimeline)
	ordinal := 0
	var collect func(javaClass, []javaStatement, javaStoredFunctionalBindingSet, bool)
	collect = func(
		bindingOwner javaClass,
		statements []javaStatement,
		scope javaStoredFunctionalBindingSet,
		recordInvocations bool,
	) {
		for _, statement := range statements {
			statementSource := statement.Text
			if statement.Kind == "block" {
				statementSource = statement.Header
			}
			if recordInvocations {
				referencedNames := javaFunctionalReferencedNames(statementSource)
				if len(referencedNames) > 0 {
					sourceKey := javaFunctionalSourceKey(statementSource)
					for _, name := range referencedNames {
						referencedBindings := scope[name]
						if len(referencedBindings) == 0 {
							continue
						}
						if timeline[sourceKey] == nil {
							timeline[sourceKey] = make(javaStoredFunctionalBindingSet)
						}
						timeline[sourceKey][name] = javaUnionStoredFunctionalBindings(
							timeline[sourceKey][name],
							referencedBindings,
						)
					}
				}
			}
			ordinal++
			if binding, ok := javaStoredFunctionalBindingFromStatement(
				bindingOwner,
				statement,
				ordinal,
			); ok {
				if binding.typeName == "" {
					for _, previous := range scope[binding.name] {
						if previous.typeName != "" {
							binding.typeName = previous.typeName
							break
						}
					}
				}
				scope[binding.name] = []javaStoredFunctionalBinding{binding}
				continue
			}
			for _, match := range javaVariableDeclRe.FindAllStringSubmatch(javaCodeOnly(statementSource), -1) {
				if len(match) == 3 {
					delete(scope, strings.TrimSpace(match[2]))
				}
			}
			if assignment := javaTopLevelAssignmentIndex(javaCodeOnly(statementSource)); assignment >= 0 {
				left := strings.TrimSpace(javaCodeOnly(statementSource)[:assignment])
				if name := javaFunctionalAssignedNameRe.FindStringSubmatch(left); len(name) == 2 {
					delete(scope, strings.TrimSpace(name[1]))
				}
			}
			if len(statement.Children) > 0 {
				childScope := javaCloneStoredFunctionalBindings(scope)
				collect(bindingOwner, statement.Children, childScope, recordInvocations)
				// A parser block may be a conditional branch. Preserve both the
				// pre-branch and post-branch targets for outer variables instead
				// of letting whichever branch was scanned last erase another
				// reachable callback.
				for name, before := range scope {
					after, ok := childScope[name]
					if !ok {
						continue
					}
					scope[name] = javaUnionStoredFunctionalBindings(before, after)
				}
			}
		}
	}

	for _, fieldOwner := range index.classHierarchy(owner) {
		for _, field := range fieldOwner.Fields {
			collect(
				fieldOwner,
				parseJavaStatements(field.File, field.Text, field.StartLine),
				bindings,
				false,
			)
		}
	}
	for name, persistentBindings := range javaPersistentFunctionalFieldBindings(
		current,
		owner,
		index,
	) {
		bindings[name] = javaUnionStoredFunctionalBindings(
			bindings[name],
			persistentBindings,
		)
	}
	for _, parameter := range current.Parameters {
		delete(bindings, strings.TrimSpace(parameter.Name))
	}
	collect(owner, current.Statements, bindings, true)
	return timeline
}

func javaCloneStoredFunctionalBindings(
	source javaStoredFunctionalBindingSet,
) javaStoredFunctionalBindingSet {
	cloned := make(javaStoredFunctionalBindingSet, len(source))
	for name, bindings := range source {
		cloned[name] = append([]javaStoredFunctionalBinding(nil), bindings...)
	}
	return cloned
}

func javaPersistentFunctionalFieldBindings(
	current javaMethod,
	owner javaClass,
	index *javaEvidenceIndex,
) javaStoredFunctionalBindingSet {
	phase := ""
	for _, lifecycle := range []string{"init", "action", "teardown"} {
		method, ok := javaVuserLifecycleMethod(index.program, lifecycle)
		if ok && javaEvidenceMethodIdentity(method) == javaEvidenceMethodIdentity(current) {
			phase = lifecycle
			break
		}
	}
	if phase == "" {
		return nil
	}
	cacheKey := javaEvidenceOwnerKey(owner) + "\x00" + phase
	if cached, ok := index.persistentFunctional[cacheKey]; ok {
		return cached
	}
	fieldTypes := make(map[string]string)
	for _, fieldOwner := range index.classHierarchy(owner) {
		for _, field := range fieldOwner.Fields {
			declared := make(map[string]string)
			collectJavaDeclaredVariableTypes(declared, field.Text)
			for name, typeName := range declared {
				fieldTypes[name] = typeName
			}
		}
	}
	if len(fieldTypes) == 0 {
		empty := make(javaStoredFunctionalBindingSet)
		index.persistentFunctional[cacheKey] = empty
		return empty
	}
	state := make(javaStoredFunctionalBindingSet)
	var applyMethod func(
		javaStoredFunctionalBindingSet,
		javaClass,
		javaMethod,
		map[string]bool,
	)
	applyMethod = func(
		targetState javaStoredFunctionalBindingSet,
		methodOwner javaClass,
		method javaMethod,
		callStack map[string]bool,
	) {
		key := javaHelperVisitKey(methodOwner, method)
		if callStack[key] {
			return
		}
		callStack[key] = true
		defer delete(callStack, key)
		shadowed := make(map[string]bool)
		for _, parameter := range method.Parameters {
			shadowed[parameter.Name] = true
		}
		javaApplyPersistentFunctionalStatements(
			targetState,
			shadowed,
			fieldTypes,
			methodOwner,
			method.Statements,
			javaDeclaredVariableTypesWithHierarchy(index, methodOwner, method),
			func(
				helperState javaStoredFunctionalBindingSet,
				source string,
				variableTypes map[string]string,
			) {
				for _, helper := range javaPersistentSameInstanceHelperCalls(
					source,
					methodOwner,
					index,
					variableTypes,
				) {
					if helper.method.Static {
						continue
					}
					applyMethod(
						helperState,
						helper.class,
						helper.method,
						callStack,
					)
				}
			},
		)
	}

	for _, step := range javaPersistentConstructionSteps(owner, index) {
		applyMethod(
			state,
			step.owner,
			step.method,
			make(map[string]bool),
		)
	}
	if phase == "action" || phase == "teardown" {
		if initMethod, ok := javaVuserLifecycleMethod(index.program, "init"); ok {
			applyMethod(
				state,
				javaEvidenceDeclaringMethodOwner(index, owner, initMethod),
				initMethod,
				make(map[string]bool),
			)
		}
	}
	if phase == "action" || phase == "teardown" {
		if actionMethod, ok := javaVuserLifecycleMethod(index.program, "action"); ok {
			beforeAction := javaCloneStoredFunctionalBindings(state)
			afterAction := javaCloneStoredFunctionalBindings(state)
			applyMethod(
				afterAction,
				javaEvidenceDeclaringMethodOwner(index, owner, actionMethod),
				actionMethod,
				make(map[string]bool),
			)
			state = javaUnionStoredFunctionalBindingSets(
				beforeAction,
				afterAction,
			)
		}
	}
	index.persistentFunctional[cacheKey] = state
	return state
}

func javaUnionStoredFunctionalBindingSets(
	left javaStoredFunctionalBindingSet,
	right javaStoredFunctionalBindingSet,
) javaStoredFunctionalBindingSet {
	union := javaCloneStoredFunctionalBindings(left)
	for name, bindings := range right {
		union[name] = javaUnionStoredFunctionalBindings(
			union[name],
			bindings,
		)
	}
	return union
}

type javaPersistentConstructionStep struct {
	owner  javaClass
	method javaMethod
}

func javaPersistentConstructionSteps(
	owner javaClass,
	index *javaEvidenceIndex,
) []javaPersistentConstructionStep {
	if index == nil {
		return nil
	}
	steps := make([]javaPersistentConstructionStep, 0, 8)
	visiting := make(map[string]bool)
	appendInitializers := func(class javaClass) {
		for initializerIndex, initializer := range javaClassInitializerStatements(class) {
			if strings.EqualFold(
				strings.TrimSpace(javaCodeOnly(initializer.Header)),
				"static",
			) {
				continue
			}
			steps = append(steps, javaPersistentConstructionStep{
				owner: class,
				method: javaMethod{
					Name: "<instance_initializer_" +
						strconv.Itoa(initializerIndex) + ">",
					File:       class.File,
					StartLine:  initializer.StartLine,
					EndLine:    initializer.EndLine,
					Statements: initializer.Children,
				},
			})
		}
	}
	var appendImplicit func(javaClass)
	var appendConstructor func(javaClass, javaMethod)
	appendImplicit = func(class javaClass) {
		key := javaEvidenceOwnerKey(class) + "\x00<implicit>"
		if visiting[key] {
			return
		}
		visiting[key] = true
		defer delete(visiting, key)
		if superClass, ok := index.superclass(class); ok {
			targets := javaHelperOverloadsForArguments(
				superClass.Constructors,
				nil,
				javaDeclaredVariableTypesWithHierarchy(
					index,
					class,
					javaMethod{},
				),
			)
			switch {
			case len(targets) == 1:
				appendConstructor(superClass, targets[0])
			case len(superClass.Constructors) == 0:
				appendImplicit(superClass)
			}
		}
		appendInitializers(class)
	}
	appendConstructor = func(class javaClass, constructor javaMethod) {
		key := javaHelperVisitKey(class, constructor)
		if visiting[key] {
			return
		}
		visiting[key] = true
		defer delete(visiting, key)
		variableTypes := javaDeclaredVariableTypesWithHierarchy(
			index,
			class,
			constructor,
		)
		if args, delegated := javaLeadingConstructorArguments(
			constructor,
			"this",
		); delegated {
			targets := javaHelperOverloadsForArguments(
				class.Constructors,
				args,
				variableTypes,
			)
			if len(targets) == 1 &&
				javaEvidenceMethodIdentity(targets[0]) !=
					javaEvidenceMethodIdentity(constructor) {
				appendConstructor(class, targets[0])
			}
			steps = append(steps, javaPersistentConstructionStep{
				owner:  class,
				method: constructor,
			})
			return
		}
		if superClass, ok := index.superclass(class); ok {
			args, explicit := javaLeadingConstructorArguments(
				constructor,
				"super",
			)
			if !explicit {
				args = nil
			}
			targets := javaHelperOverloadsForArguments(
				superClass.Constructors,
				args,
				variableTypes,
			)
			switch {
			case len(targets) == 1:
				appendConstructor(superClass, targets[0])
			case len(superClass.Constructors) == 0 && len(args) == 0:
				appendImplicit(superClass)
			}
		}
		appendInitializers(class)
		steps = append(steps, javaPersistentConstructionStep{
			owner:  class,
			method: constructor,
		})
	}

	zeroArgument := make([]javaMethod, 0, 1)
	for _, constructor := range owner.Constructors {
		if constructor.ParameterCount == 0 {
			zeroArgument = append(zeroArgument, constructor)
		}
	}
	sort.SliceStable(zeroArgument, func(left, right int) bool {
		return javaEvidenceMethodIdentity(zeroArgument[left]) <
			javaEvidenceMethodIdentity(zeroArgument[right])
	})
	switch {
	case len(zeroArgument) == 1:
		appendConstructor(owner, zeroArgument[0])
	case len(owner.Constructors) == 0:
		appendImplicit(owner)
	}
	return steps
}

func javaLeadingConstructorArguments(
	constructor javaMethod,
	keyword string,
) ([]string, bool) {
	if len(constructor.Statements) == 0 {
		return nil, false
	}
	source := strings.TrimSpace(constructor.Statements[0].Text)
	code := strings.TrimSpace(javaCodeOnly(source))
	if !strings.HasPrefix(code, keyword) {
		return nil, false
	}
	after := strings.TrimSpace(strings.TrimPrefix(code, keyword))
	if after == "" || after[0] != '(' {
		return nil, false
	}
	open := strings.IndexByte(code, '(')
	args, ok := javaCallArguments(source, code, open)
	return args, ok
}

func javaPersistentSameInstanceHelperCalls(
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []javaResolvedHelperMethod {
	code := javaCodeOnly(source)
	resolved := make([]javaResolvedHelperMethod, 0)
	appendCandidates := func(
		candidates []javaResolvedHelperMethod,
		args []string,
	) {
		seen := make(map[string]bool)
		methods := make([]javaMethod, 0, len(candidates))
		owners := make(map[string]javaClass, len(candidates))
		for _, candidate := range candidates {
			methods = append(methods, candidate.method)
			owners[javaEvidenceMethodIdentity(candidate.method)] = candidate.class
		}
		for _, method := range javaHelperOverloadsForArguments(
			methods,
			args,
			variableTypes,
		) {
			methodOwner, ok := owners[javaEvidenceMethodIdentity(method)]
			if !ok {
				continue
			}
			key := javaHelperVisitKey(methodOwner, method)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, javaResolvedHelperMethod{
				class:  methodOwner,
				method: method,
			})
		}
	}
	type helperCall struct {
		offset       int
		start        javaClass
		methodName   string
		args         []string
		allowPrivate bool
	}
	calls := make([]helperCall, 0, 2)
	add := func(call helperCall) {
		appendCandidates(
			javaEvidenceMethodsInHierarchy(
				index,
				call.start,
				owner,
				call.methodName,
				call.allowPrivate,
			),
			call.args,
		)
	}

	for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		methodName := strings.TrimSpace(code[match[2]:match[3]])
		if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) {
			continue
		}
		args, ok := javaCallArguments(source, code, match[3])
		if !ok {
			continue
		}
		calls = append(calls, helperCall{
			offset:       match[0],
			start:        owner,
			methodName:   methodName,
			args:         args,
			allowPrivate: true,
		})
	}
	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		qualifier := strings.TrimSpace(code[match[2]:match[3]])
		if qualifier != "this" && qualifier != "super" {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		methodName := strings.TrimSpace(code[match[4]:match[5]])
		if qualifier == "this" {
			calls = append(calls, helperCall{
				offset:       match[0],
				start:        owner,
				methodName:   methodName,
				args:         args,
				allowPrivate: true,
			})
		} else if superClass, ok := index.superclass(owner); ok {
			calls = append(calls, helperCall{
				offset:       match[0],
				start:        superClass,
				methodName:   methodName,
				args:         args,
				allowPrivate: false,
			})
		}
	}
	sort.SliceStable(calls, func(left, right int) bool {
		return calls[left].offset < calls[right].offset
	})
	for _, call := range calls {
		add(call)
	}
	return resolved
}

func javaEvidenceDeclaringMethodOwner(
	index *javaEvidenceIndex,
	fallback javaClass,
	method javaMethod,
) javaClass {
	for _, candidate := range index.classHierarchy(fallback) {
		for _, overload := range javaMethodsNamed(candidate, method.Name) {
			if javaEvidenceMethodIdentity(overload) == javaEvidenceMethodIdentity(method) {
				return candidate
			}
		}
	}
	return fallback
}

func javaApplyPersistentFunctionalStatements(
	state javaStoredFunctionalBindingSet,
	shadowed map[string]bool,
	fieldTypes map[string]string,
	owner javaClass,
	statements []javaStatement,
	variableTypes map[string]string,
	applyHelpers func(
		javaStoredFunctionalBindingSet,
		string,
		map[string]string,
	),
) {
	ordinal := 0
	var apply func(
		[]javaStatement,
		javaStoredFunctionalBindingSet,
		map[string]bool,
		map[string]string,
	)
	apply = func(
		currentStatements []javaStatement,
		currentState javaStoredFunctionalBindingSet,
		currentShadowed map[string]bool,
		currentVariableTypes map[string]string,
	) {
		for _, statement := range currentStatements {
			executed := javaImmediatelyExecutedStatements([]javaStatement{statement})
			if len(executed) == 0 {
				continue
			}
			executedStatement := executed[0]
			ordinal++
			executedSource := executedStatement.Text
			if executedStatement.Kind == "block" {
				executedSource = executedStatement.Header
			}
			if applyHelpers != nil {
				applyHelpers(
					currentState,
					executedSource,
					currentVariableTypes,
				)
			}
			if binding, ok := javaStoredFunctionalBindingFromStatement(
				owner,
				statement,
				ordinal,
			); ok {
				if binding.typeName != "" {
					currentShadowed[binding.name] = true
					collectJavaDeclaredVariableTypes(
						currentVariableTypes,
						statement.Text,
					)
					continue
				}
				if _, field := fieldTypes[binding.name]; field &&
					!currentShadowed[binding.name] {
					binding.typeName = javaSimpleTypeName(fieldTypes[binding.name])
					currentState[binding.name] = []javaStoredFunctionalBinding{binding}
				}
				continue
			}

			statementSource := statement.Text
			if statement.Kind == "block" {
				statementSource = statement.Header
			}
			code := javaCodeOnly(statementSource)
			declaredNames := make([]string, 0)
			for _, match := range javaVariableDeclRe.FindAllStringSubmatch(code, -1) {
				if len(match) == 3 {
					declaredNames = append(declaredNames, strings.TrimSpace(match[2]))
				}
			}
			if assignment := javaTopLevelAssignmentIndex(code); assignment >= 0 {
				left := strings.TrimSpace(code[:assignment])
				if name := javaFunctionalAssignedNameRe.FindStringSubmatch(left); len(name) == 2 {
					fieldName := strings.TrimSpace(name[1])
					if _, field := fieldTypes[fieldName]; field &&
						!currentShadowed[fieldName] {
						delete(currentState, fieldName)
					}
				}
			}
			if len(executedStatement.Children) > 0 {
				childState := javaCloneStoredFunctionalBindings(currentState)
				childShadowed := make(map[string]bool, len(currentShadowed)+len(declaredNames))
				for name, value := range currentShadowed {
					childShadowed[name] = value
				}
				for _, name := range declaredNames {
					childShadowed[name] = true
				}
				childVariableTypes := make(
					map[string]string,
					len(currentVariableTypes)+len(declaredNames),
				)
				for name, typeName := range currentVariableTypes {
					childVariableTypes[name] = typeName
				}
				collectJavaDeclaredVariableTypes(
					childVariableTypes,
					statementSource,
				)
				apply(
					executedStatement.Children,
					childState,
					childShadowed,
					childVariableTypes,
				)
				for name, alternatives := range childState {
					currentState[name] = javaUnionStoredFunctionalBindings(
						currentState[name],
						alternatives,
					)
				}
				continue
			}
			for _, name := range declaredNames {
				currentShadowed[name] = true
			}
			collectJavaDeclaredVariableTypes(
				currentVariableTypes,
				statementSource,
			)
		}
	}
	apply(statements, state, shadowed, variableTypes)
}

func javaFunctionalSourceKey(source string) string {
	return strings.TrimSpace(javaCodeOnly(source))
}

func javaFunctionalReferencedNames(source string) []string {
	code := javaCodeOnly(source)
	seen := make(map[string]bool)
	names := make([]string, 0, 2)
	add := func(name string) {
		name = strings.TrimSpace(strings.TrimPrefix(name, "this."))
		if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
			name = name[dot+1:]
		}
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		add(code[match[2]:match[3]])
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		for _, argument := range args {
			if name, simple := javaFunctionalArgumentName(argument); simple {
				add(name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func javaUnionStoredFunctionalBindings(
	left []javaStoredFunctionalBinding,
	right []javaStoredFunctionalBinding,
) []javaStoredFunctionalBinding {
	combined := append([]javaStoredFunctionalBinding(nil), left...)
	seen := make(map[string]bool, len(combined))
	for _, binding := range combined {
		seen[javaStoredFunctionalBindingIdentity(binding)] = true
	}
	for _, binding := range right {
		key := javaStoredFunctionalBindingIdentity(binding)
		if seen[key] {
			continue
		}
		seen[key] = true
		combined = append(combined, binding)
	}
	return combined
}

func javaStoredFunctionalBindingIdentity(binding javaStoredFunctionalBinding) string {
	return javaEvidenceOwnerKey(binding.owner) + "\x00" +
		binding.name + "\x00" +
		strconv.Itoa(binding.ordinal) + "\x00" +
		strconv.Itoa(binding.startLine) + "\x00" +
		binding.reference
}

func javaStoredFunctionalBindingFromStatement(
	owner javaClass,
	statement javaStatement,
	ordinal int,
) (javaStoredFunctionalBinding, bool) {
	binding := javaStoredFunctionalBinding{
		owner:     owner,
		ordinal:   ordinal,
		startLine: statement.StartLine,
		endLine:   statement.EndLine,
		named:     make(map[string][]javaMethod),
	}

	headerSource := statement.Text
	if statement.Kind == "block" {
		headerSource = statement.Header
	}
	headerCode := javaCodeOnly(headerSource)

	if arrow := strings.Index(headerCode, "->"); arrow >= 0 {
		name, typeName, ok := javaFunctionalAssignment(headerCode[:arrow])
		if !ok {
			return javaStoredFunctionalBinding{}, false
		}
		binding.name = name
		binding.typeName = typeName
		body := ""
		var bodyStatements []javaStatement
		if statement.Kind == "block" {
			bodyStatements = statement.Children
			body = javaStatementsSource(statement.Children)
		} else {
			body = strings.TrimSpace(statement.Text[arrow+2:])
			body = strings.TrimSuffix(body, ";")
			bodyStatements = parseJavaStatements(
				statement.File,
				body+";",
				statement.StartLine,
			)
		}
		method := javaFunctionalSyntheticMethod(
			binding,
			"lambda",
			javaFunctionalLambdaParameters(headerSource[:arrow]),
			body,
			bodyStatements,
		)
		binding.fallback = []javaMethod{method}
		return binding, true
	}

	if javaAnonymousClassInitializerRe.MatchString(headerSource + "{") {
		name, typeName, ok := javaFunctionalAssignment(headerCode)
		if !ok {
			return javaStoredFunctionalBinding{}, false
		}
		binding.name = name
		binding.typeName = typeName
		for memberIndex, member := range statement.Children {
			header := strings.TrimSpace(javaCodeOnly(member.Header))
			match := javaFunctionalMethodHeaderRe.FindStringSubmatch(header)
			if len(match) != 3 || len(member.Children) == 0 {
				continue
			}
			parameters := javaFunctionalParameters(match[2])
			methodBinding := binding
			methodBinding.ordinal = ordinal*1000 + memberIndex + 1
			method := javaFunctionalSyntheticMethod(
				methodBinding,
				strings.ToLower(strings.TrimSpace(match[1])),
				parameters,
				javaStatementsSource(member.Children),
				member.Children,
			)
			methodName := strings.ToLower(strings.TrimSpace(match[1]))
			binding.named[methodName] = append(binding.named[methodName], method)
		}
		if len(binding.named) == 0 {
			binding.unresolved = true
		}
		return binding, true
	}

	if reference := strings.Index(headerCode, "::"); reference >= 0 {
		name, typeName, ok := javaFunctionalAssignment(headerCode[:reference])
		if !ok {
			return javaStoredFunctionalBinding{}, false
		}
		binding.name = name
		binding.typeName = typeName
		assignment := javaTopLevelAssignmentIndex(headerCode[:reference])
		if assignment < 0 {
			binding.unresolved = true
			return binding, true
		}
		binding.reference = strings.TrimSpace(headerCode[assignment+1:])
		binding.reference = strings.TrimSpace(strings.TrimSuffix(binding.reference, ";"))
		if !javaMethodReferenceRe.MatchString(binding.reference) {
			binding.unresolved = true
		}
		return binding, true
	}
	return javaStoredFunctionalBinding{}, false
}

func javaFunctionalAssignment(prefix string) (name string, typeName string, ok bool) {
	assignment := javaTopLevelAssignmentIndex(prefix)
	if assignment < 0 {
		return "", "", false
	}
	left := strings.TrimSpace(prefix[:assignment])
	nameMatch := javaFunctionalAssignedNameRe.FindStringSubmatch(left)
	if len(nameMatch) != 2 {
		return "", "", false
	}
	name = strings.TrimSpace(nameMatch[1])
	for _, declaration := range javaVariableDeclRe.FindAllStringSubmatch(left+" =", -1) {
		if len(declaration) == 3 && declaration[2] == name {
			typeName = javaSimpleTypeName(declaration[1])
		}
	}
	return name, typeName, true
}

func javaTopLevelAssignmentIndex(code string) int {
	parenDepth := 0
	bracketDepth := 0
	for index := 0; index < len(code); index++ {
		switch code[index] {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '=':
			if parenDepth != 0 || bracketDepth != 0 {
				continue
			}
			previousEquals := index > 0 && strings.ContainsRune("=!<>", rune(code[index-1]))
			nextEquals := index+1 < len(code) && code[index+1] == '='
			if !previousEquals && !nextEquals {
				return index
			}
		}
	}
	return -1
}

func javaFunctionalLambdaParameters(prefix string) []javaParameter {
	return javaFunctionalParameters(strings.Join(javaLambdaParameterSources(prefix), ","))
}

func javaFunctionalParameters(source string) []javaParameter {
	var parameters []javaParameter
	for _, part := range splitJavaParameters(source) {
		parameter := parseJavaParameter(part)
		if parameter.Name != "" {
			parameters = append(parameters, parameter)
		}
	}
	return parameters
}

func javaFunctionalSyntheticMethod(
	binding javaStoredFunctionalBinding,
	kind string,
	parameters []javaParameter,
	body string,
	statements []javaStatement,
) javaMethod {
	signatureParts := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		signatureParts = append(signatureParts, strings.TrimSpace(parameter.Type)+" "+strings.TrimSpace(parameter.Name))
	}
	return javaMethod{
		Name: "<functional_" + strings.ToLower(binding.name) + "_" +
			strings.ToLower(kind) + "_" + strconv.Itoa(binding.ordinal) + ">",
		File:               binding.owner.File,
		StartLine:          binding.startLine,
		EndLine:            binding.endLine,
		Visibility:         "private",
		ParameterSignature: strings.Join(signatureParts, ", "),
		ParameterCount:     len(parameters),
		Parameters:         parameters,
		Body:               body,
		Statements:         statements,
	}
}

func javaStatementsSource(statements []javaStatement) string {
	var source []string
	for _, statement := range statements {
		if strings.TrimSpace(statement.Text) != "" {
			source = append(source, statement.Text)
		}
	}
	return strings.Join(source, "\n")
}

func javaInvokedFunctionalReachability(
	current javaMethod,
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) (targets []javaResolvedHelperMethod, unresolved []string) {
	bindings := javaStoredFunctionalBindings(current, owner, index, source)
	effectiveTypes := javaEffectiveEvidenceVariableTypes(index, owner, variableTypes)
	code := javaCodeOnly(source)
	seenTargets := make(map[string]bool)
	seenUnresolved := make(map[string]bool)
	addUnresolved := func(name string) {
		if name != "" && !seenUnresolved[name] {
			seenUnresolved[name] = true
			unresolved = append(unresolved, name)
		}
	}
	addTargets := func(
		valueName string,
		bindings []javaStoredFunctionalBinding,
		invokeMethod string,
		args []string,
		indirect bool,
	) {
		if len(bindings) == 0 {
			if javaLooksLikeFunctionalEvidenceType(
				index,
				owner,
				effectiveTypes[valueName],
			) {
				addUnresolved(valueName)
			}
			return
		}
		unresolvedAlternative := false
		for _, binding := range bindings {
			methods, resolved := javaFunctionalBindingMethods(
				binding,
				invokeMethod,
				args,
				effectiveTypes,
				indirect,
			)
			if !resolved {
				unresolvedAlternative = true
				continue
			}
			for _, method := range methods {
				method = javaFunctionalMethodWithCapturedTypes(method, effectiveTypes)
				key := javaHelperVisitKey(binding.owner, method)
				if seenTargets[key] {
					continue
				}
				seenTargets[key] = true
				targets = append(targets, javaResolvedHelperMethod{
					class:  binding.owner,
					method: method,
				})
			}
		}
		if unresolvedAlternative {
			addUnresolved(valueName)
		}
	}
	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		qualifier := strings.TrimSpace(code[match[2]:match[3]])
		valueName := qualifier
		if dot := strings.LastIndexByte(valueName, '.'); dot >= 0 {
			valueName = valueName[dot+1:]
		}
		invokeMethod := strings.ToLower(strings.TrimSpace(code[match[4]:match[5]]))
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		storedBindings, stored := bindings[valueName]
		if !stored {
			if javaLooksLikeFunctionalEvidenceType(index, owner, effectiveTypes[valueName]) &&
				javaCommonFunctionalInvocation(invokeMethod) {
				addUnresolved(valueName)
			}
		} else {
			addTargets(valueName, storedBindings, invokeMethod, args, false)
		}

		if !javaKnownFunctionalExecutionCall(
			qualifier,
			invokeMethod,
			effectiveTypes,
		) {
			continue
		}
		for _, argument := range args {
			valueName, ok := javaFunctionalArgumentName(argument)
			if !ok {
				continue
			}
			storedBindings, stored := bindings[valueName]
			if !stored &&
				!javaLooksLikeFunctionalEvidenceType(index, owner, effectiveTypes[valueName]) {
				continue
			}
			addTargets(valueName, storedBindings, invokeMethod, nil, true)
		}
	}
	sort.Strings(unresolved)
	return targets, unresolved
}

func javaFunctionalBindingMethods(
	binding javaStoredFunctionalBinding,
	invokeMethod string,
	args []string,
	effectiveTypes map[string]string,
	indirect bool,
) ([]javaMethod, bool) {
	if binding.unresolved {
		return nil, false
	}
	if indirect {
		if len(binding.fallback) > 0 {
			return append([]javaMethod(nil), binding.fallback...), true
		}
		if len(binding.named) > 0 {
			samMethod, _ := javaFunctionalSAM(binding.typeName)
			named := binding.named[samMethod]
			if len(named) == 0 && len(binding.named) == 1 {
				for _, candidates := range binding.named {
					named = candidates
				}
			}
			if len(named) == 0 {
				return nil, false
			}
			return append([]javaMethod(nil), named...), true
		}
		if binding.reference != "" {
			_, arity := javaFunctionalSAM(binding.typeName)
			if arity < 0 {
				return nil, false
			}
			placeholders := make([]string, arity)
			for index := range placeholders {
				placeholders[index] = "functionalArg" + strconv.Itoa(index)
			}
			method, resolved := javaFunctionalMethodReferenceSyntheticMethod(
				binding,
				invokeMethod,
				placeholders,
			)
			if resolved {
				return []javaMethod{method}, true
			}
		}
		return nil, false
	}

	if named := binding.named[invokeMethod]; len(named) > 0 {
		methods := javaHelperOverloadsForArguments(named, args, effectiveTypes)
		return methods, len(methods) > 0
	}
	if len(binding.named) > 0 ||
		!javaFunctionalInvocationMatchesType(binding.typeName, invokeMethod) {
		return nil, false
	}
	if len(binding.fallback) > 0 {
		methods := javaHelperOverloadsForArguments(binding.fallback, args, effectiveTypes)
		return methods, len(methods) > 0
	}
	if binding.reference != "" {
		method, resolved := javaFunctionalMethodReferenceSyntheticMethod(
			binding,
			invokeMethod,
			args,
		)
		if resolved {
			return []javaMethod{method}, true
		}
	}
	return nil, false
}

func javaFunctionalMethodWithCapturedTypes(
	method javaMethod,
	effectiveTypes map[string]string,
) javaMethod {
	parameterNames := make(map[string]bool, len(method.Parameters))
	for _, parameter := range method.Parameters {
		parameterNames[strings.TrimSpace(parameter.Name)] = true
	}
	names := make([]string, 0)
	for name, typeName := range effectiveTypes {
		name = strings.TrimSpace(name)
		typeName = strings.TrimSpace(typeName)
		if name == "" || typeName == "" || parameterNames[name] ||
			!javaUnqualifiedIdentifierUsed(javaCodeOnly(method.Body), name) {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return method
	}
	sort.Strings(names)
	declarations := make([]javaStatement, 0, len(names))
	for _, name := range names {
		declarations = append(declarations, javaStatement{
			Kind:      "statement",
			Text:      effectiveTypes[name] + " " + name + ";",
			File:      method.File,
			StartLine: method.StartLine,
			EndLine:   method.StartLine,
		})
	}
	method.Statements = append(declarations, method.Statements...)
	return method
}

func javaFunctionalArgumentName(argument string) (string, bool) {
	code := strings.TrimSpace(javaCodeOnly(argument))
	code = strings.TrimPrefix(code, "this.")
	if code == "" {
		return "", false
	}
	for index := range len(code) {
		if !isJavaIdentifierByte(code[index]) {
			return "", false
		}
	}
	return code, true
}

func javaKnownFunctionalExecutionCall(
	qualifier string,
	methodName string,
	effectiveTypes map[string]string,
) bool {
	valueName := strings.TrimSpace(qualifier)
	if dot := strings.LastIndexByte(valueName, '.'); dot >= 0 {
		valueName = valueName[dot+1:]
	}
	receiverType := strings.ToLower(javaSimpleTypeName(effectiveTypes[valueName]))
	if dot := strings.LastIndexByte(receiverType, '.'); dot >= 0 {
		receiverType = receiverType[dot+1:]
	}
	qualifierType := strings.ToLower(javaSimpleTypeName(qualifier))
	if dot := strings.LastIndexByte(qualifierType, '.'); dot >= 0 {
		qualifierType = qualifierType[dot+1:]
	}
	methodName = strings.ToLower(strings.TrimSpace(methodName))
	receiverContains := func(parts ...string) bool {
		for _, part := range parts {
			if strings.Contains(receiverType, part) ||
				strings.Contains(qualifierType, part) {
				return true
			}
		}
		return false
	}
	switch methodName {
	case "execute", "submit", "submitlistenable", "invokeall", "invokeany":
		return receiverContains(
			"executor",
			"forkjoinpool",
			"completionservice",
		)
	case "foreach":
		return receiverContains(
			"iterable",
			"collection",
			"list",
			"set",
			"map",
			"stream",
		)
	case "removeif":
		return receiverContains("collection", "list", "set")
	case "replaceall":
		return receiverContains("list", "map")
	case "compute", "computeifabsent", "computeifpresent", "merge":
		return receiverContains("map")
	case "ifpresent", "ifpresentorelse", "orelseget":
		return receiverContains("optional")
	case "thenapply", "thenapplyasync", "thenaccept", "thenacceptasync",
		"thenrun", "thenrunasync", "thencompose", "thencomposeasync",
		"whencomplete", "whencompleteasync", "handle", "handleasync",
		"exceptionally", "exceptionallycompose":
		return receiverContains("completionstage", "completablefuture")
	case "runasync", "supplyasync":
		return receiverContains("completablefuture")
	case "invokelater", "invokeandwait":
		return receiverContains("swingutilities", "eventqueue")
	default:
		return false
	}
}

func javaFunctionalSAM(typeName string) (methodName string, arity int) {
	normalized := strings.ToLower(strings.TrimSpace(javaSimpleTypeName(typeName)))
	if dot := strings.LastIndexByte(normalized, '.'); dot >= 0 {
		normalized = normalized[dot+1:]
	}
	switch normalized {
	case "runnable":
		return "run", 0
	case "callable":
		return "call", 0
	case "supplier":
		return "get", 0
	case "function", "unaryoperator":
		return "apply", 1
	case "bifunction", "binaryoperator":
		return "apply", 2
	case "consumer":
		return "accept", 1
	case "biconsumer":
		return "accept", 2
	case "predicate":
		return "test", 1
	case "bipredicate":
		return "test", 2
	case "comparator":
		return "compare", 2
	default:
		return "", -1
	}
}

func javaFunctionalMethodReferenceSyntheticMethod(
	binding javaStoredFunctionalBinding,
	invokeMethod string,
	args []string,
) (javaMethod, bool) {
	match := javaMethodReferenceRe.FindStringSubmatch(binding.reference)
	if len(match) != 3 {
		return javaMethod{}, false
	}
	qualifier := strings.TrimSpace(match[1])
	member := strings.TrimSpace(match[2])
	argumentText := strings.Join(args, ", ")
	var calls []string
	if strings.EqualFold(member, "new") {
		calls = append(calls, "new "+qualifier+"("+argumentText+");")
	} else {
		calls = append(calls, qualifier+"."+member+"("+argumentText+");")
		if len(args) > 0 {
			calls = append(calls, qualifier+"."+member+"("+strings.Join(args[1:], ", ")+");")
		}
	}
	body := strings.Join(calls, "\n")
	methodBinding := binding
	methodBinding.ordinal = binding.ordinal*1000 + len(args) + 1
	method := javaFunctionalSyntheticMethod(
		methodBinding,
		"reference_"+invokeMethod,
		nil,
		body,
		parseJavaStatements(binding.owner.File, body, binding.startLine),
	)
	method.ParameterSignature = "reference:" + invokeMethod + ":" + strconv.Itoa(len(args))
	return method, true
}

func javaLooksLikeFunctionalType(typeName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(javaSimpleTypeName(typeName)))
	if dot := strings.LastIndexByte(normalized, '.'); dot >= 0 {
		normalized = normalized[dot+1:]
	}
	switch normalized {
	case "runnable", "callable",
		"function", "bifunction", "unaryoperator", "binaryoperator",
		"consumer", "biconsumer", "supplier",
		"predicate", "bipredicate", "comparator":
		return true
	default:
		return strings.HasSuffix(normalized, "function") ||
			strings.HasSuffix(normalized, "consumer") ||
			strings.HasSuffix(normalized, "supplier") ||
			strings.HasSuffix(normalized, "predicate")
	}
}

func javaLooksLikeFunctionalEvidenceType(
	index *javaEvidenceIndex,
	owner javaClass,
	typeName string,
) bool {
	if javaLooksLikeFunctionalType(typeName) || index == nil {
		return javaLooksLikeFunctionalType(typeName)
	}
	class, ok := index.classForType(owner, typeName)
	if !ok || !strings.EqualFold(strings.TrimSpace(class.Kind), "interface") {
		return false
	}
	abstractSignatures := make(map[string]bool)
	visited := make(map[string]bool)
	var inspect func(javaClass)
	inspect = func(current javaClass) {
		key := javaEvidenceOwnerKey(current)
		if visited[key] {
			return
		}
		visited[key] = true
		for _, parentName := range current.Interfaces {
			if parent, resolved := index.classForType(current, parentName); resolved {
				inspect(parent)
			}
		}
		for _, overloads := range current.MethodOverloads {
			for _, method := range overloads {
				if method.Static {
					continue
				}
				if javaFunctionalObjectMethod(method) {
					continue
				}
				signature := javaEvidenceOverrideSignature(index, current, method)
				if method.Default {
					delete(abstractSignatures, signature)
				} else if method.Abstract {
					abstractSignatures[signature] = true
				}
			}
		}
	}
	inspect(class)
	return len(abstractSignatures) == 1
}

func javaFunctionalObjectMethod(method javaMethod) bool {
	switch strings.ToLower(strings.TrimSpace(method.Name)) {
	case "tostring", "hashcode", "getclass", "clone", "finalize":
		return method.ParameterCount == 0
	case "equals":
		return method.ParameterCount == 1 &&
			len(method.Parameters) == 1 &&
			strings.EqualFold(
				strings.TrimSpace(javaSimpleTypeName(method.Parameters[0].Type)),
				"Object",
			)
	default:
		return false
	}
}

func javaCommonFunctionalInvocation(methodName string) bool {
	switch strings.ToLower(strings.TrimSpace(methodName)) {
	case "run", "call", "apply", "accept", "get", "test", "compare",
		"execute", "invoke", "handle":
		return true
	default:
		return strings.HasPrefix(methodName, "applyas") ||
			strings.HasPrefix(methodName, "getas")
	}
}

func javaReturnedReceiverProducerCandidates(
	call javaReturnedReceiverCall,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) ([]javaResolvedHelperMethod, bool) {
	if index == nil {
		return nil, false
	}
	qualifier := strings.TrimSpace(call.producerQualifier)
	var candidates []javaResolvedHelperMethod
	var virtualReceiver *javaClass
	archivedReceiver := false
	addOwner := func(class javaClass, allowPrivate bool, virtual bool) {
		archivedReceiver = true
		candidates = append(
			candidates,
			javaEvidenceMethodsInHierarchy(
				index,
				class,
				owner,
				call.producerName,
				allowPrivate,
			)...,
		)
		if virtual {
			copy := class
			virtualReceiver = &copy
		}
	}
	switch qualifier {
	case "":
		addOwner(owner, true, true)
		candidates = append(
			candidates,
			javaEvidenceStaticImportedMethods(
				index,
				owner,
				call.producerName,
			)...,
		)
	case "this":
		addOwner(owner, true, true)
	case "super":
		if superClass, ok := index.superclass(owner); ok {
			addOwner(superClass, false, false)
		}
	default:
		if strings.Contains(qualifier, ".") {
			valueRoot, rootLimitExceeded := index.memberReceiverHasValueRoot(
				owner,
				qualifier,
				variableTypes,
			)
			if rootLimitExceeded {
				archivedReceiver = true
				break
			}
			if valueRoot {
				if class, archived, exact, _ := index.resolveMemberReceiver(
					owner,
					qualifier,
					variableTypes,
				); exact {
					addOwner(class, javaSameEvidenceClass(class, owner), true)
				} else if archived {
					archivedReceiver = true
				}
				break
			}
			if class, ok := index.classForType(owner, qualifier); ok {
				addOwner(class, javaSameEvidenceClass(class, owner), false)
				break
			}
			if class, archived, exact, _ := index.resolveMemberReceiver(
				owner,
				qualifier,
				variableTypes,
			); exact {
				addOwner(class, javaSameEvidenceClass(class, owner), true)
				break
			} else if archived {
				archivedReceiver = true
				break
			}
		}
		valueName := qualifier
		if dot := strings.LastIndexByte(valueName, '.'); dot >= 0 {
			valueName = valueName[dot+1:]
		}
		if typeName := variableTypes[valueName]; typeName != "" {
			if class, ok := index.classForType(owner, typeName); ok {
				addOwner(class, javaSameEvidenceClass(class, owner), true)
			} else if index.programContainsSimpleType(typeName) ||
				index.archivedNestedType(owner, typeName) {
				archivedReceiver = true
			}
		} else if class, ok := index.classForType(owner, qualifier); ok {
			addOwner(class, javaSameEvidenceClass(class, owner), false)
		} else if index.programContainsSimpleType(qualifier) {
			archivedReceiver = true
		}
	}

	methods := make([]javaMethod, 0, len(candidates))
	owners := make(map[string]javaClass, len(candidates))
	for _, candidate := range candidates {
		methods = append(methods, candidate.method)
		owners[javaEvidenceMethodIdentity(candidate.method)] = candidate.class
	}
	selected := javaHelperOverloadsForArguments(
		methods,
		call.producerArgs,
		variableTypes,
	)
	resolved := make([]javaResolvedHelperMethod, 0, len(selected))
	seen := make(map[string]bool)
	appendResolved := func(candidate javaResolvedHelperMethod) {
		key := javaHelperVisitKey(candidate.class, candidate.method)
		if seen[key] {
			return
		}
		seen[key] = true
		resolved = append(resolved, candidate)
	}
	for _, method := range selected {
		methodOwner, ok := owners[javaEvidenceMethodIdentity(method)]
		if !ok {
			continue
		}
		base := javaResolvedHelperMethod{class: methodOwner, method: method}
		appendResolved(base)
		if virtualReceiver != nil {
			for _, override := range javaEvidenceVirtualDispatchTargets(
				index,
				*virtualReceiver,
				methodOwner,
				method,
			) {
				appendResolved(override)
			}
			if strings.EqualFold(
				strings.TrimSpace(virtualReceiver.Kind),
				"interface",
			) {
				for _, target := range javaEvidenceInterfaceDispatchCandidates(
					index,
					*virtualReceiver,
					owner,
					call.producerName,
					call.producerArgs,
					variableTypes,
				) {
					appendResolved(target)
				}
			}
		}
	}
	return resolved, archivedReceiver
}

func (index *javaEvidenceIndex) programContainsSimpleType(typeName string) bool {
	if index == nil {
		return false
	}
	return index.simpleTypes[javaComparableType(typeName)]
}

func javaUnresolvedReturnedReceiverCalls(
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []string {
	if index == nil {
		return nil
	}
	effectiveTypes := javaEffectiveEvidenceVariableTypes(
		index,
		owner,
		variableTypes,
	)
	unresolved := make(map[string]bool)
	for _, call := range javaReturnedReceiverCalls(source) {
		_, archivedProducer, labels := javaResolvedReturnedReceiverChain(
			call,
			owner,
			index,
			effectiveTypes,
		)
		if !archivedProducer {
			continue
		}
		for _, label := range labels {
			unresolved[label] = true
		}
	}
	labels := make([]string, 0, len(unresolved))
	for label := range unresolved {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	return labels
}

type javaReturnedReceiverState struct {
	owner    javaClass
	typeName string
}

// javaResolvedReturnedReceiverChain resolves every method-return hop in one
// expression, retaining every conservative overload/virtual-dispatch target.
// It also returns exact unresolved labels whenever an archived chain cannot be
// completed, so callers never silently truncate a deeper expression.
func javaResolvedReturnedReceiverChain(
	call javaReturnedReceiverCall,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) ([]javaResolvedHelperMethod, bool, []string) {
	producers, archivedProducer := javaReturnedReceiverProducerCandidates(
		call,
		owner,
		index,
		variableTypes,
	)
	if !archivedProducer {
		return nil, false, nil
	}
	prefix := strings.TrimSpace(call.producerQualifier)
	if prefix != "" {
		prefix += "."
	}
	prefix += strings.TrimSpace(call.producerName) + "()"
	unresolved := make([]string, 0, 1)
	if call.limitExceeded {
		unresolved = append(unresolved, prefix+".<receiver-chain-limit>")
	}
	if len(producers) == 0 {
		// Preserve the legacy allowance for an unqualified proprietary static
		// import that is absent from uploaded source. Once an archived producer
		// is resolved, every later hop is fail-closed.
		if strings.TrimSpace(call.producerQualifier) != "" {
			unresolved = append(unresolved, prefix)
		}
		return nil, true, uniqueSortedStrings(unresolved)
	}
	states := make([]javaReturnedReceiverState, 0, len(producers))
	for _, producer := range producers {
		states = append(states, javaReturnedReceiverState{
			owner:    producer.class,
			typeName: producer.method.ReturnType,
		})
	}
	resolved := make([]javaResolvedHelperMethod, 0)
	seenResolved := make(map[string]bool)
	fieldLookupWork := 0
	for _, link := range call.links() {
		if link.isMethod() {
			prefix += "." + strings.TrimSpace(link.methodName) + "()"
		} else {
			prefix += "." + strings.TrimSpace(link.fieldName)
		}
		nextStates := make([]javaReturnedReceiverState, 0)
		seenStates := make(map[string]bool)
		linkResolved := false
		externalPath := false
		for _, state := range states {
			receiverType := state.typeName
			receiverOwner := state.owner
			validReceiver := true
			for _, operation := range link.receiverOperations {
				switch operation.kind {
				case javaReturnedReceiverCast:
					receiverType = operation.castType
					receiverOwner = owner
				case javaReturnedReceiverArrayElement:
					elementType, isArray := javaArrayElementType(receiverType)
					if !isArray {
						validReceiver = false
						break
					}
					receiverType = elementType
				}
			}
			if !validReceiver {
				continue
			}
			returnClass, ok := index.classForType(receiverOwner, receiverType)
			if !ok {
				if !index.programContainsSimpleType(receiverType) &&
					!index.archivedNestedType(receiverOwner, receiverType) {
					externalPath = true
				}
				continue
			}
			if len(returnClass.Unparsed) > 0 {
				continue
			}
			if !link.isMethod() {
				fieldType, found, exceeded := index.fieldTypeInHierarchy(
					returnClass,
					link.fieldName,
					&fieldLookupWork,
				)
				if exceeded {
					unresolved = append(
						unresolved,
						prefix+".<member-lookup-limit>",
					)
					continue
				}
				if !found {
					continue
				}
				stateKey := javaEvidenceOwnerKey(returnClass) + "\x00" +
					javaComparableType(fieldType)
				if !seenStates[stateKey] {
					seenStates[stateKey] = true
					nextStates = append(nextStates, javaReturnedReceiverState{
						owner:    returnClass,
						typeName: fieldType,
					})
				}
				linkResolved = true
				continue
			}
			candidates := javaEvidenceMethodsInHierarchy(
				index,
				returnClass,
				owner,
				link.methodName,
				javaSameEvidenceClass(returnClass, owner),
			)
			methods := make([]javaMethod, 0, len(candidates))
			owners := make(map[string]javaClass, len(candidates))
			for _, candidate := range candidates {
				methods = append(methods, candidate.method)
				owners[javaEvidenceMethodIdentity(candidate.method)] = candidate.class
			}
			for _, method := range javaHelperOverloadsForArguments(
				methods,
				link.args,
				variableTypes,
			) {
				methodOwner, exists := owners[javaEvidenceMethodIdentity(method)]
				if !exists {
					continue
				}
				targets := []javaResolvedHelperMethod{{class: methodOwner, method: method}}
				targets = append(
					targets,
					javaEvidenceVirtualDispatchTargets(
						index,
						returnClass,
						methodOwner,
						method,
					)...,
				)
				if strings.EqualFold(strings.TrimSpace(returnClass.Kind), "interface") {
					targets = append(
						targets,
						javaEvidenceInterfaceDispatchCandidates(
							index,
							returnClass,
							owner,
							link.methodName,
							link.args,
							variableTypes,
						)...,
					)
				}
				for _, target := range targets {
					key := javaHelperVisitKey(target.class, target.method)
					if !seenResolved[key] {
						seenResolved[key] = true
						resolved = append(resolved, target)
					}
					if !seenStates[key] {
						seenStates[key] = true
						nextStates = append(nextStates, javaReturnedReceiverState{
							owner:    target.class,
							typeName: target.method.ReturnType,
						})
					}
					linkResolved = true
				}
			}
		}
		if !linkResolved {
			if !externalPath {
				unresolved = append(unresolved, prefix)
			}
			break
		}
		states = nextStates
	}
	return resolved, true, uniqueSortedStrings(unresolved)
}

func javaUnresolvedInvokedFunctionalValues(
	current javaMethod,
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []string {
	_, unresolved := javaInvokedFunctionalReachability(
		current,
		source,
		owner,
		index,
		variableTypes,
	)
	return unresolved
}

func javaFunctionalInvocationMatchesType(typeName, methodName string) bool {
	normalized := strings.ToLower(strings.TrimSpace(javaSimpleTypeName(typeName)))
	if dot := strings.LastIndexByte(normalized, '.'); dot >= 0 {
		normalized = normalized[dot+1:]
	}
	methodName = strings.ToLower(strings.TrimSpace(methodName))
	switch normalized {
	case "runnable":
		return methodName == "run"
	case "callable":
		return methodName == "call"
	case "function", "bifunction", "unaryoperator", "binaryoperator":
		return methodName == "apply" || strings.HasPrefix(methodName, "applyas")
	case "consumer", "biconsumer":
		return methodName == "accept"
	case "supplier":
		return methodName == "get" || strings.HasPrefix(methodName, "getas")
	case "predicate", "bipredicate":
		return methodName == "test"
	case "comparator":
		return methodName == "compare"
	}
	switch methodName {
	case "tostring", "hashcode", "equals", "getclass", "clone", "finalize":
		return false
	default:
		return methodName != ""
	}
}

func javaResolvedEvidenceHelperCalls(
	source string,
	owner javaClass,
	current javaMethod,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []javaResolvedHelperMethod {
	variableTypes = javaEffectiveEvidenceVariableTypes(index, owner, variableTypes)
	code := javaCodeOnly(source)
	resolved := make([]javaResolvedHelperMethod, 0)
	seen := map[string]bool{}
	appendResolved := func(methodOwner javaClass, method javaMethod) {
		key := javaHelperVisitKey(methodOwner, method)
		if seen[key] {
			return
		}
		seen[key] = true
		resolved = append(resolved, javaResolvedHelperMethod{class: methodOwner, method: method})
	}
	addCandidates := func(
		candidates []javaResolvedHelperMethod,
		args []string,
		virtualReceiver *javaClass,
	) bool {
		methods := make([]javaMethod, 0, len(candidates))
		owners := make(map[string]javaClass, len(candidates))
		for _, candidate := range candidates {
			methods = append(methods, candidate.method)
			owners[javaEvidenceMethodIdentity(candidate.method)] = candidate.class
		}
		selectedMethods := javaHelperOverloadsForArguments(methods, args, variableTypes)
		for _, method := range selectedMethods {
			methodOwner, ok := owners[javaEvidenceMethodIdentity(method)]
			if !ok {
				continue
			}
			appendResolved(methodOwner, method)
			if virtualReceiver == nil {
				continue
			}
			for _, override := range javaEvidenceVirtualDispatchTargets(
				index,
				*virtualReceiver,
				methodOwner,
				method,
			) {
				appendResolved(override.class, override.method)
			}
		}
		return len(selectedMethods) > 0
	}
	add := func(
		class javaClass,
		methodName string,
		args []string,
		allowStartPrivate bool,
		virtual bool,
	) bool {
		var virtualReceiver *javaClass
		if virtual {
			copy := class
			virtualReceiver = &copy
		}
		return addCandidates(
			javaEvidenceMethodsInHierarchy(index, class, owner, methodName, allowStartPrivate),
			args,
			virtualReceiver,
		)
	}
	addConstructor := func(constructorOwner javaClass, args []string) {
		for _, constructor := range javaHelperOverloadsForArguments(constructorOwner.Constructors, args, variableTypes) {
			key := javaHelperVisitKey(constructorOwner, constructor)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, javaResolvedHelperMethod{class: constructorOwner, method: constructor})
		}
	}

	for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		methodName := code[match[2]:match[3]]
		if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) {
			continue
		}
		args, ok := javaCallArguments(source, code, match[3])
		if !ok {
			continue
		}
		if !add(owner, methodName, args, true, true) {
			addCandidates(javaEvidenceStaticImportedMethods(index, owner, methodName), args, nil)
		}
	}

	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		qualifier := strings.TrimSpace(code[match[2]:match[3]])
		methodName := strings.TrimSpace(code[match[4]:match[5]])
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		switch qualifier {
		case "this":
			add(owner, methodName, args, true, true)
			continue
		case "super":
			if superClass, ok := index.superclass(owner); ok {
				add(superClass, methodName, args, false, false)
			}
			continue
		}
		if strings.Contains(qualifier, ".") {
			valueRoot, rootLimitExceeded := index.memberReceiverHasValueRoot(
				owner,
				qualifier,
				variableTypes,
			)
			if rootLimitExceeded {
				continue
			}
			if valueRoot {
				if class, _, exact, _ := index.resolveMemberReceiver(
					owner,
					qualifier,
					variableTypes,
				); exact {
					add(class, methodName, args, javaSameEvidenceClass(class, owner), true)
				}
				continue
			}
			if class, ok := index.classForType(owner, qualifier); ok {
				add(class, methodName, args, javaSameEvidenceClass(class, owner), false)
				continue
			}
			if class, archived, exact, _ := index.resolveMemberReceiver(
				owner,
				qualifier,
				variableTypes,
			); exact {
				add(class, methodName, args, javaSameEvidenceClass(class, owner), true)
				continue
			} else if archived {
				continue
			}
		}
		valueName := qualifier
		if dot := strings.LastIndexByte(valueName, '.'); dot >= 0 {
			valueName = valueName[dot+1:]
		}
		if typeName := variableTypes[valueName]; typeName != "" {
			if class, ok := index.classForType(owner, typeName); ok {
				add(class, methodName, args, javaSameEvidenceClass(class, owner), true)
				if strings.EqualFold(strings.TrimSpace(class.Kind), "interface") {
					for _, target := range javaEvidenceInterfaceDispatchCandidates(
						index,
						class,
						owner,
						methodName,
						args,
						variableTypes,
					) {
						appendResolved(target.class, target.method)
					}
				}
				continue
			}
		}
		if class, ok := index.classForType(owner, qualifier); ok {
			add(class, methodName, args, javaSameEvidenceClass(class, owner), false)
			continue
		}
	}
	for _, match := range javaParenthesizedReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		valueName := strings.TrimSpace(code[match[2]:match[3]])
		typeName := variableTypes[valueName]
		if typeName == "" {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		if class, resolved := index.classForType(owner, typeName); resolved {
			add(
				class,
				code[match[4]:match[5]],
				args,
				javaSameEvidenceClass(class, owner),
				true,
			)
		}
	}
	for _, call := range javaReturnedReceiverCalls(source) {
		targets, _, _ := javaResolvedReturnedReceiverChain(
			call,
			owner,
			index,
			variableTypes,
		)
		for _, target := range targets {
			appendResolved(target.class, target.method)
		}
	}
	for _, match := range javaCastReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		if class, resolved := index.classForType(owner, code[match[2]:match[3]]); resolved {
			add(
				class,
				code[match[4]:match[5]],
				args,
				javaSameEvidenceClass(class, owner),
				true,
			)
		}
	}
	for _, match := range javaArrayReceiverCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		args, ok := javaCallArguments(source, code, match[5])
		if !ok {
			continue
		}
		arrayName := strings.TrimSpace(code[match[2]:match[3]])
		class, resolved := index.classForType(owner, variableTypes[arrayName])
		if !resolved {
			continue
		}
		add(
			class,
			code[match[4]:match[5]],
			args,
			javaSameEvidenceClass(class, owner),
			true,
		)
	}
	for _, match := range javaNewObjectCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		args, argsOK := javaCallArguments(source, code, match[5])
		if !argsOK {
			continue
		}
		if class, ok := index.classForType(owner, code[match[2]:match[3]]); ok {
			add(class, code[match[4]:match[5]], args, javaSameEvidenceClass(class, owner), false)
		}
	}
	for _, callback := range javaInvokedFunctionalReachabilityTargets(
		current,
		source,
		owner,
		index,
		variableTypes,
	) {
		appendResolved(callback.class, callback.method)
	}
	if current.Name == owner.Name {
		for _, match := range javaThisConstructorCallRe.FindAllStringIndex(code, -1) {
			open := match[1] - 1
			args, ok := javaCallArguments(source, code, open)
			if ok {
				addConstructor(owner, args)
			}
		}
		if strings.TrimSpace(owner.SuperClass) != "" {
			if superClass, ok := index.classForType(owner, owner.SuperClass); ok {
				for _, match := range javaSuperConstructorCallRe.FindAllStringIndex(code, -1) {
					open := match[1] - 1
					args, argsOK := javaCallArguments(source, code, open)
					if argsOK {
						addConstructor(superClass, args)
					}
				}
			}
		}
	}
	return resolved
}

type javaReturnedReceiverCall struct {
	producerQualifier  string
	producerName       string
	producerArgs       []string
	receiverOperations []javaReturnedReceiverOperation
	methodName         string
	fieldName          string
	args               []string
	continuation       []javaReturnedReceiverLink
	limitExceeded      bool
}

type javaReturnedReceiverLink struct {
	receiverOperations []javaReturnedReceiverOperation
	methodName         string
	fieldName          string
	args               []string
}

func (call javaReturnedReceiverCall) links() []javaReturnedReceiverLink {
	links := make([]javaReturnedReceiverLink, 0, len(call.continuation)+1)
	if strings.TrimSpace(call.methodName) != "" {
		links = append(links, javaReturnedReceiverLink{
			receiverOperations: call.receiverOperations,
			methodName:         call.methodName,
			args:               call.args,
		})
	} else if strings.TrimSpace(call.fieldName) != "" {
		links = append(links, javaReturnedReceiverLink{
			receiverOperations: call.receiverOperations,
			fieldName:          call.fieldName,
		})
	}
	links = append(links, call.continuation...)
	return links
}

func (link javaReturnedReceiverLink) isMethod() bool {
	return strings.TrimSpace(link.methodName) != ""
}

type javaReturnedReceiverOperationKind uint8

const (
	javaReturnedReceiverCast javaReturnedReceiverOperationKind = iota + 1
	javaReturnedReceiverArrayElement
)

type javaReturnedReceiverOperation struct {
	kind     javaReturnedReceiverOperationKind
	castType string
}

func javaReturnedReceiverCalls(source string) []javaReturnedReceiverCall {
	code := javaCodeOnly(source)
	parenthesisCloseByOpen, parenthesisOpenByClose := javaDelimiterPairs(
		code,
		'(',
		')',
	)
	bracketCloseByOpen, _ := javaDelimiterPairs(code, '[', ']')
	calls := make([]javaReturnedReceiverCall, 0)
	appendCandidate := func(
		expressionStart int,
		producerQualifier string,
		producerNameStart int,
		producerNameEnd int,
	) {
		producerName := strings.TrimSpace(code[producerNameStart:producerNameEnd])
		if javaReturnedReceiverKeyword(producerName) {
			return
		}
		open := producerNameEnd
		for open < len(code) && code[open] != '(' {
			open++
		}
		if open >= len(code) {
			return
		}
		close, ok := parenthesisCloseByOpen[open]
		if !ok {
			return
		}
		cursor := close + 1
		receiverOperations := make([]javaReturnedReceiverOperation, 0, 2)
		for {
			for cursor < len(code) && strings.ContainsRune(" \t\r\n", rune(code[cursor])) {
				cursor++
			}
			switch {
			case cursor < len(code) && code[cursor] == '[':
				closeBracket, matched := bracketCloseByOpen[cursor]
				if !matched {
					cursor = len(code)
					continue
				}
				receiverOperations = append(
					receiverOperations,
					javaReturnedReceiverOperation{
						kind: javaReturnedReceiverArrayElement,
					},
				)
				cursor = closeBracket + 1
			case cursor < len(code) && code[cursor] == ')':
				openWrapper, wraps := parenthesisOpenByClose[cursor]
				if !wraps ||
					openWrapper >= expressionStart ||
					javaReturnedReceiverInvocationOpen(code, openWrapper) {
					goto receiverMethod
				}
				castTypes, validWrapper := javaReturnedReceiverWrapperCasts(
					code,
					parenthesisCloseByOpen,
					openWrapper+1,
					expressionStart,
				)
				if !validWrapper {
					goto receiverMethod
				}
				for castIndex := len(castTypes) - 1; castIndex >= 0; castIndex-- {
					receiverOperations = append(
						receiverOperations,
						javaReturnedReceiverOperation{
							kind:     javaReturnedReceiverCast,
							castType: castTypes[castIndex],
						},
					)
				}
				expressionStart = openWrapper
				cursor++
			default:
				goto receiverMethod
			}
		}
	receiverMethod:
		producerArgs, producerOK := javaCallArguments(
			source,
			code,
			producerNameEnd,
		)
		if !producerOK {
			return
		}
		call := javaReturnedReceiverCall{
			producerQualifier: strings.TrimSpace(producerQualifier),
			producerName:      producerName,
			producerArgs:      producerArgs,
		}
		links := make([]javaReturnedReceiverLink, 0, 4)
		operations := receiverOperations
		hasConsumerMethod := false
		for {
			if len(links) > 0 {
				operations = make([]javaReturnedReceiverOperation, 0, 1)
			receiverOperations:
				for {
					for cursor < len(code) && strings.ContainsRune(" \t\r\n", rune(code[cursor])) {
						cursor++
					}
					switch {
					case cursor < len(code) && code[cursor] == '[':
						closeBracket, ok := bracketCloseByOpen[cursor]
						if !ok {
							call.limitExceeded = true
							break receiverOperations
						}
						operations = append(operations, javaReturnedReceiverOperation{
							kind: javaReturnedReceiverArrayElement,
						})
						cursor = closeBracket + 1
					case cursor < len(code) && code[cursor] == ')':
						openWrapper, wraps := parenthesisOpenByClose[cursor]
						if !wraps ||
							openWrapper >= expressionStart ||
							javaReturnedReceiverInvocationOpen(code, openWrapper) {
							break receiverOperations
						}
						castTypes, validWrapper := javaReturnedReceiverWrapperCasts(
							code,
							parenthesisCloseByOpen,
							openWrapper+1,
							expressionStart,
						)
						if !validWrapper {
							break receiverOperations
						}
						for castIndex := len(castTypes) - 1; castIndex >= 0; castIndex-- {
							operations = append(
								operations,
								javaReturnedReceiverOperation{
									kind:     javaReturnedReceiverCast,
									castType: castTypes[castIndex],
								},
							)
						}
						expressionStart = openWrapper
						cursor++
					default:
						break receiverOperations
					}
				}
				if call.limitExceeded {
					break
				}
				for cursor < len(code) && strings.ContainsRune(" \t\r\n", rune(code[cursor])) {
					cursor++
				}
			}
			if cursor >= len(code) || code[cursor] != '.' {
				break
			}
			if len(links) >= maxJavaReceiverChainLinks {
				call.limitExceeded = true
				break
			}
			cursor++
			for cursor < len(code) && strings.ContainsRune(" \t\r\n", rune(code[cursor])) {
				cursor++
			}
			nextStart := cursor
			for cursor < len(code) && isJavaIdentifierByte(code[cursor]) {
				cursor++
			}
			if cursor == nextStart {
				break
			}
			nextEnd := cursor
			for cursor < len(code) && strings.ContainsRune(" \t\r\n", rune(code[cursor])) {
				cursor++
			}
			link := javaReturnedReceiverLink{
				receiverOperations: operations,
			}
			memberName := strings.TrimSpace(code[nextStart:nextEnd])
			if cursor < len(code) && code[cursor] == '(' {
				nextArgs, ok := javaCallArguments(source, code, nextEnd)
				nextClose, matched := parenthesisCloseByOpen[cursor]
				if !ok || !matched {
					call.limitExceeded = true
					break
				}
				link.methodName = memberName
				link.args = nextArgs
				hasConsumerMethod = true
				cursor = nextClose + 1
			} else {
				link.fieldName = memberName
			}
			links = append(links, link)
		}
		if len(links) == 0 || (!hasConsumerMethod && !call.limitExceeded) {
			return
		}
		first := links[0]
		call.receiverOperations = first.receiverOperations
		call.methodName = first.methodName
		call.fieldName = first.fieldName
		call.args = first.args
		if len(links) > 1 {
			call.continuation = links[1:]
		}
		calls = append(calls, call)
	}
	for _, match := range javaUnqualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 4 {
			continue
		}
		methodName := code[match[2]:match[3]]
		if !javaUnqualifiedInvocationCandidate(code, match[0], methodName) {
			continue
		}
		appendCandidate(match[0], "", match[2], match[3])
	}
	for _, match := range javaQualifiedCallRe.FindAllStringSubmatchIndex(code, -1) {
		if len(match) < 6 {
			continue
		}
		appendCandidate(
			match[0],
			code[match[2]:match[3]],
			match[4],
			match[5],
		)
	}
	return calls
}

func javaReturnedReceiverKeyword(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "assert", "catch", "do", "for", "if", "new", "return",
		"super", "switch", "synchronized", "this", "throw", "try", "while", "yield":
		return true
	default:
		return false
	}
}

func javaDelimiterPairs(
	code string,
	openDelimiter byte,
	closeDelimiter byte,
) (map[int]int, map[int]int) {
	closeByOpen := make(map[int]int)
	openByClose := make(map[int]int)
	stack := make([]int, 0, 4)
	for offset := 0; offset < len(code); offset++ {
		switch code[offset] {
		case openDelimiter:
			stack = append(stack, offset)
		case closeDelimiter:
			if len(stack) == 0 {
				continue
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			closeByOpen[open] = offset
			openByClose[offset] = open
		}
	}
	return closeByOpen, openByClose
}

func javaReturnedReceiverInvocationOpen(
	code string,
	open int,
) bool {
	previous := open - 1
	for previous >= 0 && strings.ContainsRune(" \t\r\n", rune(code[previous])) {
		previous--
	}
	if previous < 0 {
		return false
	}
	switch code[previous] {
	case ')', ']':
		return true
	}
	if !isJavaIdentifierByte(code[previous]) {
		return false
	}
	tokenStart := previous
	for tokenStart > 0 && isJavaIdentifierByte(code[tokenStart-1]) {
		tokenStart--
	}
	switch strings.ToLower(strings.TrimSpace(code[tokenStart : previous+1])) {
	case "return", "throw", "yield":
		return false
	default:
		return true
	}
}

func javaReturnedReceiverWrapperCasts(
	code string,
	parenthesisCloseByOpen map[int]int,
	start int,
	end int,
) ([]string, bool) {
	cursor := start
	castTypes := make([]string, 0, 1)
	for cursor < end {
		for cursor < end && strings.ContainsRune(" \t\r\n", rune(code[cursor])) {
			cursor++
		}
		if cursor == end {
			return castTypes, true
		}
		if code[cursor] != '(' {
			return nil, false
		}
		close, ok := parenthesisCloseByOpen[cursor]
		if !ok || close >= end {
			return nil, false
		}
		castType := strings.TrimSpace(code[cursor+1 : close])
		if !javaReturnedReceiverCastTypeRe.MatchString(castType) {
			return nil, false
		}
		castTypes = append(castTypes, castType)
		cursor = close + 1
	}
	return castTypes, true
}

func matchingJavaBracket(code string, open int) int {
	if open < 0 || open >= len(code) || code[open] != '[' {
		return -1
	}
	depth := 0
	for offset := open; offset < len(code); offset++ {
		switch code[offset] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return offset
			}
		}
	}
	return -1
}

func javaInvokedFunctionalReachabilityTargets(
	current javaMethod,
	source string,
	owner javaClass,
	index *javaEvidenceIndex,
	variableTypes map[string]string,
) []javaResolvedHelperMethod {
	targets, _ := javaInvokedFunctionalReachability(
		current,
		source,
		owner,
		index,
		variableTypes,
	)
	return targets
}

func javaEvidenceMethodsInHierarchy(
	index *javaEvidenceIndex,
	class javaClass,
	caller javaClass,
	methodName string,
	allowStartPrivate bool,
) []javaResolvedHelperMethod {
	resolved := make([]javaResolvedHelperMethod, 0)
	shadowed := make(map[string]bool)
	seenClasses := make(map[string]bool)
	hierarchy := make([]javaClass, 0, 4)
	current := class
	for depth := 0; ; depth++ {
		classKey := javaEvidenceOwnerKey(current)
		if seenClasses[classKey] {
			break
		}
		seenClasses[classKey] = true
		hierarchy = append(hierarchy, current)
		for _, method := range javaMethodsNamed(current, methodName) {
			signature := javaEvidenceMethodSignature(method)
			if shadowed[signature] {
				continue
			}
			if !javaEvidenceMethodAccessible(
				method,
				current,
				caller,
				depth == 0 && allowStartPrivate,
			) {
				continue
			}
			shadowed[signature] = true
			resolved = append(resolved, javaResolvedHelperMethod{class: current, method: method})
		}
		superClass, ok := index.superclass(current)
		if !ok {
			break
		}
		current = superClass
	}
	for _, hierarchyClass := range hierarchy {
		interfaces := index.defaultMethodSuperinterfaces(hierarchyClass)
		for interfaceIndex := len(interfaces) - 1; interfaceIndex >= 0; interfaceIndex-- {
			interfaceClass := interfaces[interfaceIndex]
			for _, method := range javaMethodsNamed(interfaceClass, methodName) {
				if !method.Default || method.Abstract {
					continue
				}
				signature := javaEvidenceMethodSignature(method)
				if shadowed[signature] ||
					!javaEvidenceMethodAccessible(method, interfaceClass, caller, false) {
					continue
				}
				shadowed[signature] = true
				resolved = append(resolved, javaResolvedHelperMethod{
					class:  interfaceClass,
					method: method,
				})
			}
		}
	}
	return resolved
}

func javaEvidenceVirtualDispatchTargets(
	index *javaEvidenceIndex,
	receiverBase javaClass,
	methodOwner javaClass,
	method javaMethod,
) []javaResolvedHelperMethod {
	if method.Static ||
		strings.EqualFold(strings.TrimSpace(method.Visibility), "private") ||
		javaEvidenceMethodIsFinal(methodOwner, method) {
		return nil
	}
	var resolved []javaResolvedHelperMethod
	seen := make(map[string]bool)
	targetSignature := javaEvidenceOverrideSignature(index, methodOwner, method)
	targetPackagePrivate := strings.TrimSpace(method.Visibility) == ""
	for _, classIndex := range index.methodClasses[strings.ToLower(strings.TrimSpace(method.Name))] {
		candidate := index.program.Classes[classIndex]
		if javaSameEvidenceClass(candidate, methodOwner) ||
			!index.isSubtype(candidate, receiverBase) {
			continue
		}
		for _, override := range javaMethodsNamed(candidate, method.Name) {
			if override.Static ||
				override.Abstract ||
				strings.EqualFold(strings.TrimSpace(override.Visibility), "private") ||
				javaEvidenceOverrideSignature(index, candidate, override) != targetSignature {
				continue
			}
			if targetPackagePrivate &&
				!strings.EqualFold(
					strings.TrimSpace(candidate.Package),
					strings.TrimSpace(methodOwner.Package),
				) {
				continue
			}
			key := javaHelperVisitKey(candidate, override)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, javaResolvedHelperMethod{
				class:  candidate,
				method: override,
			})
		}
	}
	return resolved
}

func javaEvidenceInterfaceDispatchCandidates(
	index *javaEvidenceIndex,
	interfaceClass javaClass,
	caller javaClass,
	methodName string,
	args []string,
	variableTypes map[string]string,
) []javaResolvedHelperMethod {
	resolved := make([]javaResolvedHelperMethod, 0)
	seen := make(map[string]bool)
	for _, classIndex := range index.subtypeIndicesFor(interfaceClass) {
		subtype := index.program.Classes[classIndex]
		if strings.EqualFold(strings.TrimSpace(subtype.Kind), "interface") {
			continue
		}
		candidates := javaEvidenceMethodsInHierarchy(
			index,
			subtype,
			caller,
			methodName,
			false,
		)
		methods := make([]javaMethod, 0, len(candidates))
		owners := make(map[string]javaClass, len(candidates))
		for _, candidate := range candidates {
			methods = append(methods, candidate.method)
			owners[javaEvidenceMethodIdentity(candidate.method)] = candidate.class
		}
		for _, method := range javaHelperOverloadsForArguments(methods, args, variableTypes) {
			methodOwner, ok := owners[javaEvidenceMethodIdentity(method)]
			if !ok {
				continue
			}
			key := javaHelperVisitKey(methodOwner, method)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, javaResolvedHelperMethod{
				class:  methodOwner,
				method: method,
			})
		}
	}
	return resolved
}

func javaEvidenceOverrideSignature(
	index *javaEvidenceIndex,
	owner javaClass,
	method javaMethod,
) string {
	types := make([]string, 0, len(method.Parameters))
	for _, parameter := range method.Parameters {
		typeName := javaSimpleTypeName(parameter.Type)
		if resolved, ok := index.classForType(owner, typeName); ok {
			typeName = javaQualifiedClassName(resolved)
		}
		typeName = strings.ToLower(strings.Join(strings.Fields(typeName), ""))
		types = append(types, typeName)
	}
	return strings.ToLower(strings.TrimSpace(method.Name)) + "(" + strings.Join(types, ",") + ")"
}

func javaEvidenceMethodIsFinal(owner javaClass, method javaMethod) bool {
	if owner.BodyStartLine <= 0 || method.StartLine < owner.BodyStartLine {
		return false
	}
	lines := strings.Split(owner.Source, "\n")
	start := method.StartLine - owner.BodyStartLine
	if start < 0 || start >= len(lines) {
		return false
	}
	end := start + 8
	if end > len(lines) {
		end = len(lines)
	}
	declaration := javaCodeOnly(strings.Join(lines[start:end], "\n"))
	if open := strings.IndexByte(declaration, '{'); open >= 0 {
		declaration = declaration[:open]
	}
	return strings.Contains(declaration, method.Name) &&
		javaFinalMethodModifierRe.MatchString(declaration)
}

func javaEvidenceStaticImportedMethods(
	index *javaEvidenceIndex,
	owner javaClass,
	methodName string,
) []javaResolvedHelperMethod {
	if index == nil {
		return nil
	}
	cacheKey := javaEvidenceImportScopeKey(owner) + "\x00" + methodName
	if cached, ok := index.staticMethodCache[cacheKey]; ok {
		return append([]javaResolvedHelperMethod(nil), cached...)
	}
	var resolved []javaResolvedHelperMethod
	seen := make(map[string]bool)
	scope := index.importScope(owner)
	typeNames := append(
		[]string(nil),
		scope.staticExact[methodName]...,
	)
	typeNames = append(typeNames, scope.staticWildcard...)
	for _, typeName := range typeNames {
		if index.staticImportLookups >= maxJavaStaticImportOwnerLookups {
			index.staticImportLimitHit = true
			break
		}
		index.staticImportLookups++
		class, ok := index.classForType(owner, typeName)
		if !ok {
			continue
		}
		for _, candidate := range javaEvidenceMethodsInHierarchy(index, class, owner, methodName, false) {
			if !candidate.method.Static {
				continue
			}
			key := javaHelperVisitKey(candidate.class, candidate.method)
			if seen[key] {
				continue
			}
			seen[key] = true
			resolved = append(resolved, candidate)
		}
	}
	index.staticMethodCache[cacheKey] = append(
		[]javaResolvedHelperMethod(nil),
		resolved...,
	)
	return append([]javaResolvedHelperMethod(nil), resolved...)
}

func javaEvidenceMethodAccessible(
	method javaMethod,
	declaring javaClass,
	caller javaClass,
	allowPrivate bool,
) bool {
	switch strings.ToLower(strings.TrimSpace(method.Visibility)) {
	case "private":
		return allowPrivate
	case "public", "protected":
		return true
	default:
		return strings.EqualFold(strings.TrimSpace(declaring.Package), strings.TrimSpace(caller.Package))
	}
}

func javaEvidenceMethodSignature(method javaMethod) string {
	return strings.ToLower(strings.TrimSpace(method.Name)) + "(" +
		strings.ToLower(strings.Join(strings.Fields(method.ParameterSignature), " ")) + ")"
}

func javaEvidenceMethodIdentity(method javaMethod) string {
	return strings.ToLower(strings.TrimSpace(method.File)) + "\x00" +
		javaEvidenceMethodSignature(method) + "\x00" +
		strconv.Itoa(method.StartLine)
}

func javaSameEvidenceClass(left, right javaClass) bool {
	return javaEvidenceOwnerKey(left) == javaEvidenceOwnerKey(right)
}

func javaEvidenceImportScopeKey(owner javaClass) string {
	return strings.ToLower(strings.TrimSpace(owner.File)) + "\x00" +
		strings.ToLower(strings.TrimSpace(owner.Package))
}

func (index *javaEvidenceIndex) importScope(owner javaClass) *javaEvidenceImportScope {
	if index == nil {
		return &javaEvidenceImportScope{}
	}
	key := javaEvidenceImportScopeKey(owner)
	if cached, ok := index.importScopes[key]; ok {
		return cached
	}
	scope := &javaEvidenceImportScope{
		explicitTypes: make(map[string][]string),
		wildcardTypes: make(map[string][]javaClass),
		staticExact:   make(map[string][]string),
	}
	wildcardPackages := make(map[string]bool)
	for _, rawImport := range owner.Imports {
		imported := strings.TrimSpace(rawImport)
		if imported == "" {
			continue
		}
		if strings.HasPrefix(imported, "static ") {
			member := strings.TrimSpace(strings.TrimPrefix(imported, "static "))
			if strings.HasSuffix(member, ".*") {
				scope.staticWildcard = appendUniqueString(
					scope.staticWildcard,
					strings.TrimSuffix(member, ".*"),
				)
				continue
			}
			lastDot := strings.LastIndexByte(member, '.')
			if lastDot < 0 || lastDot+1 >= len(member) {
				continue
			}
			methodName := member[lastDot+1:]
			scope.staticExact[methodName] = appendUniqueString(
				scope.staticExact[methodName],
				member[:lastDot],
			)
			continue
		}
		if strings.HasSuffix(imported, ".*") {
			packageName := strings.TrimSuffix(imported, ".*")
			normalized := strings.ToLower(strings.TrimSpace(packageName))
			if !wildcardPackages[normalized] {
				wildcardPackages[normalized] = true
				scope.wildcardPackages = append(scope.wildcardPackages, packageName)
			}
			continue
		}
		simpleName := strings.ToLower(javaProtocolReceiverName(imported))
		scope.explicitTypes[simpleName] = appendUniqueString(
			scope.explicitTypes[simpleName],
			imported,
		)
	}
	if len(wildcardPackages) > 0 {
		for _, class := range index.program.Classes {
			if !wildcardPackages[strings.ToLower(strings.TrimSpace(class.Package))] {
				continue
			}
			simpleName := strings.ToLower(strings.TrimSpace(class.Name))
			scope.wildcardTypes[simpleName] = append(
				scope.wildcardTypes[simpleName],
				class,
			)
		}
	}
	index.importScopes[key] = scope
	return scope
}

func (index *javaEvidenceIndex) classForType(owner javaClass, typeName string) (javaClass, bool) {
	typeName = javaSimpleTypeName(typeName)
	if strings.Contains(typeName, ".") {
		return index.qualifiedClass(typeName)
	}

	scope := index.importScope(owner)
	explicit := scope.explicitTypes[strings.ToLower(strings.TrimSpace(typeName))]
	if len(explicit) > 0 {
		if len(explicit) != 1 {
			return javaClass{}, false
		}
		return index.qualifiedClass(explicit[0])
	}

	if matches := index.classesInPackage(owner.Package, typeName); len(matches) == 1 {
		return matches[0], true
	} else if len(matches) > 1 {
		return javaClass{}, false
	}

	simpleKey := strings.ToLower(strings.TrimSpace(typeName))
	wildcardMatches := scope.wildcardTypes[simpleKey]
	if len(wildcardMatches) == 1 {
		return wildcardMatches[0], true
	}
	return javaClass{}, false
}

func (index *javaEvidenceIndex) archivedNestedType(
	owner javaClass,
	typeName string,
) bool {
	typeName = javaSimpleTypeName(typeName)
	dot := strings.LastIndexByte(typeName, '.')
	if dot < 0 {
		nestedName := strings.ToLower(strings.TrimSpace(typeName))
		for _, enclosing := range index.classHierarchy(owner) {
			if index.nestedTypes[javaEvidenceOwnerKey(enclosing)][nestedName] {
				return true
			}
		}
		return false
	}
	if dot == 0 || dot >= len(typeName)-1 {
		return false
	}
	enclosingName := strings.TrimSpace(typeName[:dot])
	nestedName := strings.ToLower(strings.TrimSpace(typeName[dot+1:]))
	enclosing, ok := index.classForType(owner, enclosingName)
	if !ok {
		return false
	}
	return index.nestedTypes[javaEvidenceOwnerKey(enclosing)][nestedName]
}

// resolveMemberReceiver follows a field-only receiver path such as
// `container.helper` using archived declarations. Method-return hops are
// handled by javaReturnedReceiverCalls. archived is true once a path is proven
// to start from archived source; callers must fail closed when such a path
// cannot be completed exactly.
func (index *javaEvidenceIndex) resolveMemberReceiver(
	owner javaClass,
	qualifier string,
	variableTypes map[string]string,
) (resolved javaClass, archived bool, exact bool, limitExceeded bool) {
	qualifier = strings.TrimSpace(qualifier)
	if qualifier == "" {
		return javaClass{}, false, false, false
	}
	parts := strings.Split(qualifier, ".")
	if len(parts) > maxJavaReceiverChainLinks {
		return javaClass{}, true, false, true
	}

	position := 0
	current := javaClass{}
	lookupWork := 0
	switch parts[0] {
	case "this":
		current = owner
		archived = true
		position = 1
	case "super":
		superClass, ok := index.superclass(owner)
		if !ok {
			return javaClass{}, strings.TrimSpace(owner.SuperClass) != "", false, false
		}
		current = superClass
		archived = true
		position = 1
	default:
		typeName := variableTypes[parts[0]]
		if typeName == "" {
			typeName, _, limitExceeded = index.fieldTypeInHierarchy(
				owner,
				parts[0],
				&lookupWork,
			)
			if limitExceeded {
				return javaClass{}, true, false, true
			}
		}
		if typeName != "" {
			class, ok := index.classForType(owner, typeName)
			if !ok {
				return javaClass{}, index.programContainsSimpleType(typeName) ||
					index.archivedNestedType(owner, typeName), false, false
			}
			current = class
			archived = true
			position = 1
		} else {
			if class, ok := index.classForType(owner, qualifier); ok {
				return class, true, true, false
			}
			// A qualified archived type can itself contain dots. Choose the
			// longest resolvable prefix before interpreting the remainder as
			// field selections.
			for end := len(parts) - 1; end > 0; end-- {
				if class, ok := index.classForType(owner, strings.Join(parts[:end], ".")); ok {
					current = class
					archived = true
					position = end
					break
				}
			}
		}
	}
	if !archived {
		return javaClass{}, false, false, false
	}
	for ; position < len(parts); position++ {
		typeName, found, exceeded := index.fieldTypeInHierarchy(
			current,
			parts[position],
			&lookupWork,
		)
		if exceeded {
			return javaClass{}, true, false, true
		}
		if !found {
			return javaClass{}, true, false, false
		}
		next, ok := index.classForType(current, typeName)
		if !ok {
			return javaClass{}, true, false, false
		}
		current = next
	}
	return current, true, true, false
}

// memberReceiverHasValueRoot distinguishes an expression name from a qualified
// type name before resolving a dotted receiver. Java variables and fields
// shadow types in expression context, so callers must not let an archived
// package/type with the same spelling redirect reachability to a static call.
func (index *javaEvidenceIndex) memberReceiverHasValueRoot(
	owner javaClass,
	qualifier string,
	variableTypes map[string]string,
) (bool, bool) {
	parts := strings.Split(strings.TrimSpace(qualifier), ".")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return false, false
	}
	switch parts[0] {
	case "this", "super":
		return true, false
	}
	if strings.TrimSpace(variableTypes[parts[0]]) != "" {
		return true, false
	}
	work := 0
	_, found, exceeded := index.fieldTypeInHierarchy(owner, parts[0], &work)
	return found, exceeded
}

func (index *javaEvidenceIndex) fieldTypeInHierarchy(
	class javaClass,
	fieldName string,
	work *int,
) (string, bool, bool) {
	cacheKey := javaEvidenceOwnerKey(class) + "\x00" + fieldName
	if cached, ok := index.fieldLookupCache[cacheKey]; ok {
		if work == nil ||
			*work >= maxJavaMemberReceiverLookups ||
			index.totalMemberLookups >= maxJavaTotalMemberLookups {
			return "", false, true
		}
		(*work)++
		index.totalMemberLookups++
		return cached.typeName, cached.found, false
	}
	for {
		if work == nil ||
			*work >= maxJavaMemberReceiverLookups ||
			index.totalMemberLookups >= maxJavaTotalMemberLookups {
			return "", false, true
		}
		(*work)++
		index.totalMemberLookups++
		key := javaEvidenceOwnerKey(class)
		if typeName := index.directFieldTypes[key][fieldName]; typeName != "" {
			index.fieldLookupCache[cacheKey] = javaEvidenceFieldLookup{
				typeName: typeName,
				found:    true,
			}
			return typeName, true, false
		}
		superClass, ok := index.superclass(class)
		if !ok {
			index.fieldLookupCache[cacheKey] = javaEvidenceFieldLookup{}
			return "", false, false
		}
		class = superClass
	}
}

func javaProtocolTypeBindingEvidence(index *javaEvidenceIndex, owner javaClass) []string {
	cacheKey := javaEvidenceImportScopeKey(owner)
	if cached, ok := index.bindingCache[cacheKey]; ok {
		return append([]string(nil), cached...)
	}
	typeNames := []string{
		"web",
		"httpclient", "closeablehttpclient", "httpasyncclient", "closeablehttpasyncclient",
		"httpclients", "httpclientbuilder", "httpasyncclientbuilder",
		"url", "urlconnection", "httpurlconnection",
		"drivermanager", "datasource", "connection", "statement", "preparedstatement", "callablestatement",
		"jmscontext", "connectionfactory", "queueconnectionfactory", "topicconnectionfactory",
		"session", "queuesession", "topicsession",
		"jmsproducer", "jmsconsumer", "messageproducer", "messageconsumer", "queuebrowser", "message", "textmessage",
		"bytesmessage", "mapmessage", "objectmessage", "streammessage",
		"soapmessage", "soapconnection", "soapconnectionfactory", "messagefactory", "dispatch", "service",
	}
	evidence := make([]string, 0, len(typeNames))
	for _, typeName := range typeNames {
		kind, bound := javaProtocolTypeBindingForOwner(index, owner, typeName)
		if !bound {
			continue
		}
		evidence = append(evidence, "__apexload_protocol_type_"+typeName+"_"+javaProtocolReceiverKindLabel(kind)+"__;")
	}
	index.bindingCache[cacheKey] = append([]string(nil), evidence...)
	return evidence
}

func javaProtocolTypeBindingForOwner(
	index *javaEvidenceIndex,
	owner javaClass,
	simpleName string,
) (javaProtocolReceiverKind, bool) {
	if _, ok := index.classForType(owner, simpleName); ok {
		return javaProtocolReceiverUnknown, true
	}

	scope := index.importScope(owner)
	var explicit []javaProtocolReceiverKind
	simpleKey := strings.ToLower(strings.TrimSpace(simpleName))
	for _, rawImport := range scope.explicitTypes[simpleKey] {
		imported := strings.ToLower(strings.TrimSpace(rawImport))
		if kind, ok := javaKnownQualifiedProtocolKind(imported, simpleName); ok {
			explicit = appendUniqueJavaProtocolKind(explicit, kind)
		} else {
			explicit = appendUniqueJavaProtocolKind(explicit, javaProtocolReceiverUnknown)
		}
	}
	if len(explicit) == 1 {
		return explicit[0], true
	}
	if len(explicit) > 1 {
		return javaProtocolReceiverUnknown, true
	}

	var wildcard []javaProtocolReceiverKind
	for _, rawPackage := range scope.wildcardPackages {
		packageName := strings.ToLower(strings.TrimSpace(rawPackage))
		qualified := packageName + "." + simpleName
		if kind, ok := javaKnownQualifiedProtocolKind(qualified, simpleName); ok {
			wildcard = appendUniqueJavaProtocolKind(wildcard, kind)
		} else if javaClosedStandardProtocolPackage(packageName) {
			// The JDK/Jakarta API package is known not to contain this simple
			// protocol type. It therefore cannot make a different recognized
			// wildcard import ambiguous (for example java.net.* plus
			// javax.jms.* and TextMessage).
			continue
		} else {
			// Stage 1 permits proprietary dependencies on the governed runner
			// classpath. Without those bytecodes an external wildcard may bind
			// this simple name to a custom type, so fail closed instead of
			// guessing a standard protocol class.
			wildcard = appendUniqueJavaProtocolKind(wildcard, javaProtocolReceiverUnknown)
		}
	}
	if len(wildcard) == 1 {
		return wildcard[0], true
	}
	if len(wildcard) > 1 {
		return javaProtocolReceiverUnknown, true
	}
	// An unresolved simple name can refer to a same-package type supplied only
	// on the governed classpath. Program-backed evidence therefore never falls
	// through to the standalone snippet heuristics below.
	if simpleName == "web" {
		// VuGen exposes lrapi.web as a built-in source API even in archives that
		// omit the generated wildcard import.
		return javaProtocolReceiverLoadRunnerWeb, true
	}
	return javaProtocolReceiverUnknown, true
}

func javaClosedStandardProtocolPackage(packageName string) bool {
	switch strings.ToLower(strings.TrimSpace(packageName)) {
	case "java.net", "java.net.http", "java.sql", "javax.sql",
		"javax.jms", "jakarta.jms",
		"javax.xml.soap", "jakarta.xml.soap",
		"javax.xml.ws", "jakarta.xml.ws",
		"lrapi":
		return true
	default:
		return false
	}
}

func appendUniqueJavaProtocolKind(
	kinds []javaProtocolReceiverKind,
	kind javaProtocolReceiverKind,
) []javaProtocolReceiverKind {
	for _, existing := range kinds {
		if existing == kind {
			return kinds
		}
	}
	return append(kinds, kind)
}

func javaProtocolReceiverKindLabel(kind javaProtocolReceiverKind) string {
	switch kind {
	case javaProtocolReceiverHTTPClient:
		return "http"
	case javaProtocolReceiverHTTPBuilder:
		return "httpbuilder"
	case javaProtocolReceiverJDBCDataSource:
		return "jdbcsource"
	case javaProtocolReceiverJDBCConnection:
		return "jdbcconnection"
	case javaProtocolReceiverJDBCStatement:
		return "jdbcstatement"
	case javaProtocolReceiverJMS:
		return "jms"
	case javaProtocolReceiverSOAP:
		return "soap"
	case javaProtocolReceiverLoadRunnerWeb:
		return "web"
	case javaProtocolReceiverHTTPURL:
		return "httpurl"
	default:
		return "custom"
	}
}

func appendJavaStatementEvidence(parts *[]string, statements []javaStatement) {
	for _, statement := range statements {
		if statement.Kind != "block" {
			*parts = append(*parts, javaEvidenceCode(statement.Text))
			continue
		}
		*parts = append(*parts, javaProtocolBlockStart)
		if strings.TrimSpace(statement.Header) != "" {
			*parts = append(*parts, javaEvidenceCode(statement.Header))
			if arrow := strings.Index(statement.Header, "->"); arrow >= 0 {
				for _, parameter := range javaLambdaParameterSources(statement.Header[:arrow]) {
					fields := strings.Fields(parameter)
					if len(fields) == 1 {
						*parts = append(*parts, "Object "+fields[0]+";")
					} else if len(fields) > 1 {
						*parts = append(*parts, strings.TrimSpace(parameter)+";")
					}
				}
			}
		}
		appendJavaStatementEvidence(parts, statement.Children)
		*parts = append(*parts, javaProtocolBlockEnd)
	}
}

func javaEvidenceCode(source string) string {
	code := javaCodeOnly(source)
	masked := []byte(code)
	for _, marker := range []*regexp.Regexp{
		javaProtocolTypeBindingRe,
		javaProtocolReservedMarkerRe,
		javaProtocolReservedSuperFieldRe,
	} {
		for _, match := range marker.FindAllStringIndex(code, -1) {
			for index := match[0]; index < match[1]; index++ {
				if masked[index] != '\n' && masked[index] != '\r' {
					masked[index] = ' '
				}
			}
		}
	}
	return string(masked)
}

func inferJavaJMSProvider(text string) (string, string) {
	switch {
	case strings.Contains(text, "servicebus") || strings.Contains(text, "azure.servicebus"):
		return "servicebus", "jms-servicebus"
	case strings.Contains(text, "com.ibm.mq") || strings.Contains(text, "wmq") || strings.Contains(text, "ibm.mq"):
		return "ibmmq", "jms-ibmmq"
	default:
		return "solace", "jms-solace"
	}
}

func javaHasNonSolaceJMSProviderEvidence(text string) bool {
	return strings.Contains(text, "servicebus") ||
		strings.Contains(text, "azure.servicebus") ||
		strings.Contains(text, "com.ibm.mq") ||
		strings.Contains(text, "wmq") ||
		strings.Contains(text, "ibm.mq")
}

// inferJavaJDBCDriver maps a jdbc:<subprotocol>: URL found in the source to a
// supported DSL database driver (sqlite/postgres/mysql/oracle/odbc), defaulting
// to postgres when no recognizable JDBC URL is present.
func inferJavaJDBCDriver(text string) string {
	switch {
	case strings.Contains(text, "jdbc:postgresql"):
		return "postgres"
	case strings.Contains(text, "jdbc:mysql") || strings.Contains(text, "jdbc:mariadb"):
		return "mysql"
	case strings.Contains(text, "jdbc:oracle"):
		return "oracle"
	case strings.Contains(text, "jdbc:sqlite"):
		return "sqlite"
	case strings.Contains(text, "jdbc:sqlserver") || strings.Contains(text, "jdbc:jtds") || strings.Contains(text, "jdbc:odbc"):
		return "odbc"
	default:
		return "postgres"
	}
}

func inferJavaJMSDestinationType(text string) string {
	if strings.Contains(text, ".createtopic(") || strings.Contains(text, "topicpublisher") || strings.Contains(text, "topic ") || strings.Contains(text, "javax.jms.topic") {
		return "topic"
	}
	return "queue"
}

func inferJavaReviewFamilies(text string) []string {
	families := make([]string, 0)
	add := func(name string) {
		for _, existing := range families {
			if existing == name {
				return
			}
		}
		families = append(families, name)
	}
	if strings.Contains(text, "javax.ejb") || strings.Contains(text, "jakarta.ejb") {
		add("ejb_review")
	}
	if strings.Contains(text, "org.omg.corba") || strings.Contains(text, "corba.orb") {
		add("corba_review")
	}
	if strings.Contains(text, "naming.lookup") || strings.Contains(text, "initialcontext.lookup") || strings.Contains(text, "javax.rmi") {
		add("rmi_java_review")
	}
	if strings.Contains(text, "sapgui") || strings.Contains(text, "com.sap") {
		add("sapgui_java_review")
	}
	if strings.Contains(text, "jacada") {
		add("jacada_java_review")
	}
	if strings.Contains(text, "oracle.apps") || strings.Contains(text, "oracle.forms") || strings.Contains(text, "oracle.nca") {
		add("oracle_nca_java_review")
	}
	if strings.Contains(text, "tuxedo") || strings.Contains(text, "weblogic.wtc") {
		add("tuxedo_java_review")
	}
	sort.Strings(families)
	return families
}

func newJavaScriptForSurface(opts ConvertOptions, profile JavaProfile, surface javaSurface) *dsl.Script {
	name := convertutil.FirstNonEmpty(opts.ScriptName, "Converted_Java_Vuser")
	if len(profile.Surfaces) > 1 {
		name = name + "_" + string(surface)
	}
	script := &dsl.Script{
		Name:          sanitizeName(name),
		Version:       "1.0.0",
		GeneratedBy:   "apexload-enterprise-script-archive-java-importer",
		GeneratedFrom: "enterprise_script_archive",
		Actions:       make([]dsl.Action, 0),
	}
	switch surface {
	case javaSurfaceHTTP, javaSurfaceSOAP:
		script.Protocol = "http"
		script.Config.HTTP = dsl.HTTPConfig{}
	case javaSurfaceDatabase:
		script.Protocol = "database"
		script.Config.Database = dsl.DatabaseConfig{
			Driver:         convertutil.FirstNonEmpty(profile.DatabaseDriver, "postgres"),
			DSN:            "{{secret.database_dsn}}",
			QueryTimeoutMs: 30000,
			MaxRows:        100,
		}
	case javaSurfaceJMS:
		script.Protocol = profile.JMSProtocol
		script.Config.JMS = dsl.JMSConfig{
			Provider:        profile.JMSProvider,
			DestinationType: profile.DestinationType,
			TimeoutMs:       5000,
			// Imports default to mock: the standard build ships no real Solace/MQ
			// adapter, so a non-mock JMS script fails validation, and the import
			// fidelity promotion gate already blocks promotion until an operator
			// configures a real adapter/sidecar (so mock is not falsely green).
			Mock: true,
		}
	default:
		script.Protocol = "java_vuser"
	}
	return script
}

func sortedJavaSourceFiles(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		if strings.HasSuffix(strings.ToLower(filepath.Base(name)), ".java") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
