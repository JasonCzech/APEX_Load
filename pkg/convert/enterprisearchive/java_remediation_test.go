package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// javaActionTypesAcrossScripts collects every action type emitted across all
// scripts of a (possibly multi-script) conversion result.
func javaActionTypesAcrossScripts(result *ConvertResult) []string {
	var types []string
	for _, script := range result.effectiveScripts() {
		for _, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
			for _, action := range phase {
				types = append(types, action.Type)
			}
		}
	}
	return types
}

func javaFindAction(result *ConvertResult, actionType string) (dsl.Action, bool) {
	for _, script := range result.effectiveScripts() {
		for _, phase := range [][]dsl.Action{script.Init, script.Actions, script.Teardown} {
			for _, action := range phase {
				if action.Type == actionType {
					return action, true
				}
			}
		}
	}
	return dsl.Action{}, false
}

// JAVA-HTTP-1: a bare receiver.send(...) in an archive with NO JMS evidence must
// not be fabricated into a jms_publish to {{env.JMS_DESTINATION}}.
func TestJavaBareSendWithoutJMSEvidenceIsNotPublish(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "BareSend", "Java Vuser", `public class Actions {
  public int action() throws Throwable {
    foo.send(payload);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	if action, ok := javaFindAction(result, "jms_publish"); ok {
		t.Fatalf("bare foo.send() with no JMS evidence was fabricated as jms_publish target=%q; want a review disposition instead", action.Target)
	}
}

// JAVA-HTTP-3: in a mixed HTTP+JMS archive a single lr transaction/think must be
// counted once, not double-emitted across the HTTP and JMS lanes.
func TestJavaMixedHTTPJMSDoesNotDoubleCountTransactions(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "MixedTx", "Java Vuser", `import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    lr.start_transaction("T1");
    lr.think_time(3);
    web.url("Home", "URL=https://example.test/home", web.LAST);
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage("hi");
    producer.send(q, msg);
    lr.end_transaction("T1", lr.AUTO);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	if result.Stats.Transactions != 1 {
		t.Fatalf("Stats.Transactions = %d, want 1 (lr.start_transaction double-emitted across lanes); types=%v", result.Stats.Transactions, javaActionTypesAcrossScripts(result))
	}
	if result.Stats.ThinkTimes != 1 {
		t.Fatalf("Stats.ThinkTimes = %d, want 1 (lr.think_time double-emitted across lanes)", result.Stats.ThinkTimes)
	}
}

