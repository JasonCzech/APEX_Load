package enterprisearchive

import (
	"fmt"
	"hash/fnv"
	filepath "path"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
)

type ImportMode string

const (
	ImportModeExecutable             ImportMode = "executable"
	ImportModeReviewOnly             ImportMode = "review_only"
	ImportModeExternalRunnerRequired ImportMode = "external_runner_required"
	ImportModeUnsupported            ImportMode = "unsupported"
)

type DetectionConfidence string

const (
	DetectionConfidenceHigh   DetectionConfidence = "high"
	DetectionConfidenceMedium DetectionConfidence = "medium"
	DetectionConfidenceLow    DetectionConfidence = "low"
)

type DetectedProtocol struct {
	CanonicalSourceProtocol string              `json:"canonical_source_protocol"`
	APEXLoadProtocol        string              `json:"apexload_protocol,omitempty"`
	Bundle                  string              `json:"bundle,omitempty"`
	ImportMode              ImportMode          `json:"import_mode"`
	Confidence              DetectionConfidence `json:"confidence"`
	DisplayName             string              `json:"display_name,omitempty"`
	SourceType              string              `json:"source_type,omitempty"`
	Signals                 []string            `json:"signals,omitempty"`
	Conflicts               []string            `json:"conflicts,omitempty"`
	Warnings                []string            `json:"warnings,omitempty"`
	RequiredEvidence        []string            `json:"required_evidence,omitempty"`
	RunnerRequirement       string              `json:"runner_requirement,omitempty"`
}

type ReviewArtifact struct {
	ID                 string           `json:"id"`
	Title              string           `json:"title"`
	Status             string           `json:"status"`
	Protocol           DetectedProtocol `json:"protocol"`
	Summary            string           `json:"summary"`
	SourceFiles        []string         `json:"source_files,omitempty"`
	PreservedFunctions []string         `json:"preserved_functions,omitempty"`
	RequiredEvidence   []string         `json:"required_evidence,omitempty"`
	NextActions        []string         `json:"next_actions,omitempty"`
	Metadata           map[string]any   `json:"metadata,omitempty"`
}

type protocolProfile struct {
	Canonical        string
	DisplayName      string
	APEXLoad         string
	Bundle           string
	Mode             ImportMode
	Evidence         []string
	Warnings         []string
	Runner           string
	ReviewSummary    string
	HarmlessWithSOAP bool
}

