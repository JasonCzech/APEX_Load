package enterprisearchive

import (
	"archive/zip"
	"bytes"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func addJavaZipFile(t *testing.T, zw *zip.Writer, name string, content string) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestConvertZipJavaVuserSidecarFallbackRequiresExplicitOptIn(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaCustom/JavaCustom.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
[Actions]
Action=Actions.java
`)
	addJavaZipFile(t, zw, "JavaCustom/Actions.java", `public class Actions {
  public int action() throws Throwable {
    CustomClient client = new CustomClient();
    client.invokeBusinessOperation("opaque");
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	reviewOnly, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip without opt-in returned error: %v", err)
	}
	if reviewOnly.Script == nil || reviewOnly.Script.Protocol == "java_vuser" {
		t.Fatalf("review-only default script = %#v, want non-sidecar draft", reviewOnly.Script)
	}
	if reviewOnly.DetectedProtocol == nil || reviewOnly.DetectedProtocol.ImportMode != ImportModeReviewOnly {
		t.Fatalf("default detected = %#v, want review_only", reviewOnly.DetectedProtocol)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{AllowJavaVuserRunner: true})
	if err != nil {
		t.Fatalf("ConvertZip with opt-in returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExternalRunnerRequired || result.DetectedProtocol.RunnerRequirement != javaVuserRunnerRequirement {
		t.Fatalf("detected = %#v, want external runner java_vuser", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "java_vuser" {
		t.Fatalf("script = %#v, want java_vuser sidecar draft", result.Script)
	}
	if result.Script.Config.JavaVuser.Enabled {
		t.Fatal("java_vuser fallback should not enable runner execution by default")
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "java_vuser_run" || result.Script.Actions[0].Target != "Actions" {
		t.Fatalf("actions = %#v, want java_vuser_run Actions", result.Script.Actions)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
	if result.ReviewArtifact == nil || result.ReviewArtifact.Protocol.RunnerRequirement != javaVuserRunnerRequirement {
		t.Fatalf("review artifact = %#v, want runner requirement", result.ReviewArtifact)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 || result.SourceReview.Mappings[0].SupportMode != string(SourceFunctionSupportExternalRequired) {
		t.Fatalf("source review = %#v, want external_required mapping", result.SourceReview)
	}
}

func TestConvertZipJavaVuserSolaceMapsJMSIdioms(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "JavaSolace/JavaSolace.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
[ManuallyExtraFiles]
FILE1=request.xml
`)
	addJavaZipFile(t, zw, "JavaSolace/request.xml", `<order date="xxxdatexxx">123</order>`)
	addJavaZipFile(t, zw, "JavaSolace/Actions.java", `import lrapi.lr;
import javax.jms.*;
import javax.naming.Context;

public class Actions {
  public int init() throws Throwable {
    env.put(Context.PROVIDER_URL, "smf://broker.example.com:55555");
    env.put("Solace_JMS_VPN", "payments");
    env.put(Context.SECURITY_PRINCIPAL, "loaduser@payments");
    env.put(Context.SECURITY_CREDENTIALS, "super-secret");
    return 0;
  }

  public int action() throws Throwable {
    lr.start_transaction("PublishOrder");
    String body = readFile("request.xml");
    body = body.replaceAll("xxxdatexxx", lr.eval_string("{date}"));
    Queue q = session.createQueue("orders.in");
    TextMessage msg = session.createTextMessage(body);
    msg.setStringProperty("source", "lr");
    producer.send(q, msg);
    Message response = consumer.receive(5000);
    session.commit();
    lr.end_transaction("PublishOrder", lr.AUTO);
    return 0;
  }

  public int end() {
    return 0;
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.CanonicalSourceProtocol != "java_vuser" || result.DetectedProtocol.ImportMode != ImportModeExecutable {
		t.Fatalf("detected protocol = %#v, want executable java_vuser", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want jms-solace draft", result.Script)
	}
	if result.Script.Config.JMS.BrokerURL != "smf://broker.example.com:55555" || result.Script.Config.JMS.MessageVPN != "payments" {
		t.Fatalf("jms config = %#v, want broker and vpn mapped", result.Script.Config.JMS)
	}
	if result.Script.Config.JMS.Password != "{{secret.jms_password}}" || result.Script.Config.JMS.Username != "{{secret.jms_username}}" {
		t.Fatalf("jms credentials = %#v, want secret templates", result.Script.Config.JMS)
	}
	var publish *string
	var sawConsume bool
	var sawCommit bool
	for i := range result.Script.Actions {
		action := result.Script.Actions[i]
		switch action.Type {
		case "jms_publish":
			if action.Target != "orders.in" || action.Headers["source"] != "lr" {
				t.Fatalf("publish action = %#v, want queue and source property", action)
			}
			publish = &action.Body
		case "jms_consume":
			sawConsume = true
		case "jms_commit":
			sawCommit = true
		}
	}
	if publish == nil || *publish != `<order date="{{var.date}}">123</order>` {
		t.Fatalf("publish body = %v, want parameterized XML body", publish)
	}
	if !sawConsume || !sawCommit {
		t.Fatalf("actions = %#v, want consume and commit mapped", result.Script.Actions)
	}
	if result.SourceReview == nil || len(result.SourceReview.Mappings) == 0 {
		t.Fatal("expected Java source review mappings")
	}
}

func TestConvertZipJavaVuserSolaceMapsNestedTryHelperIdioms(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	addJavaZipFile(t, zw, "RealShape/RealShape.usr", `[General]
Type=Java Vuser
ScriptLanguage=JAVA
RunType=Java-Interpreter
[Actions]
Action=Actions.java
[ManuallyExtraFiles]
FILE1=GDDN15_Request.xml
`)
	addJavaZipFile(t, zw, "RealShape/GDDN15_Request.xml", `<request date="xxxdatexxx" time="xxxtimexxx"/>`)
	addJavaZipFile(t, zw, "RealShape/gddnsolace.perf.truststore", "placeholder")
	addJavaZipFile(t, zw, "RealShape/Actions.java", `import lrapi.lr;
import javax.jms.*;
import javax.naming.Context;
import javax.naming.InitialContext;
import com.solacesystems.jms.SupportedProperty;

public class Actions {
  Context initialContext;
  Destination destJMS;
  Session session;
  MessageProducer senderJMS;
  String originalXML = null;
  String inputXML = null;

  public int init() throws Throwable {
    // create context
    try {
      createInitialContext();
      connectToServer();
      originalXML = convertXMLFileToString("GDDN15_Request.xml");
    } catch (Exception e) {
      lr.error_message(e.getMessage());
      return -1;
    }
    return 0;
  }

  public int action() throws Throwable {
    lr.start_transaction("PublishRealShape");
    // Main try-catch block
    try {
      lr.save_string(lr.eval_string("MSG_{date}T{time}"), "messageId");
      inputXML = lr.eval_string(originalXML);
      inputXML = inputXML.replaceAll("xxxdatexxx", lr.eval_string("{date}"));
      inputXML = inputXML.replaceAll("xxxtimexxx", lr.eval_string("{time}"));
      // Post request
      try {
        postJMSRequestToQueue();
      } catch (Exception e) {
        lr.error_message(e.getMessage());
        return 1;
      }
    } catch (Exception e) {
      lr.error_message(e.getMessage());
      return 1;
    }
    lr.end_transaction("PublishRealShape", lr.AUTO);
    return 0;
  }

  public int end() {
    return 0;
  }

  public void createInitialContext() throws Exception {
    try {
      env.put(InitialContext.PROVIDER_URL, lr.eval_string("{serverURL}"));
      env.put(Context.SECURITY_PRINCIPAL, lr.eval_string("{userNameJNDI}"));
      env.put(SupportedProperty.SOLACE_JMS_VPN, lr.eval_string("{vpnName}"));
      env.put("Solace_JMS_SSL_TrustStore", "gddnsolace.perf.truststore");
      initialContext = new InitialContext(env);
    } catch (Exception e) {
      throw new Exception("context failed");
    }
  }

  public void connectToServer() throws Exception {
    factory = (ConnectionFactory) initialContext.lookup(lr.eval_string("{connectionFactoryJNDI}"));
    session = connection.createSession(false, Session.AUTO_ACKNOWLEDGE);
    destJMS = (Destination) initialContext.lookup(lr.eval_string("{requestQueueName}"));
  }

  public void postJMSRequestToQueue() throws Exception {
    TextMessage message;
    try {
      senderJMS = session.createProducer(destJMS);
      message = session.createTextMessage(inputXML);
      message.setBooleanProperty(SupportedProperty.SOLACE_JMS_PROP_ISXML, true);
      message.setStringProperty("subscriberNumber", lr.eval_string("{ctn}"));
      message.setStringProperty("seqNumber", lr.eval_string("{messageId}"));
      senderJMS.send(message);
    } catch (Exception e) {
      throw new Exception("post failed");
    } finally {
      senderJMS.close();
    }
  }

  public String convertXMLFileToString(String fileName) throws Exception {
    return "";
  }
}`)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	result, err := ConvertZip(buf.Bytes(), ConvertOptions{})
	if err != nil {
		t.Fatalf("ConvertZip returned error: %v", err)
	}
	if result.DetectedProtocol == nil || result.DetectedProtocol.ImportMode != ImportModeExecutable {
		t.Fatalf("detected = %#v, want executable Java JMS import", result.DetectedProtocol)
	}
	if result.Script == nil || result.Script.Protocol != "jms-solace" {
		t.Fatalf("script = %#v, want jms-solace draft", result.Script)
	}
	if result.Script.Config.JMS.BrokerURL != "{{var.serverurl}}" || result.Script.Config.JMS.MessageVPN != "{{var.vpnname}}" {
		t.Fatalf("jms config = %#v, want templated broker/vpn", result.Script.Config.JMS)
	}
	if result.Script.Config.JMS.Username != "{{secret.jms_username}}" || result.Script.Config.JMS.TLS == nil || result.Script.Config.JMS.TLS.TrustStoreRef != "gddnsolace.perf.truststore" {
		t.Fatalf("jms config = %#v, want secret username and truststore", result.Script.Config.JMS)
	}

	var publish *dsl.Action
	for i := range result.Script.Actions {
		if result.Script.Actions[i].Type == "jms_publish" {
			publish = &result.Script.Actions[i]
			break
		}
	}
	if publish == nil {
		t.Fatalf("actions = %#v, want jms_publish", result.Script.Actions)
	}
	if publish.Target != "{{var.requestqueuename}}" {
		t.Fatalf("publish target = %q, want requestQueueName template", publish.Target)
	}
	if publish.Body != `<request date="{{var.date}}" time="{{var.time}}"/>` {
		t.Fatalf("publish body = %q, want templated XML", publish.Body)
	}
	if publish.Headers["subscriberNumber"] != "{{var.ctn}}" || publish.Headers["seqNumber"] != "{{var.messageid}}" {
		t.Fatalf("publish headers = %#v, want templated message properties", publish.Headers)
	}
	if issues := dsl.ValidateScriptIssues(result.Script); len(issues) > 0 {
		t.Fatalf("ValidateScriptIssues returned %v", issues)
	}
}
