package enterprisearchive

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestJavaFirstClassFixturesAvoidGenericFallbackDispositions(t *testing.T) {
	tests := []struct {
		name       string
		scriptType string
		source     string
	}{
		{
			name:       "http",
			scriptType: "Java over HTTP",
			source: `public class Actions {
  public int action() throws Throwable {
    web.url("Home", "URL=https://example.test/home", web.LAST);
    web.customRequest("Create", "URL=https://example.test/api/orders", "Method=POST", "Body={\"id\":\"{orderId}\"}", web.LAST);
    return 0;
  }
}`,
		},
		{
			name:       "jms",
			scriptType: "Java Vuser",
			source: `import javax.jms.*;

public class Actions {
  public int action() throws Throwable {
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage("{\"orderId\":\"A-100\"}");
    producer.send(q, msg);
    return 0;
  }
}`,
		},
		{
			name:       "jdbc",
			scriptType: "Java Vuser",
			source: `import java.sql.*;

public class Actions {
  public int action() throws Throwable {
    PreparedStatement ps = conn.prepareStatement("select 1");
    ps.executeQuery();
    return 0;
  }
}`,
		},
		{
			name:       "soap",
			scriptType: "Java Vuser",
			source: `import javax.xml.soap.*;

public class Actions {
  public int action() throws Throwable {
    String endpoint = "https://example.test/services/order";
    String soapEnvelope = "<soapenv:Envelope><soapenv:Body><order>{orderId}</order></soapenv:Body></soapenv:Envelope>";
    SOAPMessage message = null;
    SOAPConnection connection = SOAPConnectionFactory.newInstance().createConnection();
    connection.call(message, endpoint);
    return 0;
  }
}`,
		},
		{
			name:       "named-review",
			scriptType: "Java Vuser",
			source: `public class Actions {
  public int action() throws Throwable {
    javax.ejb.EJBHome home = null;
    org.omg.CORBA.ORB orb = org.omg.CORBA.ORB.init(new String[0], null);
    Object remote = javax.naming.Naming.lookup("rmi://example.test/service");
    return 0;
  }
}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Guardrail runs against the production import entry path, not a
			// test-only replica of the retired multi-lane pipeline.
			result, err := ConvertZip(buildJavaReplayArchive(t, "JavaConservation"+tc.name, tc.scriptType, tc.source), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip returned error: %v", err)
			}
			assertNoGenericJavaFallbackDispositions(t, result)
		})
	}
}

// TestJavaCombinedImportConservesEveryLeafStatementDisposition is the
// production no-silent-drop guardrail: every leaf statement of every parsed
// class — including helper methods reached only through non-simple calls
// (OrderClient.submit(...)) and statements in classes other than the selected
// runner class — plus every unparsed region must receive exactly one
// SourceReview disposition through the real ConvertZip entry path.
func TestJavaCombinedImportConservesEveryLeafStatementDisposition(t *testing.T) {
	files := map[string]string{
		"Actions.java": `import lrapi.lr;
public class Actions {
  public int init() {
    prepare();
    return 0;
  }
  public int action() {
    lr.start_transaction("Order");
    localStep();
    OrderClient.submit("A-1");
    lr.end_transaction("Order", lr.AUTO);
    return 0;
  }
  public int end() {
    cleanup();
    return 0;
  }
  private void localStep() {
    audit("local");
  }
}`,
		"OrderClient.java": `public class OrderClient {
  public static int submit(String id) {
    validate(id);
    return post(id);
  }
  private static void validate(String id) {
    check(id);
  }
}`,
		"Broken.java": `public class Broken {
  public int flaky() {
    dangling();
`,
	}
	result, err := ConvertZip(buildJavaVuserFilesArchive(t, "JavaConserveAll", files), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.SourceReview == nil {
		t.Fatal("source review is nil, want one disposition per Java leaf statement")
	}
	program, _ := parseJavaProgram(files)
	if len(program.Classes) != 3 || len(program.Unparsed) != 1 {
		t.Fatalf("fixture parsed to %d classes / %d unparsed regions, want 3 classes and 1 unparsed region", len(program.Classes), len(program.Unparsed))
	}
	counts := javaConservationMappingCounts(result.SourceReview.Mappings)
	assertExactlyOneDisposition := func(statement javaStatement) {
		t.Helper()
		if javaNormalizeStatementText(statement.Text) == "" {
			return
		}
		key := javaConservationStatementKey{file: statement.File, startLine: statement.StartLine, endLine: statement.EndLine}
		if counts[key] != 1 {
			t.Fatalf("statement %s:%d-%d %q mapped %d time(s), want exactly one disposition; mappings=%#v", statement.File, statement.StartLine, statement.EndLine, statement.Text, counts[key], result.SourceReview.Mappings)
		}
	}
	for _, class := range program.Classes {
		for _, method := range class.Methods {
			for _, statement := range method.Statements {
				for _, leaf := range javaConservationLeafStatementList(statement) {
					assertExactlyOneDisposition(leaf)
				}
			}
		}
	}
	for _, statement := range program.Unparsed {
		assertExactlyOneDisposition(statement)
	}
	stats := result.Stats
	if got := stats.NativeFunctions + stats.ExternalRequiredFunctions + stats.ReviewOnlyFunctions + stats.CustomCallFunctions; got != stats.TotalFunctions {
		t.Fatalf("support counters = %d, want reconciliation with TotalFunctions=%d (stats=%#v)", got, stats.TotalFunctions, stats)
	}
}

func assertNoGenericJavaFallbackDispositions(t *testing.T, result *ConvertResult) {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	for _, script := range result.effectiveScripts() {
		for _, action := range append(append([]dslActionForJavaConservation{}, javaConservationActions(script.Init)...), append(javaConservationActions(script.Actions), javaConservationActions(script.Teardown)...)...) {
			if action.Type != "source_review" || action.Reason == "" {
				continue
			}
			switch action.Reason {
			case "unmapped_java_statement", "unmapped_java_block":
				t.Fatalf("script %q action %q used generic Java fallback reason %q", script.Name, action.Name, action.Reason)
			}
		}
	}
}

type dslActionForJavaConservation struct {
	Name   string
	Type   string
	Reason string
}

func javaConservationActions(actions []dsl.Action) []dslActionForJavaConservation {
	out := make([]dslActionForJavaConservation, 0, len(actions))
	for _, action := range actions {
		item := dslActionForJavaConservation{Name: action.Name, Type: action.Type}
		if action.Metadata != nil {
			if reason, ok := action.Metadata["review_reason"].(string); ok {
				item.Reason = reason
			}
		}
		out = append(out, item)
	}
	return out
}

type javaConservationStatementKey struct {
	file      string
	startLine int
	endLine   int
}

func javaConservationLeafStatements(program javaProgram) []javaStatement {
	out := make([]javaStatement, 0)
	for _, phase := range []string{"init", "action", "teardown"} {
		for _, statement := range javaLifecycleStatements(program, phase) {
			out = append(out, javaConservationLeafStatementList(statement)...)
		}
	}
	return out
}

func javaConservationLeafStatementList(statement javaStatement) []javaStatement {
	if statement.Kind != "block" {
		return []javaStatement{statement}
	}
	out := make([]javaStatement, 0)
	for _, child := range statement.Children {
		out = append(out, javaConservationLeafStatementList(child)...)
	}
	return out
}

func javaConservationMappingCounts(mappings []SourceReviewMapping) map[javaConservationStatementKey]int {
	counts := map[javaConservationStatementKey]int{}
	for _, mapping := range mappings {
		if mapping.SourceFile == "" || mapping.StartLine == 0 {
			continue
		}
		key := javaConservationStatementKey{file: mapping.SourceFile, startLine: mapping.StartLine, endLine: mapping.EndLine}
		counts[key]++
	}
	return counts
}