var protocolTypeAliases = map[string]protocolProfile{
	"web":                           webHTTPProfile(),
	"web http/html":                 webHTTPProfile(),
	"web - http/html":               webHTTPProfile(),
	"http/html":                     webHTTPProfile(),
	"web services":                  soapProfile(),
	"web services (soap)":           soapProfile(),
	"soap":                          soapProfile(),
	"soa":                           soapProfile(),
	"truclientmobileweb":            trueClientMobileProfile(),
	"truclient mobile web":          trueClientMobileProfile(),
	"truclient 2.0 - mobile web":    trueClientMobileProfile(),
	"truclient 2.0 mobile web":      trueClientMobileProfile(),
	"web2uimob":                     trueClientMobileProfile(),
	"tc4mobile":                     trueClientNativeMobileProfile(),
	"truclient native mobile":       trueClientNativeMobileProfile(),
	"truclient - native mobile":     trueClientNativeMobileProfile(),
	"truclient 2.0 native mobile":   trueClientNativeMobileProfile(),
	"truclient 2.0 - native mobile": trueClientNativeMobileProfile(),
	"truclient 2.0 - web":           trueClientWeb2Profile(),
	"truclient2 web":                trueClientWeb2Profile(),
	"truclient - web":               trueClientWebProfile(),
	"truclient web":                 trueClientWebProfile(),
	"truclientweb":                  trueClientWebProfile(),
	"truclient":                     externalProfile("truclient_legacy", "TruClient", "Foundation Entitlement", "Legacy desktop browser-profile archives require an external runtime and are preserved for review."),
	"database":                      databaseProfile(),
	"odbc":                          databaseProfile(),
	"sql":                           databaseProfile(),
	"dns":                           networkProfile("dns", "DNS"),
	"ftp":                           networkProfile("ftp", "FTP"),
	"smtp":                          networkProfile("smtp", "SMTP"),
	"pop3":                          networkProfile("pop3", "POP3"),
	"imap":                          networkProfile("imap", "IMAP"),
	"ldap":                          networkProfile("ldap", "LDAP"),
	"windows sockets":               networkProfile("winsock", "Windows Sockets"),
	"winsock":                       networkProfile("winsock", "Winsock"),
	"oracle-web":                    oebWebProfile("oracle_web", "Oracle-Web"),
	"oracle web":                    oebWebProfile("oracle_web", "Oracle-Web"),
	"siebel-web":                    oebWebProfile("siebel_web", "Siebel-Web"),
	"siebel web":                    oebWebProfile("siebel_web", "Siebel-Web"),
	"sap-web":                       sapWebProfile(),
	"sap web":                       sapWebProfile(),
	"oracle nca":                    externalProfile("oracle_nca", "Oracle NCA", "Oracle Enterprise Business Protocol Bundle", "Oracle NCA remains review-only until an approved client runner bridge is available."),
	"oracle 2-tier":                 externalProfile("oracle_2_tier", "Oracle 2-Tier", "Oracle Enterprise Business Protocol Bundle", "Oracle 2-Tier is a client-heavy protocol that requires an external runner."),
	"sap gui":                       externalProfile("sap_gui", "SAP GUI", "Foundation Entitlement", "SAP GUI requires an external SAP GUI runner bridge."),
	"sapgui":                        externalProfile("sap_gui", "SAP GUI", "Foundation Entitlement", "SAP GUI requires an external SAP GUI runner bridge."),
	"sap gui protocol":              externalProfile("sap_gui", "SAP GUI", "Foundation Entitlement", "SAP GUI requires an external SAP GUI runner bridge."),
	"rdp":                           externalProfile("rdp", "RDP", "Remote Access Protocol Bundle", "RDP replay requires an approved remote desktop runner bridge."),
	"remote desktop":                externalProfile("rdp", "RDP", "Remote Access Protocol Bundle", "RDP replay requires an approved remote desktop runner bridge."),
	"remote desktop protocol":       externalProfile("rdp", "RDP", "Remote Access Protocol Bundle", "RDP replay requires an approved remote desktop runner bridge."),
	"rte":                           externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE terminal replay requires an approved terminal runner bridge."),
	"te":                            externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE terminal replay requires an approved terminal runner bridge."),
	"terminal/rte":                  externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE terminal replay requires an approved terminal runner bridge."),
	"rte terminal":                  externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE terminal replay requires an approved terminal runner bridge."),
	"terminal emulator":             externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE terminal replay requires an approved terminal runner bridge."),
	"terminal emulation":            externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE terminal replay requires an approved terminal runner bridge."),
	"mapi":                          reviewProfile("mapi", "MAPI", "Network Protocol Bundle", "MAPI imports are classified for review until a runner bridge exists."),
	"tuxedo":                        reviewProfile("tuxedo", "Tuxedo", "Network Protocol Bundle", "Tuxedo imports are classified for review until a runner bridge exists."),
	"template":                      templateProfile("template_vuser", "Template VUser"),
	"c vuser":                       templateProfile("c_vuser", "C Vuser"),
	"c++ .net":                      templateProfile("cpp_dotnet_vuser", "C++ .NET Vuser"),
	"c# .net":                       templateProfile("csharp_dotnet_vuser", "C# .NET Vuser"),
	"java vuser":                    templateProfile("java_vuser", "Java Vuser"),
	"java protocols":                javaJMSSolaceProfile(),
	"java record replay":            javaHTTPProfile(),
	"java record/replay":            javaHTTPProfile(),
	"java over http":                javaHTTPProfile(),
	"vb .net":                       templateProfile("vb_dotnet_vuser", "VB .NET Vuser"),
	"devweb":                        reviewProfile("devweb", "DevWeb", "Templates Protocol Bundle", "Use the native DevWeb importer for executable conversion."),
	"jmeter":                        reviewProfile("jmeter", "JMeter", "Templates Protocol Bundle", "Import this archive with the JMeter importer, which reads the .jmx inside a ZIP or USZ directly."),
}

