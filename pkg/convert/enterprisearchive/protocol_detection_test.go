package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestDetectProtocolFromUSRType(t *testing.T) {
	tests := []struct {
		name       string
		scriptType string
		canonical  string
		storm      string
		mode       ImportMode
	}{
		{name: "web", scriptType: "Web HTTP/HTML", canonical: "web_http_html", storm: "http", mode: ImportModeExecutable},
		{name: "soap", scriptType: "Web Services", canonical: "soap", storm: "soap", mode: ImportModeExecutable},
		{name: "mobile", scriptType: "TruClientMobileWeb", canonical: "truclient_mobile_web", storm: "browser", mode: ImportModeExecutable},
		{name: "mobile web2 ui", scriptType: "Web2UIMob", canonical: "truclient_mobile_web", storm: "browser", mode: ImportModeExecutable},
		{name: "web2", scriptType: "TruClient 2.0 - Web", canonical: "truclient2_web", storm: "browser", mode: ImportModeExecutable},
		{name: "truclient web", scriptType: "TruClientWeb", canonical: "truclient_web", storm: "browser", mode: ImportModeReviewOnly},
		{name: "database", scriptType: "ODBC", canonical: "database", storm: "database", mode: ImportModeExecutable},
		{name: "dns", scriptType: "DNS", canonical: "dns", storm: "network", mode: ImportModeExecutable},
		{name: "ftp", scriptType: "FTP", canonical: "ftp", storm: "network", mode: ImportModeExecutable},
		{name: "smtp", scriptType: "SMTP", canonical: "smtp", storm: "network", mode: ImportModeExecutable},
		{name: "pop3", scriptType: "POP3", canonical: "pop3", storm: "network", mode: ImportModeExecutable},
		{name: "imap", scriptType: "IMAP", canonical: "imap", storm: "network", mode: ImportModeExecutable},
		{name: "ldap", scriptType: "LDAP", canonical: "ldap", storm: "network", mode: ImportModeExecutable},
		{name: "winsock", scriptType: "Windows Sockets", canonical: "winsock", storm: "network", mode: ImportModeExecutable},
		{name: "oracle web", scriptType: "Oracle-Web", canonical: "oracle_web", storm: "http", mode: ImportModeExecutable},
		{name: "siebel web", scriptType: "Siebel-Web", canonical: "siebel_web", storm: "http", mode: ImportModeExecutable},
		{name: "sap web", scriptType: "SAP Web", canonical: "sap_web", storm: "http", mode: ImportModeExecutable},
		{name: "oracle nca", scriptType: "Oracle NCA", canonical: "oracle_nca", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "sap gui", scriptType: "SAP GUI", canonical: "sap_gui", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "sapgui alias", scriptType: "SAPGUI", canonical: "sap_gui", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "rdp", scriptType: "RDP", canonical: "rdp", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "remote desktop alias", scriptType: "Remote Desktop Protocol", canonical: "rdp", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "rte", scriptType: "RTE", canonical: "rte", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "terminal emulation alias", scriptType: "Terminal Emulation", canonical: "rte", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "native mobile", scriptType: "TC4Mobile", canonical: "truclient_native_mobile", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "native mobile 2.0 alias", scriptType: "TruClient 2.0 - Native Mobile", canonical: "truclient_native_mobile", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "template", scriptType: "C Vuser", canonical: "c_vuser", storm: "", mode: ImportModeReviewOnly},
		{name: "java vuser", scriptType: "Java Vuser", canonical: "java_vuser", storm: "", mode: ImportModeReviewOnly},
		{name: "java protocols", scriptType: "Java_protocols", canonical: "java_jms_solace", storm: "jms-solace", mode: ImportModeExecutable},
		{name: "java record replay", scriptType: "Java Record Replay", canonical: "java_over_http", storm: "http", mode: ImportModeExecutable},
		{name: "java over http", scriptType: "Java over HTTP", canonical: "java_over_http", storm: "http", mode: ImportModeExecutable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected := DetectProtocol(nil, &ProjectConfig{ScriptType: tt.scriptType}, nil)
			if detected.CanonicalSourceProtocol != tt.canonical || detected.APEXLoadProtocol != tt.storm || detected.ImportMode != tt.mode {
				t.Fatalf("detected = %#v, want canonical=%s storm=%s mode=%s", detected, tt.canonical, tt.storm, tt.mode)
			}
		})
	}
}

