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
			result, err := ConvertZip(buildJavaReplayArchive(t, "JavaConservation"+tc.name, tc.scriptType, tc.source), ConvertOptions{})
			if err != nil {
				t.Fatalf("ConvertZip returned error: %v", err)
			}
			assertNoGenericJavaFallbackDispositions(t, result)
		})
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