func DetectProtocol(files map[string]string, projectConfig *ProjectConfig, actions []*ParsedAction) DetectedProtocol {
	signals := make([]string, 0)
	var metadata *protocolProfile
	sourceType := ""
	if projectConfig != nil {
		sourceType = strings.TrimSpace(projectConfig.ScriptType)
		if profile, projectSignals, ok := profileForProjectConfig(projectConfig); ok {
			metadata = &profile
			signals = append(signals, projectSignals...)
		}
	}

	contentProfile, contentSignals := profileFromContent(files, actions, projectConfig)
	signals = append(signals, contentSignals...)

	selected := webHTTPProfile()
	confidence := DetectionConfidenceLow
	conflicts := make([]string, 0)
	if metadata != nil {
		selected = *metadata
		confidence = DetectionConfidenceMedium
		if contentProfile != nil {
			if compatibleProfiles(*metadata, *contentProfile) {
				confidence = DetectionConfidenceHigh
				selected.Warnings = append(selected.Warnings, contentProfile.Warnings...)
				if metadata.Canonical == "java_vuser" && isSpecializedJavaProfile(contentProfile.Canonical) {
					selected = *contentProfile
				}
				if metadata.Canonical == "web_http_html" && contentProfile.Canonical == "soap" {
					selected = *contentProfile
					selected.Warnings = append(selected.Warnings, "Web HTTP/HTML metadata contains SOAP-only request evidence; routing to SOAP converter.")
				}
			} else {
				conflicts = append(conflicts, fmt.Sprintf(".usr Type=%s conflicts with content evidence for %s", sourceType, contentProfile.DisplayName))
				confidence = DetectionConfidenceLow
			}
		}
	} else if contentProfile != nil {
		selected = *contentProfile
		confidence = DetectionConfidenceMedium
		signals = append(signals, "protocol inferred from script content")
	}

	detected := DetectedProtocol{
		CanonicalSourceProtocol: selected.Canonical,
		APEXLoadProtocol:        selected.APEXLoad,
		Bundle:                  selected.Bundle,
		ImportMode:              selected.Mode,
		Confidence:              confidence,
		DisplayName:             selected.DisplayName,
		SourceType:              sourceType,
		Signals:                 uniqueSortedStrings(signals),
		Conflicts:               uniqueSortedStrings(conflicts),
		Warnings:                append([]string(nil), selected.Warnings...),
		RequiredEvidence:        append([]string(nil), selected.Evidence...),
		RunnerRequirement:       selected.Runner,
	}

	if len(conflicts) > 0 && !isHarmlessSOAPConflict(selected, contentProfile) {
		detected.ImportMode = ImportModeReviewOnly
		detected.APEXLoadProtocol = ""
		detected.Warnings = append(detected.Warnings, "Metadata and content disagree; import is gated as review-only until an operator confirms the correct protocol.")
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Operator-confirmed source protocol profile", "Replay or runtime evidence for the selected protocol")
	}
	if detected.CanonicalSourceProtocol == "" {
		detected.CanonicalSourceProtocol = "unknown"
		detected.DisplayName = "Unknown Source Protocol"
		detected.ImportMode = ImportModeUnsupported
		detected.RequiredEvidence = appendUnique(detected.RequiredEvidence, "Project metadata file or protocol-specific source files")
	}
	detected.Warnings = uniqueSortedStrings(detected.Warnings)
	detected.RequiredEvidence = uniqueSortedStrings(detected.RequiredEvidence)
	return detected
}

func profileForType(scriptType string) (protocolProfile, bool) {
	key := normalizeProtocolKey(scriptType)
	if profile, ok := protocolTypeAliases[key]; ok {
		return profile, true
	}
	switch {
	case strings.Contains(key, "oracle") && strings.Contains(key, "web"):
		return oebWebProfile("oracle_web", "Oracle-Web"), true
	case strings.Contains(key, "siebel") && strings.Contains(key, "web"):
		return oebWebProfile("siebel_web", "Siebel-Web"), true
	case strings.Contains(key, "sap") && strings.Contains(key, "web"):
		return sapWebProfile(), true
	case strings.Contains(key, "sap") && strings.Contains(key, "gui"):
		return externalProfile("sap_gui", "SAP GUI", "Foundation Entitlement", "SAP GUI requires an external SAP GUI runner bridge."), true
	case strings.Contains(key, "java") && (strings.Contains(key, "protocol") || strings.Contains(key, "jms") || strings.Contains(key, "solace")):
		return javaJMSSolaceProfile(), true
	case strings.Contains(key, "java") && strings.Contains(key, "http"):
		return javaHTTPProfile(), true
	case strings.Contains(key, "java") && (strings.Contains(key, "jdbc") || strings.Contains(key, "sql") || strings.Contains(key, "database")):
		return javaJDBCProfile(), true
	case strings.Contains(key, "java") && strings.Contains(key, "soap"):
		return javaSOAPProfile(), true
	case strings.Contains(key, "odbc") || strings.Contains(key, "database"):
		return databaseProfile(), true
	case strings.Contains(key, "web service") || strings.Contains(key, "soap"):
		return soapProfile(), true
	case strings.Contains(key, "java"):
		return templateProfile("java_vuser", "Java Vuser"), true
	case strings.Contains(key, "web"):
		return webHTTPProfile(), true
	default:
		return protocolProfile{}, false
	}
}

