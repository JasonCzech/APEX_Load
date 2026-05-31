package loadrunner

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
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
		{name: "web2", scriptType: "TruClient 2.0 - Web", canonical: "truclient2_web", storm: "browser", mode: ImportModeReviewOnly},
		{name: "database", scriptType: "ODBC", canonical: "database", storm: "database", mode: ImportModeExecutable},
		{name: "dns", scriptType: "DNS", canonical: "dns", storm: "network", mode: ImportModeExecutable},
		{name: "ftp", scriptType: "FTP", canonical: "ftp", storm: "network", mode: ImportModeExecutable},
		{name: "smtp", scriptType: "SMTP", canonical: "smtp", storm: "network", mode: ImportModeExecutable},
		{name: "pop3", scriptType: "POP3", canonical: "pop3", storm: "network", mode: ImportModeExecutable},
		{name: "imap", scriptType: "IMAP", canonical: "imap", storm: "network", mode: ImportModeExecutable},
		{name: "ldap", scriptType: "LDAP", canonical: "ldap", storm: "network", mode: ImportModeExecutable},
		{name: "winsock", scriptType: "Windows Sockets", canonical: "winsock", storm: "network", mode: ImportModeExecutable},
		{name: "jms", scriptType: "JMS", canonical: "jms", storm: "jms-solace", mode: ImportModeExecutable},
		{name: "websocket", scriptType: "WebSocket", canonical: "websocket", storm: "websocket", mode: ImportModeExecutable},
		{name: "grpc", scriptType: "gRPC", canonical: "grpc", storm: "grpc", mode: ImportModeExecutable},
		{name: "oracle web", scriptType: "Oracle-Web", canonical: "oracle_web", storm: "http", mode: ImportModeExecutable},
		{name: "siebel web", scriptType: "Siebel-Web", canonical: "siebel_web", storm: "http", mode: ImportModeExecutable},
		{name: "oracle nca", scriptType: "Oracle NCA", canonical: "oracle_nca", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "sap gui", scriptType: "SAP GUI", canonical: "sap_gui", storm: "", mode: ImportModeExternalRunnerRequired},
		{name: "template", scriptType: "C Vuser", canonical: "c_vuser", storm: "", mode: ImportModeReviewOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detected := DetectProtocol(nil, &ProjectConfig{ScriptType: tt.scriptType}, nil)
			if detected.CanonicalLoadRunnerProtocol != tt.canonical || detected.APEXLoadProtocol != tt.storm || detected.ImportMode != tt.mode {
				t.Fatalf("detected = %#v, want canonical=%s storm=%s mode=%s", detected, tt.canonical, tt.storm, tt.mode)
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
	if detected.CanonicalLoadRunnerProtocol != "database" || detected.Confidence != DetectionConfidenceMedium {
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
	dbResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "ODBC", `Action()
{
    lrd_stmt("select id from customers where status = ?", "ParamStatus=active", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("database ConvertZip returned error: %v", err)
	}
	if dbResult.Script == nil || dbResult.Script.Protocol != "database" {
		t.Fatalf("database script = %#v, want database script", dbResult.Script)
	}
	dbAction := firstActionOfType(dbResult.Script.Actions, "db_query")
	if dbAction.Type != "db_query" || dbResult.Script.Config.Database.Driver != "odbc" {
		t.Fatalf("database action/config = %#v/%#v, want db_query odbc", dbAction, dbResult.Script.Config.Database)
	}

	networkResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "DNS", `Action()
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

func TestConvertZipRoutesDatabaseLifecycleAndNetworkFamilies(t *testing.T) {
	dbResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "ODBC", `Action()
{
    lrd_open_connection("DSN=Orders", LAST);
    lrd_stmt("select id from customers where status = ?", "ParamStatus=active", LAST);
    lrd_exec(LAST);
    lrd_commit(LAST);
    lrd_close_connection(LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("database ConvertZip returned error: %v", err)
	}
	for _, actionType := range []string{"db_connect", "db_query", "db_commit", "db_disconnect"} {
		if action := firstActionOfType(dbResult.Script.Actions, actionType); action.Type == "" {
			t.Fatalf("database actions = %#v, missing %s", dbResult.Script.Actions, actionType)
		}
	}
	if len(dbResult.Stats.UnsupportedFuncs) != 0 || dbResult.Stats.Skipped != 0 {
		t.Fatalf("database stats = %#v, want no unsupported functions", dbResult.Stats)
	}

	ftpResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "FTP", `Action()
{
    ftp_logon("Host=ftp.example.test", "User=demo", "Password=secret", LAST);
    ftp_get("orders.csv", LAST);
    ftp_logout(LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ftp ConvertZip returned error: %v", err)
	}
	if ftpResult.Script.Protocol != "network" || ftpResult.Script.Config.Network.Subprotocol != "ftp" {
		t.Fatalf("ftp script = %#v, want network/ftp", ftpResult.Script)
	}
	if firstActionOfType(ftpResult.Script.Actions, "ftp_login").Type == "" || firstActionOfType(ftpResult.Script.Actions, "ftp_command").Type == "" {
		t.Fatalf("ftp actions = %#v, want login and command", ftpResult.Script.Actions)
	}

	smtpResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "SMTP", `Action()
{
    smtp_send_mail("Host=smtp.example.test", "From=ops@example.test", "To=qa@example.test", "Body=hello", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("smtp ConvertZip returned error: %v", err)
	}
	if action := firstActionOfType(smtpResult.Script.Actions, "smtp_send"); action.Type != "smtp_send" || action.Body != "hello" {
		t.Fatalf("smtp action = %#v, want smtp_send body", action)
	}

	ldapResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "LDAP", `Action()
{
    ldap_bind("Host=ldap.example.test", "User=cn=admin", "Password=secret", LAST);
    ldap_search("BaseDN=dc=example,dc=test", "Filter=(uid=alice)", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ldap ConvertZip returned error: %v", err)
	}
	if firstActionOfType(ldapResult.Script.Actions, "ldap_bind").Type == "" || firstActionOfType(ldapResult.Script.Actions, "ldap_search").Type == "" {
		t.Fatalf("ldap actions = %#v, want bind and search", ldapResult.Script.Actions)
	}
}

func TestConvertZipRoutesMessagingImports(t *testing.T) {
	jmsResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "JMS", `Action()
{
    jms_send_message("Destination=Orders", "Body={\"id\":\"A-100\"}", LAST);
    jms_receive_message("Destination=Orders", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("jms ConvertZip returned error: %v", err)
	}
	if jmsResult.Script.Protocol != "jms-solace" || !jmsResult.Script.Config.JMS.Mock {
		t.Fatalf("jms script = %#v, want jms-solace mock script", jmsResult.Script)
	}
	if firstActionOfType(jmsResult.Script.Actions, "jms_publish").Type == "" || firstActionOfType(jmsResult.Script.Actions, "jms_consume").Type == "" {
		t.Fatalf("jms actions = %#v, want publish and consume", jmsResult.Script.Actions)
	}

	wsResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "WebSocket", `Action()
{
    websocket_connect("URL=ws://example.test/socket", LAST);
    websocket_send("Message=hello", LAST);
    websocket_receive("Timeout=30", LAST);
    websocket_close(LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("websocket ConvertZip returned error: %v", err)
	}
	for _, actionType := range []string{"ws_connect", "ws_send", "ws_receive", "ws_close"} {
		if action := firstActionOfType(wsResult.Script.Actions, actionType); action.Type == "" {
			t.Fatalf("websocket actions = %#v, missing %s", wsResult.Script.Actions, actionType)
		}
	}

	grpcResult, err := ConvertZip(buildLoadRunnerProtocolZip(t, "gRPC", `Action()
{
    grpc_invoke("Method=acme.OrderService/Create", "Body={\"id\":\"A-100\"}", LAST);
    grpc_server_stream("Method=acme.OrderService/Watch", "Body={}", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("grpc ConvertZip returned error: %v", err)
	}
	if grpcResult.Script.Protocol != "grpc" || !grpcResult.Script.Config.GRPC.UseReflection {
		t.Fatalf("grpc script = %#v, want grpc script with reflection", grpcResult.Script)
	}
	if firstActionOfType(grpcResult.Script.Actions, "grpc_unary").Type == "" || firstActionOfType(grpcResult.Script.Actions, "grpc_server_stream").Type == "" {
		t.Fatalf("grpc actions = %#v, want unary and server stream", grpcResult.Script.Actions)
	}
}

func TestConvertZipReturnsReviewArtifactForExternalRunnerProtocol(t *testing.T) {
	result, err := ConvertZip(buildLoadRunnerProtocolZip(t, "SAP GUI", `Action()
{
    sapgui_logon("Login", "ASHOST=server", LAST);
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script != nil {
		t.Fatalf("script = %#v, want nil for SAP GUI external runner", result.Script)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalLoadRunnerProtocol != "sap_gui" || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired {
		t.Fatalf("detected protocol = %#v, want SAP GUI external-runner classification", result.DetectedProtocol)
	}
	if result.ReviewArtifact == nil || len(result.ReviewArtifact.RequiredEvidence) == 0 {
		t.Fatalf("review artifact = %#v, want required evidence", result.ReviewArtifact)
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

func firstActionOfType(actions []dsl.Action, actionType string) dsl.Action {
	for _, action := range actions {
		if action.Type == actionType {
			return action
		}
	}
	return dsl.Action{}
}

func buildLoadRunnerProtocolZip(t *testing.T, scriptType string, action string) []byte {
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
