package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	_ "github.com/jasonczech/apexload/pkg/protocol/database"
	_ "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/jms"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
)

func TestJavaHTTPImportReplaySmokeAndFidelityContracts(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/app/home" && r.Header.Get("X-Trace") != "abc-123" {
			t.Fatalf("X-Trace header = %q, want abc-123", r.Header.Get("X-Trace"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/app/home":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"token":"T-123"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/orders":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"id":"ORD-9"`) {
				t.Fatalf("POST body = %q, want ORD-9", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	archive := buildJavaReplayArchive(t, "JavaHTTP", "Java over HTTP", `import lrapi.lr;

public class Actions {
  public int action() throws Throwable {
    web.addHeader("X-Trace", "abc-123");
    web.regSaveParam("session", "LB=\"token\":\"", "RB=\"", "NotFound=Warning", web.LAST);
    web.url("Home", "URL=`+server.URL+`/app/home", web.LAST);
    web.customRequest("Create", "URL=`+server.URL+`/api/orders", "Method=POST", "Body={\"id\":\"{orderId}\"}", web.LAST);
    return 0;
  }
}`)
	result, err := convertZipJavaNativeAnalysisForTest(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "http" {
		t.Fatalf("script = %#v, want http", result.Script)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %#v, want two HTTP requests", result.Script.Actions)
	}
	first := result.Script.Actions[0]
	if len(first.HeaderList) == 0 || first.HeaderList[0].Name != "X-Trace" {
		t.Fatalf("header_list = %#v, want ordered X-Trace header", first.HeaderList)
	}
	if len(first.Extractors) != 1 || first.Extractors[0].OnMiss != "empty" {
		t.Fatalf("extractors = %#v, want NotFound=Warning -> on_miss empty", first.Extractors)
	}
	result.Script.Parameters = append(result.Script.Parameters, dsl.Parameter{Name: "orderid", Type: "list", Values: []string{"ORD-9"}})
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
	wantSeen := []string{"GET /app/home", "POST /api/orders"}
	if strings.Join(seen, "|") != strings.Join(wantSeen, "|") {
		t.Fatalf("requests = %#v, want %#v", seen, wantSeen)
	}
}

func TestJavaJMSImportReplaySmokeWithMockBroker(t *testing.T) {
	archive := buildJavaReplayArchive(t, "JavaJMS", "Java Vuser", `import javax.jms.*;

public class Actions {
  public int action() throws Throwable {
    Queue q = session.createQueue("orders.replay");
    TextMessage msg = session.createTextMessage("{\"orderId\":\"A-100\"}");
    producer.send(q, msg);
    return 0;
  }
}`)
	result, err := convertZipJavaNativeAnalysisForTest(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want jms-solace", result.Script)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
}

func TestJavaJDBCImportReplaySmokeWithSQLite(t *testing.T) {
	archive := buildJavaReplayArchive(t, "JavaDB", "Java Vuser", `import java.sql.*;

public class Actions {
  public int action() throws Throwable {
    PreparedStatement ps = conn.prepareStatement("select 1");
    ps.executeQuery();
    return 0;
  }
}`)
	result, err := convertZipJavaNativeAnalysisForTest(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "database" {
		t.Fatalf("script = %#v, want database", result.Script)
	}
	result.Script.Config.Database.Driver = "sqlite"
	result.Script.Config.Database.DSN = ":memory:"
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
}

func TestJavaSOAPImportReplaySmokeWithLocalEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodPost || r.URL.Path != "/services/order" {
			t.Fatalf("unexpected SOAP request %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(string(body), "<order>ORD-7</order>") {
			t.Fatalf("SOAP body = %q, want ORD-7", body)
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<soapenv:Envelope><soapenv:Body><ok>true</ok></soapenv:Body></soapenv:Envelope>`)
	}))
	defer server.Close()

	archive := buildJavaReplayArchive(t, "JavaSOAP", "Java Vuser", `import javax.xml.soap.*;

public class Actions {
  public int action() throws Throwable {
    String endpoint = "`+server.URL+`/services/order";
    String soapEnvelope = "<soapenv:Envelope><soapenv:Body><order>{orderId}</order></soapenv:Body></soapenv:Envelope>";
    SOAPMessage message = null;
    SOAPConnection connection = SOAPConnectionFactory.newInstance().createConnection();
    connection.call(message, endpoint);
    return 0;
  }
}`)
	result, err := convertZipJavaNativeAnalysisForTest(archive, ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.Script == nil || result.Script.Protocol != "soap" {
		t.Fatalf("script = %#v, want soap", result.Script)
	}
	result.Script.Parameters = append(result.Script.Parameters, dsl.Parameter{Name: "orderid", Type: "list", Values: []string{"ORD-7"}})
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
	report, err := engine.ReplayScript(context.Background(), result.Script, engine.ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript returned error: %v", err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s events=%#v", report.Error, report.Events)
	}
}

func buildJavaReplayArchive(t *testing.T, name string, scriptType string, javaSource string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, name+"/"+name+".usr", `[General]
Type=`+scriptType+`
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, name+"/Actions.java", javaSource)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