func profileForProjectConfig(cfg *ProjectConfig) (protocolProfile, []string, bool) {
	if cfg == nil {
		return protocolProfile{}, nil, false
	}
	signals := make([]string, 0, 1+len(cfg.ActiveTypes))
	sourceType := strings.TrimSpace(cfg.ScriptType)
	if sourceType != "" {
		signals = append(signals, fmt.Sprintf(".usr [General] Type=%s", sourceType))
	}
	for _, activeType := range cfg.ActiveTypes {
		if strings.TrimSpace(activeType) == "" {
			continue
		}
		signals = append(signals, fmt.Sprintf(".usr [General] ActiveTypes=%s", activeType))
	}
	if profile, ok := profileForType(sourceType); ok {
		return profile, signals, true
	}
	for _, activeType := range cfg.ActiveTypes {
		if profile, ok := profileForType(activeType); ok {
			return profile, signals, true
		}
	}
	if javaSignals, ok := javaProjectConfigSignals(cfg); ok {
		return templateProfile("java_vuser", "Java Vuser"), append(signals, javaSignals...), true
	}
	return protocolProfile{}, signals, false
}

// javaProjectConfigSignals recognizes Java VUser projects from .usr [General]
// settings beyond Type/ActiveTypes — VuGen writes ScriptLanguage=JAVA and
// RunType=Java-Interpreter for every Java-family protocol.
func javaProjectConfigSignals(cfg *ProjectConfig) ([]string, bool) {
	signals := make([]string, 0, 2)
	if scriptLanguageIsJava(cfg.ScriptLanguage) {
		signals = append(signals, fmt.Sprintf(".usr [General] ScriptLanguage=%s", strings.TrimSpace(cfg.ScriptLanguage)))
	}
	if scriptLanguageIsJava(cfg.RunType) {
		signals = append(signals, fmt.Sprintf(".usr [General] RunType=%s", strings.TrimSpace(cfg.RunType)))
	}
	return signals, len(signals) > 0
}

func profileFromContent(files map[string]string, actions []*ParsedAction, projectConfig *ProjectConfig) (*protocolProfile, []string) {
	functions := flattenParsedFunctions(actions)
	signals := make([]string, 0)
	if len(functions) > 0 {
		counts := map[string]int{}
		for _, fn := range functions {
			lower := strings.ToLower(strings.TrimSpace(fn.Name))
			counts[lower]++
		}
		if containsFunctionPrefix(counts, "sapgui_") {
			profile := externalProfile("sap_gui", "SAP GUI", "Foundation Entitlement", "SAP GUI function calls require an external SAP GUI runner bridge.")
			return &profile, []string{"sapgui_* function calls"}
		}
		if containsFunctionPrefix(counts, "rdp_") {
			profile := externalProfile("rdp", "RDP", "Remote Access Protocol Bundle", "rdp_* function calls require a remote desktop runner bridge.")
			return &profile, []string{"rdp_* function calls"}
		}
		if containsFunctionPrefix(counts, "te_") {
			profile := externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "TE_* function calls require a terminal runner bridge.")
			return &profile, []string{"TE_* function calls"}
		}
		if containsFunctionPrefix(counts, "nca_") {
			profile := externalProfile("oracle_nca", "Oracle NCA", "Oracle Enterprise Business Protocol Bundle", "nca_* function calls require a client runner bridge.")
			return &profile, []string{"nca_* function calls"}
		}
		if containsFunctionPrefix(counts, "lrd_") || containsFunctionPrefix(counts, "lr_db") {
			profile := databaseProfile()
			return &profile, []string{"database function calls"}
		}
		if subprotocol, ok := networkSubprotocolFromFunctions(counts); ok {
			profile := networkProfile(subprotocol, strings.ToUpper(subprotocol))
			return &profile, []string{fmt.Sprintf("%s network function calls", subprotocol)}
		}
		if requestCount(counts) > 0 {
			protocolName, _, warning := detectImportProtocol(functions)
			if protocolName == "soap" {
				profile := soapProfile()
				if warning != "" {
					profile.Warnings = append(profile.Warnings, warning)
				}
				return &profile, []string{"SOAP request evidence"}
			}
			profile := webHTTPProfile()
			if warning != "" {
				profile.Warnings = append(profile.Warnings, warning)
			}
			return &profile, []string{"web_* request function calls"}
		}
	}

	program, _ := parseJavaProgram(files)
	program = javaProgramWithProjectVuserClass(program, projectConfig)
	archiveText := strings.ToLower(protocolEvidenceArchiveText(files, program))
	if javaSignals, ok := javaVuserContentSignals(files, program); ok {
		executable := javaExecutableEvidenceText(program)
		profile := templateProfile("java_vuser", "Java Vuser")
		if javaLooksLikeJMS(executable) {
			profile = javaJMSSolaceProfile()
		} else if javaLooksLikeHTTP(executable) {
			profile = javaHTTPProfile()
		} else if javaLooksLikeDatabase(executable) {
			profile = javaJDBCProfile()
		} else if javaLooksLikeSOAP(executable) {
			profile = javaSOAPProfile()
		}
		return &profile, javaSignals
	}

	switch {
	case strings.Contains(archiveText, "sapgui_"):
		profile := externalProfile("sap_gui", "SAP GUI", "Foundation Entitlement", "SAP GUI source evidence requires an external runner bridge.")
		return &profile, []string{"sapgui_* source evidence"}
	case strings.Contains(archiveText, "web2uimob") || strings.Contains(archiveText, "truclientmobileweb"):
		profile := trueClientMobileProfile()
		return &profile, []string{"TruClient Mobile Web source evidence"}
	case strings.Contains(archiveText, "tc4mobile"):
		profile := trueClientNativeMobileProfile()
		return &profile, []string{"TruClient Native Mobile source evidence"}
	case strings.Contains(archiveText, "truclientweb"):
		profile := trueClientWebProfile()
		return &profile, []string{"TruClient Web source evidence"}
	case strings.Contains(archiveText, "rdp_"):
		profile := externalProfile("rdp", "RDP", "Remote Access Protocol Bundle", "RDP source evidence requires an external remote desktop runner bridge.")
		return &profile, []string{"rdp_* source evidence"}
	case strings.Contains(archiveText, "te_connect") || strings.Contains(archiveText, "te_wait_") || strings.Contains(archiveText, "te_type"):
		profile := externalProfile("rte", "RTE", "Terminal/RTE Protocol Bundle", "RTE source evidence requires an external terminal runner bridge.")
		return &profile, []string{"TE_* source evidence"}
	case strings.Contains(archiveText, "nca_") || strings.Contains(archiveText, "frmservlet"):
		profile := externalProfile("oracle_nca", "Oracle NCA", "Oracle Enterprise Business Protocol Bundle", "Oracle Forms/NCA source evidence")
		return &profile, []string{"Oracle Forms/NCA source evidence"}
	case strings.Contains(archiveText, "swecmd") || strings.Contains(archiveText, "sweextsource") || strings.Contains(archiveText, "siebel"):
		profile := oebWebProfile("siebel_web", "Siebel-Web")
		return &profile, []string{"Siebel Web source evidence"}
	case strings.Contains(archiveText, "icx_session") || strings.Contains(archiveText, "oa_html") || strings.Contains(archiveText, "oafunc"):
		profile := oebWebProfile("oracle_web", "Oracle-Web")
		return &profile, []string{"Oracle E-Business web source evidence"}
	case looksLikeSQLText(archiveText):
		profile := databaseProfile()
		return &profile, []string{"SQL text evidence"}
	case strings.Contains(archiveText, "soapaction") || strings.Contains(archiveText, "<soap") || strings.Contains(archiveText, ":envelope"):
		profile := soapProfile()
		return &profile, []string{"SOAP envelope evidence"}
	case strings.Contains(archiveText, "com.solacesystems.jms") ||
		javaArchiveLooksLikeJMS(archiveText):
		profile := javaJMSSolaceProfile()
		return &profile, []string{"Java JMS/Solace source evidence"}
	}
	return nil, signals
}

