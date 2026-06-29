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