// DB-1 + DB-2: a literal (uppercase) prepared SELECT executed via the assignment
// form must convert to an executable db_query, and its setXxx binds captured.
func TestJavaPreparedStatementProducesExecutableQueryWithBinds(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "Prepared", "Java Vuser", `import java.sql.*;
public class Actions {
  public int action() throws Throwable {
    String sql = "SELECT * FROM orders WHERE id = ?";
    PreparedStatement ps = conn.prepareStatement(sql);
    ps.setInt(1, 42);
    ResultSet rs = ps.executeQuery();
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	action, ok := javaFindAction(result, "db_query")
	if !ok {
		t.Fatalf("uppercase prepared SELECT did not produce an executable db_query; types=%v", javaActionTypesAcrossScripts(result))
	}
	if !strings.Contains(strings.ToUpper(action.Target+actionSQLText(action)), "SELECT") {
		t.Fatalf("db_query did not carry the SELECT text: target=%q params=%v", action.Target, action.Params)
	}
	if action.Params == nil || action.Params["binds"] == nil {
		t.Fatalf("db_query did not capture PreparedStatement binds (params=%v)", action.Params)
	}
}

func TestJavaJDBCLRTransactionDoesNotBecomeDatabaseBegin(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JDBCLRTransaction", "Java Vuser", `import java.sql.*;
public class Actions {
  public int action() throws Throwable {
    lr.start_transaction("T1");
    PreparedStatement ps = conn.prepareStatement("insert into audit values (1)");
    ps.executeUpdate();
    lr.end_transaction("T1", lr.AUTO);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	if _, ok := javaFindAction(result, "db_begin"); ok {
		t.Fatalf("lr.start_transaction was emitted as db_begin; types=%v", javaActionTypesAcrossScripts(result))
	}
	if _, ok := javaFindAction(result, "db_exec"); !ok {
		t.Fatalf("expected JDBC DML to remain executable; types=%v", javaActionTypesAcrossScripts(result))
	}
}

func TestJavaDetectedButUnconvertedHTTPLaneStaysReviewVisible(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "IncompleteHTTPAndJMS", "Java Vuser", `import java.net.*;
import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    HttpURLConnection conn = null;
    conn.setRequestMethod("POST");
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage("hi");
    producer.send(q, msg);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	if _, ok := javaFindAction(result, "jms_publish"); !ok {
		t.Fatalf("expected JMS publish to remain executable; types=%v", javaActionTypesAcrossScripts(result))
	}
	if result.ReviewArtifact == nil {
		t.Fatalf("incomplete HTTP statements were hidden by lane ownership; want review artifact, types=%v", javaActionTypesAcrossScripts(result))
	}
	assertSourceReviewSupportMode(t, result.SourceReview.Mappings, "java_statement", SourceFunctionSupportReviewOnly)
}

func actionSQLText(action dsl.Action) string {
	if action.Params == nil {
		return ""
	}
	if s, ok := action.Params["query"].(string); ok {
		return s
	}
	if s, ok := action.Params["sql"].(string); ok {
		return s
	}
	return ""
}

// JMS-3: a jms_publish whose body could not be resolved is downgraded to review,
// not shipped as an executable empty-body publish.
func TestJavaUnresolvableJMSBodyIsReviewed(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JMSBody", "Java Vuser", `import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage(unresolvableBody);
    producer.send(q, msg);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	if action, ok := javaFindAction(result, "jms_publish"); ok {
		t.Fatalf("unresolvable JMS body shipped as executable jms_publish (body=%q); want a review disposition", action.Body)
	}
}

// JMS-4: a single-arg String.replace(..., lr.eval_string("{p}")) templates the
// JMS body (the capability the deleted lane had, now ported via the regex).
func TestJavaJMSBodyReplaceTemplating(t *testing.T) {
	result, err := ConvertZip(buildJavaReplayArchive(t, "JMSReplace", "Java Vuser", `import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    String envelope = "<order>PLACEHOLDER</order>";
    String body = envelope.replace("PLACEHOLDER", lr.eval_string("{orderId}"));
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage(body);
    producer.send(q, msg);
    return 0;
  }
}`), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip error: %v", err)
	}
	action, ok := javaFindAction(result, "jms_publish")
	if !ok {
		t.Fatalf("expected an executable jms_publish; types=%v", javaActionTypesAcrossScripts(result))
	}
	if strings.Contains(action.Body, "PLACEHOLDER") || !strings.Contains(strings.ToLower(action.Body), "orderid") {
		t.Fatalf("jms body not templated via .replace(): %q", action.Body)
	}
}

// Tier 3: the JDBC script's driver is inferred from the connection URL, not
// hardcoded to postgres.
func TestJavaJDBCDriverInferredFromConnectionURL(t *testing.T) {
	cases := map[string]string{
		"jdbc:mysql://db/app":           "mysql",
		"jdbc:oracle:thin:@host:1521:x": "oracle",
		"jdbc:postgresql://db/app":      "postgres",
	}
	for url, wantDriver := range cases {
		t.Run(wantDriver, func(t *testing.T) {
			src := `import java.sql.*;
public class Actions {
  public int action() throws Throwable {
    Connection conn = DriverManager.getConnection("` + url + `");
    String sql = "select 1";
    PreparedStatement ps = conn.prepareStatement(sql);
    ps.executeQuery();
    return 0;
  }
}`
			result, err := ConvertZip(buildJavaReplayArchive(t, "Driver", "Java Vuser", src), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip error: %v", err)
			}
			found := false
			for _, script := range result.effectiveScripts() {
				if script.Protocol == "database" {
					found = true
					if script.Config.Database.Driver != wantDriver {
						t.Fatalf("driver = %q, want %q for %s", script.Config.Database.Driver, wantDriver, url)
					}
				}
			}
			if !found {
				t.Fatalf("no database script produced for %s", url)
			}
		})
	}
}

// Tier 2 #13: count-based conservation over MULTI-surface archives — every leaf
// statement must receive at least one disposition (no silent drop).
func TestJavaMultiSurfaceConservesEveryStatement(t *testing.T) {
	fixtures := map[string]struct {
		scriptType string
		source     string
	}{
		"http+jms+lr": {"Java Vuser", `import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    lr.start_transaction("T1");
    web.url("Home", "URL=https://example.test/home", web.LAST);
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage("hi");
    producer.send(q, msg);
    lr.end_transaction("T1", lr.AUTO);
    return 0;
  }
}`},
		"jms+jdbc+commit": {"Java Vuser", `import javax.jms.*;
import java.sql.*;
public class Actions {
  public int action() throws Throwable {
    PreparedStatement ps = conn.prepareStatement("insert into audit values (1)");
    ps.executeUpdate();
    conn.commit();
    Queue q = session.createQueue("orders.in");
    producer.send(q, session.createTextMessage("hi"));
    session.commit();
    return 0;
  }
}`},
		"dynamic-sql": {"Java Vuser", `import java.sql.*;
public class Actions {
  public int action() throws Throwable {
    String sql = buildQuery(userId);
    Statement st = conn.createStatement();
    ResultSet rs = st.executeQuery(sql);
    return 0;
  }
}`},
		"synchronized-block": {"Java Vuser", `import javax.jms.*;
public class Actions {
  public int action() throws Throwable {
    synchronized (lock) {
      lr.start_transaction("Sync");
      producer.send(session.createQueue("orders.in"), session.createTextMessage("hi"));
      lr.end_transaction("Sync", lr.AUTO);
    }
    return 0;
  }
}`},
	}
	for name, fx := range fixtures {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"Actions.java": fx.source}
			result, err := ConvertZip(buildJavaReplayArchive(t, "Conserve", fx.scriptType, fx.source), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip error: %v", err)
			}
			program, _ := parseJavaProgram(files)
			if result.SourceReview == nil {
				t.Fatalf("no SourceReview produced; cannot prove conservation")
			}
			counts := javaConservationMappingCounts(result.SourceReview.Mappings)
			for _, statement := range javaConservationLeafStatements(program) {
				key := javaConservationStatementKey{file: statement.File, startLine: statement.StartLine, endLine: statement.EndLine}
				if counts[key] < 1 {
					t.Fatalf("statement %s:%d %q has NO disposition (silent drop); mappings=%d", statement.File, statement.StartLine, strings.TrimSpace(statement.Text), len(result.SourceReview.Mappings))
				}
			}
		})
	}
}