// javaVuserContentSignals recognizes Java VUser archives from their source
// files: VuGen Java protocols ship vuser_init.java/Actions.java/vuser_end.java
// importing lrapi.lr instead of .c action files. Native Java API families are
// reported as extra signals so detection can name the executable target before
// ConvertJava performs the final per-statement disposition.
func javaVuserContentSignals(files map[string]string, program javaProgram) ([]string, bool) {
	signals := make([]string, 0, 2)
	executable := javaExecutableEvidenceText(program)
	jms := javaLooksLikeJMS(executable)
	http := javaLooksLikeHTTP(executable)
	database := javaLooksLikeDatabase(executable)
	soap := javaLooksLikeSOAP(executable)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.HasSuffix(strings.ToLower(name), ".java") {
			continue
		}
		content := strings.ToLower(javaCodeOnly(files[name]))
		base := strings.ToLower(filepath.Base(name))
		switch {
		case strings.Contains(content, "lrapi"),
			base == "actions.java", base == "vuser_init.java", base == "vuser_end.java":
			signals = append(signals, fmt.Sprintf("Java Vuser source file %s", filepath.Base(name)))
		default:
			continue
		}
	}
	if len(signals) == 0 {
		return nil, false
	}
	if jms {
		signals = append(signals, "javax.jms API usage in Java Vuser source")
	}
	if http {
		signals = append(signals, "Java HTTP API usage in Java Vuser source")
	}
	if database {
		signals = append(signals, "java.sql API usage in Java Vuser source")
	}
	if soap {
		signals = append(signals, "Java SOAP API usage in Java Vuser source")
	}
	return signals, true
}