func TestDetectProtocolUsesUSRActiveTypesForMultiScripts(t *testing.T) {
	cfg, err := ParseUSRFile(`[General]
Type=Multi
ActiveTypes=SAP_Web
GenerateTypes=SAP_Web
[Actions]
Action=Action.c
`)
	if err != nil {
		t.Fatalf("ParseUSRFile returned error: %v", err)
	}
	parsed := mustParseActionFile(t, `Action()
{
	web_url("flp", "URL=https://example.test/sap/bc/ui2/flp", LAST);
}`)
	detected := DetectProtocol(nil, cfg, []*ParsedAction{parsed})
	if detected.CanonicalSourceProtocol != "sap_web" || detected.APEXLoadProtocol != "http" || detected.ImportMode != ImportModeExecutable {
		t.Fatalf("detected = %#v, want executable SAP Web over generic Web HTTP/HTML", detected)
	}
	if !containsString(detected.Signals, ".usr [General] ActiveTypes=SAP_Web") {
		t.Fatalf("signals = %#v, want ActiveTypes evidence", detected.Signals)
	}
}

func TestDetectProtocolExternalRunnerContentFallbacks(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		canonical string
		signal    string
	}{
		{name: "rdp", canonical: "rdp", signal: "rdp_* function calls", action: `Action()
{
    rdp_connect_server("Host=server", LAST);
}`},
		{name: "rte", canonical: "rte", signal: "TE_* function calls", action: `Action()
{
    TE_connect("terminal.example.test", LAST);
}`},
		{name: "sap gui", canonical: "sap_gui", signal: "sapgui_* function calls", action: `Action()
{
    sapgui_logon("Login", "ASHOST=server", LAST);
}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := mustParseActionFile(t, tt.action)
			detected := DetectProtocol(nil, nil, []*ParsedAction{parsed})
			if detected.CanonicalSourceProtocol != tt.canonical || detected.ImportMode != ImportModeExternalRunnerRequired {
				t.Fatalf("detected = %#v, want %s external-runner", detected, tt.canonical)
			}
			if !containsString(detected.Signals, tt.signal) {
				t.Fatalf("signals = %#v, want %q", detected.Signals, tt.signal)
			}
		})
	}
}

func TestDetectProtocolContentFallbackAndConflicts(t *testing.T) {
	parsed, err := ParseActionFile("Action.c", `Action()
{
    lrd_stmt("select id from customers where status = ?", LAST);
}`)
	if err != nil {
		t.Fatalf("ParseActionFile returned error: %v", err)
	}
	detected := DetectProtocol(nil, nil, []*ParsedAction{parsed})
	if detected.CanonicalSourceProtocol != "database" || detected.Confidence != DetectionConfidenceMedium {
		t.Fatalf("detected = %#v, want content-only database classification", detected)
	}

	conflict := DetectProtocol(nil, &ProjectConfig{ScriptType: "ODBC"}, []*ParsedAction{mustParseActionFile(t, `Action()
{
    web_url("Home", "URL=https://example.test/", LAST);
}`)})
	if conflict.ImportMode != ImportModeReviewOnly || len(conflict.Conflicts) == 0 {
		t.Fatalf("conflict = %#v, want low-confidence review-only conflict", conflict)
	}
}

func TestDetectProtocolKeepsMixedSOAPAndHTTPExecutableHTTP(t *testing.T) {
	parsed := mustParseActionFile(t, `Action()
{
    web_custom_request("Soap", "URL=https://example.test/soap", "Method=POST", "RecContentType=text/xml", "Body=<soap:Envelope></soap:Envelope>", LAST);
    web_url("Home", "URL=https://example.test/", LAST);
}`)
	detected := DetectProtocol(nil, &ProjectConfig{ScriptType: "Web HTTP/HTML"}, []*ParsedAction{parsed})
	if detected.ImportMode != ImportModeExecutable || detected.APEXLoadProtocol != "http" {
		t.Fatalf("detected = %#v, want executable HTTP for mixed SOAP/HTTP", detected)
	}
	if len(detected.Warnings) == 0 {
		t.Fatalf("warnings = %#v, want mixed SOAP warning", detected.Warnings)
	}
}

func TestConvertZipRoutesDatabaseAndNetworkImports(t *testing.T) {
	dbResult, err := ConvertZip(buildSourceProtocolZip(t, "ODBC", `Action()
{
    lrd_stmt("select id from customers where status = ?", "ParamStatus=active", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("database ConvertZip returned error: %v", err)
	}
	if dbResult.Script == nil || dbResult.Script.Protocol != "database" || len(dbResult.Script.Actions) != 1 {
		t.Fatalf("database script = %#v, want one database action", dbResult.Script)
	}
	if dbResult.Script.Actions[0].Type != "db_query" || dbResult.Script.Config.Database.Driver != "odbc" {
		t.Fatalf("database action/config = %#v/%#v, want db_query odbc", dbResult.Script.Actions[0], dbResult.Script.Config.Database)
	}

	networkResult, err := ConvertZip(buildSourceProtocolZip(t, "DNS", `Action()
{
    dns_query("Lookup", "Domain=example.test", "RecordType=MX", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("network ConvertZip returned error: %v", err)
	}
	if networkResult.Script == nil || networkResult.Script.Protocol != "network" || networkResult.Script.Config.Network.Subprotocol != "dns" {
		t.Fatalf("network script = %#v, want DNS network script", networkResult.Script)
	}
	if networkResult.Script.Actions[0].Type != "dns_query" || networkResult.Script.Actions[0].Target != "example.test" {
		t.Fatalf("network action = %#v, want DNS query for example.test", networkResult.Script.Actions[0])
	}
}

func TestConvertZipPreservesDatabaseAndNetworkLifecyclePhases(t *testing.T) {
	dbResult, err := ConvertZip(buildSourceZip(t, map[string]string{
		"Database.usr": `[General]
Type=ODBC

[Actions]
Action0=vuser_init.c
Action1=Action.c
Action2=vuser_end.c
`,
		"vuser_init.c": `vuser_init()
{
    lrd_stmt("create table setup_users(id int)", LAST);
    return 0;
}`,
		"Action.c": `Action()
{
    lrd_stmt("select id from setup_users", LAST);
    return 0;
}`,
		"vuser_end.c": `vuser_end()
{
    lrd_stmt("drop table setup_users", LAST);
    return 0;
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("database ConvertZip returned error: %v", err)
	}
	if dbResult.Script == nil || dbResult.Script.Protocol != "database" {
		t.Fatalf("database script = %#v, want database script", dbResult.Script)
	}
	if len(dbResult.Script.Init) != 1 || len(dbResult.Script.Actions) != 1 || len(dbResult.Script.Teardown) != 1 {
		t.Fatalf("database lifecycle = init:%d action:%d teardown:%d, want 1/1/1", len(dbResult.Script.Init), len(dbResult.Script.Actions), len(dbResult.Script.Teardown))
	}
	if dbResult.Script.Init[0].Type != "db_exec" || dbResult.Script.Actions[0].Type != "db_query" || dbResult.Script.Teardown[0].Type != "db_exec" {
		t.Fatalf("database lifecycle actions = %#v/%#v/%#v, want exec/query/exec", dbResult.Script.Init[0], dbResult.Script.Actions[0], dbResult.Script.Teardown[0])
	}
	assertSourceReviewMapping(t, dbResult.SourceReview.Mappings, "lrd_stmt", "init[0]", "converted")
	assertSourceReviewMapping(t, dbResult.SourceReview.Mappings, "lrd_stmt", "actions[0]", "converted")
	assertSourceReviewMapping(t, dbResult.SourceReview.Mappings, "lrd_stmt", "teardown[0]", "converted")

	networkResult, err := ConvertZip(buildSourceZip(t, map[string]string{
		"Network.usr": `[General]
Type=DNS

[Actions]
Action0=vuser_init.c
Action1=Action.c
Action2=vuser_end.c
`,
		"vuser_init.c": `vuser_init()
{
    dns_query("Warm DNS", "Domain=init.example.test", "RecordType=A", LAST);
    return 0;
}`,
		"Action.c": `Action()
{
    dns_query("Lookup", "Domain=app.example.test", "RecordType=MX", LAST);
    return 0;
}`,
		"vuser_end.c": `vuser_end()
{
    dns_query("Cleanup DNS", "Domain=end.example.test", "RecordType=TXT", LAST);
    return 0;
}`,
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("network ConvertZip returned error: %v", err)
	}
	if networkResult.Script == nil || networkResult.Script.Protocol != "network" || networkResult.Script.Config.Network.Subprotocol != "dns" {
		t.Fatalf("network script = %#v, want DNS network script", networkResult.Script)
	}
	if len(networkResult.Script.Init) != 1 || len(networkResult.Script.Actions) != 1 || len(networkResult.Script.Teardown) != 1 {
		t.Fatalf("network lifecycle = init:%d action:%d teardown:%d, want 1/1/1", len(networkResult.Script.Init), len(networkResult.Script.Actions), len(networkResult.Script.Teardown))
	}
	if networkResult.Script.Init[0].Target != "init.example.test" || networkResult.Script.Actions[0].Target != "app.example.test" || networkResult.Script.Teardown[0].Target != "end.example.test" {
		t.Fatalf("network targets = %#v/%#v/%#v, want phase-specific DNS targets", networkResult.Script.Init[0], networkResult.Script.Actions[0], networkResult.Script.Teardown[0])
	}
	assertSourceReviewMapping(t, networkResult.SourceReview.Mappings, "dns_query", "init[0]", "converted")
	assertSourceReviewMapping(t, networkResult.SourceReview.Mappings, "dns_query", "actions[0]", "converted")
	assertSourceReviewMapping(t, networkResult.SourceReview.Mappings, "dns_query", "teardown[0]", "converted")
}

func TestConvertZipReturnsReviewArtifactForExternalRunnerProtocol(t *testing.T) {
	result, err := ConvertZip(buildSourceProtocolZip(t, "SAP GUI", `Action()
{
    sapgui_logon("Login", "ASHOST=server", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "sap_gui" {
		t.Fatalf("script = %#v, want SAP GUI draft with external runner gate", result.Script)
	}
	if result.Script.Config.SAPGUI.Password != "{{secret.sapgui_password}}" {
		t.Fatalf("sapgui password = %q, want governed secret reference", result.Script.Config.SAPGUI.Password)
	}
	if result.Script.Config.SAPGUI.ConnectionString != "ASHOST=server" {
		t.Fatalf("connection_string = %q, want source SAP GUI connection string", result.Script.Config.SAPGUI.ConnectionString)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "sap_gui" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("detected protocol = %#v, want SAP GUI external-runner classification", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || len(result.ReviewArtifact.RequiredEvidence) == 0 {
		t.Fatalf("review artifact = %#v, want required evidence", result.ReviewArtifact)
	}
}

func TestConvertZipReturnsExecutableDraftForMappedJavaVuser(t *testing.T) {
	actionsJava := `import lrapi.lr;
import javax.jms.ConnectionFactory;
import javax.jms.TextMessage;
import javax.naming.InitialContext;

public class Actions
{
    public int action() throws Throwable {
        InitialContext ctx = new InitialContext();
        ConnectionFactory factory = (ConnectionFactory) ctx.lookup("/jms/cf/orders");
        lr.start_transaction("publish_order");
        TextMessage message = session.createTextMessage("{\"order\":1}");
        producer.send(message);
        lr.end_transaction("publish_order", lr.AUTO);
        return 0;
    }
}
`
	result, err := ConvertZip(buildSourceZip(t, map[string]string{
		"SolacePublisher.usr": `[General]
Type=Java_protocols
ActiveTypes=Java_protocols
RunType=Java-Interpreter
ScriptLanguage=JAVA
[Actions]
vuser_init=vuser_init.java
Actions=Actions.java
vuser_end=vuser_end.java
`,
		"vuser_init.java": "import lrapi.lr;\n\npublic class vuser_init\n{\n    public int init() throws Throwable {\n        return 0;\n    }\n}\n",
		"Actions.java":    actionsJava,
		"vuser_end.java":  "import lrapi.lr;\n\npublic class vuser_end\n{\n    public int end() throws Throwable {\n        return 0;\n    }\n}\n",
	}), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want executable Java JMS draft", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" || result.DetectedProtocol.ImportMode != ImportModeExecutable || result.DetectedProtocol.APEXLoadProtocol != "jms" {
		t.Fatalf("detected protocol = %#v, want executable Java Vuser JMS mapping", result.DetectedProtocol)
	}
	if result.ReviewArtifact != nil {
		t.Fatalf("review artifact = %#v, want nil for fully mapped Java Vuser", result.ReviewArtifact)
	}
	var sawPublish bool
	for _, action := range result.Script.Actions {
		if action.Type == "jms_publish" && action.Body == `{"order":1}` {
			sawPublish = true
			break
		}
	}
	if !sawPublish {
		t.Fatalf("actions = %#v, want mapped jms_publish action", result.Script.Actions)
	}
}

func TestDetectProtocolJavaVuserFromScriptLanguageAndRunType(t *testing.T) {
	detected := DetectProtocol(nil, &ProjectConfig{
		ScriptType:     "SomeFutureJVMProtocol",
		ScriptLanguage: "JAVA",
		RunType:        "Java-Interpreter",
	}, nil)
	if detected.CanonicalSourceProtocol != "java_vuser" || detected.ImportMode != ImportModeReviewOnly {
		t.Fatalf("detected = %#v, want java_vuser review_only from ScriptLanguage/RunType signals", detected)
	}
	foundSignal := false
	for _, signal := range detected.Signals {
		if strings.Contains(signal, "ScriptLanguage=JAVA") {
			foundSignal = true
			break
		}
	}
	if !foundSignal {
		t.Fatalf("signals = %v, want ScriptLanguage=JAVA signal", detected.Signals)
	}
}

func TestDetectProtocolJavaVuserFromJavaSourcesWithoutMetadata(t *testing.T) {
	detected := DetectProtocol(map[string]string{
		"Actions.java": "import lrapi.lr;\npublic class Actions { public int action() { return 0; } }\n",
	}, nil, nil)
	if detected.CanonicalSourceProtocol != "java_vuser" || detected.ImportMode != ImportModeReviewOnly {
		t.Fatalf("detected = %#v, want java_vuser review_only from .java content evidence", detected)
	}
}

func TestDetectProtocolJavaHTTPFromJavaSourcesWithoutMetadata(t *testing.T) {
	detected := DetectProtocol(map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
    public int action() {
        web.url("Home", "URL=https://example.test/home", new String[] {});
        return 0;
    }
}
`,
	}, nil, nil)
	if detected.CanonicalSourceProtocol != "java_over_http" || detected.APEXLoadProtocol != "http" || detected.ImportMode != ImportModeExecutable {
		t.Fatalf("detected = %#v, want executable java_over_http from Java HTTP idioms", detected)
	}
	if !containsString(detected.Signals, "Java HTTP API usage in Java Vuser source") {
		t.Fatalf("signals = %#v, want Java HTTP API usage signal", detected.Signals)
	}
}

func TestDetectProtocolJavaJDBCFromJavaSourcesWithoutMetadata(t *testing.T) {
	detected := DetectProtocol(map[string]string{
		"Actions.java": `import lrapi.lr;
import java.sql.PreparedStatement;

public class Actions {
    public int action() throws Exception {
        PreparedStatement stmt = conn.prepareStatement("select id from orders");
        stmt.executeQuery();
        return 0;
    }
}
`,
	}, nil, nil)
	if detected.CanonicalSourceProtocol != "java_jdbc" || detected.APEXLoadProtocol != "database" || detected.ImportMode != ImportModeExecutable {
		t.Fatalf("detected = %#v, want executable java_jdbc from Java SQL idioms", detected)
	}
	if !containsString(detected.Signals, "java.sql API usage in Java Vuser source") {
		t.Fatalf("signals = %#v, want java.sql API usage signal", detected.Signals)
	}
}

func mustParseActionFile(t *testing.T, content string) *ParsedAction {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", content)
	if err != nil {
		t.Fatalf("ParseActionFile returned error: %v", err)
	}
	return parsed
}

func buildSourceProtocolZip(t *testing.T, scriptType string, action string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addFile := func(name string, content string) {
		t.Helper()
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addFile("Protocol.usr", "[General]\nType="+scriptType+"\n")
	addFile("Action.c", action)
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	return buf.Bytes()
}