func NewReviewArtifact(detected DetectedProtocol, files map[string]string, actions []*ParsedAction, metadata map[string]any) *ReviewArtifact {
	functions := flattenParsedFunctions(actions)
	functionNames := make([]string, 0, len(functions))
	for _, fn := range functions {
		functionNames = append(functionNames, fn.Name)
	}
	sourceFiles := make([]string, 0, len(files))
	for name := range files {
		sourceFiles = append(sourceFiles, filepath.Base(name))
	}
	sort.Strings(sourceFiles)
	title := convertutil.FirstNonEmpty(detected.DisplayName, detected.CanonicalSourceProtocol, "Enterprise script archive import") + " review artifact"
	status := string(detected.ImportMode)
	if status == "" {
		status = string(ImportModeReviewOnly)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["signals"] = append([]string(nil), detected.Signals...)
	if len(detected.Conflicts) > 0 {
		metadata["conflicts"] = append([]string(nil), detected.Conflicts...)
	}
	return &ReviewArtifact{
		ID:                 stableReviewArtifactID(detected, sourceFiles, functionNames),
		Title:              title,
		Status:             status,
		Protocol:           detected,
		Summary:            reviewArtifactSummary(detected),
		SourceFiles:        sourceFiles,
		PreservedFunctions: uniqueSortedStrings(functionNames),
		RequiredEvidence:   append([]string(nil), detected.RequiredEvidence...),
		NextActions:        reviewArtifactNextActions(detected),
		Metadata:           metadata,
	}
}

func reviewArtifactSummary(detected DetectedProtocol) string {
	if detected.ImportMode == ImportModeExecutable {
		return "Executable conversion is available, but source evidence was preserved for protocol-aware review."
	}
	if detected.RunnerRequirement != "" {
		return detected.RunnerRequirement
	}
	switch detected.ImportMode {
	case ImportModeExternalRunnerRequired:
		return "This protocol requires an external runner before APEX Load can execute it natively."
	case ImportModeUnsupported:
		return "APEX Load can classify this enterprise script archive, but no executable converter is available."
	default:
		return "APEX Load preserved this archive as a review-only import artifact."
	}
}

func reviewArtifactNextActions(detected DetectedProtocol) []string {
	actions := []string{"Review detected protocol, source files, and preserved functions."}
	switch detected.ImportMode {
	case ImportModeExternalRunnerRequired:
		actions = append(actions, "Attach external runner approval or bridge evidence before promotion.")
	case ImportModeUnsupported:
		actions = append(actions, "Confirm whether this protocol should be deferred or mapped to a supported APEX Load runtime.")
	case ImportModeReviewOnly:
		actions = append(actions, "Resolve protocol conflicts or required evidence before saving as an executable script.")
	}
	if len(detected.RequiredEvidence) > 0 {
		actions = append(actions, "Collect required evidence: "+strings.Join(detected.RequiredEvidence, "; "))
	}
	if detected.CanonicalSourceProtocol == "java_vuser" {
		for _, signal := range detected.Signals {
			if strings.Contains(strings.ToLower(signal), "javax.jms") {
				actions = append(actions, "Java source uses JMS APIs; verify the governed Java runner classpath, JMS client dependencies, and broker connectivity before promotion. The JMS calls remain inside the combined Java Vuser.")
				break
			}
		}
	}
	return actions
}

func stableReviewArtifactID(detected DetectedProtocol, files []string, functions []string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(detected.CanonicalSourceProtocol))
	_, _ = h.Write([]byte(strings.Join(files, "|")))
	_, _ = h.Write([]byte(strings.Join(functions, "|")))
	return fmt.Sprintf("lr-review-%08x", h.Sum32())
}

func compatibleProfiles(a, b protocolProfile) bool {
	if a.Canonical == b.Canonical {
		return true
	}
	if isJavaCompatibleProfile(a.Canonical) && isJavaCompatibleProfile(b.Canonical) {
		return true
	}
	if a.Canonical == "web_http_html" && b.Canonical == "soap" {
		return true
	}
	if a.Canonical == "soap" && b.Canonical == "web_http_html" {
		return true
	}
	if (a.Canonical == "oracle_web" || a.Canonical == "siebel_web" || a.Canonical == "sap_web") && b.Canonical == "web_http_html" {
		return true
	}
	if a.APEXLoad != "" && a.APEXLoad == b.APEXLoad && a.Mode == b.Mode {
		return true
	}
	return false
}

func javaArchiveLooksLikeJMS(archiveText string) bool {
	return strings.Contains(archiveText, "javax.jms") ||
		strings.Contains(archiveText, "jakarta.jms") ||
		strings.Contains(archiveText, "com.solacesystems.jms") ||
		(strings.Contains(archiveText, "initialcontext") && strings.Contains(archiveText, "connectionfactory") && strings.Contains(archiveText, "textmessage"))
}

func isJavaCompatibleProfile(canonical string) bool {
	switch canonical {
	case "java_vuser", "java_jms_solace", "java_over_http", "java_jdbc", "java_soap":
		return true
	default:
		return false
	}
}

func isSpecializedJavaProfile(canonical string) bool {
	return canonical == "java_jms_solace" || canonical == "java_over_http" || canonical == "java_jdbc" || canonical == "java_soap"
}

func isHarmlessSOAPConflict(selected protocolProfile, contentProfile *protocolProfile) bool {
	return contentProfile != nil && selected.Canonical == "web_http_html" && contentProfile.Canonical == "soap"
}

func flattenParsedFunctions(actions []*ParsedAction) []ParsedFunction {
	functions := make([]ParsedFunction, 0)
	for _, action := range actions {
		if action == nil {
			continue
		}
		functions = append(functions, action.Functions...)
	}
	return functions
}

func requestCount(counts map[string]int) int {
	total := 0
	for name, count := range counts {
		if isRequestFunction(name) {
			total += count
		}
	}
	return total
}

func containsFunctionPrefix(counts map[string]int, prefix string) bool {
	for name := range counts {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func networkSubprotocolFromFunctions(counts map[string]int) (string, bool) {
	prefixes := []struct {
		Prefix      string
		Subprotocol string
	}{
		{"dns_", "dns"},
		{"ftp_", "ftp"},
		{"smtp_", "smtp"},
		{"pop3_", "pop3"},
		{"imap_", "imap"},
		{"ldap_", "ldap"},
		{"lrs_", "winsock"},
		{"winsock_", "winsock"},
	}
	for _, candidate := range prefixes {
		if containsFunctionPrefix(counts, candidate.Prefix) {
			return candidate.Subprotocol, true
		}
	}
	return "", false
}

func joinArchiveText(files map[string]string) string {
	var b strings.Builder
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		b.WriteString("\n")
		b.WriteString(name)
		b.WriteString("\n")
		b.WriteString(files[name])
	}
	return b.String()
}

// protocolEvidenceArchiveText keeps the existing raw evidence behavior for
// non-Java artifacts, but replaces Java files with code reachable from the
// selected VUser lifecycle. This prevents imports, comments, literal payloads,
// and uncalled bundled classes from choosing a protocol in the generic content
// fallback when Java metadata or conventional filenames are absent.
func protocolEvidenceArchiveText(files map[string]string, program javaProgram) string {
	var b strings.Builder
	names := sortedFileNames(files)
	for _, name := range names {
		if strings.HasSuffix(strings.ToLower(filepath.Base(name)), ".java") {
			continue
		}
		b.WriteString("\n")
		b.WriteString(name)
		b.WriteString("\n")
		b.WriteString(files[name])
	}
	if executable := javaExecutableEvidenceText(program); strings.TrimSpace(executable) != "" {
		b.WriteString("\n")
		b.WriteString(executable)
	}
	return b.String()
}

func looksLikeSQLText(text string) bool {
	lower := strings.ToLower(text)
	for _, token := range []string{"select ", "insert into", "update ", "delete from", "create table", "drop table", "merge into", "truncate ", "exec ", "call ", "with "} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func normalizeProtocolKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func appendUnique(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	out := make([]string, 0, len(values)+len(additions))
	for _, value := range append(values, additions...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func uniqueSortedStrings(values []string) []string {
	out := uniqueStrings(values)
	sort.Strings(out)
	return out
}

func webHTTPProfile() protocolProfile {
	return protocolProfile{
		Canonical:        "web_http_html",
		DisplayName:      "Web HTTP/HTML",
		APEXLoad:         "http",
		Bundle:           "Foundation Entitlement",
		Mode:             ImportModeExecutable,
		HarmlessWithSOAP: true,
	}
}

func soapProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "soap",
		DisplayName: "Web Services/SOAP",
		APEXLoad:    "soap",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeExecutable,
	}
}

func trueClientMobileProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "truclient_mobile_web",
		DisplayName: "TruClient Mobile Web",
		APEXLoad:    "browser",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeExecutable,
		Evidence:    []string{"Browser replay evidence before promotion"},
	}
}

func trueClientWeb2Profile() protocolProfile {
	return protocolProfile{
		Canonical:   "truclient2_web",
		DisplayName: "TruClient 2.0 Web",
		APEXLoad:    "browser",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeExecutable,
		Evidence:    []string{"Selector review", "Browser replay evidence", "Runtime settings parity review", "Promotion review artifact"},
	}
}

func trueClientWebProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "truclient_web",
		DisplayName: "TruClient - Web",
		APEXLoad:    "browser",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeReviewOnly,
		Evidence:    []string{"Fixture-backed parser coverage", "Runtime settings parity review", "Browser replay evidence"},
		Warnings:    []string{"Legacy TruClient Web imports are preserved as review artifacts until the Web profile is promoted."},
	}
}

func trueClientNativeMobileProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "truclient_native_mobile",
		DisplayName: "TruClient Native Mobile",
		APEXLoad:    "",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeExternalRunnerRequired,
		Evidence:    []string{"Approved native mobile runner bridge", "Device or simulator replay evidence"},
		Runner:      "TruClient Native Mobile requires a governed mobile/device runner bridge and is preserved as a review artifact.",
	}
}

func databaseProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "database",
		DisplayName: "Database/ODBC",
		APEXLoad:    "database",
		Bundle:      "Enterprise Database Protocol Bundle",
		Mode:        ImportModeExecutable,
		Evidence:    []string{"DSN or connection string", "SQL bind data review"},
	}
}

func networkProfile(canonical, display string) protocolProfile {
	if display == "" {
		display = strings.ToUpper(canonical)
	}
	return protocolProfile{
		Canonical:   canonical,
		DisplayName: display,
		APEXLoad:    "network",
		Bundle:      "Enterprise Network Protocol Bundle",
		Mode:        ImportModeExecutable,
		Evidence:    []string{"Endpoint and credential review"},
	}
}

func oebWebProfile(canonical, display string) protocolProfile {
	return protocolProfile{
		Canonical:   canonical,
		DisplayName: display,
		APEXLoad:    "http",
		Bundle:      "Oracle Enterprise Business Protocol Bundle",
		Mode:        ImportModeExecutable,
		Evidence:    []string{"Oracle E-Business correlation review", "Replay validation evidence"},
	}
}

func sapWebProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "sap_web",
		DisplayName: "SAP Web",
		APEXLoad:    "http",
		Bundle:      "SAP Web Protocol Bundle",
		Mode:        ImportModeExecutable,
		Evidence:    []string{"SAP Web async/correlation review", "Replay validation evidence"},
		Warnings:    []string{"SAP Web imports execute through the HTTP runtime; SAP-specific async helpers remain review-gated."},
	}
}

func javaJMSSolaceProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "java_jms_solace",
		DisplayName: "Java JMS/Solace",
		APEXLoad:    "jms-solace",
		Bundle:      "Enterprise Messaging Protocol Bundle",
		Mode:        ImportModeExecutable,
		Evidence: []string{
			"Approved Solace JMS adapter or governed sidecar runner policy",
			"Runtime Solace credentials and truststore",
			"Replay validation evidence against the target VPN/destination",
		},
		Warnings: []string{"Java JMS/Solace imports are generated as JMS DSL but promotion is blocked until a real Solace adapter or governed sidecar runner is configured."},
	}
}

func javaHTTPProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "java_over_http",
		DisplayName: "Java Vuser HTTP",
		APEXLoad:    "http",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeExecutable,
		Evidence: []string{
			"Static Java HTTP idiom recognition",
			"HTTP replay validation evidence",
			"Correlation and ordered header review",
		},
	}
}

func javaJDBCProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "java_jdbc",
		DisplayName: "Java Vuser JDBC",
		APEXLoad:    "database",
		Bundle:      "Enterprise Database Protocol Bundle",
		Mode:        ImportModeExecutable,
		Evidence: []string{
			"Database DSN secret",
			"SQL bind data review",
			"Replay validation evidence against the target database",
		},
	}
}

func javaSOAPProfile() protocolProfile {
	return protocolProfile{
		Canonical:   "java_soap",
		DisplayName: "Java Vuser SOAP",
		APEXLoad:    "soap",
		Bundle:      "Foundation Entitlement",
		Mode:        ImportModeExecutable,
		Evidence: []string{
			"Recoverable SOAP endpoint and envelope",
			"SOAPAction and namespace review",
			"Replay validation evidence",
		},
	}
}

func reviewProfile(canonical, display, bundle, summary string) protocolProfile {
	return protocolProfile{
		Canonical:     canonical,
		DisplayName:   display,
		Bundle:        bundle,
		Mode:          ImportModeReviewOnly,
		Evidence:      []string{"Operator protocol review", "Runner feasibility decision"},
		ReviewSummary: summary,
		Runner:        summary,
	}
}

func externalProfile(canonical, display, bundle, summary string) protocolProfile {
	return protocolProfile{
		Canonical:   canonical,
		DisplayName: display,
		Bundle:      bundle,
		Mode:        ImportModeExternalRunnerRequired,
		Evidence:    []string{"External runner bridge approval", "Replay evidence from source runner"},
		Runner:      summary,
	}
}

func templateProfile(canonical, display string) protocolProfile {
	return protocolProfile{
		Canonical:   canonical,
		DisplayName: display,
		Bundle:      "Enterprise Templates Protocol Bundle",
		Mode:        ImportModeReviewOnly,
		Evidence:    []string{"Template implementation review", "Mapping to supported APEX Load DSL actions"},
		Runner:      "Template VUser imports require manual mapping to executable APEX Load actions.",
	}
}
